#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
matrix_root="$(mktemp -d)"
gateway_pid=""
services_pid=""
stream_pid=""

fail() {
  printf 'universal service matrix: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  local exit_status=$?
  trap - EXIT
  if [[ -n "$stream_pid" ]] && kill -0 "$stream_pid" 2>/dev/null; then
    kill -TERM "$stream_pid" 2>/dev/null || true
    wait "$stream_pid" 2>/dev/null || true
  fi
  if [[ -n "$gateway_pid" ]] && kill -0 "$gateway_pid" 2>/dev/null; then
    kill -TERM "$gateway_pid" 2>/dev/null || true
    wait "$gateway_pid" 2>/dev/null || true
  fi
  if [[ -n "$services_pid" ]] && kill -0 "$services_pid" 2>/dev/null; then
    kill -TERM "$services_pid" 2>/dev/null || true
    wait "$services_pid" 2>/dev/null || true
  fi
  rm -rf -- "$matrix_root"
  exit "$exit_status"
}
trap cleanup EXIT

choose_port() {
  python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    print(listener.getsockname()[1])
PY
}

gateway_port="$(choose_port)"
upstream_port="$(choose_port)"
adapter_port="$(choose_port)"
while [[ "$gateway_port" == "$upstream_port" || "$gateway_port" == "$adapter_port" || "$upstream_port" == "$adapter_port" || "$gateway_port" == "8317" ]]; do
  gateway_port="$(choose_port)"
  upstream_port="$(choose_port)"
  adapter_port="$(choose_port)"
done

gateway_binary="$project_root/gateway/localrouter"
[[ -x "$gateway_binary" ]] || fail "build gateway/localrouter before running this acceptance test"

mkdir -p \
  "$matrix_root/config" \
  "$matrix_root/data/protocol-secrets" \
  "$matrix_root/data/protocol-pools" \
  "$matrix_root/state" \
  "$matrix_root/cache" \
  "$matrix_root/logs" \
  "$matrix_root/protocols/modelauth/guides"
chmod 0700 "$matrix_root/config" "$matrix_root/data" "$matrix_root/data/protocol-secrets" \
  "$matrix_root/data/protocol-pools" "$matrix_root/state" "$matrix_root/cache" "$matrix_root/logs"

printf '%s\n' 'matrix-header-placeholder' >"$matrix_root/data/protocol-secrets/modelauth"
printf '%s\n' 'matrix-adapter-placeholder' >"$matrix_root/data/protocol-secrets/adapterfx"
jq -n '{schema_version:"1",credentials:[
  {id:"matrix-a",secret:"matrix-pool-a-placeholder"},
  {id:"matrix-b",secret:"matrix-pool-b-placeholder"}
]}' >"$matrix_root/data/protocol-pools/asyncjobs.json"
chmod 0600 "$matrix_root/data/protocol-secrets/modelauth" \
  "$matrix_root/data/protocol-secrets/adapterfx" \
  "$matrix_root/data/protocol-pools/asyncjobs.json"
install -m 0644 "$project_root/tests/fixtures/universal-matrix/quickstart.md" \
  "$matrix_root/protocols/modelauth/guides/quickstart.md"

upstream_origin="http://127.0.0.1:${upstream_port}"
adapter_origin="http://127.0.0.1:${adapter_port}"
verified_at="2026-09-02T00:00:00Z"

jq -n --arg base "$upstream_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"restnone",name:"No-key JSON REST",description:"A public JSON REST service with no upstream credential.",enabled:true,
  base_url:$base,timeout_seconds:10,auth:{type:"none"},routes:[{
    operation_id:"service.status",capabilities:["service.status"],methods:["GET"],path:"/status",upstream_path:"/rest/status",summary:"Read public service status",retry:{mode:"safe"},
    availability:{status:"verified",level:"mock",covers:["schema","upstream","response"],verified_at:$verified_at}
  }]
}' >"$matrix_root/protocols/restnone.json"

