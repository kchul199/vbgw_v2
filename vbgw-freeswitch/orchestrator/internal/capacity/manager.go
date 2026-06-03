package capacity

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"time"

	"vbgw-orchestrator/internal/cluster"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/slots"
)

type Decision struct {
	Allowed           bool
	Configured        bool
	ServiceName       string
	SlotID            string
	OverflowPolicy    string
	TransferTarget    string
	OverflowService   string
	QueueMaxWaitSec   int
	QueueAnnouncement string
	QueueOnTimeout    string
	Reason            string
	Backend           string
	Extension         string
}

type AdmitRequest struct {
	SessionID   string
	ServiceName string
	CallerID    string
}

type SlotSnapshot struct {
	SlotID         string    `json:"slot_id"`
	SessionID      string    `json:"session_id,omitempty"`
	InUse          bool      `json:"in_use"`
	Backend        string    `json:"backend"`
	Extension      string    `json:"extension,omitempty"`
	Registered     bool      `json:"registered"`
	OwnerNodeID    string    `json:"owner_node_id,omitempty"`
	LocalOwner     bool      `json:"local_owner,omitempty"`
	LeaseRenewedAt time.Time `json:"lease_renewed_at,omitempty"`
	LeaseTTLMS     int64     `json:"lease_ttl_ms,omitempty"`
	Stale          bool      `json:"stale,omitempty"`
	StaleReason    string    `json:"stale_reason,omitempty"`
	LastSeenAt     time.Time `json:"last_seen_at,omitempty"`
	LastReleasedAt time.Time `json:"last_released_at,omitempty"`
}

type ServiceSnapshot struct {
	ServiceName         string         `json:"service_name"`
	Backend             string         `json:"backend"`
	MaxConcurrent       int            `json:"max_concurrent"`
	ActiveCalls         int            `json:"active_calls"`
	AvailableSlots      int            `json:"available_slots"`
	RegisteredSlots     int            `json:"registered_slots"`
	RequireRegistered   bool           `json:"require_registered"`
	Allocator           string         `json:"allocator"`
	ControlState        string         `json:"control_state"`
	ControlReason       string         `json:"control_reason,omitempty"`
	ControlUpdatedAt    time.Time      `json:"control_updated_at,omitempty"`
	OverflowPolicy      string         `json:"overflow_policy"`
	TransferTarget      string         `json:"transfer_target,omitempty"`
	OverflowService     string         `json:"overflow_service,omitempty"`
	QueueMaxWaitSec     int            `json:"queue_max_wait_seconds,omitempty"`
	QueueAnnouncement   string         `json:"queue_announcement,omitempty"`
	QueueOnTimeout      string         `json:"queue_on_timeout,omitempty"`
	ExtensionsInventory []string       `json:"extensions_inventory,omitempty"`
	Slots               []SlotSnapshot `json:"slots"`
}

type slotState struct {
	id             string
	backend        string
	extension      string
	sessionID      string
	lastReleasedAt time.Time
}

type serviceState struct {
	name              string
	backend           string
	maxConcurrent     int
	requireRegistered bool
	allocator         string
	controlState      string
	controlReason     string
	controlUpdatedAt  time.Time
	overflowPolicy    string
	transferTarget    string
	overflowService   string
	queueMaxWaitSec   int
	queueAnnouncement string
	queueOnTimeout    string
	nextIndex         int
	slots             []slotState
	sessionSlot       map[string]int
}

// Manager tracks service slot occupancy per backend.
type Manager struct {
	mu       sync.Mutex
	services map[string]*serviceState
	registry *slots.Registry
	leases   *cluster.LeaseStore
	node     nodeStateProvider

	leaseRenewDegraded bool
	leaseRenewReason   string
}

type nodeStateProvider interface {
	CurrentState() string
	BlocksNewAdmits() bool
	ListLeases(ctx context.Context) ([]cluster.LeaseRecord, error)
}

const (
	ControlStateActive   = "active"
	ControlStatePaused   = "paused"
	ControlStateDraining = "draining"
)

