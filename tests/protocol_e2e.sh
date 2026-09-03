#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
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
  rm -rf -- "$test_root"
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

gateway_port="$(choose_port 18360 18379)"
upstream_port="$(choose_port 18420 18439)"
mkdir -p "$test_root/protocols" "$test_root/data/protocol-secrets" "$test_root/data/protocol-pool-sources" "$test_root/external"
jq --arg base_url "http://127.0.0.1:${upstream_port}" \
  '.base_url = $base_url | del(.pool.credentials_file) | .pool.source = {
    format:"json",locator_file:"protocol-pool-sources/search.json",id_path:"email",secret_path:"api_key",disabled_path:"disabled"
  }' \
  "$project_root/tests/fixtures/search-v3.json" >"$test_root/protocols/search.json"
cp -a "$project_root/tests/fixtures/search" "$test_root/protocols/search"
cp -a "$project_root/gateway/protocols/catalogs" "$test_root/protocols/catalogs"
jq --arg base_url "http://127.0.0.1:${upstream_port}" \
  '.base_url = $base_url | del(.pool.credentials_file) | .pool.source = {
    format:"json",locator_file:"protocol-pool-sources/video.json",id_path:"email",secret_path:"api_key",disabled_path:"disabled",eligible_path:"ok"
  }' \
  "$project_root/tests/fixtures/video-v2.json" >"$test_root/protocols/video.json"
jq -n '[
  {email:"account-a@example.invalid",api_key:"fixture-account-a",ok:true,disabled:false},
  {email:"account-b@example.invalid",api_key:"fixture-account-b",ok:true,disabled:false}
]' >"$test_root/external/video-source.json"
jq -n '[{email:"search@example.invalid",api_key:"fixture-value-only",disabled:false,source:"fixture"}]' >"$test_root/external/search-source.json"
jq -n --arg path "$test_root/external/video-source.json" '{schema_version:"1",path:$path}' >"$test_root/data/protocol-pool-sources/video.json"
jq -n --arg path "$test_root/external/search-source.json" '{schema_version:"1",path:$path}' >"$test_root/data/protocol-pool-sources/search.json"
chmod 0700 "$test_root/data" "$test_root/data/protocol-secrets" "$test_root/data/protocol-pool-sources" "$test_root/external"
chmod 0600 "$test_root/data/protocol-pool-sources/search.json" "$test_root/data/protocol-pool-sources/video.json" "$test_root/external/search-source.json" "$test_root/external/video-source.json"

PROTOCOL_TEST_AUTH='fixture-value-only' python3 "$project_root/tests/mock_protocol.py" "$upstream_port" >"$test_root/upstream.log" 2>&1 &
upstream_pid=$!
LOCAL_GATEWAY_CONFIG_DIR="$test_root/config" \
LOCAL_GATEWAY_DATA_DIR="$test_root/data" \
LOCAL_GATEWAY_STATE_DIR="$test_root/state" \
LOCAL_GATEWAY_CACHE_DIR="$test_root/cache" \
LOCAL_GATEWAY_PROTOCOL_DIR="$test_root/protocols" \
LOCAL_GATEWAY_PORT="$gateway_port" \
GIN_MODE=release \
"$project_root/gateway/localrouter" --log-dir "$test_root/logs" >"$test_root/gateway.log" 2>&1 &
gateway_pid=$!

for _ in $(seq 1 120); do
  upstream_ready=false
  gateway_ready=false
  if curl --fail --silent "http://127.0.0.1:${upstream_port}/health" >/dev/null; then upstream_ready=true; fi
  if curl --fail --silent "http://127.0.0.1:${gateway_port}/healthz" >/dev/null; then gateway_ready=true; fi
  if [[ "$upstream_ready" == true && "$gateway_ready" == true ]]; then break; fi
  if ! kill -0 "$upstream_pid" 2>/dev/null || ! kill -0 "$gateway_pid" 2>/dev/null; then
    sed -n '1,160p' "$test_root/upstream.log" >&2
    sed -n '1,240p' "$test_root/gateway.log" >&2
    exit 1
  fi
  sleep 0.25