jq -n --arg base "$upstream_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"modelauth",name:"Header-auth model service",description:"Header-auth JSON service with a live model catalogue.",enabled:true,
  base_url:$base,timeout_seconds:10,auth:{type:"header",header:"X-Matrix-Key",secret_file:"protocol-secrets/modelauth",value_template:"Token {{secret}}"},routes:[
    {operation_id:"models",capabilities:["ai.models"],methods:["GET"],path:"/models",upstream_path:"/catalog/models",summary:"List current models",retry:{mode:"safe"},availability:{status:"verified",level:"mock",covers:["schema","auth","upstream","response"],verified_at:$verified_at}},
    {operation_id:"chat.completions",capabilities:["ai.chat"],methods:["POST"],path:"/chat/completions",summary:"Create a model response",retry:{mode:"never"},
      request_example:{model:"shape-only",messages:[{role:"user",content:"hello"}]},
      request_schema:{type:"object",required:["model","messages"],properties:{model:{type:"string"},messages:{type:"array"}}},
      availability:{status:"verified",level:"mock",covers:["schema","auth","upstream","response","side-effects"],verified_at:$verified_at}}
  ]
}' >"$matrix_root/protocols/modelauth.json"

jq -n --arg base "$upstream_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"sseevents",name:"SSE event service",description:"Chunked server-sent events with a terminal marker.",enabled:true,
  base_url:$base,timeout_seconds:10,auth:{type:"none"},routes:[{
    operation_id:"events.stream",capabilities:["events.stream"],methods:["POST"],path:"/events",summary:"Stream events",streaming:true,retry:{mode:"never"},
    availability:{status:"verified",level:"mock",covers:["schema","upstream","response","stream"],verified_at:$verified_at}
  }]
}' >"$matrix_root/protocols/sseevents.json"

jq -n --arg base "$upstream_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"binary",name:"Opaque binary service",description:"Exact byte passthrough for non-JSON payloads and responses.",enabled:true,
  base_url:$base,timeout_seconds:10,auth:{type:"none"},routes:[{
    operation_id:"files.echo",capabilities:["files.binary"],methods:["POST"],path:"/files/echo",upstream_path:"/binary/echo",summary:"Echo opaque bytes",retry:{mode:"never"},
    availability:{status:"verified",level:"mock",covers:["schema","upstream","response"],verified_at:$verified_at}
  }]
}' >"$matrix_root/protocols/binary.json"

jq -n --arg base "$upstream_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"asyncjobs",name:"Pooled async job service",description:"Idempotent create and affinity-bound polling over two local credentials.",enabled:true,
  base_url:$base,timeout_seconds:10,auth:{type:"bearer"},pool:{mode:"local",credentials_file:"protocol-pools/asyncjobs.json",strategy:"round-robin",max_attempts:2,max_inflight_per_credential:1,rate_limit_cooldown_seconds:1,inflight_lease_seconds:30},routes:[
    {operation_id:"jobs.create",capabilities:["jobs.create"],methods:["POST"],path:"/jobs",upstream_path:"/async/jobs",summary:"Create an async job",retry:{mode:"idempotent",max_attempts:2,statuses:[429],idempotency_header:"Idempotency-Key"},affinity:{response_json_path:"job_id",ttl_seconds:3600},availability:{status:"verified",level:"mock",covers:["schema","auth","pool","upstream","response","side-effects"],verified_at:$verified_at}},
    {operation_id:"jobs.get",capabilities:["jobs.status"],methods:["GET"],path:"/jobs/{id}",upstream_path:"/async/jobs/{id}",summary:"Poll an async job",retry:{mode:"safe"},affinity:{request_path_param:"id",ttl_seconds:3600},availability:{status:"verified",level:"mock",covers:["schema","auth","pool","upstream","response"],verified_at:$verified_at}}
  ],workflows:[{
    id:"jobs.process",name:"Create and poll a job",create_operation:"jobs.create",poll_operation:"jobs.get",resource_id_path:"job_id",status_path:"status",result_path:"result.uri",pending_values:["pending"],running_values:["running"],success_values:["done"],failure_values:["failed"],poll_interval_ms:250,max_poll_attempts:10
  }]
}' >"$matrix_root/protocols/asyncjobs.json"

