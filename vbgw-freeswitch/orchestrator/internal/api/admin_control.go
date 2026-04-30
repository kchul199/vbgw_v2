package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/cluster"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/session"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	operationStatusAccepted  = "accepted"
	operationStatusCompleted = "completed"
	operationStatusFailed    = "failed"
)

type ControlOperation struct {
	ID           string                 `json:"id"`
	Kind         string                 `json:"kind"`
	TargetType   string                 `json:"target_type"`
	Target       string                 `json:"target"`
	Status       string                 `json:"status"`
	Reason       string                 `json:"reason"`
	RequestedBy  AuthPrincipal          `json:"requested_by"`
	RollbackHint string                 `json:"rollback_hint,omitempty"`
	RequestedAt  time.Time              `json:"requested_at"`
	CompletedAt  time.Time              `json:"completed_at,omitempty"`
	Error        string                 `json:"error,omitempty"`
	Result       map[string]interface{} `json:"result,omitempty"`
}

type OperationRegistry struct {
	mu    sync.RWMutex
	ops   map[string]ControlOperation
	order []string
	limit int
}

func NewOperationRegistry(limit int) *OperationRegistry {
	if limit <= 0 {
		limit = 100
	}
	return &OperationRegistry{
		ops:   make(map[string]ControlOperation, limit),
		order: make([]string, 0, limit),
		limit: limit,
	}
}

