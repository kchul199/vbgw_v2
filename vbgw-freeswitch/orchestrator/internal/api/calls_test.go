package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vbgw-orchestrator/internal/session"
)

// mockESLCalls implements esl.Commander for testing.
type mockESLCalls struct {
	originateErr            error
	lastGateways            []string
	originateCalls          []string
	transferViaGatewayCalls []string
	attendedTransferCalls   []string
	transferViaGatewayErrs  map[string]error
	attendedTransferErrs    map[string]error
	originateHook           func(uuid string, gatewayOrder []string)
	dumpResp                map[string]string
	dumpErr                 error
	dtmfErr                 error
	transferErr             error
	bridgeErr               error
	unbridgeErr             error
	recordErr               error
	breakErr                error
	killErr                 error
	connected               bool
}

func (m *mockESLCalls) Originate(ctx context.Context, uuid, target, callerID string, gatewayOrder []string) (string, error) {
	m.originateCalls = append(m.originateCalls, uuid)
	m.lastGateways = append([]string(nil), gatewayOrder...)
	if m.originateHook != nil {
		m.originateHook(uuid, gatewayOrder)
	}
	return "job-uuid", m.originateErr
}
func (m *mockESLCalls) SendDtmf(ctx context.Context, uuid, digits string) error { return m.dtmfErr }
func (m *mockESLCalls) SetVar(ctx context.Context, uuid, key, value string) error {
	return nil
}
func (m *mockESLCalls) Transfer(ctx context.Context, uuid, target string) error { return m.transferErr }
func (m *mockESLCalls) TransferViaGateway(ctx context.Context, uuid, target, gateway string) error {
	m.transferViaGatewayCalls = append(m.transferViaGatewayCalls, gateway)
	if err, ok := m.transferViaGatewayErrs[gateway]; ok {
		return err
	}
	return m.transferErr
}
func (m *mockESLCalls) Bridge(ctx context.Context, uuidA, uuidB string) error    { return m.bridgeErr }
func (m *mockESLCalls) Unbridge(ctx context.Context, uuid string) error          { return m.unbridgeErr }
func (m *mockESLCalls) RecordStart(ctx context.Context, uuid, path string) error { return m.recordErr }
func (m *mockESLCalls) RecordStop(ctx context.Context, uuid string) error        { return m.recordErr }
func (m *mockESLCalls) Break(ctx context.Context, uuid string) error             { return m.breakErr }
func (m *mockESLCalls) Kill(ctx context.Context, uuid string) error              { return m.killErr }
func (m *mockESLCalls) Dump(ctx context.Context, uuid string) (map[string]string, error) {
	return m.dumpResp, m.dumpErr
}
func (m *mockESLCalls) Pause(ctx context.Context) error  { return nil }
func (m *mockESLCalls) Resume(ctx context.Context) error { return nil }
func (m *mockESLCalls) IsConnected() bool                { return m.connected }
func (m *mockESLCalls) Eavesdrop(ctx context.Context, supervisorUUID, targetUUID string) error {
	return nil
}
func (m *mockESLCalls) ConferenceKick(ctx context.Context, confName, memberID string) error {
	return nil
}
func (m *mockESLCalls) AttendedTransfer(ctx context.Context, uuid, target, gateway string) error {
	m.attendedTransferCalls = append(m.attendedTransferCalls, gateway)
	if err, ok := m.attendedTransferErrs[gateway]; ok {
		return err
	}
	return m.transferErr
}
func (m *mockESLCalls) SendAPI(ctx context.Context, cmd string) (string, error)   { return "+OK", nil }
func (m *mockESLCalls) SendBgAPI(ctx context.Context, cmd string) (string, error) { return "+OK", nil }

func TestCreateCall_Success(t *testing.T) {
	sessions := session.NewMemoryStore(100)
	handler := &CallsHandler{
		ESL:             &mockESLCalls{connected: true},
		Sessions:        sessions,
		OutboundEnabled: true,
	}

	body := `{"target_uri":"1001@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp createCallResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if resp.Status != "initiating" {
		t.Fatalf("expected status=initiating, got %s", resp.Status)
	}
	if resp.CallID == "" {
		t.Fatal("expected non-empty call_id")
	}
}

func TestCreateCall_EmptyTarget(t *testing.T) {
	handler := &CallsHandler{
		ESL:             &mockESLCalls{},
		Sessions:        session.NewMemoryStore(100),
		OutboundEnabled: true,
	}

	body := `{"target_uri":""}`
	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateCall_CapacityExceeded(t *testing.T) {
	sessions := session.NewMemoryStore(1) // Max 1 session
	// Fill capacity
	s := session.NewSession("node-test", "existing", "fs-1", "010", "1001")
	sessions.AddIfUnderCapacity(context.Background(), s)

	handler := &CallsHandler{
		ESL:             &mockESLCalls{connected: true},
		Sessions:        sessions,
		OutboundEnabled: true,
	}

	body := `{"target_uri":"1002@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestCreateCall_InvalidJSON(t *testing.T) {
	handler := &CallsHandler{
		ESL:             &mockESLCalls{},
		Sessions:        session.NewMemoryStore(100),
		OutboundEnabled: true,
	}

	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestCreateCall_ESLOriginateFailure(t *testing.T) {
	handler := &CallsHandler{
		ESL:             &mockESLCalls{originateErr: http.ErrServerClosed},
		Sessions:        session.NewMemoryStore(100),
		OutboundEnabled: true,
	}

	body := `{"target_uri":"1001@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	// Verify session was cleaned up
	if handler.Sessions.Count(context.Background()) != 0 {
		t.Fatalf("expected 0 sessions after failed originate, got %d", handler.Sessions.Count(context.Background()))
	}
}

func TestMaskURI_ShortURI(t *testing.T) {
	if maskURI("abc") != "****" {
		t.Fatalf("expected '****', got '%s'", maskURI("abc"))
	}
}

func TestMaskURI_LongURI(t *testing.T) {
	result := maskURI("sip:1234@pbx")
	if !strings.HasSuffix(result, "@pbx") {
		t.Fatalf("expected suffix '@pbx', got '%s'", result)
	}
	if !strings.HasPrefix(result, "****") {
		t.Fatalf("expected masked prefix, got '%s'", result)
	}
}

func TestCreateCall_InterconnectDisabled(t *testing.T) {
	handler := &CallsHandler{
		ESL:             &mockESLCalls{connected: true},
		Sessions:        session.NewMemoryStore(100),
		OutboundEnabled: false,
	}

	body := `{"target_uri":"1001@pbx"}`
	req := httptest.NewRequest("POST", "/api/v1/calls", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.CreateCall(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
	if handler.Sessions.Count(context.Background()) != 0 {
		t.Fatalf("expected no sessions when interconnect disabled, got %d", handler.Sessions.Count(context.Background()))
	}
}
