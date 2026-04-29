package main

import "testing"

func TestGatewayHealthy_RegisterMode(t *testing.T) {
	if !gatewayHealthy(true, "pbx-main REGED") {
		t.Fatal("expected REGED gateway to be healthy in register mode")
	}
	if gatewayHealthy(true, "pbx-main NOREG") {
		t.Fatal("expected NOREG gateway to be unhealthy in register mode")
	}
}

func TestGatewayHealthy_TrunkMode(t *testing.T) {
	if !gatewayHealthy(false, "pbx-main NOREG") {
		t.Fatal("expected NOREG gateway to be healthy in trunk mode")
	}
	if gatewayHealthy(false, "Invalid Gateway!") {
		t.Fatal("expected invalid gateway to be unhealthy")
	}
	if gatewayHealthy(false, "pbx-main DOWN") {
		t.Fatal("expected DOWN gateway to be unhealthy")
	}
}
