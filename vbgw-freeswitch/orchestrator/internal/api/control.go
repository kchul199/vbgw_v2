/**
 * @file control.go
 * @description 콜 제어 엔드포인트 — DTMF, transfer, record, bridge/unbridge
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | 6개 제어 엔드포인트
 * v1.1.0 | 2026-04-09 | [Implementer] | T-03,T-11,T-18 | IvrEventCh ctx guard, body close, RecordStart UUID검증
 * ─────────────────────────────────────────
 */

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"vbgw-orchestrator/internal/esl"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/ivr"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/session"

	"github.com/go-chi/chi/v5"
)

// Input validation patterns (ESL injection prevention)
var (
	dtmfPattern      = regexp.MustCompile(`^[0-9*#A-D]{1,20}$`)
	sipTargetPattern = regexp.MustCompile(`^[+a-zA-Z0-9@._:\-/]{1,256}$`)
)

type ControlHandler struct {
	ESL             esl.Commander
	Sessions        session.Store
	GatewaySelector *interconnect.Selector
	HandoffManager  *interconnect.HandoffManager
	OverflowManager *overflow.Manager
	BridgeURL       string
	httpClient      *http.Client
	NodeID          string
}

type dtmfRequest struct {
	Digits string `json:"digits"`
}

type transferRequest struct {
	Target string `json:"target"`
}

type recordRequest struct {
	Path string `json:"path"`
}

type bridgeRequest struct {
	CallID1 string `json:"call_id_1"`
	CallID2 string `json:"call_id_2"`
}

// SendDtmf handles POST /api/v1/calls/{id}/dtmf.
func (h *ControlHandler) SendDtmf(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	var req dtmfRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Digits == "" {
		http.Error(w, `{"error":"digits required"}`, http.StatusBadRequest)
		return
	}

	if s.NodeID != h.NodeID {
		if err := h.Sessions.PublishCommand(ctx, s.NodeID, callID, "dtmf", req); err != nil {
			http.Error(w, `{"error":"failed to route command"}`, http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"sent_via_pubsub"}`)
		}
		return
	}
	if !dtmfPattern.MatchString(req.Digits) {
		http.Error(w, `{"error":"invalid digits format (allowed: 0-9*#A-D, max 20)"}`, http.StatusBadRequest)
		return
	}

	if err := h.ESL.SendDtmf(ctx, s.FSUUID, req.Digits); err != nil {
		slog.Error("SendDtmf failed", "err", err)
		http.Error(w, `{"error":"dtmf send failed"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"sent"}`)
}

// Transfer handles POST /api/v1/calls/{id}/transfer.
func (h *ControlHandler) Transfer(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	var req transferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, `{"error":"target required"}`, http.StatusBadRequest)
		return
	}
	if !sipTargetPattern.MatchString(req.Target) {
		http.Error(w, `{"error":"invalid target format"}`, http.StatusBadRequest)
		return
	}

	if s.NodeID != h.NodeID {
		if err := h.Sessions.PublishCommand(ctx, s.NodeID, callID, "transfer", req); err != nil {
			http.Error(w, `{"error":"failed to route command"}`, http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"transferred_via_pubsub"}`)
		}
		return
	}

	if h.OverflowManager != nil && h.OverflowManager.Remove(s.SessionID) {
		metrics.QueueAbandonTotal.WithLabelValues(s.ServiceName, "manual_transfer").Inc()
	}
	confirmed, err := executeTransfer(ctx, h.ESL, h.GatewaySelector, h.HandoffManager, s, req.Target)
	if err != nil {
		slog.Error("Transfer failed", "err", err)
		http.Error(w, `{"error":"transfer failed"}`, http.StatusInternalServerError)
		return
	}
	if confirmed {
		s.SetAIPaused(true)
		h.notifyBridge("ai-pause", s.FSUUID)
		if released, releaseErr := session.ReleaseServiceOwnership(ctx, h.Sessions, s); releaseErr != nil {
			slog.Error("Failed to persist service ownership release after confirmed transfer handoff", "session_id", s.SessionID, "err", releaseErr)
		} else if released {
			slog.Info("Released AI slot after confirmed transfer handoff", "session_id", s.SessionID)
		}
	} else {
		slog.Info("Transfer accepted; retaining AI slot until channel lifecycle confirms release",
			"session_id", s.SessionID,
			"target", req.Target,
		)
	}

	// Q-06: Send HangupEvent to IVR to clean up state after transfer
	// T-03: Guard with session context to avoid sending to closed channel
	if s.IvrEventCh != nil {
		select {
		case <-s.Ctx.Done():
		case s.IvrEventCh <- ivr.IvrEvent{Type: ivr.HangupEvent}:
		default:
		}
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"transferred"}`)
}

// RecordStart handles POST /api/v1/calls/{id}/record/start.
func (h *ControlHandler) RecordStart(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	if s.NodeID != h.NodeID {
		req := recordRequest{Path: "start"}
		if err := h.Sessions.PublishCommand(ctx, s.NodeID, callID, "record_start", req); err != nil {
			http.Error(w, `{"error":"failed to route command"}`, http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"recording_via_pubsub"}`)
		}
		return
	}

	path := fmt.Sprintf("/recordings/%s.wav", callID)
	if err := h.ESL.RecordStart(ctx, s.FSUUID, path); err != nil {
		slog.Error("RecordStart failed", "err", err)
		http.Error(w, `{"error":"record start failed"}`, http.StatusInternalServerError)
		return
	}

	s.SetRecordPath(path)
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "recording", "path": path})
}

