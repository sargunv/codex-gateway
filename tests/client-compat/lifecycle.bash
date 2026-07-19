#!/usr/bin/env bash

setup_file() {
  source "$BATS_TEST_DIRNAME/helpers.bash"
  export COMPAT_ROOT="$BATS_FILE_TMPDIR/compat"
  export REQUESTS_FILE="$COMPAT_ROOT/requests.jsonl"
  mkdir -p "$COMPAT_ROOT/bin" "$COMPAT_ROOT/logs" "$COMPAT_ROOT/home"
  chmod 700 "$COMPAT_ROOT/home"

  go build -o "$COMPAT_ROOT/bin/testmodel" ./cmd/testmodel
  go build -o "$COMPAT_ROOT/bin/agent-api-gateway" ./cmd/agent-api-gateway

  export TESTMODEL_READY="$COMPAT_ROOT/testmodel.ready"
  setsid "$COMPAT_ROOT/bin/testmodel" \
    --listen 127.0.0.1:0 \
    --ready-file "$TESTMODEL_READY" \
    --requests-file "$REQUESTS_FILE" \
    --expected-auth "$UPSTREAM_KEY" \
    --downstream-auth "$DOWNSTREAM_KEY" \
    >"$COMPAT_ROOT/logs/testmodel.stdout" \
    2>"$COMPAT_ROOT/logs/testmodel.stderr" &
  export TESTMODEL_PID=$!
  export TESTMODEL_ADDR
  if ! TESTMODEL_ADDR=$(wait_for_ready_file "$TESTMODEL_READY" "$TESTMODEL_PID" testmodel); then
    print_diagnostics
    stop_group "$TESTMODEL_PID"
    return 1
  fi
  export TESTMODEL_URL="http://$TESTMODEL_ADDR"
  if ! wait_for_http "$TESTMODEL_URL/healthz" "$TESTMODEL_PID" testmodel; then
    print_diagnostics
    stop_group "$TESTMODEL_PID"
    return 1
  fi

  export GATEWAY_CONFIG="$COMPAT_ROOT/providers.toml"
  cat >"$GATEWAY_CONFIG" <<EOF
[[providers]]
id = "fixture"
api_key_env = "FIXTURE_UPSTREAM_KEY"

[[providers.endpoints]]
id = "chat"
family = "openai-chat"
base_url = "$TESTMODEL_URL/v1"

[[providers.endpoints]]
id = "responses"
family = "openai-responses"
base_url = "$TESTMODEL_URL/v1"

[[providers.endpoints]]
id = "messages"
family = "anthropic-messages"
base_url = "$TESTMODEL_URL"

[[providers.models]]
id = "fixture-chat"
upstream_id = "fixture-chat"
routes = ["chat"]
preferred_route = "chat"

[[providers.models]]
id = "fixture-responses"
upstream_id = "fixture-responses"
routes = ["responses"]
preferred_route = "responses"

[[providers.models]]
id = "fixture-messages"
upstream_id = "fixture-messages"
routes = ["messages"]
preferred_route = "messages"
EOF

  export GATEWAY_READY="$COMPAT_ROOT/gateway.ready"
  setsid env -i \
    HOME="$COMPAT_ROOT/home" \
    PATH="$PATH" \
    GATEWAY_API_KEY="$DOWNSTREAM_KEY" \
    GATEWAY_CONFIG="$GATEWAY_CONFIG" \
    GATEWAY_ADDR=127.0.0.1:0 \
    GATEWAY_READY_FILE="$GATEWAY_READY" \
    FIXTURE_UPSTREAM_KEY="$UPSTREAM_KEY" \
    "$COMPAT_ROOT/bin/agent-api-gateway" serve \
    >"$COMPAT_ROOT/logs/gateway.stdout" \
    2>"$COMPAT_ROOT/logs/gateway.stderr" &
  export GATEWAY_PID=$!
  export GATEWAY_ADDR
  if ! GATEWAY_ADDR=$(wait_for_ready_file "$GATEWAY_READY" "$GATEWAY_PID" gateway); then
    print_diagnostics
    stop_group "$GATEWAY_PID"
    stop_group "$TESTMODEL_PID"
    return 1
  fi
  export GATEWAY_URL="http://$GATEWAY_ADDR"
  if ! wait_for_http "$GATEWAY_URL/healthz" "$GATEWAY_PID" gateway; then
    print_diagnostics
    stop_group "$GATEWAY_PID"
    stop_group "$TESTMODEL_PID"
    return 1
  fi
}

teardown_file() {
  source "$BATS_TEST_DIRNAME/helpers.bash"
  stop_group "${GATEWAY_PID:-}"
  stop_group "${TESTMODEL_PID:-}"
}

setup() {
  source "$BATS_TEST_DIRNAME/helpers.bash"
  make_client_home
  reset_requests
}

teardown() {
  if [[ "${BATS_TEST_COMPLETED:-}" != "1" ]]; then
    mkdir -p "$BATS_TEST_DIRNAME/artifacts"
    cp "$COMPAT_ROOT/logs/gateway.stderr" "$BATS_TEST_DIRNAME/artifacts/gateway.stderr" 2>/dev/null || true
    cp "$COMPAT_ROOT/logs/testmodel.stderr" "$BATS_TEST_DIRNAME/artifacts/testmodel.stderr" 2>/dev/null || true
    cp "$REQUESTS_FILE" "$BATS_TEST_DIRNAME/artifacts/requests.jsonl" 2>/dev/null || true
  fi
}
