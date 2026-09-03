#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
base_url="${LOCALROUTER_BASE_URL:-http://127.0.0.1:8317}"
xdg_data_home="${XDG_DATA_HOME:-${HOME:?HOME is required}/.local/share}"
[[ "$xdg_data_home" = /* ]] || xdg_data_home="$HOME/.local/share"
admin_token_file="${LOCALROUTER_ADMIN_TOKEN_FILE:-$xdg_data_home/localrouter/admin-token}"

usage() {
  cat >&2 <<'EOF'
usage: localrouter-protocols {validate|plan|apply DIGEST|history|rollback DIGEST|pool-reset PACK [CREDENTIAL]|verify-live|verify-lifecycle}

This is a compatibility helper for an explicitly delegated local operator. Agent
Pack work should use `lr manage-status`, `lr manage-list`, and `lr manage-call`
to create, review, plan, and apply a semantic draft. Neither workflow may print
or copy credential values.
EOF
}

command="${1:-help}"
case "$command" in
  help|-h|--help)
    usage
    exit 0
    ;;
esac

if [[ ! -r "$admin_token_file" ]]; then
  echo "LocalRouter admin token file is not readable: $admin_token_file" >&2
  exit 1
fi

admin_token="$(<"$admin_token_file")"

request() {
  local method="$1"
  local path="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl --fail-with-body --silent --show-error \
      --request "$method" \
      --header "X-Local-Admin: $admin_token" \
      --header 'Content-Type: application/json' \
      --data "$body" \
      "$base_url$path"
  else
    curl --fail-with-body --silent --show-error \
      --request "$method" \
      --header "X-Local-Admin: $admin_token" \
      "$base_url$path"
  fi
}

case "$command" in
  validate)
    request POST /local/api/protocols/validate | jq .
    ;;
  plan)
    request POST /local/api/protocols/plan | jq .
    ;;
  apply)
    digest="${2:-}"
    [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || { echo "apply requires the exact reviewed 64-character digest" >&2; exit 2; }
    request POST /local/api/protocols/apply "$(jq -nc --arg digest "$digest" '{digest:$digest}')" | jq .
    ;;
  history)
    request GET /local/api/protocols/history | jq .
    ;;
  rollback)
    digest="${2:-}"
    [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || { echo "rollback requires a known 64-character revision digest" >&2; exit 2; }
    request POST /local/api/protocols/rollback "$(jq -nc --arg digest "$digest" '{digest:$digest}')" | jq .
    ;;
  pool-reset)
    pack="${2:-}"
    credential="${3:-}"
    [[ "$pack" =~ ^[a-z][a-z0-9-]{1,31}$ ]] || { echo "pool-reset requires a pack id" >&2; exit 2; }
    request POST "/local/api/protocols/$pack/pool/reset" "$(jq -nc --arg credential "$credential" '{credential_id:$credential}')" | jq .
    ;;
  verify-live)
    LOCALROUTER_BASE_URL="$base_url" "$project_root/tests/live_docs_acceptance.sh"
    ;;
  verify-lifecycle)
    LOCALROUTER_BASE_URL="$base_url" LOCALROUTER_ADMIN_TOKEN_FILE="$admin_token_file" \
      "$project_root/tests/live_lifecycle_acceptance.sh"
    ;;
  *)
    usage
    exit 2
    ;;
esac

unset admin_token
