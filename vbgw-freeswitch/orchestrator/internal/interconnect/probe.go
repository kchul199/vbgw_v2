package interconnect

import (
	"strings"
	"time"

	"vbgw-orchestrator/internal/routing"
)

func ModeForGateway(registerMode bool) string {
	if registerMode {
		return ModeRegister
	}
	return ModeTrunk
}

func BuildGatewayState(gatewayName string, registerMode bool, resp, source string, observedAt time.Time, freshnessTTL time.Duration) routing.GatewayState {
	status := strings.TrimSpace(resp)
	return routing.GatewayState{
		GatewayName:    gatewayName,
		Mode:           ModeForGateway(registerMode),
		HealthClass:    ClassifyGatewayHealth(registerMode, status),
		ObservedStatus: status,
		Source:         source,
		ObservedAtUnix: observedAt.Unix(),
		FreshnessTTL:   int64(freshnessTTL.Seconds()),
		Producer:       "orchestrator",
	}
}

func ClassifyGatewayHealth(registerMode bool, resp string) string {
	if resp == "" {
		return HealthUnknown
	}

	upper := strings.ToUpper(strings.TrimSpace(resp))
	if upper == "" {
		return HealthUnknown
	}
	if strings.Contains(upper, "INVALID GATEWAY") || strings.Contains(upper, "-ERR") {
		return HealthUnhealthy
	}

	if registerMode {
		if strings.Contains(upper, "REGED") {
			return HealthHealthy
		}
		return HealthUnhealthy
	}

	if gatewayStatusLineHas(upper, "DOWN") || gatewayStatusLineHas(upper, "FAILED") {
		return HealthUnhealthy
	}
	if strings.Contains(upper, "NOREG") || strings.Contains(upper, "REGED") || strings.Contains(upper, "UP") {
		return HealthHealthy
	}
	return HealthUnknown
}

func gatewayStatusLineHas(status, needle string) bool {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "STATE", "STATUS", "PINGSTATE":
			for _, field := range fields[1:] {
				if field == needle {
					return true
				}
			}
		}
	}
	return false
}
