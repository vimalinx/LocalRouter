#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/localrouter-docker.XXXXXX)"
case "$test_root" in
  /tmp/localrouter-docker.*) ;;
  *) echo "unsafe Docker test directory" >&2; exit 2 ;;
esac
suffix="$(basename "$test_root" | tr -cd 'a-zA-Z0-9')"
image="localrouter:acceptance-${suffix}"
container="localrouter-acceptance-${suffix}"
volumes=(
  "${container}-config"
  "${container}-data"
  "${container}-state"
  "${container}-cache"
)

cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker volume rm "${volumes[@]}" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
  find "$test_root" -depth -delete
}
trap cleanup EXIT

free_port() {
  python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    print(listener.getsockname()[1])
PY
}

local_port="$(free_port)"
lan_port="$(free_port)"
while [[ "$lan_port" = "$local_port" ]]; do lan_port="$(free_port)"; done

docker build --quiet \
  --build-arg VCS_REF=acceptance \
  --build-arg BUILD_DATE=2026-09-04T00:00:00Z \
  --tag "$image" "$project_root" >"$test_root/image-id"

for volume in "${volumes[@]}"; do docker volume create "$volume" >/dev/null; done

run_container() {
  docker run --detach --name "$container" \
    --network host \
    --read-only \
    --tmpfs /tmp:rw,noexec,nosuid,nodev,size=64m \
    --cap-drop ALL \
    --security-opt no-new-privileges:true \
    --init \
    --env LOCAL_GATEWAY_PORT="$local_port" \
    --env LOCAL_GATEWAY_UPDATE_CHECK_ENABLED=false \
    --env LOCAL_GATEWAY_LAN_ENABLED=true \
    --env LOCAL_GATEWAY_LAN_HOST=0.0.0.0 \
    --env LOCAL_GATEWAY_LAN_PORT="$lan_port" \
    --volume "${volumes[0]}:/var/lib/localrouter/config" \
    --volume "${volumes[1]}:/var/lib/localrouter/data" \
    --volume "${volumes[2]}:/var/lib/localrouter/state" \
    --volume "${volumes[3]}:/var/lib/localrouter/cache" \
    "$image" >/dev/null
}

wait_ready() {
  for _ in $(seq 1 160); do
    if curl --noproxy '*' --fail --silent "http://127.0.0.1:$lan_port/healthz" >"$test_root/health.json" 2>/dev/null; then
      return
    fi
    if ! docker inspect --format '{{.State.Running}}' "$container" 2>/dev/null | grep -Fxq true; then
      docker logs "$container" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  docker logs "$container" >&2 || true
  echo "Dockerized LocalRouter did not become ready" >&2
  exit 1
}

wait_healthy() {
  for _ in $(seq 1 160); do
    health_status="$(docker inspect --format '{{.State.Health.Status}}' "$container")"
    if [[ "$health_status" = healthy ]]; then
      return
    fi
    if [[ "$health_status" = unhealthy ]]; then
      docker inspect --format '{{json .State.Health.Log}}' "$container" >&2 || true
      exit 1
    fi
    sleep 0.1
  done
  docker inspect --format '{{json .State.Health}}' "$container" >&2 || true
  echo "Dockerized LocalRouter did not become healthy" >&2
  exit 1
}

run_container
wait_ready
wait_healthy
jq -e '.ok == true and .mode == "lan-service"' "$test_root/health.json" >/dev/null
curl --noproxy '*' --fail --silent "http://127.0.0.1:$local_port/docs/agent-start.md" >"$test_root/agent-guide.md"
grep -Fq 'lr init' "$test_root/agent-guide.md"

test "$(docker image inspect --format '{{.Config.User}}' "$image")" = "10001:10001"
test "$(docker inspect --format '{{.HostConfig.ReadonlyRootfs}}' "$container")" = "true"
docker inspect --format '{{json .HostConfig.CapDrop}}' "$container" | jq -e 'index("ALL") != null' >/dev/null

for path in /local/status /local/api/summary /manage/mcp; do
  status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$lan_port$path")"
  test "$status" = "404"
done
unauthorized_status="$(curl --noproxy '*' --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$lan_port/v1/models")"
test "$unauthorized_status" = "401"

test "$(docker exec "$container" stat -c '%a' /var/lib/localrouter/data/api-token)" = "600"
token_hash_before="$(docker exec "$container" sha256sum /var/lib/localrouter/data/api-token | awk '{print $1}')"
test "${#token_hash_before}" = "64"

docker stop --time 35 "$container" >/dev/null
docker logs "$container" >"$test_root/first-run.log" 2>&1
grep -Fq 'received signal terminated, shutting down' "$test_root/first-run.log"
docker rm "$container" >/dev/null

run_container
wait_ready
wait_healthy
token_hash_after="$(docker exec "$container" sha256sum /var/lib/localrouter/data/api-token | awk '{print $1}')"
test "$token_hash_after" = "$token_hash_before"
docker exec "$container" localrouter version | grep -Fq 'commit=acceptance'

LOCALROUTER_LAN_HOST=192.168.1.10 docker compose --project-directory "$project_root" -f "$project_root/compose.yaml" config --format json >"$test_root/compose.json"
jq -e '.services.localrouter.healthcheck.test == ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:$${LOCAL_GATEWAY_PORT}/healthz || exit 1"]' "$test_root/compose.json" >/dev/null
jq -e '.services.localrouter.environment.LOCAL_GATEWAY_UPDATE_CHECK_ENABLED == "true"' "$test_root/compose.json" >/dev/null
mkdir -p "$test_root/bind-config" "$test_root/bind-data" "$test_root/bind-state" "$test_root/bind-cache"
LOCALROUTER_LAN_HOST=192.168.1.10 \
LOCALROUTER_CONFIG_DIR="$test_root/bind-config" \
LOCALROUTER_DATA_DIR="$test_root/bind-data" \
LOCALROUTER_STATE_DIR="$test_root/bind-state" \
LOCALROUTER_CACHE_DIR="$test_root/bind-cache" \
  docker compose --project-directory "$project_root" \
    -f "$project_root/compose.yaml" \
    -f "$project_root/packaging/docker/compose.bind.yaml" \
    config --format json >"$test_root/bind-compose.json"
jq -e --arg root "$test_root" '
  [.services.localrouter.volumes[] | select(.type == "bind") | .source] ==
  [$root + "/bind-config", $root + "/bind-data", $root + "/bind-state", $root + "/bind-cache"]
' "$test_root/bind-compose.json" >/dev/null

echo "LocalRouter Docker acceptance passed"