done

sed 's/^/Authorization: Bearer /' "$test_root/data/api-token" >"$test_root/api-header"
sed 's/^/X-Local-Admin: /' "$test_root/data/admin-token" >"$test_root/admin-header"
chmod 0600 "$test_root/api-header"
chmod 0600 "$test_root/admin-header"

curl --fail --silent --show-error \
  --request POST \
  --header @"$test_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/validate" >"$test_root/lifecycle-validate.json"
jq -e '.success == true and (.data.digest | test("^[0-9a-f]{64}$")) and any(.data.protocols[]; .id == "search" and .pool_mode == "local") and any(.data.protocols[]; .id == "video" and .pool_mode == "local" and .workflows == 1)' "$test_root/lifecycle-validate.json" >/dev/null

curl --fail --silent --show-error \
  --request POST \
  --header @"$test_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/plan" >"$test_root/lifecycle-plan.json"
lifecycle_digest="$(jq -r '.data.digest' "$test_root/lifecycle-plan.json")"
test "${#lifecycle_digest}" = 64

jq -nc --arg digest "$lifecycle_digest" '{digest:$digest}' >"$test_root/lifecycle-apply-request.json"
curl --fail --silent --show-error \
  --request POST \
  --header @"$test_root/admin-header" \
  --header 'Content-Type: application/json' \
  --data @"$test_root/lifecycle-apply-request.json" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/apply" >"$test_root/lifecycle-apply.json"
jq -e --arg digest "$lifecycle_digest" '.success == true and .data.digest == $digest and (.data.receipt | startswith("PAP-"))' "$test_root/lifecycle-apply.json" >/dev/null

curl --fail --silent --show-error \
  --header @"$test_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/history" >"$test_root/lifecycle-history.json"
jq -e --arg digest "$lifecycle_digest" '.success == true and .live_digest == $digest and any(.data[]; .digest == $digest)' "$test_root/lifecycle-history.json" >/dev/null

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/protocols" >"$test_root/docs.json"
jq -e '.object == "protocol.list" and any(.data[]; .id == "search" and .ready == true and .mount == "/p/search")' "$test_root/docs.json" >/dev/null
jq -e 'any(.data[]; .id == "video" and .pool.mode == "local" and .pool.source == "external-readonly" and .pool.total == 2 and .pool.eligible == 2)' "$test_root/docs.json" >/dev/null
! grep -q 'base_url\|secret_file\|fixture-value-only' "$test_root/docs.json"

