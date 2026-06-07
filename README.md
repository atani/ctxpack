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

## Install from source

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
- `--stats` — include per-run token savings in Markdown output.
- `--no-record` — do not add this run to cumulative savings.
- `-o FILE` — write output to a file.

## Design principles

- **Agent-first:** stable JSON and concise Markdown, not a human browsing experience.
- **Token-aware:** every result includes raw, cleaned, final, saved, and reduction metrics.
- **Cumulative:** `ctxpack stats` shows how much context was saved across runs.
- **Local-first:** no dependency on third-party reader APIs.
- **Zero knobs by default:** no token budget or compression level required.
- **Transparent:** stats make the value measurable.

## Current status

Early MVP. The current implementation intentionally uses Python standard library only. It works best for article/docs-like pages and local HTML. JavaScript-rendered pages, heavy bot protection, and login-only content are out of scope for the initial version.

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
