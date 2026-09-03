#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "$script_dir/.." && pwd)"
action="${1:-install}"
shift || true

home_dir="${HOME:?HOME is required}"
config_home="${XDG_CONFIG_HOME:-$home_dir/.config}"
data_home="${XDG_DATA_HOME:-$home_dir/.local/share}"
state_home="${XDG_STATE_HOME:-$home_dir/.local/state}"
cache_home="${XDG_CACHE_HOME:-$home_dir/.cache}"
prefix="${LOCALROUTER_PREFIX:-$home_dir/.local}"

for directory in "$config_home" "$data_home" "$state_home" "$cache_home" "$prefix"; do
  [[ "$directory" = /* ]] || { echo "XDG and install directories must be absolute: $directory" >&2; exit 2; }
done

config_dir="$config_home/localrouter"
data_dir="$data_home/localrouter"
state_dir="$state_home/localrouter"
cache_dir="$cache_home/localrouter"
bin_dir="$prefix/bin"
unit_dir="$config_home/systemd/user"
unit_file="$unit_dir/localrouter.service"
shared_skill_dir="$home_dir/.agents/skills"
omp_skill_dir="$home_dir/.omp/agent/skills"
protocol_skill_source="$project_root/.agents/skills/localrouter-protocol-pack"
agent_contract_source="$project_root/packaging/agent-contract/AGENTS.localrouter.md"
agent_contract_begin='<!-- LOCALROUTER:BEGIN managed-block global-consumer-contract version=1 -->'
agent_contract_end='<!-- LOCALROUTER:END managed-block global-consumer-contract -->'
legacy_data=""
start_service=1
manage_systemd=1

while (($#)); do
  case "$1" in
    --migrate-from)
      shift
      legacy_data="${1:-}"
      [[ -n "$legacy_data" ]] || { echo "--migrate-from requires a directory" >&2; exit 2; }
      ;;
    --no-start) start_service=0 ;;
    --no-systemd) manage_systemd=0; start_service=0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

install_private_dir() {
  install -d -m 0700 "$1"
  [[ ! -L "$1" ]] || { echo "refusing symlink directory: $1" >&2; exit 1; }
}

copy_without_overwrite() {
  local source="$1"
  local destination_dir="$2"
  local destination="$destination_dir/$(basename -- "$source")"
  [[ ! -e "$destination" && ! -L "$destination" ]] || {
    echo "migration target already exists: $destination" >&2
    exit 1
  }
  cp -a -- "$source" "$destination"
}

migrate_legacy() {
  local source="$1"
  source="$(realpath -- "$source")"
  [[ -d "$source" && ! -L "$source" ]] || { echo "legacy data directory is invalid: $source" >&2; exit 1; }
  if ((manage_systemd)) && systemctl --user --quiet is-active localrouter.service 2>/dev/null; then
    systemctl --user stop localrouter.service
  fi
  install_private_dir "$data_dir"
  install_private_dir "$state_dir"
  local entry name target
  while IFS= read -r -d '' entry; do
    name="$(basename -- "$entry")"
    case "$name" in
      protocol-events.jsonl|protocol-events.jsonl.1|protocol-state|workflow-state|protocol-revisions|protocol-drafts)
        target="$state_dir"
        ;;
      state-locks)
        continue
        ;;
      *) target="$data_dir" ;;
    esac
    copy_without_overwrite "$entry" "$target"
  done < <(find "$source" -mindepth 1 -maxdepth 1 -print0)
  find "$data_dir" "$state_dir" -type d -exec chmod 0700 {} +
  find "$data_dir" "$state_dir" -type f -exec chmod 0600 {} +
  echo "copied legacy state from $source; the source was preserved"
}

install_agent_skill() {
  [[ -f "$protocol_skill_source/SKILL.md" ]] || {
    echo "LocalRouter Protocol Pack Skill is missing from this release" >&2
    exit 1
  }

  local skill_root target staging
  for skill_root in "$shared_skill_dir" "$omp_skill_dir"; do
    install -d -m 0755 "$skill_root"
    target="$skill_root/localrouter-protocol-pack"
    if [[ -e "$target" || -L "$target" ]]; then
      if [[ ! -f "$target/.localrouter-managed" ]]; then
        echo "refusing to overwrite unmanaged global skill: $target" >&2
        exit 1
      fi
      find "$target" -depth -delete
    fi
    staging="$(mktemp -d "$skill_root/.localrouter-protocol-pack.XXXXXX")"
    cp -a -- "$protocol_skill_source/." "$staging/"
    printf '%s\n' 'Installed and owned by LocalRouter; do not edit in place.' >"$staging/.localrouter-managed"
    mv -- "$staging" "$target"
  done
}

install_agent_contract() {
  [[ -f "$agent_contract_source" ]] || {
    echo "LocalRouter global Agent contract is missing from this release" >&2
    exit 1
  }
  local target target_dir staging mode begin_count end_count begin_line end_line total_lines
  for target in \
    "$home_dir/.agents/AGENTS.md" \
    "$home_dir/.codex/AGENTS.md" \
    "$home_dir/.omp/agent/AGENTS.md"; do
    target_dir="$(dirname -- "$target")"
    install -d -m 0755 "$target_dir"
    [[ ! -L "$target" ]] || { echo "refusing symlink Agent contract: $target" >&2; exit 1; }
    [[ ! -e "$target" || -f "$target" ]] || { echo "Agent contract is not a regular file: $target" >&2; exit 1; }
    mode=0644
    if [[ -f "$target" ]]; then
      mode="$(stat -c '%a' "$target")"
    fi
    staging="$(mktemp "$target_dir/.localrouter-agents.XXXXXX")"
    if [[ ! -f "$target" ]]; then
      install -m "$mode" "$agent_contract_source" "$staging"
    else
      begin_count="$(grep -Fxc "$agent_contract_begin" "$target" || true)"
      end_count="$(grep -Fxc "$agent_contract_end" "$target" || true)"
      if [[ "$begin_count" = "0" && "$end_count" = "0" ]]; then
        {
          sed -n '1,$p' "$target"
          printf '\n'
          sed -n '1,$p' "$agent_contract_source"
        } >"$staging"
      elif [[ "$begin_count" = "1" && "$end_count" = "1" ]]; then
        begin_line="$(grep -nF "$agent_contract_begin" "$target" | cut -d: -f1)"
        end_line="$(grep -nF "$agent_contract_end" "$target" | cut -d: -f1)"
        ((begin_line <= end_line)) || { find "$staging" -maxdepth 0 -type f -delete; echo "invalid LocalRouter managed block ordering: $target" >&2; exit 1; }
        total_lines="$(wc -l <"$target")"
        {
          if ((begin_line > 1)); then sed -n "1,$((begin_line - 1))p" "$target"; fi
          sed -n '1,$p' "$agent_contract_source"
          if ((end_line < total_lines)); then sed -n "$((end_line + 1)),${total_lines}p" "$target"; fi
        } >"$staging"
      else
        find "$staging" -maxdepth 0 -type f -delete
        echo "refusing malformed LocalRouter managed block: $target" >&2
        exit 1
      fi
      chmod "$mode" "$staging"
    fi
    mv -f -- "$staging" "$target"
  done
}

remove_agent_contract() {
  local target target_dir staging begin_count end_count begin_line end_line total_lines
  for target in \
    "$home_dir/.agents/AGENTS.md" \
    "$home_dir/.codex/AGENTS.md" \
    "$home_dir/.omp/agent/AGENTS.md"; do
    [[ -f "$target" && ! -L "$target" ]] || continue
    begin_count="$(grep -Fxc "$agent_contract_begin" "$target" || true)"
    end_count="$(grep -Fxc "$agent_contract_end" "$target" || true)"
    [[ "$begin_count" = "1" && "$end_count" = "1" ]] || continue
    begin_line="$(grep -nF "$agent_contract_begin" "$target" | cut -d: -f1)"
    end_line="$(grep -nF "$agent_contract_end" "$target" | cut -d: -f1)"
    ((begin_line <= end_line)) || continue
    target_dir="$(dirname -- "$target")"
    total_lines="$(wc -l <"$target")"
    staging="$(mktemp "$target_dir/.localrouter-agents.XXXXXX")"
    {
      if ((begin_line > 1)); then sed -n "1,$((begin_line - 1))p" "$target"; fi
      if ((end_line < total_lines)); then sed -n "$((end_line + 1)),${total_lines}p" "$target"; fi
    } >"$staging"
    chmod "$(stat -c '%a' "$target")" "$staging"
    if [[ -s "$staging" ]]; then
      mv -f -- "$staging" "$target"
    else
      find "$staging" "$target" -maxdepth 0 -type f -delete
    fi
  done
}

remove_managed_agent_skills() {
  local target
  for target in \
    "$shared_skill_dir/localrouter-protocol-pack" \
    "$omp_skill_dir/localrouter-protocol-pack"; do
    if [[ -d "$target" && ! -L "$target" && -f "$target/.localrouter-managed" ]]; then
      find "$target" -depth -delete
    fi
  done
}

install_executable_atomic() {
  local source="$1"
  local destination="$2"
  local destination_dir staging
  destination_dir="$(dirname -- "$destination")"
  staging="$(mktemp "$destination_dir/.localrouter-install.XXXXXX")"
  install -m 0755 "$source" "$staging"
  mv -f -- "$staging" "$destination"
}

install_release() {
  local release_binary
  if [[ -f "$project_root/gateway/go.mod" ]]; then
    local build_lock_fd build_lock_path
    build_lock_path="${XDG_RUNTIME_DIR:-/run/user/$UID}/localrouter-build.lock"
    [[ "$build_lock_path" = /* ]] || { echo "build lock path must be absolute: $build_lock_path" >&2; exit 2; }
    exec {build_lock_fd}>"$build_lock_path"
    chmod 0600 "$build_lock_path"
    flock "$build_lock_fd"
    make -C "$project_root/gateway" build
    release_binary="$project_root/gateway/localrouter"
  elif [[ -x "$project_root/localrouter" ]]; then
    release_binary="$project_root/localrouter"
  else
    echo "LocalRouter source tree or release binary is missing" >&2
    exit 1
  fi
  install -d -m 0755 "$bin_dir"
  install_executable_atomic "$release_binary" "$bin_dir/localrouter"
  install_executable_atomic "$project_root/tools/lr" "$bin_dir/lr"
  install_executable_atomic "$project_root/tools/protocol-pack-lifecycle.sh" "$bin_dir/localrouter-protocols"
  if [[ -n "${build_lock_fd:-}" ]]; then
    flock -u "$build_lock_fd"
  fi
  install_agent_skill
  install_agent_contract
  install_private_dir "$config_dir"
  install_private_dir "$data_dir"
  install_private_dir "$state_dir"
  install_private_dir "$cache_dir"
  if [[ ! -e "$config_dir/config.env" ]]; then
    install -m 0600 "$project_root/packaging/localrouter.env.example" "$config_dir/config.env"
  fi
  if [[ -n "$legacy_data" ]]; then
    migrate_legacy "$legacy_data"
  fi
  install -d -m 0755 "$unit_dir"
  escaped_exec="${bin_dir//\\/\\\\}/localrouter"
  escaped_exec="${escaped_exec//&/\\&}"
  escaped_exec="${escaped_exec//|/\\|}"
  sed "s|@EXEC_START@|$escaped_exec|g" "$project_root/packaging/systemd/localrouter.service.in" >"$unit_file"
  chmod 0644 "$unit_file"
  if ((manage_systemd)); then
    systemctl --user daemon-reload
    if ((start_service)); then
      systemctl --user enable --now localrouter.service
    fi
  fi
  "$bin_dir/localrouter" paths
  echo "LocalRouter installed. Global Agent contracts and Protocol Pack Skills were installed for Codex, OMP, and shared Agents. API keys remain only in the reported mode-600 files."
}

uninstall_release() {
  if ((manage_systemd)); then
    systemctl --user disable --now localrouter.service 2>/dev/null || true
  fi
  rm -f -- "$unit_file" "$bin_dir/localrouter" "$bin_dir/lr" "$bin_dir/localrouter-protocols"
  remove_agent_contract
  remove_managed_agent_skills
  if ((manage_systemd)); then
    systemctl --user daemon-reload
  fi
  echo "LocalRouter executables, user service, and LocalRouter-owned global Agent files were removed; XDG config, data, state, and cache were preserved."
}

case "$action" in
  install) install_release ;;
  uninstall) uninstall_release ;;
  *) echo "usage: $0 {install|uninstall} [--migrate-from DIR] [--no-start|--no-systemd]" >&2; exit 2 ;;
esac
