package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/routing"
)

func TestGenerateDialplan_ValidRequest(t *testing.T) {
	handler := NewDialplanHandler([]string{"9196", "5551212"}, nil, nil)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=9196&Caller-Caller-ID-Number=9999")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	respBody := w.Body.String()

	// Verify XML structure
	if !strings.Contains(respBody, `<document type="freeswitch/xml">`) {
		t.Error("response missing XML document root")
	}
	if !strings.Contains(respBody, `<section name="dialplan"`) {
		t.Error("response missing dialplan section")
	}
	if !strings.Contains(respBody, "vbgw-ai-dynamic") {
		t.Error("response missing vbgw-ai-dynamic extension")
	}
	if !strings.Contains(respBody, `application="park"`) {
		t.Error("response missing staged park action")
	}
	if !strings.Contains(respBody, "application/xml") {
		// Check Content-Type header
		ct := w.Header().Get("Content-Type")
		if !strings.Contains(ct, "application/xml") {
			t.Errorf("expected application/xml content type, got %s", ct)
		}
	}
}

func TestGenerateDialplan_EmptyContext(t *testing.T) {
	handler := NewDialplanHandler([]string{"9196"}, nil, nil)

	body := strings.NewReader("Caller-Destination-Number=1004")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	// Unknown/non-default traffic should fall back to static XML via not found.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `status="not found"`) {
		t.Fatal("expected not found response for empty context")
	}
}

func TestGenerateDialplan_AdditionalOperationalRoute(t *testing.T) {
	resolver := routing.NewResolver(&routing.Config{
		Version: 1,
		Defaults: routing.Defaults{
			OnUnknownEntry: routing.UnknownStaticFallback,
		},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"5551212"},
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
		},
	})
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: resolverConfig("5551212", "bot-main"), Resolver: resolver}, nil)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=5551212&Caller-Caller-ID-Number=82101234&variable_sip_gateway_name=pbx-main")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "^5551212$") {
		t.Fatal("expected operational DID to be routed dynamically")
	}
	if !strings.Contains(w.Body.String(), "vbgw_service_name=bot-main") {
		t.Fatal("expected service metadata for operational DID")
	}
}

func TestGenerateDialplan_PolicyResolverMatch(t *testing.T) {
	resolver := routing.NewResolver(&routing.Config{
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
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main"},
				},
			},
		},
	})
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: resolverConfig("1000", "bot-main"), Resolver: resolver}, nil)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=1000&variable_sip_gateway_name=pbx-main")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "vbgw_service_name=bot-main") {
		t.Fatal("expected policy matched service metadata in dialplan")
	}
	if !strings.Contains(respBody, "^1000$") {
		t.Fatal("expected policy matched destination expression")
	}
}

func TestGenerateDialplan_PolicyResolverMatchVIPRoute(t *testing.T) {
	resolver := routing.NewResolver(&routing.Config{
		Version: 1,
		Defaults: routing.Defaults{
			OnUnknownEntry: routing.UnknownStaticFallback,
		},
		Services: []routing.ServiceRoute{
			{
				Name:      "vip-bot",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"2000"},
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
		},
	})
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: resolverConfig("2000", "vip-bot"), Resolver: resolver}, nil)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=2000&variable_sip_gateway_name=pbx-standby")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "vbgw_service_name=vip-bot") {
		t.Fatal("expected VIP route metadata in dialplan")
	}
	if !strings.Contains(respBody, "^2000$") {
		t.Fatal("expected VIP route destination expression")
	}
}

func TestGenerateDialplan_PolicyResolverGatewayMismatchFallsBack(t *testing.T) {
	resolver := routing.NewResolver(&routing.Config{
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
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
		},
	})
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: resolverConfig("1000", "bot-main"), Resolver: resolver}, nil)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=1000")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `status="not found"`) {
		t.Fatal("expected gateway mismatch to fall back to static XML")
	}
}

func TestGenerateDialplan_OverflowBusyPrecheck(t *testing.T) {
	cfg := &routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main"},
				},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	}
	resolver := routing.NewResolver(cfg)
	capacityMgr := capacity.NewManager(cfg)
	capacityMgr.Admit("existing", "bot-main")
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: cfg, Resolver: resolver}, capacityMgr)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=1000&variable_sip_gateway_name=pbx-main")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `vbgw_overflow_policy=busy`) {
		t.Fatal("expected busy overflow metadata")
	}
	if !strings.Contains(respBody, `application="hangup" data="USER_BUSY"`) {
		t.Fatal("expected USER_BUSY hangup in overflow dialplan")
	}
}

func TestGenerateDialplan_OverflowDirectTransferPrecheck(t *testing.T) {
	cfg := &routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "vip-bot",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"2000"},
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main"},
				},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowDirectTransfer,
					TransferTarget: "2100",
				},
			},
		},
	}
	resolver := routing.NewResolver(cfg)
	capacityMgr := capacity.NewManager(cfg)
	capacityMgr.Admit("existing", "vip-bot")
	handler := NewDialplanHandler([]string{"9196"}, &routing.Runtime{Config: cfg, Resolver: resolver}, capacityMgr)

	body := strings.NewReader("Hunt-Context=default&Caller-Destination-Number=2000&variable_sip_gateway_name=pbx-main")
	req := httptest.NewRequest("POST", "/api/v1/fs/dialplan", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	handler.GenerateDialplan(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, `vbgw_overflow_policy=direct_transfer`) {
		t.Fatal("expected direct_transfer overflow metadata")
	}
	if !strings.Contains(respBody, `application="transfer" data="2100 XML default"`) {
		t.Fatal("expected direct transfer in overflow dialplan")
	}
}

func resolverConfig(entry, serviceName string) *routing.Config {
	return &routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      serviceName,
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{entry},
				Match: routing.MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
		},
	}
}
