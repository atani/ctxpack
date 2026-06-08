from __future__ import annotations

import io
import json
from pathlib import Path
from urllib.error import URLError

import pytest

import ctxpack.core as core
from ctxpack.cli import main
from ctxpack.core import (
    apply_query,
    estimate_tokens,
    html_to_markdown,
    load_history,
    pack,
    record_run,
    reset_history,
    result_to_markdown,
    summarize_history,
)


FIXTURE = Path(__file__).with_name("fixture.html")


class _FakeResponse:
    def __init__(self, body: bytes, headers: dict[str, str] | None = None):
        self._body = body
        self._headers = headers or {}

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    @property
    def headers(self):
        outer = self

        class _Headers:
            def get(self, key, default=None):
                return outer._headers.get(key, default)

            def get_content_charset(self):
                return outer._headers.get("charset")

        return _Headers()

    def read(self, amt: int | None = None) -> bytes:
        if amt is None:
            return self._body
        return self._body[:amt]


def test_pack_removes_obvious_noise_and_reports_savings():
    result = pack(str(FIXTURE))
    assert result.title == "Agent Token Budget Guide"
    assert "console.log" not in result.content
    assert "Home Pricing Blog" not in result.content
    assert "AI agents should avoid reading raw HTML" in result.content
    assert result.stats.raw_html_tokens > result.stats.final_tokens
    assert result.stats.saved_tokens > 0
    assert result.stats.reduction_percent > 0


def test_query_moves_relevant_section_first():
    result = pack(str(FIXTURE), query="pricing")
    assert result.content.startswith("## Pricing")


def test_markdown_stats_include_token_savings():
    result = pack(str(FIXTURE))
    output = result_to_markdown(result, include_stats=True)
    assert "saved_tokens:" in output
    assert "reduction_percent:" in output


def test_cli_json_output(capsys, monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    code = main([str(FIXTURE), "--json"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["ok"] is True
    assert payload["content"]["format"] == "markdown"
    assert payload["stats"]["saved_tokens"] > 0


def test_cumulative_stats_and_reset(capsys, monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    assert main([str(FIXTURE), "--no-record"]) == 0
    assert summarize_history(load_history())["runs"] == 0

    assert main([str(FIXTURE)]) == 0
    assert main([str(FIXTURE), "--query", "pricing"]) == 0
    rows = load_history()
    summary = summarize_history(rows)
    assert summary["runs"] == 2
    assert summary["saved_tokens"] > 0

    assert main(["stats"]) == 0
    out = capsys.readouterr().out
    assert "Runs:" in out
    assert "Saved:" in out

    assert main(["reset", "--yes"]) == 0
    assert summarize_history(load_history())["runs"] == 0


def test_record_and_reset_helpers(tmp_path):
    stats_path = tmp_path / "stats.jsonl"
    result = pack(str(FIXTURE))
    record_run(result, stats_path)
    summary = summarize_history(load_history(stats_path))
    assert summary["runs"] == 1
    assert reset_history(stats_path) == 1
    assert load_history(stats_path) == []


def _pack_stdin(text: str, monkeypatch):
    monkeypatch.setattr("sys.stdin", io.StringIO(text))
    return pack("-")


def test_cli_version_flag(capsys):
    import pytest as _pytest

    from ctxpack import __version__

    with _pytest.raises(SystemExit) as exc:
        main(["--version"])
    assert exc.value.code == 0
    assert __version__ in capsys.readouterr().out


def test_estimate_tokens_handles_empty_and_cjk():
    assert estimate_tokens("") == 0
    # Pure CJK text uses the ~0.8-per-character factor.
    assert estimate_tokens("日本語テスト") == round(6 * 0.8)


def test_reduction_percent_is_zero_when_nothing_saved(monkeypatch):
    # A plain-text source with no markup cannot save tokens.
    plain = _pack_stdin("just plain words with no markup at all", monkeypatch)
    assert plain.stats.saved_tokens == 0
    assert plain.stats.reduction_percent == 0.0
    assert pack(str(FIXTURE)).stats.reduction_percent > 0


def test_void_elements_inside_dropped_nav_do_not_swallow_content():
    # Regression: a void tag (<input>/<img>) inside <nav> previously left the
    # parser stuck in skip mode, dropping all content after the nav.
    html = (
        "<body><nav><input type='text'><img src='x'>Menu Home</nav>"
        "<article><p>REAL ARTICLE CONTENT</p></article></body>"
    )
    _title, markdown = html_to_markdown(html)
    assert "REAL ARTICLE CONTENT" in markdown
    assert "Menu Home" not in markdown


def test_yaml_front_matter_is_safe_for_tricky_titles(monkeypatch):
    # title is whitespace-normalised and json-quoted, so "---" / newlines in a
    # title cannot break the YAML front matter.
    html = "<html><head><title>--- not\na real\tdelimiter</title></head><body><p>Body.</p></body></html>"
    result = _pack_stdin(html, monkeypatch)
    md = result_to_markdown(result)
    # Front matter delimiters are exactly two (open/close), not split by title.
    assert md.count("\n---\n") <= 1
    assert md.startswith("---\n")
    assert "Body." in md


def test_query_tie_breaks_keep_document_order():
    markdown = "## Alpha\nshared\n\n## Beta\nshared\n\n## Gamma\nother"
    ordered = apply_query(markdown, "shared")
    # Alpha and Beta tie; Alpha keeps its earlier position.
    assert ordered.index("## Alpha") < ordered.index("## Beta")


def test_cli_run_subcommand_form(capsys, monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    assert main(["run", str(FIXTURE), "--json"]) == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload["ok"] is True


def test_cli_output_writes_file(monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    out = tmp_path / "out.md"
    assert main([str(FIXTURE), "--no-record", "-o", str(out)]) == 0
    assert "Agent Token Budget Guide" in out.read_text(encoding="utf-8")


def test_cli_missing_file_returns_2(capsys, monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))
    code = main([str(tmp_path / "does-not-exist.html"), "--no-record"])
    assert code == 2
    assert "file not found" in capsys.readouterr().err


def test_cli_network_error_is_retriable(capsys, monkeypatch, tmp_path):
    monkeypatch.setenv("HOME", str(tmp_path))

    def boom(*_a, **_k):
        raise URLError("connection refused")

    monkeypatch.setattr(core, "urlopen", boom)
    code = main(["https://example.com/page", "--no-record"])
    assert code == 1
    assert "network error (retriable)" in capsys.readouterr().err


def test_fetch_url_rejects_oversized_response(monkeypatch):
    monkeypatch.setattr(core, "MAX_FETCH_SIZE", 10)
    big = _FakeResponse(b"x" * 100, {"charset": "utf-8"})
    monkeypatch.setattr(core, "urlopen", lambda *a, **k: big)
    with pytest.raises(ValueError):
        core.fetch_url("https://example.com/big")


def test_fetch_url_rejects_declared_oversize(monkeypatch):
    monkeypatch.setattr(core, "MAX_FETCH_SIZE", 10)
    resp = _FakeResponse(b"x" * 5, {"Content-Length": "999", "charset": "utf-8"})
    monkeypatch.setattr(core, "urlopen", lambda *a, **k: resp)
    with pytest.raises(ValueError):
        core.fetch_url("https://example.com/declared")


def test_read_source_rejects_oversized_file(monkeypatch, tmp_path):
    monkeypatch.setattr(core, "MAX_FILE_SIZE", 8)
    big = tmp_path / "big.html"
    big.write_text("x" * 100, encoding="utf-8")
    with pytest.raises(ValueError):
        core.read_source(str(big))
