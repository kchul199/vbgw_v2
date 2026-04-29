package routing

import "testing"

func TestResolverResolve_Matched(t *testing.T) {
	cfg := &Config{
		Version:  1,
		Defaults: Defaults{OnUnknownEntry: UnknownStaticFallback},
		Services: []ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: RouteTypeAI,
				EntryNums: []string{"9196"},
				Match: MatchRule{
					IngressStages: []string{"default-policy"},
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cfg)
	route, ok := resolver.Resolve(ResolveInput{
		DestinationNumber: "9196",
		IngressStage:      "default",
	})
	if !ok {
		t.Fatal("expected route match")
	}
	if route.ServiceName != "bot-main" || route.RouteType != RouteTypeAI {
		t.Fatalf("unexpected route: %+v", route)
	}
	if route.IngressStage != "default-policy" {
		t.Fatalf("expected normalized ingress stage, got %q", route.IngressStage)
	}
}

func TestResolverResolve_GatewayFiltered(t *testing.T) {
	cfg := &Config{
		Version:  1,
		Defaults: Defaults{OnUnknownEntry: UnknownStaticFallback},
		Services: []ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: RouteTypeAI,
				EntryNums: []string{"1000"},
				Match: MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main"},
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cfg)
	if _, ok := resolver.Resolve(ResolveInput{
		DestinationNumber: "1000",
		IngressStage:      "default-policy",
		SourceGateway:     "pbx-standby",
	}); ok {
		t.Fatal("expected gateway mismatch to fail")
	}
}

func TestResolverResolve_PBXRepresentativeNumbers(t *testing.T) {
	cfg := &Config{
		Version:  1,
		Defaults: Defaults{OnUnknownEntry: UnknownStaticFallback},
		Services: []ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: RouteTypeAI,
				EntryNums: []string{"1000", "5551212"},
				Match: MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
			{
				Name:      "vip-bot",
				Enabled:   true,
				RouteType: RouteTypeAI,
				EntryNums: []string{"2000"},
				Match: MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main", "pbx-standby"},
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	resolver := NewResolver(cfg)

	testCases := []struct {
		dest     string
		gateway  string
		wantName string
	}{
		{dest: "1000", gateway: "pbx-main", wantName: "bot-main"},
		{dest: "5551212", gateway: "pbx-standby", wantName: "bot-main"},
		{dest: "2000", gateway: "pbx-main", wantName: "vip-bot"},
	}

	for _, tc := range testCases {
		route, ok := resolver.Resolve(ResolveInput{
			DestinationNumber: tc.dest,
			IngressStage:      "default-policy",
			SourceGateway:     tc.gateway,
		})
		if !ok {
			t.Fatalf("expected route match for %s via %s", tc.dest, tc.gateway)
		}
		if route.ServiceName != tc.wantName {
			t.Fatalf("expected %s to resolve to %s, got %+v", tc.dest, tc.wantName, route)
		}
	}

	if _, ok := resolver.Resolve(ResolveInput{
		DestinationNumber: "1000",
		IngressStage:      "default-policy",
		SourceGateway:     "",
	}); ok {
		t.Fatal("expected local call without source gateway to fall through")
	}
}
