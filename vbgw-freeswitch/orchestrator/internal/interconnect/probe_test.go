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
	if got := ClassifyGatewayHealth(false, "Invalid Gateway!"); got != HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", got)
	}
	if got := ClassifyGatewayHealth(false, "pbx-main DOWN"); got != HealthUnhealthy {
		t.Fatalf("expected unhealthy, got %s", got)
	}
}
