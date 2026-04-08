#!/usr/bin/env bash
# run_tc_all.sh — TC-01~TC-08 자동 테스트 실행
# 변경이력: v1.0.0 | 2026-04-07 | [Implementer] | Phase 3 | 전체 테스트 케이스 자동화
#
# Usage: ./scripts/run_tc_all.sh [TARGET_IP]
#
# Prerequisites:
#   - Docker Compose stack running
#   - SIPp installed

set -euo pipefail

TARGET_IP="${1:-127.0.0.1}"
ADMIN_KEY="${ADMIN_API_KEY:-changeme-admin-key}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
RESULTS_DIR="${SCRIPT_DIR}/../results/tc_$(date +%Y%m%d_%H%M%S)"
PASS_COUNT=0
FAIL_COUNT=0

mkdir -p "${RESULTS_DIR}"

pass() { echo "  ✓ PASS: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { echo "  ✗ FAIL: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }

echo "============================================"
echo " TC-01 ~ TC-08 Automated Test Suite"
echo "============================================"
echo " Target: ${TARGET_IP}"
echo ""

# ─────────────────────────────────────────
# TC-01: SIP Call Setup / Teardown
# ─────────────────────────────────────────
echo "--- TC-01: SIP Call Setup / Teardown ---"

# Single call test
sipp -sn uac -d 3000 -l 1 -m 1 "${TARGET_IP}:5060" -timeout 10 > "${RESULTS_DIR}/tc01.log" 2>&1
if [ $? -eq 0 ]; then
    pass "Single call setup/teardown"
else
    fail "Single call setup/teardown"
fi

# Verify active_calls returns to 0
sleep 2
ACTIVE=$(curl -s "http://${TARGET_IP}:8080/health" 2>/dev/null | python3 -c "import sys,json; print(json.load(sys.stdin).get('active_calls','-1'))" 2>/dev/null || echo "-1")
if [ "${ACTIVE}" = "0" ]; then
    pass "active_calls = 0 after hangup"
else
    fail "active_calls = ${ACTIVE} (expected 0)"
fi

# ─────────────────────────────────────────
# TC-02: RTP Media Quality (basic check)
# ─────────────────────────────────────────
echo ""
echo "--- TC-02: RTP Media Quality ---"

# Check health endpoint is reachable
HEALTH_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://${TARGET_IP}:8080/health" 2>/dev/null || echo "000")
if [ "${HEALTH_CODE}" = "200" ] || [ "${HEALTH_CODE}" = "503" ]; then
    pass "Health endpoint reachable (${HEALTH_CODE})"
else
    fail "Health endpoint unreachable (${HEALTH_CODE})"
fi

# ─────────────────────────────────────────
# TC-03: VAD + gRPC (check metrics)
# ─────────────────────────────────────────
echo ""
echo "--- TC-03: VAD + gRPC ---"

METRICS=$(curl -s "http://${TARGET_IP}:8080/metrics" 2>/dev/null || echo "")
if echo "${METRICS}" | grep -q "vbgw_vad_speech_events_total"; then
    pass "VAD metrics registered"
else
    fail "VAD metrics not found"
fi

if echo "${METRICS}" | grep -q "vbgw_grpc_active_sessions"; then
    pass "gRPC session metrics registered"
else
    fail "gRPC session metrics not found"
fi

# ─────────────────────────────────────────
# TC-04: Barge-in (check metric exists)
# ─────────────────────────────────────────
echo ""
echo "--- TC-04: Barge-in ---"

if echo "${METRICS}" | grep -q "vbgw_bargein_events_total"; then
    pass "Barge-in metrics registered"
else
    fail "Barge-in metrics not found"
fi

# ─────────────────────────────────────────
# TC-05: IVR/DTMF (SIPp DTMF scenario)
# ─────────────────────────────────────────
echo ""
echo "--- TC-05: IVR/DTMF ---"

if [ -f "${SCRIPT_DIR}/../tests/sipp/dtmf_scenario.xml" ]; then
    sipp -sf "${SCRIPT_DIR}/../tests/sipp/dtmf_scenario.xml" \
        -l 1 -m 1 "${TARGET_IP}:5060" \
        -timeout 30 > "${RESULTS_DIR}/tc05.log" 2>&1
    if [ $? -eq 0 ]; then
        pass "DTMF scenario completed"
    else
        fail "DTMF scenario failed"
    fi
else
    fail "DTMF scenario file not found"
fi

# ─────────────────────────────────────────
# TC-06: Call Bridge (API test)
# ─────────────────────────────────────────
echo ""
echo "--- TC-06: Call Bridge ---"

# Test bridge endpoint exists (expect 400 or 404 without valid sessions)
BRIDGE_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "http://${TARGET_IP}:8080/api/v1/calls/bridge" \
    -H "X-Admin-Key: ${ADMIN_KEY}" \
    -H "Content-Type: application/json" \
    -d '{"call_id_1":"none","call_id_2":"none"}' 2>/dev/null || echo "000")
if [ "${BRIDGE_CODE}" = "404" ] || [ "${BRIDGE_CODE}" = "400" ]; then
    pass "Bridge endpoint responds (${BRIDGE_CODE})"
else
    fail "Bridge endpoint unexpected (${BRIDGE_CODE})"
fi

# ─────────────────────────────────────────
# TC-07: Recording Quota
# ─────────────────────────────────────────
echo ""
echo "--- TC-07: Recording Quota ---"

if echo "${METRICS}" | grep -q "vbgw_recording_cleanup_files_total"; then
    pass "Recording cleanup metrics registered"
else
    fail "Recording cleanup metrics not found"
fi

# ─────────────────────────────────────────
# TC-08: Graceful Shutdown (verify health)
# ─────────────────────────────────────────
echo ""
echo "--- TC-08: Graceful Shutdown ---"

# Test live endpoint
LIVE_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://${TARGET_IP}:8080/live" 2>/dev/null || echo "000")
if [ "${LIVE_CODE}" = "200" ]; then
    pass "Live endpoint OK"
else
    fail "Live endpoint failed (${LIVE_CODE})"
fi

# Test ready endpoint
READY_CODE=$(curl -s -o /dev/null -w "%{http_code}" "http://${TARGET_IP}:8080/ready" 2>/dev/null || echo "000")
if [ "${READY_CODE}" = "200" ] || [ "${READY_CODE}" = "503" ]; then
    pass "Ready endpoint responds (${READY_CODE})"
else
    fail "Ready endpoint failed (${READY_CODE})"
fi

# ─────────────────────────────────────────
# Summary
# ─────────────────────────────────────────
echo ""
echo "============================================"
echo " Test Results: ${PASS_COUNT} PASS / ${FAIL_COUNT} FAIL"
echo "============================================"
echo " Results saved to: ${RESULTS_DIR}"

if [ "${FAIL_COUNT}" -gt 0 ]; then
    echo " STATUS: SOME TESTS FAILED"
    exit 1
else
    echo " STATUS: ALL TESTS PASSED"
    exit 0
fi
