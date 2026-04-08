/**
 * @file manager.go
 * @description 세션 매니저 — sync.Map + atomic CAS (TOCTOU-safe)
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | atomic CAS 기반 세션 관리
 * v1.0.1 | 2026-04-07 | [Implementer] | 코드리뷰 | AddIfUnderCapacity 원자 연산 추가
 * ─────────────────────────────────────────
 */

package session

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Manager manages active call sessions with TOCTOU-safe capacity control.
type Manager struct {
	sessions sync.Map     // map[string]*SessionState (key: session_id)
	byFSUUID sync.Map     // map[string]string (FS UUID → session_id)
	count    atomic.Int64 // atomic counter for capacity check
	maxCalls int64
}

// NewManager creates a session manager with the given capacity.
func NewManager(maxCalls int64) *Manager {
	return &Manager{maxCalls: maxCalls}
}

// TryAcquire atomically checks and increments the session counter.
// Returns false if at capacity.
func (m *Manager) TryAcquire() bool {
	for {
		cur := m.count.Load()
		if cur >= m.maxCalls {
			return false
		}
		if m.count.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// Add stores a session. Call TryAcquire first.
func (m *Manager) Add(s *SessionState) {
	m.sessions.Store(s.SessionID, s)
	if s.FSUUID != "" {
		m.byFSUUID.Store(s.FSUUID, s.SessionID)
	}
}

// AddIfUnderCapacity atomically acquires capacity and stores the session.
// Returns false if at capacity (session is NOT stored).
// This is the preferred method over separate TryAcquire+Add to avoid race conditions.
func (m *Manager) AddIfUnderCapacity(s *SessionState) bool {
	for {
		cur := m.count.Load()
		if cur >= m.maxCalls {
			return false
		}
		if m.count.CompareAndSwap(cur, cur+1) {
			m.sessions.Store(s.SessionID, s)
			if s.FSUUID != "" {
				m.byFSUUID.Store(s.FSUUID, s.SessionID)
			}
			return true
		}
	}
}

// Get retrieves a session by session ID.
func (m *Manager) Get(sessionID string) (*SessionState, bool) {
	v, ok := m.sessions.Load(sessionID)
	if !ok {
		return nil, false
	}
	return v.(*SessionState), true
}

// GetByFSUUID retrieves a session by FreeSWITCH channel UUID.
func (m *Manager) GetByFSUUID(fsUUID string) (*SessionState, bool) {
	sid, ok := m.byFSUUID.Load(fsUUID)
	if !ok {
		return nil, false
	}
	return m.Get(sid.(string))
}

// Release removes a session and decrements the counter.
// Q-05: Also closes IvrEventCh to prevent goroutine leak in the bridge goroutine.
func (m *Manager) Release(sessionID string) {
	v, loaded := m.sessions.LoadAndDelete(sessionID)
	if !loaded {
		return
	}
	s := v.(*SessionState)
	// Q-05: Close IVR event channel to unblock the bridge goroutine
	if s.IvrEventCh != nil {
		close(s.IvrEventCh)
		s.IvrEventCh = nil
	}
	s.Cancel()
	m.byFSUUID.Delete(s.FSUUID)
	m.count.Add(-1)
}

// Count returns the current number of active sessions.
func (m *Manager) Count() int64 {
	return m.count.Load()
}

// WaitAllDrained blocks until all sessions are released or context expires.
// killFn is called for each remaining session on timeout to send BYE via ESL.
func (m *Manager) WaitAllDrained(ctx context.Context, killFn func(fsUUID string)) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		if m.count.Load() == 0 {
			slog.Info("All sessions drained")
			return
		}
		select {
		case <-ctx.Done():
			remaining := m.count.Load()
			slog.Warn("Drain timeout, forcefully closing remaining sessions", "remaining", remaining)
			m.sessions.Range(func(key, value any) bool {
				s := value.(*SessionState)
				// P-04: Send BYE via ESL before cancelling context
				if killFn != nil && s.FSUUID != "" {
					killFn(s.FSUUID)
				}
				s.Cancel()
				return true
			})
			return
		case <-ticker.C:
			slog.Info("Draining sessions", "remaining", m.count.Load())
		}
	}
}

// ForEach iterates over all sessions.
func (m *Manager) ForEach(fn func(s *SessionState)) {
	m.sessions.Range(func(key, value any) bool {
		fn(value.(*SessionState))
		return true
	})
}
