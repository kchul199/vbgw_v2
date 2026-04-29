package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"
)

func TestGetActiveSessions_Empty(t *testing.T) {
	store := session.NewMemoryStore(10)
	handler := NewAdminHandler(store, nil, nil, nil, nil)

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

	handler := NewAdminHandler(store, nil, nil, nil, nil)
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
	}), nil, nil, nil)

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
	}), nil, nil, nil)

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

	handler := NewAdminHandler(session.NewMemoryStore(10), nil, nil, gatewayStore, selector)

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

	handler := NewAdminHandler(session.NewMemoryStore(10), nil, overflowMgr, nil, nil)
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
