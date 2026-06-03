package interconnect

import "testing"

func TestClassifyGatewayHealth_RegisterMode(t *testing.T) {
	if got := ClassifyGatewayHealth(true, "pbx-main REGED"); got != HealthHealthy {
		t.Fatalf("expected healthy, got %s", got)
	}
	if got := ClassifyGatewayHealth(true, "pbx-main NOREG"); got != HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", got)
	}
}

func TestClassifyGatewayHealth_TrunkMode(t *testing.T) {
	if got := ClassifyGatewayHealth(false, "pbx-main NOREG"); got != HealthHealthy {
		t.Fatalf("expected healthy, got %s", got)
	}
	status := "State   \tNOREG\nStatus  \tUP (ping)\nFailedCallsIN\t0\nFailedCallsOUT\t0"
	if got := ClassifyGatewayHealth(false, status); got != HealthHealthy {
		t.Fatalf("expected healthy for trunk status with failed-call counters, got %s", got)
	}
	if got := ClassifyGatewayHealth(false, "Invalid Gateway!"); got != HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", got)
	}
	if got := ClassifyGatewayHealth(false, "Status  \tDOWN"); got != HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", got)
	}
}
