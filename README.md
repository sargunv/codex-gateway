# codex-gateway

Expose your existing ChatGPT (Codex) subscription as a local OpenAI **Responses
API** endpoint, so any client that speaks the Responses API can use `gpt-5.5`
and friends without a paid API account.

It borrows the OAuth token the official `codex` CLI stores in
`~/.codex/auth.json`, refreshes it as needed, and proxies requests to
`https://chatgpt.com/backend-api/codex` with the right headers injected. SSE
streams pass through unchanged.
[OpenAI has said](https://twitter.com/romainhuet/status/2038699202834841962)
this is fine to use.

## Requirements

- An active **ChatGPT Plus/Pro/Team** subscription.
- The [`codex` CLI](https://github.com/openai/codex) installed and logged in
  (`codex login`) so `~/.codex/auth.json` exists.

## Run with Docker / Podman

```sh
docker run -d --name codex-gateway --restart unless-stopped \
  -p 127.0.0.1:8080:8080 \
  -v "$HOME/.codex/auth.json:/auth.json" \
  ghcr.io/sargunv/codex-gateway:main
```

The image runs as root and defaults to `/auth.json`, so rootless engines map the
process to your uid (the owner of the `0600` auth file) with no extra flags.
`podman` works the same; replace `docker` with `podman`.

To expose on all interfaces, pass `--addr 0.0.0.0:8080` and map the port without
the `127.0.0.1:` prefix.

## Run a binary

Pre-built binaries are on the [Releases](../../releases) page.

```sh
codex-gateway serve
# -> http://localhost:8080/v1/responses

# point any Responses-API client at it:
export OPENAI_BASE_URL=http://localhost:8080/v1
```

### Flags

```
codex-gateway serve [flags]

Flags:
  -a, --addr string       listen address (default "127.0.0.1:8080")
      --auth-file string  path to the Codex CLI auth.json
                         (default $CODEX_HOME/auth.json or ~/.codex/auth.json)
```

## Limitations

- Responses API only — no Chat Completions translation.
- HTTP+SSE only — no WebSocket transport.
- If the refresh token expires, run `codex login` again.
