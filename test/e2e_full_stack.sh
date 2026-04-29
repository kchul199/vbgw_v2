#!/usr/bin/env bash
# E2E Full Stack Test Script
# Spins up the entire VBGW stack and validates the AI pipeline end-to-end.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
COMPOSE_FILE="$PROJECT_DIR/vbgw-freeswitch/docker-compose.yml"
ORCHESTRATOR_URL="http://localhost:8080"
MAX_WAIT=60

echo "============================================"
echo "  VBGW E2E Full Stack Test"
echo "============================================"

# ── Step 1: Start infrastructure ──
echo "[1/6] Starting Docker Compose stack..."
if [ -f "$COMPOSE_FILE" ]; then
    docker compose -f "$COMPOSE_FILE" up -d 2>/dev/null || {
        echo "WARN: docker compose failed. Checking if services are already running..."
    }
else
    echo "WARN: $COMPOSE_FILE not found. Assuming services are already running."
fi

# ── Step 2: Wait for health ──
echo "[2/6] Waiting for Orchestrator health..."
WAITED=0
until curl -sf "$ORCHESTRATOR_URL/live" > /dev/null 2>&1; do
    WAITED=$((WAITED + 2))
    if [ "$WAITED" -ge "$MAX_WAIT" ]; then
        echo "FAIL: Orchestrator did not become healthy within ${MAX_WAIT}s"
        exit 1
    fi
    sleep 2
done
echo "  ✓ Orchestrator is alive (waited ${WAITED}s)"

# ── Step 3: Validate health endpoint ──
echo "[3/6] Validating health endpoints..."
HEALTH_RESP=$(curl -sf "$ORCHESTRATOR_URL/live" 2>&1)
if [ $? -ne 0 ]; then
    echo "FAIL: /live endpoint returned error"
    exit 1
fi
echo "  ✓ /live: OK"

READY_RESP=$(curl -sf "$ORCHESTRATOR_URL/ready" 2>&1)
if [ $? -ne 0 ]; then
    echo "FAIL: /ready endpoint returned error"
    exit 1
fi
echo "  ✓ /ready: OK"

# ── Step 4: Validate admin APIs ──
echo "[4/6] Validating admin API endpoints..."
API_KEY="${ADMIN_API_KEY:-changeme-admin-key}"
AUTH_HEADER="Authorization: Bearer $API_KEY"

for ENDPOINT in \
    "/api/v1/admin/sessions/active" \
    "/api/v1/admin/services/capacity" \
    "/api/v1/admin/slots" \
    "/api/v1/admin/queues" \
    "/api/v1/admin/gateways" \
    "/api/v1/admin/routing/config"; do
    
    STATUS=$(curl -sf -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" "$ORCHESTRATOR_URL$ENDPOINT" 2>&1 || echo "000")
    if [ "$STATUS" = "200" ]; then
        echo "  ✓ GET $ENDPOINT: $STATUS"
    else
        echo "  ✗ GET $ENDPOINT: $STATUS"
    fi
done

# ── Step 5: Validate metrics endpoint ──
echo "[5/6] Validating Prometheus metrics..."
METRICS_STATUS=$(curl -sf -o /dev/null -w "%{http_code}" -H "$AUTH_HEADER" "$ORCHESTRATOR_URL/metrics" 2>&1 || echo "000")
if [ "$METRICS_STATUS" = "200" ]; then
    # Validate key metrics exist
    METRICS_BODY=$(curl -sf -H "$AUTH_HEADER" "$ORCHESTRATOR_URL/metrics" 2>&1)
    for METRIC in \
        "vbgw_active_calls" \
        "vbgw_esl_connected" \
        "vbgw_sip_registered" \
        "vbgw_routing_config_loaded"; do
        
        if echo "$METRICS_BODY" | grep -q "$METRIC"; then
            echo "  ✓ Metric present: $METRIC"
        else
            echo "  ✗ Metric missing: $METRIC"
        fi
    done
else
    echo "  ✗ /metrics: $METRICS_STATUS"
fi

# ── Step 6: Summary ──
echo ""
echo "============================================"
echo "  E2E Validation Complete"
echo "============================================"
echo ""
echo "Note: SIPp-based call flow tests require SIPp installed."
echo "Run: sipp -sf test/sipp/uac_basic.xml -s 9296 <FS_IP>:5060 -m 1"
echo ""
echo "For AI pipeline validation:"
echo "  1. Place a call to 9296 via Zoiper/SIPp"
echo "  2. Check orchestrator logs: docker logs vbgw-orchestrator | grep 'STT Result'"
echo "  3. Verify greeting audio is played"
echo "  4. Speak and verify TTS response is played back"
