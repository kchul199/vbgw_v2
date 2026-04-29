package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"

	"github.com/go-chi/chi/v5"
)

func newControlTestRouter(h *ControlHandler) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/api/v1/calls/{id}/dtmf", h.SendDtmf)
	r.Post("/api/v1/calls/{id}/transfer", h.Transfer)
	r.Post("/api/v1/calls/{id}/attended-transfer", h.AttendedTransfer)
	r.Post("/api/v1/calls/{id}/record/start", h.RecordStart)
	r.Post("/api/v1/calls/{id}/record/stop", h.RecordStop)
	r.Post("/api/v1/calls/bridge", h.BridgeCalls)
	r.Post("/api/v1/calls/unbridge", h.UnbridgeCalls)
	r.Post("/internal/barge-in/{uuid}", h.BargeIn)
	return r
}

func TestSendDtmf_Success(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-1", "fs-1", "010", "1001")
	sessions.AddIfUnderCapacity(context.Background(), s)

	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer bridgeSrv.Close()

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		BridgeURL:  bridgeSrv.URL,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"digits":"123#"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-1/dtmf", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSendDtmf_SessionNotFound(t *testing.T) {
	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   session.NewMemoryStore(100),
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"digits":"1"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/nonexistent/dtmf", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestSendDtmf_InvalidDigits(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-1", "fs-1", "010", "1001")
	sessions.AddIfUnderCapacity(context.Background(), s)

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"digits":"invalid!@"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-1/dtmf", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestTransfer_Success(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-1", "fs-1", "010", "1001")
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1001"},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})
	if decision := mgr.Admit(s.SessionID, "bot-main"); !decision.Allowed {
		t.Fatalf("expected lease for transfer test, got %+v", decision)
	}
	s.SetOnRelease(mgr.Release)
	s.SetRoutingMetadata("1001", "bot-main", "pbx-main", "default-policy", routing.RouteTypeAI, 1)
	s.SetCapacityMetadata("bot-main-01", session.AllocationLeased, "", "")
	sessions.AddIfUnderCapacity(context.Background(), s)

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"target":"1000@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-1/transfer", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, ok := sessions.Get(context.Background(), "call-1")
	if !ok {
		t.Fatal("session not found after transfer")
	}
	if updated.AllocationState != session.AllocationLeased {
		t.Fatalf("expected allocation to remain leased until hangup, got %q", updated.AllocationState)
	}
	if updated.SlotID == "" {
		t.Fatalf("expected slot_id to remain assigned until hangup")
	}
	if snapshots := mgr.Snapshots(); len(snapshots) != 1 || snapshots[0].ActiveCalls != 1 {
		t.Fatalf("expected slot to remain leased until hangup, got %+v", snapshots)
	}
}

func TestAttendedTransfer_ReleasesLeasedSlot(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-2", "fs-2", "010", "1001")
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1001"},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})
	if decision := mgr.Admit(s.SessionID, "bot-main"); !decision.Allowed {
		t.Fatalf("expected lease for attended transfer test, got %+v", decision)
	}
	s.SetOnRelease(mgr.Release)
	s.SetRoutingMetadata("1001", "bot-main", "pbx-main", "default-policy", routing.RouteTypeAI, 1)
	s.SetCapacityMetadata("bot-main-01", session.AllocationLeased, "", "")
	sessions.AddIfUnderCapacity(context.Background(), s)

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"target":"2000@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-2/attended-transfer", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	updated, ok := sessions.Get(context.Background(), "call-2")
	if !ok {
		t.Fatal("session not found after attended transfer")
	}
	if updated.AllocationState != session.AllocationLeased {
		t.Fatalf("expected allocation to remain leased until hangup, got %q", updated.AllocationState)
	}
	if snapshots := mgr.Snapshots(); len(snapshots) != 1 || snapshots[0].ActiveCalls != 1 {
		t.Fatalf("expected slot to remain leased until hangup, got %+v", snapshots)
	}
}

func TestTransfer_GatewayHandoffFallsBackToStandbyAndReleasesSlotOnConfirmedBridge(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-3", "fs-3", "010", "1001")
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{{
			Name:      "bot-main",
			Enabled:   true,
			RouteType: routing.RouteTypeAI,
			EntryNums: []string{"1001"},
			Capacity: routing.Capacity{
				MaxConcurrent:  1,
				Allocator:      routing.AllocatorRoundRobin,
				OverflowPolicy: routing.OverflowBusy,
			},
		}},
	})
	if decision := mgr.Admit(s.SessionID, "bot-main"); !decision.Allowed {
		t.Fatalf("expected lease for gateway handoff test, got %+v", decision)
	}
	s.SetOnRelease(mgr.Release)
	s.SetRoutingMetadata("1001", "bot-main", "pbx-main", "default-policy", routing.RouteTypeAI, 1)
	s.SetCapacityMetadata("bot-main-01", session.AllocationLeased, "", "")
	sessions.AddIfUnderCapacity(context.Background(), s)

	store := interconnect.NewStore()
	now := time.Now()
	store.Update(interconnect.BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", now, 90*time.Second))
	store.Update(interconnect.BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))
	selector := interconnect.NewSelector(store, "pbx-main", "pbx-standby", interconnect.SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})
	handoffMgr := interconnect.NewHandoffManager()

	eslMock := &mockESLCalls{
		originateHook: func(uuid string, gatewayOrder []string) {
			if len(gatewayOrder) == 0 {
				t.Fatalf("expected gateway order for handoff originate")
			}
			switch gatewayOrder[0] {
			case "pbx-main":
				handoffMgr.ResolveFailed(uuid, "pbx-main", "NORMAL_TEMPORARY_FAILURE", "503")
			case "pbx-standby":
				handoffMgr.ResolveAnswered(uuid)
			default:
				t.Fatalf("unexpected gateway %q", gatewayOrder[0])
			}
		},
	}
	h := &ControlHandler{
		NodeID:          "node-test",
		ESL:             eslMock,
		Sessions:        sessions,
		GatewaySelector: selector,
		HandoffManager:  handoffMgr,
		httpClient:      &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"target":"+82105551234"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-3/transfer", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(eslMock.originateCalls) != 2 {
		t.Fatalf("expected two originate attempts, got %+v", eslMock.originateCalls)
	}
	updated, ok := sessions.Get(context.Background(), "call-3")
	if !ok {
		t.Fatal("session not found after gateway handoff")
	}
	if updated.AllocationState != session.AllocationReleased {
		t.Fatalf("expected allocation released after confirmed handoff, got %q", updated.AllocationState)
	}
	if snapshots := mgr.Snapshots(); len(snapshots) != 1 || snapshots[0].ActiveCalls != 0 {
		t.Fatalf("expected slot to be released after confirmed handoff, got %+v", snapshots)
	}
}

