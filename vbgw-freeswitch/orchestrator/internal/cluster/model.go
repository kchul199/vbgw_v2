package cluster

import "time"

const (
	NodeStateActive   = "active"
	NodeStateDraining = "draining"
	NodeStatePaused   = "paused"
	NodeStateOffline  = "offline"
)

type NodeHeartbeat struct {
	NodeID               string    `json:"node_id"`
	State                string    `json:"state"`
	Reason               string    `json:"reason,omitempty"`
	StartedAt            time.Time `json:"started_at"`
	HeartbeatAt          time.Time `json:"heartbeat_at"`
	HeartbeatTTLMS       int64     `json:"heartbeat_ttl_ms"`
	LocalActiveSessions  int64     `json:"local_active_sessions"`
	OrchestratorVersion  string    `json:"orchestrator_version"`
	RoutingSchemaVersion int       `json:"routing_schema_version"`
	LeaseSchemaVersion   int       `json:"lease_schema_version"`
}

type CompatibilityIssue struct {
	NodeID   string `json:"node_id"`
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
}

type CompatibilityReport struct {
	Compatible bool                 `json:"compatible"`
	CheckedAt  time.Time            `json:"checked_at"`
	LocalNode  string               `json:"local_node"`
	Nodes      []NodeHeartbeat      `json:"nodes"`
	Issues     []CompatibilityIssue `json:"issues"`
}

type LeaseRecord struct {
	ServiceName        string        `json:"service_name"`
	SlotID             string        `json:"slot_id"`
	SessionID          string        `json:"session_id"`
	OwnerNodeID        string        `json:"owner_node_id"`
	OwnerState         string        `json:"owner_state"`
	LeaseSchemaVersion int           `json:"lease_schema_version"`
	LeasedAt           time.Time     `json:"leased_at"`
	RenewedAt          time.Time     `json:"renewed_at"`
	TTLRemaining       time.Duration `json:"ttl_remaining"`
	Stale              bool          `json:"stale"`
	StaleReason        string        `json:"stale_reason,omitempty"`
}

type Command struct {
	Action      string    `json:"action"`
	State       string    `json:"state,omitempty"`
	Reason      string    `json:"reason,omitempty"`
	RequestedBy string    `json:"requested_by,omitempty"`
	RequestedAt time.Time `json:"requested_at"`
}
