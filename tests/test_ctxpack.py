import json
from pathlib import Path

from ctxpack.cli import main
from ctxpack.core import (
    load_history,
    pack,
    record_run,
    reset_history,
    result_to_markdown,
    summarize_history,
)


FIXTURE = Path(__file__).with_name("fixture.html")


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