func TestAttendedTransfer_GatewayHandoffFallsBackToStandbyAndReleasesSlotOnConfirmedBridge(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-4", "fs-4", "010", "1001")
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{{
			Name:      "bot-main",
			Enabled:   true,
			RouteType: routing.RouteTypeAI,
			EntryNums: []string{"1001"},
			Capacity: routing.Capacity{
				MaxConcurrent:  1,
				Allocator:      routing.AllocatorRoundRobin,
				OverflowPolicy: routing.OverflowBusy,
			},
		}},
	})
	if decision := mgr.Admit(s.SessionID, "bot-main"); !decision.Allowed {
		t.Fatalf("expected lease for attended gateway handoff test, got %+v", decision)
	}
	s.SetOnRelease(mgr.Release)
	s.SetRoutingMetadata("1001", "bot-main", "pbx-main", "default-policy", routing.RouteTypeAI, 1)
	s.SetCapacityMetadata("bot-main-01", session.AllocationLeased, "", "")
	sessions.AddIfUnderCapacity(context.Background(), s)

	store := interconnect.NewStore()
	now := time.Now()
	store.Update(interconnect.BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", now, 90*time.Second))
	store.Update(interconnect.BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))
	selector := interconnect.NewSelector(store, "pbx-main", "pbx-standby", interconnect.SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})
	handoffMgr := interconnect.NewHandoffManager()

	eslMock := &mockESLCalls{
		originateHook: func(uuid string, gatewayOrder []string) {
			if len(gatewayOrder) == 0 {
				t.Fatalf("expected gateway order for handoff originate")
			}
			switch gatewayOrder[0] {
			case "pbx-main":
				handoffMgr.ResolveFailed(uuid, "pbx-main", "NORMAL_TEMPORARY_FAILURE", "503")
			case "pbx-standby":
				handoffMgr.ResolveAnswered(uuid)
			default:
				t.Fatalf("unexpected gateway %q", gatewayOrder[0])
			}
		},
	}
	h := &ControlHandler{
		NodeID:          "node-test",
		ESL:             eslMock,
		Sessions:        sessions,
		GatewaySelector: selector,
		HandoffManager:  handoffMgr,
		httpClient:      &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"target":"2000@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/call-4/attended-transfer", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(eslMock.originateCalls) != 2 {
		t.Fatalf("expected two originate attempts, got %+v", eslMock.originateCalls)
	}
	updated, ok := sessions.Get(context.Background(), "call-4")
	if !ok {
		t.Fatal("session not found after attended gateway handoff")
	}
	if updated.AllocationState != session.AllocationReleased {
		t.Fatalf("expected allocation released after confirmed attended handoff, got %q", updated.AllocationState)
	}
	if snapshots := mgr.Snapshots(); len(snapshots) != 1 || snapshots[0].ActiveCalls != 0 {
		t.Fatalf("expected slot to be released after confirmed attended handoff, got %+v", snapshots)
	}
}

func TestRecordStart_Success(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	s := session.NewSession("node-test", "call-1", "fs-1", "010", "1001")
	sessions.AddIfUnderCapacity(context.Background(), s)

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	req := httptest.NewRequest("POST", "/api/v1/calls/call-1/record/start", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBridgeCalls_Success(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	sA := session.NewSession("node-test", "call-A", "fs-A", "010", "1001")
	sB := session.NewSession("node-test", "call-B", "fs-B", "020", "1002")
	sessions.AddIfUnderCapacity(context.Background(), sA)
	sessions.AddIfUnderCapacity(context.Background(), sB)

	bridgeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer bridgeSrv.Close()

	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   sessions,
		BridgeURL:  bridgeSrv.URL,
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"call_id_1":"call-A","call_id_2":"call-B"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/bridge", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBridgeCalls_SessionNotFound(t *testing.T) {
	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   session.NewMemoryStore(100),
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	body := `{"call_id_1":"none-A","call_id_2":"none-B"}`
	req := httptest.NewRequest("POST", "/api/v1/calls/bridge", strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestBargeIn_Success(t *testing.T) {
	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{},
		Sessions:   session.NewMemoryStore(100),
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	req := httptest.NewRequest("POST", "/internal/barge-in/fs-uuid-123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestBargeIn_ESLFailure(t *testing.T) {
	h := &ControlHandler{
		NodeID:     "node-test",
		ESL:        &mockESLCalls{breakErr: fmt.Errorf("ESL timeout")},
		Sessions:   session.NewMemoryStore(100),
		httpClient: &http.Client{Timeout: 2 * time.Second},
	}
	router := newControlTestRouter(h)

	req := httptest.NewRequest("POST", "/internal/barge-in/fs-uuid-123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
