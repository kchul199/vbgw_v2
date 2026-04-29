package slots

import (
	"context"
	"testing"
	"time"
)

type mockAPICaller struct {
	payload string
}

func (m *mockAPICaller) SendAPI(ctx context.Context, cmd string) (string, error) {
	return m.payload, nil
}

func TestParseRegistrations(t *testing.T) {
	now := time.Now()
	payload := "Call-ID,User,Contact,Agent\n1000,token,sip:1000@127.0.0.1,Zoiper\n1001,token,sip:1001@127.0.0.1,Zoiper\n"
	states := ParseRegistrations(payload, now)
	if len(states) != 2 {
		t.Fatalf("expected 2 registrations, got %d", len(states))
	}
	if states[0].Extension != "1000" || states[1].Extension != "1001" {
		t.Fatalf("unexpected registrations: %+v", states)
	}
}

func TestRegistryRefreshAndGet(t *testing.T) {
	registry := NewRegistry(30 * time.Second)
	if err := registry.RefreshFromESL(context.Background(), &mockAPICaller{
		payload: "Call-ID,User,Contact\n1001,token,sip:1001@test\n",
	}); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	state, ok := registry.Get("1001")
	if !ok || !state.Registered {
		t.Fatalf("expected registered extension, got %+v ok=%v", state, ok)
	}
	if _, ok := registry.Get("1099"); ok {
		t.Fatal("expected unknown extension to be absent")
	}
}
