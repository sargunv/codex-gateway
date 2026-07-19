# agent-api-gateway

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

- Re-use provider native CLI credential files verbatim; preserve unknown fields
  and rotate tokens only with atomic directory writes. Do not store credentials
  anywhere else.
- Never log request/response bodies, headers, tokens, account identifiers, or
  credential paths.
- Public model IDs are exact `provider/model` values and routing is
  model-specific.
- Widely-used Go libraries are fine; avoid niche deps.