func (r *OperationRegistry) Start(kind, targetType, target, reason, rollbackHint string, principal AuthPrincipal) ControlOperation {
	if r == nil {
		return ControlOperation{}
	}
	op := ControlOperation{
		ID:           uuid.NewString(),
		Kind:         kind,
		TargetType:   targetType,
		Target:       target,
		Status:       operationStatusAccepted,
		Reason:       reason,
		RequestedBy:  principal,
		RollbackHint: rollbackHint,
		RequestedAt:  time.Now(),
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.ops[op.ID] = op
	r.order = append(r.order, op.ID)
	if len(r.order) > r.limit {
		expiredID := r.order[0]
		r.order = r.order[1:]
		delete(r.ops, expiredID)
	}
	metrics.AdminControlOperationsTotal.WithLabelValues(kind, operationStatusAccepted).Inc()
	return op
}

func (r *OperationRegistry) Complete(op ControlOperation, result map[string]interface{}, err error) ControlOperation {
	if r == nil || op.ID == "" {
		return op
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	current, ok := r.ops[op.ID]
	if !ok {
		current = op
	}
	current.CompletedAt = time.Now()
	current.Result = result
	if err != nil {
		current.Status = operationStatusFailed
		current.Error = err.Error()
		metrics.AdminControlOperationsTotal.WithLabelValues(current.Kind, operationStatusFailed).Inc()
	} else {
		current.Status = operationStatusCompleted
		metrics.AdminControlOperationsTotal.WithLabelValues(current.Kind, operationStatusCompleted).Inc()
	}
	r.ops[current.ID] = current
	return current
}

func (r *OperationRegistry) Get(id string) (ControlOperation, bool) {
	if r == nil || id == "" {
		return ControlOperation{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	op, ok := r.ops[id]
	return op, ok
}

func (r *OperationRegistry) List(limit int) []ControlOperation {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	if limit <= 0 || limit > len(r.order) {
		limit = len(r.order)
	}
	out := make([]ControlOperation, 0, limit)
	for i := len(r.order) - 1; i >= 0 && len(out) < limit; i-- {
		if op, ok := r.ops[r.order[i]]; ok {
			out = append(out, op)
		}
	}
	return out
}

type adminControlRequest struct {
	Reason  string `json:"reason"`
	Enabled *bool  `json:"enabled,omitempty"`
}

func (h *AdminHandler) GetService(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "name")
	if h.capacityMgr == nil {
		http.Error(w, `{"error":"capacity manager unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	snapshot, ok := h.capacityMgr.ServiceSnapshot(serviceName)
	if !ok {
		http.Error(w, `{"error":"service not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   snapshot,
	})
}

func (h *AdminHandler) GetOperations(w http.ResponseWriter, r *http.Request) {
	operations := []ControlOperation{}
	if h.operations != nil {
		operations = h.operations.List(50)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"count":  len(operations),
		"data":   operations,
	})
}

func (h *AdminHandler) GetOperation(w http.ResponseWriter, r *http.Request) {
	if h.operations == nil {
		http.Error(w, `{"error":"operation registry unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	opID := chi.URLParam(r, "id")
	op, ok := h.operations.Get(opID)
	if !ok {
		http.Error(w, `{"error":"operation not found"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status": "success",
		"data":   op,
	})
}

func (h *AdminHandler) PauseService(w http.ResponseWriter, r *http.Request) {
	h.applyServiceControl(w, r, "pause_service", "pause", "Resume with POST /api/v1/admin/services/"+chi.URLParam(r, "name")+"/resume")
}

func (h *AdminHandler) ResumeService(w http.ResponseWriter, r *http.Request) {
	h.applyServiceControl(w, r, "resume_service", "resume", "Pause again with POST /api/v1/admin/services/"+chi.URLParam(r, "name")+"/pause")
}

func (h *AdminHandler) DrainService(w http.ResponseWriter, r *http.Request) {
	h.applyServiceControl(w, r, "drain_service", "drain", "Resume with POST /api/v1/admin/services/"+chi.URLParam(r, "name")+"/resume once maintenance ends")
}

func (h *AdminHandler) applyServiceControl(w http.ResponseWriter, r *http.Request, operationKind, action, rollbackHint string) {
	serviceName := chi.URLParam(r, "name")
	if h.capacityMgr == nil {
		http.Error(w, `{"error":"capacity manager unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := decodeAdminControlRequest(r, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	principal := PrincipalFromContext(r.Context())
	op := h.startOperation(operationKind, "service", serviceName, req.Reason, rollbackHint, principal)
	slog.Info("Admin control request",
		"op_id", op.ID,
		"kind", operationKind,
		"service", serviceName,
		"requested_by", principal.Subject,
		"reason", req.Reason,
	)

	var applied bool
	switch action {
	case "pause":
		applied = h.capacityMgr.PauseService(serviceName, req.Reason)
	case "resume":
		applied = h.capacityMgr.ResumeService(serviceName)
	case "drain":
		applied = h.capacityMgr.DrainService(serviceName, req.Reason)
	}
	if !applied {
		h.finishOperation(w, op, nil, fmt.Errorf("service %s not found", serviceName), http.StatusNotFound)
		return
	}

	snapshot, _ := h.capacityMgr.ServiceSnapshot(serviceName)
	h.finishOperation(w, op, map[string]interface{}{
		"service": snapshot,
		"action":  action,
	}, nil, http.StatusAccepted)
}

func (h *AdminHandler) FlushQueue(w http.ResponseWriter, r *http.Request) {
	serviceName := chi.URLParam(r, "name")
	if h.overflowMgr == nil {
		http.Error(w, `{"error":"overflow manager unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := decodeAdminControlRequest(r, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	principal := PrincipalFromContext(r.Context())
	op := h.startOperation("flush_queue", "queue", serviceName, req.Reason, "Re-admit callers manually or let them redial after maintenance", principal)
	flushed := h.overflowMgr.FlushService(serviceName)

	killErrors := make([]string, 0)
	for _, entry := range flushed {
		metrics.QueueAbandonTotal.WithLabelValues(serviceName, "operator_flush").Inc()
		if sess, ok := h.sessionMgr.Get(r.Context(), entry.SessionID); ok {
			sess.SetLifecycleState(session.StateEnded, "", "operator_flush", time.Time{}, time.Time{})
			if err := h.sessionMgr.SaveSession(r.Context(), sess); err != nil {
				killErrors = append(killErrors, err.Error())
			}
			if h.eslClient != nil && sess.FSUUID != "" {
				if err := h.eslClient.Kill(r.Context(), sess.FSUUID); err != nil {
					killErrors = append(killErrors, err.Error())
				}
			}
		}
	}

	result := map[string]interface{}{
		"service_name":  serviceName,
		"flushed_count": len(flushed),
	}
	if len(killErrors) > 0 {
		result["errors"] = killErrors
		h.finishOperation(w, op, result, fmt.Errorf("queue flushed with %d follow-up errors", len(killErrors)), http.StatusInternalServerError)
		return
	}
	h.finishOperation(w, op, result, nil, http.StatusAccepted)
}

func (h *AdminHandler) ForceReleaseSlot(w http.ResponseWriter, r *http.Request) {
	slotID := chi.URLParam(r, "slotID")
	if h.capacityMgr == nil {
		http.Error(w, `{"error":"capacity manager unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := decodeAdminControlRequest(r, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	principal := PrincipalFromContext(r.Context())
	op := h.startOperation("force_release_slot", "slot", slotID, req.Reason, "Session remains active; re-admit requires manual service resume if needed", principal)
	serviceName, sessionID, ok := h.capacityMgr.ForceReleaseSlot(slotID)
	if !ok {
		h.finishOperation(w, op, nil, fmt.Errorf("slot %s not found", slotID), http.StatusNotFound)
		return
	}

	if sessionID != "" {
		if sess, found := h.sessionMgr.Get(r.Context(), sessionID); found {
			sess.SetCapacityMetadata("", session.AllocationReleased, "", "")
			if err := h.sessionMgr.SaveSession(r.Context(), sess); err != nil {
				h.finishOperation(w, op, map[string]interface{}{
					"service_name": serviceName,
					"slot_id":      slotID,
					"session_id":   sessionID,
				}, err, http.StatusInternalServerError)
				return
			}
		}
	}

	result := map[string]interface{}{
		"service_name": serviceName,
		"slot_id":      slotID,
		"session_id":   sessionID,
	}
	h.finishOperation(w, op, result, nil, http.StatusAccepted)
}

func (h *AdminHandler) SetGatewayStandby(w http.ResponseWriter, r *http.Request) {
	gatewayName := chi.URLParam(r, "name")
	if h.gatewayStore == nil {
		http.Error(w, `{"error":"gateway store unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := decodeAdminControlRequest(r, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	principal := PrincipalFromContext(r.Context())
	op := h.startOperation("set_gateway_standby", "gateway", gatewayName, req.Reason, "Clear standby with POST /api/v1/admin/gateways/"+gatewayName+"/standby and body {\"reason\":\"...\",\"enabled\":false}", principal)
	if !h.gatewayStore.SetManualStandby(gatewayName, req.Reason, enabled) {
		h.finishOperation(w, op, nil, fmt.Errorf("gateway %s not found", gatewayName), http.StatusNotFound)
		return
	}

	state, _ := h.gatewayStore.Get(gatewayName)
	h.finishOperation(w, op, map[string]interface{}{
		"gateway": state,
		"enabled": enabled,
	}, nil, http.StatusAccepted)
}

func (h *AdminHandler) DrainNode(w http.ResponseWriter, r *http.Request) {
	h.applyNodeState(w, r, "drain_node", "draining", "Resume node with POST /api/v1/admin/cluster/nodes/"+chi.URLParam(r, "id")+"/resume")
}

func (h *AdminHandler) ResumeNode(w http.ResponseWriter, r *http.Request) {
	h.applyNodeState(w, r, "resume_node", "active", "Drain node again with POST /api/v1/admin/cluster/nodes/"+chi.URLParam(r, "id")+"/drain")
}

func (h *AdminHandler) applyNodeState(w http.ResponseWriter, r *http.Request, operationKind, state, rollbackHint string) {
	nodeID := chi.URLParam(r, "id")
	if h.clusterMgr == nil {
		http.Error(w, `{"error":"cluster manager unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	req, err := decodeAdminControlRequest(r, true)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}

	if state == "active" {
		expectedRoutingVersion := 0
		if h.routeRuntime != nil && h.routeRuntime.Config != nil {
			expectedRoutingVersion = h.routeRuntime.Config.Version
		}
		report, err := h.clusterMgr.CompatibilityReport(r.Context(), expectedRoutingVersion)
		if err != nil {
			http.Error(w, `{"error":"compatibility lookup failed"}`, http.StatusInternalServerError)
			return
		}
		if !report.Compatible {
			writeJSON(w, http.StatusConflict, map[string]interface{}{
				"status": "failed",
				"error":  "cluster compatibility gate failed",
				"data":   report,
			})
			return
		}
	}

	principal := PrincipalFromContext(r.Context())
	op := h.startOperation(operationKind, "node", nodeID, req.Reason, rollbackHint, principal)

	cmd := cluster.Command{
		Action:      "set_state",
		State:       state,
		Reason:      req.Reason,
		RequestedBy: principal.Subject,
		RequestedAt: time.Now(),
	}
	if nodeID == h.clusterMgr.NodeID() {
		err = h.clusterMgr.SetState(r.Context(), state, req.Reason)
	} else {
		err = h.clusterMgr.PublishCommand(r.Context(), nodeID, cmd)
	}
	if err != nil {
		h.finishOperation(w, op, nil, err, http.StatusInternalServerError)
		return
	}

	h.finishOperation(w, op, map[string]interface{}{
		"node_id": nodeID,
		"state":   state,
	}, nil, http.StatusAccepted)
}

func decodeAdminControlRequest(r *http.Request, requireReason bool) (adminControlRequest, error) {
	var req adminControlRequest
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "eof") {
			return req, err
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if requireReason && req.Reason == "" {
		return req, fmt.Errorf("reason is required")
	}
	return req, nil
}

func (h *AdminHandler) startOperation(kind, targetType, target, reason, rollbackHint string, principal AuthPrincipal) ControlOperation {
	if principal.Subject == "" {
		principal.Subject = "unknown"
	}
	if h.operations == nil {
		return ControlOperation{
			ID:           uuid.NewString(),
			Kind:         kind,
			TargetType:   targetType,
			Target:       target,
			Status:       operationStatusAccepted,
			Reason:       reason,
			RequestedBy:  principal,
			RollbackHint: rollbackHint,
			RequestedAt:  time.Now(),
		}
	}
	return h.operations.Start(kind, targetType, target, reason, rollbackHint, principal)
}

func (h *AdminHandler) finishOperation(w http.ResponseWriter, op ControlOperation, result map[string]interface{}, err error, statusCode int) {
	if h.operations != nil {
		op = h.operations.Complete(op, result, err)
	} else {
		op.Result = result
		op.CompletedAt = time.Now()
		if err != nil {
			op.Status = operationStatusFailed
			op.Error = err.Error()
		} else {
			op.Status = operationStatusCompleted
		}
	}

	payload := map[string]interface{}{
		"status":    op.Status,
		"operation": op,
	}
	writeJSON(w, statusCode, payload)
}

func writeJSON(w http.ResponseWriter, statusCode int, payload map[string]interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func sortServiceNames(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}