// NewManager creates a capacity manager from routing config.
func NewManager(cfg *routing.Config, registries ...*slots.Registry) *Manager {
	mgr := &Manager{
		services: make(map[string]*serviceState),
	}
	if len(registries) > 0 {
		mgr.registry = registries[0]
	}
	if cfg == nil {
		return mgr
	}

	for _, svc := range cfg.Services {
		state := newServiceState(svc)
		if state == nil {
			continue
		}
		mgr.services[svc.Name] = state

		metrics.ServiceCapacityMax.WithLabelValues(svc.Name).Set(float64(state.maxConcurrent))
		metrics.ServiceActiveCalls.WithLabelValues(svc.Name).Set(0)
		metrics.ServiceQueueDepth.WithLabelValues(svc.Name).Set(0)
		metrics.QueueWaitSeconds.WithLabelValues(svc.Name).Set(0)
		metrics.SlotBackendAvailable.WithLabelValues(svc.Name, state.backend).Set(float64(state.maxConcurrent))
		for _, slot := range state.slots {
			metrics.SlotInUse.WithLabelValues(svc.Name, slot.id).Set(0)
		}
	}

	return mgr
}

func (m *Manager) SetDistributedLeases(leases *cluster.LeaseStore) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leases = leases
}

func (m *Manager) SetNodeStateProvider(provider nodeStateProvider) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.node = provider
}

func newServiceState(svc routing.ServiceRoute) *serviceState {
	if !svc.Enabled {
		return nil
	}

	backend := svc.Capacity.Backend
	if backend == "" {
		backend = routing.BackendLogical
	}

	maxConcurrent := svc.Capacity.MaxConcurrent
	if backend == routing.BackendSIPExtension && maxConcurrent <= 0 {
		maxConcurrent = len(svc.Capacity.Extensions)
	}
	if maxConcurrent <= 0 {
		return nil
	}

	state := &serviceState{
		name:              svc.Name,
		backend:           backend,
		maxConcurrent:     maxConcurrent,
		requireRegistered: svc.Capacity.RequireRegistered,
		allocator:         svc.Capacity.Allocator,
		controlState:      ControlStateActive,
		overflowPolicy:    svc.Capacity.OverflowPolicy,
		transferTarget:    svc.Capacity.TransferTarget,
		overflowService:   svc.Capacity.OverflowService,
		queueMaxWaitSec:   svc.Capacity.QueueMaxWaitSec,
		queueAnnouncement: svc.Capacity.QueueAnnouncement,
		queueOnTimeout:    svc.Capacity.QueueOnTimeout,
		sessionSlot:       make(map[string]int, maxConcurrent),
		slots:             make([]slotState, 0, maxConcurrent),
	}

	switch backend {
	case routing.BackendSIPExtension:
		for idx, ext := range svc.Capacity.Extensions {
			if idx >= maxConcurrent {
				break
			}
			state.slots = append(state.slots, slotState{
				id:        ext,
				backend:   backend,
				extension: ext,
			})
		}
	default:
		for idx := 0; idx < maxConcurrent; idx++ {
			state.slots = append(state.slots, slotState{
				id:      logicalSlotID(svc.Name, idx),
				backend: routing.BackendLogical,
			})
		}
	}

	return state
}

// Preview returns the best-effort admission decision without allocating a slot.
func (m *Manager) Preview(serviceName string) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.previewLocked(serviceName)
}

// Admit retains backward compatibility for tests and older callers.
func (m *Manager) Admit(sessionID, serviceName string) Decision {
	return m.AdmitRequest(AdmitRequest{
		SessionID:   sessionID,
		ServiceName: serviceName,
	})
}

// AdmitRequest allocates a slot using backend-aware inventory and ACD policy.
func (m *Manager) AdmitRequest(req AdmitRequest) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()

	preview := m.previewLocked(req.ServiceName)
	if !preview.Configured {
		return preview
	}
	state := m.services[req.ServiceName]
	if slotIndex, exists := state.sessionSlot[req.SessionID]; exists {
		slot := state.slots[slotIndex]
		preview.Allowed = true
		preview.SlotID = slot.id
		preview.Extension = slot.extension
		return preview
	}
	if !preview.Allowed {
		metrics.OverflowTotal.WithLabelValues(req.ServiceName, preview.OverflowPolicy).Inc()
		return preview
	}

	candidates := m.candidateSlotOrderLocked(state, req.CallerID, time.Now())
	slotIndex := -1
	for _, candidate := range candidates {
		if candidate < 0 || candidate >= len(state.slots) {
			continue
		}
		if m.leases != nil {
			ok, err := m.leases.Acquire(context.Background(), state.name, state.slots[candidate].id, req.SessionID, m.currentNodeStateLocked(), time.Now())
			if err != nil {
				continue
			}
			if !ok {
				continue
			}
			slotIndex = candidate
			break
		}
		slotIndex = candidate
		break
	}
	if slotIndex < 0 {
		preview.Allowed = false
		preview.Reason = "service capacity exceeded"
		metrics.OverflowTotal.WithLabelValues(req.ServiceName, preview.OverflowPolicy).Inc()
		m.updateServiceMetricsLocked(state)
		return preview
	}

	state.slots[slotIndex].sessionID = req.SessionID
	state.sessionSlot[req.SessionID] = slotIndex
	if state.allocator == routing.AllocatorRoundRobin {
		state.nextIndex = (slotIndex + 1) % len(state.slots)
	}

	slot := state.slots[slotIndex]
	preview.Allowed = true
	preview.SlotID = slot.id
	preview.Extension = slot.extension
	m.updateServiceMetricsLocked(state)
	return preview
}

