package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalHandler_Health(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("GET", "/internal/health", nil)
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["status"] != "degraded" {
		t.Fatalf("expected degraded, got %v", resp["status"])
	}
}

func TestInternalHandler_AIPause_NoSession(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/ai-pause/nonexistent-uuid", nil)
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Should return 200 even if session doesn't exist (fire-and-forget)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestInternalHandler_AIResume_NoSession(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/ai-resume/nonexistent-uuid", nil)
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestInternalHandler_DTMF_MissingDigit(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/dtmf/test-uuid", strings.NewReader(`{}`))
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInternalHandler_DTMF_InvalidJSON(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/dtmf/test-uuid", strings.NewReader(`{invalid`))
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestInternalHandler_Shutdown(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/shutdown", nil)
	req.Header.Set("X-Internal-Secret", "secret-123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestInternalHandler_RejectsMissingSecret(t *testing.T) {
	s := &Server{}
	handler := s.InternalHandler("secret-123")

	req := httptest.NewRequest("POST", "/internal/shutdown", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestExtractUUID(t *testing.T) {
	tests := []struct {
		path, prefix, expected string
	}{
		{"/internal/ai-pause/abc-123", "/internal/ai-pause/", "abc-123"},
		{"/internal/dtmf/uuid-456", "/internal/dtmf/", "uuid-456"},
		{"/internal/ai-resume/", "/internal/ai-resume/", ""},
	}

	for _, tc := range tests {
		result := extractUUID(tc.path, tc.prefix)
		if result != tc.expected {
			t.Errorf("extractUUID(%q, %q) = %q, want %q", tc.path, tc.prefix, result, tc.expected)
		}
	}
}

func TestMakeOriginChecker(t *testing.T) {
	checker := makeOriginChecker([]string{"https://admin.example.com"})

	tests := []struct {
		name    string
		origin  string
		allowed bool
	}{
		{name: "empty origin", origin: "", allowed: true},
		{name: "localhost", origin: "http://localhost:3000", allowed: true},
		{name: "configured origin", origin: "https://admin.example.com", allowed: true},
		{name: "random external origin", origin: "https://evil.example.com", allowed: false},
	}

	for _, tc := range tests {
		req := httptest.NewRequest(http.MethodGet, "/audio/test", nil)
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		if got := checker(req); got != tc.allowed {
			t.Fatalf("%s: expected %v, got %v", tc.name, tc.allowed, got)
		}
	}
}
