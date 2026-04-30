package interconnect

import (
	"fmt"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/routing"
)

type Selector struct {
	store          *Store
	primaryGateway string
	standbyGateway string
	policy         SelectionPolicy
	mu             sync.RWMutex
	history        []DecisionRecord
}

func NewSelector(store *Store, primaryGateway, standbyGateway string, policy SelectionPolicy) *Selector {
	if primaryGateway == "" {
		primaryGateway = "pbx-main"
	}
	if standbyGateway == "" {
		standbyGateway = "pbx-standby"
	}
	return &Selector{
		store:          store,
		primaryGateway: primaryGateway,
		standbyGateway: standbyGateway,
		policy:         policy,
	}
}

func (s *Selector) SelectOriginate() (Selection, error) {
	selection, err := s.selectInternal(true)
	s.recordSelection(DecisionKindOriginate, selection, err)
	return selection, err
}

func (s *Selector) SelectTransfer() (Selection, error) {
	selection, err := s.selectInternal(true)
	s.recordSelection(DecisionKindTransfer, selection, err)
	return selection, err
}

func (s *Selector) selectInternal(includeStandbyFallback bool) (Selection, error) {
	now := time.Now()
	var selection Selection

	primary, primaryOK := s.currentState(s.primaryGateway, now)
	standby, standbyOK := s.currentState(s.standbyGateway, now)
	selection.Primary = primary
	selection.Standby = standby

	if primaryOK && primary.OperatorState == "standby" {
		if s.policy.AllowStandby && standbyOK && standby.HealthClass == HealthHealthy && standby.OperatorState != "standby" {
			selection.SelectedGateway = s.standbyGateway
			selection.GatewayOrder = []string{s.standbyGateway}
			selection.Reason = ReasonPrimaryOperatorStandby
			return selection, nil
		}
		selection.Reason = ReasonPrimaryOperatorStandby
		return selection, fmt.Errorf("primary gateway %s is in operator standby", s.primaryGateway)
	}

	if primaryOK && primary.HealthClass == HealthHealthy {
		selection.SelectedGateway = s.primaryGateway
		selection.GatewayOrder = []string{s.primaryGateway}
		selection.Reason = ReasonPrimaryHealthy
		if includeStandbyFallback && s.policy.AllowStandby && standbyOK && standby.HealthClass == HealthHealthy && standby.OperatorState != "standby" {
			selection.GatewayOrder = append(selection.GatewayOrder, s.standbyGateway)
			selection.Reason = ReasonPrimaryHealthyWithStandby
		}
		return selection, nil
	}

	if s.policy.AllowStandby && standbyOK && standby.HealthClass == HealthHealthy && standby.OperatorState != "standby" {
		selection.SelectedGateway = s.standbyGateway
		selection.GatewayOrder = []string{s.standbyGateway}
		selection.Reason = ReasonStandbySelected
		return selection, nil
	}

	if primaryOK && primary.HealthClass == HealthStale && !s.policy.FailFastWhenStale {
		selection.SelectedGateway = s.primaryGateway
		selection.GatewayOrder = []string{s.primaryGateway}
		selection.Reason = ReasonPrimaryStaleBestEffort
		return selection, nil
	}

	if !primaryOK && !s.policy.FailFastWhenStale {
		selection.SelectedGateway = s.primaryGateway
		selection.GatewayOrder = []string{s.primaryGateway}
		selection.Reason = ReasonPrimaryMissingBestEffort
		return selection, nil
	}

	if primaryOK && primary.HealthClass == HealthStale {
		selection.Reason = ReasonFailFastPrimaryStale
		return selection, fmt.Errorf("primary gateway %s is stale", s.primaryGateway)
	}

	selection.Reason = ReasonNoHealthyGateway
	return selection, fmt.Errorf("no healthy gateway available")
}

func (s *Selector) currentState(gatewayName string, now time.Time) (routing.GatewayState, bool) {
	if s == nil || s.store == nil || gatewayName == "" {
		return routing.GatewayState{}, false
	}
	state, ok := s.store.Get(gatewayName)
	if !ok {
		return routing.GatewayState{}, false
	}
	state = currentState(state, now)
	return state, true
}

func (s *Selector) DecisionHistory(limit int) []DecisionRecord {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit <= 0 || limit > len(s.history) {
		limit = len(s.history)
	}
	if limit == 0 {
		return nil
	}

	out := make([]DecisionRecord, limit)
	copy(out, s.history[len(s.history)-limit:])
	return out
}

func (s *Selector) recordSelection(kind string, selection Selection, err error) {
	selected := selection.SelectedGateway
	if selected == "" {
		selected = "none"
	}
	reason := selection.Reason
	if reason == "" {
		if err != nil {
			reason = "selection_error"
		} else {
			reason = "selected"
		}
	}
	metrics.FailoverDecisionsTotal.WithLabelValues(reason, selected).Inc()

	record := DecisionRecord{
		Kind:               kind,
		SelectedGateway:    selected,
		GatewayOrder:       append([]string(nil), selection.GatewayOrder...),
		Reason:             reason,
		RecordedAtUnix:     time.Now().Unix(),
		PrimaryHealthClass: selection.Primary.HealthClass,
		StandbyHealthClass: selection.Standby.HealthClass,
	}
	if err != nil {
		record.Error = err.Error()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, record)
	if len(s.history) > 20 {
		s.history = s.history[len(s.history)-20:]
	}
}
