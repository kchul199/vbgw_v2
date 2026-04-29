package main

import (
	"context"
	"testing"

	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/config"
	"vbgw-orchestrator/internal/esl"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"
)

type mockESLPark struct {
	killCalled         int
	transferCalled     int
	lastTransferTarget string
	breakCalled        int
	setVars            map[string]string
}

func (m *mockESLPark) Originate(ctx context.Context, uuid, target, callerID string, gatewayOrder []string) (string, error) {
	return "", nil
}

func (m *mockESLPark) SendDtmf(ctx context.Context, uuid, digits string) error { return nil }
func (m *mockESLPark) SetVar(ctx context.Context, uuid, key, value string) error {
	if m.setVars == nil {
		m.setVars = make(map[string]string)
	}
	m.setVars[key] = value
	return nil
}

func (m *mockESLPark) Transfer(ctx context.Context, uuid, target string) error {
	m.transferCalled++
	m.lastTransferTarget = target
	return nil
}

func (m *mockESLPark) TransferViaGateway(ctx context.Context, uuid, target, gateway string) error {
	m.transferCalled++
	m.lastTransferTarget = gateway + ":" + target
	return nil
}

func (m *mockESLPark) Bridge(ctx context.Context, uuidA, uuidB string) error { return nil }

func (m *mockESLPark) Unbridge(ctx context.Context, uuid string) error { return nil }

func (m *mockESLPark) RecordStart(ctx context.Context, uuid, path string) error { return nil }

func (m *mockESLPark) RecordStop(ctx context.Context, uuid string) error { return nil }

func (m *mockESLPark) Break(ctx context.Context, uuid string) error {
	m.breakCalled++
	return nil
}

func (m *mockESLPark) Kill(ctx context.Context, uuid string) error {
	m.killCalled++
	return nil
}

func (m *mockESLPark) Dump(ctx context.Context, uuid string) (map[string]string, error) {
	return nil, nil
}

func (m *mockESLPark) Pause(ctx context.Context) error { return nil }

func (m *mockESLPark) Resume(ctx context.Context) error { return nil }

func (m *mockESLPark) IsConnected() bool { return true }

func (m *mockESLPark) Eavesdrop(ctx context.Context, supervisorUUID, targetUUID string) error {
	return nil
}

func (m *mockESLPark) ConferenceKick(ctx context.Context, confName, memberID string) error {
	return nil
}

func (m *mockESLPark) AttendedTransfer(ctx context.Context, uuid, target, gateway string) error {
	return nil
}

func (m *mockESLPark) SendAPI(ctx context.Context, cmd string) (string, error) {
	return "+OK", nil
}

var _ esl.Commander = (*mockESLPark)(nil)

func TestOnChannelPark_OverflowBusyKillsCall(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{}
	mgr := capacity.NewManager(capacityConfig(routing.OverflowBusy, ""))
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-1", "fs-1", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-1", "1000", "bot-main")
	eslMock := &mockESLPark{}

	onChannelPark(context.Background(), evt, store, mgr, nil, nil, nil, eslMock, cfg, "node")

	if eslMock.killCalled != 1 {
		t.Fatalf("expected one kill call, got %d", eslMock.killCalled)
	}
	if eslMock.transferCalled != 0 {
		t.Fatalf("expected no transfer call, got %d", eslMock.transferCalled)
	}

	updated, ok := store.GetByFSUUID(context.Background(), "fs-1")
	if !ok {
		t.Fatal("session missing after overflow handling")
	}
	if updated.AllocationState != session.AllocationOverflowed {
		t.Fatalf("expected allocation state %q, got %q", session.AllocationOverflowed, updated.AllocationState)
	}
	if updated.OverflowPolicy != routing.OverflowBusy {
		t.Fatalf("expected overflow policy %q, got %q", routing.OverflowBusy, updated.OverflowPolicy)
	}
}

