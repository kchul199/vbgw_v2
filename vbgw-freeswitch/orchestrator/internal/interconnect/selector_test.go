package interconnect

import (
	"testing"
	"time"
)

func TestSelectorSelectOriginate_PrimaryHealthyWithStandbyFallback(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.Update(BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", now, 90*time.Second))
	store.Update(BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))

	selector := NewSelector(store, "pbx-main", "pbx-standby", SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})

	selection, err := selector.SelectOriginate()
	if err != nil {
		t.Fatalf("expected selection, got %v", err)
	}
	if selection.SelectedGateway != "pbx-main" {
		t.Fatalf("expected primary, got %s", selection.SelectedGateway)
	}
	if len(selection.GatewayOrder) != 2 || selection.GatewayOrder[1] != "pbx-standby" {
		t.Fatalf("expected primary then standby, got %+v", selection.GatewayOrder)
	}
}

func TestSelectorSelectOriginate_StandbyWhenPrimaryUnhealthy(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.Update(BuildGatewayState("pbx-main", true, "pbx-main NOREG", "test", now, 90*time.Second))
	store.Update(BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))

	selector := NewSelector(store, "pbx-main", "pbx-standby", SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})

	selection, err := selector.SelectOriginate()
	if err != nil {
		t.Fatalf("expected standby selection, got %v", err)
	}
	if selection.SelectedGateway != "pbx-standby" {
		t.Fatalf("expected standby, got %s", selection.SelectedGateway)
	}
	if len(selection.GatewayOrder) != 1 || selection.GatewayOrder[0] != "pbx-standby" {
		t.Fatalf("expected standby only, got %+v", selection.GatewayOrder)
	}
}

func TestSelectorSelectOriginate_FailFastOnPrimaryStale(t *testing.T) {
	store := NewStore()
	store.Update(BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", time.Now().Add(-5*time.Minute), 30*time.Second))

	selector := NewSelector(store, "pbx-main", "pbx-standby", SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      false,
		FailFastWhenStale: true,
	})

	if _, err := selector.SelectOriginate(); err == nil {
		t.Fatal("expected stale primary selection to fail fast")
	}
}

func TestSelectorSelectTransfer_PrimaryHealthyWithStandbyFallback(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.Update(BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", now, 90*time.Second))
	store.Update(BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))

	selector := NewSelector(store, "pbx-main", "pbx-standby", SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})

	selection, err := selector.SelectTransfer()
	if err != nil {
		t.Fatalf("expected selection, got %v", err)
	}
	if len(selection.GatewayOrder) != 2 {
		t.Fatalf("expected transfer fallback order, got %+v", selection.GatewayOrder)
	}
	if selection.GatewayOrder[0] != "pbx-main" || selection.GatewayOrder[1] != "pbx-standby" {
		t.Fatalf("expected primary then standby, got %+v", selection.GatewayOrder)
	}
}

func TestSelectorSelectOriginate_PrimaryOperatorStandby(t *testing.T) {
	store := NewStore()
	now := time.Now()
	store.Update(BuildGatewayState("pbx-main", true, "pbx-main REGED", "test", now, 90*time.Second))
	store.Update(BuildGatewayState("pbx-standby", false, "pbx-standby NOREG", "test", now, 90*time.Second))
	if !store.SetManualStandby("pbx-main", "maintenance", true) {
		t.Fatal("expected manual standby to be applied")
	}

	selector := NewSelector(store, "pbx-main", "pbx-standby", SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      true,
		FailFastWhenStale: true,
	})

	selection, err := selector.SelectOriginate()
	if err != nil {
		t.Fatalf("expected standby selection, got %v", err)
	}
	if selection.SelectedGateway != "pbx-standby" {
		t.Fatalf("expected standby, got %s", selection.SelectedGateway)
	}
	if selection.Reason != ReasonPrimaryOperatorStandby {
		t.Fatalf("expected operator standby reason, got %s", selection.Reason)
	}
}
