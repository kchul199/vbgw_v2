package routing

// Config is the top-level routing policy document for Phase 0/1 ingress routing.
type Config struct {
	Version  int            `yaml:"version"`
	Defaults Defaults       `yaml:"defaults"`
	Services []ServiceRoute `yaml:"services"`
}

// Defaults holds global fallback behavior for v1 routing.
type Defaults struct {
	OnUnknownEntry string `yaml:"on_unknown_entry"`
}

// ServiceRoute defines one logical service entry mapping.
type ServiceRoute struct {
	Name      string    `yaml:"name"`
	Enabled   bool      `yaml:"enabled"`
	RouteType string    `yaml:"route_type"`
	Priority  int       `yaml:"priority"`
	EntryNums []string  `yaml:"entry_numbers"`
	Match     MatchRule `yaml:"match"`
	Capacity  Capacity  `yaml:"capacity"`
}

// Capacity defines optional Phase 2 per-service logical slot policy.
type Capacity struct {
	Backend           string   `yaml:"backend"`
	MaxConcurrent     int      `yaml:"max_concurrent"`
	Allocator         string   `yaml:"allocator"`
	Extensions        []string `yaml:"extensions"`
	RequireRegistered bool     `yaml:"require_registered"`
	OverflowPolicy    string   `yaml:"overflow_policy"`
	TransferTarget    string   `yaml:"transfer_target"`
	OverflowService   string   `yaml:"overflow_service"`
	QueueMaxWaitSec   int      `yaml:"queue_max_wait_seconds"`
	QueueAnnouncement string   `yaml:"queue_announcement"`
	QueueOnTimeout    string   `yaml:"queue_on_timeout"`
}

// MatchRule constrains which ingress paths can resolve into a service.
type MatchRule struct {
	IngressStages  []string `yaml:"ingress_stages"`
	SourceGateways []string `yaml:"source_gateways"`
}

// ResolvedRoute is the result of ingress policy resolution.
type ResolvedRoute struct {
	ServiceName          string
	RouteType            string
	EntryNumber          string
	IngressStage         string
	SourceGateway        string
	RoutingConfigVersion int
	Matched              bool
}

// ResolveInput is the normalized ingress information used to resolve a route.
type ResolveInput struct {
	DestinationNumber string
	IngressStage      string
	SourceGateway     string
}

// GatewayState is a Phase 0 read-only health contract for future routing integration.
type GatewayState struct {
	GatewayName    string
	Mode           string
	HealthClass    string
	ObservedStatus string
	Source         string
	ObservedAtUnix int64
	FreshnessTTL   int64
	Producer       string
}

const (
	RouteTypeAI            = "ai"
	UnknownStaticFallback  = "static_fallback"
	BackendLogical         = "logical"
	BackendSIPExtension    = "sip_extension"
	AllocatorRoundRobin    = "round_robin"
	AllocatorPriority      = "priority"
	AllocatorStickyCaller  = "sticky_by_caller"
	AllocatorLeastRecently = "least_recently_used"
	OverflowBusy           = "busy"
	OverflowDirectTransfer = "direct_transfer"
	OverflowQueue          = "queue"
	OverflowFailoverSvc    = "failover_service"
	OverflowFallbackHuman  = "fallback_to_human"
)
