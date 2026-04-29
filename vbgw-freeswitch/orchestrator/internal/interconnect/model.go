package interconnect

import (
	"strings"

	"vbgw-orchestrator/internal/routing"
)

const (
	ModeRegister = "register"
	ModeTrunk    = "trunk"

	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
	HealthStale     = "stale"
	HealthUnknown   = "unknown"

	ReasonPrimaryHealthy            = "primary_healthy"
	ReasonPrimaryHealthyWithStandby = "primary_healthy_with_standby_fallback"
	ReasonStandbySelected           = "standby_selected"
	ReasonPrimaryStaleBestEffort    = "primary_stale_best_effort"
	ReasonPrimaryMissingBestEffort  = "primary_snapshot_missing_best_effort"
	ReasonFailFastPrimaryStale      = "fail_fast_primary_stale"
	ReasonNoHealthyGateway          = "no_healthy_gateway"

	DecisionKindOriginate = "originate"
	DecisionKindTransfer  = "transfer"
)

type SelectionPolicy struct {
	PreferPrimary     bool
	AllowStandby      bool
	FailFastWhenStale bool
}

type Selection struct {
	SelectedGateway string
	GatewayOrder    []string
	Reason          string
	Primary         routing.GatewayState
	Standby         routing.GatewayState
}

type DecisionRecord struct {
	Kind               string   `json:"kind"`
	SelectedGateway    string   `json:"selected_gateway"`
	GatewayOrder       []string `json:"gateway_order,omitempty"`
	Reason             string   `json:"reason"`
	Error              string   `json:"error,omitempty"`
	RecordedAtUnix     int64    `json:"recorded_at_unix"`
	PrimaryHealthClass string   `json:"primary_health_class,omitempty"`
	StandbyHealthClass string   `json:"standby_health_class,omitempty"`
}

func ShouldUseGatewayTransfer(target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	return strings.Contains(target, "@") ||
		strings.HasPrefix(target, "sip:") ||
		strings.HasPrefix(target, "tel:") ||
		strings.HasPrefix(target, "+")
}
