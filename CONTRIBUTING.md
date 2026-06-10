# Contributing to CtxPack

Thanks for helping improve CtxPack.

## Development

```bash
go test ./...
go run ./cmd/ctxpack tests/fixture.html --stats
```

## Good first areas

- Better page-noise detection fixtures
- Tokenizer adapters for specific models
- JSON schema improvements for agent frameworks
- Playwright-based rendering experiments

## Pull request checklist

- Add or update tests for behavior changes
- Keep JSON output backward-compatible when possible
- Include before/after token savings for extraction changes