jq -n --arg base "$upstream_origin" --arg adapter "$adapter_origin" --arg verified_at "$verified_at" '{
  schema_version:"3",id:"adapterfx",name:"Loopback envelope adapter",description:"A fixed loopback sidecar owns a non-standard provider exchange.",enabled:true,
  base_url:$base,targets:{provider:$base,adapter:$adapter},timeout_seconds:10,
  auth:{type:"header",header:"X-Adapter-Key",secret_file:"protocol-secrets/adapterfx"},routes:[
    {operation_id:"adapter.echo",capabilities:["adapter.invoke"],methods:["POST"],path:"/adapter/echo",upstream_path:"/adapter-native/invoke",target:"provider",transport:"adapter",summary:"Invoke the fixed adapter",adapter:{type:"http-envelope",target:"adapter",path:"/invoke"},retry:{mode:"never"},availability:{status:"verified",level:"mock",covers:["schema","auth","upstream","response","side-effects"],verified_at:$verified_at}},
    {operation_id:"adapter.unknown",capabilities:["adapter.unknown"],methods:["POST"],path:"/adapter/unknown",upstream_path:"/adapter-native/invoke",target:"provider",transport:"adapter",summary:"Return an explicit unknown outcome",adapter:{type:"http-envelope",target:"adapter",path:"/invoke"},retry:{mode:"never",unknown_outcome_status:521},availability:{status:"verified",level:"mock",covers:["schema","auth","upstream","response","side-effects"],verified_at:$verified_at}}
  ]
}' >"$matrix_root/protocols/adapterfx.json"

python3 "$project_root/tests/fixtures/universal-matrix/mock_services.py" "$upstream_port" "$adapter_port" \
  >"$matrix_root/services.log" 2>&1 &
services_pid=$!

LOCAL_GATEWAY_CONFIG_DIR="$matrix_root/config" \
LOCAL_GATEWAY_DATA_DIR="$matrix_root/data" \
LOCAL_GATEWAY_STATE_DIR="$matrix_root/state" \
LOCAL_GATEWAY_CACHE_DIR="$matrix_root/cache" \
LOCAL_GATEWAY_PROTOCOL_DIR="$matrix_root/protocols" \
LOCAL_GATEWAY_PORT="$gateway_port" \
GIN_MODE=release \
"$gateway_binary" --log-dir "$matrix_root/logs" >"$matrix_root/gateway.log" 2>&1 &
gateway_pid=$!

ready=false
for _ in $(seq 1 120); do
  if curl --noproxy '*' --fail --silent "${upstream_origin}/healthz" >/dev/null \
      && curl --noproxy '*' --fail --silent "${adapter_origin}/healthz" >/dev/null \
      && curl --noproxy '*' --fail --silent "http://127.0.0.1:${gateway_port}/healthz" >/dev/null; then
    ready=true
    break
  fi
  if ! kill -0 "$services_pid" 2>/dev/null || ! kill -0 "$gateway_pid" 2>/dev/null; then
    sed -n '1,160p' "$matrix_root/services.log" >&2
    sed -n '1,240p' "$matrix_root/gateway.log" >&2
    fail "isolated listener exited during startup"
  fi
  sleep 0.1
done
[[ "$ready" == true ]] || fail "isolated listeners did not become ready"

sed 's/^/Authorization: Bearer /' "$matrix_root/data/api-token" >"$matrix_root/api-header"
sed 's/^/X-Local-Admin: /' "$matrix_root/data/admin-token" >"$matrix_root/admin-header"
chmod 0600 "$matrix_root/api-header" "$matrix_root/admin-header"

