/**
 * @file health.go
 * @description 헬스체크 엔드포인트 — /live, /ready, /health, /metrics
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | 3-way 헬스체크
 * v1.0.1 | 2026-04-09 | [Implementer] | T-11 | Bridge 응답 Body 항상 Close
 * ─────────────────────────────────────────
 */

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/session"
)

// ESLChecker is the interface HealthHandler needs from ESL client.
type ESLChecker interface {
	IsConnected() bool
}

// HealthHandler holds dependencies for health endpoints.
type HealthHandler struct {
	ESL          ESLChecker
	Sessions     session.Store
	BridgeURL    string
	BridgeSecret string
	StartTime    time.Time
	httpClient   *http.Client
}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler(eslClient ESLChecker, sessions session.Store, bridgeURL, bridgeSecret string) *HealthHandler {
	return &HealthHandler{
		ESL:          eslClient,
		Sessions:     sessions,
		BridgeURL:    bridgeURL,
		BridgeSecret: bridgeSecret,
		StartTime:    time.Now(),
		httpClient:   &http.Client{Timeout: 2 * time.Second},
	}
}

// Live returns 200 if the process is alive.
func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

// Ready returns 200 if ESL and the backing session store are available.
func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	if !h.ESL.IsConnected() {
		http.Error(w, `{"status":"not_ready","reason":"ESL disconnected"}`, http.StatusServiceUnavailable)
		return
	}
	if err := h.checkSessionStore(r.Context()); err != nil {
		http.Error(w, `{"status":"not_ready","reason":"session store unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

type healthResponse struct {
	Status      string `json:"status"`
	Uptime      string `json:"uptime"`
	ActiveCalls int64  `json:"active_calls"`
	ESL         string `json:"esl"`
	Redis       string `json:"redis"`
	Bridge      string `json:"bridge"`
}

// Health returns a JSON health summary including ESL, Bridge, and session count.
func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:      "healthy",
		Uptime:      time.Since(h.StartTime).Truncate(time.Second).String(),
		ActiveCalls: h.Sessions.Count(r.Context()),
	}

	// Check ESL
	if h.ESL.IsConnected() {
		resp.ESL = "connected"
		metrics.ESLConnected.Set(1)
	} else {
		resp.ESL = "disconnected"
		resp.Status = "degraded"
		metrics.ESLConnected.Set(0)
	}

	if err := h.checkSessionStore(r.Context()); err != nil {
		resp.Redis = "unreachable"
		resp.Status = "degraded"
	} else {
		resp.Redis = "healthy"
	}

	// Check Bridge
	// T-11: Always close response body to prevent fd leak
	req, _ := http.NewRequest(http.MethodGet, h.BridgeURL+"/internal/health", nil)
	if h.BridgeSecret != "" {
		req.Header.Set("X-Internal-Secret", h.BridgeSecret)
	}
	bridgeResp, err := h.httpClient.Do(req)
	if err != nil {
		resp.Bridge = "unreachable"
		resp.Status = "degraded"
		metrics.BridgeHealthy.Set(0)
	} else {
		defer bridgeResp.Body.Close()
		if bridgeResp.StatusCode != http.StatusOK {
			resp.Bridge = "unreachable"
			resp.Status = "degraded"
			metrics.BridgeHealthy.Set(0)
		} else {
			resp.Bridge = "healthy"
			metrics.BridgeHealthy.Set(1)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if resp.Status != "healthy" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(resp)
}

func (h *HealthHandler) checkSessionStore(ctx context.Context) error {
	if h == nil || h.Sessions == nil {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	return h.Sessions.HealthCheck(checkCtx)
}