curl --fail --silent "http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" >"$test_root/discovery.json"
jq -e '.schema_version == "1" and (.contract.digest | test("^[0-9a-f]{64}$")) and .contract.schema_version == "9" and (.topology.digest | test("^[0-9a-f]{64}$")) and .topology.schema_version == "9" and .pack_model.unit == "service-pack" and (.surfaces | length) == 4 and (.compatibility_packs | type) == "array" and .agent.catalog == "/agent/operations" and .agent.resolve == "/agent/resolve" and .agent.compare == "/agent/compare" and .agent.selection_mode == "agent" and .agent.merged == false and .invocation.operation_id_is_url == false and .documentation.agent == "/docs/agent.json" and .documentation.html == "/docs" and .documentation.openapi == "/docs/openapi.json" and .documentation.pool_catalog == "/docs/pools/index.json" and .documentation.pool_guide == "/docs/pools/catalog.md" and any(.protocols[]; .id == "search" and .docs.markdown == "/docs/packs/search/guide.md" and (.mount as $mount | all(.routes[]; .call_url == ($mount + .path))))' "$test_root/discovery.json" >/dev/null
! grep -q 'base_url\|secret_file\|fixture-value-only' "$test_root/discovery.json"

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" resolve web.search >"$test_root/lr-resolve.json"
jq -e '.selection_mode == "agent" and .merged == false and .count == 1 and .matches[0].operation_key == "search.search" and .matches[0].ready == true and (.matches[0].capabilities | index("web.search") != null)' "$test_root/lr-resolve.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find model 'fixture model alpha for a coding agent' >"$test_root/lr-find-model.json"
jq -e '.object == "localrouter.model.search" and .domain == "model" and .count >= 1 and any(.matches[]; .model_key == "search:fixture-model-alpha" and .id == "fixture-model-alpha" and any(.compatible_operations[]; .operation_key == "search.chat.completions" and .operation_id == "chat.completions" and .methods == ["POST"] and .call_url == "/p/search/chat/completions")) and (.boundary | contains("does not configure OMP"))' "$test_root/lr-find-model.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find model 'fixture-model-alpha' >"$test_root/lr-find-model-exact.json"
jq -e '.object == "localrouter.model.search" and .match_mode == "exact" and .count == 1 and .matches[0].model_key == "search:fixture-model-alpha"' "$test_root/lr-find-model-exact.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find model --exact 'search:fixture-model-alpha' >"$test_root/lr-find-model-strict.json"
jq -e '.match_mode == "exact-only" and .count == 1 and .returned == 1 and .truncated == false and .matches[0].model_key == "search:fixture-model-alpha"' "$test_root/lr-find-model-strict.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find model --exact 'search:no-such-model' >"$test_root/lr-find-model-missing.json"
jq -e '.match_mode == "exact-only" and .count == 0 and .returned == 0 and .matches == [] and (.exact_next_step | contains("no exact live model matched"))' "$test_root/lr-find-model-missing.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find pool search >"$test_root/lr-find-pool.json"
jq -e '.object == "localrouter.pool.search" and .domain == "pool" and .count >= 1 and any(.matches[]; .pool_id == "search" and .integrated == true and .ready == true)' "$test_root/lr-find-pool.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" find 'fixture model alpha for a coding agent' >"$test_root/lr-find-all.json"
jq -e '.object == "localrouter.resource.search" and .no_match == false and .domains.model.matches[0].model_key == "search:fixture-model-alpha" and (.next_actions | length) == 4' "$test_root/lr-find-all.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" catalog search >"$test_root/lr-catalog.json"
jq -e '.object == "localrouter.operation.list" and .merged == false and .count > 0 and all(.operations[]; .pack == "search" and (.operation_key | startswith("search.")))' "$test_root/lr-catalog.json" >/dev/null
jq -e '.returned == (.operations | length) and .truncated == false and (.next_action | contains("lr describe"))' "$test_root/lr-catalog.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" catalog --limit 1 >"$test_root/lr-catalog-limited.json"
jq -e '.count > 1 and .returned == 1 and .truncated == true and (.next_action | contains("--all"))' "$test_root/lr-catalog-limited.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" describe search search >"$test_root/lr-describe.json"
jq -e '.pack == "search" and .operation == "search" and .call.authenticated == true and .operation_id_is_url == false' "$test_root/lr-describe.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" describe search search.search >"$test_root/lr-describe-operation-key.json"
jq -e '.operation_key == "search.search" and .operation_id == "search"' "$test_root/lr-describe-operation-key.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" preflight search search '{"query":"e2e","numResults":5}' >"$test_root/lr-preflight.json"
jq -e '.success == true and .ok == true and .code == null and .retryable == false and .upstream_called == false and all(.checks[] | select(.blocking == true); .status != "fail")' "$test_root/lr-preflight.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" preflight search search.search '{"query":"e2e","numResults":5}' >"$test_root/lr-preflight-operation-key.json"
jq -e '.ok == true and .operation.operation_key == "search.search"' "$test_root/lr-preflight-operation-key.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" call search search.search '{"query":"operation-key-call"}' >"$test_root/lr-call-operation-key.json"
jq -e '.ok == true and .query == "operation-key-call"' "$test_root/lr-call-operation-key.json" >/dev/null

