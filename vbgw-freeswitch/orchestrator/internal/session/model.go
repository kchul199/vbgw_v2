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

	// Node binding (which orchestrator instance owns this session locally)
	NodeID string `json:"node_id"`

	// IDs (immutable after creation — no lock needed for reads)
	SessionID string `json:"session_id"`
	FSUUID    string `json:"fs_uuid"`
	CallerID  string `json:"caller_id"`
	DestNum   string `json:"dest_num"`

	// Timing
	CreatedAt  time.Time `json:"created_at"`
	AnsweredAt time.Time `json:"answered_at"`
	HangupAt   time.Time `json:"hangup_at"`

	// Phase 0 routing metadata
	EntryNumber          string `json:"entry_number"`
	ServiceName          string `json:"service_name"`
	SourceGateway        string `json:"source_gateway"`
	IngressStage         string `json:"ingress_stage"`
	RouteType            string `json:"route_type"`
	RoutingConfigVersion int    `json:"routing_config_version"`

	// Phase 2 capacity metadata
	SlotID          string `json:"slot_id"`
	AllocationState string `json:"allocation_state"`
	OverflowPolicy  string `json:"overflow_policy"`
	OverflowTarget  string `json:"overflow_target"`

	// Phase 4 overflow lifecycle metadata
	State          string    `json:"state"`
	QueueName      string    `json:"queue_name"`
	QueueEnteredAt time.Time `json:"queue_entered_at"`
	QueueTimeoutAt time.Time `json:"queue_timeout_at"`
	OverflowReason string    `json:"overflow_reason"`

	// Mutable state (must use accessors)
	aiPaused       bool `json:"-"`
	AiPausedExport bool `json:"ai_paused"` // Exported for JSON Unmarshal only

	recordPath       string `json:"-"`
	RecordPathExport string `json:"record_path"`

	bridgedWith       string `json:"-"`
	BridgedWithExport string `json:"bridged_with"`

	// Local context/channels (Not serialized to Redis)
	IvrEventCh chan any               `json:"-"`
	Ctx        context.Context        `json:"-"`
	Cancel     context.CancelFunc     `json:"-"`
	OnRelease  func(sessionID string) `json:"-"`
}

// NewSession creates a new SessionState with the given IDs.
func NewSession(nodeID, sessionID, fsUUID, callerID, destNum string) *SessionState {
	ctx, cancel := context.WithCancel(context.Background())
	return &SessionState{
		NodeID:    nodeID,
		SessionID: sessionID,
		FSUUID:    fsUUID,
		CallerID:  callerID,
		DestNum:   destNum,
		CreatedAt: time.Now(),
		State:     StateCreated,
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

func (s *SessionState) SetRoutingMetadata(entryNumber, serviceName, sourceGateway, ingressStage, routeType string, configVersion int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entryNumber != "" {
		s.EntryNumber = entryNumber
	}
	if serviceName != "" {
		s.ServiceName = serviceName
	}
	if sourceGateway != "" {
		s.SourceGateway = sourceGateway
	}
	if ingressStage != "" {
		s.IngressStage = ingressStage
	}
	if routeType != "" {
		s.RouteType = routeType
	}
	if configVersion != 0 {
		s.RoutingConfigVersion = configVersion
	}
}

func (s *SessionState) SetCapacityMetadata(slotID, allocationState, overflowPolicy, overflowTarget string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SlotID = slotID
	s.AllocationState = allocationState
	s.OverflowPolicy = overflowPolicy
	s.OverflowTarget = overflowTarget
}

func (s *SessionState) SetLifecycleState(state, queueName, overflowReason string, queueEnteredAt, queueTimeoutAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state != "" {
		s.State = state
	}
	s.QueueName = queueName
	s.QueueEnteredAt = queueEnteredAt
	s.QueueTimeoutAt = queueTimeoutAt
	s.OverflowReason = overflowReason
}

func (s *SessionState) SetOnRelease(fn func(sessionID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OnRelease = fn
}

func (s *SessionState) releaseHook() func(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn := s.OnRelease
	s.OnRelease = nil
	return fn
}

const (
	AllocationPending    = "pending"
	AllocationLeased     = "leased"
	AllocationReleased   = "released"
	AllocationOverflowed = "overflowed"

	StateCreated      = "created"
	StateActive       = "active"
	StateQueued       = "queued"
	StateOverflowed   = "overflowed"
	StateTransferring = "transferring"
	StateEnded        = "ended"
)