func TestOnChannelPark_OverflowDirectTransferTransfersCall(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{}
	mgr := capacity.NewManager(capacityConfig(routing.OverflowDirectTransfer, "2100"))
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-2", "fs-2", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-2", "1000", "bot-main")
	eslMock := &mockESLPark{}

	onChannelPark(context.Background(), evt, store, mgr, nil, nil, nil, eslMock, cfg, "node")

	if eslMock.transferCalled != 1 {
		t.Fatalf("expected one transfer call, got %d", eslMock.transferCalled)
	}
	if eslMock.lastTransferTarget != "2100" {
		t.Fatalf("expected transfer target 2100, got %q", eslMock.lastTransferTarget)
	}
	if eslMock.killCalled != 0 {
		t.Fatalf("expected no kill call, got %d", eslMock.killCalled)
	}

	updated, ok := store.GetByFSUUID(context.Background(), "fs-2")
	if !ok {
		t.Fatal("session missing after overflow handling")
	}
	if updated.AllocationState != session.AllocationOverflowed {
		t.Fatalf("expected allocation state %q, got %q", session.AllocationOverflowed, updated.AllocationState)
	}
	if updated.OverflowPolicy != routing.OverflowDirectTransfer {
		t.Fatalf("expected overflow policy %q, got %q", routing.OverflowDirectTransfer, updated.OverflowPolicy)
	}
	if updated.OverflowTarget != "2100" {
		t.Fatalf("expected overflow target 2100, got %q", updated.OverflowTarget)
	}
}

func TestOnChannelPark_LeasedSlotReleasedOnSessionRelease(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{}
	mgr := capacity.NewManager(capacityConfig(routing.OverflowBusy, ""))

	s := session.NewSession("node", "session-3", "fs-3", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-3", "1000", "bot-main")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, nil, nil, nil, eslMock, cfg, "node")

	updated, ok := store.GetByFSUUID(context.Background(), "fs-3")
	if !ok {
		t.Fatal("session missing after park")
	}
	if updated.AllocationState != session.AllocationLeased {
		t.Fatalf("expected allocation state %q, got %q", session.AllocationLeased, updated.AllocationState)
	}
	if eslMock.transferCalled != 1 || eslMock.lastTransferTarget != vbgwAIStartExtension {
		t.Fatalf("expected AI start transfer, got count=%d target=%q", eslMock.transferCalled, eslMock.lastTransferTarget)
	}

	snapshots := mgr.Snapshots()
	if len(snapshots) != 1 || snapshots[0].ActiveCalls != 1 {
		t.Fatalf("expected one active leased slot, got %+v", snapshots)
	}

	store.Release(context.Background(), updated.SessionID)

	snapshots = mgr.Snapshots()
	if len(snapshots) != 1 || snapshots[0].ActiveCalls != 0 {
		t.Fatalf("expected slot to be released, got %+v", snapshots)
	}
}

func capacityConfig(overflowPolicy, transferTarget string) *routing.Config {
	return &routing.Config{
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
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: overflowPolicy,
					TransferTarget: transferTarget,
				},
			},
		},
	}
}

func TestOnChannelPark_OverflowQueueEnqueuesCall(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{BridgeHost: "127.0.0.1", BridgeInternalPort: 8091}
	mgr := capacity.NewManager(capacityConfigWithOptions(routing.OverflowQueue, "", "", 15, routing.OverflowBusy))
	queueMgr := overflow.NewManager()
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-4", "fs-4", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-4", "1000", "bot-main")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, queueMgr, nil, nil, eslMock, cfg, "node")

	if eslMock.killCalled != 0 || eslMock.transferCalled != 1 {
		t.Fatalf("expected queued call to enter hold media path, got kill=%d transfer=%d", eslMock.killCalled, eslMock.transferCalled)
	}
	if eslMock.lastTransferTarget != vbgwQueueHoldExtension {
		t.Fatalf("expected queue hold transfer target %q, got %q", vbgwQueueHoldExtension, eslMock.lastTransferTarget)
	}
	if eslMock.setVars["vbgw_queue_hold_music"] != vbgwDefaultQueueHoldMusic {
		t.Fatalf("expected queue hold music %q, got %q", vbgwDefaultQueueHoldMusic, eslMock.setVars["vbgw_queue_hold_music"])
	}
	updated, ok := store.GetByFSUUID(context.Background(), "fs-4")
	if !ok {
		t.Fatal("session missing after queue handling")
	}
	if updated.State != session.StateQueued {
		t.Fatalf("expected session queued, got %q", updated.State)
	}
	if snapshot := queueMgr.Snapshot("bot-main", updated.CreatedAt); snapshot.Depth != 1 {
		t.Fatalf("expected queue depth 1, got %+v", snapshot)
	}
}