curl --noproxy '*' --fail --silent --show-error --request POST --header @"$matrix_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/validate" >"$matrix_root/validate.json"
jq -e '.success == true and (.data.protocols | length) == 6' "$matrix_root/validate.json" >/dev/null
curl --noproxy '*' --fail --silent --show-error --request POST --header @"$matrix_root/admin-header" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/plan" >"$matrix_root/plan.json"
digest="$(jq -r '.data.digest' "$matrix_root/plan.json")"
[[ "$digest" =~ ^[0-9a-f]{64}$ ]] || fail "plan did not return an exact digest"
jq -nc --arg digest "$digest" '{digest:$digest}' >"$matrix_root/apply-request.json"
curl --noproxy '*' --fail --silent --show-error --request POST --header @"$matrix_root/admin-header" \
  --header 'Content-Type: application/json' --data @"$matrix_root/apply-request.json" \
  "http://127.0.0.1:${gateway_port}/local/api/protocols/apply" >"$matrix_root/apply.json"
jq -e --arg digest "$digest" '.success == true and .data.digest == $digest' "$matrix_root/apply.json" >/dev/null

lr() {
  env \
    LOCALROUTER_BASE_URL="http://127.0.0.1:${gateway_port}" \
    LOCALROUTER_DISCOVERY_URL="http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" \
    LOCALROUTER_API_TOKEN_FILE="$matrix_root/data/api-token" \
    "$project_root/tools/lr" "$@"
}

curl --noproxy '*' --fail --silent "http://127.0.0.1:${gateway_port}/.well-known/localrouter.json" >"$matrix_root/discovery.json"
jq -e --arg digest "$digest" '
  .contract.digest == $digest and (.protocols | length) == 6
  and all(.protocols[]; .ready == true)
  and any(.protocols[]; .id == "modelauth" and any(.routes[]; .operation_id == "chat.completions" and .operation_id_is_url == false and .call_url == "/p/modelauth/chat/completions"))
' "$matrix_root/discovery.json" >/dev/null

lr call restnone service.status '{}' >"$matrix_root/rest.json"
jq -e '.service == "plain-rest" and .status == "ok" and .upstream_auth_absent == true' "$matrix_root/rest.json" >/dev/null

lr find model matrix-text-v1 >"$matrix_root/models-find.json"
jq -e '
  .object == "localrouter.model.search" and .count == 1
  and .matches[0].model_key == "modelauth:matrix-text-v1"
  and .matches[0].compatible_operations == [{operation_key:"modelauth.chat.completions",operation_id:"chat.completions",methods:["POST"],call_url:"/p/modelauth/chat/completions",summary:"Create a model response"}]
' "$matrix_root/models-find.json" >/dev/null
lr describe modelauth chat.completions >"$matrix_root/chat-describe.json"
jq -e '
  .operation_id == "chat.completions" and .operation_id_is_url == false
  and .call_url == "/p/modelauth/chat/completions"
  and .dynamic_inputs.model.source_operation_key == "modelauth.models"
  and .dynamic_inputs.model.source_call_url == "/p/modelauth/models"
' "$matrix_root/chat-describe.json" >/dev/null
lr preflight modelauth chat.completions '{"model":"matrix-text-v1","messages":[]}' >"$matrix_root/chat-preflight.json"
jq -e '.ok == true and .upstream_called == false' "$matrix_root/chat-preflight.json" >/dev/null
lr call modelauth chat.completions '{"model":"matrix-text-v1","messages":[{"role":"user","content":"matrix"}]}' >"$matrix_root/chat.json"
jq -e '.model == "matrix-text-v1" and .choices[0].message.content == "matrix-chat-ok"' "$matrix_root/chat.json" >/dev/null
wrong_path_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' \
  --request POST --header @"$matrix_root/api-header" --header 'Content-Type: application/json' --data '{}' \
  "http://127.0.0.1:${gateway_port}/p/modelauth/chat.completions")"
