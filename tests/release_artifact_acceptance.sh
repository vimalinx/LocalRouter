#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
artifact_root="${1:-$project_root/dist}"
[[ "$artifact_root" = /* ]] || artifact_root="$project_root/$artifact_root"
[[ -d "$artifact_root" ]] || { echo "release artifact directory is missing: $artifact_root" >&2; exit 1; }
[[ -f "$artifact_root/checksums.txt" ]] || { echo "release checksums are missing" >&2; exit 1; }

(cd "$artifact_root" && sha256sum --check checksums.txt)

mapfile -t archives < <(find "$artifact_root" -maxdepth 1 -type f -name 'localrouter_*_linux_*.tar.gz' -print | sort)
test "${#archives[@]}" -eq 2

amd64_archive=""
arm64_archive=""
for archive in "${archives[@]}"; do
  case "$(basename -- "$archive")" in
    *_linux_amd64.tar.gz) amd64_archive="$archive" ;;
    *_linux_arm64.tar.gz) arm64_archive="$archive" ;;
    *) echo "unexpected release archive: $archive" >&2; exit 1 ;;
  esac
done
[[ -n "$amd64_archive" && -n "$arm64_archive" ]] || { echo "both amd64 and arm64 archives are required" >&2; exit 1; }

acceptance_root="$(mktemp -d /tmp/localrouter-artifacts.XXXXXX)"
case "$acceptance_root" in
  /tmp/localrouter-artifacts.*) ;;
  *) echo "unsafe temporary artifact path" >&2; exit 2 ;;
esac
cleanup() { find "$acceptance_root" -depth -delete; }
trap cleanup EXIT

required_files=(
  README.md CHANGELOG.md LICENSE NOTICE PROVENANCE.md SECURITY.md
  CONTRIBUTING.md THIRD-PARTY-LICENSES.md VERSION
  packaging/agent-contract/AGENTS.localrouter.md
  packaging/localrouter.env.example packaging/systemd/localrouter.service.in
  tools/lr tools/protocol-pack-lifecycle.sh tools/install-localrouter.sh
  .agents/skills/localrouter-protocol-pack/SKILL.md
  .agents/skills/localrouter-protocol-pack/references/runtime-handoff.md
)

inspect_archive() {
  local archive="$1"
  local expected_machine="$2"
  local label destination top listing binary_description
  label="$(basename -- "$archive" .tar.gz)"
  destination="$acceptance_root/$label"
  install -d -m 0700 "$destination"
  listing="$(tar -tzf "$archive")"
  ! grep -Eq '(^/|(^|/)\.\.(/|$))' <<<"$listing"
  top="$(sed -n '1{s|/.*||;p;}' <<<"$listing")"
  [[ -n "$top" ]] || { echo "archive has no wrapping directory: $archive" >&2; exit 1; }
  for required in "${required_files[@]}" localrouter; do
    grep -Fxq "$top/$required" <<<"$listing" || {
      echo "archive is missing $required: $archive" >&2
      exit 1
    }
  done
  ! grep -Eq '(^|/)(gateway/data|gateway/logs|protocol-drafts|protocol-revisions|node_modules)(/|$)' <<<"$listing"
  tar -xzf "$archive" --no-same-owner -C "$destination"
  binary_description="$(file -b "$destination/$top/localrouter")"
  grep -Fq "$expected_machine" <<<"$binary_description" || {
    echo "unexpected executable architecture: $binary_description" >&2
    exit 1
  }
  printf '%s\n' "$destination/$top"
}

amd64_root="$(inspect_archive "$amd64_archive" 'x86-64')"
inspect_archive "$arm64_archive" 'ARM aarch64' >/dev/null

"$amd64_root/localrouter" version | grep -Fq 'localrouter '
"$amd64_root/tools/lr" help | grep -Fq 'lr runtime-openai <pack> <model>'

test_home="$acceptance_root/home"
install -d -m 0700 "$test_home"
env \
  HOME="$test_home" \
  XDG_CONFIG_HOME="$test_home/.config" \
  XDG_DATA_HOME="$test_home/.local/share" \
  XDG_STATE_HOME="$test_home/.local/state" \
  XDG_CACHE_HOME="$test_home/.cache" \
  LOCALROUTER_PREFIX="$test_home/.local" \
  "$amd64_root/tools/install-localrouter.sh" install --no-systemd >/dev/null

test -x "$test_home/.local/bin/localrouter"
test -x "$test_home/.local/bin/lr"
test -f "$test_home/.agents/skills/localrouter-protocol-pack/SKILL.md"
test -f "$test_home/.omp/agent/skills/localrouter-protocol-pack/SKILL.md"
grep -Fq 'Provider and runtime handoff' "$test_home/.agents/skills/localrouter-protocol-pack/references/runtime-handoff.md"
for agent_contract in "$test_home/.agents/AGENTS.md" "$test_home/.codex/AGENTS.md" "$test_home/.omp/agent/AGENTS.md"; do
  grep -Fq '<!-- LOCALROUTER:BEGIN managed-block global-consumer-contract version=1 -->' "$agent_contract"
  grep -Fq 'lr find model --exact <pack>:<model-id>' "$agent_contract"
done

echo "release artifacts accepted: checksums=ok amd64=installed arm64=inspected skill=installed global-contract=installed"
