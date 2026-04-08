/**
 * @file model.go
 * @description 세션 상태 모델 — 콜 세션 데이터 구조
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | SessionState 정의
 * v1.0.1 | 2026-04-07 | [Implementer] | 코드리뷰 C-2 | 뮤텍스 추가 (race condition 수정)
 * ─────────────────────────────────────────
 */

package session

import (
	"context"
	"sync"
	"time"
)

// SessionState holds the runtime state for a single call session.
type SessionState struct {
	mu sync.RWMutex

	// IDs (immutable after creation — no lock needed for reads)
	SessionID string
	FSUUID    string
	CallerID  string
	DestNum   string

	// Timing
	CreatedAt  time.Time
	AnsweredAt time.Time
	HangupAt   time.Time

	// Mutable state (must use accessors)
	aiPaused    bool
	recordPath  string
	bridgedWith string

	// IVR event channel (set by onChannelPark, used by onDtmf)
	// Typed as chan any to avoid circular dependency with ivr package.
	// Actual type is chan ivr.IvrEvent.
	IvrEventCh chan any

	// Context for cancellation
	Ctx    context.Context
	Cancel context.CancelFunc
}

// NewSession creates a new SessionState with the given IDs.
func NewSession(sessionID, fsUUID, callerID, destNum string) *SessionState {
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionState{
		SessionID: sessionID,
		FSUUID:    fsUUID,
		CallerID:  callerID,
		DestNum:   destNum,
		CreatedAt: time.Now(),
		Ctx:       ctx,
		Cancel:    cancel,
	}
}

func (s *SessionState) IsAIPaused() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.aiPaused
}

func (s *SessionState) SetAIPaused(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aiPaused = v
}

func (s *SessionState) RecordPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordPath
}

func (s *SessionState) SetRecordPath(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordPath = v
}

func (s *SessionState) BridgedWith() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.bridgedWith
}

func (s *SessionState) SetBridgedWith(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bridgedWith = v
}

func (s *SessionState) SetAnsweredAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.AnsweredAt = t
}

func (s *SessionState) SetHangupAt(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HangupAt = t
}
