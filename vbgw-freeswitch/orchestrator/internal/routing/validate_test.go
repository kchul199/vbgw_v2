package routing

import "testing"

func TestValidate_RepresentativeEntriesRequireSourceGateways(t *testing.T) {
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
					IngressStages: []string{"default-policy"},
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	err := Validate(cfg, ValidateOptions{
		AllowedGatewayIDs:               []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages:            []string{"default-policy"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	})
	if err == nil {
		t.Fatal("expected validation error for representative entry without source gateways")
	}
}

func TestValidate_CapacityDirectTransferRequiresTarget(t *testing.T) {
	cfg := &Config{
		Version:  1,
		Defaults: Defaults{OnUnknownEntry: UnknownStaticFallback},
		Services: []ServiceRoute{
			{
				Name:      "vip-bot",
				Enabled:   true,
				RouteType: RouteTypeAI,
				EntryNums: []string{"2000"},
				Match: MatchRule{
					IngressStages:  []string{"default-policy"},
					SourceGateways: []string{"pbx-main"},
				},
				Capacity: Capacity{
					MaxConcurrent:  1,
					Allocator:      AllocatorRoundRobin,
					OverflowPolicy: OverflowDirectTransfer,
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	err := Validate(cfg, ValidateOptions{
		AllowedGatewayIDs:               []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages:            []string{"default-policy"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	})
	if err == nil {
		t.Fatal("expected validation error for missing transfer_target")
	}
}

func TestValidate_CapacityRoundRobinSupported(t *testing.T) {
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
				Capacity: Capacity{
					MaxConcurrent:  2,
					Allocator:      AllocatorRoundRobin,
					OverflowPolicy: OverflowBusy,
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if err := Validate(cfg, ValidateOptions{
		AllowedGatewayIDs:               []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages:            []string{"default-policy"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	}); err != nil {
		t.Fatalf("expected config to validate, got %v", err)
	}
}

func TestValidate_QueuePolicyDefaultsAndTargetChecks(t *testing.T) {
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
				Capacity: Capacity{
					MaxConcurrent:  2,
					Allocator:      AllocatorRoundRobin,
					OverflowPolicy: OverflowQueue,
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Services[0].Capacity.QueueMaxWaitSec != 20 {
		t.Fatalf("expected default queue wait 20s, got %d", cfg.Services[0].Capacity.QueueMaxWaitSec)
	}
	if cfg.Services[0].Capacity.QueueOnTimeout != OverflowBusy {
		t.Fatalf("expected default queue timeout policy busy, got %q", cfg.Services[0].Capacity.QueueOnTimeout)
	}
	if err := Validate(cfg, ValidateOptions{
		AllowedGatewayIDs:               []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages:            []string{"default-policy"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	}); err != nil {
		t.Fatalf("expected queue config to validate, got %v", err)
	}
}

func TestValidate_FailoverServiceRequiresTargetService(t *testing.T) {
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
				Capacity: Capacity{
					MaxConcurrent:  1,
					Allocator:      AllocatorRoundRobin,
					OverflowPolicy: OverflowFailoverSvc,
				},
			},
		},
	}

	if err := Normalize(cfg); err != nil {
		t.Fatal(err)
	}
	err := Validate(cfg, ValidateOptions{
		AllowedGatewayIDs:               []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages:            []string{"default-policy"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	})
	if err == nil {
		t.Fatal("expected validation error for missing overflow_service")
	}
}