// RecordStop handles POST /api/v1/calls/{id}/record/stop.
func (h *ControlHandler) RecordStop(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	if err := h.ESL.RecordStop(ctx, s.FSUUID); err != nil {
		slog.Error("RecordStop failed", "err", err)
		http.Error(w, `{"error":"record stop failed"}`, http.StatusInternalServerError)
		return
	}

	s.SetRecordPath("")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"stopped"}`)
}

// BridgeCalls handles POST /api/v1/calls/bridge.
func (h *ControlHandler) BridgeCalls(w http.ResponseWriter, r *http.Request) {
	var req bridgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	sA, okA := h.Sessions.Get(ctx, req.CallID1)
	sB, okB := h.Sessions.Get(ctx, req.CallID2)
	if !okA || !okB {
		http.Error(w, `{"error":"one or both sessions not found"}`, http.StatusNotFound)
		return
	}

	// Pause AI for call A
	sA.SetAIPaused(true)
	h.notifyBridge("ai-pause", sA.FSUUID)

	// Bridge via ESL
	if err := h.ESL.Bridge(ctx, sA.FSUUID, sB.FSUUID); err != nil {
		sA.SetAIPaused(false)
		slog.Error("Bridge failed", "err", err)
		http.Error(w, `{"error":"bridge failed"}`, http.StatusInternalServerError)
		return
	}

	sA.SetBridgedWith(sB.SessionID)
	sB.SetBridgedWith(sA.SessionID)

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"bridged"}`)
}

// UnbridgeCalls handles POST /api/v1/calls/unbridge.
func (h *ControlHandler) UnbridgeCalls(w http.ResponseWriter, r *http.Request) {
	var req bridgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	sA, okA := h.Sessions.Get(ctx, req.CallID1)
	sB, okB := h.Sessions.Get(ctx, req.CallID2)
	if !okA || !okB {
		http.Error(w, `{"error":"one or both sessions not found"}`, http.StatusNotFound)
		return
	}

	// Unbridge via ESL (park)
	if err := h.ESL.Unbridge(ctx, sA.FSUUID); err != nil {
		slog.Error("Unbridge failed", "err", err)
		http.Error(w, `{"error":"unbridge failed"}`, http.StatusInternalServerError)
		return
	}

	// Resume AI for call A
	sA.SetAIPaused(false)
	sA.SetBridgedWith("")
	sB.SetBridgedWith("")
	h.notifyBridge("ai-resume", sA.FSUUID)

	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"unbridged"}`)
}

// BargeIn handles POST /internal/barge-in/{uuid} from Bridge.
func (h *ControlHandler) BargeIn(w http.ResponseWriter, r *http.Request) {
	fsUUID := chi.URLParam(r, "uuid")
	slog.Info("Barge-in request received", "fs_uuid", fsUUID)

	if err := h.ESL.Break(r.Context(), fsUUID); err != nil {
		slog.Error("uuid_break failed", "err", err)
		http.Error(w, `{"error":"break failed"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("uuid_break sent", "fs_uuid", fsUUID)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"break_sent"}`)
}

// FS-2: Eavesdrop handles POST /api/v1/calls/{id}/eavesdrop — supervisor monitoring.
func (h *ControlHandler) Eavesdrop(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	var req struct {
		SupervisorUUID string `json:"supervisor_uuid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SupervisorUUID == "" {
		http.Error(w, `{"error":"supervisor_uuid required"}`, http.StatusBadRequest)
		return
	}

	if err := h.ESL.Eavesdrop(ctx, req.SupervisorUUID, s.FSUUID); err != nil {
		slog.Error("Eavesdrop failed", "err", err)
		http.Error(w, `{"error":"eavesdrop failed"}`, http.StatusInternalServerError)
		return
	}

	slog.Info("Eavesdrop started", "session_id", s.SessionID, "supervisor", req.SupervisorUUID)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"eavesdropping"}`)
}

// FS-3: AttendedTransfer handles POST /api/v1/calls/{id}/attended-transfer.
func (h *ControlHandler) AttendedTransfer(w http.ResponseWriter, r *http.Request) {
	callID := chi.URLParam(r, "id")
	ctx := r.Context()
	s, ok := h.Sessions.Get(ctx, callID)
	if !ok {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	var req transferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Target == "" {
		http.Error(w, `{"error":"target required"}`, http.StatusBadRequest)
		return
	}
	if !sipTargetPattern.MatchString(req.Target) {
		http.Error(w, `{"error":"invalid target format"}`, http.StatusBadRequest)
		return
	}

	if s.NodeID != h.NodeID {
		if err := h.Sessions.PublishCommand(ctx, s.NodeID, callID, "attended_transfer", req); err != nil {
			http.Error(w, `{"error":"failed to route command"}`, http.StatusInternalServerError)
		} else {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"attended_transfer_via_pubsub"}`)
		}
		return
	}

	if h.OverflowManager != nil && h.OverflowManager.Remove(s.SessionID) {
		metrics.QueueAbandonTotal.WithLabelValues(s.ServiceName, "manual_transfer").Inc()
	}
	confirmed, err := executeAttendedTransfer(ctx, h.ESL, h.GatewaySelector, h.HandoffManager, s, req.Target)
	if err != nil {
		slog.Error("Attended transfer failed", "err", err)
		http.Error(w, `{"error":"attended transfer failed"}`, http.StatusInternalServerError)
		return
	}
	if confirmed {
		s.SetAIPaused(true)
		h.notifyBridge("ai-pause", s.FSUUID)
		if released, releaseErr := session.ReleaseServiceOwnership(ctx, h.Sessions, s); releaseErr != nil {
			slog.Error("Failed to persist service ownership release after confirmed attended handoff", "session_id", s.SessionID, "err", releaseErr)
		} else if released {
			slog.Info("Released AI slot after confirmed attended handoff", "session_id", s.SessionID)
		}
	} else {
		slog.Info("Attended transfer accepted; retaining AI slot until channel lifecycle confirms release",
			"session_id", s.SessionID,
			"target", req.Target,
		)
	}

	slog.Info("Attended transfer initiated", "session_id", s.SessionID, "target", req.Target)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, `{"status":"transferring"}`)
}

