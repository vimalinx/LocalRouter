#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/localrouter-lan.XXXXXX)"
case "$test_root" in
  /tmp/localrouter-lan.*) ;;
  *) echo "unsafe LAN test directory" >&2; exit 2 ;;
esac
gateway_pid=""

cleanup() {
  if [[ -n "$gateway_pid" ]] && kill -0 "$gateway_pid" 2>/dev/null; then
    kill -TERM "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
  find "$test_root" -depth -delete
}
trap cleanup EXIT

free_port() {
  python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    print(listener.getsockname()[1])
PY
}

local_port="$(free_port)"
lan_port="$(free_port)"
while [[ "$lan_port" = "$local_port" ]]; do lan_port="$(free_port)"; done

LOCAL_GATEWAY_CONFIG_DIR="$test_root/config" \
LOCAL_GATEWAY_DATA_DIR="$test_root/data" \
LOCAL_GATEWAY_STATE_DIR="$test_root/state" \
LOCAL_GATEWAY_CACHE_DIR="$test_root/cache" \
LOCAL_GATEWAY_PORT="$local_port" \
LOCAL_GATEWAY_LAN_ENABLED=true \
LOCAL_GATEWAY_LAN_HOST=0.0.0.0 \
LOCAL_GATEWAY_LAN_PORT="$lan_port" \
LOCAL_GATEWAY_LAN_ALLOWED_ORIGINS=https://client.home \
GIN_MODE=release \
  "$project_root/gateway/localrouter" >"$test_root/gateway.log" 2>&1 &
gateway_pid=$!

for _ in $(seq 1 120); do
  if curl --noproxy '*' --fail --silent "http://127.0.0.1:$local_port/healthz" >/dev/null 2>&1 && \
     curl --noproxy '*' --fail --silent "http://127.0.0.1:$lan_port/healthz" >"$test_root/lan-health.json" 2>/dev/null; then
    break
  fi
  if ! kill -0 "$gateway_pid" 2>/dev/null; then
    sed -n '1,160p' "$test_root/gateway.log" >&2
    exit 1
  fi
  sleep 0.05
done

jq -e '.ok == true and .mode == "lan-service"' "$test_root/lan-health.json" >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$local_port/.well-known/localrouter.json" |
  jq -e '.scope == "loopback" and .maintenance.available == true' >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$lan_port/.well-known/localrouter.json" >"$test_root/lan-discovery.json"
jq -e '.scope == "lan-service" and .maintenance.available == false and .maintenance.scope == "loopback" and .agent_identity.console == "loopback-only"' "$test_root/lan-discovery.json" >/dev/null

for path in /local/status /local/api/summary /manage/mcp; do
  status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$lan_port$path")"
  test "$status" = "404"
done

unauthorized_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$lan_port/v1/models")"
test "$unauthorized_status" = "401"
sed 's/^/Authorization: Bearer /' "$test_root/data/api-token" >"$test_root/api-header"
chmod 0600 "$test_root/api-header"
curl --noproxy '*' --fail --silent --header @"$test_root/api-header" "http://127.0.0.1:$lan_port/v1/models" |
  jq -e '.object == "list"' >/dev/null

if env -u LOCALROUTER_DISCOVERY_URL -u LOCALROUTER_MCP_URL -u LOCALROUTER_MAINTENANCE_MCP_URL \
   LOCALROUTER_BASE_URL="http://0.0.0.0:$lan_port" \
   LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
   "$project_root/tools/lr" status >"$test_root/lr-refused.out" 2>"$test_root/lr-refused.err"; then
  echo "lr accepted a LAN URL without explicit opt-in" >&2
  exit 1
fi
grep -Fq 'LOCALROUTER_ALLOW_LAN=true' "$test_root/lr-refused.err"
env -u LOCALROUTER_DISCOVERY_URL -u LOCALROUTER_MCP_URL -u LOCALROUTER_MAINTENANCE_MCP_URL \
LOCALROUTER_ALLOW_LAN=true \
LOCALROUTER_BASE_URL="http://0.0.0.0:$lan_port" \
LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" status | jq -e '.console.available == false and .console.scope == "loopback-only"' >/dev/null
env -u LOCALROUTER_DISCOVERY_URL -u LOCALROUTER_MCP_URL -u LOCALROUTER_MAINTENANCE_MCP_URL \
LOCALROUTER_ALLOW_LAN=true \
LOCALROUTER_BASE_URL="http://0.0.0.0:$lan_port" \
LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" request GET /v1/models | jq -e '.object == "list"' >/dev/null

curl --noproxy '*' --fail --silent --header 'Origin: https://client.home' "http://127.0.0.1:$lan_port/" >"$test_root/allowed.json"
jq -e '.scope == "lan-service" and .console == "loopback-only"' "$test_root/allowed.json" >/dev/null
blocked_origin_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' --header 'Origin: https://untrusted.home' "http://127.0.0.1:$lan_port/")"
test "$blocked_origin_status" = "403"

local_listener="$(ss -ltnH "sport = :$local_port" | awk '{print $4}')"
test "$local_listener" = "127.0.0.1:$local_port"
lan_listener="$(ss -ltnH "sport = :$lan_port" | awk '{print $4}')"
[[ "$lan_listener" == "0.0.0.0:$lan_port" || "$lan_listener" == "*:$lan_port" ]]

kill -TERM "$gateway_pid"
wait "$gateway_pid"
gateway_pid=""
grep -Fq 'LocalRouter lan-service listener' "$test_root/gateway.log"
grep -Fq 'received signal terminated, shutting down' "$test_root/gateway.log"

echo "LocalRouter LAN service acceptance passed"
