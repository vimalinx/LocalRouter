#!/usr/bin/env bash
set -euo pipefail

unset LOCALROUTER_BASE_URL LOCALROUTER_DISCOVERY_URL LOCALROUTER_DOCS_URL \
  LOCALROUTER_OPENAPI_URL LOCALROUTER_MCP_URL LOCALROUTER_MAINTENANCE_MCP_URL

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/localrouter-xdg.XXXXXX)"
case "$test_root" in
  /tmp/localrouter-xdg.*) ;;
  *) echo "unsafe XDG test directory" >&2; exit 2 ;;
esac
gateway_pid=""
cleanup() {
  if [[ -n "$gateway_pid" ]]; then
    kill "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
  chmod -R u+w "$test_root" 2>/dev/null || true
  find "$test_root" -depth -delete
}
trap cleanup EXIT

test_home="$test_root/home"
config_home="$test_root/config"
data_home="$test_root/data"
state_home="$test_root/state"
cache_home="$test_root/cache"
prefix="$test_root/prefix"
go_module_cache="$(go env GOMODCACHE)"
go_build_cache="$(go env GOCACHE)"
mkdir -p "$test_home"
mkdir -p "$test_home/.agents" "$test_home/.codex" "$test_home/.omp/agent"
printf '%s\n' 'shared-agent-sentinel' >"$test_home/.agents/AGENTS.md"
printf '%s\n' 'codex-sentinel' >"$test_home/.codex/AGENTS.md"
printf '%s\n' 'omp-sentinel' >"$test_home/.omp/agent/AGENTS.md"

HOME="$test_home" \
XDG_CONFIG_HOME="$config_home" \
XDG_DATA_HOME="$data_home" \
XDG_STATE_HOME="$state_home" \
XDG_CACHE_HOME="$cache_home" \
LOCALROUTER_PREFIX="$prefix" \
GOMODCACHE="$go_module_cache" \
GOCACHE="$go_build_cache" \
  "$project_root/tools/install-localrouter.sh" install --no-systemd >"$test_root/install.out"

test -x "$prefix/bin/localrouter"
test -x "$prefix/bin/lr"
"$prefix/bin/lr" help | grep -Fq 'runtime-openai <pack> <model>'
test -f "$test_home/.agents/skills/localrouter-protocol-pack/SKILL.md"
test -f "$test_home/.omp/agent/skills/localrouter-protocol-pack/SKILL.md"
test -f "$test_home/.agents/skills/localrouter-protocol-pack/.localrouter-managed"
test -f "$test_home/.omp/agent/skills/localrouter-protocol-pack/.localrouter-managed"
grep -Fq 'Never read the administrator credential' "$test_home/.agents/skills/localrouter-protocol-pack/SKILL.md"
grep -Fq 'Provider and runtime handoff' "$test_home/.agents/skills/localrouter-protocol-pack/references/runtime-handoff.md"
grep -Fq 'lr runtime-openai <pack> <exact-model>' "$test_home/.agents/skills/localrouter-protocol-pack/references/runtime-handoff.md"
for agent_contract in "$test_home/.agents/AGENTS.md" "$test_home/.codex/AGENTS.md" "$test_home/.omp/agent/AGENTS.md"; do
  test "$(grep -Fc '<!-- LOCALROUTER:BEGIN managed-block global-consumer-contract version=1 -->' "$agent_contract")" = "1"
  grep -Fq 'lr find model --exact <pack>:<model-id>' "$agent_contract"
done
grep -Fq 'shared-agent-sentinel' "$test_home/.agents/AGENTS.md"
grep -Fq 'codex-sentinel' "$test_home/.codex/AGENTS.md"
grep -Fq 'omp-sentinel' "$test_home/.omp/agent/AGENTS.md"
expected_version="$(tr -d '\n' <"$project_root/VERSION")"
test "$("$prefix/bin/localrouter" version | awk '{print $2}')" = "$expected_version"
test -f "$config_home/systemd/user/localrouter.service"
grep -Fq "ExecStart=$prefix/bin/localrouter" "$config_home/systemd/user/localrouter.service"
test "$(stat -c '%a' "$config_home/localrouter/config.env")" = "600"