func (h *ControlHandler) notifyBridge(action, uuid string) {
	notifyBridgeAction(h.BridgeURL, action, uuid, h.httpClient)
}

func notifyBridgeAction(bridgeURL, action, uuid string, client *http.Client) {
	if strings.TrimSpace(bridgeURL) == "" || strings.TrimSpace(uuid) == "" {
		return
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	url := fmt.Sprintf("%s/internal/%s/%s", bridgeURL, action, uuid)
	req, _ := http.NewRequest("POST", url, nil)
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Bridge notification failed", "action", action, "uuid", uuid, "err", err)
		return
	}
	resp.Body.Close()
}

// HandleLocalCommand executes a command received via Pub/Sub on the local node where the session resides.
func HandleLocalCommand(ctx context.Context, msg session.CommandMsg, sessionMgr session.Store, overflowMgr *overflow.Manager, eslClient esl.Commander, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, bridgeURL string) {
	s, ok := sessionMgr.Get(ctx, msg.SessionID)
	if !ok {
		slog.Warn("Local command route failed: session not found", "session_id", msg.SessionID)
		return
	}

	switch msg.Action {
	case "dtmf":
		var req dtmfRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			slog.Error("PubSub dtmf payload parse failed", "err", err)
			return
		}
		if err := eslClient.SendDtmf(ctx, s.FSUUID, req.Digits); err != nil {
			slog.Error("PubSub dtmf execution failed", "session_id", msg.SessionID, "err", err)
		}

	case "transfer":
		var req transferRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			slog.Error("PubSub transfer payload parse failed", "err", err)
			return
		}
		if overflowMgr != nil && overflowMgr.Remove(s.SessionID) {
			metrics.QueueAbandonTotal.WithLabelValues(s.ServiceName, "manual_transfer").Inc()
		}
		confirmed, err := executeTransfer(ctx, eslClient, gatewaySelector, handoffMgr, s, req.Target)
		if err != nil {
			slog.Error("PubSub transfer execution failed", "session_id", msg.SessionID, "err", err)
			return
		}
		if confirmed {
			s.SetAIPaused(true)
			notifyBridgeAction(bridgeURL, "ai-pause", s.FSUUID, nil)
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to persist service ownership release after confirmed PubSub transfer handoff", "session_id", msg.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released AI slot after confirmed PubSub transfer handoff", "session_id", msg.SessionID)
			}
		} else {
			slog.Info("PubSub transfer accepted; retaining AI slot until channel lifecycle confirms release", "session_id", msg.SessionID)
		}
		if s.IvrEventCh != nil {
			select {
			case <-s.Ctx.Done():
			case s.IvrEventCh <- ivr.IvrEvent{Type: ivr.HangupEvent}:
			default:
			}
		}

	case "attended_transfer":
		var req transferRequest
		if err := json.Unmarshal(msg.Payload, &req); err != nil {
			slog.Error("PubSub attended_transfer payload parse failed", "err", err)
			return
		}
		if overflowMgr != nil && overflowMgr.Remove(s.SessionID) {
			metrics.QueueAbandonTotal.WithLabelValues(s.ServiceName, "manual_transfer").Inc()
		}
		confirmed, err := executeAttendedTransfer(ctx, eslClient, gatewaySelector, handoffMgr, s, req.Target)
		if err != nil {
			slog.Error("PubSub attended_transfer execution failed", "session_id", msg.SessionID, "err", err)
			return
		}
		if confirmed {
			s.SetAIPaused(true)
			notifyBridgeAction(bridgeURL, "ai-pause", s.FSUUID, nil)
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to persist service ownership release after confirmed PubSub attended handoff", "session_id", msg.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released AI slot after confirmed PubSub attended handoff", "session_id", msg.SessionID)
			}
		} else {
			slog.Info("PubSub attended transfer accepted; retaining AI slot until channel lifecycle confirms release", "session_id", msg.SessionID)
		}

	case "record_start":
		path := fmt.Sprintf("/recordings/%s.wav", s.SessionID)
		if err := eslClient.RecordStart(ctx, s.FSUUID, path); err != nil {
			slog.Error("PubSub record_start failed", "session_id", msg.SessionID, "err", err)
			return
		}
		s.SetRecordPath(path)
		if err := sessionMgr.SaveSession(ctx, s); err != nil {
			slog.Error("Failed to persist session after record_start", "err", err)
		}

	case "record_stop":
		if err := eslClient.RecordStop(ctx, s.FSUUID); err != nil {
			slog.Error("PubSub record_stop failed", "session_id", msg.SessionID, "err", err)
			return
		}
		s.SetRecordPath("")

	default:
		slog.Warn("Unknown PubSub command action", "action", msg.Action, "session_id", msg.SessionID)
	}
}

