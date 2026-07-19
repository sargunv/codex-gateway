#!/usr/bin/env bash

readonly COMPAT_SENTINEL="COMPAT_OK"
readonly DOWNSTREAM_KEY="test-gateway-key"
readonly UPSTREAM_KEY="fixture-upstream-key"

wait_for_ready_file() {
  local file=$1 pid=$2 label=$3
  local i
  for i in {1..200}; do
    if [[ -s "$file" ]]; then
      tr -d '\r\n' <"$file"
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$label exited before publishing readiness" >&2
      return 1
    fi
    sleep 0.05
  done
  echo "timed out waiting for $label readiness" >&2
  return 1
}

wait_for_http() {
  local url=$1 pid=$2 label=$3
  local i
  for i in {1..100}; do
    if curl --fail --silent --show-error --max-time 1 "$url" >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "$label exited before becoming healthy" >&2
      return 1
    fi
    sleep 0.05
  done
  echo "timed out waiting for $label health" >&2
  return 1
}

stop_group() {
  local pid=${1:-}
  [[ -n "$pid" ]] || return 0
  kill -TERM -- "-$pid" 2>/dev/null || true
  local i
  for i in {1..40}; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.05
  done
  if kill -0 "$pid" 2>/dev/null; then
    kill -KILL -- "-$pid" 2>/dev/null || true
  fi
  wait "$pid" 2>/dev/null || true
}

print_diagnostics() {
  echo "--- gateway stderr (last 80 lines) ---" >&2
  tail -n 80 "$COMPAT_ROOT/logs/gateway.stderr" >&2 2>/dev/null || true
  echo "--- testmodel stderr (last 80 lines) ---" >&2
  tail -n 80 "$COMPAT_ROOT/logs/testmodel.stderr" >&2 2>/dev/null || true
  echo "--- structural requests ---" >&2
  tail -n 20 "$REQUESTS_FILE" >&2 2>/dev/null || true
}

assert_success_with_marker() {
  if [[ "$status" -ne 0 || "$output" != *"$COMPAT_SENTINEL"* ]]; then
    echo "status: $status" >&2
    echo "output: $output" >&2
    echo "stderr: ${stderr:-}" >&2
    print_diagnostics
    return 1
  fi
}

assert_request() {
  local family=$1 model=$2 auth_header=$3 tools=${4:-any}
  python3 - "$REQUESTS_FILE" "$family" "$model" "$auth_header" "$tools" <<'PY'
import json, pathlib, sys
path, family, model, auth_header, tools = sys.argv[1:]
lines = [line for line in pathlib.Path(path).read_text().splitlines() if line]
assert len(lines) == 1, f"expected exactly one generation request, got {len(lines)}: {lines}"
r = json.loads(lines[0])
assert r["family"] == family, r
assert r["model"] == model, r
assert r["stream"] is True, r
assert r["has_user_input"] is True, r
assert r["auth_header"] == auth_header, r
assert r["auth_valid"] is True, r
assert r["downstream_auth_leaked"] is False, r
if tools != "any":
    assert r["tools_present"] is (tools == "true"), r
if family == "anthropic-messages":
    assert r["anthropic_version"] == "2023-06-01", r
PY
}

reset_requests() {
  curl --fail --silent --show-error -X DELETE "$TESTMODEL_URL/requests" >/dev/null
}

make_client_home() {
  CLIENT_ROOT="$BATS_TEST_TMPDIR/client"
  CLIENT_HOME="$CLIENT_ROOT/home"
  CLIENT_WORK="$CLIENT_ROOT/work"
  mkdir -p "$CLIENT_HOME" "$CLIENT_WORK" \
    "$CLIENT_ROOT/config" "$CLIENT_ROOT/data" "$CLIENT_ROOT/cache" "$CLIENT_ROOT/state"
  chmod 700 "$CLIENT_HOME"
}

run_clean() {
  run --separate-stderr timeout --kill-after=5s 50s env -i \
    HOME="$CLIENT_HOME" \
    XDG_CONFIG_HOME="$CLIENT_ROOT/config" \
    XDG_DATA_HOME="$CLIENT_ROOT/data" \
    XDG_CACHE_HOME="$CLIENT_ROOT/cache" \
    XDG_STATE_HOME="$CLIENT_ROOT/state" \
    PATH="$PATH" \
    LANG=C.UTF-8 \
    LC_ALL=C.UTF-8 \
    TERM=dumb \
    NO_COLOR=1 \
    sh -c 'cd "$1" && shift && exec "$@"' sh "$CLIENT_WORK" "$@"
}

write_pi_models() {
  local api=$1 model=$2
  local base="$GATEWAY_URL/v1"
  if [[ "$api" == "anthropic-messages" ]]; then
    base="$GATEWAY_URL"
  fi
  mkdir -p "$CLIENT_ROOT/pi"
  cat >"$CLIENT_ROOT/pi/models.json" <<EOF
{
  "providers": {
    "fixture": {
      "baseUrl": "$base",
      "apiKey": "$DOWNSTREAM_KEY",
      "api": "$api",
      "models": [{
        "id": "$model",
        "name": "$model",
        "reasoning": false,
        "input": ["text"],
        "supportsTools": false,
        "contextWindow": 128000,
        "maxTokens": 4096
      }]
    }
  }
}
EOF
}

write_omp_models() {
  local api=$1 model=$2
  mkdir -p "$CLIENT_ROOT/omp"
  cat >"$CLIENT_ROOT/omp/models.yml" <<EOF
providers:
  fixture:
    baseUrl: $GATEWAY_URL/v1
    apiKey: COMPAT_API_KEY
    api: $api
    models:
      - id: $model
        name: $model
        reasoning: false
        input: [text]
        supportsTools: false
        contextWindow: 128000
        maxTokens: 4096
EOF
  cat >"$CLIENT_ROOT/omp/config.yml" <<'EOF'
startup:
  checkUpdate: false
telemetry:
  enabled: false
EOF
}
