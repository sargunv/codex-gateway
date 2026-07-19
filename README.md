# agent-api-gateway

A local, single-key gateway for coding subscriptions and explicitly configured
OpenAI/Anthropic-compatible endpoints. It exposes:

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`
- `POST /v1/messages/count_tokens` (only when the selected model has a native,
  accurate Anthropic endpoint)
- unauthenticated `GET /healthz` and `GET /readyz`

Every public model is addressed as an exact `provider/model`. The gateway first
selects a model route matching the caller's wire family. When there is no native
route it converts only the researched text/image/JSON-function-tool
intersection; unsupported populated fields receive a typed 422 error and are
never dropped. Native JSON and SSE responses are passed through without schema
reserialization. Cross-family streaming is rejected before dispatch with a typed
422 until a complete, stateful event translator is available.

## Configuration

`GATEWAY_API_KEY` is required. Clients may send it as a raw or Bearer
`Authorization` value, or as `x-api-key`. This downstream key is never
forwarded.

Built-ins activate implicitly:

| Subscription     | Signal                                                               | Native routes                          |
| ---------------- | -------------------------------------------------------------------- | -------------------------------------- |
| ChatGPT Codex    | `CODEX_AUTH_FILE`, or `~/.codex/auth.json` if present                | Responses                              |
| Kimi Coding Plan | `KIMI_AUTH_FILE`, or `~/.kimi/credentials/kimi-code.json` if present | Messages                               |
| Z.AI Coding Plan | `ZAI_API_KEY`                                                        | Chat                                   |
| NeuralWatt       | `NEURALWATT_API_KEY`                                                 | Chat                                   |
| OpenCode Go      | `OPENCODE_GO_API_KEY`                                                | model-specific Chat/Responses/Messages |

If an explicit credential/config path is set, invalid or missing content is a
startup error. Native Codex and Kimi credential **directories** must be mounted
read-write: refresh uses a mode-0600 temporary file, fsync, atomic rename, and
directory fsync while preserving unknown JSON fields.

Optional `GATEWAY_CONFIG` points to strict TOML. Secrets are
environment-variable references, not TOML values; see
[`examples/providers.toml`](examples/providers.toml). `GATEWAY_ADDR` defaults to
`127.0.0.1:8080`.

### Model discovery and metadata

At startup, the gateway fetches one model catalog per built-in provider. ChatGPT
Codex and NeuralWatt use their authenticated catalogs. Kimi Coding Plan, Z.AI
Coding Plan, and OpenCode Go use the public models.dev catalog. Public catalog
requests never receive provider credentials, and the shared models.dev document
is fetched once per startup. Built-in providers contain no compiled model IDs or
model metadata: if their catalog is unavailable, they advertise no models and
emit `catalog_unavailable`.

Provider model-selection fields are retained in `GET /v1/models` (prompt and
instruction payloads are excluded), alongside normalized `context_window`,
`max_output_tokens`, `supports_tools`, `supports_reasoning`, and
`input_modalities` fields when the upstream supplies equivalent data.
`endpoint_families` and `preferred_endpoint_family` are generated from the
validated route graph rather than trusted from provider metadata. The public
`id` remains the exact `provider/model` route.

Catalog handling is direct:

- complete, safely classified models are exposed directly;
- incomplete models are exposed only when their endpoint family is known and
  produce a `metadata_incomplete` warning naming the absent fields;
- models whose catalog does not identify a safe endpoint family are withheld
  with `route_unclassified`;
- catalog values replace conflicting explicit custom-provider values and emit
  `metadata_conflict` or `route_conflict`.

For models.dev catalogs, exact known SDK packages map OpenAI-compatible, OpenAI,
and Anthropic to Chat Completions, Responses, and Messages respectively. Unknown
package values are withheld. Custom endpoints can opt in with `models_url` and
supplement fields absent from that custom catalog with `context_window`,
`max_output_tokens`, `supports_tools`, `supports_reasoning`, and
`input_modalities` in TOML.

```sh
export GATEWAY_API_KEY='local-only-key'
agent-api-gateway serve
export OPENAI_BASE_URL=http://127.0.0.1:8080/v1
export OPENAI_API_KEY="$GATEWAY_API_KEY"
```

Container example (Codex):

```sh
podman run --rm -p 127.0.0.1:8080:8080 \
  -e GATEWAY_API_KEY="$GATEWAY_API_KEY" \
  -e CODEX_AUTH_FILE=/credentials/auth.json \
  -v "$HOME/.codex:/credentials:rw" \
  ghcr.io/sargunv/agent-api-gateway:main
```

## Security and limitations

The server has an exact path/method allowlist, bounded request sizes,
cancellation/backpressure propagation, strict upstream header construction, and
sanitized inventory/error logs. It never logs bodies, prompts, headers, tokens,
account IDs, or credential paths.

There is no UI, account/key database, billing, usage tracking, prompt rewriting,
automatic provider/model rerouting or rotation, plugin execution, WebSocket
transport, or estimated `count_tokens`. Cross-family hosted tools,
documents/files, audio, stateful Responses features, citations, and signed
reasoning/thinking are rejected. Catalog failures and incomplete metadata are
surfaced as structured startup warnings.

## Development

```sh
mise run check
mise run test:unit
mise run test:race
mise run test:clients
mise run build
mise run release:snapshot
```
