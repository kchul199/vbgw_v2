#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TS="$(date +%Y%m%d_%H%M%S)"
LOG_REL="logs/stage5_live_ha_${TS}"
LOG_DIR="${ROOT_DIR}/${LOG_REL}"
mkdir -p "${LOG_DIR}"

PRIMARY_API="${PRIMARY_API:-http://127.0.0.1:8180}"
STANDBY_API="${STANDBY_API:-http://127.0.0.1:8181}"
READ_TOKEN="$(grep '^ADMIN_API_KEY=' .env | cut -d= -f2-)"
CONTROL_TOKEN="$(grep '^ADMIN_CONTROL_KEY=' .env | cut -d= -f2-)"
REDIS_PASS="$(grep '^REDIS_PASS=' .env | cut -d= -f2-)"
STANDBY_CONTAINER="${STANDBY_CONTAINER:-vbgw-orchestrator-standby}"
STANDBY_NODE_ID="${STANDBY_NODE_ID:-vbgw-orchestrator-standby}"
BINARY_PATH="${BINARY_PATH:-/tmp/vbgw-orchestrator-linux-arm64}"

if [[ -z "${READ_TOKEN}" || -z "${CONTROL_TOKEN}" || -z "${REDIS_PASS}" ]]; then
  echo "FAIL: ADMIN_API_KEY, ADMIN_CONTROL_KEY, and REDIS_PASS must be set in .env" | tee "${LOG_DIR}/result.txt"
  exit 1
fi

api_get() {
  local base="$1"
  local path="$2"
  curl -sS --max-time 8 -H "Authorization: Bearer ${READ_TOKEN}" "${base}${path}"
}

redis_cli() {
  docker exec -e REDISCLI_AUTH="${REDIS_PASS}" vbgw-redis redis-cli -p 16379 "$@"
}

cleanup() {
  docker start vbgw-orchestrator >/dev/null 2>&1 || true
  docker rm -f "${STANDBY_CONTAINER}" >/dev/null 2>&1 || true
  redis_cli DEL \
    vbgw:lease:service:bot-main:slot:stage5-live-ha \
    vbgw:lease:session:stage5-live-ha-session >/dev/null 2>&1 || true
}
trap cleanup EXIT

capture() {
  local base="$1"
  local prefix="$2"
  curl -sS --max-time 5 "${base}/live" >"${LOG_DIR}/${prefix}_live.txt" 2>&1 || true
  curl -sS --max-time 5 "${base}/ready" >"${LOG_DIR}/${prefix}_ready.txt" 2>&1 || true
  api_get "${base}" "/health" | jq . >"${LOG_DIR}/${prefix}_health.json" 2>&1 || true
  api_get "${base}" "/api/v1/admin/cluster/nodes" | jq . >"${LOG_DIR}/${prefix}_nodes.json" 2>&1 || true
  api_get "${base}" "/api/v1/admin/cluster/leases" | jq . >"${LOG_DIR}/${prefix}_leases.json" 2>&1 || true
  api_get "${base}" "/api/v1/admin/cluster/compatibility" | jq . >"${LOG_DIR}/${prefix}_compatibility.json" 2>&1 || true
}

