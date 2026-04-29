/**
 * @file calls.go
 * @description POST /api/v1/calls — 아웃바운드 콜 생성 엔드포인트
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | atomic CAS 기반 용량 체크 + ESL originate
 * ─────────────────────────────────────────
 */

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"vbgw-orchestrator/internal/esl"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/session"

	"github.com/google/uuid"
)

// CallsHandler handles origination of outbound calls.
type CallsHandler struct {
	ESL                 esl.Commander
	Sessions            session.Store
	GatewaySelector     *interconnect.Selector
	DefaultGatewayOrder []string
	OutboundEnabled     bool
	NodeID              string
}

type createCallRequest struct {
	TargetURI string `json:"target_uri"`
	CallerID  string `json:"caller_id,omitempty"` // P-07: Outbound Caller-ID
}

type createCallResponse struct {
	CallID string `json:"call_id"`
	Status string `json:"status"`
}

// CreateCall handles POST /api/v1/calls.
func (h *CallsHandler) CreateCall(w http.ResponseWriter, r *http.Request) {
	metrics.ApiOutboundRequests.Inc()

	var req createCallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.TargetURI == "" {
		http.Error(w, `{"error":"target_uri is required"}`, http.StatusBadRequest)
		return
	}
	if !h.OutboundEnabled {
		http.Error(w, `{"error":"pbx interconnect disabled"}`, http.StatusServiceUnavailable)
		return
	}

	// Atomic capacity check + session creation
	sessionID := uuid.New().String()
	s := session.NewSession(h.NodeID, sessionID, sessionID, "", req.TargetURI)
	ctx := r.Context()
	if !h.Sessions.AddIfUnderCapacity(ctx, s) {
		metrics.ApiOutboundRejected.Inc()
		s.Cancel()
		http.Error(w, `{"error":"capacity exceeded"}`, http.StatusServiceUnavailable)
		return
	}

	slog.Info("Creating outbound call", "session_id", sessionID, "target_masked", maskURI(req.TargetURI))

	gatewayOrder := append([]string(nil), h.DefaultGatewayOrder...)
	if h.GatewaySelector != nil {
		selection, err := h.GatewaySelector.SelectOriginate()
		if err != nil {
			h.Sessions.Release(ctx, sessionID)
			slog.Error("Gateway selection failed", "err", err)
			http.Error(w, `{"error":"no healthy gateway available"}`, http.StatusServiceUnavailable)
			return
		}
		gatewayOrder = append([]string(nil), selection.GatewayOrder...)
		slog.Info("Selected outbound gateway order", "session_id", sessionID, "gateway_order", gatewayOrder, "reason", selection.Reason)
	}

	// ESL originate (background API) — P-07: with Caller-ID, Phase 3: health-aware gateway selection
	_, err := h.ESL.Originate(ctx, sessionID, req.TargetURI, req.CallerID, gatewayOrder)
	if err != nil {
		h.Sessions.Release(ctx, sessionID)
		slog.Error("ESL originate failed", "err", err)
		http.Error(w, `{"error":"originate failed"}`, http.StatusInternalServerError)
		return
	}
	metrics.ActiveCalls.Set(float64(h.Sessions.Count(ctx)))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createCallResponse{
		CallID: sessionID,
		Status: "initiating",
	})
}

func defaultGatewayOrder(primaryGateway, standbyGateway string, allowStandby bool) []string {
	order := []string{}
	if primary := strings.TrimSpace(primaryGateway); primary != "" {
		order = append(order, primary)
	}
	if allowStandby {
		if standby := strings.TrimSpace(standbyGateway); standby != "" && standby != primaryGateway {
			order = append(order, standby)
		}
	}
	return order
}

// maskURI masks a SIP URI for PII protection in logs.
func maskURI(uri string) string {
	if len(uri) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(uri)-4) + uri[len(uri)-4:]
}
