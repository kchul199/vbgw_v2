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
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/session"

	"github.com/google/uuid"
)

// CallsHandler handles outbound call requests.
type CallsHandler struct {
	ESL          esl.Commander
	Sessions     *session.Manager
	UseStandbyGW bool // Q-03: true if PBX_STANDBY_HOST is configured
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

	// Atomic capacity check + session creation
	sessionID := uuid.New().String()
	s := session.NewSession(sessionID, sessionID, "", req.TargetURI)
	if !h.Sessions.AddIfUnderCapacity(s) {
		metrics.ApiOutboundRejected.Inc()
		s.Cancel()
		http.Error(w, `{"error":"capacity exceeded"}`, http.StatusServiceUnavailable)
		return
	}

	slog.Info("Creating outbound call", "session_id", sessionID, "target_masked", maskURI(req.TargetURI))

	// ESL originate (background API) — P-07: with Caller-ID, Q-03: conditional standby
	_, err := h.ESL.Originate(sessionID, req.TargetURI, req.CallerID, h.UseStandbyGW)
	if err != nil {
		h.Sessions.Release(sessionID)
		slog.Error("ESL originate failed", "err", err)
		http.Error(w, `{"error":"originate failed"}`, http.StatusInternalServerError)
		return
	}
	metrics.ActiveCalls.Set(float64(h.Sessions.Count()))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(createCallResponse{
		CallID: sessionID,
		Status: "initiating",
	})
}

// maskURI masks a SIP URI for PII protection in logs.
func maskURI(uri string) string {
	if len(uri) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(uri)-4) + uri[len(uri)-4:]
}
