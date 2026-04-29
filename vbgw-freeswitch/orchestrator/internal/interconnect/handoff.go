package interconnect

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/esl"

	"github.com/google/uuid"
)

const gatewayHandoffTimeout = 35 * time.Second

type HandoffState string

const (
	HandoffAnswered HandoffState = "answered"
	HandoffFailed   HandoffState = "failed"
)

type HandoffResult struct {
	State       HandoffState
	HangupCause string
	SIPCode     string
	Gateway     string
}

type pendingHandoff struct {
	resultCh chan HandoffResult
}

type HandoffManager struct {
	mu      sync.Mutex
	pending map[string]*pendingHandoff
}

func NewHandoffManager() *HandoffManager {
	return &HandoffManager{
		pending: make(map[string]*pendingHandoff),
	}
}

func (m *HandoffManager) Register(fsUUID string) (<-chan HandoffResult, error) {
	if m == nil {
		return nil, fmt.Errorf("handoff manager is nil")
	}
	if strings.TrimSpace(fsUUID) == "" {
		return nil, fmt.Errorf("handoff fsuuid is empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pending[fsUUID]; exists {
		return nil, fmt.Errorf("handoff already registered for fsuuid %s", fsUUID)
	}

	ch := make(chan HandoffResult, 1)
	m.pending[fsUUID] = &pendingHandoff{resultCh: ch}
	return ch, nil
}

func (m *HandoffManager) IsPending(fsUUID string) bool {
	if m == nil || strings.TrimSpace(fsUUID) == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.pending[fsUUID]
	return ok
}

func (m *HandoffManager) ResolveAnswered(fsUUID string) bool {
	return m.resolve(fsUUID, HandoffResult{State: HandoffAnswered})
}

func (m *HandoffManager) ResolveFailed(fsUUID, gateway, hangupCause, sipCode string) bool {
	return m.resolve(fsUUID, HandoffResult{
		State:       HandoffFailed,
		Gateway:     gateway,
		HangupCause: hangupCause,
		SIPCode:     sipCode,
	})
}

func (m *HandoffManager) Cancel(fsUUID string) {
	if m == nil || strings.TrimSpace(fsUUID) == "" {
		return
	}

	m.mu.Lock()
	pending, ok := m.pending[fsUUID]
	if ok {
		delete(m.pending, fsUUID)
	}
	m.mu.Unlock()

	if ok {
		close(pending.resultCh)
	}
}

func (m *HandoffManager) resolve(fsUUID string, result HandoffResult) bool {
	if m == nil || strings.TrimSpace(fsUUID) == "" {
		return false
	}

	m.mu.Lock()
	pending, ok := m.pending[fsUUID]
	if ok {
		delete(m.pending, fsUUID)
	}
	m.mu.Unlock()

	if !ok {
		return false
	}

	pending.resultCh <- result
	close(pending.resultCh)
	return true
}

type GatewayHandoffOutcome struct {
	Confirmed       bool
	SelectedGateway string
	Reason          string
}

func ExecuteGatewayHandoff(ctx context.Context, commander esl.Commander, selector *Selector, handoffs *HandoffManager, sourceUUID, callerID, target string) (GatewayHandoffOutcome, error) {
	if selector == nil {
		return GatewayHandoffOutcome{}, fmt.Errorf("gateway selector not configured")
	}
	if handoffs == nil {
		return GatewayHandoffOutcome{}, fmt.Errorf("handoff manager not configured")
	}
	if strings.TrimSpace(sourceUUID) == "" {
		return GatewayHandoffOutcome{}, fmt.Errorf("source uuid is empty")
	}

	selection, err := selector.SelectTransfer()
	if err != nil {
		return GatewayHandoffOutcome{}, err
	}

	var lastErr error
	for _, gateway := range selection.GatewayOrder {
		attemptUUID := uuid.NewString()
		resultCh, err := handoffs.Register(attemptUUID)
		if err != nil {
			return GatewayHandoffOutcome{}, err
		}

		_, err = commander.Originate(ctx, attemptUUID, target, callerID, []string{gateway})
		if err != nil {
			handoffs.Cancel(attemptUUID)
			lastErr = fmt.Errorf("originate via gateway %s failed: %w", gateway, err)
			continue
		}

		result, waitErr := waitForHandoffResult(ctx, resultCh)
		if waitErr != nil {
			handoffs.Cancel(attemptUUID)
			_ = commander.Kill(context.Background(), attemptUUID)
			lastErr = fmt.Errorf("handoff wait failed via gateway %s: %w", gateway, waitErr)
			continue
		}

		if result.State == HandoffAnswered {
			if err := commander.Bridge(ctx, sourceUUID, attemptUUID); err != nil {
				_ = commander.Kill(context.Background(), attemptUUID)
				lastErr = fmt.Errorf("bridge after gateway answer failed via %s: %w", gateway, err)
				continue
			}
			return GatewayHandoffOutcome{
				Confirmed:       true,
				SelectedGateway: gateway,
				Reason:          selection.Reason,
			}, nil
		}

		lastErr = fmt.Errorf("gateway handoff failed via %s (cause=%s sip=%s)", gateway, result.HangupCause, result.SIPCode)
		if !ShouldRetryTransferFailure(result.HangupCause, result.SIPCode) {
			return GatewayHandoffOutcome{}, lastErr
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no gateway handoff attempt was executed")
	}
	return GatewayHandoffOutcome{}, lastErr
}

func waitForHandoffResult(ctx context.Context, resultCh <-chan HandoffResult) (HandoffResult, error) {
	select {
	case result, ok := <-resultCh:
		if !ok {
			return HandoffResult{}, fmt.Errorf("handoff result channel closed")
		}
		return result, nil
	case <-time.After(gatewayHandoffTimeout):
		return HandoffResult{}, fmt.Errorf("handoff timed out after %s", gatewayHandoffTimeout)
	case <-ctx.Done():
		return HandoffResult{}, ctx.Err()
	}
}

func ShouldRetryTransferFailure(hangupCause, sipCode string) bool {
	cause := strings.ToUpper(strings.TrimSpace(hangupCause))
	switch cause {
	case "NORMAL_TEMPORARY_FAILURE",
		"NORMAL_CIRCUIT_CONGESTION",
		"NETWORK_OUT_OF_ORDER",
		"DESTINATION_OUT_OF_ORDER",
		"RECOVERY_ON_TIMER_EXPIRE",
		"REQUESTED_CHAN_UNAVAIL",
		"SERVICE_UNAVAILABLE",
		"PROGRESS_TIMEOUT":
		return true
	case "USER_BUSY",
		"CALL_REJECTED",
		"NO_ANSWER",
		"SUBSCRIBER_ABSENT",
		"UNALLOCATED_NUMBER",
		"NO_ROUTE_DESTINATION",
		"INCOMPATIBLE_DESTINATION":
		return false
	}

	switch strings.TrimSpace(sipCode) {
	case "408", "480", "500", "502", "503", "504":
		return true
	case "404", "486", "603":
		return false
	}

	return false
}
