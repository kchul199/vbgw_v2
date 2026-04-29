/**
 * @file admin.go
 * @description 백오피스 연동을 위한 전역 관제/모니터링 API 엔드포인트
 */

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"
)

type AdminHandler struct {
	sessionMgr      session.Store
	capacityMgr     *capacity.Manager
	overflowMgr     *overflow.Manager
	gatewayStore    *interconnect.Store
	gatewaySelector *interconnect.Selector
	routeRuntime    *routing.Runtime
}

func NewAdminHandler(sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewayStore *interconnect.Store, gatewaySelector *interconnect.Selector, routeRuntime ...*routing.Runtime) *AdminHandler {
	h := &AdminHandler{
		sessionMgr:      sessionMgr,
		capacityMgr:     capacityMgr,
		overflowMgr:     overflowMgr,
		gatewayStore:    gatewayStore,
		gatewaySelector: gatewaySelector,
	}
	if len(routeRuntime) > 0 {
		h.routeRuntime = routeRuntime[0]
	}
	return h
}

// GetActiveSessions fetches a comprehensive list of all active sessions across the cluster.
// For purely local memory stores, it fetches local.
// For Redis, it iterates the keyspace (this should be used sparingly by Dashboards, e.g. polling every 5s).
func (h *AdminHandler) GetActiveSessions(w http.ResponseWriter, r *http.Request) {
	// Create an aggregation slice
	var activeSessions []map[string]interface{}

	// Iterate through local sessions (Since RedisStore isn't exposing a Scan() method natively yet,
	// we will proxy through ForEachLocal for the MemoryStore, and for Redis deployments this will fetch
	// the locally homed sessions per orchestrator pod. In a fully distributed Service Mesh,
	// dashboards can aggregate /admin/sessions/active across all orchestrator pods).
	h.sessionMgr.ForEachLocal(func(s *session.SessionState) {
		activeSessions = append(activeSessions, map[string]interface{}{
			"session_id":       s.SessionID,
			"node_id":          s.NodeID,
			"fs_uuid":          s.FSUUID,
			"caller_id":        s.CallerID,
			"dest_num":         s.DestNum,
			"entry_number":     s.EntryNumber,
			"service_name":     s.ServiceName,
			"source_gateway":   s.SourceGateway,
			"ingress_stage":    s.IngressStage,
			"route_type":       s.RouteType,
			"slot_id":          s.SlotID,
			"allocation_state": s.AllocationState,
			"overflow_policy":  s.OverflowPolicy,
			"overflow_target":  s.OverflowTarget,
			"state":            s.State,
			"queue_name":       s.QueueName,
			"queue_entered_at": s.QueueEnteredAt,
			"queue_timeout_at": s.QueueTimeoutAt,
			"overflow_reason":  s.OverflowReason,
			"is_ai_paused":     s.IsAIPaused(),
			"created_at":       s.CreatedAt,
		})
	})

	// Wrap in a response structure
	payload := map[string]interface{}{
		"status": "success",
		"count":  len(activeSessions),
		"data":   activeSessions,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetQueues exposes local overflow queue state per service.
func (h *AdminHandler) GetQueues(w http.ResponseWriter, r *http.Request) {
	snapshots := []overflow.QueueSnapshot{}
	if h.overflowMgr != nil {
		snapshots = h.overflowMgr.Snapshots(time.Now())
	}

	payload := map[string]interface{}{
		"status": "success",
		"count":  len(snapshots),
		"data":   snapshots,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetServiceCapacity exposes configured logical service capacity and local slot usage.
func (h *AdminHandler) GetServiceCapacity(w http.ResponseWriter, r *http.Request) {
	snapshots := []capacity.ServiceSnapshot{}
	if h.capacityMgr != nil {
		snapshots = h.capacityMgr.Snapshots()
	}

	payload := map[string]interface{}{
		"status": "success",
		"count":  len(snapshots),
		"data":   snapshots,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetSlots exposes flattened slot inventory across services.
func (h *AdminHandler) GetSlots(w http.ResponseWriter, r *http.Request) {
	type slotRecord struct {
		ServiceName string `json:"service_name"`
		capacity.SlotSnapshot
	}

	records := make([]slotRecord, 0)
	if h.capacityMgr != nil {
		for _, snapshot := range h.capacityMgr.Snapshots() {
			for _, slot := range snapshot.Slots {
				records = append(records, slotRecord{
					ServiceName:  snapshot.ServiceName,
					SlotSnapshot: slot,
				})
			}
		}
	}

	payload := map[string]interface{}{
		"status": "success",
		"count":  len(records),
		"data":   records,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetGatewayHealth exposes the current gateway health snapshots used by the selector.
func (h *AdminHandler) GetGatewayHealth(w http.ResponseWriter, r *http.Request) {
	snapshots := []interface{}{}
	if h.gatewayStore != nil {
		gatewayStates := h.gatewayStore.List()
		snapshots = make([]interface{}, 0, len(gatewayStates))
		for _, state := range gatewayStates {
			snapshots = append(snapshots, state)
		}
	}

	decisions := []interconnect.DecisionRecord{}
	if h.gatewaySelector != nil {
		decisions = h.gatewaySelector.DecisionHistory(10)
	}
	if decisions == nil {
		decisions = make([]interconnect.DecisionRecord, 0)
	}

	payload := map[string]interface{}{
		"status":    "success",
		"count":     len(snapshots),
		"data":      snapshots,
		"decisions": decisions,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// GetRoutingConfig exposes the current loaded routing configuration.
func (h *AdminHandler) GetRoutingConfig(w http.ResponseWriter, r *http.Request) {
	if h.routeRuntime == nil || h.routeRuntime.Config == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "no routing config loaded (legacy AI_ROUTE_NUMBERS mode)",
		})
		return
	}

	payload := map[string]interface{}{
		"status":  "success",
		"version": h.routeRuntime.Config.Version,
		"data":    h.routeRuntime.Config,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ReloadRoutingConfig triggers a hot-reload of routing.yaml from disk.
func (h *AdminHandler) ReloadRoutingConfig(w http.ResponseWriter, r *http.Request) {
	if h.routeRuntime == nil {
		http.Error(w, `{"status":"error","message":"routing runtime not initialized"}`, http.StatusServiceUnavailable)
		return
	}

	if err := h.routeRuntime.Reload(); err != nil {
		slog.Error("Routing config reload failed", "err", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "error",
			"message": err.Error(),
		})
		return
	}

	slog.Info("Routing config reloaded successfully", "version", h.routeRuntime.Config.Version)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"version": h.routeRuntime.Config.Version,
	})
}
