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

if [[ -n "${LOCALROUTER_RELEASE_TREEISH:-}" ]]; then
  treeish="$LOCALROUTER_RELEASE_TREEISH"
  git -C "$project_root" cat-file -e "$treeish^{tree}"
else
  candidate_index="$release_root/.candidate-index"
  GIT_INDEX_FILE="$candidate_index" git -C "$project_root" read-tree HEAD
  GIT_INDEX_FILE="$candidate_index" git -C "$project_root" add -A -- .
  treeish="$(GIT_INDEX_FILE="$candidate_index" git -C "$project_root" write-tree)"
  find "$candidate_index" -maxdepth 0 -type f -delete
fi
git -C "$project_root" archive --format=tar "$treeish" | tar -xf - -C "$release_root"

git -C "$release_root" init --quiet -b main
git -C "$release_root" add -A

git -C "$release_root" diff --cached --check
python3 "$release_root/tests/open_source_release_test.py"
go run github.com/zricethezav/gitleaks/v8@v8.28.0 git --staged --no-banner --redact=100 "$release_root"
git -C "$release_root" -c user.name=LocalRouter -c user.email=release@local.invalid commit --quiet -m 'release candidate'

"$release_root/tests/verify.sh"
"$release_root/tests/docker_acceptance.sh"
git -C "$release_root" diff --exit-code
test -z "$(git -C "$release_root" status --short --untracked-files=all)"

(
  cd "$release_root/gateway"
  go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
)
go run github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.5.1 \
  scan source --lockfile "$release_root/gateway/web-src/bun.lock" \
  --format table --verbosity warn

(
  cd "$release_root"
  go run github.com/goreleaser/goreleaser/v2@v2.18.0 release --snapshot --clean
)
"$release_root/tests/release_artifact_acceptance.sh" "$release_root/dist"

echo "clean public-source release acceptance passed tree=$treeish source=worktree-or-explicit-treeish"