if env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" preflight video jobs.get '{}' >"$test_root/lr-preflight-blocked.json"; then
  echo "blocked preflight unexpectedly exited zero" >&2
  exit 1
fi
jq -e '.success == false and .ok == false and .code == "preflight_blocked" and .retryable == false and .upstream_called == false and (.alternatives | type) == "array" and any(.checks[]; .blocking == true and .status == "fail")' "$test_root/lr-preflight-blocked.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" whoami >"$test_root/lr-whoami.json"
jq -e '.service_access == true and .maintenance_access == false' "$test_root/lr-whoami.json" >/dev/null
! grep -q "$(tr -d '\n' <"$test_root/data/api-token")" "$test_root/lr-whoami.json"

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/pools/index.json" >"$test_root/pool-catalog.json"
jq -e '.schema_version == "1" and .summary.indexed == 0 and (.pools | length == 0)' "$test_root/pool-catalog.json" >/dev/null
! grep -Eq 'api_key|password|cookies|secret_file|/home/[^/]+/' "$test_root/pool-catalog.json"

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/pools/catalog.md" >"$test_root/pool-catalog.md"
grep -q 'LocalRouter 号池目录' "$test_root/pool-catalog.md"
grep -q 'external-readonly.*source' "$test_root/pool-catalog.md"

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/index.json" >"$test_root/index.json"
jq -e '.object == "protocol.list" and any(.data[]; .id == "search" and any(.guides[]; .id == "quickstart" and .status == "verified"))' "$test_root/index.json" >/dev/null

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/openapi.json" >"$test_root/openapi.json"
jq -e '.openapi == "3.1.0" and .paths["/p/search/search"].post.operationId == "search.search" and .paths["/p/search/search"].post["x-localrouter-call-url"] == "/p/search/search" and .paths["/p/search/search"].post["x-localrouter-operation-id-is-url"] == false and .paths["/w/video/video.generate"].post.operationId == "video.workflow.video.generate.create" and .components.securitySchemes.LocalRouterBearer.scheme == "bearer"' "$test_root/openapi.json" >/dev/null

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/packs/search/manifest.json" >"$test_root/manifest.json"
jq -e '.id == "search" and any(.routes[]; .operation_id == "search") and .docs.examples == "/docs/packs/search/examples.json"' "$test_root/manifest.json" >/dev/null

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/packs/search/examples.json" >"$test_root/examples.json"
jq -e '.object == "protocol.examples" and any(.data[]; .operation_id == "search" and .operation_id_is_url == false and .call_url == "/p/search/search" and .call.url == .call_url and (has("url") | not))' "$test_root/examples.json" >/dev/null

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/packs/search/guide.md" >"$test_root/guide.md"
grep -q 'Fixture search quickstart' "$test_root/guide.md"
grep -q '/docs/packs/search/manifest.json' "$test_root/guide.md"

alias_status="$(curl --silent --output /dev/null --write-out '%{http_code}:%{redirect_url}' "http://127.0.0.1:${gateway_port}/doc")"
test "$alias_status" = "308:http://127.0.0.1:${gateway_port}/docs"

unauthorized="$(curl --silent --output /dev/null --write-out '%{http_code}' -X POST "http://127.0.0.1:${gateway_port}/p/search/search")"
test "$unauthorized" = "401"

jq -n '{query:"custom protocol acceptance"}' >"$test_root/search.json"
curl --fail --silent --show-error \
  --header @"$test_root/api-header" \
  --header 'X-Client-Authorization: do-not-forward' \
  --header 'Content-Type: application/json' \
  --data @"$test_root/search.json" \
  "http://127.0.0.1:${gateway_port}/p/search/search?mode=template" >"$test_root/search-response.json"
