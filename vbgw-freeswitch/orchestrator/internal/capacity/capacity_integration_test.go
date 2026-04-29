package capacity

import (
	"testing"
	"vbgw-orchestrator/internal/routing"
)

func TestCapacityAdmitAndRelease(t *testing.T) {
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{
			{
				Name:    "test-svc",
				Enabled: true,
				Capacity: routing.Capacity{
					Backend:        "logical",
					MaxConcurrent:  2,
					Allocator:      "round_robin",
					OverflowPolicy: "busy",
				},
			},
		},
	}

	mgr := NewManager(cfg, nil)

	// First admit should succeed
	d1 := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-01",
		ServiceName: "test-svc",
		CallerID:    "1234",
	})
	if !d1.Allowed {
		t.Fatal("First admit should succeed")
	}

	// Second admit should succeed
	d2 := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-02",
		ServiceName: "test-svc",
		CallerID:    "5678",
	})
	if !d2.Allowed {
		t.Fatal("Second admit should succeed")
	}

	// Third should fail (max_concurrent=2)
	d3 := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-03",
		ServiceName: "test-svc",
		CallerID:    "9999",
	})
	if d3.Allowed {
		t.Fatal("Third admit should be rejected (capacity 2)")
	}
	if d3.OverflowPolicy != "busy" {
		t.Fatalf("Expected overflow_policy=busy, got %s", d3.OverflowPolicy)
	}

	// Release one slot
	mgr.Release("sess-01")

	// Now fourth should succeed
	d4 := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-04",
		ServiceName: "test-svc",
		CallerID:    "0000",
	})
	if !d4.Allowed {
		t.Fatal("Fourth admit should succeed after release")
	}
}

func TestCapacityPreview(t *testing.T) {
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{
			{
				Name:    "preview-svc",
				Enabled: true,
				Capacity: routing.Capacity{
					Backend:        "logical",
					MaxConcurrent:  1,
					Allocator:      "round_robin",
					OverflowPolicy: "busy",
				},
			},
		},
	}

	mgr := NewManager(cfg, nil)

	// Preview before any admits — should be allowed
	p1 := mgr.Preview("preview-svc")
	if !p1.Allowed {
		t.Fatal("Preview should show allowed when empty")
	}

	// Admit one
	mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-01",
		ServiceName: "preview-svc",
		CallerID:    "1234",
	})

	// Preview now — should show not allowed
	p2 := mgr.Preview("preview-svc")
	if p2.Allowed {
		t.Fatal("Preview should show not allowed when full")
	}
}

func TestCapacityUnknownService(t *testing.T) {
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{},
	}

	mgr := NewManager(cfg, nil)

	d := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-01",
		ServiceName: "nonexistent",
		CallerID:    "1234",
	})

	// Unknown service should be allowed (no capacity config)
	if d.Configured {
		t.Fatal("Unknown service should not be configured")
	}
	if !d.Allowed {
		t.Fatal("Unknown service should be allowed by default")
	}
}

func TestCapacityQueueOverflow(t *testing.T) {
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{
			{
				Name:    "queue-svc",
				Enabled: true,
				Capacity: routing.Capacity{
					Backend:             "logical",
					MaxConcurrent:       1,
					Allocator:           "round_robin",
					OverflowPolicy:      "queue",
					QueueMaxWaitSec:     20,
					QueueAnnouncement:   "tone_stream://%(3000,1000,440,480)",
					QueueOnTimeout:      "busy",
				},
			},
		},
	}

	mgr := NewManager(cfg, nil)

	// Fill capacity
	mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-01",
		ServiceName: "queue-svc",
		CallerID:    "1234",
	})

	// Next should overflow with queue policy
	d := mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-02",
		ServiceName: "queue-svc",
		CallerID:    "5678",
	})

	if d.Allowed {
		t.Fatal("Should be rejected due to capacity")
	}
	if d.OverflowPolicy != "queue" {
		t.Fatalf("Expected queue overflow policy, got %s", d.OverflowPolicy)
	}
	if d.QueueMaxWaitSec != 20 {
		t.Fatalf("Expected queue max wait 20, got %d", d.QueueMaxWaitSec)
	}
}

func TestCapacitySnapshots(t *testing.T) {
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{
			{
				Name:    "snap-svc",
				Enabled: true,
				Capacity: routing.Capacity{
					Backend:        "logical",
					MaxConcurrent:  5,
					Allocator:      "round_robin",
					OverflowPolicy: "busy",
				},
			},
		},
	}

	mgr := NewManager(cfg, nil)
	mgr.AdmitRequest(AdmitRequest{
		SessionID:   "sess-01",
		ServiceName: "snap-svc",
		CallerID:    "1234",
	})

	snapshots := mgr.Snapshots()
	if len(snapshots) == 0 {
		t.Fatal("Expected at least 1 snapshot")
	}

	found := false
	for _, s := range snapshots {
		if s.ServiceName == "snap-svc" {
			found = true
			if s.MaxConcurrent != 5 {
				t.Fatalf("Expected max_concurrent 5, got %d", s.MaxConcurrent)
			}
		}
	}
	if !found {
		t.Fatal("snap-svc not found in snapshots")
	}
}
