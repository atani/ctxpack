# Launch Kit

CtxPack is aimed at developers building AI agents and coding assistants who care about context-window efficiency.

## One-liner

CtxPack is a token-aware context extractor for AI agents: it converts noisy pages into compact structured context and reports exactly how many tokens were saved.

## Positioning

- Not another web-to-markdown wrapper.
- Agent-first output: stable JSON + compact Markdown.
- Token savings are first-class metrics.
- Local-first and independent of hosted reader APIs.

## Demo command

```bash
ctxpack https://example.com/docs --json
ctxpack stats
```

## Social copy

### X / Bluesky

Built CtxPack: a token-aware context extractor for AI agents.

It turns noisy web pages into compact Markdown/JSON, tracks cumulative token savings, and shows how much context agents avoided reading.

For agents, less page chrome = more useful context.

https://github.com/atani/ctxpack

### Show HN

Title: Show HN: CtxPack – token-aware context extraction for AI agents

Post:

AI agents often read raw HTML or overly verbose Markdown when they only need task-relevant context. I built CtxPack as a small local CLI that cleans page chrome, preserves useful structure, and keeps cumulative stats on how many tokens were saved across runs.

It supports Markdown or JSON output so agents can call it as a tool.

### Reddit / Hacker News angle

The core idea is that web-to-markdown is not enough for agents. Agents need token-aware context packing: clean, measurable, and optimized by default.

## Manual GitHub settings

Suggested repository metadata:

- Description: Token-aware context extractor for AI agents
- Topics: ai-agents, llm, context, tokens, markdown, cli, python
- Website: empty until docs site exists
