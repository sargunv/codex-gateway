# codex-gateway

Expose your existing OpenAI Codex (ChatGPT) subscription as a local OpenAI
**Responses API** endpoint, so any tool that speaks the Responses API can use
`gpt-5.5` and friends without a paid API account.

It borrows the OAuth token the official `codex` CLI stores in
`~/.codex/auth.json`, refreshes it as needed, and proxies requests to
`https://chatgpt.com/backend-api/codex` with `Authorization` and
`ChatGPT-Account-ID` headers injected. Responses-API SSE streams pass through
unchanged.

## Why

OpenAI's Codex/ChatGPT subscriptions are cheaper than the API, and
[OpenAI has said](https://twitter.com/romainhuet/status/2038699202834841962)
this backdoor is fine to use. This repo turns that into a localhost HTTP server.

## Requirements

- An active **ChatGPT Plus/Pro/Team** subscription.
- The [`codex` CLI](https://github.com/openai/codex) installed and logged in
  (`codex login`) so `~/.codex/auth.json` exists.

## Install

Pre-built binaries and container images are published on the
[GitHub Releases](../../releases) page. Pull the image:

```sh
docker pull ghcr.io/sargunv/codex-gateway:latest
```

Or build from source:

```sh
git clone https://github.com/sargunv/codex-gateway
cd codex-gateway
go build .
```

## Use

```sh
codex-gateway serve
# -> http://localhost:8080/v1/responses

# point any Responses-API client at it:
export OPENAI_BASE_URL=http://localhost:8080/v1
```

### Options

```
codex-gateway serve [flags]

Flags:
  -a, --addr string       listen address (default "127.0.0.1:8080")
      --auth-file string  path to the Codex CLI auth.json
                         (default $CODEX_HOME/auth.json or ~/.codex/auth.json)
```

Run `codex-gateway --help` and `codex-gateway serve --help` for the full
listing.

### Docker

```sh
docker run --rm -p 127.0.0.1:8080:8080 \
  -v "$HOME/.codex/auth.json:/home/nonroot/.codex/auth.json" \
  ghcr.io/sargunv/codex-gateway:latest serve
```

The `--addr` default is `127.0.0.1:8080` (localhost-only). To expose on all
interfaces, pass `--addr 0.0.0.0:8080` and map the port without the `127.0.0.1:`
prefix.

## What it does not do

- No Chat Completions translation — Responses API only.
- No WebSocket transport — HTTP+SSE only. `gpt-5.5` works fine over HTTP; the
  `prefer_websockets` hint in the model manifest is a Codex-CLI-specific latency
  optimization.
- No re-login flow — if the refresh token expires, run `codex login` again.