// Release frees a previously allocated slot.
func (m *Manager) Release(sessionID string) {
	if sessionID == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.services {
		slotIndex, exists := state.sessionSlot[sessionID]
		if !exists {
			continue
		}
		if m.leases != nil {
			_, _ = m.leases.Release(context.Background(), state.name, state.slots[slotIndex].id, sessionID)
		}
		state.slots[slotIndex].sessionID = ""
		state.slots[slotIndex].lastReleasedAt = time.Now()
		delete(state.sessionSlot, sessionID)
		m.updateServiceMetricsLocked(state)
		return
	}
}

// Snapshots returns a sorted view of configured services and slots.
func (m *Manager) Snapshots() []ServiceSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()

	snapshots := make([]ServiceSnapshot, 0, len(m.services))
	now := time.Now()
	for _, state := range m.services {
		available, registered := m.availabilityLocked(state, now)
		remoteLeases := m.distributedLeaseMapLocked(state.name)
		snapshot := ServiceSnapshot{
			ServiceName:       state.name,
			Backend:           state.backend,
			MaxConcurrent:     state.maxConcurrent,
			ActiveCalls:       len(state.sessionSlot) + m.remoteLeaseCountLocked(state.name),
			AvailableSlots:    available,
			RegisteredSlots:   registered,
			RequireRegistered: state.requireRegistered,
			Allocator:         state.allocator,
			ControlState:      state.controlState,
			ControlReason:     state.controlReason,
			ControlUpdatedAt:  state.controlUpdatedAt,
			OverflowPolicy:    state.overflowPolicy,
			TransferTarget:    state.transferTarget,
			OverflowService:   state.overflowService,
			QueueMaxWaitSec:   state.queueMaxWaitSec,
			QueueAnnouncement: state.queueAnnouncement,
			QueueOnTimeout:    state.queueOnTimeout,
			Slots:             make([]SlotSnapshot, 0, len(state.slots)),
		}
		for _, slot := range state.slots {
			slotSnapshot := SlotSnapshot{
				SlotID:         slot.id,
				SessionID:      slot.sessionID,
				InUse:          slot.sessionID != "",
				Backend:        slot.backend,
				Extension:      slot.extension,
				LastReleasedAt: slot.lastReleasedAt,
			}
			if lease, ok := remoteLeases[slot.id]; ok {
				slotSnapshot.SessionID = lease.SessionID
				slotSnapshot.InUse = true
				slotSnapshot.OwnerNodeID = lease.OwnerNodeID
				slotSnapshot.LocalOwner = lease.OwnerNodeID == "" || lease.OwnerNodeID == m.localNodeIDLocked()
				slotSnapshot.LeaseRenewedAt = lease.RenewedAt
				slotSnapshot.LeaseTTLMS = lease.TTLRemaining.Milliseconds()
				slotSnapshot.Stale = lease.Stale
				slotSnapshot.StaleReason = lease.StaleReason
			}
			if slot.extension != "" {
				snapshot.ExtensionsInventory = append(snapshot.ExtensionsInventory, slot.extension)
				if reg, ok := m.registrationLocked(slot.extension, now); ok {
					slotSnapshot.Registered = true
					slotSnapshot.LastSeenAt = reg.LastSeenAt
				}
			}
			snapshot.Slots = append(snapshot.Slots, slotSnapshot)
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].ServiceName < snapshots[j].ServiceName
	})
	return snapshots
}

