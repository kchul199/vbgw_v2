package cluster

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"vbgw-orchestrator/internal/metrics"

	"github.com/redis/go-redis/v9"
)

var (
	luaAcquireLease = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 1 then
	return 0
end
redis.call('HSET', KEYS[1],
	'service_name', ARGV[1],
	'slot_id', ARGV[2],
	'session_id', ARGV[3],
	'owner_node_id', ARGV[4],
	'owner_state', ARGV[5],
	'lease_schema_version', ARGV[6],
	'leased_at_unix_ms', ARGV[7],
	'renewed_at_unix_ms', ARGV[7])
redis.call('PEXPIRE', KEYS[1], ARGV[8])
redis.call('SET', KEYS[2], KEYS[1], 'PX', ARGV[8])
return 1
`)
	luaRenewLease = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
	return 0
end
if redis.call('HGET', KEYS[1], 'session_id') ~= ARGV[1] then
	return -1
end
if redis.call('HGET', KEYS[1], 'owner_node_id') ~= ARGV[2] then
	return -2
end
redis.call('HSET', KEYS[1],
	'owner_state', ARGV[3],
	'renewed_at_unix_ms', ARGV[4])
redis.call('PEXPIRE', KEYS[1], ARGV[5])
redis.call('SET', KEYS[2], KEYS[1], 'PX', ARGV[5])
return 1
`)
	luaReleaseLease = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
	return 0
end
if redis.call('HGET', KEYS[1], 'session_id') ~= ARGV[1] then
	return -1
end
if redis.call('HGET', KEYS[1], 'owner_node_id') ~= ARGV[2] then
	return -2