wait_http_ready() {
  local base="$1"
  local deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if curl -sS --max-time 3 "${base}/ready" >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_nodes_count() {
  local base="$1"
  local expected="$2"
  local deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    local body count
    body="$(api_get "${base}" "/api/v1/admin/cluster/nodes" || true)"
    count="$(printf '%s' "${body}" | jq -r '.count // 0' 2>/dev/null || echo 0)"
    printf '%s\n' "${body}" >"${LOG_DIR}/nodes_count_poll_${expected}.json"
    if [[ "${count}" == "${expected}" ]]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

wait_lease_absent() {
  local base="$1"
  local slot="$2"
  local deadline=$((SECONDS + 45))
  while (( SECONDS < deadline )); do
    local body
    body="$(api_get "${base}" "/api/v1/admin/cluster/leases" || true)"
    printf '%s\n' "${body}" >"${LOG_DIR}/lease_reap_poll.json"
    if ! printf '%s' "${body}" | jq -e --arg slot "${slot}" '.data[]? | select(.slot_id == $slot)' >/dev/null 2>&1; then
      return 0
    fi
    sleep 2
  done
  return 1
}

echo "== Stage 5 live 2-node HA rehearsal =="
echo "logs: ${LOG_DIR}"

echo
echo "== Build Linux arm64 orchestrator binary for standby bind mount =="
(cd orchestrator && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o "${BINARY_PATH}" ./cmd)

echo
echo "== Start standby orchestrator container =="
docker rm -f "${STANDBY_CONTAINER}" >/dev/null 2>&1 || true
docker run -d \
  --name "${STANDBY_CONTAINER}" \
  --network vbgw-freeswitch_vbgw-net \
  -p 8181:8080 \
  -v "${BINARY_PATH}:/usr/local/bin/orchestrator:ro" \
  -e HTTP_PORT=8080 \
  -e ESL_HOST=vbgw-freeswitch \
  -e ESL_PORT=8021 \
  -e ESL_PASSWORD="$(grep '^ESL_PASSWORD=' .env | cut -d= -f2-)" \
  -e REDIS_ADDR=vbgw-redis:16379 \
  -e REDIS_PASS="${REDIS_PASS}" \
  -e BRIDGE_HOST=bridge \
  -e BRIDGE_INTERNAL_PORT=8091 \
  -e NODE_ID="${STANDBY_NODE_ID}" \
  -e ORCHESTRATOR_VERSION=7.0.0 \
  -e LEASE_SCHEMA_VERSION=1 \
  -e CLUSTER_HEARTBEAT_INTERVAL_MS=1000 \
  -e CLUSTER_HEARTBEAT_TTL_MS=4000 \
  -e CLUSTER_REAPER_INTERVAL_MS=1000 \
  -e CLUSTER_LEASE_TTL_MS=15000 \
  -e CLUSTER_LEASE_STALE_GRACE_MS=3000 \
  -e ADMIN_API_KEY="${READ_TOKEN}" \
  -e ADMIN_CONTROL_KEY="${CONTROL_TOKEN}" \
  -e JWT_SECRET="$(grep '^JWT_SECRET=' .env | cut -d= -f2-)" \
  -e INTERNAL_API_SECRET="$(grep '^INTERNAL_API_SECRET=' .env | cut -d= -f2-)" \
  -e RUNTIME_PROFILE=production \
  -e ROUTING_CONFIG_PATH=/app/config/routing.yaml \
  -e AI_ROUTE_NUMBERS=9196 \
  -e LOG_LEVEL=warn \
  vbgw-freeswitch-orchestrator:latest >"${LOG_DIR}/standby_container_id.txt"

if ! wait_http_ready "${STANDBY_API}"; then
  docker logs "${STANDBY_CONTAINER}" >"${LOG_DIR}/standby_start_failed.log" 2>&1 || true
  echo "FAIL: standby orchestrator did not become ready" | tee "${LOG_DIR}/result.txt"
  exit 1
fi

capture "${PRIMARY_API}" "10_primary_with_standby"
capture "${STANDBY_API}" "10_standby_with_primary"

if ! wait_nodes_count "${STANDBY_API}" 2; then
  capture "${STANDBY_API}" "11_nodes_count_failed"
  echo "FAIL: expected 2 cluster nodes while primary and standby are running" | tee "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Inject stale lease owned by primary and verify standby sees it =="
OLD_MS="$(( ($(date +%s) - 120) * 1000 ))"
redis_cli DEL vbgw:lease:service:bot-main:slot:stage5-live-ha vbgw:lease:session:stage5-live-ha-session >"${LOG_DIR}/20_cleanup_lease.txt" 2>&1
redis_cli HSET vbgw:lease:service:bot-main:slot:stage5-live-ha \
  service_name bot-main \
  slot_id stage5-live-ha \
  session_id stage5-live-ha-session \
  owner_node_id vbgw-orchestrator \
  owner_state active \
  lease_schema_version 1 \
  leased_at_unix_ms "${OLD_MS}" \
  renewed_at_unix_ms "${OLD_MS}" >"${LOG_DIR}/20_stale_hset.txt" 2>&1
redis_cli PEXPIRE vbgw:lease:service:bot-main:slot:stage5-live-ha 60000 >"${LOG_DIR}/20_stale_expire.txt" 2>&1
redis_cli SET vbgw:lease:session:stage5-live-ha-session vbgw:lease:service:bot-main:slot:stage5-live-ha PX 60000 >"${LOG_DIR}/20_stale_session_set.txt" 2>&1
capture "${STANDBY_API}" "20_stale_visible"

echo
echo "== Stop primary orchestrator and verify standby remains ready =="
docker stop vbgw-orchestrator >"${LOG_DIR}/30_primary_stop.txt" 2>&1
sleep 6
capture "${STANDBY_API}" "30_standby_after_primary_stop"

if ! jq -e --arg id "${STANDBY_NODE_ID}" '.data[]? | select(.node_id == $id and .state == "active")' "${LOG_DIR}/30_standby_after_primary_stop_nodes.json" >/dev/null; then
  echo "FAIL: standby node not active after primary stop" | tee "${LOG_DIR}/result.txt"
  exit 1
fi
if ! wait_lease_absent "${STANDBY_API}" "stage5-live-ha"; then
  capture "${STANDBY_API}" "31_stale_not_reaped"
  echo "FAIL: standby did not reap stale primary-owned lease" | tee "${LOG_DIR}/result.txt"
  exit 1
fi
capture "${STANDBY_API}" "31_stale_reaped"

echo
echo "== Restart primary and verify cluster recovers to 2 nodes =="
docker start vbgw-orchestrator >"${LOG_DIR}/40_primary_start.txt" 2>&1
sleep 8
capture "${PRIMARY_API}" "40_primary_recovered"
capture "${STANDBY_API}" "40_standby_after_primary_recovered"

if ! wait_nodes_count "${PRIMARY_API}" 2; then
  capture "${PRIMARY_API}" "41_recovery_nodes_failed"
  echo "FAIL: expected 2 cluster nodes after primary restart" | tee "${LOG_DIR}/result.txt"
  exit 1
fi

echo "PASS: Stage 5 live 2-node HA rehearsal completed; standby heartbeat, primary loss, stale lease reap, and primary recovery verified." | tee "${LOG_DIR}/result.txt"
echo "Evidence: ${LOG_DIR}"
