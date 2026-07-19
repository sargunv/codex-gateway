#!/usr/bin/env bats

load lifecycle.bash
bats_require_minimum_version 1.5.0

readonly PROMPT="Return the fixture marker COMPAT_OK exactly."

@test "fixture fails closed on unknown routes, bad auth, and duplicate generations" {
  run curl --silent --output /dev/null --write-out '%{http_code}' "$TESTMODEL_URL/not-a-route"
  [[ "$status" -eq 0 && "$output" == "404" ]]

  run curl --silent --output /dev/null --write-out '%{http_code}' \
    -H 'Authorization: Bearer wrong-key' -H 'Content-Type: application/json' \
    --data '{"model":"fixture-chat","stream":true,"messages":[{"role":"user","content":"Return the fixture marker"}]}' \
    "$TESTMODEL_URL/v1/chat/completions"
  [[ "$status" -eq 0 && "$output" == "400" ]]

  run curl --silent --output /dev/null --write-out '%{http_code}' \
    -H "Authorization: Bearer $UPSTREAM_KEY" -H 'Content-Type: application/json' \
    --data '{"model":"fixture-chat","stream":true,"messages":[{"role":"user","content":"Return the fixture marker"}]}' \
    "$TESTMODEL_URL/v1/chat/completions"
  [[ "$status" -eq 0 && "$output" == "409" ]]
}

@test "Codex consumes OpenAI Responses SSE" {
  run_clean env \
    CODEX_API_KEY="$DOWNSTREAM_KEY" \
    codex exec \
    --ephemeral \
    --skip-git-repo-check \
    --ignore-rules \
    --ignore-user-config \
    --sandbox read-only \
    --json \
    --model fixture/fixture-responses \
    -c "model_providers.fixture={name='Fixture',base_url='$GATEWAY_URL/v1',env_key='CODEX_API_KEY',wire_api='responses',requires_openai_auth=false,supports_websockets=false,request_max_retries=0,stream_max_retries=0}" \
    -c 'model_provider="fixture"' \
    "$PROMPT"
  assert_success_with_marker
  assert_request openai-responses fixture-responses authorization true
}

@test "Claude Code consumes Anthropic Messages SSE" {
  run_clean env \
    ANTHROPIC_BASE_URL="$GATEWAY_URL" \
    ANTHROPIC_API_KEY="$DOWNSTREAM_KEY" \
    ANTHROPIC_MODEL=fixture/fixture-messages \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
    DISABLE_AUTOUPDATER=1 \
    claude --bare --safe-mode -p "$PROMPT" \
    --model fixture/fixture-messages \
    --output-format json \
    --no-session-persistence \
    --strict-mcp-config \
    --mcp-config '{"mcpServers":{}}' \
    --tools ""
  assert_success_with_marker
  assert_request anthropic-messages fixture-messages x-api-key true
}

@test "OpenCode consumes OpenAI Chat Completions SSE" {
  local config
  config=$(cat <<EOF
{"\$schema":"https://opencode.ai/config.json","plugin":[],"agent":{"title":{"disable":true}},"provider":{"fixture-chat":{"npm":"@ai-sdk/openai-compatible","name":"Fixture Chat","options":{"baseURL":"$GATEWAY_URL/v1","apiKey":"$DOWNSTREAM_KEY"},"models":{"fixture/fixture-chat":{"name":"Fixture Chat","limit":{"context":128000,"output":4096}}}}}}
EOF
)
  run_clean env \
    OPENCODE_CONFIG_CONTENT="$config" \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    opencode run --pure --model fixture-chat/fixture/fixture-chat --format json "$PROMPT"
  assert_success_with_marker
  assert_request openai-chat fixture-chat authorization true
}

@test "OpenCode consumes OpenAI Responses SSE" {
  local config
  config=$(cat <<EOF
{"\$schema":"https://opencode.ai/config.json","plugin":[],"agent":{"title":{"disable":true}},"provider":{"fixture-responses":{"npm":"@ai-sdk/openai","name":"Fixture Responses","options":{"baseURL":"$GATEWAY_URL/v1","apiKey":"$DOWNSTREAM_KEY"},"models":{"fixture/fixture-responses":{"name":"Fixture Responses","limit":{"context":128000,"output":4096}}}}}}
EOF
)
  run_clean env \
    OPENCODE_CONFIG_CONTENT="$config" \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    opencode run --pure --model fixture-responses/fixture/fixture-responses --format json "$PROMPT"
  assert_success_with_marker
  assert_request openai-responses fixture-responses authorization true
}

