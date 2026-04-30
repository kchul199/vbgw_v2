package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/cluster"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

func withURLParam(req *http.Request, key, value string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add(key, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
}

func TestGetActiveSessions_Empty(t *testing.T) {
	store := session.NewMemoryStore(10)
	handler := NewAdminHandler(store, nil, nil, nil, nil, nil, NewOperationRegistry(10))

	req := httptest.NewRequest("GET", "/api/v1/admin/sessions/active", nil)
	w := httptest.NewRecorder()
	handler.GetActiveSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["count"].(float64) != 0 {
		t.Fatalf("expected count=0, got %v", resp["count"])
	}
}

func TestGetActiveSessions_WithSessions(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore(10)

	s1 := session.NewSession("n1", "sess-1", "fs-1", "010-1111-2222", "100")
	s2 := session.NewSession("n1", "sess-2", "fs-2", "010-3333-4444", "200")
	store.AddIfUnderCapacity(ctx, s1)
	store.AddIfUnderCapacity(ctx, s2)

	handler := NewAdminHandler(store, nil, nil, nil, nil, nil, NewOperationRegistry(10))
	req := httptest.NewRequest("GET", "/api/v1/admin/sessions/active", nil)
	w := httptest.NewRecorder()
	handler.GetActiveSessions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	count := int(resp["count"].(float64))
	if count != 2 {
		t.Fatalf("expected count=2, got %d", count)
	}

	data := resp["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("expected 2 sessions in data array, got %d", len(data))
	}
}

func TestGetServiceCapacity(t *testing.T) {
	handler := NewAdminHandler(session.NewMemoryStore(10), capacity.NewManager(&routing.Config{
		Version: 1,
		Defaults: routing.Defaults{
			OnUnknownEntry: routing.UnknownStaticFallback,
		},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Capacity: routing.Capacity{
					MaxConcurrent:  2,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	}), nil, nil, nil, nil, NewOperationRegistry(10))

	req := httptest.NewRequest("GET", "/api/v1/admin/services/capacity", nil)
	w := httptest.NewRecorder()
	handler.GetServiceCapacity(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if int(resp["count"].(float64)) != 1 {
		t.Fatalf("expected count=1, got %v", resp["count"])
	}
}

func TestGetSlots(t *testing.T) {
	handler := NewAdminHandler(session.NewMemoryStore(10), capacity.NewManager(&routing.Config{
		Version: 1,
		Defaults: routing.Defaults{
			OnUnknownEntry: routing.UnknownStaticFallback,
		},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Capacity: routing.Capacity{
					MaxConcurrent:  2,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	}), nil, nil, nil, nil, NewOperationRegistry(10))

	req := httptest.NewRequest("GET", "/api/v1/admin/slots", nil)
	w := httptest.NewRecorder()
	handler.GetSlots(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if int(resp["count"].(float64)) != 2 {
		t.Fatalf("expected 2 slots, got %v", resp["count"])
	}
}

func TestGetGatewayHealth(t *testing.T) {
	gatewayStore := interconnect.NewStore()
	gatewayStore.Update(interconnect.BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", time.Now(), 90*time.Second))
	selector := interconnect.NewSelector(gatewayStore, "pbx-main", "pbx-standby", interconnect.SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})
	if _, err := selector.SelectOriginate(); err != nil {
		t.Fatalf("expected selection, got %v", err)
	}

	handler := NewAdminHandler(session.NewMemoryStore(10), nil, nil, gatewayStore, selector, nil, NewOperationRegistry(10))

	req := httptest.NewRequest("GET", "/api/v1/admin/gateways", nil)
	w := httptest.NewRecorder()
	handler.GetGatewayHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if int(resp["count"].(float64)) != 1 {
		t.Fatalf("expected count=1, got %v", resp["count"])
	}
	decisions, ok := resp["decisions"].([]interface{})
	if !ok || len(decisions) == 0 {
		t.Fatalf("expected decision history in response, got %+v", resp["decisions"])
	}
}

func TestGetQueues(t *testing.T) {
	overflowMgr := overflow.NewManager()
	now := time.Now()
	overflowMgr.Enqueue(overflow.QueueEntry{
		SessionID:      "sess-1",
		ServiceName:    "bot-main",
		QueueOnTimeout: routing.OverflowBusy,
		EnqueuedAt:     now.Add(-3 * time.Second),
		TimeoutAt:      now.Add(17 * time.Second),
	})

	handler := NewAdminHandler(session.NewMemoryStore(10), nil, overflowMgr, nil, nil, nil, NewOperationRegistry(10))
	req := httptest.NewRequest("GET", "/api/v1/admin/queues", nil)
	w := httptest.NewRecorder()
	handler.GetQueues(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if int(resp["count"].(float64)) != 1 {
		t.Fatalf("expected count=1, got %v", resp["count"])
	}
}

func TestPauseService_UpdatesControlState(t *testing.T) {
	mgr := capacity.NewManager(&routing.Config{
		Version: 1,
		Defaults: routing.Defaults{
			OnUnknownEntry: routing.UnknownStaticFallback,
		},
		Services: []routing.ServiceRoute{{
			Name:    "bot-main",
			Enabled: true,
			Capacity: routing.Capacity{
				MaxConcurrent:  1,
				Allocator:      routing.AllocatorRoundRobin,
				OverflowPolicy: routing.OverflowBusy,
			},
		}},
	})
	ops := NewOperationRegistry(10)
	handler := NewAdminHandler(session.NewMemoryStore(10), mgr, nil, nil, nil, nil, ops)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/services/bot-main/pause", strings.NewReader(`{"reason":"maintenance"}`))
	req = withURLParam(req, "name", "bot-main")
	req = req.WithContext(context.WithValue(req.Context(), principalContextKey, AuthPrincipal{Subject: "ops-user", Scope: "admin control"}))
	w := httptest.NewRecorder()
	handler.PauseService(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	snapshot, ok := mgr.ServiceSnapshot("bot-main")
	if !ok {
		t.Fatal("expected service snapshot")
	}
	if snapshot.ControlState != capacity.ControlStatePaused {
		t.Fatalf("expected paused state, got %s", snapshot.ControlState)
	}
	if len(ops.List(10)) != 1 {
		t.Fatalf("expected one operation recorded, got %d", len(ops.List(10)))
	}
}

func TestFlushQueue_KillsQueuedSessions(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore(10)
	sess := session.NewSession("node-test", "sess-1", "fs-1", "010", "1000")
	sess.SetLifecycleState(session.StateQueued, "bot-main", "capacity", time.Now(), time.Now().Add(20*time.Second))
	store.AddIfUnderCapacity(ctx, sess)

	overflowMgr := overflow.NewManager()
	now := time.Now()
	overflowMgr.Enqueue(overflow.QueueEntry{
		SessionID:      sess.SessionID,
		ServiceName:    "bot-main",
		QueueOnTimeout: routing.OverflowBusy,
		EnqueuedAt:     now.Add(-2 * time.Second),
		TimeoutAt:      now.Add(18 * time.Second),
	})

	eslMock := &mockESLCalls{}
	handler := NewAdminHandler(store, nil, overflowMgr, nil, nil, eslMock, NewOperationRegistry(10))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/queues/bot-main/flush", strings.NewReader(`{"reason":"drain queue"}`))
	req = withURLParam(req, "name", "bot-main")
	req = req.WithContext(context.WithValue(req.Context(), principalContextKey, AuthPrincipal{Subject: "ops-user", Scope: "admin control"}))
	w := httptest.NewRecorder()
	handler.FlushQueue(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if len(eslMock.killCalls) != 1 || eslMock.killCalls[0] != "fs-1" {
		t.Fatalf("expected queued call to be killed, got %+v", eslMock.killCalls)
	}
	if snapshot := overflowMgr.Snapshot("bot-main", time.Now()); snapshot.Depth != 0 {
		t.Fatalf("expected queue to be empty, got %+v", snapshot)
	}
}

func TestSetGatewayStandby(t *testing.T) {
	gatewayStore := interconnect.NewStore()
	gatewayStore.Update(interconnect.BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", time.Now(), 90*time.Second))
	handler := NewAdminHandler(session.NewMemoryStore(10), nil, nil, gatewayStore, nil, nil, NewOperationRegistry(10))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gateways/pbx-main/standby", strings.NewReader(`{"reason":"maintenance"}`))
	req = withURLParam(req, "name", "pbx-main")
	req = req.WithContext(context.WithValue(req.Context(), principalContextKey, AuthPrincipal{Subject: "ops-user", Scope: "admin control"}))
	w := httptest.NewRecorder()
	handler.SetGatewayStandby(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	state, ok := gatewayStore.Get("pbx-main")
	if !ok {
		t.Fatal("expected gateway state")
	}
	if state.OperatorState != "standby" {
		t.Fatalf("expected operator standby, got %+v", state)
	}
}

func TestGetClusterNodesAndCompatibility(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgrA := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	mgrB := cluster.NewManager(client, "node-b", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "8.0.0",
		LeaseSchemaVersion:  2,
		RoutingVersion:      func() int { return 2 },
	})
	mgrA.Start(ctx)
	mgrB.Start(ctx)
	time.Sleep(120 * time.Millisecond)

	handler := NewAdminHandler(
		session.NewMemoryStore(10),
		nil,
		nil,
		nil,
		nil,
		nil,
		NewOperationRegistry(10),
		&routing.Runtime{Config: &routing.Config{Version: 1}},
	)
	handler.SetClusterManager(mgrA)

	nodesReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/cluster/nodes", nil)
	nodesW := httptest.NewRecorder()
	handler.GetClusterNodes(nodesW, nodesReq)
	if nodesW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", nodesW.Code, nodesW.Body.String())
	}
	var nodesResp map[string]interface{}
	if err := json.NewDecoder(nodesW.Body).Decode(&nodesResp); err != nil {
		t.Fatalf("decode nodes response: %v", err)
	}
	if int(nodesResp["count"].(float64)) < 2 {
		t.Fatalf("expected at least 2 cluster nodes, got %v", nodesResp["count"])
	}

	compatReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/cluster/compatibility", nil)
	compatW := httptest.NewRecorder()
	handler.GetClusterCompatibility(compatW, compatReq)
	if compatW.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", compatW.Code, compatW.Body.String())
	}
	var compatResp struct {
		Status string                      `json:"status"`
		Data   cluster.CompatibilityReport `json:"data"`
	}
	if err := json.NewDecoder(compatW.Body).Decode(&compatResp); err != nil {
		t.Fatalf("decode compatibility response: %v", err)
	}
	if compatResp.Data.Compatible {
		t.Fatalf("expected compatibility mismatch report, got %+v", compatResp.Data)
	}
	if len(compatResp.Data.Issues) == 0 {
		t.Fatalf("expected compatibility issues, got %+v", compatResp.Data)
	}
}

func TestDrainNodeAndResumeNode(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	mgr.Start(ctx)
	time.Sleep(80 * time.Millisecond)

	handler := NewAdminHandler(
		session.NewMemoryStore(10),
		nil,
		nil,
		nil,
		nil,
		nil,
		NewOperationRegistry(10),
		&routing.Runtime{Config: &routing.Config{Version: 1}},
	)
	handler.SetClusterManager(mgr)

	drainReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/cluster/nodes/node-a/drain", strings.NewReader(`{"reason":"rolling deploy"}`))
	drainReq = withURLParam(drainReq, "id", "node-a")
	drainReq = drainReq.WithContext(context.WithValue(drainReq.Context(), principalContextKey, AuthPrincipal{Subject: "ops-user", Scope: "admin control"}))
	drainW := httptest.NewRecorder()
	handler.DrainNode(drainW, drainReq)
	if drainW.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", drainW.Code, drainW.Body.String())
	}
	if got := mgr.CurrentState(); got != cluster.NodeStateDraining {
		t.Fatalf("expected node to be draining, got %q", got)
	}

	resumeReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/cluster/nodes/node-a/resume", strings.NewReader(`{"reason":"deploy complete"}`))
	resumeReq = withURLParam(resumeReq, "id", "node-a")
	resumeReq = resumeReq.WithContext(context.WithValue(resumeReq.Context(), principalContextKey, AuthPrincipal{Subject: "ops-user", Scope: "admin control"}))
	resumeW := httptest.NewRecorder()
	handler.ResumeNode(resumeW, resumeReq)
	if resumeW.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", resumeW.Code, resumeW.Body.String())
	}
	if got := mgr.CurrentState(); got != cluster.NodeStateActive {
		t.Fatalf("expected node to be active, got %q", got)
	}
}

func TestReloadRoutingConfig_CompatibilityGate(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgrA := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	mgrB := cluster.NewManager(client, "node-b", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "8.0.0",
		LeaseSchemaVersion:  2,
		RoutingVersion:      func() int { return 1 },
	})
	mgrA.Start(ctx)
	mgrB.Start(ctx)
	time.Sleep(120 * time.Millisecond)

	path := filepath.Join(t.TempDir(), "routing.yaml")
	content := []byte(`version: 1
defaults:
  on_unknown_entry: static_fallback
services:
  - name: bot-main-dev
    enabled: true
    route_type: ai
    entry_numbers: ["9296"]
    match:
      ingress_stages: ["default-policy"]
    capacity:
      backend: logical
      max_concurrent: 1
      allocator: round_robin
      overflow_policy: busy
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write routing config: %v", err)
	}

	runtime := &routing.Runtime{
		Path:     path,
		Config:   &routing.Config{Version: 1},
		Resolver: routing.NewResolver(&routing.Config{Version: 1}),
	}
	handler := NewAdminHandler(
		session.NewMemoryStore(10),
		nil,
		nil,
		nil,
		nil,
		nil,
		NewOperationRegistry(10),
		runtime,
	)
	handler.SetClusterManager(mgrA)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/config/reload", nil)
	w := httptest.NewRecorder()
	handler.ReloadRoutingConfig(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if runtime.Config.Version != 1 {
		t.Fatalf("expected runtime version to remain 1, got %d", runtime.Config.Version)
	}
}
