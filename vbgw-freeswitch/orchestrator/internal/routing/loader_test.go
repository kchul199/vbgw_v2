package routing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromPath_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routing.yaml")
	content := `
version: 1
defaults:
  on_unknown_entry: static_fallback
services:
  - name: bot-main
    enabled: true
    route_type: ai
    entry_numbers: ["9196"]
    match:
      ingress_stages: ["default-policy"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromPath(path, ValidateOptions{
		AllowedGatewayIDs:    []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages: []string{"default-policy", "public-admission"},
	})
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if cfg.Version != 1 {
		t.Fatalf("expected version 1, got %d", cfg.Version)
	}
	if len(cfg.Services) != 1 || cfg.Services[0].Name != "bot-main" {
		t.Fatalf("unexpected services: %+v", cfg.Services)
	}
}

func TestLoadFromPath_RejectsOverlap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "routing.yaml")
	content := `
version: 1
defaults:
  on_unknown_entry: static_fallback
services:
  - name: bot-main
    enabled: true
    route_type: ai
    entry_numbers: ["1000"]
    match:
      ingress_stages: ["default-policy"]
  - name: bot-shadow
    enabled: true
    route_type: ai
    entry_numbers: ["1000"]
    match:
      ingress_stages: ["default-policy"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadFromPath(path, ValidateOptions{
		AllowedGatewayIDs:    []string{"pbx-main", "pbx-standby"},
		AllowedIngressStages: []string{"default-policy"},
	})
	if err == nil {
		t.Fatal("expected overlap validation error")
	}
}
