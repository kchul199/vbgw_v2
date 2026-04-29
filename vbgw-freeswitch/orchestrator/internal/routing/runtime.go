package routing

import (
	"errors"
	"fmt"
	"os"
	"strings"

	appconfig "vbgw-orchestrator/internal/config"
	"vbgw-orchestrator/internal/metrics"
)

// Runtime holds the loaded routing document and its resolver.
type Runtime struct {
	Path     string
	Config   *Config
	Resolver *Resolver
}

// LoadRuntime loads the routing config using the deployment defaults.
func LoadRuntime(cfg *appconfig.Config) (*Runtime, error) {
	path := strings.TrimSpace(cfg.RoutingConfigPath)
	if path == "" {
		metrics.RoutingConfigLoaded.Set(0)
		return nil, nil
	}

	validateOpts := ValidateOptions{
		AllowedGatewayIDs:               allowedGatewayIDs(cfg),
		AllowedIngressStages:            []string{"default-policy", "public-admission"},
		RequireSourceGatewaysForEntries: []string{"1000", "2000", "5551212"},
	}

	candidates := []string{path}
	if path == "/app/config/routing.yaml" {
		candidates = append(candidates, "config/routing.yaml")
	}

	var routeCfg *Config
	var err error
	for _, candidate := range candidates {
		routeCfg, err = LoadFromPath(candidate, validateOpts)
		if err == nil {
			path = candidate
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			metrics.RoutingConfigLoaded.Set(0)
			return nil, err
		}
	}
	if err != nil {
		metrics.RoutingConfigLoaded.Set(0)
		return nil, err
	}

	if overlaps := overlapEntries(routeCfg, cfg.AIRouteNumbers); len(overlaps) > 0 {
		metrics.RoutingConfigLoaded.Set(0)
		return nil, fmt.Errorf("%w: legacy AI_ROUTE_NUMBERS overlaps policy-owned entries %v", ErrInvalidConfig, overlaps)
	}

	metrics.RoutingConfigLoaded.Set(1)
	return &Runtime{
		Path:     path,
		Config:   routeCfg,
		Resolver: NewResolver(routeCfg),
	}, nil
}

// Reload re-reads routing.yaml from disk and updates Config + Resolver in-place.
func (rt *Runtime) Reload() error {
	if rt == nil || rt.Path == "" {
		return fmt.Errorf("routing runtime or path not initialized")
	}

	routeCfg, err := LoadFromPath(rt.Path, ValidateOptions{
		AllowedIngressStages: []string{"default-policy", "public-admission"},
	})
	if err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}

	rt.Config = routeCfg
	rt.Resolver = NewResolver(routeCfg)
	metrics.RoutingConfigLoaded.Set(1)
	return nil
}

func allowedGatewayIDs(cfg *appconfig.Config) []string {
	gatewayIDs := []string{cfg.PBXMainGateway}
	if standby := strings.TrimSpace(cfg.PBXStandbyGateway); standby != "" {
		gatewayIDs = append(gatewayIDs, standby)
	}
	return unique(gatewayIDs)
}

func overlapEntries(cfg *Config, legacyAIRoutes []string) []string {
	if cfg == nil {
		return nil
	}
	legacy := make(map[string]struct{}, len(legacyAIRoutes))
	for _, route := range legacyAIRoutes {
		route = normalizeValue(route)
		if route != "" {
			legacy[route] = struct{}{}
		}
	}
	if len(legacy) == 0 {
		return nil
	}

	var overlaps []string
	seen := make(map[string]struct{})
	for _, svc := range cfg.Services {
		if !svc.Enabled {
			continue
		}
		for _, entry := range svc.EntryNums {
			if _, ok := legacy[entry]; ok {
				if _, dup := seen[entry]; dup {
					continue
				}
				seen[entry] = struct{}{}
				overlaps = append(overlaps, entry)
			}
		}
	}
	return overlaps
}
