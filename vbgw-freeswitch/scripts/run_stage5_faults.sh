#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TS="$(date +%Y%m%d_%H%M%S)"
LOG_REL="logs/stage5_faults_${TS}"
LOG_DIR="${ROOT_DIR}/${LOG_REL}"
mkdir -p "${LOG_DIR}"

API_BASE="${API_BASE:-http://127.0.0.1:8180}"
READ_TOKEN="$(grep '^ADMIN_API_KEY=' .env | cut -d= -f2-)"
CONTROL_TOKEN="$(grep '^ADMIN_CONTROL_KEY=' .env | cut -d= -f2-)"
REDIS_PASS="$(grep '^REDIS_PASS=' .env | cut -d= -f2-)"
SIP_PORT="${SIP_PORT:-5080}"

if [[ -z "${READ_TOKEN}" || -z "${CONTROL_TOKEN}" || -z "${REDIS_PASS}" ]]; then
  echo "FAIL: ADMIN_API_KEY, ADMIN_CONTROL_KEY, and REDIS_PASS must be set in .env" | tee "${LOG_DIR}/result.txt"
  exit 1
fi

cleanup() {
  docker start vbgw-ai >/dev/null 2>&1 || true
  docker start vbgw-redis >/dev/null 2>&1 || true
  curl -sS -X POST \
    -H "Authorization: Bearer ${CONTROL_TOKEN}" \
    -H "Content-Type: application/json" \
    -d '{"reason":"stage5 cleanup"}' \
    "${API_BASE}/api/v1/admin/cluster/nodes/vbgw-orchestrator/resume" >/dev/null 2>&1 || true
}
trap cleanup EXIT

api_get() {
  local path="$1"
  curl -sS -H "Authorization: Bearer ${READ_TOKEN}" "${API_BASE}${path}"
}

api_post_control() {
  local path="$1"
  local body="$2"
  curl -sS -X POST \
    -H "Authorization: Bearer ${CONTROL_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "${body}" \
    "${API_BASE}${path}"
}

redis_cli() {
  docker exec -e REDISCLI_AUTH="${REDIS_PASS}" vbgw-redis redis-cli -p 16379 "$@"
}

