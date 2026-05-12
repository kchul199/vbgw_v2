/**
 * @file metrics_handler.go
 * @description 주요 Prometheus gauge를 JSON으로 응답 — 포탈 대시보드용
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-05-11 | Portal | 최초 생성 | GET /api/v1/admin/metrics/summary
 * ─────────────────────────────────────────
 */

package api

import (
	"encoding/json"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"vbgw-orchestrator/internal/metrics"
)

// MetricsSummaryHandler serves a JSON summary of key operational metrics.
type MetricsSummaryHandler struct{}

// MetricsSummary is the JSON response for /api/v1/admin/metrics/summary.
type MetricsSummary struct {
	ActiveCalls          float64 `json:"active_calls"`
	ESLConnected         float64 `json:"esl_connected"`
	BridgeHealthy        float64 `json:"bridge_healthy"`
	SIPRegistered        float64 `json:"sip_registered"`
	SIPRegistrationAlarm float64 `json:"sip_registration_alarm"`
	GRPCActiveSessions   float64 `json:"grpc_active_sessions"`
	RoutingConfigLoaded  float64 `json:"routing_config_loaded"`
}

// GetSummary handles GET /api/v1/admin/metrics/summary.
func (h *MetricsSummaryHandler) GetSummary(w http.ResponseWriter, r *http.Request) {
	summary := MetricsSummary{
		ActiveCalls:          readGauge(metrics.ActiveCalls),
		ESLConnected:         readGauge(metrics.ESLConnected),
		BridgeHealthy:        readGauge(metrics.BridgeHealthy),
		SIPRegistered:        readGauge(metrics.SipRegistered),
		SIPRegistrationAlarm: readGauge(metrics.SipRegistrationAlarm),
		GRPCActiveSessions:   readGauge(metrics.GrpcActiveSessions),
		RoutingConfigLoaded:  readGauge(metrics.RoutingConfigLoaded),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"data":   summary,
	})
}

// readGauge extracts the current value from a prometheus.Gauge.
func readGauge(g prometheus.Gauge) float64 {
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		return 0
	}
	if m.Gauge != nil {
		return m.Gauge.GetValue()
	}
	return 0
}
