package session

import (
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreReconcileActiveCallsCorrectsStaleCounter(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()

	store := &RedisStore{
		client:      client,
		maxCalls:    100,
		nodeID:      "node-test",
		localMap:    make(map[string]*SessionState),
		localByUUID: make(map[string]string),
	}

	if err := client.Set(ctx, "vbgw:active_calls", 98, 0).Err(); err != nil {
		t.Fatalf("seed active counter: %v", err)
	}
	result, err := store.ReconcileActiveCalls(ctx, 0)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !result.Corrected {
		t.Fatal("expected stale counter to be corrected")
	}
	if result.Previous != 98 || result.Actual != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := store.Count(ctx); got != 0 {
		t.Fatalf("expected reconciled count 0, got %d", got)
	}
}

func TestRedisStoreReconcileActiveCallsUsesExpectedActualCount(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()

	store := &RedisStore{
		client:      client,
		maxCalls:    100,
		nodeID:      "node-test",
		localMap:    make(map[string]*SessionState),
		localByUUID: make(map[string]string),
	}
	if err := client.Set(ctx, "vbgw:active_calls", 0, 0).Err(); err != nil {
		t.Fatalf("seed active counter: %v", err)
	}
	result, err := store.ReconcileActiveCalls(ctx, 2)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !result.Corrected || result.Previous != 0 || result.Actual != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if got := store.Count(ctx); got != 2 {
		t.Fatalf("expected reconciled count 2, got %d", got)
	}
}

func TestRedisStoreReleaseDoesNotDecrementBelowZero(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	defer client.Close()

	store := &RedisStore{
		client:      client,
		maxCalls:    100,
		nodeID:      "node-test",
		localMap:    make(map[string]*SessionState),
		localByUUID: make(map[string]string),
	}
	s := NewSession("node-test", "s1", "fs1", "", "")
	store.localMap[s.SessionID] = s
	store.localByUUID[s.FSUUID] = s.SessionID
	if err := client.Set(ctx, "vbgw:active_calls", 0, 0).Err(); err != nil {
		t.Fatalf("seed active counter: %v", err)
	}

	store.Release(ctx, s.SessionID)

	if got := store.Count(ctx); got != 0 {
		t.Fatalf("expected count to stay at 0, got %d", got)
	}
}