wait_healthy() {
  local container="$1"
  local deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    local status
    status="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "${container}" 2>/dev/null || true)"
    if [[ "${status}" == "healthy" || "${status}" == "running" ]]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

fs_cli() {
  docker exec vbgw-freeswitch sh -lc \
    '/usr/local/freeswitch/bin/fs_cli -H 127.0.0.1 -P 8021 -p "$ESL_PASSWORD" -x "$1"' sh "$1"
}

capture_state() {
  local prefix="$1"
  curl -sS "${API_BASE}/live" >"${LOG_DIR}/${prefix}_live.txt" 2>&1 || true
  curl -sS "${API_BASE}/ready" >"${LOG_DIR}/${prefix}_ready.txt" 2>&1 || true
  api_get "/health" | jq . >"${LOG_DIR}/${prefix}_health.json" 2>&1 || true
  api_get "/api/v1/admin/services" | jq . >"${LOG_DIR}/${prefix}_services.json" 2>&1 || true
  api_get "/api/v1/admin/cluster/nodes" | jq . >"${LOG_DIR}/${prefix}_nodes.json" 2>&1 || true
  api_get "/api/v1/admin/cluster/leases" | jq . >"${LOG_DIR}/${prefix}_leases.json" 2>&1 || true
  api_get "/api/v1/admin/cluster/compatibility" | jq . >"${LOG_DIR}/${prefix}_compatibility.json" 2>&1 || true
  redis_cli GET vbgw:active_calls >"${LOG_DIR}/${prefix}_redis_active_calls.txt" 2>&1 || true
  fs_cli "show calls" >"${LOG_DIR}/${prefix}_fs_show_calls.log" 2>&1 || true
}

sipp_call() {
  local name="$1"
  local duration_ms="${2:-8000}"
  local port="${SIP_PORT}"
  SIP_PORT=$((SIP_PORT + 1))
  echo "== SIPp ${name} on local port ${port} =="
  docker run --rm --network vbgw-freeswitch_vbgw-net \
    -v "${ROOT_DIR}:/work" -w /work alpine:3.19 sh -lc \
    'apk add --no-cache sipp >/tmp/apk.log && sipp -sn uac -p '"${port}"' -s 9296 -d '"${duration_ms}"' -l 1 -m 1 stage4-kamailio:5060 -timeout 45 -trace_msg -trace_err -trace_screen -message_file '"${LOG_REL}"'/sipp_'"${name}"'_messages.log -error_file '"${LOG_REL}"'/sipp_'"${name}"'_errors.log -screen_file '"${LOG_REL}"'/sipp_'"${name}"'_screen.log' \
    >"${LOG_DIR}/sipp_${name}_stdout.log" 2>&1 || true
  docker logs vbgw-bridge --since 2m >"${LOG_DIR}/bridge_after_${name}.log" 2>&1 || true
  docker logs vbgw-ai --since 2m >"${LOG_DIR}/ai_after_${name}.log" 2>&1 || true
  docker logs vbgw-orchestrator --since 2m >"${LOG_DIR}/orchestrator_after_${name}.log" 2>&1 || true
  fs_cli "show calls" >"${LOG_DIR}/fs_show_calls_after_${name}.log" 2>&1 || true
}

echo "== Stage 5 HA/fault injection =="
echo "logs: ${LOG_DIR}"

capture_state "00_baseline"

HEALTH_ACTIVE="$(jq -r '.active_calls // -1' "${LOG_DIR}/00_baseline_health.json" 2>/dev/null || echo -1)"
SERVICE_ACTIVE_SUM="$(jq '[.data[]?.active_calls // 0] | add // 0' "${LOG_DIR}/00_baseline_services.json" 2>/dev/null || echo -1)"
LEASE_COUNT="$(jq -r '.count // -1' "${LOG_DIR}/00_baseline_leases.json" 2>/dev/null || echo -1)"

if [[ "${HEALTH_ACTIVE}" != "0" && "${SERVICE_ACTIVE_SUM}" == "0" && "${LEASE_COUNT}" == "0" ]]; then
  {
    echo "Detected stale Redis active_calls drift before Stage 5."
    echo "health.active_calls=${HEALTH_ACTIVE}, service_active_sum=${SERVICE_ACTIVE_SUM}, lease_count=${LEASE_COUNT}"
    echo "Resetting local test key vbgw:active_calls to 0 for clean fault injection."
  } | tee "${LOG_DIR}/00_active_calls_drift.txt"
  redis_cli SET vbgw:active_calls 0 >"${LOG_DIR}/00_active_calls_reset.txt" 2>&1
  capture_state "01_after_active_calls_reset"
else
  echo "No stale active_calls drift detected." | tee "${LOG_DIR}/00_active_calls_drift.txt"
fi

NODE_ID="$(api_get "/api/v1/admin/cluster/nodes" | jq -r '.data[0].node_id // empty')"
if [[ -z "${NODE_ID}" ]]; then
  echo "FAIL: cluster node id not found" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Drain node and verify new call is blocked =="
api_post_control "/api/v1/admin/cluster/nodes/${NODE_ID}/drain" '{"reason":"stage5 drain admission test"}' | jq . >"${LOG_DIR}/10_drain_response.json" 2>&1 || true
sleep 3
capture_state "10_after_drain"
sipp_call "drain_blocked" 5000

if ! jq -e --arg id "${NODE_ID}" '.data[] | select(.node_id == $id and .state == "draining")' "${LOG_DIR}/10_after_drain_nodes.json" >/dev/null; then
  echo "FAIL: node did not enter draining state" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Resume node and verify call recovers =="
api_post_control "/api/v1/admin/cluster/nodes/${NODE_ID}/resume" '{"reason":"stage5 resume admission test"}' | jq . >"${LOG_DIR}/20_resume_response.json" 2>&1 || true
sleep 3
capture_state "20_after_resume"
sipp_call "resume_success" 12000

if ! grep -q '"grpc_recvs":[1-9]' "${LOG_DIR}/bridge_after_resume_success.log"; then
  echo "FAIL: resumed call did not produce grpc_recvs > 0" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi
if grep -q 'local fallback greeting injected' "${LOG_DIR}/bridge_after_resume_success.log"; then
  echo "FAIL: resumed call used fallback greeting" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Restart bridge and verify recovery call =="
docker restart vbgw-bridge >"${LOG_DIR}/30_bridge_restart.txt" 2>&1
wait_healthy vbgw-bridge
sleep 3
capture_state "30_after_bridge_restart"
sipp_call "bridge_restart_success" 12000

if ! grep -q '"grpc_recvs":[1-9]' "${LOG_DIR}/bridge_after_bridge_restart_success.log"; then
  echo "FAIL: bridge restart recovery call did not produce grpc_recvs > 0" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi
if grep -q 'local fallback greeting injected' "${LOG_DIR}/bridge_after_bridge_restart_success.log"; then
  echo "FAIL: bridge restart recovery call used fallback greeting" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Stop AI, capture failure behavior, restart AI and verify recovery =="
docker stop vbgw-ai >"${LOG_DIR}/40_ai_stop.txt" 2>&1
sleep 4
capture_state "40_ai_down"
sipp_call "ai_down" 7000
docker start vbgw-ai >"${LOG_DIR}/41_ai_start.txt" 2>&1
sleep 8
capture_state "41_ai_recovered"
sipp_call "ai_recovery_success" 12000

if ! grep -q '"grpc_recvs":[1-9]' "${LOG_DIR}/bridge_after_ai_recovery_success.log"; then
  echo "FAIL: AI recovery call did not produce grpc_recvs > 0" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi
if grep -q 'local fallback greeting injected' "${LOG_DIR}/bridge_after_ai_recovery_success.log"; then
  echo "FAIL: AI recovery call used fallback greeting" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Stop Redis, capture failure behavior, restart Redis and verify recovery =="
docker stop vbgw-redis >"${LOG_DIR}/50_redis_stop.txt" 2>&1
sleep 5
capture_state "50_redis_down"
sipp_call "redis_down" 7000
docker start vbgw-redis >"${LOG_DIR}/51_redis_start.txt" 2>&1
wait_healthy vbgw-redis
sleep 8
capture_state "51_redis_recovered"
sipp_call "redis_recovery_success" 12000

if ! grep -q '"grpc_recvs":[1-9]' "${LOG_DIR}/bridge_after_redis_recovery_success.log"; then
  echo "FAIL: Redis recovery call did not produce grpc_recvs > 0" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi
if grep -q 'local fallback greeting injected' "${LOG_DIR}/bridge_after_redis_recovery_success.log"; then
  echo "FAIL: Redis recovery call used fallback greeting" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

echo
echo "== Inject stale lease and verify reaper clears it =="
OLD_MS="$(( ($(date +%s) - 120) * 1000 ))"
redis_cli DEL vbgw:lease:service:bot-main:slot:stage5-stale vbgw:lease:session:stage5-stale-session >"${LOG_DIR}/60_stale_cleanup_before.txt" 2>&1
redis_cli HSET vbgw:lease:service:bot-main:slot:stage5-stale \
  service_name bot-main \
  slot_id stage5-stale \
  session_id stage5-stale-session \
  owner_node_id stage5-missing-node \
  owner_state active \
  lease_schema_version 1 \
  leased_at_unix_ms "${OLD_MS}" \
  renewed_at_unix_ms "${OLD_MS}" >"${LOG_DIR}/60_stale_hset.txt" 2>&1
redis_cli PEXPIRE vbgw:lease:service:bot-main:slot:stage5-stale 60000 >"${LOG_DIR}/60_stale_expire.txt" 2>&1
redis_cli SET vbgw:lease:session:stage5-stale-session vbgw:lease:service:bot-main:slot:stage5-stale PX 60000 >"${LOG_DIR}/60_stale_session_set.txt" 2>&1
redis_cli EXISTS vbgw:lease:service:bot-main:slot:stage5-stale >"${LOG_DIR}/60_stale_exists_before_reaper.txt" 2>&1
redis_cli HGETALL vbgw:lease:service:bot-main:slot:stage5-stale >"${LOG_DIR}/60_stale_hgetall_before_reaper.txt" 2>&1
capture_state "60_stale_visible"
sleep 7
capture_state "61_stale_reaped"
redis_cli EXISTS vbgw:lease:service:bot-main:slot:stage5-stale >"${LOG_DIR}/61_stale_exists_after_reaper.txt" 2>&1

if [[ "$(tr -d '\r\n' <"${LOG_DIR}/60_stale_hset.txt")" != "8" ]]; then
  echo "FAIL: stale lease HSET did not create the expected fields" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi
if jq -e '.data[]? | select(.slot_id == "stage5-stale" and .stale == true)' "${LOG_DIR}/60_stale_visible_leases.json" >/dev/null; then
  echo "Stale lease was visible through the cluster API before reaping." | tee "${LOG_DIR}/60_stale_api_visibility.txt"
else
  echo "Stale lease was reaped before the API visibility snapshot; Redis pre-reaper HGETALL is preserved." | tee "${LOG_DIR}/60_stale_api_visibility.txt"
fi
if [[ "$(tr -d '\r\n' <"${LOG_DIR}/61_stale_exists_after_reaper.txt")" != "0" ]] || jq -e '.data[]? | select(.slot_id == "stage5-stale")' "${LOG_DIR}/61_stale_reaped_leases.json" >/dev/null; then
  echo "FAIL: stale lease was not reaped" | tee -a "${LOG_DIR}/result.txt"
  exit 1
fi

capture_state "99_final"

echo "PASS: Stage 5 single-node fault injection completed; drain/resume, bridge restart, AI recovery, Redis recovery, and stale lease reaping verified." | tee "${LOG_DIR}/result.txt"
echo "Evidence: ${LOG_DIR}"
