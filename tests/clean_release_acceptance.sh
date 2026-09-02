#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
release_root="$(mktemp -d /tmp/localrouter-release.XXXXXX)"
case "$release_root" in
  /tmp/localrouter-release.*) ;;
  *) echo "unsafe temporary release path" >&2; exit 2 ;;
esac
cleanup() { find "$release_root" -depth -delete; }
trap cleanup EXIT

treeish="${LOCALROUTER_RELEASE_TREEISH:-$(git -C "$project_root" write-tree)}"
git -C "$project_root" archive --format=tar "$treeish" | tar -xf - -C "$release_root"

git -C "$release_root" init --quiet -b main
git -C "$release_root" add -A

bun install --cwd "$release_root/gateway/web-src" --frozen-lockfile
bun run --cwd "$release_root/gateway/web-src" typecheck
bun run --cwd "$release_root/gateway/web-src" test
bun run --cwd "$release_root/gateway/web-src" build
git -C "$release_root" add -A

git -C "$release_root" diff --cached --check
python3 "$release_root/tests/open_source_release_test.py"
"$release_root/tests/verify.sh"

gitleaks git --staged --no-banner --redact=100 "$release_root"
(
  cd "$release_root/gateway"
  go run golang.org/x/vuln/cmd/govulncheck@latest ./...
)
go run github.com/google/osv-scanner/v2/cmd/osv-scanner@latest \
  scan source --lockfile "$release_root/gateway/web-src/bun.lock" \
  --format table --verbosity warn

echo "clean public-source release acceptance passed tree=$treeish"