// RefreshMetrics recomputes service/slot gauges from the current backend and registration inventory.
func (m *Manager) RefreshMetrics() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, state := range m.services {
		m.updateServiceMetricsLocked(state)
	}
}

func (m *Manager) previewLocked(serviceName string) Decision {
	state, ok := m.services[serviceName]
	if serviceName == "" || !ok {
		return Decision{
			Allowed:     true,
			Configured:  false,
			ServiceName: serviceName,
		}
	}

	switch state.controlState {
	case ControlStatePaused:
		decision := m.allowedDecision(state)
		decision.Allowed = false
		decision.Reason = "service paused"
		return decision
	case ControlStateDraining:
		decision := m.allowedDecision(state)
		decision.Allowed = false
		decision.Reason = "service draining"
		return decision
	}
	if m.node != nil && m.node.BlocksNewAdmits() {
		decision := m.allowedDecision(state)
		decision.Allowed = false
		decision.Reason = "node draining"
		return decision
	}
	if m.leaseRenewDegraded {
		decision := m.allowedDecision(state)
		decision.Allowed = false
		decision.Reason = m.leaseRenewReason
		if decision.Reason == "" {
			decision.Reason = "distributed lease renew degraded"
		}
		return decision
	}

	available := 0
	now := time.Now()
	for idx := range state.slots {
		if ok, _ := m.slotEligibleLocked(state, &state.slots[idx], now); ok {
			available++
		}
	}
	if available > 0 {
		return m.allowedDecision(state)
	}
	decision := m.allowedDecision(state)
	decision.Allowed = false
	decision.Reason = "service capacity exceeded"
	return decision
}

func (m *Manager) allowedDecision(state *serviceState) Decision {
	return Decision{
		Allowed:           true,
		Configured:        true,
		ServiceName:       state.name,
		OverflowPolicy:    state.overflowPolicy,
		TransferTarget:    state.transferTarget,
		OverflowService:   state.overflowService,
		QueueMaxWaitSec:   state.queueMaxWaitSec,
		QueueAnnouncement: state.queueAnnouncement,
		QueueOnTimeout:    state.queueOnTimeout,
		Backend:           state.backend,
	}
}

func (m *Manager) availabilityLocked(state *serviceState, now time.Time) (available int, registered int) {
	remoteLeases := m.distributedLeaseMapLocked(state.name)
	for idx := range state.slots {
		slot := &state.slots[idx]
		if _, ok := remoteLeases[slot.id]; ok && slot.sessionID == "" {
			if slot.extension != "" {
				if _, reg := m.registrationLocked(slot.extension, now); reg {
					registered++
				}
			}
			continue
		}
		eligible, reg := m.slotEligibleLocked(state, slot, now)
		if reg {
			registered++
		}
		if eligible {
			available++
		}
	}
	return available, registered
}

func (m *Manager) slotEligibleLocked(state *serviceState, slot *slotState, now time.Time) (eligible bool, registered bool) {
	if slot.sessionID != "" {
		return false, false
	}
	if state.backend != routing.BackendSIPExtension {
		return true, false
	}
	if !state.requireRegistered {
		return true, false
	}
	_, ok := m.registrationLocked(slot.extension, now)
	return ok, ok
}

func (m *Manager) registrationLocked(extension string, now time.Time) (slots.RegistrationState, bool) {
	if m.registry == nil {
		return slots.RegistrationState{}, false
	}
	return m.registry.Get(extension)
}

func (m *Manager) findCandidateSlotLocked(state *serviceState, callerID string, now time.Time) int {
	order := m.candidateSlotOrderLocked(state, callerID, now)
	if len(order) == 0 {
		return -1
	}
	return order[0]
}

