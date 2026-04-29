/**
 * @file config.go
 * @description Orchestrator 환경변수 설정 로더
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | 100+ env var 매핑
 * ─────────────────────────────────────────
 */

package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// ESL
	ESLHost     string
	ESLPort     int
	ESLPassword string

	// PBX/SBC interconnect
	PBXInterconnectEnabled bool
	PBXMainRegister        bool
	PBXMainGateway         string
	PBXStandbyEnabled      bool
	PBXStandbyRegister     bool
	PBXStandbyGateway      string
	PBXStandbyHost         string
	PBXProbeIntervalSec    int
	PBXHealthTTLSec        int
	PBXFailFastOnStale     bool

	// IVR Transfer target (R-07: DTMF '0' → 상담원 연결 대상)
	IVRTransferTarget string

	// Bridge
	BridgeHost         string
	BridgeInternalPort int

	// HTTP API
	HTTPPort       int
	AdminAPIKey    string
	RateLimitRPS   float64
	RateLimitBurst int

	// Session (Redis + Limits)
	MaxSessions int64
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	// Recording
	RecordingEnable  bool
	RecordingDir     string
	RecordingMaxDays int
	RecordingMaxMB   int64

	// Webhooks
	CDRWebhookURL    string
	CDRWebhookSecret string

	// JWT Authentication
	JWTSecret string
	JWTIssuer string

	// OpenTelemetry
	OTelEndpoint string
	OTelEnabled  bool

	// Runtime
	RuntimeProfile string
	LogLevel       string

	// Phase 0 Routing
	RoutingConfigPath string

	// Dynamic AI dialplan
	AIRouteNumbers []string

	// Phase 4 Overflow
	OverflowQueueTickMS  int
	HumanFallbackEnabled bool

	// Phase 5 SIP extension slots
	SlotRegistrationRefreshMS int
	SlotRegistrationTTLSec    int

	// Phase 7 distributed queue
	DistributedQueueClaimTTLMS int
	DistributedQueueScanLimit  int
}

func Load() *Config {
	return &Config{
		ESLHost:                    envStr("ESL_HOST", "127.0.0.1"),
		ESLPort:                    envInt("ESL_PORT", 8021),
		ESLPassword:                envStr("ESL_PASSWORD", "ClueCon"),
		PBXInterconnectEnabled:     envBool("PBX_INTERCONNECT_ENABLED", false),
		PBXMainRegister:            envBool("PBX_MAIN_REGISTER", true),
		PBXMainGateway:             envStr("PBX_MAIN_GATEWAY", "pbx-main"),
		PBXStandbyEnabled:          envBool("PBX_STANDBY_ENABLED", false),
		PBXStandbyRegister:         envBool("PBX_STANDBY_REGISTER", true),
		PBXStandbyGateway:          envStr("PBX_STANDBY_GATEWAY", "pbx-standby"),
		PBXStandbyHost:             envStr("PBX_STANDBY_HOST", ""),
		PBXProbeIntervalSec:        envInt("PBX_PROBE_INTERVAL_SECONDS", 30),
		PBXHealthTTLSec:            envInt("PBX_HEALTH_TTL_SECONDS", 90),
		PBXFailFastOnStale:         envBool("PBX_FAIL_FAST_ON_STALE", true),
		IVRTransferTarget:          envStr("IVR_TRANSFER_TARGET", ""),
		BridgeHost:                 envStr("BRIDGE_HOST", "127.0.0.1"),
		BridgeInternalPort:         envInt("BRIDGE_INTERNAL_PORT", 8091),
		HTTPPort:                   envInt("HTTP_PORT", 8080),
		AdminAPIKey:                envStr("ADMIN_API_KEY", "changeme-admin-key"),
		RateLimitRPS:               envFloat("RATE_LIMIT_RPS", 20),
		RateLimitBurst:             envInt("RATE_LIMIT_BURST", 40),
		MaxSessions:                int64(envInt("MAX_SESSIONS", 100)),
		RedisAddr:                  envStr("REDIS_ADDR", "127.0.0.1:6379"),
		RedisPass:                  envStr("REDIS_PASS", ""),
		RedisDB:                    envInt("REDIS_DB", 0),
		RecordingEnable:            envBool("RECORDING_ENABLE", false),
		RecordingDir:               envStr("RECORDING_DIR", "/recordings"),
		RecordingMaxDays:           envInt("RECORDING_MAX_DAYS", 30),
		RecordingMaxMB:             int64(envInt("RECORDING_MAX_MB", 1024)),
		CDRWebhookURL:              envStr("CDR_WEBHOOK_URL", ""),
		CDRWebhookSecret:           envStr("CDR_WEBHOOK_SECRET", ""),
		JWTSecret:                  envStr("JWT_SECRET", ""),
		JWTIssuer:                  envStr("JWT_ISSUER", "vbgw-orchestrator"),
		OTelEndpoint:               envStr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OTelEnabled:                envBool("OTEL_ENABLED", false),
		RuntimeProfile:             envStr("RUNTIME_PROFILE", "dev"),
		LogLevel:                   envStr("LOG_LEVEL", "info"),
		RoutingConfigPath:          envStr("ROUTING_CONFIG_PATH", "/app/config/routing.yaml"),
		AIRouteNumbers:             envCSV("AI_ROUTE_NUMBERS", []string{"9196"}),
		OverflowQueueTickMS:        envInt("OVERFLOW_QUEUE_TICK_MS", 1000),
		HumanFallbackEnabled:       envBool("HUMAN_FALLBACK_ENABLED", true),
		SlotRegistrationRefreshMS:  envInt("SLOT_REGISTRATION_REFRESH_MS", 5000),
		SlotRegistrationTTLSec:     envInt("SLOT_REGISTRATION_TTL_SECONDS", 30),
		DistributedQueueClaimTTLMS: envInt("DISTRIBUTED_QUEUE_CLAIM_TTL_MS", 3000),
		DistributedQueueScanLimit:  envInt("DISTRIBUTED_QUEUE_SCAN_LIMIT", 8),
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

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
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

func envCSV(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return append([]string(nil), fallback...)
	}

	parts := strings.Split(v, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	if len(values) == 0 {
		return append([]string(nil), fallback...)
	}
	return values
}
