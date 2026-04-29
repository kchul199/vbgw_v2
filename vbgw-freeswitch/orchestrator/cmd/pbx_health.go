package main

import "vbgw-orchestrator/internal/interconnect"

func gatewayHealthy(registerMode bool, resp string) bool {
	return interconnect.ClassifyGatewayHealth(registerMode, resp) == interconnect.HealthHealthy
}
