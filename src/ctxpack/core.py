from __future__ import annotations

from dataclasses import asdict, dataclass
from html.parser import HTMLParser
from pathlib import Path
from urllib.request import Request, urlopen
import json
import re
import sys
from datetime import datetime, timezone


BLOCK_TAGS = {
    "address", "article", "aside", "blockquote", "br", "dd", "details", "div", "dl",
    "dt", "figcaption", "figure", "footer", "form", "h1", "h2", "h3", "h4", "h5",
    "h6", "header", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "table",
    "tbody", "td", "tfoot", "th", "thead", "tr", "ul",
}
DROP_TAGS = {"script", "style", "noscript", "svg", "canvas", "iframe"}
DROP_CLASS_ID_HINTS = (
    "ad", "ads", "advert", "banner", "breadcrumb", "cookie", "footer", "header",
    "menu", "modal", "nav", "newsletter", "promo", "recommend", "related", "share",
    "sidebar", "social", "subscribe", "tracking",
)


@dataclass
class TokenStats:
    raw_html_tokens: int
    clean_text_tokens: int
    final_tokens: int

    @property
    def saved_tokens(self) -> int:
        return max(0, self.raw_html_tokens - self.final_tokens)

    @property
    def reduction_percent(self) -> float:
        if self.raw_html_tokens <= 0:
            return 0.0
        return round((self.saved_tokens / self.raw_html_tokens) * 100, 1)

    def to_dict(self) -> dict:
        data = asdict(self)
        data["saved_tokens"] = self.saved_tokens
        data["reduction_percent"] = self.reduction_percent
        return data


@dataclass
class PackResult:
    source_url: str
    title: str | None
    fetched_at: str
    content: str
    stats: TokenStats
    query: str | None = None

    def to_dict(self) -> dict:
        return {
            "ok": True,
            "source": {"url": self.source_url, "fetched_at": self.fetched_at},
            "title": self.title,
            "query": self.query,
            "content": {"format": "markdown", "text": self.content},
            "stats": self.stats.to_dict(),
        }


class ReadabilityHTMLParser(HTMLParser):
    def __init__(self) -> None:
        super().__init__(convert_charrefs=True)
        self.parts: list[str] = []
        self.title_parts: list[str] = []
        self.stack: list[str] = []
        self.skip_depth = 0
        self.in_title = False

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        tag = tag.lower()
        attrs_dict = {k.lower(): (v or "") for k, v in attrs}
        self.stack.append(tag)
        if tag == "title":
            self.in_title = True
        if self.skip_depth or tag in DROP_TAGS or self._looks_noisy(tag, attrs_dict):
            self.skip_depth += 1
            return
        if tag in {"h1", "h2", "h3", "h4", "h5", "h6"}:
            level = int(tag[1])
            self.parts.append("\n" + "#" * level + " ")
        elif tag == "li":
            self.parts.append("\n- ")
        elif tag in BLOCK_TAGS:
            self.parts.append("\n")

    def handle_endtag(self, tag: str) -> None:
        tag = tag.lower()
        if tag == "title":
            self.in_title = False
        if self.skip_depth:
            self.skip_depth -= 1
        elif tag in BLOCK_TAGS:
            self.parts.append("\n")
        if self.stack:
            self.stack.pop()

    def handle_data(self, data: str) -> None:
        text = data.strip()
        if not text:
            return
        if self.in_title:
            self.title_parts.append(text)
        if self.skip_depth:
            return
        self.parts.append(text + " ")

    def _looks_noisy(self, tag: str, attrs: dict[str, str]) -> bool:
        if tag in {"nav", "footer", "aside"}:
            return True
        blob = " ".join([attrs.get("class", ""), attrs.get("id", ""), attrs.get("role", "")]).lower()
        tokens = re.split(r"[^a-z0-9]+", blob)
        return any(token in DROP_CLASS_ID_HINTS for token in tokens)

    @property
    def markdown(self) -> str:
        return normalize_text("".join(self.parts))

    @property
    def title(self) -> str | None:
        title = normalize_inline(" ".join(self.title_parts))
        return title or None


def estimate_tokens(text: str) -> int:
    """Fast model-agnostic token estimate suitable for before/after comparison."""
    if not text:
        return 0
    cjk = len(re.findall(r"[\u3040-\u30ff\u3400-\u9fff]", text))
    non_cjk = len(text) - cjk
    return max(1, round(non_cjk / 4 + cjk * 0.8))


def fetch_url(url: str, timeout: int = 20) -> str:
    req = Request(url, headers={"User-Agent": "ctxpack/0.1 (+https://github.com/atani/ctxpack)"})
    with urlopen(req, timeout=timeout) as res:
        raw = res.read()
        encoding = res.headers.get_content_charset() or "utf-8"
    return raw.decode(encoding, errors="replace")


def read_source(source: str) -> tuple[str, str]:
    if source == "-":
        return "stdin", sys.stdin.read()
    if re.match(r"https?://", source):
        return source, fetch_url(source)
    path = Path(source)
    return str(path), path.read_text(encoding="utf-8", errors="replace")


def html_to_markdown(html: str) -> tuple[str | None, str]:
    parser = ReadabilityHTMLParser()
    parser.feed(html)
    return parser.title, parser.markdown


def normalize_inline(text: str) -> str:
    return re.sub(r"\s+", " ", text).strip()


def normalize_text(text: str) -> str:
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    text = re.sub(r"[ \t]+", " ", text)
    text = re.sub(r" *\n *", "\n", text)
    text = re.sub(r"\n{3,}", "\n\n", text)
    text = re.sub(r"(?m)^# +$", "", text)
    return text.strip()


