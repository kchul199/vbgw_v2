package capacity

import (
	"context"
	"testing"
	"time"

	"vbgw-orchestrator/internal/cluster"
	"vbgw-orchestrator/internal/routing"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestManagerDistributedLeasePreventsDoubleAllocation(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{{
			Name:    "bot-main",
			Enabled: true,
			Capacity: routing.Capacity{
				Backend:       routing.BackendLogical,
				MaxConcurrent: 1,
				Allocator:     routing.AllocatorRoundRobin,
			},
		}},
	}

	clusterA := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		LeaseTTL:            5 * time.Second,
		LeaseStaleGrace:     2 * time.Second,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	clusterB := cluster.NewManager(client, "node-b", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		LeaseTTL:            5 * time.Second,
		LeaseStaleGrace:     2 * time.Second,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clusterA.Start(ctx)
	clusterB.Start(ctx)

	mgrA := NewManager(cfg)
	mgrA.SetDistributedLeases(clusterA.LeaseStore())
	mgrA.SetNodeStateProvider(clusterA)
	mgrB := NewManager(cfg)
	mgrB.SetDistributedLeases(clusterB.LeaseStore())
	mgrB.SetNodeStateProvider(clusterB)

	first := mgrA.AdmitRequest(AdmitRequest{SessionID: "sess-a", ServiceName: "bot-main"})
	if !first.Allowed {
		t.Fatalf("expected first admit to succeed, got %+v", first)
	}
	second := mgrB.AdmitRequest(AdmitRequest{SessionID: "sess-b", ServiceName: "bot-main"})
	if second.Allowed {
		t.Fatalf("expected second admit to fail due to distributed lease, got %+v", second)
	}
}

func TestManagerDistributedLeaseStaleReapAllowsStandbyAdmit(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{{
			Name:    "bot-main",
			Enabled: true,
			Capacity: routing.Capacity{
				Backend:       routing.BackendLogical,
				MaxConcurrent: 1,
				Allocator:     routing.AllocatorRoundRobin,
			},
		}},
	}

	clusterA := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   25 * time.Millisecond,
		HeartbeatTTL:        80 * time.Millisecond,
		ReaperInterval:      25 * time.Millisecond,
		LeaseTTL:            5 * time.Second,
		LeaseStaleGrace:     40 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	clusterB := cluster.NewManager(client, "node-b", cluster.Options{
		HeartbeatInterval:   25 * time.Millisecond,
		HeartbeatTTL:        80 * time.Millisecond,
		ReaperInterval:      25 * time.Millisecond,
		LeaseTTL:            5 * time.Second,
		LeaseStaleGrace:     40 * time.Millisecond,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})

	ctxA, cancelA := context.WithCancel(context.Background())
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	clusterA.Start(ctxA)
	clusterB.Start(ctxB)

	mgrA := NewManager(cfg)
	mgrA.SetDistributedLeases(clusterA.LeaseStore())
	mgrA.SetNodeStateProvider(clusterA)
	mgrB := NewManager(cfg)
	mgrB.SetDistributedLeases(clusterB.LeaseStore())
	mgrB.SetNodeStateProvider(clusterB)

	first := mgrA.AdmitRequest(AdmitRequest{SessionID: "sess-a", ServiceName: "bot-main"})
	if !first.Allowed {
		t.Fatalf("expected first admit on node-a to succeed, got %+v", first)
	}
	blocked := mgrB.AdmitRequest(AdmitRequest{SessionID: "sess-b", ServiceName: "bot-main"})
	if blocked.Allowed {
		t.Fatalf("expected node-b admit to be blocked before stale reap, got %+v", blocked)
	}

	cancelA()
	if err := client.Del(context.Background(), "vbgw:node:node-a:heartbeat").Err(); err != nil {
		t.Fatalf("delete node-a heartbeat: %v", err)
	}
	if err := client.HSet(context.Background(), "vbgw:lease:service:bot-main:slot:bot-main-01", "renewed_at_unix_ms", time.Now().Add(-time.Minute).UnixMilli()).Err(); err != nil {
		t.Fatalf("age node-a lease: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		leases, err := clusterB.ListLeases(context.Background())
		if err != nil {
			t.Fatalf("list leases: %v", err)
		}
		if len(leases) == 0 {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	recovered := mgrB.AdmitRequest(AdmitRequest{SessionID: "sess-b", ServiceName: "bot-main"})
	if !recovered.Allowed {
		t.Fatalf("expected standby node-b admit after stale reap, got %+v", recovered)
	}
	if recovered.SlotID != "bot-main-01" {
		t.Fatalf("expected recovered slot bot-main-01, got %q", recovered.SlotID)
	}
}

func TestManagerRenewFailureBlocksNewAdmits(t *testing.T) {
	srv := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	cfg := &routing.Config{
		Services: []routing.ServiceRoute{{
			Name:    "bot-main",
			Enabled: true,
			Capacity: routing.Capacity{
				Backend:       routing.BackendLogical,
				MaxConcurrent: 2,
				Allocator:     routing.AllocatorRoundRobin,
			},
		}},
	}
	clusterA := cluster.NewManager(client, "node-a", cluster.Options{
		HeartbeatInterval:   50 * time.Millisecond,
		HeartbeatTTL:        200 * time.Millisecond,
		LeaseTTL:            5 * time.Second,
		LeaseStaleGrace:     2 * time.Second,
		OrchestratorVersion: "7.1.0",
		LeaseSchemaVersion:  1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clusterA.Start(ctx)

	mgr := NewManager(cfg)
	mgr.SetDistributedLeases(clusterA.LeaseStore())
	mgr.SetNodeStateProvider(clusterA)

	first := mgr.AdmitRequest(AdmitRequest{SessionID: "sess-a", ServiceName: "bot-main"})
	if !first.Allowed {
		t.Fatalf("expected first admit to succeed, got %+v", first)
	}

	srv.Close()
	mgr.RenewLeases()

	second := mgr.AdmitRequest(AdmitRequest{SessionID: "sess-b", ServiceName: "bot-main"})
	if second.Allowed {
		t.Fatalf("expected new admit blocked after renew failure, got %+v", second)
	}
	if second.Reason != "distributed lease renew degraded" {
		t.Fatalf("expected renew degraded reason, got %q", second.Reason)
	}
}