func (m *Manager) candidateSlotOrderLocked(state *serviceState, callerID string, now time.Time) []int {
	if state == nil || len(state.slots) == 0 {
		return nil
	}
	switch state.allocator {
	case routing.AllocatorPriority:
		var order []int
		for idx := range state.slots {
			if ok, _ := m.slotEligibleLocked(state, &state.slots[idx], now); ok {
				order = append(order, idx)
			}
		}
		return order
	case routing.AllocatorStickyCaller:
		if callerID != "" {
			var order []int
			start := int(hashString(callerID) % uint32(len(state.slots)))
			for offset := 0; offset < len(state.slots); offset++ {
				idx := (start + offset) % len(state.slots)
				if ok, _ := m.slotEligibleLocked(state, &state.slots[idx], now); ok {
					order = append(order, idx)
				}
			}
			return order
		}
		fallthrough
	case routing.AllocatorRoundRobin:
		var order []int
		for offset := 0; offset < len(state.slots); offset++ {
			idx := (state.nextIndex + offset) % len(state.slots)
			if ok, _ := m.slotEligibleLocked(state, &state.slots[idx], now); ok {
				order = append(order, idx)
			}
		}
		return order
	case routing.AllocatorLeastRecently:
		type candidate struct {
			idx int
			at  time.Time
		}
		candidates := make([]candidate, 0, len(state.slots))
		for idx := range state.slots {
			if ok, _ := m.slotEligibleLocked(state, &state.slots[idx], now); !ok {
				continue
			}
			candidates = append(candidates, candidate{idx: idx, at: state.slots[idx].lastReleasedAt})
		}
		sort.Slice(candidates, func(i, j int) bool {
			return earlierRelease(candidates[i].at, candidates[j].at)
		})
		order := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			order = append(order, candidate.idx)
		}
		return order
	}
	return nil
}

func (m *Manager) updateServiceMetricsLocked(state *serviceState) {
	metrics.ServiceActiveCalls.WithLabelValues(state.name).Set(float64(len(state.sessionSlot)))
	available, _ := m.availabilityLocked(state, time.Now())
	metrics.SlotBackendAvailable.WithLabelValues(state.name, state.backend).Set(float64(available))
	for _, candidate := range []string{ControlStateActive, ControlStatePaused, ControlStateDraining} {
		value := 0.0
		if state.controlState == candidate {
			value = 1
		}
		metrics.ServiceControlState.WithLabelValues(state.name, candidate).Set(value)
	}
	for _, slot := range state.slots {
		value := 0.0
		if slot.sessionID != "" {
			value = 1
		}
		metrics.SlotInUse.WithLabelValues(state.name, slot.id).Set(value)
	}
}

func logicalSlotID(serviceName string, index int) string {
	return fmt.Sprintf("%s-%02d", serviceName, index+1)
}

func hashString(value string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(value))
	return hasher.Sum32()
}

func earlierRelease(candidate, current time.Time) bool {
	if current.IsZero() {
		return true
	}
	if candidate.IsZero() {
		return true
	}
	return candidate.Before(current)
}

func (m *Manager) SetServiceControlState(serviceName, controlState, reason string) bool {
	if m == nil || serviceName == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.services[serviceName]
	if !ok {
		return false
	}
	if controlState == "" {
		controlState = ControlStateActive
	}
	state.controlState = controlState
	state.controlReason = reason
	state.controlUpdatedAt = time.Now()
	m.updateServiceMetricsLocked(state)
	return true
}

func (m *Manager) PauseService(serviceName, reason string) bool {
	return m.SetServiceControlState(serviceName, ControlStatePaused, reason)
}

func (m *Manager) ResumeService(serviceName string) bool {
	return m.SetServiceControlState(serviceName, ControlStateActive, "")
}

func (m *Manager) DrainService(serviceName, reason string) bool {
	return m.SetServiceControlState(serviceName, ControlStateDraining, reason)
}

