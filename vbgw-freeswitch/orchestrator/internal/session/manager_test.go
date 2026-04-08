package session

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAddIfUnderCapacity_BasicFlow(t *testing.T) {
	m := NewManager(3)

	s1 := NewSession("s1", "fs1", "010", "100")
	s2 := NewSession("s2", "fs2", "011", "101")
	s3 := NewSession("s3", "fs3", "012", "102")
	s4 := NewSession("s4", "fs4", "013", "103")

	if !m.AddIfUnderCapacity(s1) {
		t.Fatal("expected s1 to be added")
	}
	if !m.AddIfUnderCapacity(s2) {
		t.Fatal("expected s2 to be added")
	}
	if !m.AddIfUnderCapacity(s3) {
		t.Fatal("expected s3 to be added")
	}
	// s4 should be rejected (capacity=3)
	if m.AddIfUnderCapacity(s4) {
		t.Fatal("expected s4 to be rejected at capacity")
	}
	if m.Count() != 3 {
		t.Fatalf("expected count=3, got %d", m.Count())
	}
}

func TestAddIfUnderCapacity_ConcurrentSafety(t *testing.T) {
	capacity := int64(100)
	m := NewManager(capacity)

	// Spawn 200 goroutines, each trying to add a session
	var wg sync.WaitGroup
	accepted := make(chan string, 200)

	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sid := "s" + time.Now().Format("150405.000000") + string(rune('A'+idx%26))
			s := NewSession(sid, "fs-"+sid, "", "")
			if m.AddIfUnderCapacity(s) {
				accepted <- sid
			}
		}(i)
	}

	wg.Wait()
	close(accepted)

	count := 0
	for range accepted {
		count++
	}

	if int64(count) != capacity {
		t.Fatalf("expected exactly %d accepted, got %d", capacity, count)
	}
	if m.Count() != capacity {
		t.Fatalf("expected count=%d, got %d", capacity, m.Count())
	}
}

func TestRelease_DecrementsAndCleansUp(t *testing.T) {
	m := NewManager(10)

	s := NewSession("s1", "fs1", "010", "100")
	m.AddIfUnderCapacity(s)

	if m.Count() != 1 {
		t.Fatalf("expected count=1, got %d", m.Count())
	}

	m.Release("s1")

	if m.Count() != 0 {
		t.Fatalf("expected count=0 after release, got %d", m.Count())
	}

	// Get should return not found
	if _, ok := m.Get("s1"); ok {
		t.Fatal("expected s1 to be gone after release")
	}
	if _, ok := m.GetByFSUUID("fs1"); ok {
		t.Fatal("expected fs1 lookup to fail after release")
	}
}

func TestRelease_ClosesIvrEventCh(t *testing.T) {
	m := NewManager(10)
	s := NewSession("s1", "fs1", "", "")
	ch := make(chan any, 16)
	s.IvrEventCh = ch
	m.AddIfUnderCapacity(s)

	m.Release("s1")

	// After Release, the channel (captured before Release) should be closed.
	// Reading from a closed channel returns zero value immediately.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed, but received a value")
		}
		// ok == false means channel is closed — this is correct
	case <-time.After(1 * time.Second):
		t.Fatal("expected closed channel, but read blocked (channel not closed)")
	}
}

func TestRelease_DoubleRelease(t *testing.T) {
	m := NewManager(10)
	s := NewSession("s1", "fs1", "", "")
	m.AddIfUnderCapacity(s)

	m.Release("s1")
	m.Release("s1") // should not panic or go negative

	if m.Count() != 0 {
		t.Fatalf("expected count=0, got %d", m.Count())
	}
}

func TestWaitAllDrained_ImmediateReturn(t *testing.T) {
	m := NewManager(10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// No sessions — should return immediately
	done := make(chan struct{})
	go func() {
		m.WaitAllDrained(ctx, nil)
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(1 * time.Second):
		t.Fatal("WaitAllDrained should return immediately with 0 sessions")
	}
}

func TestGetByFSUUID(t *testing.T) {
	m := NewManager(10)
	s := NewSession("s1", "fs-uuid-123", "010", "100")
	m.AddIfUnderCapacity(s)

	found, ok := m.GetByFSUUID("fs-uuid-123")
	if !ok {
		t.Fatal("expected to find session by FS UUID")
	}
	if found.SessionID != "s1" {
		t.Fatalf("expected session_id=s1, got %s", found.SessionID)
	}

	_, ok = m.GetByFSUUID("nonexistent")
	if ok {
		t.Fatal("expected not found for nonexistent UUID")
	}
}

func TestSessionState_Accessors(t *testing.T) {
	s := NewSession("s1", "fs1", "010", "100")

	// AIPaused
	if s.IsAIPaused() {
		t.Fatal("should not be paused initially")
	}
	s.SetAIPaused(true)
	if !s.IsAIPaused() {
		t.Fatal("should be paused after SetAIPaused(true)")
	}

	// RecordPath
	if s.RecordPath() != "" {
		t.Fatal("should be empty initially")
	}
	s.SetRecordPath("/recordings/test.wav")
	if s.RecordPath() != "/recordings/test.wav" {
		t.Fatalf("expected /recordings/test.wav, got %s", s.RecordPath())
	}

	// BridgedWith
	if s.BridgedWith() != "" {
		t.Fatal("should be empty initially")
	}
	s.SetBridgedWith("s2")
	if s.BridgedWith() != "s2" {
		t.Fatalf("expected s2, got %s", s.BridgedWith())
	}
}