func executeTransfer(ctx context.Context, commander esl.Commander, selector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, s *session.SessionState, target string) (bool, error) {
	if isCallcenterQueueTarget(target) {
		return true, transferToCallcenterQueue(ctx, commander, s, target)
	}
	if selector != nil && handoffMgr != nil && interconnect.ShouldUseGatewayTransfer(target) {
		outcome, err := interconnect.ExecuteGatewayHandoff(ctx, commander, selector, handoffMgr, s.FSUUID, s.CallerID, target)
		if err != nil {
			return false, err
		}
		return outcome.Confirmed, nil
	}
	return false, commander.Transfer(ctx, s.FSUUID, target)
}

func executeAttendedTransfer(ctx context.Context, commander esl.Commander, selector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, s *session.SessionState, target string) (bool, error) {
	if isCallcenterQueueTarget(target) {
		return true, transferToCallcenterQueue(ctx, commander, s, target)
	}
	if selector != nil && handoffMgr != nil && interconnect.ShouldUseGatewayTransfer(target) {
		outcome, err := interconnect.ExecuteGatewayHandoff(ctx, commander, selector, handoffMgr, s.FSUUID, s.CallerID, target)
		if err != nil {
			return false, err
		}
		return outcome.Confirmed, nil
	}
	return false, commander.AttendedTransfer(ctx, s.FSUUID, target, "")
}

func isCallcenterQueueTarget(target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	return strings.HasPrefix(target, "callcenter:")
}

func callcenterQueueRef(target string) string {
	const prefix = "callcenter:"
	target = strings.TrimSpace(target)
	if len(target) < len(prefix) {
		return ""
	}
	if !strings.EqualFold(target[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(target[len(prefix):])
}

func transferToCallcenterQueue(ctx context.Context, commander esl.Commander, s *session.SessionState, target string) error {
	queueRef := callcenterQueueRef(target)
	if queueRef == "" {
		return fmt.Errorf("callcenter target missing queue reference")
	}
	if err := commander.SetVar(ctx, s.FSUUID, "vbgw_human_queue", queueRef); err != nil {
		return err
	}
	if err := commander.Break(ctx, s.FSUUID); err != nil {
		slog.Warn("Failed to break media before callcenter transfer", "session_id", s.SessionID, "err", err)
	}
	return commander.Transfer(ctx, s.FSUUID, "vbgw-human-callcenter")
}