func (m *Manager) ServiceSnapshot(serviceName string) (ServiceSnapshot, bool) {
	if m == nil || serviceName == "" {
		return ServiceSnapshot{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	state, ok := m.services[serviceName]
	if !ok {
		return ServiceSnapshot{}, false
	}
	available, registered := m.availabilityLocked(state, time.Now())
	remoteLeases := m.distributedLeaseMapLocked(state.name)
	snapshot := ServiceSnapshot{
		ServiceName:       state.name,
		Backend:           state.backend,
		MaxConcurrent:     state.maxConcurrent,
		ActiveCalls:       len(state.sessionSlot) + m.remoteLeaseCountLocked(state.name),
		AvailableSlots:    available,
		RegisteredSlots:   registered,
		RequireRegistered: state.requireRegistered,
		Allocator:         state.allocator,
		ControlState:      state.controlState,
		ControlReason:     state.controlReason,
		ControlUpdatedAt:  state.controlUpdatedAt,
		OverflowPolicy:    state.overflowPolicy,
		TransferTarget:    state.transferTarget,
		OverflowService:   state.overflowService,
		QueueMaxWaitSec:   state.queueMaxWaitSec,
		QueueAnnouncement: state.queueAnnouncement,
		QueueOnTimeout:    state.queueOnTimeout,
		Slots:             make([]SlotSnapshot, 0, len(state.slots)),
	}
	for _, slot := range state.slots {
		record := SlotSnapshot{
			SlotID:         slot.id,
			SessionID:      slot.sessionID,
			InUse:          slot.sessionID != "",
			Backend:        slot.backend,
			Extension:      slot.extension,
			LastReleasedAt: slot.lastReleasedAt,
		}
		if lease, ok := remoteLeases[slot.id]; ok {
			record.SessionID = lease.SessionID
			record.InUse = true
			record.OwnerNodeID = lease.OwnerNodeID
			record.LocalOwner = lease.OwnerNodeID == "" || lease.OwnerNodeID == m.localNodeIDLocked()
			record.LeaseRenewedAt = lease.RenewedAt
			record.LeaseTTLMS = lease.TTLRemaining.Milliseconds()
			record.Stale = lease.Stale
			record.StaleReason = lease.StaleReason
		}
		if slot.extension != "" {
			snapshot.ExtensionsInventory = append(snapshot.ExtensionsInventory, slot.extension)
			if reg, ok := m.registrationLocked(slot.extension, time.Now()); ok {
				record.Registered = true
				record.LastSeenAt = reg.LastSeenAt
			}
		}
		snapshot.Slots = append(snapshot.Slots, record)
	}
	return snapshot, true
}

func (m *Manager) ForceReleaseSlot(slotID string) (serviceName, sessionID string, ok bool) {
	if m == nil || slotID == "" {
		return "", "", false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for _, state := range m.services {
		for idx := range state.slots {
			if state.slots[idx].id != slotID {
				continue
			}
			sessionID = state.slots[idx].sessionID
			serviceName = state.name
			if m.leases != nil {
				if sessionID == "" {
					if lease, exists, err := m.leases.LookupSlot(context.Background(), state.name, slotID); err == nil && exists {
						sessionID = lease.SessionID
					}
				}
				_ = m.leases.ForceRelease(context.Background(), state.name, slotID, sessionID)
			}
			if sessionID != "" {
				delete(state.sessionSlot, sessionID)
			}
			state.slots[idx].sessionID = ""
			state.slots[idx].lastReleasedAt = time.Now()
			m.updateServiceMetricsLocked(state)
			return serviceName, sessionID, true
		}
	}
	return "", "", false
}

func (m *Manager) RenewLeases() {
	if m == nil || m.leases == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	activeSlots := 0
	failures := 0
	for _, state := range m.services {
		for _, slot := range state.slots {
			if slot.sessionID == "" {
				continue
			}
			activeSlots++
			ok, err := m.leases.Renew(context.Background(), state.name, slot.id, slot.sessionID, m.currentNodeStateLocked(), now)
			if err != nil || !ok {
				failures++
			}
		}
	}
	if activeSlots == 0 || failures == 0 {
		m.leaseRenewDegraded = false
		m.leaseRenewReason = ""
		return
	}
	m.leaseRenewDegraded = true
	m.leaseRenewReason = "distributed lease renew degraded"
}

func (m *Manager) distributedLeaseMapLocked(serviceName string) map[string]cluster.LeaseRecord {
	if m.leases == nil {
		return nil
	}
	var (
		records []cluster.LeaseRecord
		err     error
	)
	if m.node != nil {
		records, err = m.node.ListLeases(context.Background())
	} else {
		records, err = m.leases.List(context.Background())
	}
	if err != nil || len(records) == 0 {
		return nil
	}
	out := make(map[string]cluster.LeaseRecord)
	for _, record := range records {
		if record.ServiceName != serviceName {
			continue
		}
		out[record.SlotID] = record
	}
	return out
}

func (m *Manager) remoteLeaseCountLocked(serviceName string) int {
	leases := m.distributedLeaseMapLocked(serviceName)
	if len(leases) == 0 {
		return 0
	}
	count := 0
	for _, record := range leases {
		if record.OwnerNodeID != m.localNodeIDLocked() {
			count++
		}
	}
	return count
}

func (m *Manager) currentNodeStateLocked() string {
	if m.node == nil {
		return ControlStateActive
	}
	return m.node.CurrentState()
}

func (m *Manager) localNodeIDLocked() string {
	if m.leases == nil {
		return ""
	}
	return m.leases.NodeID()
}