[[ "$wrong_path_status" == "405" ]] || fail "semantic dotted operation ID was incorrectly accepted as a URL"

curl --noproxy '*' --fail --silent --show-error --no-buffer --request POST \
  --header @"$matrix_root/api-header" --header 'Content-Type: application/json' --data '{"topic":"matrix"}' \
  "http://127.0.0.1:${gateway_port}/p/sseevents/events" >"$matrix_root/stream.txt" &
stream_pid=$!
first_chunk_seen=false
for _ in $(seq 1 8); do
  if grep -q 'matrix-stream-first' "$matrix_root/stream.txt" 2>/dev/null; then
    first_chunk_seen=true
    break
  fi
  sleep 0.1
done
[[ "$first_chunk_seen" == true ]] || fail "SSE first chunk was buffered"
! grep -q 'data: \[DONE\]' "$matrix_root/stream.txt" || fail "SSE terminal marker arrived before the fixture delay"
wait "$stream_pid"
stream_pid=""
grep -q 'data: \[DONE\]' "$matrix_root/stream.txt"

dd if=/dev/zero of="$matrix_root/binary.in" bs=65536 count=16 status=none
printf '\x00matrix-binary-tail\xff' >>"$matrix_root/binary.in"
binary_call_url="$(jq -r '.protocols[] | select(.id == "binary") | .routes[] | select(.operation_id == "files.echo") | .call_url' "$matrix_root/discovery.json")"
[[ "$binary_call_url" == "/p/binary/files/echo" ]] || fail "binary call_url mismatch"
curl --noproxy '*' --fail --silent --show-error --request POST --header @"$matrix_root/api-header" \
  --header 'Content-Type: application/octet-stream' --data-binary @"$matrix_root/binary.in" \
  "http://127.0.0.1:${gateway_port}${binary_call_url}" >"$matrix_root/binary.out"
cmp "$matrix_root/binary.in" "$matrix_root/binary.out"

lr run asyncjobs jobs.process '{"task":"matrix"}' >"$matrix_root/workflow-created.json"
workflow_job="$(jq -r '.data.id' "$matrix_root/workflow-created.json")"
[[ "$workflow_job" =~ ^job-[a-zA-Z0-9_-]{8,128}$ ]] || fail "workflow did not return a resumable job ID"
LOCALROUTER_WATCH_INTERVAL_SECONDS=1 lr watch asyncjobs jobs.process "$workflow_job" 10 >"$matrix_root/workflow-finished.json"
jq -e '.data.state == "succeeded" and .data.result == "matrix://job/matrix-job-1/result"' "$matrix_root/workflow-finished.json" >/dev/null

lr call adapterfx adapter.echo '{"target_url":"http://attacker.invalid/override","payload":"matrix"}' >"$matrix_root/adapter.json"
jq -e '.adapter == "ok" and .fixed_target == true and .auth == true and .untrusted_target_was_data == true' "$matrix_root/adapter.json" >/dev/null
lr describe adapterfx adapter.unknown >"$matrix_root/adapter-unknown-describe.json"
jq -e '.transport == "adapter" and .retry.mode == "never" and .retry.unknown_outcome_status == 521' "$matrix_root/adapter-unknown-describe.json" >/dev/null
set +e
lr call adapterfx adapter.unknown '{"side_effect":"ambiguous"}' >"$matrix_root/unknown.json" 2>"$matrix_root/unknown.err"
unknown_exit=$?
set -e
[[ "$unknown_exit" -ne 0 ]] || fail "unknown adapter outcome unexpectedly returned success"
jq -e '
  .success == false and .code == "upstream_outcome_unknown"
  and .message == "adapter provider outcome is unknown"
  and .reason == "the adapter explicitly reported that the provider outcome is unknown"
  and .owner == "provider" and .outcome == "unknown" and .retryable == false
  and (.next_action | contains("do not replay blindly"))
  and .operation_id == "adapter.unknown"
' "$matrix_root/unknown.json" >/dev/null

