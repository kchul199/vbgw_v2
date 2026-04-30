package config

import (
	"os"
	"testing"
)

func TestLoad_BridgeDefaults(t *testing.T) {
	os.Unsetenv("WS_PORT")
	os.Unsetenv("BRIDGE_WS_PORT")
	os.Unsetenv("INTERNAL_PORT")
	os.Unsetenv("BRIDGE_INTERNAL_PORT")
	os.Unsetenv("AI_GRPC_ADDR")
	os.Unsetenv("WS_ALLOWED_ORIGINS")
	os.Unsetenv("INTERNAL_API_SECRET")
	os.Unsetenv("RUNTIME_PROFILE")

	cfg := Load()

	if cfg.WSPort != 8090 {
		t.Fatalf("expected WS_PORT=8090, got %d", cfg.WSPort)
	}
	if cfg.InternalPort != 8091 {
		t.Fatalf("expected INTERNAL_PORT=8091, got %d", cfg.InternalPort)
	}
	if cfg.AIGrpcAddr != "127.0.0.1:50051" {
		t.Fatalf("expected AI_GRPC_ADDR=127.0.0.1:50051, got %s", cfg.AIGrpcAddr)
	}
	if cfg.AIGrpcTLS {
		t.Fatal("expected AI_GRPC_TLS=false")
	}
	if cfg.GrpcMaxRetries != 5 {
		t.Fatalf("expected GRPC_MAX_RETRIES=5, got %d", cfg.GrpcMaxRetries)
	}
	if cfg.RuntimeProfile != "dev" {
		t.Fatalf("expected RUNTIME_PROFILE=dev, got %s", cfg.RuntimeProfile)
	}
}

func TestLoad_BridgeOverrides(t *testing.T) {
	os.Setenv("WS_PORT", "9090")
	os.Setenv("AI_GRPC_ADDR", "ai.example.com:50051")
	os.Setenv("AI_GRPC_TLS", "true")
	os.Setenv("INTERNAL_API_SECRET", "secret-123")
	os.Setenv("RUNTIME_PROFILE", "production")
	defer func() {
		os.Unsetenv("WS_PORT")
		os.Unsetenv("AI_GRPC_ADDR")
		os.Unsetenv("AI_GRPC_TLS")
		os.Unsetenv("INTERNAL_API_SECRET")
		os.Unsetenv("RUNTIME_PROFILE")
	}()

	cfg := Load()

	if cfg.WSPort != 9090 {
		t.Fatalf("expected WS_PORT=9090, got %d", cfg.WSPort)
	}
	if cfg.AIGrpcAddr != "ai.example.com:50051" {
		t.Fatalf("expected ai.example.com:50051, got %s", cfg.AIGrpcAddr)
	}
	if !cfg.AIGrpcTLS {
		t.Fatal("expected AI_GRPC_TLS=true")
	}
	if cfg.InternalAPISecret != "secret-123" {
		t.Fatalf("expected internal secret override, got %q", cfg.InternalAPISecret)
	}
	if cfg.RuntimeProfile != "production" {
		t.Fatalf("expected production profile, got %s", cfg.RuntimeProfile)
	}
}

func TestLoad_BridgeAliasOverrides(t *testing.T) {
	os.Unsetenv("WS_PORT")
	os.Unsetenv("INTERNAL_PORT")
	os.Setenv("BRIDGE_WS_PORT", "10090")
	os.Setenv("BRIDGE_INTERNAL_PORT", "10091")
	defer func() {
		os.Unsetenv("BRIDGE_WS_PORT")
		os.Unsetenv("BRIDGE_INTERNAL_PORT")
	}()

	cfg := Load()

	if cfg.WSPort != 10090 {
		t.Fatalf("expected BRIDGE_WS_PORT=10090, got %d", cfg.WSPort)
	}
	if cfg.InternalPort != 10091 {
		t.Fatalf("expected BRIDGE_INTERNAL_PORT=10091, got %d", cfg.InternalPort)
	}
}

func TestLoad_WSAllowedOrigins(t *testing.T) {
	os.Setenv("WS_ALLOWED_ORIGINS", "http://localhost:3000, https://admin.example.com ")
	defer os.Unsetenv("WS_ALLOWED_ORIGINS")

	cfg := Load()

	if len(cfg.WSAllowedOrigins) != 2 {
		t.Fatalf("expected 2 allowed origins, got %d", len(cfg.WSAllowedOrigins))
	}
	if cfg.WSAllowedOrigins[0] != "http://localhost:3000" {
		t.Fatalf("unexpected first origin: %s", cfg.WSAllowedOrigins[0])
	}
	if cfg.WSAllowedOrigins[1] != "https://admin.example.com" {
		t.Fatalf("unexpected second origin: %s", cfg.WSAllowedOrigins[1])
	}
}
