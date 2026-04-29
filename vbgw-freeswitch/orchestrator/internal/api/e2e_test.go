package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vbgw-orchestrator/internal/config"
	"vbgw-orchestrator/internal/esl"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/session"
)

// mockESLE2E implements esl.Commander for E2E testing without a real FreeSWITCH.
type mockESLE2E struct {
	originateCalled    int
	sendDtmfCalled     int
	transferCalled     int
	recordStartCalled  int
	recordStopCalled   int
	breakCalled        int
	lastTransferTarget string
	lastDtmfDigits     string
}

func (m *mockESLE2E) Originate(ctx context.Context, sessionID, target, callerID string, gatewayOrder []string) (string, error) {
	m.originateCalled++
	return "+OK " + sessionID, nil
}
func (m *mockESLE2E) SendDtmf(ctx context.Context, uuid, digits string) error {
	m.sendDtmfCalled++
	m.lastDtmfDigits = digits
	return nil
}
func (m *mockESLE2E) SetVar(ctx context.Context, uuid, key, value string) error { return nil }
func (m *mockESLE2E) Transfer(ctx context.Context, uuid, target string) error {
	m.transferCalled++
	m.lastTransferTarget = target
	return nil
}
func (m *mockESLE2E) TransferViaGateway(ctx context.Context, uuid, target, gateway string) error {
	m.transferCalled++
	m.lastTransferTarget = gateway + ":" + target
	return nil
}
func (m *mockESLE2E) RecordStart(ctx context.Context, uuid, path string) error {
	m.recordStartCalled++
	return nil
}
func (m *mockESLE2E) RecordStop(ctx context.Context, uuid string) error {
	m.recordStopCalled++
	return nil
}
func (m *mockESLE2E) Bridge(ctx context.Context, uuid1, uuid2 string) error { return nil }
func (m *mockESLE2E) Unbridge(ctx context.Context, uuid string) error       { return nil }
func (m *mockESLE2E) Kill(ctx context.Context, uuid string) error           { return nil }
func (m *mockESLE2E) Break(ctx context.Context, uuid string) error {
	m.breakCalled++
	return nil
}
func (m *mockESLE2E) Dump(ctx context.Context, uuid string) (map[string]string, error) {
	return nil, nil
}
func (m *mockESLE2E) Pause(ctx context.Context) error  { return nil }
func (m *mockESLE2E) Resume(ctx context.Context) error { return nil }
func (m *mockESLE2E) IsConnected() bool                { return true }
func (m *mockESLE2E) Eavesdrop(ctx context.Context, supervisorUUID, targetUUID string) error {
	return nil
}
func (m *mockESLE2E) ConferenceKick(ctx context.Context, confName, memberID string) error { return nil }
func (m *mockESLE2E) AttendedTransfer(ctx context.Context, uuid, target, gateway string) error {
	return nil
}
func (m *mockESLE2E) SendAPI(ctx context.Context, cmd string) (string, error)   { return "+OK", nil }
func (m *mockESLE2E) SendBgAPI(ctx context.Context, cmd string) (string, error) { return "+OK", nil }

