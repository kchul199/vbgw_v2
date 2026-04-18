/**
 * @file repository.go
 * @description Redis 기반의 세션 저장소 및 Pub/Sub 인터페이스
 */

package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrCapacityExceeded = errors.New("capacity exceeded")

// Store defines the interface for session management
type Store interface {
	TryAcquire(ctx context.Context) bool
	AddIfUnderCapacity(ctx context.Context, s *SessionState) bool
	Get(ctx context.Context, sessionID string) (*SessionState, bool)
	GetByFSUUID(ctx context.Context, fsUUID string) (*SessionState, bool)
	Release(ctx context.Context, sessionID string)
	Count(ctx context.Context) int64
	WaitAllDrained(ctx context.Context, killFn func(fsUUID string))
	ForEachLocal(fn func(s *SessionState))
}

// RedisStore implements the Store interface using Redis.
type RedisStore struct {
	client      *redis.Client
	nodeID      string
	maxCalls    int64
	localMap    map[string]*SessionState // track local context references
	localByUUID map[string]string
}

// NewRedisStore creates a new Redis-backed session store.
func NewRedisStore(addr, pass string, db int, maxCalls int64, nodeID string) (*RedisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: pass,
		DB:       db,
	})

	// Check connection
	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	return &RedisStore{
		client:      client,
		maxCalls:    maxCalls,
		nodeID:      nodeID,
		localMap:    make(map[string]*SessionState),
		localByUUID: make(map[string]string),
	}, nil
}

// TryAcquire atomically checks and increments the session counter via Redis INCR.
func (rs *RedisStore) TryAcquire(ctx context.Context) bool {
	// Simple atomic check with INCR
	val, err := rs.client.Incr(ctx, "vbgw:active_calls").Result()
	if err != nil {
		slog.Error("Redis INCR failed", "err", err)
		return false
	}
	if val > rs.maxCalls {
		rs.client.Decr(ctx, "vbgw:active_calls") // Revert
		return false
	}
	return true
}

func (rs *RedisStore) saveToRedis(ctx context.Context, s *SessionState) error {
	// Update Export fields before serialization
	s.mu.RLock()
	s.AiPausedExport = s.aiPaused
	s.RecordPathExport = s.recordPath
	s.BridgedWithExport = s.bridgedWith
	s.mu.RUnlock()

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	
	pipe := rs.client.Pipeline()
	pipe.Set(ctx, "vbgw:session:"+s.SessionID, data, 24*time.Hour)
	if s.FSUUID != "" {
		pipe.Set(ctx, "vbgw:fsuuid:"+s.FSUUID, s.SessionID, 24*time.Hour)
	}
	_, err = pipe.Exec(ctx)
	return err
}

func (rs *RedisStore) AddIfUnderCapacity(ctx context.Context, s *SessionState) bool {
	if !rs.TryAcquire(ctx) {
		return false
	}

	if err := rs.saveToRedis(ctx, s); err != nil {
		slog.Error("Redis save failed", "err", err)
		rs.client.Decr(ctx, "vbgw:active_calls")
		return false
	}

	// Save to local registry so channels/ctx can be accessed
	rs.localMap[s.SessionID] = s
	if s.FSUUID != "" {
		rs.localByUUID[s.FSUUID] = s.SessionID
	}
	return true
}

// Get finds session data from Local map if present, else from Redis.
func (rs *RedisStore) Get(ctx context.Context, sessionID string) (*SessionState, bool) {
	if s, ok := rs.localMap[sessionID]; ok {
		return s, true
	}

	data, err := rs.client.Get(ctx, "vbgw:session:"+sessionID).Bytes()
	if err == redis.Nil || err != nil {
		return nil, false
	}

	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, false
	}

	state.aiPaused = state.AiPausedExport
	state.recordPath = state.RecordPathExport
	state.bridgedWith = state.BridgedWithExport

	return &state, true
}

func (rs *RedisStore) GetByFSUUID(ctx context.Context, fsUUID string) (*SessionState, bool) {
	if sid, ok := rs.localByUUID[fsUUID]; ok {
		return rs.Get(ctx, sid)
	}

	sid, err := rs.client.Get(ctx, "vbgw:fsuuid:"+fsUUID).Result()
	if err == redis.Nil || err != nil {
		return nil, false
	}
	return rs.Get(ctx, sid)
}

func (rs *RedisStore) Release(ctx context.Context, sessionID string) {
	// Pop from local
	if s, loaded := rs.localMap[sessionID]; loaded {
		if s.IvrEventCh != nil {
			close(s.IvrEventCh)
			s.IvrEventCh = nil
		}
		s.Cancel()
		delete(rs.localByUUID, s.FSUUID)
		delete(rs.localMap, sessionID)
		
		pipe := rs.client.Pipeline()
		pipe.Del(ctx, "vbgw:session:"+sessionID)
		pipe.Del(ctx, "vbgw:fsuuid:"+s.FSUUID)
		pipe.Decr(ctx, "vbgw:active_calls")
		pipe.Exec(ctx)
	}
}

func (rs *RedisStore) Count(ctx context.Context) int64 {
	val, err := rs.client.Get(ctx, "vbgw:active_calls").Int64()
	if err != nil {
		return 0
	}
	return val
}

func (rs *RedisStore) WaitAllDrained(ctx context.Context, killFn func(fsUUID string)) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if len(rs.localMap) == 0 {
			slog.Info("All local sessions drained")
			return
		}
		select {
		case <-ctx.Done():
			slog.Warn("Drain timeout", "remaining_local", len(rs.localMap))
			for _, s := range rs.localMap {
				if killFn != nil && s.FSUUID != "" {
					killFn(s.FSUUID)
				}
				rs.Release(context.Background(), s.SessionID)
			}
			return
		case <-ticker.C:
			slog.Info("Draining local sessions", "remaining", len(rs.localMap))
		}
	}
}

func (rs *RedisStore) ForEachLocal(fn func(s *SessionState)) {
	for _, s := range rs.localMap {
		fn(s)
	}
}
