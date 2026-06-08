# CtxPack

**Token-aware context extraction for AI agents.**

CtxPack turns noisy web pages, HTML, or Markdown into compact context that agents can safely consume. It removes obvious page chrome, preserves useful structure, and tracks cumulative token savings over time.

```bash
ctxpack https://example.com/article
ctxpack https://example.com/article --query "pricing and limits" --json
ctxpack stats
ctxpack reset --yes
```

## Why

AI agents often waste context on navigation, scripts, styles, cookie banners, footers, related links, and repeated boilerplate. Existing web-to-markdown tools are useful, but agents need one more step: **token-aware packing with measurable savings**.

CtxPack is designed as a tool an agent can call before reading a URL.

```text
URL / HTML / Markdown
  -> clean page chrome
  -> extract readable text
  -> move task-relevant sections toward the top
  -> return compact Markdown or structured JSON
  -> record how many tokens were saved
```

## Example

```bash
ctxpack https://example.com/docs --stats
```

Markdown output includes source metadata and optional per-run savings:

```text
Raw input:   42,100 tokens
Clean text:   7,800 tokens
Final:        7,800 tokens
Saved:       34,300 tokens
Reduction:   81.5%
```

For agents, use JSON:

```bash
ctxpack https://example.com/docs --json
```

```json
{
  "ok": true,
  "source": { "url": "https://example.com/docs", "fetched_at": "..." },
  "title": "Example Docs",
  "content": { "format": "markdown", "text": "..." },
  "stats": {
    "raw_html_tokens": 42100,
    "clean_text_tokens": 7800,
    "final_tokens": 7800,
    "saved_tokens": 34300,
    "reduction_percent": 81.5
  }
}
```

## Cumulative savings

Every normal run is recorded locally in `~/.ctxpack/stats.jsonl`.

```bash
ctxpack stats
```

```text
Runs:        24
Raw input:   1,024,000 tokens
Clean text:    181,200 tokens
Final:         181,200 tokens
Saved:         842,800 tokens
Reduction:     82.3%
```

Reset the history:

```bash
ctxpack reset --yes
```

Skip recording one run:

```bash
ctxpack https://example.com --no-record
```

## Install

### Homebrew

```bash
brew install atani/tap/ctxpack
```

### From source

```bash
git clone https://github.com/atani/ctxpack.git
cd ctxpack
uv tool install .
```

Or run during development:

```bash
uv run ctxpack https://example.com --stats
```

## CLI

```bash
ctxpack SOURCE [--query TEXT] [--json] [--stats] [--no-record] [-o FILE]
ctxpack run SOURCE [--query TEXT] [--json] [--stats] [--no-record] [-o FILE]
ctxpack stats [--json] [--reset]
ctxpack reset --yes
```

`SOURCE` can be:

- `https://...` or `http://...`
- a local HTML/Markdown file
- `-` for stdin

Options:

- `--query TEXT` — move task-relevant sections toward the top.
- `--json` — structured output for agent tool calls.
- `--stats` — include the per-run token savings table in Markdown output. JSON output always includes `stats`, so this flag only affects Markdown.
- `--no-record` — do not add this run to cumulative savings.
- `-o FILE` — write output to a file.

Exit codes:

- `0` — success.
- `1` — runtime error such as a network failure or an oversized response (often retriable).
- `2` — usage error or missing input file (not retriable).

## How it works

### Content filtering

CtxPack removes, before extracting text:

- `<script>`, `<style>`, `<noscript>`, `<svg>`, `<canvas>`, `<iframe>` (dropped entirely).
- `<nav>`, `<footer>`, `<aside>` elements.
- Elements whose `class`, `id`, or `role` contains one of these whitespace/punctuation-delimited keywords (case-insensitive): `ad`, `ads`, `advert`, `banner`, `breadcrumb`, `cookie`, `footer`, `header`, `menu`, `modal`, `nav`, `newsletter`, `promo`, `recommend`, `related`, `share`, `sidebar`, `social`, `subscribe`, `tracking`.

Matching is keyword-based, so a class like `site-nav-primary` is treated as noise (`nav`), while `navigation` is kept. If useful content is dropped, save the page to a local file and inspect what `ctxpack file.html` preserves.

### Token estimation

Token counts are a fast, model-agnostic approximation, not exact per-model counts:

- CJK characters (common Hiragana/Katakana and CJK Unified Ideographs blocks) count as ~0.8 tokens each.
- All other characters count as ~1 token per 4 characters.

This is designed for relative before/after comparison. Absolute numbers will differ from any specific tokenizer, and rare ideographs (e.g. CJK Extension B and above) and emoji fall back to the non-CJK estimate.

### Query matching

`--query` reorders sections by a simple relevance score while keeping all content:

- Words of 2+ characters are matched case-insensitively (no stemming, lemmatization, or synonyms).
- Each body match scores +2; a term appearing in a section's leading heading scores an extra +3.
- Sections are sorted by descending score; ties keep their original document order.

### Network behavior

- **Timeout:** URL fetches time out after 20 seconds.
- **Encoding:** the response's declared charset is used, defaulting to UTF-8. Invalid bytes are replaced with U+FFFD rather than failing.
- **Size limit:** responses over 50 MB (and local files over 100 MB) are rejected to avoid memory exhaustion.
- **User-Agent:** `ctxpack/0.1 (+https://github.com/atani/ctxpack)`.

## Limitations

CtxPack 0.1 targets static HTML/Markdown content. Out of scope for now:

- JavaScript-rendered pages (single-page apps, lazy-loaded content).
- Heavy bot protection / CAPTCHA pages.
- Login-only content.
- PDF, DOCX, and other binary formats (pre-convert to HTML/Markdown).

For HTML vs. Markdown on stdin, the format is auto-detected by the presence of angle brackets; pass a `.md`/`.html` file path when you need deterministic handling.

## Design principles

- **Agent-first:** stable JSON and concise Markdown, not a human browsing experience.
- **Token-aware:** every result includes raw, cleaned, final, saved, and reduction metrics.
- **Cumulative:** `ctxpack stats` shows how much context was saved across runs.
- **Local-first:** no dependency on third-party reader APIs.
- **Zero knobs by default:** no token budget or compression level required.
- **Transparent:** stats make the value measurable.

## Current status

Early MVP. The current implementation intentionally uses Python standard library only. It works best for article/docs-like pages and local HTML. See [Limitations](#limitations) for what is out of scope in this version.

## Roadmap

- Better readability extraction
- Optional Playwright rendering for JavaScript-heavy pages
- Model-specific tokenizers
- Section-level relevance scores in JSON
- Cache support
- MCP/server mode for agent frameworks
- Benchmarks against raw HTML and common web-to-markdown tools

## License

MIT
