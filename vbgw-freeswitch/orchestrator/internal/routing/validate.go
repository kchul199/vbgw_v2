package routing

import (
	"fmt"
	"slices"
	"strings"
)

// Normalize mutates the config into its canonical v1 form.
func Normalize(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}

	cfg.Defaults.OnUnknownEntry = strings.TrimSpace(cfg.Defaults.OnUnknownEntry)
	if cfg.Defaults.OnUnknownEntry == "" {
		cfg.Defaults.OnUnknownEntry = UnknownStaticFallback
	}

	for i := range cfg.Services {
		svc := &cfg.Services[i]
		svc.Name = strings.TrimSpace(svc.Name)
		svc.RouteType = strings.TrimSpace(strings.ToLower(svc.RouteType))
		svc.Capacity.Backend = strings.TrimSpace(strings.ToLower(svc.Capacity.Backend))
		svc.Capacity.Allocator = strings.TrimSpace(strings.ToLower(svc.Capacity.Allocator))
		svc.Capacity.OverflowPolicy = strings.TrimSpace(strings.ToLower(svc.Capacity.OverflowPolicy))
		svc.Capacity.TransferTarget = strings.TrimSpace(svc.Capacity.TransferTarget)
		svc.Capacity.OverflowService = strings.TrimSpace(svc.Capacity.OverflowService)
		svc.Capacity.QueueAnnouncement = strings.TrimSpace(svc.Capacity.QueueAnnouncement)
		svc.Capacity.QueueOnTimeout = strings.TrimSpace(strings.ToLower(svc.Capacity.QueueOnTimeout))
		if svc.Priority < 0 {
			return fmt.Errorf("%w: service %q has negative priority", ErrInvalidConfig, svc.Name)
		}

		entries := make([]string, 0, len(svc.EntryNums))
		for _, entry := range svc.EntryNums {
			entry = normalizeValue(entry)
			if entry != "" {
				entries = append(entries, entry)
			}
		}
		svc.EntryNums = unique(entries)

		stages := make([]string, 0, len(svc.Match.IngressStages))
		for _, stage := range svc.Match.IngressStages {
			stage = normalizeIngressStage(stage)
			if stage != "" {
				stages = append(stages, stage)
			}
		}
		if len(stages) == 0 {
			stages = []string{"default-policy"}
		}
		svc.Match.IngressStages = unique(stages)

		gateways := make([]string, 0, len(svc.Match.SourceGateways))
		for _, gateway := range svc.Match.SourceGateways {
			gateway = normalizeValue(gateway)
			if gateway != "" {
				gateways = append(gateways, gateway)
			}
		}
		svc.Match.SourceGateways = unique(gateways)

		extensions := make([]string, 0, len(svc.Capacity.Extensions))
		for _, ext := range svc.Capacity.Extensions {
			ext = normalizeValue(ext)
			if ext != "" {
				extensions = append(extensions, ext)
			}
		}
		svc.Capacity.Extensions = unique(extensions)

		if svc.Capacity.MaxConcurrent > 0 || len(svc.Capacity.Extensions) > 0 {
			if svc.Capacity.Backend == "" {
				svc.Capacity.Backend = BackendLogical
			}
			if svc.Capacity.Allocator == "" {
				svc.Capacity.Allocator = AllocatorRoundRobin
			}
			if svc.Capacity.Backend == BackendSIPExtension && svc.Capacity.MaxConcurrent <= 0 {
				svc.Capacity.MaxConcurrent = len(svc.Capacity.Extensions)
			}
			if svc.Capacity.OverflowPolicy == "" {
				svc.Capacity.OverflowPolicy = OverflowBusy
			}
			if svc.Capacity.OverflowPolicy == OverflowQueue {
				if svc.Capacity.QueueMaxWaitSec <= 0 {
					svc.Capacity.QueueMaxWaitSec = 20
				}
				if svc.Capacity.QueueOnTimeout == "" {
					svc.Capacity.QueueOnTimeout = OverflowBusy
				}
			}
		}
	}

	return nil
}

