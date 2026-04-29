package slots

import (
	"context"
	"encoding/csv"
	"io"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"
)

type apiCaller interface {
	SendAPI(ctx context.Context, cmd string) (string, error)
}

type RegistrationState struct {
	Extension  string    `json:"extension"`
	Contact    string    `json:"contact,omitempty"`
	Registered bool      `json:"registered"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Raw        string    `json:"raw,omitempty"`
}

type Registry struct {
	mu          sync.RWMutex
	ttl         time.Duration
	byExt       map[string]RegistrationState
	refreshedAt time.Time
}

func NewRegistry(ttl time.Duration) *Registry {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Registry{
		ttl:   ttl,
		byExt: make(map[string]RegistrationState),
	}
}

func (r *Registry) RefreshFromESL(ctx context.Context, caller apiCaller) error {
	if r == nil || caller == nil {
		return nil
	}
	payload, err := caller.SendAPI(ctx, "show registrations")
	if err != nil {
		return err
	}
	r.Replace(ParseRegistrations(payload, time.Now()))
	return nil
}

func (r *Registry) Replace(states []RegistrationState) {
	if r == nil {
		return
	}
	observed := time.Now()
	next := make(map[string]RegistrationState, len(states))
	for _, state := range states {
		ext := strings.TrimSpace(state.Extension)
		if ext == "" {
			continue
		}
		state.Extension = ext
		state.Registered = true
		if state.LastSeenAt.IsZero() {
			state.LastSeenAt = observed
		}
		next[ext] = state
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for ext := range r.byExt {
		if _, ok := next[ext]; !ok {
			metrics.ExtensionRegistered.WithLabelValues(ext).Set(0)
		}
	}
	r.byExt = next
	r.refreshedAt = observed
	for ext := range next {
		metrics.ExtensionRegistered.WithLabelValues(ext).Set(1)
	}
}

func (r *Registry) Get(extension string) (RegistrationState, bool) {
	if r == nil {
		return RegistrationState{}, false
	}
	extension = strings.TrimSpace(extension)
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.byExt[extension]
	if !ok {
		return RegistrationState{}, false
	}
	if r.ttl > 0 && time.Since(state.LastSeenAt) > r.ttl {
		state.Registered = false
		return state, false
	}
	return state, true
}

func (r *Registry) Snapshot() []RegistrationState {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegistrationState, 0, len(r.byExt))
	for _, state := range r.byExt {
		if r.ttl > 0 && time.Since(state.LastSeenAt) > r.ttl {
			state.Registered = false
		}
		out = append(out, state)
	}
	return out
}

func (r *Registry) RefreshedAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.refreshedAt
}

func ParseRegistrations(payload string, observedAt time.Time) []RegistrationState {
	reader := csv.NewReader(strings.NewReader(payload))
	reader.FieldsPerRecord = -1

	states := make([]RegistrationState, 0)
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(record) == 0 {
			continue
		}
		extension := strings.TrimSpace(record[0])
		if extension == "" || !allDigits(extension) {
			continue
		}
		contact := ""
		if len(record) > 2 {
			contact = strings.TrimSpace(record[2])
		}
		states = append(states, RegistrationState{
			Extension:  extension,
			Contact:    contact,
			Registered: true,
			LastSeenAt: observedAt,
			Raw:        strings.Join(record, ","),
		})
	}
	return states
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