func TestOnChannelPark_OverflowFallbackHumanTransfersToCallcenter(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{
		BridgeHost:           "127.0.0.1",
		BridgeInternalPort:   8091,
		HumanFallbackEnabled: true,
	}
	mgr := capacity.NewManager(capacityConfig(routing.OverflowFallbackHuman, "callcenter:support@default"))
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-human", "fs-human", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-human", "1000", "bot-main")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, overflow.NewManager(), nil, nil, eslMock, cfg, "node")

	if eslMock.killCalled != 0 {
		t.Fatalf("expected no kill call, got %d", eslMock.killCalled)
	}
	if eslMock.transferCalled != 1 {
		t.Fatalf("expected one transfer call, got %d", eslMock.transferCalled)
	}
	if eslMock.lastTransferTarget != vbgwHumanCallcenterExt {
		t.Fatalf("expected human fallback transfer target %q, got %q", vbgwHumanCallcenterExt, eslMock.lastTransferTarget)
	}
	if eslMock.setVars["vbgw_human_queue"] != "support@default" {
		t.Fatalf("expected callcenter queue var support@default, got %q", eslMock.setVars["vbgw_human_queue"])
	}
	updated, ok := store.GetByFSUUID(context.Background(), "fs-human")
	if !ok {
		t.Fatal("session missing after human fallback handling")
	}
	if !updated.IsAIPaused() {
		t.Fatal("expected AI to be paused during human fallback")
	}
	if updated.State != session.StateTransferring {
		t.Fatalf("expected transferring state, got %q", updated.State)
	}
	if updated.AllocationState != session.AllocationOverflowed {
		t.Fatalf("expected overflowed allocation state, got %q", updated.AllocationState)
	}
	if updated.OverflowTarget != "callcenter:support@default" {
		t.Fatalf("expected overflow target callcenter:support@default, got %q", updated.OverflowTarget)
	}
}

func TestOnChannelPark_SIPExtensionBackendTransfersToExtension(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{BridgeHost: "127.0.0.1", BridgeInternalPort: 8091}
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "frontdesk-bot",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"9390"},
				Capacity: routing.Capacity{
					Backend:        routing.BackendSIPExtension,
					Allocator:      routing.AllocatorPriority,
					Extensions:     []string{"1001", "1002"},
					MaxConcurrent:  2,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})

	s := session.NewSession("node", "session-ext", "fs-ext", "010", "9390")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-ext", "9390", "frontdesk-bot")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, overflow.NewManager(), nil, nil, eslMock, cfg, "node")

	if eslMock.transferCalled != 1 {
		t.Fatalf("expected one transfer call, got %d", eslMock.transferCalled)
	}
	if eslMock.lastTransferTarget != "1001" {
		t.Fatalf("expected transfer target 1001, got %q", eslMock.lastTransferTarget)
	}
	updated, ok := store.GetByFSUUID(context.Background(), "fs-ext")
	if !ok {
		t.Fatal("session missing after sip extension routing")
	}
	if updated.SlotID != "1001" {
		t.Fatalf("expected slot id 1001, got %q", updated.SlotID)
	}
	if !updated.IsAIPaused() {
		t.Fatal("expected AI paused when call is handed to extension slot")
	}
}

