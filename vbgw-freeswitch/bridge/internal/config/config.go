/**
 * @file config.go
 * @description Bridge 환경변수 설정 로더
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | WS, gRPC, VAD 설정
 * ─────────────────────────────────────────
 */

package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// WebSocket (from FS mod_audio_fork)
	WSPort int

	// Internal HTTP (from Orchestrator)
	InternalPort int

	// AI Engine (gRPC)
	AIGrpcAddr string
	AIGrpcTLS  bool

	// VAD
	OnnxModelPath string

	// gRPC Retry
	GrpcMaxRetries      int
	GrpcMaxBackoffMs    int
	GrpcStreamDeadlineS int

	// Orchestrator (for barge-in callback)
	OrchestratorURL   string
	InternalAPISecret string

	// Logging
	LogLevel       string
	RuntimeProfile string

	// WebSocket origin policy
	WSAllowedOrigins []string
}

func Load() *Config {
	return &Config{
		WSPort:           envIntAny(8090, "WS_PORT", "BRIDGE_WS_PORT"),
		InternalPort:     envIntAny(8091, "INTERNAL_PORT", "BRIDGE_INTERNAL_PORT"),
		AIGrpcAddr:       envStr("AI_GRPC_ADDR", "127.0.0.1:50051"),
		AIGrpcTLS:        envBool("AI_GRPC_TLS", false),
		OnnxModelPath:    envStr("ONNX_MODEL_PATH", "/models/silero_vad.onnx"),
		GrpcMaxRetries:   envInt("GRPC_MAX_RETRIES", 5),
		GrpcMaxBackoffMs: envInt("GRPC_MAX_BACKOFF_MS", 4000),
		// T-28: Reduced from 86400 (24h) to 7200 (2h) to prevent zombie streams
		GrpcStreamDeadlineS: envInt("GRPC_STREAM_DEADLINE_SECS", 7200),
		OrchestratorURL:     envStr("ORCHESTRATOR_URL", "http://127.0.0.1:8080"),
		InternalAPISecret:   envStr("INTERNAL_API_SECRET", ""),
		LogLevel:            envStr("LOG_LEVEL", "info"),
		RuntimeProfile:      envStr("RUNTIME_PROFILE", "dev"),
		WSAllowedOrigins:    envCSV("WS_ALLOWED_ORIGINS"),
	}
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envIntAny(fallback int, keys ...string) int {
	for _, key := range keys {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envCSV(key string) []string {
	v := os.Getenv(key)
	if strings.TrimSpace(v) == "" {
		return nil
	}

	parts := strings.Split(v, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}
