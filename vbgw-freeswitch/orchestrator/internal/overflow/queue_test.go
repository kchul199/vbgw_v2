package overflow

import (
	"testing"
	"time"
)

func TestManagerEnqueueRemoveAndSnapshot(t *testing.T) {
	mgr := NewManager()
	now := time.Now()
	first := QueueEntry{
		SessionID:      "s1",
		ServiceName:    "bot-main",
		QueueOnTimeout: "busy",
		EnqueuedAt:     now.Add(-5 * time.Second),
		TimeoutAt:      now.Add(15 * time.Second),
	}
	second := QueueEntry{
		SessionID:      "s2",
		ServiceName:    "bot-main",
		QueueOnTimeout: "busy",
		EnqueuedAt:     now.Add(-2 * time.Second),
		TimeoutAt:      now.Add(18 * time.Second),
	}

	if !mgr.Enqueue(first) {
		t.Fatal("expected first enqueue to succeed")
	}
	if !mgr.Enqueue(second) {
		t.Fatal("expected second enqueue to succeed")
	}

	peek, ok := mgr.Peek("bot-main")
	if !ok || peek.SessionID != "s1" {
		t.Fatalf("expected FIFO peek s1, got %+v ok=%v", peek, ok)
	}

	snapshot := mgr.Snapshot("bot-main", now)
	if snapshot.Depth != 2 {
		t.Fatalf("expected depth 2, got %+v", snapshot)
	}
	if len(snapshot.Sessions) != 2 || snapshot.Sessions[0].SessionID != "s1" {
		t.Fatalf("unexpected snapshot sessions: %+v", snapshot.Sessions)
	}

	if !mgr.Remove("s1") {
		t.Fatal("expected remove s1 to succeed")
	}
	peek, ok = mgr.Peek("bot-main")
	if !ok || peek.SessionID != "s2" {
		t.Fatalf("expected remaining head s2, got %+v ok=%v", peek, ok)
	}
}

func TestManagerFlushService(t *testing.T) {
	mgr := NewManager()
	now := time.Now()
	if !mgr.Enqueue(QueueEntry{
		SessionID:      "s1",
		ServiceName:    "bot-main",
		QueueOnTimeout: "busy",
		EnqueuedAt:     now,
		TimeoutAt:      now.Add(10 * time.Second),
	}) {
		t.Fatal("expected enqueue to succeed")
	}
	if !mgr.Enqueue(QueueEntry{
		SessionID:      "s2",
		ServiceName:    "bot-main",
		QueueOnTimeout: "busy",
		EnqueuedAt:     now.Add(time.Second),
		TimeoutAt:      now.Add(11 * time.Second),
	}) {
		t.Fatal("expected enqueue to succeed")
	}

	flushed := mgr.FlushService("bot-main")
	if len(flushed) != 2 {
		t.Fatalf("expected 2 flushed entries, got %d", len(flushed))
	}
	if snapshot := mgr.Snapshot("bot-main", time.Now()); snapshot.Depth != 0 {
		t.Fatalf("expected empty queue after flush, got %+v", snapshot)
	}
}
