#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

TS="$(date +%Y%m%d_%H%M%S)"
LOG_REL="logs/stage4_kamailio_${TS}"
LOG_DIR="${ROOT_DIR}/${LOG_REL}"
mkdir -p "${LOG_DIR}"

COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.prod.yml
  -f docker-compose.stage3.yml
  -f docker-compose.stage4-kamailio.yml
)

fs_cli() {
  docker exec vbgw-freeswitch sh -lc \
    '/usr/local/freeswitch/bin/fs_cli -H 127.0.0.1 -P 8021 -p "$ESL_PASSWORD" -x "$1"' sh "$1"
}

gateway_json() {
  local token
  token="$(grep '^ADMIN_API_KEY=' .env | cut -d= -f2-)"
  docker exec -e READ_TOKEN="${token}" vbgw-orchestrator sh -lc \
    'wget -q -O- --header="Authorization: Bearer $READ_TOKEN" http://127.0.0.1:8080/api/v1/admin/gateways'
}

wait_for_gateway_class() {
  local expected="$1"
  local deadline=$((SECONDS + 45))
  local body
  while (( SECONDS < deadline )); do
    body="$(gateway_json || true)"
    printf '%s\n' "${body}" >"${LOG_DIR}/gateway_${expected}_poll.json"
    if printf '%s' "${body}" | grep -q "\"health_class\":\"${expected}\""; then
      return 0
    fi
    sleep 2
  done
  return 1
}

echo "== Stage 4 Kamailio SBC harness =="
echo "logs: ${LOG_DIR}"

echo
echo "== Build/start Kamailio and VBGW trunk mode =="
STAGE4_PBX_MAIN_REGISTER=false docker compose "${COMPOSE_FILES[@]}" up -d --build stage4-kamailio freeswitch orchestrator bridge vbgw-ai redis portal
sleep 10

echo
echo "== FreeSWITCH trunk gateway status =="
fs_cli "sofia status gateway pbx-main" | tee "${LOG_DIR}/trunk_sofia_gateway_pbx_main.log"

echo
echo "== Orchestrator gateway API, trunk mode =="
gateway_json | tee "${LOG_DIR}/trunk_gateways.json"

if ! wait_for_gateway_class healthy; then
  echo "FAIL: trunk gateway did not become healthy" | tee "${LOG_DIR}/trunk_result.txt"
  exit 1
fi

echo
echo "== SIPp -> Kamailio -> VBGW trunk call to 9296 =="
docker run --rm --network vbgw-freeswitch_vbgw-net \
  -v "${ROOT_DIR}:/work" -w /work alpine:3.19 sh -lc \
  'apk add --no-cache sipp >/tmp/apk.log && sipp -sn uac -p 5072 -s 9296 -d 15000 -l 1 -m 1 stage4-kamailio:5060 -timeout 45 -trace_msg -trace_err -trace_screen -message_file '"${LOG_REL}"'/sipp_9296_messages_kamailio.log -error_file '"${LOG_REL}"'/sipp_9296_errors_kamailio.log -screen_file '"${LOG_REL}"'/sipp_9296_screen_kamailio.log' || true

sleep 3
docker logs vbgw-stage4-kamailio --since 3m 2>&1 | tee "${LOG_DIR}/kamailio_after_trunk_call.log" >/dev/null
docker logs vbgw-bridge --since 3m 2>&1 | tee "${LOG_DIR}/bridge_after_trunk_call.log" >/dev/null
docker logs vbgw-ai --since 3m 2>&1 | tee "${LOG_DIR}/ai_after_trunk_call.log" >/dev/null
fs_cli "show calls" | tee "${LOG_DIR}/fs_show_calls_after_trunk.log"

if ! grep -q '"grpc_recvs":[1-9]' "${LOG_DIR}/bridge_after_trunk_call.log"; then
  echo "FAIL: trunk call did not produce grpc_recvs > 0" | tee "${LOG_DIR}/trunk_result.txt"
  exit 1
fi
if grep -q 'local fallback greeting injected' "${LOG_DIR}/bridge_after_trunk_call.log"; then
  echo "FAIL: trunk call used fallback greeting" | tee "${LOG_DIR}/trunk_result.txt"
  exit 1
fi

echo "PASS: trunk mode gateway healthy and SIPp -> Kamailio -> VBGW call produced grpc_recvs > 0 without fallback" | tee "${LOG_DIR}/trunk_result.txt"

echo
echo "== Recreate VBGW in register mode =="
STAGE4_PBX_MAIN_REGISTER=true docker compose "${COMPOSE_FILES[@]}" up -d --force-recreate freeswitch orchestrator
sleep 20

echo
echo "== FreeSWITCH register gateway status =="
fs_cli "sofia status gateway pbx-main" | tee "${LOG_DIR}/register_sofia_gateway_pbx_main.log"

echo
echo "== Kamailio REGISTER logs =="
docker logs vbgw-stage4-kamailio --since 2m 2>&1 | tee "${LOG_DIR}/kamailio_register_logs.log"

echo
echo "== Orchestrator gateway API, register mode =="
gateway_json | tee "${LOG_DIR}/register_gateways.json"

if ! grep -q 'REGED' "${LOG_DIR}/register_sofia_gateway_pbx_main.log"; then
  echo "FAIL: register gateway did not reach REGED" | tee "${LOG_DIR}/register_result.txt"
  exit 1
fi
if ! wait_for_gateway_class healthy; then
  echo "FAIL: register gateway did not become healthy in API" | tee "${LOG_DIR}/register_result.txt"
  exit 1
fi

echo "PASS: register mode gateway reached REGED and API healthy" | tee "${LOG_DIR}/register_result.txt"

echo
echo "== Stage 4 Kamailio harness completed =="
echo "Evidence: ${LOG_DIR}"
