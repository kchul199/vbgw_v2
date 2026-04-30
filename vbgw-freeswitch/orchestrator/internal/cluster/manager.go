package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"

	"github.com/redis/go-redis/v9"
)

type Options struct {
	HeartbeatInterval   time.Duration
	HeartbeatTTL        time.Duration
	ReaperInterval      time.Duration
	LeaseTTL            time.Duration
	LeaseStaleGrace     time.Duration
	OrchestratorVersion string
	LeaseSchemaVersion  int
	LocalSessions       func() int64
	RoutingVersion      func() int
}

type Manager struct {
	client *redis.Client
	nodeID string

	mu            sync.RWMutex
	state         string
	reason        string
	startedAt     time.Time
	lastHeartbeat time.Time

	opts   Options
	leases *LeaseStore
}

func NewManager(client *redis.Client, nodeID string, opts Options) *Manager {
	if client == nil || nodeID == "" {
		return nil
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = 2 * time.Second
	}
	if opts.HeartbeatTTL <= 0 {
		opts.HeartbeatTTL = 8 * time.Second
	}
	if opts.ReaperInterval <= 0 {
		opts.ReaperInterval = 5 * time.Second
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = 15 * time.Second
	}
	if opts.LeaseStaleGrace <= 0 {
		opts.LeaseStaleGrace = opts.HeartbeatTTL
	}
	if opts.OrchestratorVersion == "" {
		opts.OrchestratorVersion = "phase7-dev"
	}
	if opts.LeaseSchemaVersion <= 0 {
		opts.LeaseSchemaVersion = 1
	}
	mgr := &Manager{
		client:    client,
		nodeID:    nodeID,
		state:     NodeStateActive,
		startedAt: time.Now(),
		opts:      opts,
	}
	mgr.leases = NewLeaseStore(client, nodeID, opts.LeaseTTL, opts.LeaseStaleGrace, opts.LeaseSchemaVersion)
	return mgr
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	go m.heartbeatLoop(ctx)
	go m.commandLoop(ctx)
	go m.reaperLoop(ctx)
}

func (m *Manager) NodeID() string {
	if m == nil {
		return ""
	}
	return m.nodeID
}

func (m *Manager) LeaseStore() *LeaseStore {
	if m == nil {
		return nil
	}
	return m.leases
}

