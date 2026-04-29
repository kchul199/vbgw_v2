package capacity

import (
	"testing"
	"time"

	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/slots"
)

func TestManagerAdmitReleaseRoundRobin(t *testing.T) {
	mgr := NewManager(&routing.Config{
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
					MaxConcurrent:  2,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})

	first := mgr.Admit("s1", "bot-main")
	if !first.Allowed || first.SlotID != "bot-main-01" {
		t.Fatalf("unexpected first admission: %+v", first)
	}
	second := mgr.Admit("s2", "bot-main")
	if !second.Allowed || second.SlotID != "bot-main-02" {
		t.Fatalf("unexpected second admission: %+v", second)
	}
	third := mgr.Admit("s3", "bot-main")
	if third.Allowed || third.OverflowPolicy != routing.OverflowBusy {
		t.Fatalf("expected busy overflow, got %+v", third)
	}

	mgr.Release("s1")
	fourth := mgr.Admit("s4", "bot-main")
	if !fourth.Allowed || fourth.SlotID != "bot-main-01" {
		t.Fatalf("expected released slot to be reused, got %+v", fourth)
	}
}

func TestManagerDirectTransferPolicy(t *testing.T) {
	mgr := NewManager(&routing.Config{
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
	})

	first := mgr.Admit("s1", "vip-bot")
	if !first.Allowed {
		t.Fatalf("expected first call to be admitted, got %+v", first)
	}
	overflow := mgr.Admit("s2", "vip-bot")
	if overflow.Allowed || overflow.OverflowPolicy != routing.OverflowDirectTransfer || overflow.TransferTarget != "2100" {
		t.Fatalf("expected direct transfer overflow, got %+v", overflow)
	}
}

func TestManagerQueuePolicyExposesQueueMetadata(t *testing.T) {
	mgr := NewManager(&routing.Config{
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
					MaxConcurrent:     1,
					Allocator:         routing.AllocatorRoundRobin,
					OverflowPolicy:    routing.OverflowQueue,
					QueueMaxWaitSec:   30,
					QueueAnnouncement: "ivr/queue.wav",
					QueueOnTimeout:    routing.OverflowBusy,
				},
			},
		},
	})

	if first := mgr.Admit("s1", "bot-main"); !first.Allowed {
		t.Fatalf("expected first call to be admitted, got %+v", first)
	}
	overflow := mgr.Admit("s2", "bot-main")
	if overflow.Allowed || overflow.OverflowPolicy != routing.OverflowQueue {
		t.Fatalf("expected queue overflow, got %+v", overflow)
	}
	if overflow.QueueMaxWaitSec != 30 || overflow.QueueAnnouncement != "ivr/queue.wav" || overflow.QueueOnTimeout != routing.OverflowBusy {
		t.Fatalf("unexpected queue metadata: %+v", overflow)
	}
}

func TestManagerFailoverServicePolicyExposesTargetService(t *testing.T) {
	mgr := NewManager(&routing.Config{
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
					MaxConcurrent:   1,
					Allocator:       routing.AllocatorRoundRobin,
					OverflowPolicy:  routing.OverflowFailoverSvc,
					OverflowService: "bot-backup",
				},
			},
		},
	})

	if first := mgr.Admit("s1", "bot-main"); !first.Allowed {
		t.Fatalf("expected first call to be admitted, got %+v", first)
	}
	overflow := mgr.Admit("s2", "bot-main")
	if overflow.Allowed || overflow.OverflowPolicy != routing.OverflowFailoverSvc || overflow.OverflowService != "bot-backup" {
		t.Fatalf("expected failover_service overflow, got %+v", overflow)
	}
}

func TestManagerPreviewUnconfiguredService(t *testing.T) {
	mgr := NewManager(nil)
	decision := mgr.Preview("bot-main")
	if !decision.Allowed || decision.Configured {
		t.Fatalf("expected unconfigured service to pass through, got %+v", decision)
	}
}

func TestManagerSnapshotsSorted(t *testing.T) {
	mgr := NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "vip-bot",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"2000"},
				Match:     routing.MatchRule{IngressStages: []string{"default-policy"}},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Match:     routing.MatchRule{IngressStages: []string{"default-policy"}},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})

	snapshots := mgr.Snapshots()
	if len(snapshots) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snapshots))
	}
	if snapshots[0].ServiceName != "bot-main" || snapshots[1].ServiceName != "vip-bot" {
		t.Fatalf("expected sorted snapshots, got %+v", snapshots)
	}
}

func TestManagerSIPBackendRequiresRegisteredExtension(t *testing.T) {
	registry := slots.NewRegistry(30 * time.Second)
	registry.Replace([]slots.RegistrationState{{
		Extension:  "1001",
		Registered: true,
		LastSeenAt: time.Now(),
	}})

	mgr := NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "frontdesk-bot",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"3000"},
				Capacity: routing.Capacity{
					Backend:           routing.BackendSIPExtension,
					Allocator:         routing.AllocatorPriority,
					Extensions:        []string{"1001", "1002"},
					RequireRegistered: true,
					OverflowPolicy:    routing.OverflowBusy,
				},
			},
		},
	}, registry)

	first := mgr.AdmitRequest(AdmitRequest{SessionID: "s1", ServiceName: "frontdesk-bot"})
	if !first.Allowed || first.SlotID != "1001" {
		t.Fatalf("expected registered extension 1001 to be selected, got %+v", first)
	}
	second := mgr.AdmitRequest(AdmitRequest{SessionID: "s2", ServiceName: "frontdesk-bot"})
	if second.Allowed {
		t.Fatalf("expected overflow with no additional registered extensions, got %+v", second)
	}
}

func TestManagerStickyCallerUsesStableSlot(t *testing.T) {
	mgr := NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Capacity: routing.Capacity{
					MaxConcurrent:  3,
					Allocator:      routing.AllocatorStickyCaller,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})

	first := mgr.AdmitRequest(AdmitRequest{SessionID: "s1", ServiceName: "bot-main", CallerID: "01012345678"})
	if !first.Allowed {
		t.Fatalf("expected first admission, got %+v", first)
	}
	mgr.Release("s1")
	second := mgr.AdmitRequest(AdmitRequest{SessionID: "s2", ServiceName: "bot-main", CallerID: "01012345678"})
	if !second.Allowed {
		t.Fatalf("expected second admission, got %+v", second)
	}
	if first.SlotID != second.SlotID {
		t.Fatalf("expected sticky caller to reuse same slot, got %q then %q", first.SlotID, second.SlotID)
	}
}
