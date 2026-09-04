#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
smoke_root="$(mktemp -d)"
smoke_port=""
server_pid=""

cleanup() {
  if [[ -n "$server_pid" ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -TERM "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf -- "$smoke_root"
}
trap cleanup EXIT

for candidate_port in $(seq 18320 18339); do
  if ! ss -ltn | grep -q ":${candidate_port} "; then
    smoke_port="$candidate_port"
    break
  fi
done
if [[ -z "$smoke_port" ]]; then
  echo "no free smoke port found in 18320-18339" >&2
  exit 1
fi

LOCAL_GATEWAY_CONFIG_DIR="$smoke_root/config" \
LOCAL_GATEWAY_DATA_DIR="$smoke_root/data" \
LOCAL_GATEWAY_STATE_DIR="$smoke_root/state" \
LOCAL_GATEWAY_CACHE_DIR="$smoke_root/cache" \
LOCAL_GATEWAY_PORT="$smoke_port" \
GIN_MODE=release \
"$project_root/gateway/localrouter" --log-dir "$smoke_root/logs" >"$smoke_root/server.log" 2>&1 &
server_pid=$!

for _ in $(seq 1 120); do
  if curl --fail --silent "http://127.0.0.1:${smoke_port}/healthz" >"$smoke_root/health.json"; then
    break
  fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    sed -n '1,200p' "$smoke_root/server.log" >&2
    exit 1
  fi
  sleep 0.25
done

jq -e '.ok == true and .mode == "local-self-use" and .oauth == false' "$smoke_root/health.json" >/dev/null
curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/local/status" >"$smoke_root/status.json"
jq -e '.success == true and .oauth == "external-maintainer" and .admin_auth_enabled == false' "$smoke_root/status.json" >/dev/null
curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/" >"$smoke_root/index.html"
grep -q 'LocalRouter' "$smoke_root/index.html"

curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/local/api/summary" >"$smoke_root/summary.json"
jq -e '.success == true and .data.admin_auth_enabled == false and .data.billing == "usage-accounting" and .data.oauth == "external-maintainer"' "$smoke_root/summary.json" >/dev/null

initial_admin_token="fixture-custom-console-password"
jq -n --arg token "$initial_admin_token" '{enabled:true,token:$token}' >"$smoke_root/admin-auth-enable.json"
curl --fail --silent --show-error --header 'Content-Type: application/json' \
  --request PUT \
  --data-binary @"$smoke_root/admin-auth-enable.json" \
  "http://127.0.0.1:${smoke_port}/local/api/admin-auth" >"$smoke_root/admin-auth-enable-response.json"
jq -e '.success == true and .data.enabled == true and .data.changed == true' "$smoke_root/admin-auth-enable-response.json" >/dev/null
test "$(tr -d '\n' <"$smoke_root/data/admin-token")" = "$initial_admin_token"

unauthorized_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:${smoke_port}/local/api/summary")"
test "$unauthorized_status" = "401"

sed 's/^/X-Local-Admin: /' "$smoke_root/data/admin-token" >"$smoke_root/admin-header"
chmod 0600 "$smoke_root/admin-header"
curl --fail --silent --show-error --header @"$smoke_root/admin-header" \
  "http://127.0.0.1:${smoke_port}/local/api/summary" >"$smoke_root/summary.json"
jq -e '.success == true and .data.admin_auth_enabled == true' "$smoke_root/summary.json" >/dev/null

rotated_admin_token="fixture-custom-admin-token-rotate"
jq -n --arg token "$rotated_admin_token" '{token:$token}' >"$smoke_root/admin-token-change.json"
curl --fail --silent --show-error --header @"$smoke_root/admin-header" \
  --header 'Content-Type: application/json' \
  --request PUT \
  --data-binary @"$smoke_root/admin-token-change.json" \
  "http://127.0.0.1:${smoke_port}/local/api/admin-token" >"$smoke_root/admin-token-change-response.json"
jq -e '.success == true and .data.changed == true' "$smoke_root/admin-token-change-response.json" >/dev/null
! grep -q "$rotated_admin_token" "$smoke_root/admin-token-change-response.json"
test "$(tr -d '\n' <"$smoke_root/data/admin-token")" = "$rotated_admin_token"

old_admin_status="$(curl --silent --output /dev/null --write-out '%{http_code}' --header @"$smoke_root/admin-header" \
  "http://127.0.0.1:${smoke_port}/local/api/summary")"
test "$old_admin_status" = "401"
printf 'X-Local-Admin: %s\n' "$rotated_admin_token" >"$smoke_root/admin-header"
chmod 0600 "$smoke_root/admin-header"
curl --fail --silent --show-error --header @"$smoke_root/admin-header" \
  "http://127.0.0.1:${smoke_port}/local/api/summary" >"$smoke_root/rotated-summary.json"
jq -e '.success == true' "$smoke_root/rotated-summary.json" >/dev/null

curl --fail --silent --show-error --header @"$smoke_root/admin-header" \
  --header 'Content-Type: application/json' \
  --request PUT \
  --data '{"enabled":false}' \
  "http://127.0.0.1:${smoke_port}/local/api/admin-auth" >"$smoke_root/admin-auth-disable-response.json"
jq -e '.success == true and .data.enabled == false and .data.changed == true' "$smoke_root/admin-auth-disable-response.json" >/dev/null
curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/local/api/summary" | jq -e '.data.admin_auth_enabled == false' >/dev/null

sed 's/^/Authorization: Bearer /' "$smoke_root/data/api-token" >"$smoke_root/api-header"
chmod 0600 "$smoke_root/api-header"

curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/.well-known/localrouter.json" >"$smoke_root/discovery.json"
contract_digest="$(jq -r '.contract.digest' "$smoke_root/discovery.json")"
test "${#contract_digest}" = "64"
jq -e '.contract.schema_version == "9" and (.topology.digest | test("^[0-9a-f]{64}$")) and .topology.schema_version == "9" and .pack_model.unit == "service-pack" and (.surfaces | length) == 4 and (.compatibility_packs | length) == 3 and .agent.catalog == "/agent/operations" and .agent.resolve == "/agent/resolve" and .agent.compare == "/agent/compare" and .agent.selection_mode == "agent" and .agent.merged == false and .agent.preflight == "/agent/preflight" and .documentation.agent == "/docs/agent.json" and .invocation.operation_id_is_url == false and (.authentication.applies_to | index("/agent/*") != null) and (.protocols | length == 0)' "$smoke_root/discovery.json" >/dev/null
curl --fail --silent --show-error "http://127.0.0.1:${smoke_port}/docs/agent.json" >"$smoke_root/agent-docs.json"
jq -e --arg digest "$contract_digest" '.schema_version == "9" and .contract_digest == $digest and .selection.mode == "agent" and .selection.merged == false and .identifiers.operation_id_is_url == false and (.discovery_domains.model.cli | contains("lr find model")) and .discovery_domains.pool.cli == "lr find pool <provider-or-pack>" and .discovery_domains.agent_runtime.localrouter_owned == false and .endpoints.catalog.path == "/agent/operations" and .endpoints.compare.path == "/agent/compare" and .endpoints.preflight.upstream_called == false' "$smoke_root/agent-docs.json" >/dev/null

agent_unauthorized_status="$(curl --silent --output "$smoke_root/agent-unauthorized.json" --write-out '%{http_code}' --request POST --header 'Content-Type: application/json' --data '{"query":"web.search"}' "http://127.0.0.1:${smoke_port}/agent/resolve")"
test "$agent_unauthorized_status" = "401"
jq -e '.code == "service_token_required" and .retryable == false and (.next_action | length > 0)' "$smoke_root/agent-unauthorized.json" >/dev/null

resolve_status="$(curl --silent --show-error --output "$smoke_root/resolve.json" --write-out '%{http_code}' --header @"$smoke_root/api-header" --header 'Content-Type: application/json' \
  --request POST --data '{"query":"web.search"}' \
  "http://127.0.0.1:${smoke_port}/agent/resolve")"
test "$resolve_status" = "404"
jq -e '.code == "capability_not_found" and .retryable == false and .owner == "agent"' "$smoke_root/resolve.json" >/dev/null

curl --fail --silent --show-error --header @"$smoke_root/api-header" \
  "http://127.0.0.1:${smoke_port}/agent/operations" >"$smoke_root/catalog.json"
jq -e '.object == "localrouter.operation.list" and .selection_mode == "agent" and .merged == false and .count == 0 and (.operations | length == 0)' "$smoke_root/catalog.json" >/dev/null

curl --fail --silent --show-error --header @"$smoke_root/api-header" \
  "http://127.0.0.1:${smoke_port}/agent/whoami" >"$smoke_root/whoami.json"
jq -e '.service_access == true and .maintenance_access == false and .unrestricted == true' "$smoke_root/whoami.json" >/dev/null
! grep -q "$(tr -d '\n' <"$smoke_root/data/api-token")" "$smoke_root/whoami.json"

curl --fail --silent --show-error --header @"$smoke_root/api-header" \
  "http://127.0.0.1:${smoke_port}/v1/models" >"$smoke_root/models.json"
jq -e '.object == "list" and (.data | type == "array")' "$smoke_root/models.json" >/dev/null

for removed_path in /api/oauth/github /api/user/register /api/stripe/webhook /api/subscription/plans; do
  removed_status="$(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:${smoke_port}${removed_path}")"
  test "$removed_status" = "404"
done

test "$(stat -c '%a' "$smoke_root/data")" = "700"
test "$(stat -c '%a' "$smoke_root/data/admin-token")" = "600"
test "$(stat -c '%a' "$smoke_root/data/admin-auth.json")" = "600"
test "$(stat -c '%a' "$smoke_root/data/api-token")" = "600"
test "$(stat -c '%a' "$smoke_root/data/localrouter.db")" = "600"

local_listener="$(ss -ltnH "sport = :${smoke_port}" | awk '{print $4}')"
test "$local_listener" = "127.0.0.1:${smoke_port}"

echo "local gateway smoke acceptance passed"
