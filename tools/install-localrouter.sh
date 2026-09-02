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

install_release() {
  local release_binary
  if [[ -f "$project_root/gateway/go.mod" ]]; then
    make -C "$project_root/gateway" build
    release_binary="$project_root/gateway/localrouter"
  elif [[ -x "$project_root/localrouter" ]]; then
    release_binary="$project_root/localrouter"
  else
    echo "LocalRouter source tree or release binary is missing" >&2
    exit 1
  fi
  install -d -m 0755 "$bin_dir"
  install -m 0755 "$release_binary" "$bin_dir/localrouter"
  install -m 0755 "$project_root/tools/lr" "$bin_dir/lr"
  install -m 0755 "$project_root/tools/protocol-pack-lifecycle.sh" "$bin_dir/localrouter-protocols"
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
  echo "LocalRouter installed. API keys remain only in the reported mode-600 files."
}

uninstall_release() {
  if ((manage_systemd)); then
    systemctl --user disable --now localrouter.service 2>/dev/null || true
  fi
  rm -f -- "$unit_file" "$bin_dir/localrouter" "$bin_dir/lr" "$bin_dir/localrouter-protocols"
  if ((manage_systemd)); then
    systemctl --user daemon-reload
  fi
  echo "LocalRouter executables and user service removed; XDG config, data, state, and cache were preserved."
}

case "$action" in
  install) install_release ;;
  uninstall) uninstall_release ;;
  *) echo "usage: $0 {install|uninstall} [--migrate-from DIR] [--no-start|--no-systemd]" >&2; exit 2 ;;
esac
