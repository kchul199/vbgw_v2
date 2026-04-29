package routing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "vbgw-orchestrator/internal/config"
)

func TestLoadRuntime_RejectsLegacyAIRouteOverlap(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "routing.yaml")

	payload := `version: 1
defaults:
  on_unknown_entry: static_fallback
services:
  - name: bot-main
    enabled: true
    route_type: ai
    entry_numbers: ["1000"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main"]
`

	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("failed to write routing config: %v", err)
	}

	cfg := &appconfig.Config{
		RoutingConfigPath: path,
		AIRouteNumbers:    []string{"9196", "1000"},
		PBXMainGateway:    "pbx-main",
	}

	_, err := LoadRuntime(cfg)
	if err == nil {
		t.Fatal("expected overlap error")
	}
	if !strings.Contains(err.Error(), "overlaps policy-owned entries") {
		t.Fatalf("expected overlap error, got %v", err)
	}
}

func TestLoadRuntime_IgnoresDisabledServiceOverlap(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "routing.yaml")

	payload := `version: 1
defaults:
  on_unknown_entry: static_fallback
services:
  - name: bot-main
    enabled: false
    route_type: ai
    entry_numbers: ["1000"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main"]
`

	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("failed to write routing config: %v", err)
	}

	cfg := &appconfig.Config{
		RoutingConfigPath: path,
		AIRouteNumbers:    []string{"9196", "1000"},
		PBXMainGateway:    "pbx-main",
	}

	runtime, err := LoadRuntime(cfg)
	if err != nil {
		t.Fatalf("expected disabled service overlap to be ignored, got %v", err)
	}
	if runtime == nil || runtime.Config == nil {
		t.Fatal("expected runtime to load")
	}
}
