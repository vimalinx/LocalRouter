#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
e2e_root="$(mktemp -d)"
gateway_port=""
upstream_port=""
gateway_pid=""
upstream_pid=""

cleanup() {
  if [[ -n "$gateway_pid" ]] && kill -0 "$gateway_pid" 2>/dev/null; then
    kill -TERM "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
  if [[ -n "$upstream_pid" ]] && kill -0 "$upstream_pid" 2>/dev/null; then
    kill -TERM "$upstream_pid" 2>/dev/null || true
    wait "$upstream_pid" 2>/dev/null || true
  fi
  rm -rf -- "$e2e_root"
}
trap cleanup EXIT

choose_port() {
  local range_start="$1"
  local range_end="$2"
  local candidate=""
  for candidate in $(seq "$range_start" "$range_end"); do
    if ! ss -ltn | grep -q ":${candidate} "; then
      printf '%s' "$candidate"
      return 0
    fi
  done
  return 1
}

gateway_port="$(choose_port 18340 18359)"
upstream_port="$(choose_port 18400 18419)"

python3 "$project_root/tests/mock_openai.py" "$upstream_port" >"$e2e_root/upstream.log" 2>&1 &
upstream_pid=$!

LOCAL_GATEWAY_CONFIG_DIR="$e2e_root/config" \
LOCAL_GATEWAY_DATA_DIR="$e2e_root/data" \
LOCAL_GATEWAY_STATE_DIR="$e2e_root/state" \
LOCAL_GATEWAY_CACHE_DIR="$e2e_root/cache" \
LOCAL_GATEWAY_PORT="$gateway_port" \
GIN_MODE=release \
"$project_root/gateway/localrouter" --log-dir "$e2e_root/logs" >"$e2e_root/gateway.log" 2>&1 &
gateway_pid=$!

for _ in $(seq 1 120); do
  upstream_ready=false
  gateway_ready=false
  if curl --fail --silent "http://127.0.0.1:${upstream_port}/health" >"$e2e_root/upstream-health.json"; then upstream_ready=true; fi
  if curl --fail --silent "http://127.0.0.1:${gateway_port}/healthz" >"$e2e_root/gateway-health.json"; then gateway_ready=true; fi
  if [[ "$upstream_ready" == true && "$gateway_ready" == true ]]; then break; fi
  if ! kill -0 "$upstream_pid" 2>/dev/null || ! kill -0 "$gateway_pid" 2>/dev/null; then
    sed -n '1,160p' "$e2e_root/upstream.log" >&2
    sed -n '1,240p' "$e2e_root/gateway.log" >&2
    exit 1
  fi
  sleep 0.25
done

sed 's/^/X-Local-Admin: /' "$e2e_root/data/admin-token" >"$e2e_root/admin-header"
sed 's/^/Authorization: Bearer /' "$e2e_root/data/api-token" >"$e2e_root/api-header"
chmod 0600 "$e2e_root/admin-header" "$e2e_root/api-header"

jq -n \
  --arg base_url "http://127.0.0.1:${upstream_port}" \
  '{mode:"single",channel:{type:1,name:"Deterministic local upstream",key:"mock-key",models:"localrouter-smoke",group:"default",status:1,weight:100,priority:0,auto_ban:1,base_url:$base_url}}' \
  >"$e2e_root/channel.json"
curl --fail --silent --show-error \
  --header @"$e2e_root/admin-header" \
  --header 'Content-Type: application/json' \
  --data @"$e2e_root/channel.json" \
  "http://127.0.0.1:${gateway_port}/local/api/channels" >"$e2e_root/channel-response.json"
jq -e '.success == true' "$e2e_root/channel-response.json" >/dev/null

curl --fail --silent --show-error --header @"$e2e_root/api-header" \
  "http://127.0.0.1:${gateway_port}/v1/models" >"$e2e_root/models.json"
jq -e '.object == "list" and any(.data[]; .id == "localrouter-smoke")' "$e2e_root/models.json" >/dev/null

jq -n '{model:"localrouter-smoke",messages:[{role:"user",content:"reply deterministically"}],stream:false}' >"$e2e_root/nonstream-request.json"
curl --fail --silent --show-error \
  --header @"$e2e_root/api-header" \
  --header 'Content-Type: application/json' \
  --data @"$e2e_root/nonstream-request.json" \
  "http://127.0.0.1:${gateway_port}/v1/chat/completions" >"$e2e_root/nonstream-response.json"
jq -e '.choices[0].message.content == "non-stream-ok" and .usage.total_tokens == 5' "$e2e_root/nonstream-response.json" >/dev/null

jq -n '{model:"localrouter-smoke",messages:[{role:"user",content:"stream deterministically"}],stream:true,stream_options:{include_usage:true}}' >"$e2e_root/stream-request.json"
curl --fail --silent --show-error --no-buffer \
  --header @"$e2e_root/api-header" \
  --header 'Content-Type: application/json' \
  --data @"$e2e_root/stream-request.json" \
  "http://127.0.0.1:${gateway_port}/v1/chat/completions" >"$e2e_root/stream-response.txt"
grep -q 'stream-ok' "$e2e_root/stream-response.txt"
grep -q 'data: \[DONE\]' "$e2e_root/stream-response.txt"

curl --fail --silent --show-error --header @"$e2e_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/logs?page=1&page_size=20" >"$e2e_root/logs.json"
jq -e '.success == true and (.data.items | length) >= 2' "$e2e_root/logs.json" >/dev/null

echo "non-streaming and streaming relay acceptance passed"
