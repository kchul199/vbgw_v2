package cluster

import (
	"context"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newRedisClient(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	return srv, client
}

func TestLeaseStoreAcquireRenewRelease(t *testing.T) {
	_, client := newRedisClient(t)
	storeA := NewLeaseStore(client, "node-a", 5*time.Second, 2*time.Second, 1)
	storeB := NewLeaseStore(client, "node-b", 5*time.Second, 2*time.Second, 1)

	ok, err := storeA.Acquire(context.Background(), "bot-main", "bot-main-01", "sess-a", NodeStateActive, time.Now())
	if err != nil || !ok {
		t.Fatalf("expected node A acquire to succeed, ok=%v err=%v", ok, err)
	}
	ok, err = storeB.Acquire(context.Background(), "bot-main", "bot-main-01", "sess-b", NodeStateActive, time.Now())
	if err != nil {
		t.Fatalf("unexpected acquire conflict err: %v", err)
	}
	if ok {
		t.Fatal("expected conflicting acquire to fail")
	}
	ok, err = storeA.Renew(context.Background(), "bot-main", "bot-main-01", "sess-a", NodeStateActive, time.Now())
	if err != nil || !ok {
		t.Fatalf("expected renew to succeed, ok=%v err=%v", ok, err)
	}
	ok, err = storeA.Release(context.Background(), "bot-main", "bot-main-01", "sess-a")
	if err != nil || !ok {
		t.Fatalf("expected release to succeed, ok=%v err=%v", ok, err)
	}
}

func TestManagerHeartbeatAndCompatibility(t *testing.T) {
	_, client := newRedisClient(t)
	mgrA := NewManager(client, "node-a", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	mgrB := NewManager(client, "node-b", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.2.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgrA.Start(ctx)
	mgrB.Start(ctx)
	time.Sleep(120 * time.Millisecond)

	nodes, err := mgrA.ListNodes(context.Background())
	if err != nil {
		t.Fatalf("list nodes: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}
	report, err := mgrA.CompatibilityReport(context.Background(), 1)
	if err != nil {
		t.Fatalf("compatibility report: %v", err)
	}
	if !report.Compatible {
		t.Fatalf("expected 7.x nodes with same schema to be compatible, issues=%+v", report.Issues)
	}
}

func TestManagerCompatibilityMismatch(t *testing.T) {
	_, client := newRedisClient(t)
	mgrA := NewManager(client, "node-a", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
		RoutingVersion:      func() int { return 1 },
	})
	mgrB := NewManager(client, "node-b", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "8.0.0",
		LeaseSchemaVersion:  2,
		RoutingVersion:      func() int { return 2 },
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgrA.Start(ctx)
	mgrB.Start(ctx)
	time.Sleep(120 * time.Millisecond)

	report, err := mgrA.CompatibilityReport(context.Background(), 1)
	if err != nil {
		t.Fatalf("compatibility report: %v", err)
	}
	if report.Compatible {
		t.Fatalf("expected incompatible report, got %+v", report)
	}
}

func TestManagerPublishDrainCommand(t *testing.T) {
	_, client := newRedisClient(t)
	mgrA := NewManager(client, "node-a", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	mgrB := NewManager(client, "node-b", Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgrA.Start(ctx)
	mgrB.Start(ctx)
	time.Sleep(80 * time.Millisecond)

	if err := mgrA.PublishCommand(context.Background(), "node-b", Command{
		Action:      "set_state",
		State:       NodeStateDraining,
		Reason:      "deploy",
		RequestedAt: time.Now(),
	}); err != nil {
		t.Fatalf("publish drain command: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := mgrB.CurrentState(); got != NodeStateDraining {
		t.Fatalf("expected node-b to enter draining, got %q", got)
	}
}

func TestLeaseStoreReapStale(t *testing.T) {
	_, client := newRedisClient(t)
	storeA := NewLeaseStore(client, "node-a", 10*time.Second, 20*time.Millisecond, 1)
	if ok, err := storeA.Acquire(context.Background(), "bot-main", "bot-main-01", "sess-a", NodeStateActive, time.Now().Add(-time.Minute)); err != nil || !ok {
		t.Fatalf("acquire stale test lease: ok=%v err=%v", ok, err)
	}
	// Simulate stale ownership by moving renewed time into the past.
	if err := client.HSet(context.Background(), leaseKey("bot-main", "bot-main-01"), "renewed_at_unix_ms", time.Now().Add(-time.Minute).UnixMilli()).Err(); err != nil {
		t.Fatalf("set renewed_at: %v", err)
	}
	reaped, err := storeA.ReapStale(context.Background(), map[string]NodeHeartbeat{}, time.Now())
	if err != nil {
		t.Fatalf("reap stale: %v", err)
	}
	if reaped != 1 {
		t.Fatalf("expected one stale lease reaped, got %d", reaped)
	}
}
