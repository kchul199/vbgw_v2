package routing

import "strings"

// Resolver matches ingress calls to logical services.
type Resolver struct {
	cfg *Config
}

// NewResolver creates a resolver from a validated routing config.
func NewResolver(cfg *Config) *Resolver {
	if cfg == nil {
		return nil
	}
	return &Resolver{cfg: cfg}
}

// Resolve returns the matching service route for an ingress call.
func (r *Resolver) Resolve(in ResolveInput) (ResolvedRoute, bool) {
	if r == nil || r.cfg == nil {
		return ResolvedRoute{}, false
	}

	entry := normalizeValue(in.DestinationNumber)
	stage := normalizeIngressStage(in.IngressStage)
	sourceGateway := normalizeValue(in.SourceGateway)

	for _, svc := range r.cfg.Services {
		if !svc.Enabled {
			continue
		}
		if !contains(svc.EntryNums, entry) {
			continue
		}
		if !contains(svc.Match.IngressStages, stage) {
			continue
		}
		if len(svc.Match.SourceGateways) > 0 && !contains(svc.Match.SourceGateways, sourceGateway) {
			continue
		}

		return ResolvedRoute{
			ServiceName:          svc.Name,
			RouteType:            svc.RouteType,
			EntryNumber:          entry,
			IngressStage:         stage,
			SourceGateway:        sourceGateway,
			RoutingConfigVersion: r.cfg.Version,
			Matched:              true,
		}, true
	}

	return ResolvedRoute{}, false
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
