package overflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"

	"github.com/redis/go-redis/v9"
)

type Option func(*Manager)

func WithRedisClient(client *redis.Client, nodeID string, claimTTL time.Duration, scanLimit int) Option {
	return func(m *Manager) {
		if client == nil {
			return
		}
		if claimTTL <= 0 {
			claimTTL = 3 * time.Second
		}
		if scanLimit <= 0 {
			scanLimit = 8
		}
		m.redis = client
		m.nodeID = nodeID
		m.claimTTL = claimTTL
		m.scanLimit = scanLimit
	}
}

type Manager struct {
	mu        sync.Mutex
	byService map[string][]QueueEntry
	bySession map[string]string

	redis     *redis.Client
	nodeID    string
	claimTTL  time.Duration
	scanLimit int
}

func NewManager(opts ...Option) *Manager {
	mgr := &Manager{
		byService: make(map[string][]QueueEntry),
		bySession: make(map[string]string),
		claimTTL:  3 * time.Second,
		scanLimit: 8,
	}
	for _, opt := range opts {
		opt(mgr)
	}
	return mgr
}

func (m *Manager) Enqueue(entry QueueEntry) bool {
	if m == nil || entry.SessionID == "" || entry.ServiceName == "" {
		return false
	}
	if !entry.EnqueuedAt.IsZero() && entry.TimeoutAt.Before(entry.EnqueuedAt) {
		entry.TimeoutAt = entry.EnqueuedAt
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if serviceName, ok := m.bySession[entry.SessionID]; ok {
		if serviceName == entry.ServiceName {
			return false
		}
		m.removeLocked(context.Background(), entry.SessionID)
	}

	m.byService[entry.ServiceName] = append(m.byService[entry.ServiceName], entry)
	m.bySession[entry.SessionID] = entry.ServiceName
	if m.redis != nil {
		m.persistEntry(context.Background(), entry)
	}
	m.updateMetricsLocked(entry.ServiceName)
	return true
}

func (m *Manager) Remove(sessionID string) bool {
	if m == nil || sessionID == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeLocked(context.Background(), sessionID)
}

func (m *Manager) removeLocked(ctx context.Context, sessionID string) bool {
	serviceName, ok := m.bySession[sessionID]
	if !ok {
		if m.redis != nil {
			serviceName = m.serviceNameForSession(ctx, sessionID)
		}
	}

	queue := m.byService[serviceName]
	removed := false
	for idx, entry := range queue {
		if entry.SessionID != sessionID {
			continue
		}
		queue = append(queue[:idx], queue[idx+1:]...)
		if len(queue) == 0 {
			delete(m.byService, serviceName)
		} else {
			m.byService[serviceName] = queue
		}
		removed = true
		break
	}
	delete(m.bySession, sessionID)
	if m.redis != nil {
		m.deleteEntry(ctx, serviceName, sessionID)
	}
	if serviceName != "" {
		m.updateMetricsLocked(serviceName)
	}
	return removed || serviceName != ""
}

func (m *Manager) Peek(serviceName string) (QueueEntry, bool) {
	if m == nil || serviceName == "" {
		return QueueEntry{}, false
	}
	if m.redis != nil {
		return m.peekRedis(context.Background(), serviceName)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	queue := m.byService[serviceName]
	if len(queue) == 0 {
		return QueueEntry{}, false
	}
	return queue[0], true
}

func (m *Manager) ClaimNext(serviceName string, now time.Time) (QueueEntry, bool) {
	if m == nil || serviceName == "" {
		return QueueEntry{}, false
	}
	if m.redis != nil {
		return m.claimNextRedis(context.Background(), serviceName)
	}
	return m.Peek(serviceName)
}

func (m *Manager) ReleaseClaim(sessionID string) {
	if m == nil || sessionID == "" || m.redis == nil {
		return
	}
	_ = m.redis.Del(context.Background(), claimKey(sessionID)).Err()
}

func (m *Manager) Snapshot(serviceName string, now time.Time) QueueSnapshot {
	if m == nil {
		return QueueSnapshot{ServiceName: serviceName}
	}
	if m.redis != nil {
		return m.snapshotRedis(context.Background(), serviceName, now)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return snapshotFromQueue(serviceName, m.byService[serviceName], now)
}

func (m *Manager) Snapshots(now time.Time) []QueueSnapshot {
	if m == nil {
		return nil
	}
	if m.redis != nil {
		return m.snapshotsRedis(context.Background(), now)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	serviceNames := make([]string, 0, len(m.byService))
	for serviceName := range m.byService {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)

	snapshots := make([]QueueSnapshot, 0, len(serviceNames))
	for _, serviceName := range serviceNames {
		snapshots = append(snapshots, snapshotFromQueue(serviceName, m.byService[serviceName], now))
	}
	return snapshots
}

func (m *Manager) ServiceNames() []string {
	if m == nil {
		return nil
	}
	if m.redis != nil {
		return m.serviceNamesRedis(context.Background())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	serviceNames := make([]string, 0, len(m.byService))
	for serviceName := range m.byService {
		serviceNames = append(serviceNames, serviceName)
	}
	sort.Strings(serviceNames)
	return serviceNames
}

func (m *Manager) RefreshMetrics(now time.Time) {
	if m == nil {
		return
	}
	for _, snapshot := range m.Snapshots(now) {
		metrics.ServiceQueueDepth.WithLabelValues(snapshot.ServiceName).Set(float64(snapshot.Depth))
		metrics.QueueWaitSeconds.WithLabelValues(snapshot.ServiceName).Set(snapshot.OldestWaitSeconds)
	}
}

func (m *Manager) updateMetricsLocked(serviceName string) {
	depth := 0
	oldest := 0.0
	if queue := m.byService[serviceName]; len(queue) > 0 {
		depth = len(queue)
		oldest = time.Since(queue[0].EnqueuedAt).Seconds()
	}
	metrics.ServiceQueueDepth.WithLabelValues(serviceName).Set(float64(depth))
	metrics.QueueWaitSeconds.WithLabelValues(serviceName).Set(oldest)
}

func snapshotFromQueue(serviceName string, queue []QueueEntry, now time.Time) QueueSnapshot {
	snapshot := QueueSnapshot{
		ServiceName: serviceName,
		Depth:       len(queue),
		Sessions:    make([]QueueEntrySnapshot, 0, len(queue)),
	}
	if len(queue) > 0 {
		snapshot.OldestWaitSeconds = now.Sub(queue[0].EnqueuedAt).Seconds()
	}
	for _, entry := range queue {
		snapshot.Sessions = append(snapshot.Sessions, QueueEntrySnapshot{
			SessionID:      entry.SessionID,
			QueueOnTimeout: entry.QueueOnTimeout,
			EnqueuedAt:     entry.EnqueuedAt,
			TimeoutAt:      entry.TimeoutAt,
		})
	}
	return snapshot
}

func (m *Manager) persistEntry(ctx context.Context, entry QueueEntry) {
	if ctx == nil {
		ctx = context.Background()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	serviceKey := queueKey(entry.ServiceName)
	pipe := m.redis.Pipeline()
	pipe.SAdd(ctx, queueServicesKey(), entry.ServiceName)
	pipe.Set(ctx, sessionKey(entry.SessionID), data, 24*time.Hour)
	pipe.Set(ctx, queueSessionServiceKey(entry.SessionID), entry.ServiceName, 24*time.Hour)
	pipe.ZAdd(ctx, serviceKey, redis.Z{
		Score:  float64(entry.EnqueuedAt.UnixNano()),
		Member: entry.SessionID,
	})
	_, _ = pipe.Exec(ctx)
}

func (m *Manager) deleteEntry(ctx context.Context, serviceName, sessionID string) {
	if ctx == nil {
		ctx = context.Background()
	}
	pipe := m.redis.Pipeline()
	if serviceName != "" {
		pipe.ZRem(ctx, queueKey(serviceName), sessionID)
	}
	pipe.Del(ctx, sessionKey(sessionID))
	pipe.Del(ctx, queueSessionServiceKey(sessionID))
	pipe.Del(ctx, claimKey(sessionID))
	_, _ = pipe.Exec(ctx)
}

func (m *Manager) serviceNameForSession(ctx context.Context, sessionID string) string {
	serviceName, err := m.redis.Get(ctx, queueSessionServiceKey(sessionID)).Result()
	if err != nil {
		return ""
	}
	return stringsTrim(serviceName)
}

func (m *Manager) peekRedis(ctx context.Context, serviceName string) (QueueEntry, bool) {
	ids, err := m.redis.ZRange(ctx, queueKey(serviceName), 0, 0).Result()
	if err != nil || len(ids) == 0 {
		return QueueEntry{}, false
	}
	return m.loadEntry(ctx, ids[0])
}

func (m *Manager) claimNextRedis(ctx context.Context, serviceName string) (QueueEntry, bool) {
	ids, err := m.redis.ZRange(ctx, queueKey(serviceName), 0, int64(m.scanLimit-1)).Result()
	if err != nil || len(ids) == 0 {
		return QueueEntry{}, false
	}
	for _, sessionID := range ids {
		ok, err := m.redis.SetNX(ctx, claimKey(sessionID), m.nodeID, m.claimTTL).Result()
		if err != nil || !ok {
			continue
		}
		entry, exists := m.loadEntry(ctx, sessionID)
		if !exists {
			_ = m.redis.Del(ctx, claimKey(sessionID)).Err()
			continue
		}
		return entry, true
	}
	return QueueEntry{}, false
}

func (m *Manager) loadEntry(ctx context.Context, sessionID string) (QueueEntry, bool) {
	data, err := m.redis.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		return QueueEntry{}, false
	}
	var entry QueueEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return QueueEntry{}, false
	}
	return entry, true
}

func (m *Manager) snapshotRedis(ctx context.Context, serviceName string, now time.Time) QueueSnapshot {
	ids, err := m.redis.ZRange(ctx, queueKey(serviceName), 0, -1).Result()
	if err != nil {
		return QueueSnapshot{ServiceName: serviceName}
	}
	queue := make([]QueueEntry, 0, len(ids))
	for _, sessionID := range ids {
		if entry, ok := m.loadEntry(ctx, sessionID); ok {
			queue = append(queue, entry)
		}
	}
	return snapshotFromQueue(serviceName, queue, now)
}

func (m *Manager) snapshotsRedis(ctx context.Context, now time.Time) []QueueSnapshot {
	serviceNames := m.serviceNamesRedis(ctx)
	snapshots := make([]QueueSnapshot, 0, len(serviceNames))
	for _, serviceName := range serviceNames {
		snapshot := m.snapshotRedis(ctx, serviceName, now)
		if snapshot.Depth == 0 {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots
}

func (m *Manager) serviceNamesRedis(ctx context.Context) []string {
	names, err := m.redis.SMembers(ctx, queueServicesKey()).Result()
	if err != nil {
		return nil
	}
	filtered := make([]string, 0, len(names))
	for _, name := range names {
		name = stringsTrim(name)
		if name != "" {
			filtered = append(filtered, name)
		}
	}
	sort.Strings(filtered)
	return filtered
}

func queueServicesKey() string {
	return "vbgw:queue:services"
}

func queueKey(serviceName string) string {
	return fmt.Sprintf("vbgw:queue:%s", serviceName)
}

func sessionKey(sessionID string) string {
	return fmt.Sprintf("vbgw:queue:session:%s", sessionID)
}

func queueSessionServiceKey(sessionID string) string {
	return fmt.Sprintf("vbgw:queue:service:%s", sessionID)
}

func claimKey(sessionID string) string {
	return fmt.Sprintf("vbgw:queue:claim:%s", sessionID)
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}
