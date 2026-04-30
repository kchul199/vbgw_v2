package config

import (
	"os"
	"testing"
)

func TestLoad_DefaultsIncludePhase6Secrets(t *testing.T) {
	os.Unsetenv("ADMIN_CONTROL_KEY")
	os.Unsetenv("INTERNAL_API_SECRET")
	cfg := Load()

	if cfg.AdminControlKey != "" {
		t.Fatalf("expected empty admin control key by default, got %q", cfg.AdminControlKey)
	}
	if cfg.InternalAPISecret != "" {
		t.Fatalf("expected empty internal api secret by default, got %q", cfg.InternalAPISecret)
	}
}

func TestLoad_ReadsPhase6Secrets(t *testing.T) {
	os.Setenv("ADMIN_CONTROL_KEY", "control-secret-123")
	os.Setenv("INTERNAL_API_SECRET", "internal-secret-456")
	defer os.Unsetenv("ADMIN_CONTROL_KEY")
	defer os.Unsetenv("INTERNAL_API_SECRET")

	cfg := Load()
	if cfg.AdminControlKey != "control-secret-123" {
		t.Fatalf("expected admin control key override, got %q", cfg.AdminControlKey)
	}
	if cfg.InternalAPISecret != "internal-secret-456" {
		t.Fatalf("expected internal api secret override, got %q", cfg.InternalAPISecret)
	}
}

func TestLoad_ReadsPhase7ClusterConfig(t *testing.T) {
	os.Setenv("NODE_ID", "orch-a")
	os.Setenv("ORCHESTRATOR_VERSION", "7.1.0")
	os.Setenv("LEASE_SCHEMA_VERSION", "2")
	defer os.Unsetenv("NODE_ID")
	defer os.Unsetenv("ORCHESTRATOR_VERSION")
	defer os.Unsetenv("LEASE_SCHEMA_VERSION")

	cfg := Load()
	if cfg.NodeID != "orch-a" {
		t.Fatalf("expected node id override, got %q", cfg.NodeID)
	}
	if cfg.OrchestratorVersion != "7.1.0" {
		t.Fatalf("expected orchestrator version override, got %q", cfg.OrchestratorVersion)
	}
	if cfg.LeaseSchemaVersion != 2 {
		t.Fatalf("expected lease schema version override, got %d", cfg.LeaseSchemaVersion)
	}
}
