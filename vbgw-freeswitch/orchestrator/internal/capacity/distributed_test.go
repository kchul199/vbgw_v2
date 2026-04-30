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