if ! jq -e '.ok == true and .query == "custom protocol acceptance" and .mode == "template" and .client_authorization_forwarded == false' "$test_root/search-response.json" >/dev/null; then
  jq -c . "$test_root/search-response.json" >&2
  exit 1
fi

curl --fail --silent --show-error --no-buffer \
  --header @"$test_root/api-header" \
  --header 'Content-Type: application/json' \
  --data '{"query":"stream"}' \
  "http://127.0.0.1:${gateway_port}/p/search/answer" >"$test_root/stream.txt"
grep -q 'protocol-stream-ok' "$test_root/stream.txt"
grep -q 'data: \[DONE\]' "$test_root/stream.txt"

blocked="$(curl --silent --output /dev/null --write-out '%{http_code}' --header @"$test_root/api-header" "http://127.0.0.1:${gateway_port}/p/search/internal/admin")"
test "$blocked" = "405"

curl --fail --silent --show-error \
  --header @"$test_root/api-header" \
  --header 'Content-Type: application/json' \
  --data '{"prompt":"e2e video","private":"drop"}' \
  "http://127.0.0.1:${gateway_port}/w/video/video.generate" >"$test_root/workflow-create.json"
jq -e '.object == "workflow.job" and .data.state == "pending" and .data.resource_id == "job-1" and .create_response.job_id == "job-1"' "$test_root/workflow-create.json" >/dev/null
workflow_job="$(jq -r '.data.id' "$test_root/workflow-create.json")"

sleep 0.3
curl --fail --silent --show-error --header @"$test_root/api-header" \
  "http://127.0.0.1:${gateway_port}/w/video/video.generate/${workflow_job}" >"$test_root/workflow-pending.json"
jq -e '.object == "workflow.job" and .data.state == "pending" and .data.upstream_state == "pending" and .data.attempts == 1' "$test_root/workflow-pending.json" >/dev/null

sleep 0.3
curl --fail --silent --show-error --header @"$test_root/api-header" \
  "http://127.0.0.1:${gateway_port}/w/video/video.generate/${workflow_job}" >"$test_root/workflow-done.json"
jq -e '.object == "workflow.job" and .data.state == "succeeded" and .data.result == "https://example.invalid/video.mp4" and .data.attempts == 2' "$test_root/workflow-done.json" >/dev/null

env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" \
  "$project_root/tools/lr" run video video.generate '{"prompt":"e2e video","private":"drop"}' >"$test_root/lr-workflow-create.json"
lr_workflow_job="$(jq -r '.data.id' "$test_root/lr-workflow-create.json")"
test -n "$lr_workflow_job"
env LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" LOCALROUTER_API_TOKEN_FILE="$test_root/data/api-token" LOCALROUTER_WATCH_INTERVAL_SECONDS=1 \
  "$project_root/tools/lr" watch video video.generate "$lr_workflow_job" 10 >"$test_root/lr-workflow-done.json"
jq -e '.object == "workflow.job" and .data.state == "succeeded" and .data.result == "https://example.invalid/video.mp4"' "$test_root/lr-workflow-done.json" >/dev/null

test -f "$test_root/state/protocol-state/video.json"
test -f "$test_root/state/workflow-state/video.json"
! grep -q 'fixture-account-a\|fixture-account-b' "$test_root/state/protocol-state/video.json" "$test_root/state/workflow-state/video.json"

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs" >"$test_root/docs.html"
grep -q 'Fixture Search Service' "$test_root/docs.html"
grep -q '/p/search/search' "$test_root/docs.html"

curl --fail --silent "http://127.0.0.1:${gateway_port}/docs/packs/search" >"$test_root/pack.html"
grep -q 'Fixture search quickstart' "$test_root/pack.html"
grep -q '/docs/packs/search/manifest.json' "$test_root/pack.html"

echo "custom protocol REST, SSE, transforms, external-readonly pool source, pooled retry, affinity, async workflow, hash-bound lifecycle, pool catalog, discovery, OpenAPI, and layered docs acceptance passed"