@test "OpenCode consumes Anthropic Messages SSE" {
  local config
  config=$(cat <<EOF
{"\$schema":"https://opencode.ai/config.json","plugin":[],"agent":{"title":{"disable":true}},"provider":{"fixture-messages":{"npm":"@ai-sdk/anthropic","name":"Fixture Messages","options":{"baseURL":"$GATEWAY_URL/v1","apiKey":"$DOWNSTREAM_KEY"},"models":{"fixture/fixture-messages":{"name":"Fixture Messages","limit":{"context":128000,"output":4096}}}}}}
EOF
)
  run_clean env \
    OPENCODE_CONFIG_CONTENT="$config" \
    OPENCODE_DISABLE_AUTOUPDATE=1 \
    OPENCODE_DISABLE_MODELS_FETCH=1 \
    opencode run --pure --model fixture-messages/fixture/fixture-messages --format json "$PROMPT"
  assert_success_with_marker
  assert_request anthropic-messages fixture-messages x-api-key true
}

@test "Pi consumes OpenAI Chat Completions SSE" {
  write_pi_models openai-completions fixture/fixture-chat
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/pi" \
    PI_OFFLINE=1 PI_TELEMETRY=0 COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    pi -p --no-session --no-tools --no-extensions --no-skills \
    --no-prompt-templates --no-context-files --offline \
    --provider fixture --model fixture/fixture-chat "$PROMPT"
  assert_success_with_marker
  assert_request openai-chat fixture-chat authorization false
}

@test "Pi consumes OpenAI Responses SSE" {
  write_pi_models openai-responses fixture/fixture-responses
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/pi" \
    PI_OFFLINE=1 PI_TELEMETRY=0 COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    pi -p --no-session --no-tools --no-extensions --no-skills \
    --no-prompt-templates --no-context-files --offline \
    --provider fixture --model fixture/fixture-responses "$PROMPT"
  assert_success_with_marker
  assert_request openai-responses fixture-responses authorization false
}

@test "Pi consumes Anthropic Messages SSE" {
  write_pi_models anthropic-messages fixture/fixture-messages
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/pi" \
    PI_OFFLINE=1 PI_TELEMETRY=0 COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    pi -p --no-session --no-tools --no-extensions --no-skills \
    --no-prompt-templates --no-context-files --offline \
    --provider fixture --model fixture/fixture-messages "$PROMPT"
  assert_success_with_marker
  assert_request anthropic-messages fixture-messages x-api-key false
}

@test "OMP consumes OpenAI Chat Completions SSE" {
  write_omp_models openai-completions fixture/fixture-chat
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/omp" \
    COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    omp -p --no-session --no-tools --no-extensions --no-skills --no-rules \
    --no-prewalk --no-title --max-time 45s \
    --provider fixture --model fixture/fixture-chat "$PROMPT"
  assert_success_with_marker
  assert_request openai-chat fixture-chat authorization false
}

@test "OMP consumes OpenAI Responses SSE" {
  write_omp_models openai-responses fixture/fixture-responses
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/omp" \
    COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    omp -p --no-session --no-tools --no-extensions --no-skills --no-rules \
    --no-prewalk --no-title --max-time 45s \
    --provider fixture --model fixture/fixture-responses "$PROMPT"
  assert_success_with_marker
  assert_request openai-responses fixture-responses authorization true
}

@test "OMP consumes Anthropic Messages SSE" {
  write_omp_models anthropic-messages fixture/fixture-messages
  run_clean env \
    PI_CODING_AGENT_DIR="$CLIENT_ROOT/omp" \
    COMPAT_API_KEY="$DOWNSTREAM_KEY" \
    omp -p --no-session --no-tools --no-extensions --no-skills --no-rules \
    --no-prewalk --no-title --max-time 45s \
    --provider fixture --model fixture/fixture-messages "$PROMPT"
  assert_success_with_marker
  assert_request anthropic-messages fixture-messages x-api-key true
}
