package interconnect

import (
	"sort"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/routing"
)

type Store struct {
	mu        sync.RWMutex
	snapshots map[string]routing.GatewayState
}

func NewStore() *Store {
	return &Store{
		snapshots: make(map[string]routing.GatewayState),
	}
}

func (s *Store) Update(state routing.GatewayState) {
	if state.GatewayName == "" {
		return
	}

	s.mu.Lock()
	s.snapshots[state.GatewayName] = state
	s.mu.Unlock()

	updateGatewayHealthMetrics(state, time.Now())
}

func (s *Store) SetManualStandby(gatewayName, reason string, enabled bool) bool {
	if gatewayName == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.snapshots[gatewayName]
	if !ok {
		return false
	}
	if enabled {
		state.OperatorState = "standby"
		state.OperatorReason = reason
		state.OperatorAtUnix = time.Now().Unix()
	} else {
		state.OperatorState = ""
		state.OperatorReason = ""
		state.OperatorAtUnix = 0
	}
	s.snapshots[gatewayName] = state
	return true
}

func (s *Store) Get(gatewayName string) (routing.GatewayState, bool) {
	s.mu.RLock()
	state, ok := s.snapshots[gatewayName]
	s.mu.RUnlock()
	if ok {
		state = currentState(state, time.Now())
		updateGatewayHealthMetrics(state, time.Now())
	}
	return state, ok
}

func (s *Store) List() []routing.GatewayState {
	return s.currentStates(time.Now())
}

func (s *Store) RefreshMetrics() {
	now := time.Now()
	for _, state := range s.currentStates(now) {
		updateGatewayHealthMetrics(state, now)
	}
}

func (s *Store) currentStates(now time.Time) []routing.GatewayState {
	s.mu.RLock()
	snapshotCopy := make([]routing.GatewayState, 0, len(s.snapshots))
	for _, state := range s.snapshots {
		snapshotCopy = append(snapshotCopy, state)
	}
	s.mu.RUnlock()

	out := make([]routing.GatewayState, 0, len(snapshotCopy))
	for _, state := range snapshotCopy {
		state = currentState(state, now)
		updateGatewayHealthMetrics(state, now)
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GatewayName < out[j].GatewayName
	})
	return out
}

func currentState(state routing.GatewayState, now time.Time) routing.GatewayState {
	if state.HealthClass == HealthHealthy && state.ObservedAtUnix > 0 && state.FreshnessTTL > 0 {
		if now.Sub(time.Unix(state.ObservedAtUnix, 0)) > time.Duration(state.FreshnessTTL)*time.Second {
			state.HealthClass = HealthStale
		}
	}
	return state
}

func updateGatewayHealthMetrics(state routing.GatewayState, now time.Time) {
	for _, class := range []string{HealthHealthy, HealthUnhealthy, HealthStale, HealthUnknown} {
		value := 0.0
		if state.HealthClass == class {
			value = 1
		}
		metrics.GatewayHealth.WithLabelValues(state.GatewayName, state.Mode, class).Set(value)
	}

	age := 0.0
	if state.ObservedAtUnix > 0 {
		age = now.Sub(time.Unix(state.ObservedAtUnix, 0)).Seconds()
		if age < 0 {
			age = 0
		}
	}
	metrics.GatewayHealthAgeSeconds.WithLabelValues(state.GatewayName).Set(age)
}