end
redis.call('DEL', KEYS[1])
redis.call('DEL', KEYS[2])
return 1
`)
)

type LeaseStore struct {
	client          *redis.Client
	nodeID          string
	leaseTTL        time.Duration
	staleGrace      time.Duration
	leaseSchemaVers int
}

func (s *LeaseStore) NodeID() string {
	if s == nil {
		return ""
	}
	return s.nodeID
}

func NewLeaseStore(client *redis.Client, nodeID string, leaseTTL, staleGrace time.Duration, leaseSchemaVersion int) *LeaseStore {
	if client == nil || nodeID == "" {
		return nil
	}
	if leaseTTL <= 0 {
		leaseTTL = 15 * time.Second
	}
	if staleGrace <= 0 {
		staleGrace = 10 * time.Second
	}
	if leaseSchemaVersion <= 0 {
		leaseSchemaVersion = 1
	}
	return &LeaseStore{
		client:          client,
		nodeID:          nodeID,
		leaseTTL:        leaseTTL,
		staleGrace:      staleGrace,
		leaseSchemaVers: leaseSchemaVersion,
	}
}

func (s *LeaseStore) Acquire(ctx context.Context, serviceName, slotID, sessionID, ownerState string, now time.Time) (bool, error) {
	if s == nil || serviceName == "" || slotID == "" || sessionID == "" {
		return false, nil
	}
	result, err := luaAcquireLease.Run(ctx, s.client,
		[]string{leaseKey(serviceName, slotID), leaseSessionKey(sessionID)},
		serviceName,
		slotID,
		sessionID,
		s.nodeID,
		ownerState,
		strconv.Itoa(s.leaseSchemaVers),
		strconv.FormatInt(now.UnixMilli(), 10),
		strconv.FormatInt(s.leaseTTL.Milliseconds(), 10),
	).Int()
	if err != nil {
		metrics.DistributedLeaseTotal.WithLabelValues("acquire_error").Inc()
		return false, err
	}
	if result == 1 {
		metrics.DistributedLeaseTotal.WithLabelValues("acquire_ok").Inc()
		return true, nil
	}
	metrics.DistributedLeaseTotal.WithLabelValues("acquire_conflict").Inc()
	return false, nil
}

func (s *LeaseStore) Renew(ctx context.Context, serviceName, slotID, sessionID, ownerState string, now time.Time) (bool, error) {
	if s == nil || serviceName == "" || slotID == "" || sessionID == "" {
		return false, nil
	}
	result, err := luaRenewLease.Run(ctx, s.client,
		[]string{leaseKey(serviceName, slotID), leaseSessionKey(sessionID)},
		sessionID,
		s.nodeID,
		ownerState,
		strconv.FormatInt(now.UnixMilli(), 10),
		strconv.FormatInt(s.leaseTTL.Milliseconds(), 10),
	).Int()
	if err != nil {
		metrics.DistributedLeaseTotal.WithLabelValues("renew_error").Inc()
		return false, err
	}
	if result == 1 {
		metrics.DistributedLeaseTotal.WithLabelValues("renew_ok").Inc()
		return true, nil
	}
	metrics.DistributedLeaseTotal.WithLabelValues("renew_miss").Inc()
	return false, nil
}

func (s *LeaseStore) Release(ctx context.Context, serviceName, slotID, sessionID string) (bool, error) {
	if s == nil || serviceName == "" || slotID == "" || sessionID == "" {
		return false, nil
	}
	result, err := luaReleaseLease.Run(ctx, s.client,
		[]string{leaseKey(serviceName, slotID), leaseSessionKey(sessionID)},
		sessionID,
		s.nodeID,
	).Int()
	if err != nil {
		metrics.DistributedLeaseTotal.WithLabelValues("release_error").Inc()
		return false, err
	}
	if result == 1 {
		metrics.DistributedLeaseTotal.WithLabelValues("release_ok").Inc()
		return true, nil
	}
	metrics.DistributedLeaseTotal.WithLabelValues("release_miss").Inc()
	return false, nil
}

func (s *LeaseStore) ForceRelease(ctx context.Context, serviceName, slotID, sessionID string) error {
	if s == nil || serviceName == "" || slotID == "" {
		return nil
	}
	pipe := s.client.Pipeline()
	pipe.Del(ctx, leaseKey(serviceName, slotID))
	if sessionID != "" {
		pipe.Del(ctx, leaseSessionKey(sessionID))
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (s *LeaseStore) List(ctx context.Context) ([]LeaseRecord, error) {
	if s == nil {
		return nil, nil
	}
	var (
		cursor uint64
		keys   []string
	)
	for {
		batch, next, err := s.client.Scan(ctx, cursor, "vbgw:lease:service:*", 64).Result()
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	records := make([]LeaseRecord, 0, len(keys))
	for _, key := range keys {
		values, err := s.client.HGetAll(ctx, key).Result()
		if err != nil || len(values) == 0 {
			continue
		}
		ttl, _ := s.client.PTTL(ctx, key).Result()
		record := decodeLeaseRecord(values, ttl)
		if record.ServiceName == "" || record.SlotID == "" {
			record.ServiceName, record.SlotID = parseLeaseKey(key)
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].ServiceName == records[j].ServiceName {
			return records[i].SlotID < records[j].SlotID
		}
		return records[i].ServiceName < records[j].ServiceName
	})
	return records, nil
}

func (s *LeaseStore) ReapStale(ctx context.Context, nodes map[string]NodeHeartbeat, now time.Time) (int, error) {
	if s == nil {
		return 0, nil
	}
	records, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, record := range records {
		stale := false
		if heartbeat, ok := nodes[record.OwnerNodeID]; !ok {
			stale = now.Sub(record.RenewedAt) >= s.staleGrace
		} else if !heartbeat.HeartbeatAt.IsZero() && now.Sub(heartbeat.HeartbeatAt) > time.Duration(heartbeat.HeartbeatTTLMS)*time.Millisecond {
			stale = true
		} else if heartbeat.State == NodeStateOffline {
			stale = true
		}
		if !stale {
			continue
		}
		if err := s.ForceRelease(ctx, record.ServiceName, record.SlotID, record.SessionID); err != nil {
			return reaped, err
		}
		reaped++
		metrics.DistributedLeaseStaleTotal.Inc()
	}
	return reaped, nil
}

func decodeLeaseRecord(values map[string]string, ttl time.Duration) LeaseRecord {
	record := LeaseRecord{
		ServiceName:  values["service_name"],
		SlotID:       values["slot_id"],
		SessionID:    values["session_id"],
		OwnerNodeID:  values["owner_node_id"],
		OwnerState:   values["owner_state"],
		TTLRemaining: ttl,
	}
	if v, err := strconv.Atoi(values["lease_schema_version"]); err == nil {
		record.LeaseSchemaVersion = v
	}
	if v, err := strconv.ParseInt(values["leased_at_unix_ms"], 10, 64); err == nil {
		record.LeasedAt = time.UnixMilli(v)
	}
	if v, err := strconv.ParseInt(values["renewed_at_unix_ms"], 10, 64); err == nil {
		record.RenewedAt = time.UnixMilli(v)
	}
	return record
}

func parseLeaseKey(key string) (serviceName, slotID string) {
	parts := strings.Split(key, ":")
	if len(parts) < 6 {
		return "", ""
	}
	return parts[3], parts[5]
}

func (s *LeaseStore) SessionLeaseKey(ctx context.Context, sessionID string) (string, error) {
	if s == nil || sessionID == "" {
		return "", nil
	}
	value, err := s.client.Get(ctx, leaseSessionKey(sessionID)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

func (s *LeaseStore) LookupSlot(ctx context.Context, serviceName, slotID string) (LeaseRecord, bool, error) {
	if s == nil || serviceName == "" || slotID == "" {
		return LeaseRecord{}, false, nil
	}
	values, err := s.client.HGetAll(ctx, leaseKey(serviceName, slotID)).Result()
	if err != nil {
		return LeaseRecord{}, false, err
	}
	if len(values) == 0 {
		return LeaseRecord{}, false, nil
	}
	ttl, _ := s.client.PTTL(ctx, leaseKey(serviceName, slotID)).Result()
	record := decodeLeaseRecord(values, ttl)
	if record.ServiceName == "" {
		record.ServiceName = serviceName
	}
	if record.SlotID == "" {
		record.SlotID = slotID
	}
	return record, true, nil
}

func (s *LeaseStore) MustDescribe(record LeaseRecord) string {
	return fmt.Sprintf("%s/%s owner=%s session=%s", record.ServiceName, record.SlotID, record.OwnerNodeID, record.SessionID)
}
