/**
 * @file prometheus.go
 * @description Prometheus 메트릭 — C++ RuntimeMetrics 호환 20+ 게이지/카운터
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | 기존 C++ 메트릭 1:1 매핑
 * v1.1.0 | 2026-04-08 | [Implementer] | S-01~S-03 | SIP hangup, PDD, 등록 알람 메트릭
 * ─────────────────────────────────────────
 */

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ActiveCalls = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_active_calls",
		Help: "Number of currently active call sessions",
	})

	SipRegistered = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_sip_registered",
		Help: "SIP registration status (1=registered, 0=not)",
	})

	GrpcActiveSessions = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_grpc_active_sessions",
		Help: "Number of active gRPC streaming sessions",
	})

	GrpcDroppedFrames = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_grpc_dropped_frames_total",
		Help: "Total audio frames dropped due to queue overflow",
	})

	GrpcStreamErrors = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_grpc_stream_errors_total",
		Help: "Total gRPC stream errors",
	})

	GrpcReconnectAttempts = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_grpc_reconnect_attempts_total",
		Help: "Total gRPC reconnection attempts",
	})

	VadSpeechEvents = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_vad_speech_events_total",
		Help: "Total VAD speech detection events",
	})

	BargeInEvents = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_bargein_events_total",
		Help: "Total barge-in (TTS interrupt) events",
	})

	ApiOutboundRequests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_admin_api_outbound_requests_total",
		Help: "Total outbound call API requests",
	})

	ApiOutboundRejected = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_admin_api_outbound_rejected_capacity_total",
		Help: "Total outbound calls rejected due to capacity",
	})

	ApiRateLimited = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_admin_api_rate_limited_total",
		Help: "Total API requests rejected by rate limiter",
	})

	SessionDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "vbgw_session_duration_seconds",
		Help:    "Call session duration distribution",
		Buckets: prometheus.ExponentialBuckets(1, 2, 12),
	})

	ApiLatency = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "vbgw_api_latency_seconds",
		Help:    "HTTP API endpoint latency",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path", "status"})

	ESLConnected = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_esl_connected",
		Help: "ESL connection status (1=connected, 0=not)",
	})

	BridgeHealthy = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_bridge_healthy",
		Help: "Bridge health status (1=healthy, 0=not)",
	})

	RecordingCleanupFiles = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_recording_cleanup_files_total",
		Help: "Total recording files cleaned up",
	})

	RecordingCleanupBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "vbgw_recording_cleanup_bytes_total",
		Help: "Total bytes freed by recording cleanup",
	})

	// S-01: SIP hangup cause + SIP response code breakdown
	CallHangupTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_call_hangup_total",
		Help: "Total call hangups by cause and SIP response code",
	}, []string{"cause", "sip_code"})

	// S-02: Call setup time (PDD — Post Dial Delay)
	CallSetupDuration = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "vbgw_call_setup_duration_seconds",
		Help:    "Time from CHANNEL_CREATE to CHANNEL_ANSWER (PDD)",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 3, 5, 10},
	})

	// S-03: PBX gateway registration alarm
	SipRegistrationAlarm = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_sip_registration_alarm",
		Help: "PBX gateway registration alarm (0=ok, 1=alarm — unregistered 3+ min)",
	})

	RoutingConfigLoaded = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "vbgw_routing_config_loaded",
		Help: "Routing configuration load status (1=loaded, 0=not loaded)",
	})

	RouteResolutionTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_route_resolution_total",
		Help: "Dynamic dialplan route resolution results",
	}, []string{"result"})

	ServiceActiveCalls = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_service_active_calls",
		Help: "Current active calls per logical service on this orchestrator node",
	}, []string{"service"})

	ServiceCapacityMax = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_service_capacity_max",
		Help: "Configured max concurrent calls per logical service",
	}, []string{"service"})

	SlotInUse = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_slot_in_use",
		Help: "Logical slot occupancy per service and slot",
	}, []string{"service", "slot"})

	SlotBackendAvailable = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_slot_backend_available",
		Help: "Currently available slots per service and backend",
	}, []string{"service", "backend"})

	ExtensionRegistered = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_extension_registered",
		Help: "Current SIP extension registration status (1=registered, 0=not)",
	}, []string{"extension"})

	OverflowTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_overflow_total",
		Help: "Total service overflow events by policy",
	}, []string{"service", "policy"})

	ServiceQueueDepth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_service_queue_depth",
		Help: "Current queued sessions per logical service",
	}, []string{"service"})

	QueueWaitSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_queue_wait_seconds",
		Help: "Current oldest queue wait time in seconds per logical service",
	}, []string{"service"})

	HumanFallbackTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_human_fallback_total",
		Help: "Total human fallback transfers by service and target",
	}, []string{"service", "target"})

	QueueAbandonTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_queue_abandon_total",
		Help: "Total queued session removals by reason",
	}, []string{"service", "reason"})

	GatewayHealth = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_gateway_health",
		Help: "Current gateway health class (1 for current class, 0 otherwise)",
	}, []string{"gateway", "mode", "class"})

	GatewayHealthAgeSeconds = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "vbgw_gateway_health_age_seconds",
		Help: "Age of the latest gateway health snapshot in seconds",
	}, []string{"gateway"})

	FailoverDecisionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_failover_decisions_total",
		Help: "Gateway selection decisions by reason and selected gateway",
	}, []string{"reason", "selected_gateway"})

	GatewayProbeFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "vbgw_gateway_probe_failures_total",
		Help: "Total gateway probe failures by gateway",
	}, []string{"gateway"})
)
