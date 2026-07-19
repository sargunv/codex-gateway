# Deterministic external-client compatibility harness

This Bats suite starts `cmd/testmodel` and the real gateway on kernel-assigned
loopback ports. Both processes publish atomic readiness files after binding.
Every client runs with an empty, per-test `HOME`, XDG directories, config tree,
and working directory. The environment contains fixture-only keys; the suite
never reads or needs subscription credentials.

Run the pinned matrix with:

```sh
mise install
mise run test:clients
```

The suite exercises these native streaming protocols:

- Codex CLI: OpenAI Responses
- Claude Code: Anthropic Messages
- OpenCode: OpenAI Chat Completions and Anthropic Messages
- Pi: Chat Completions, Responses, and Messages
- Oh My Pi (`omp`): Chat Completions, Responses, and Messages

Each invocation must print `COMPAT_OK`. The sanitized JSONL recorder separately
asserts the family, upstream model, streaming mode, user-input presence, tools
field presence, upstream auth kind/validity, and that the downstream gateway key
did not leak. Unknown routes, invalid fixture auth, and a second generation
request fail closed. Commands have hard timeouts, and process groups are always
terminated and reaped.

Auto-update, remote model fetching, telemetry, plugins/extensions, sessions,
rules, skills, and tools are disabled where each pinned client supports it. The
CI runner does not provide a portable unprivileged network namespace, so hard
non-loopback egress blocking is not claimed; isolation instead relies on a clean
environment/config plus each client's offline/nonessential-traffic switches.

## LibreChat

LibreChat is intentionally excluded from required PR CI. Version `v0.8.7` is
source-locked at commit `8e5ef1fb31e9d63b735c089b21cbc82c50acce46`, but it has
no supported noninteractive chat CLI. A meaningful check requires a Mongo-backed
application stack and browser-driven registration/login/chat. The project's
convenient development image is mutable, and this repository does not yet have
an independently built and published image digest for that exact commit. Adding
an allegedly deterministic container smoke test without such an immutable digest
would weaken this harness. LibreChat should remain a separate, opt-in Compose +
Playwright tier once a commit-built image is published and pinned by digest; the
required CI contract is already covered at the protocol level and by the
external CLIs above.
