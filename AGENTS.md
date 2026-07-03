# codex-gateway

Local reverse proxy that exposes an OpenAI Responses API endpoint backed by a
ChatGPT/Codex subscription, borrowing OAuth tokens from the Codex CLI.

## Dev tool commands

```
mise install              boot the toolchain
mise run fix              hk fix --all (lint, tidy, format)
mise run release:snapshot build a release snapshot with GoReleaser (no push)
mise run build            build the binary
mise run serve            run the gateway locally
```

Point a Responses-API client at it:

```
export OPENAI_BASE_URL=http://localhost:8080/v1
```

## Project invariants

- Re-use the Codex CLI's `~/.codex/auth.json` verbatim; do not store credentials
  anywhere else.
- Widely-used Go libraries are fine; avoid niche deps.