gateway_port="$(python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
)"
HOME="$test_home" \
XDG_CONFIG_HOME="$config_home" \
XDG_DATA_HOME="$data_home" \
XDG_STATE_HOME="$state_home" \
XDG_CACHE_HOME="$cache_home" \
LOCAL_GATEWAY_PORT="$gateway_port" \
LOCAL_GATEWAY_UPDATE_CHECK_ENABLED=false \
  "$prefix/bin/localrouter" >"$test_root/gateway.log" 2>&1 &
gateway_pid=$!

for _ in $(seq 1 100); do
  if curl --noproxy '*' --fail --silent "http://127.0.0.1:$gateway_port/healthz" >/dev/null 2>&1; then
    break
  fi
  sleep 0.05
done
curl --noproxy '*' --fail --silent "http://127.0.0.1:$gateway_port/healthz" | jq -e '.ok and .engine == "localrouter-native"' >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$gateway_port/.well-known/localrouter.json" | jq -e '.scope == "loopback" and (.protocols | length == 0)' >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$gateway_port/local/status" | jq -e '.admin_auth_enabled == false' >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$gateway_port/local/api/summary" | jq -e '.success == true and .data.admin_auth_enabled == false' >/dev/null

test -z "$(find "$config_home/localrouter/protocols" -maxdepth 1 -type f -name '*.json' -print -quit)"
test -f "$config_home/localrouter/protocols/schema/protocol-pack-v3.schema.json"
test -f "$config_home/localrouter/channel-profiles.json"
test -f "$data_home/localrouter/admin-token"
test -f "$data_home/localrouter/api-token"
test -f "$data_home/localrouter/localrouter.db"
test -f "$state_home/localrouter/protocol-events.jsonl"
test -d "$state_home/localrouter/protocol-revisions"
test -d "$cache_home/localrouter"
test "$(stat -c '%a' "$config_home/localrouter")" = "700"
test "$(stat -c '%a' "$data_home/localrouter")" = "700"
test "$(stat -c '%a' "$state_home/localrouter")" = "700"
test "$(stat -c '%a' "$data_home/localrouter/api-token")" = "600"

paths_json="$(HOME="$test_home" XDG_CONFIG_HOME="$config_home" XDG_DATA_HOME="$data_home" XDG_STATE_HOME="$state_home" XDG_CACHE_HOME="$cache_home" "$prefix/bin/localrouter" paths)"
jq -e --arg config "$config_home/localrouter" --arg data "$data_home/localrouter" --arg state "$state_home/localrouter" --arg cache "$cache_home/localrouter" \
  '.config_dir == $config and .channel_profiles == ($config + "/channel-profiles.json") and .data_dir == $data and .state_dir == $state and .cache_dir == $cache and .admin_auth_file == ($data + "/admin-auth.json")' <<<"$paths_json" >/dev/null

HOME="$test_home" \
XDG_DATA_HOME="$data_home" \
LOCALROUTER_BASE_URL="http://127.0.0.1:$gateway_port" \
LOCALROUTER_API_TOKEN_FILE="$data_home/localrouter/api-token" \
  "$prefix/bin/lr" request GET /v1/models | jq -e '.object == "list"' >/dev/null

env -u LOCALROUTER_API_TOKEN_FILE -u LOCALROUTER_ADMIN_TOKEN_FILE -u LOCALROUTER_PROJECT_ROOT \
  HOME="$test_home" \
  XDG_DATA_HOME="$data_home" \
  LOCALROUTER_BASE_URL="http://127.0.0.1:$gateway_port" \
  "$prefix/bin/lr" env | jq -e --arg token_file "$data_home/localrouter/api-token" \
    '.api_token_file == $token_file and .token_value_exported == false' >/dev/null