curl --noproxy '*' --fail --silent "${upstream_origin}/__matrix/state" >"$matrix_root/mock-state.json"
jq -e '
  .rest_calls == 1 and .header_auth_ok == true
  and .consumer_auth_leaked == false and .client_secret_leaked == false
  and .binary_calls == 1 and .stream_calls == 1
  and .create_attempts == 2 and .credential_order_ok == true and .idempotency_stable == true
  and .poll_attempts >= 2 and .affinity_ok == true
  and .adapter_echo_calls == 1 and .adapter_unknown_calls == 1
  and .adapter_fixed_target_ok == true and .adapter_auth_ok == true
' "$matrix_root/mock-state.json" >/dev/null

lr whoami >"$matrix_root/whoami.json"
jq -e '.service_access == true and .maintenance_access == false' "$matrix_root/whoami.json" >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:${gateway_port}/docs/packs/modelauth/manifest.json" >"$matrix_root/manifest.json"
curl --noproxy '*' --fail --silent "http://127.0.0.1:${gateway_port}/docs/openapi.json" >"$matrix_root/openapi.json"
lr docs modelauth >"$matrix_root/guide.md"
jq -e '
  .id == "modelauth" and any(.routes[]; .operation_id == "chat.completions" and .call_url == "/p/modelauth/chat/completions")
' "$matrix_root/manifest.json" >/dev/null
jq -e '
  .openapi == "3.1.0"
  and .paths["/p/modelauth/chat/completions"].post.operationId == "modelauth.chat.completions"
  and .paths["/p/sseevents/events"].post["x-localrouter-streaming"] == true
  and .paths["/w/asyncjobs/jobs.process"].post.operationId == "asyncjobs.workflow.jobs.process.create"
' "$matrix_root/openapi.json" >/dev/null
grep -q 'The semantic operation ID is not a URL' "$matrix_root/guide.md"

[[ "$(stat -c '%a' "$matrix_root/data/api-token")" == "600" ]]
[[ "$(stat -c '%a' "$matrix_root/data/admin-token")" == "600" ]]
[[ "$(stat -c '%a' "$matrix_root/data/protocol-secrets/modelauth")" == "600" ]]
[[ "$(stat -c '%a' "$matrix_root/data/protocol-pools/asyncjobs.json")" == "600" ]]
ss -ltn | grep -Eq "127\.0\.0\.1:${gateway_port}[[:space:]]" || fail "gateway is not bound to IPv4 loopback"
if grep -Fq 'matrix-header-placeholder' "$matrix_root/discovery.json" "$matrix_root/manifest.json" "$matrix_root/openapi.json" "$matrix_root/guide.md" "$matrix_root/chat-describe.json" "$matrix_root/chat-preflight.json" "$matrix_root/gateway.log"; then
  fail "header credential leaked into a public or diagnostic surface"
fi
if grep -Fq 'matrix-pool-' "$matrix_root/discovery.json" "$matrix_root/manifest.json" "$matrix_root/openapi.json" "$matrix_root/guide.md" "$matrix_root/workflow-created.json" "$matrix_root/workflow-finished.json" "$matrix_root/gateway.log"; then
  fail "pool credential leaked into a public, workflow, or diagnostic surface"
fi
if grep -Fq 'matrix-adapter-placeholder' "$matrix_root/discovery.json" "$matrix_root/manifest.json" "$matrix_root/openapi.json" "$matrix_root/guide.md" "$matrix_root/adapter.json" "$matrix_root/unknown.json" "$matrix_root/gateway.log"; then
  fail "adapter credential leaked into a public or diagnostic surface"
fi
if grep -R -Fq 'matrix-pool-' "$matrix_root/state"; then
  fail "pool credential leaked into LocalRouter state"
fi

printf 'universal service matrix passed: packs=6 rest=pass header-model=pass sse=pass binary=pass workflow-affinity=pass adapter=pass digest=%s\n' "$digest"
