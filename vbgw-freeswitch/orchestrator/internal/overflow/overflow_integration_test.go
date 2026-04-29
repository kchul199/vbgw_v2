package overflow

import (
	"testing"
	"time"
)

func TestOverflowEnqueueDequeue(t *testing.T) {
	mgr := NewManager()

	entry := QueueEntry{
		SessionID:         "sess-01",
		ServiceName:       "bot-main-dev",
		OverflowPolicy:    "queue",
		QueueAnnouncement: "tone_stream://%(3000,1000,440,480)",
		QueueOnTimeout:    "busy",
		EnqueuedAt:        time.Now(),
		TimeoutAt:         time.Now().Add(20 * time.Second),
	}

	if !mgr.Enqueue(entry) {
		t.Fatal("Expected enqueue to succeed")
	}

	// Verify peek
	peeked, ok := mgr.Peek("bot-main-dev")
	if !ok {
		t.Fatal("Expected peek to succeed")
	}
	if peeked.SessionID != "sess-01" {
		t.Fatalf("Expected sess-01, got %s", peeked.SessionID)
	}

	// Duplicate enqueue same service should fail
	if mgr.Enqueue(entry) {
		t.Fatal("Duplicate enqueue should fail")
	}

	// Remove and verify empty
	mgr.Remove("sess-01")
	_, ok = mgr.Peek("bot-main-dev")
	if ok {
		t.Fatal("Expected peek to fail after remove")
	}
}

func TestOverflowMultiServiceQueues(t *testing.T) {
	mgr := NewManager()

	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-01",
		ServiceName: "svc-a",
		EnqueuedAt:  time.Now(),
		TimeoutAt:   time.Now().Add(10 * time.Second),
	})
	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-02",
		ServiceName: "svc-b",
		EnqueuedAt:  time.Now(),
		TimeoutAt:   time.Now().Add(10 * time.Second),
	})
	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-03",
		ServiceName: "svc-a",
		EnqueuedAt:  time.Now(),
		TimeoutAt:   time.Now().Add(10 * time.Second),
	})

	names := mgr.ServiceNames()
	if len(names) != 2 {
		t.Fatalf("Expected 2 services, got %d", len(names))
	}

	snap := mgr.Snapshot("svc-a", time.Now())
	if snap.Depth != 2 {
		t.Fatalf("Expected depth 2 for svc-a, got %d", snap.Depth)
	}

	// Remove first from svc-a, next in line should be sess-03
	mgr.Remove("sess-01")
	peeked, ok := mgr.Peek("svc-a")
	if !ok || peeked.SessionID != "sess-03" {
		t.Fatalf("Expected sess-03 as next, got %v %v", peeked.SessionID, ok)
	}
}

func TestOverflowSnapshotMetrics(t *testing.T) {
	mgr := NewManager()
	now := time.Now()

	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-01",
		ServiceName: "svc-x",
		EnqueuedAt:  now.Add(-5 * time.Second),
		TimeoutAt:   now.Add(15 * time.Second),
	})

	snap := mgr.Snapshot("svc-x", now)
	if snap.Depth != 1 {
		t.Fatalf("Expected depth 1, got %d", snap.Depth)
	}
	if snap.OldestWaitSeconds < 4.0 {
		t.Fatalf("Expected oldest wait >= 4s, got %f", snap.OldestWaitSeconds)
	}
}

func TestOverflowMoveAcrossServices(t *testing.T) {
	mgr := NewManager()

	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-01",
		ServiceName: "svc-a",
		EnqueuedAt:  time.Now(),
		TimeoutAt:   time.Now().Add(10 * time.Second),
	})

	// Re-enqueue to different service — should auto-remove from svc-a
	mgr.Enqueue(QueueEntry{
		SessionID:   "sess-01",
		ServiceName: "svc-b",
		EnqueuedAt:  time.Now(),
		TimeoutAt:   time.Now().Add(10 * time.Second),
	})

	snapA := mgr.Snapshot("svc-a", time.Now())
	if snapA.Depth != 0 {
		t.Fatalf("Expected svc-a depth 0, got %d", snapA.Depth)
	}

	snapB := mgr.Snapshot("svc-b", time.Now())
	if snapB.Depth != 1 {
		t.Fatalf("Expected svc-b depth 1, got %d", snapB.Depth)
	}
}