// Validate checks a config against the deployment contract.
func Validate(cfg *Config, opts ValidateOptions) error {
	if cfg == nil {
		return fmt.Errorf("%w: nil config", ErrInvalidConfig)
	}
	if cfg.Version != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrUnsupportedKey, cfg.Version)
	}
	if cfg.Defaults.OnUnknownEntry != UnknownStaticFallback {
		return fmt.Errorf("%w: unsupported on_unknown_entry %q", ErrUnsupportedKey, cfg.Defaults.OnUnknownEntry)
	}

	allowedStages := unique(opts.AllowedIngressStages)
	if len(allowedStages) == 0 {
		allowedStages = []string{"default-policy"}
	}
	allowedGateways := unique(opts.AllowedGatewayIDs)
	requiredGatewayEntries := unique(opts.RequireSourceGatewaysForEntries)

	seen := make(map[string]string)
	serviceNames := make(map[string]struct{})

	for _, svc := range cfg.Services {
		if svc.Name == "" {
			return fmt.Errorf("%w: empty service name", ErrInvalidConfig)
		}
		if _, exists := serviceNames[svc.Name]; exists {
			return fmt.Errorf("%w: duplicate service name %q", ErrInvalidConfig, svc.Name)
		}
		serviceNames[svc.Name] = struct{}{}

		if svc.RouteType != RouteTypeAI {
			return fmt.Errorf("%w: service %q has unsupported route_type %q", ErrUnsupportedKey, svc.Name, svc.RouteType)
		}
		if len(svc.EntryNums) == 0 {
			return fmt.Errorf("%w: service %q must define at least one entry number", ErrInvalidConfig, svc.Name)
		}
		for _, stage := range svc.Match.IngressStages {
			if !slices.Contains(allowedStages, stage) {
				return fmt.Errorf("%w: service %q has unsupported ingress stage %q", ErrInvalidConfig, svc.Name, stage)
			}
		}
		for _, gateway := range svc.Match.SourceGateways {
			if !slices.Contains(allowedGateways, gateway) {
				return fmt.Errorf("%w: service %q references unknown gateway %q", ErrInvalidConfig, svc.Name, gateway)
			}
		}
		if svc.Capacity.MaxConcurrent < 0 {
			return fmt.Errorf("%w: service %q has negative max_concurrent", ErrInvalidConfig, svc.Name)
		}
		if svc.Capacity.MaxConcurrent > 0 || len(svc.Capacity.Extensions) > 0 {
			switch svc.Capacity.Backend {
			case "", BackendLogical:
				if svc.Capacity.Backend == "" {
					svc.Capacity.Backend = BackendLogical
				}
				if len(svc.Capacity.Extensions) > 0 {
					return fmt.Errorf("%w: service %q logical backend cannot define extensions", ErrInvalidConfig, svc.Name)
				}
			case BackendSIPExtension:
				if len(svc.Capacity.Extensions) == 0 {
					return fmt.Errorf("%w: service %q sip_extension backend requires extensions", ErrInvalidConfig, svc.Name)
				}
				if svc.Capacity.MaxConcurrent <= 0 {
					return fmt.Errorf("%w: service %q sip_extension backend requires positive max_concurrent", ErrInvalidConfig, svc.Name)
				}
				if svc.Capacity.MaxConcurrent > len(svc.Capacity.Extensions) {
					return fmt.Errorf("%w: service %q max_concurrent exceeds extensions inventory", ErrInvalidConfig, svc.Name)
				}
			default:
				return fmt.Errorf("%w: service %q has unsupported backend %q", ErrUnsupportedKey, svc.Name, svc.Capacity.Backend)
			}
			switch svc.Capacity.Allocator {
			case AllocatorRoundRobin, AllocatorPriority, AllocatorStickyCaller, AllocatorLeastRecently:
			default:
				return fmt.Errorf("%w: service %q has unsupported allocator %q", ErrUnsupportedKey, svc.Name, svc.Capacity.Allocator)
			}
			switch svc.Capacity.OverflowPolicy {
			case OverflowBusy, OverflowDirectTransfer, OverflowQueue, OverflowFailoverSvc, OverflowFallbackHuman:
			default:
				return fmt.Errorf("%w: service %q has unsupported overflow_policy %q", ErrUnsupportedKey, svc.Name, svc.Capacity.OverflowPolicy)
			}
			switch svc.Capacity.OverflowPolicy {
			case OverflowDirectTransfer, OverflowFallbackHuman:
				if svc.Capacity.TransferTarget == "" {
					return fmt.Errorf("%w: service %q requires transfer_target for overflow_policy %s", ErrInvalidConfig, svc.Name, svc.Capacity.OverflowPolicy)
				}
			case OverflowFailoverSvc:
				if svc.Capacity.OverflowService == "" {
					return fmt.Errorf("%w: service %q requires overflow_service for overflow_policy failover_service", ErrInvalidConfig, svc.Name)
				}
				if strings.EqualFold(svc.Capacity.OverflowService, svc.Name) {
					return fmt.Errorf("%w: service %q cannot fail over to itself", ErrInvalidConfig, svc.Name)
				}
			case OverflowQueue:
				if svc.Capacity.QueueMaxWaitSec <= 0 {
					return fmt.Errorf("%w: service %q requires positive queue_max_wait_seconds for overflow_policy queue", ErrInvalidConfig, svc.Name)
				}
				switch svc.Capacity.QueueOnTimeout {
				case OverflowBusy, OverflowDirectTransfer, OverflowFailoverSvc, OverflowFallbackHuman:
				default:
					return fmt.Errorf("%w: service %q has unsupported queue_on_timeout %q", ErrUnsupportedKey, svc.Name, svc.Capacity.QueueOnTimeout)
				}
				switch svc.Capacity.QueueOnTimeout {
				case OverflowDirectTransfer, OverflowFallbackHuman:
					if svc.Capacity.TransferTarget == "" {
						return fmt.Errorf("%w: service %q requires transfer_target for queue_on_timeout %s", ErrInvalidConfig, svc.Name, svc.Capacity.QueueOnTimeout)
					}
				case OverflowFailoverSvc:
					if svc.Capacity.OverflowService == "" {
						return fmt.Errorf("%w: service %q requires overflow_service for queue_on_timeout failover_service", ErrInvalidConfig, svc.Name)
					}
				}
			}
		}
		if len(requiredGatewayEntries) > 0 && len(svc.Match.SourceGateways) == 0 {
			for _, entry := range svc.EntryNums {
				if slices.Contains(requiredGatewayEntries, entry) {
					return fmt.Errorf("%w: service %q must scope representative entry %q to explicit source_gateways", ErrInvalidConfig, svc.Name, entry)
				}
			}
		}

		gatewayKeys := svc.Match.SourceGateways
		if len(gatewayKeys) == 0 {
			gatewayKeys = []string{"*"}
		}

		for _, entry := range svc.EntryNums {
			for _, stage := range svc.Match.IngressStages {
				for _, gateway := range gatewayKeys {
					key := entry + "|" + stage + "|" + gateway
					if other, exists := seen[key]; exists {
						return fmt.Errorf("%w: route overlap between %q and %q on %s", ErrInvalidConfig, other, svc.Name, key)
					}
					seen[key] = svc.Name
				}
			}
		}
	}

	for key, owner := range seen {
		parts := strings.Split(key, "|")
		if len(parts) != 3 || parts[2] != "*" {
			continue
		}
		prefix := parts[0] + "|" + parts[1] + "|"
		for otherKey, otherOwner := range seen {
			if !strings.HasPrefix(otherKey, prefix) || otherKey == key {
				continue
			}
			return fmt.Errorf("%w: wildcard route %q shadows %q on %s", ErrInvalidConfig, owner, otherOwner, key)
		}
	}

	return nil
}

func normalizeIngressStage(v string) string {
	v = normalizeValue(v)
	switch v {
	case "", "default", "default-policy":
		return "default-policy"
	case "public", "public-admission":
		return "public-admission"
	default:
		return v
	}
}

func normalizeValue(v string) string {
	return strings.TrimSpace(v)
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
