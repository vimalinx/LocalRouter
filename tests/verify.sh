#!/usr/bin/env bash
set -euo pipefail

unset LOCALROUTER_BASE_URL LOCALROUTER_DISCOVERY_URL LOCALROUTER_DOCS_URL \
  LOCALROUTER_OPENAPI_URL LOCALROUTER_MCP_URL LOCALROUTER_MAINTENANCE_MCP_URL

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

bun install --cwd "$project_root/gateway/web-src" --frozen-lockfile
bun run --cwd "$project_root/gateway/web-src" typecheck
bun run --cwd "$project_root/gateway/web-src" test
bun run --cwd "$project_root/gateway/web-src" build
go -C "$project_root/gateway" test -count=1 ./...
go -C "$project_root/gateway" test -race -count=1 ./...
go -C "$project_root/gateway" vet ./...
go -C "$project_root/gateway" build -trimpath -o localrouter .
bash -n "$project_root/tools/protocol-pack-lifecycle.sh" \
  "$project_root/tools/lr" \
  "$project_root/tools/install-localrouter.sh" \
  "$project_root/tests/clean_release_acceptance.sh" \
	"$project_root/tests/docker_acceptance.sh" \
  "$project_root/tests/release_artifact_acceptance.sh" \
  "$project_root/tests/xdg_install_acceptance.sh" \
	"$project_root/tests/lan_service_acceptance.sh" \
  "$project_root/tests/protocol_e2e.sh"
python3 "$project_root/tests/service_workspace_acceptance.py"
"$project_root/tests/agent_skill_acceptance.sh"
"$project_root/tests/smoke_local_gateway.sh"
"$project_root/tests/lan_service_acceptance.sh"
"$project_root/tests/e2e_relay.sh"
"$project_root/tests/protocol_e2e.sh"
"$project_root/tests/universal_service_matrix_acceptance.sh"
"$project_root/tests/xdg_install_acceptance.sh"
python3 "$project_root/tests/open_source_release_test.py"

test "$(readlink "$project_root/gateway/LICENSE")" = "../LICENSE"
! go -C "$project_root/gateway" list -deps ./... | grep -q 'QuantumNous/new-api'
! find "$project_root" -maxdepth 2 -type d -path "$project_root/upstream/*" -print -quit | grep -q .

echo "LocalRouter full verification passed"