func (m *Manager) CurrentState() string {
	if m == nil {
		return NodeStateActive
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) BlocksNewAdmits() bool {
	state := m.CurrentState()
	return state == NodeStateDraining || state == NodeStatePaused
}

func (m *Manager) SetState(ctx context.Context, state, reason string) error {
	if m == nil {
		return nil
	}
	state = strings.TrimSpace(strings.ToLower(state))
	if state == "" {
		state = NodeStateActive
	}
	switch state {
	case NodeStateActive, NodeStateDraining, NodeStatePaused, NodeStateOffline:
	default:
		return errors.New("invalid node state")
	}
	m.mu.Lock()
	m.state = state
	m.reason = reason
	m.mu.Unlock()
	return m.writeHeartbeat(ctx, time.Now())
}

func (m *Manager) PublishCommand(ctx context.Context, targetNodeID string, cmd Command) error {
	if m == nil || targetNodeID == "" {
		return nil
	}
	body, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	return m.client.Publish(ctx, nodeChannel(targetNodeID), body).Err()
}

func (m *Manager) ListNodes(ctx context.Context) ([]NodeHeartbeat, error) {
	if m == nil {
		return nil, nil
	}
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := m.client.Scan(ctx, cursor, "vbgw:node:*:heartbeat", 64).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	nodes := make([]NodeHeartbeat, 0, len(keys))
	for _, key := range keys {
		data, err := m.client.Get(ctx, key).Bytes()
		if err != nil {
			continue
		}
		var hb NodeHeartbeat
		if err := json.Unmarshal(data, &hb); err != nil {
			continue
		}
		nodes = append(nodes, hb)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	return nodes, nil
}

func (m *Manager) CompatibilityReport(ctx context.Context, expectedRoutingVersion int) (CompatibilityReport, error) {
	report := CompatibilityReport{
		Compatible: true,
		CheckedAt:  time.Now(),
		LocalNode:  m.nodeID,
	}
	nodes, err := m.ListNodes(ctx)
	if err != nil {
		return report, err
	}
	report.Nodes = nodes
	for _, node := range nodes {
		if node.NodeID == m.nodeID {
			continue
		}
		if node.LeaseSchemaVersion != m.opts.LeaseSchemaVersion {
			report.Compatible = false
			report.Issues = append(report.Issues, CompatibilityIssue{
				NodeID:   node.NodeID,
				Field:    "lease_schema_version",
				Expected: strconvI(m.opts.LeaseSchemaVersion),
				Actual:   strconvI(node.LeaseSchemaVersion),
			})
		}
		if expectedRoutingVersion > 0 && node.RoutingSchemaVersion != 0 && node.RoutingSchemaVersion != expectedRoutingVersion {
			report.Compatible = false
			report.Issues = append(report.Issues, CompatibilityIssue{
				NodeID:   node.NodeID,
				Field:    "routing_schema_version",
				Expected: strconvI(expectedRoutingVersion),
				Actual:   strconvI(node.RoutingSchemaVersion),
			})
		}
		if !versionCompatible(m.opts.OrchestratorVersion, node.OrchestratorVersion) {
			report.Compatible = false
			report.Issues = append(report.Issues, CompatibilityIssue{
				NodeID:   node.NodeID,
				Field:    "orchestrator_version",
				Expected: m.opts.OrchestratorVersion,
				Actual:   node.OrchestratorVersion,
			})
		}
	}
	return report, nil
}

func (m *Manager) ListLeases(ctx context.Context) ([]LeaseRecord, error) {
	if m == nil || m.leases == nil {
		return nil, nil
	}
	records, err := m.leases.List(ctx)
	if err != nil {
		return nil, err
	}
	nodeMap := make(map[string]NodeHeartbeat)
	nodes, err := m.ListNodes(ctx)
	if err == nil {
		for _, node := range nodes {
			nodeMap[node.NodeID] = node
		}
	}
	now := time.Now()
	for idx := range records {
		if heartbeat, ok := nodeMap[records[idx].OwnerNodeID]; !ok {
			if now.Sub(records[idx].RenewedAt) >= m.opts.LeaseStaleGrace {
				records[idx].Stale = true
				records[idx].StaleReason = "owner_heartbeat_missing"
			}
		} else if now.Sub(heartbeat.HeartbeatAt) > time.Duration(heartbeat.HeartbeatTTLMS)*time.Millisecond {
			records[idx].Stale = true
			records[idx].StaleReason = "owner_heartbeat_stale"
		} else if heartbeat.State == NodeStateOffline {
			records[idx].Stale = true
			records[idx].StaleReason = "owner_offline"
		}
	}
	return records, nil
}

func (m *Manager) writeHeartbeat(ctx context.Context, now time.Time) error {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	state := m.state
	reason := m.reason
	startedAt := m.startedAt
	m.mu.RUnlock()

	localSessions := int64(0)
	if m.opts.LocalSessions != nil {
		localSessions = m.opts.LocalSessions()
	}
	routingVersion := 0
	if m.opts.RoutingVersion != nil {
		routingVersion = m.opts.RoutingVersion()
	}
	hb := NodeHeartbeat{
		NodeID:               m.nodeID,
		State:                state,
		Reason:               reason,
		StartedAt:            startedAt,
		HeartbeatAt:          now,
		HeartbeatTTLMS:       m.opts.HeartbeatTTL.Milliseconds(),
		LocalActiveSessions:  localSessions,
		OrchestratorVersion:  m.opts.OrchestratorVersion,
		RoutingSchemaVersion: routingVersion,
		LeaseSchemaVersion:   m.opts.LeaseSchemaVersion,
	}
	body, err := json.Marshal(hb)
	if err != nil {
		return err
	}
	pipe := m.client.Pipeline()
	pipe.Set(ctx, nodeHeartbeatKey(m.nodeID), body, m.opts.HeartbeatTTL)
	pipe.Set(ctx, nodeStateKey(m.nodeID), state, m.opts.HeartbeatTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.lastHeartbeat = now
	m.mu.Unlock()
	metrics.NodeHeartbeatAgeSeconds.WithLabelValues(m.nodeID).Set(0)
	for _, candidate := range []string{NodeStateActive, NodeStateDraining, NodeStatePaused, NodeStateOffline} {
		value := 0.0
		if state == candidate {
			value = 1
		}
		metrics.NodeState.WithLabelValues(m.nodeID, candidate).Set(value)
	}
	metrics.DrainSessionsRemaining.WithLabelValues(m.nodeID).Set(float64(localSessions))
	return nil
}

func (m *Manager) heartbeatLoop(ctx context.Context) {
	_ = m.writeHeartbeat(context.Background(), time.Now())
	ticker := time.NewTicker(m.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = m.SetState(context.Background(), NodeStateOffline, "shutdown")
			return
		case <-ticker.C:
			now := time.Now()
			_ = m.writeHeartbeat(context.Background(), now)
			m.mu.RLock()
			lastHeartbeat := m.lastHeartbeat
			m.mu.RUnlock()
			metrics.NodeHeartbeatAgeSeconds.WithLabelValues(m.nodeID).Set(now.Sub(lastHeartbeat).Seconds())
		}
	}
}

func (m *Manager) commandLoop(ctx context.Context) {
	pubsub := m.client.Subscribe(ctx, nodeChannel(m.nodeID))
	defer pubsub.Close()
	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			if msg == nil {
				continue
			}
			var cmd Command
			if err := json.Unmarshal([]byte(msg.Payload), &cmd); err != nil {
				continue
			}
			switch cmd.Action {
			case "set_state":
				_ = m.SetState(context.Background(), cmd.State, cmd.Reason)
			}
		}
	}
}

func (m *Manager) reaperLoop(ctx context.Context) {
	if m.leases == nil {
		return
	}
	ticker := time.NewTicker(m.opts.ReaperInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := m.ListNodes(context.Background())
			if err != nil {
				continue
			}
			nodeMap := make(map[string]NodeHeartbeat, len(nodes))
			for _, node := range nodes {
				nodeMap[node.NodeID] = node
			}
			_, _ = m.leases.ReapStale(context.Background(), nodeMap, time.Now())
		}
	}
}

func versionCompatible(local, remote string) bool {
	local = strings.TrimSpace(local)
	remote = strings.TrimSpace(remote)
	if local == "" || remote == "" {
		return true
	}
	localMajor := versionMajor(local)
	remoteMajor := versionMajor(remote)
	if localMajor != "" && remoteMajor != "" {
		return localMajor == remoteMajor
	}
	return local == remote
}

func versionMajor(version string) string {
	if version == "" {
		return ""
	}
	version = strings.TrimPrefix(version, "v")
	parts := strings.Split(version, ".")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

func strconvI(value int) string {
	return strconv.Itoa(value)
}

func DefaultNodeID() string {
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	return ""
}