env -u LOCALROUTER_API_TOKEN_FILE -u LOCALROUTER_ADMIN_TOKEN_FILE -u LOCALROUTER_PROJECT_ROOT \
  HOME="$test_home" \
  XDG_DATA_HOME="$data_home" \
  LOCALROUTER_BASE_URL="http://127.0.0.1:$gateway_port" \
  "$prefix/bin/lr" request GET /v1/models | jq -e '.object == "list"' >/dev/null
env -u LOCALROUTER_API_TOKEN_FILE -u LOCALROUTER_ADMIN_TOKEN_FILE -u LOCALROUTER_PROJECT_ROOT -u XDG_DATA_HOME \
  HOME="$test_home" \
  "$prefix/bin/lr" env | jq -e --arg token_file "$test_home/.local/share/localrouter/api-token" \
    '.api_token_file == $token_file and .token_value_exported == false' >/dev/null

legacy_project_root="$test_root/legacy-project"
HOME="$test_home" \
XDG_DATA_HOME="$data_home" \
LOCALROUTER_PROJECT_ROOT="$legacy_project_root" \
LOCALROUTER_API_TOKEN_FILE="$legacy_project_root/gateway/data/api-token" \
LOCALROUTER_ADMIN_TOKEN_FILE="$legacy_project_root/gateway/data/admin-token" \
LOCALROUTER_BASE_URL="http://127.0.0.1:$gateway_port" \
  "$prefix/bin/lr" env | jq -e \
    --arg api_token_file "$data_home/localrouter/api-token" \
    --arg admin_token_file "$data_home/localrouter/admin-token" \
    '.api_token_file == $api_token_file and .admin_token_file == $admin_token_file' >/dev/null
HOME="$test_home" \
XDG_DATA_HOME="$data_home" \
LOCALROUTER_PROJECT_ROOT="$legacy_project_root" \
LOCALROUTER_API_TOKEN_FILE="$legacy_project_root/gateway/data/api-token" \
LOCALROUTER_BASE_URL="http://127.0.0.1:$gateway_port" \
  "$prefix/bin/lr" request GET /v1/models | jq -e '.object == "list"' >/dev/null

relative_home="$test_root/relative-home"
mkdir -p "$relative_home"
relative_paths="$(HOME="$relative_home" XDG_CONFIG_HOME=relative XDG_DATA_HOME=relative XDG_STATE_HOME=relative XDG_CACHE_HOME=relative "$prefix/bin/localrouter" paths)"
jq -e --arg data "$relative_home/.local/share/localrouter" --arg state "$relative_home/.local/state/localrouter" \
  '.data_dir == $data and .state_dir == $state' <<<"$relative_paths" >/dev/null

HOME="$test_home" \
XDG_CONFIG_HOME="$config_home" \
XDG_DATA_HOME="$data_home" \
XDG_STATE_HOME="$state_home" \
XDG_CACHE_HOME="$cache_home" \
LOCALROUTER_PREFIX="$prefix" \
  "$project_root/tools/install-localrouter.sh" uninstall --no-systemd >"$test_root/uninstall.out"
test ! -e "$test_home/.agents/skills/localrouter-protocol-pack"
test ! -e "$test_home/.omp/agent/skills/localrouter-protocol-pack"
for agent_contract in "$test_home/.agents/AGENTS.md" "$test_home/.codex/AGENTS.md" "$test_home/.omp/agent/AGENTS.md"; do
  ! grep -Fq '<!-- LOCALROUTER:BEGIN' "$agent_contract"
done
grep -Fq 'shared-agent-sentinel' "$test_home/.agents/AGENTS.md"
grep -Fq 'codex-sentinel' "$test_home/.codex/AGENTS.md"
grep -Fq 'omp-sentinel' "$test_home/.omp/agent/AGENTS.md"

echo "XDG layout, standalone install/uninstall, global Agent contract, generated API key, and loopback service acceptance passed"