func TestOnChannelPark_OverflowFailoverServiceUsesSecondary(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{}
	mgr := capacity.NewManager(&routing.Config{
		Version:  1,
		Defaults: routing.Defaults{OnUnknownEntry: routing.UnknownStaticFallback},
		Services: []routing.ServiceRoute{
			{
				Name:      "bot-main",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Capacity: routing.Capacity{
					MaxConcurrent:   1,
					Allocator:       routing.AllocatorRoundRobin,
					OverflowPolicy:  routing.OverflowFailoverSvc,
					OverflowService: "bot-backup",
				},
			},
			{
				Name:      "bot-backup",
				Enabled:   true,
				RouteType: routing.RouteTypeAI,
				EntryNums: []string{"1000"},
				Capacity: routing.Capacity{
					MaxConcurrent:  1,
					Allocator:      routing.AllocatorRoundRobin,
					OverflowPolicy: routing.OverflowBusy,
				},
			},
		},
	})
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-5", "fs-5", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-5", "1000", "bot-main")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, overflow.NewManager(), nil, nil, eslMock, cfg, "node")

	updated, ok := store.GetByFSUUID(context.Background(), "fs-5")
	if !ok {
		t.Fatal("session missing after failover handling")
	}
	if updated.ServiceName != "bot-backup" {
		t.Fatalf("expected service failover to bot-backup, got %q", updated.ServiceName)
	}
	if updated.AllocationState != session.AllocationLeased {
		t.Fatalf("expected failover service slot to be leased, got %q", updated.AllocationState)
	}
	if eslMock.lastTransferTarget != vbgwAIStartExtension {
		t.Fatalf("expected failover service to enter AI start path, got %q", eslMock.lastTransferTarget)
	}
}

func TestProcessOverflowQueuesOnce_ActivatesQueuedSession(t *testing.T) {
	store := session.NewMemoryStore(10)
	cfg := &config.Config{BridgeHost: "127.0.0.1", BridgeInternalPort: 8091}
	mgr := capacity.NewManager(capacityConfigWithOptions(routing.OverflowQueue, "", "", 15, routing.OverflowBusy))
	queueMgr := overflow.NewManager()
	mgr.Admit("existing", "bot-main")

	s := session.NewSession("node", "session-6", "fs-6", "010", "1000")
	s.SetOnRelease(mgr.Release)
	if !store.AddIfUnderCapacity(context.Background(), s) {
		t.Fatal("failed to add session")
	}

	evt := parkEvent("fs-6", "1000", "bot-main")
	eslMock := &mockESLPark{}
	onChannelPark(context.Background(), evt, store, mgr, queueMgr, nil, nil, eslMock, cfg, "node")

	mgr.Release("existing")
	processOverflowQueuesOnce(context.Background(), store, mgr, queueMgr, nil, nil, eslMock, cfg)

	updated, ok := store.GetByFSUUID(context.Background(), "fs-6")
	if !ok {
		t.Fatal("session missing after dequeue handling")
	}
	if updated.State != session.StateActive {
		t.Fatalf("expected queued session to become active, got %q", updated.State)
	}
	if updated.AllocationState != session.AllocationLeased {
		t.Fatalf("expected dequeued session slot lease, got %q", updated.AllocationState)
	}
	if eslMock.transferCalled != 2 || eslMock.lastTransferTarget != vbgwAIStartExtension {
		t.Fatalf("expected queue hold then AI start transfers, got count=%d target=%q", eslMock.transferCalled, eslMock.lastTransferTarget)
	}
}

func capacityConfigWithOptions(overflowPolicy, transferTarget, overflowService string, queueWaitSec int, queueOnTimeout string) *routing.Config {
	cfg := capacityConfig(overflowPolicy, transferTarget)
	cfg.Services[0].Capacity.OverflowService = overflowService
	cfg.Services[0].Capacity.QueueMaxWaitSec = queueWaitSec
	cfg.Services[0].Capacity.QueueOnTimeout = queueOnTimeout
	return cfg
}

func parkEvent(fsUUID, entryNumber, serviceName string) *esl.Event {
	return &esl.Event{Headers: map[string]string{
		"Unique-ID":                            fsUUID,
		"variable_vbgw_entry_number":           entryNumber,
		"variable_vbgw_service_name":           serviceName,
		"variable_vbgw_route_type":             "ai",
		"variable_vbgw_ingress_stage":          "default-policy",
		"variable_vbgw_routing_config_version": "1",
		"variable_sip_gateway_name":            "pbx-main",
	}}
}