def split_sections(markdown: str) -> list[str]:
    chunks = re.split(r"(?=^#{1,6} .*$)", markdown, flags=re.MULTILINE)
    sections = [c.strip() for c in chunks if c.strip()]
    if not sections:
        return [p.strip() for p in markdown.split("\n\n") if p.strip()]
    return sections


def score_section(section: str, query: str | None) -> float:
    if not query:
        return 0.0
    terms = [t.lower() for t in re.findall(r"\w+", query) if len(t) >= 2]
    haystack = section.lower()
    score = 0.0
    for term in terms:
        score += haystack.count(term) * 2
    if section.startswith("#"):
        first_line = section.splitlines()[0].lower()
        score += sum(3 for term in terms if term in first_line)
    return score


def apply_query(markdown: str, query: str | None) -> str:
    """Move likely relevant sections first while keeping the full compact context."""
    if not query:
        return markdown
    sections = split_sections(markdown)
    if len(sections) <= 1:
        return markdown
    scored = [(score_section(section, query), idx, section) for idx, section in enumerate(sections)]
    relevant = [(score, idx, section) for score, idx, section in scored if score > 0]
    if not relevant:
        return markdown
    relevant.sort(key=lambda item: item[0], reverse=True)
    selected = {idx for _, idx, _ in relevant}
    ordered = [section for _, _, section in relevant]
    ordered.extend(section for idx, section in enumerate(sections) if idx not in selected)
    return "\n\n".join(ordered)


def pack(source: str, *, query: str | None = None) -> PackResult:
    source_url, raw = read_source(source)
    title, clean = html_to_markdown(raw) if "<" in raw and ">" in raw else (None, normalize_text(raw))
    final = apply_query(clean, query)
    stats = TokenStats(
        raw_html_tokens=estimate_tokens(raw),
        clean_text_tokens=estimate_tokens(clean),
        final_tokens=estimate_tokens(final),
    )
    return PackResult(
        source_url=source_url,
        title=title,
        fetched_at=datetime.now(timezone.utc).isoformat(),
        content=final,
        stats=stats,
        query=query,
    )


def result_to_markdown(result: PackResult, include_stats: bool = False) -> str:
    lines: list[str] = ["---"]
    if result.title:
        lines.append(f"title: {json.dumps(result.title, ensure_ascii=False)}")
    lines.extend([
        f"source_url: {json.dumps(result.source_url, ensure_ascii=False)}",
        f"fetched_at: {json.dumps(result.fetched_at)}",
    ])
    if result.query:
        lines.append(f"query: {json.dumps(result.query, ensure_ascii=False)}")
    if include_stats:
        for key, value in result.stats.to_dict().items():
            lines.append(f"{key}: {value}")
    lines.extend(["---", "", result.content])
    return "\n".join(lines).strip() + "\n"


def stats_table(result: PackResult) -> str:
    stats = result.stats.to_dict()
    return "\n".join([
        f"Raw input:   {stats['raw_html_tokens']:,} tokens",
        f"Clean text:  {stats['clean_text_tokens']:,} tokens",
        f"Final:       {stats['final_tokens']:,} tokens",
        f"Saved:       {stats['saved_tokens']:,} tokens",
        f"Reduction:   {stats['reduction_percent']}%",
    ]) + "\n"


def default_stats_path() -> Path:
    return Path.home() / ".ctxpack" / "stats.jsonl"


def record_run(result: PackResult, stats_path: Path | None = None) -> None:
    path = stats_path or default_stats_path()
    path.parent.mkdir(parents=True, exist_ok=True)
    row = {
        "source_url": result.source_url,
        "title": result.title,
        "fetched_at": result.fetched_at,
        "query": result.query,
        "stats": result.stats.to_dict(),
    }
    with path.open("a", encoding="utf-8") as f:
        f.write(json.dumps(row, ensure_ascii=False) + "\n")


def load_history(stats_path: Path | None = None) -> list[dict]:
    path = stats_path or default_stats_path()
    if not path.exists():
        return []
    rows = []
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        if not line.strip():
            continue
        try:
            rows.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    return rows


def summarize_history(rows: list[dict]) -> dict:
    total_raw = 0
    total_clean = 0
    total_final = 0
    for row in rows:
        stats = row.get("stats", {})
        total_raw += int(stats.get("raw_html_tokens", 0))
        total_clean += int(stats.get("clean_text_tokens", 0))
        total_final += int(stats.get("final_tokens", 0))
    saved = max(0, total_raw - total_final)
    reduction = round((saved / total_raw) * 100, 1) if total_raw else 0.0
    return {
        "runs": len(rows),
        "raw_input_tokens": total_raw,
        "clean_text_tokens": total_clean,
        "final_tokens": total_final,
        "saved_tokens": saved,
        "reduction_percent": reduction,
    }


def reset_history(stats_path: Path | None = None) -> int:
    path = stats_path or default_stats_path()
    rows = load_history(path)
    if path.exists():
        path.unlink()
    return len(rows)


def history_table(summary: dict) -> str:
    return "\n".join([
        f"Runs:        {summary['runs']:,}",
        f"Raw input:   {summary['raw_input_tokens']:,} tokens",
        f"Clean text:  {summary['clean_text_tokens']:,} tokens",
        f"Final:       {summary['final_tokens']:,} tokens",
        f"Saved:       {summary['saved_tokens']:,} tokens",
        f"Reduction:   {summary['reduction_percent']}%",
    ]) + "\n"