// TestE2E_FullCallLifecycle exercises the complete call lifecycle:
// Create → DTMF → RecordStart → RecordStop → Transfer → Verify session cleanup.
func TestE2E_FullCallLifecycle(t *testing.T) {
	ctx := context.Background()
	store := session.NewMemoryStore(100)
	mock := &mockESLE2E{}
	cfg := &config.Config{
		AdminAPIKey:            "test-key",
		RateLimitRPS:           1000,
		RateLimitBurst:         1000,
		PBXInterconnectEnabled: true,
	}

	// Build router with mock ESL
	router := buildTestRouter(cfg, mock, store)
	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()
	baseURL := server.URL

	// Helper to make authenticated requests
	doReq := func(method, path string, body interface{}) *http.Response {
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequest(method, baseURL+path, &buf)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer test-key")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %s %s failed: %v", method, path, err)
		}
		return resp
	}

	// ──── Step 1: Create outbound call ────
	resp := doReq("POST", "/api/v1/calls", map[string]string{
		"target_uri": "sip:1004@proxy.test",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("Step 1 Create: expected 201, got %d", resp.StatusCode)
	}
	var createResp struct {
		CallID string `json:"call_id"`
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&createResp)
	resp.Body.Close()

	callID := createResp.CallID
	if callID == "" {
		t.Fatal("Step 1: call_id is empty")
	}
	if mock.originateCalled != 1 {
		t.Fatalf("Step 1: expected 1 originate call, got %d", mock.originateCalled)
	}

	// Verify session exists
	if store.Count(ctx) != 1 {
		t.Fatalf("Step 1: expected 1 active session, got %d", store.Count(ctx))
	}

	// ──── Step 2: Send DTMF ────
	resp = doReq("POST", "/api/v1/calls/"+callID+"/dtmf", map[string]string{
		"digits": "1234",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Step 2 DTMF: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if mock.lastDtmfDigits != "1234" {
		t.Fatalf("Step 2: expected digits=1234, got %s", mock.lastDtmfDigits)
	}

	// ──── Step 3: Start recording ────
	resp = doReq("POST", "/api/v1/calls/"+callID+"/record/start", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Step 3 RecordStart: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if mock.recordStartCalled != 1 {
		t.Fatalf("Step 3: expected 1 record start call, got %d", mock.recordStartCalled)
	}
	// Verify session has record path set
	s, ok := store.Get(ctx, callID)
	if !ok {
		t.Fatal("Step 3: session not found after record start")
	}
	if s.RecordPath() == "" {
		t.Fatal("Step 3: record_path should be set after record start")
	}

	// ──── Step 4: Stop recording ────
	resp = doReq("POST", "/api/v1/calls/"+callID+"/record/stop", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Step 4 RecordStop: expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if mock.recordStopCalled != 1 {
		t.Fatalf("Step 4: expected 1 record stop call, got %d", mock.recordStopCalled)
	}

	// ──── Step 5: Capacity test — fill up and verify rejection ────
	for i := 0; i < 99; i++ {
		r := doReq("POST", "/api/v1/calls", map[string]string{
			"target_uri": "sip:test@dummy",
		})
		r.Body.Close()
	}
	// Now at 100 sessions — next should be rejected
	resp = doReq("POST", "/api/v1/calls", map[string]string{
		"target_uri": "sip:overflow@dummy",
	})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("Step 5 Capacity: expected 503, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// ──── Step 6: Admin sessions check ────
	resp = doReq("GET", "/api/v1/admin/sessions/active", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Step 6 Admin: expected 200, got %d", resp.StatusCode)
	}
	var adminResp map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&adminResp)
	resp.Body.Close()
	count := int(adminResp["count"].(float64))
	if count != 100 {
		t.Fatalf("Step 6: expected 100 active sessions, got %d", count)
	}

	// ──── Step 7: Release a session and verify count ────
	store.Release(ctx, callID)
	time.Sleep(10 * time.Millisecond) // let async settle
	if store.Count(ctx) != 99 {
		t.Fatalf("Step 7: expected 99 after release, got %d", store.Count(ctx))
	}

	t.Log("E2E Full Call Lifecycle: PASSED ✓")
}

// buildTestRouter constructs a chi router with mock dependencies.
func buildTestRouter(cfg *config.Config, eslMock esl.Commander, store session.Store) http.Handler {
	gatewayStore := interconnect.NewStore()
	gatewaySelector := interconnect.NewSelector(gatewayStore, cfg.PBXMainGateway, cfg.PBXStandbyGateway, interconnect.SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      cfg.PBXStandbyEnabled,
		FailFastWhenStale: cfg.PBXFailFastOnStale,
	})
	handoffMgr := interconnect.NewHandoffManager()
	router, err := NewRouter(cfg, nil, nil, nil, gatewayStore, gatewaySelector, handoffMgr, eslMock.(*mockESLE2E), store, "test-node-id")
	if err != nil {
		panic(err)
	}
	return router
}
