/**
 * @file main.go
 * @description Orchestrator 진입점 — DI + 5-stage graceful shutdown
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | ESL 연결, HTTP API, 5단계 셧다운
 * v1.1.0 | 2026-04-09 | [Implementer] | T-03~T-13,T-19~T-20 | 28개 이슈 수정
 * ─────────────────────────────────────────
 */

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"vbgw-orchestrator/internal/api"
	"vbgw-orchestrator/internal/capacity"
	"vbgw-orchestrator/internal/cdr"
	"vbgw-orchestrator/internal/cluster"
	"vbgw-orchestrator/internal/config"
	"vbgw-orchestrator/internal/esl"
	"vbgw-orchestrator/internal/interconnect"
	"vbgw-orchestrator/internal/ivr"
	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/overflow"
	"vbgw-orchestrator/internal/recording"
	"vbgw-orchestrator/internal/routing"
	"vbgw-orchestrator/internal/session"
	"vbgw-orchestrator/internal/slots"
	"vbgw-orchestrator/internal/telemetry"
)

const (
	vbgwAIStartExtension         = "vbgw-ai-start"
	vbgwQueueHoldExtension       = "vbgw-queue-hold"
	vbgwHumanCallcenterExt       = "vbgw-human-callcenter"
	vbgwDefaultQueueHoldMusic    = "tone_stream://%(3000,1000,440,480)"
	vbgwDefaultQueueAnnouncement = "silence_stream://50"
)

func main() {
	// Load config
	cfg := config.Load()
	setupLogging(cfg.LogLevel)

	nodeID := strings.TrimSpace(cfg.NodeID)
	if nodeID == "" {
		nodeID = cluster.DefaultNodeID()
	}
	if nodeID == "" {
		nodeID = "vbgw-orchestrator"
	}
	slog.Info("Orchestrator starting",
		"node_id", nodeID,
		"profile", cfg.RuntimeProfile,
		"max_sessions", cfg.MaxSessions,
		"http_port", cfg.HTTPPort,
	)

	// Production security validation
	if cfg.RuntimeProfile == "production" {
		validateProdConfig(cfg)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	routeRuntime, err := routing.LoadRuntime(cfg)
	if err != nil {
		slog.Error("Failed to load routing runtime", "err", err, "routing_config_path", cfg.RoutingConfigPath)
		os.Exit(1)
	}
	slotRegistry := slots.NewRegistry(time.Duration(cfg.SlotRegistrationTTLSec) * time.Second)
	capacityMgr := capacity.NewManager(runtimeConfig(routeRuntime), slotRegistry)
	gatewayStore := interconnect.NewStore()
	gatewaySelector := interconnect.NewSelector(gatewayStore, cfg.PBXMainGateway, cfg.PBXStandbyGateway, interconnect.SelectionPolicy{
		PreferPrimary:     true,
		AllowStandby:      cfg.PBXStandbyEnabled,
		FailFastWhenStale: cfg.PBXFailFastOnStale,
	})
	handoffMgr := interconnect.NewHandoffManager()
	bridgeURL := fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort)

	// Initialize OpenTelemetry
	shutdownOTel, err := telemetry.InitOTel(ctx, cfg, nodeID)
	if err != nil {
		slog.Error("Failed to initialize OpenTelemetry", "err", err)
		// Don't exit, just continue without OTel
	} else {
		defer shutdownOTel(ctx)
	}

	// Initialize session manager (Redis primary, Memory fallback)
	// C-3 FIX: Redis 연결 실패 시 MemoryStore로 자동 폴백하여 게이트웨이 가용성 유지
	var sessionMgr session.Store
	var cdrStore *cdr.CDRStore
	overflowOpts := make([]overflow.Option, 0, 1)
	var clusterMgr *cluster.Manager
	redisMgr, err := session.NewRedisStore(cfg.RedisAddr, cfg.RedisPass, cfg.RedisDB, cfg.MaxSessions, nodeID)
	if err != nil {
		slog.Error("Redis connection failed — falling back to in-memory session store",
			"redis_addr", cfg.RedisAddr, "err", err,
			"impact", "Multi-node session sync and Pub/Sub command routing will be unavailable")
		if cfg.RuntimeProfile == "production" {
			slog.Error("Production profile requires Redis-backed session store — refusing to start with memory fallback")
			os.Exit(1)
		}
		sessionMgr = session.NewMemoryStore(cfg.MaxSessions)
	} else {
		sessionMgr = redisMgr
		if result, reconcileErr := reconcileActiveCallCounter(ctx, sessionMgr, nil); reconcileErr != nil {
			slog.Error("Initial active call counter reconciliation failed", "err", reconcileErr)
			if cfg.RuntimeProfile == "production" {
				os.Exit(1)
			}
		} else if result.Corrected {
			slog.Warn("Initial active call counter reconciled", "previous", result.Previous, "actual", result.Actual)
		}
		cdrStore = cdr.NewCDRStore(redisMgr.Client())
		cdr.SetCDRStore(cdrStore)
		overflowOpts = append(overflowOpts, overflow.WithRedisClient(
			redisMgr.Client(),
			nodeID,
			time.Duration(cfg.DistributedQueueClaimTTLMS)*time.Millisecond,
			cfg.DistributedQueueScanLimit,
		))
		clusterMgr = cluster.NewManager(redisMgr.Client(), nodeID, cluster.Options{
			HeartbeatInterval:   time.Duration(cfg.ClusterHeartbeatIntervalMS) * time.Millisecond,
			HeartbeatTTL:        time.Duration(cfg.ClusterHeartbeatTTLMS) * time.Millisecond,
			ReaperInterval:      time.Duration(cfg.ClusterReaperIntervalMS) * time.Millisecond,
			LeaseTTL:            time.Duration(cfg.ClusterLeaseTTLMS) * time.Millisecond,
			LeaseStaleGrace:     time.Duration(cfg.ClusterLeaseStaleGraceMS) * time.Millisecond,
			OrchestratorVersion: cfg.OrchestratorVersion,
			LeaseSchemaVersion:  cfg.LeaseSchemaVersion,
			LocalSessions: func() int64 {
				var count int64
				sessionMgr.ForEachLocal(func(_ *session.SessionState) {
					count++
				})
				return count
			},
			RoutingVersion: func() int {
				if routeRuntime != nil && routeRuntime.Config != nil {
					return routeRuntime.Config.Version
				}
				return 0
			},
		})
	}
	overflowMgr := overflow.NewManager(overflowOpts...)
	if clusterMgr != nil {
		capacityMgr.SetNodeStateProvider(clusterMgr)
		capacityMgr.SetDistributedLeases(clusterMgr.LeaseStore())
		clusterMgr.Start(ctx)
		if report, compatErr := clusterMgr.CompatibilityReport(ctx, func() int {
			if routeRuntime != nil && routeRuntime.Config != nil {
				return routeRuntime.Config.Version
			}
			return 0
		}()); compatErr == nil && !report.Compatible {
			slog.Error("Cluster compatibility gate failed on startup", "issues", report.Issues)
			if cfg.RuntimeProfile == "production" {
				os.Exit(1)
			}
			_ = clusterMgr.SetState(ctx, cluster.NodeStatePaused, "compatibility gate failed")
		}
		go runLeaseRenewalLoop(ctx, 3*time.Second, capacityMgr)
		go runActiveCallReconciler(ctx, 30*time.Second, sessionMgr, clusterMgr)
	}

	// Connect ESL (eslClient used in handler closure, declared first)
	var eslClient *esl.Client
	eslHandler := func(evt *esl.Event) {
		handleESLEvent(evt, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, cfg, eslClient, nodeID)
	}
	eslClient = esl.NewClient(cfg.ESLHost, cfg.ESLPort, cfg.ESLPassword, eslHandler)
	eslClient.SetPBXGateways(cfg.PBXMainGateway, cfg.PBXStandbyGateway)

	// Subscribe to internal Pub/Sub commands for distributed API routing
	go sessionMgr.SubscribeCommands(ctx, func(msg session.CommandMsg) {
		slog.Info("Received Pub/Sub command", "action", msg.Action, "session_id", msg.SessionID)
		// API commands are routed to local node
		api.HandleLocalCommand(ctx, msg, sessionMgr, overflowMgr, eslClient, gatewaySelector, handoffMgr, bridgeURL, cfg.InternalAPISecret)
	})

	// Register reconnect callback: reconcile orphan sessions after ESL reconnection
	eslClient.SetOnReconnect(func() {
		metrics.ESLConnected.Set(1)
		slog.Info("ESL reconnected — syncing active sessions")
		activeUUIDs, err := eslClient.GetActiveChannelUUIDs()
		if err != nil {
			slog.Error("Failed to get active channels after reconnect", "err", err)
			return
		}
		// Release sessions that no longer exist in FreeSWITCH
		orphanCount := 0
		sessionMgr.ForEachLocal(func(s *session.SessionState) {
			if !activeUUIDs[s.FSUUID] {
				slog.Warn("Releasing orphan session", "session_id", s.SessionID, "fs_uuid", s.FSUUID)
				sessionMgr.Release(ctx, s.SessionID)
				orphanCount++
			}
		})
		if orphanCount > 0 {
			slog.Info("Orphan session cleanup complete", "released", orphanCount)
		}
		metrics.ActiveCalls.Set(float64(sessionMgr.Count(ctx)))
	})

	if err := eslClient.ConnectWithRetry(ctx); err != nil {
		slog.Error("ESL connection failed", "err", err)
		os.Exit(1)
	}
	metrics.ESLConnected.Set(1)
	if err := slotRegistry.RefreshFromESL(ctx, eslClient); err != nil {
		slog.Warn("Failed to load initial SIP registration inventory", "err", err)
	}

	// Q-09: Ensure FS is not stuck in paused state from previous Orchestrator shutdown
	if err := eslClient.Resume(ctx); err != nil {
		slog.Warn("fsctl resume failed (may be normal on fresh start)", "err", err)
	}

	// Start recording cleaner
	cleaner := recording.NewCleaner(
		cfg.RecordingDir, cfg.RecordingMaxDays, cfg.RecordingMaxMB, cfg.RecordingEnable,
	)
	go cleaner.Run(ctx)

	queueTick := time.Duration(cfg.OverflowQueueTickMS) * time.Millisecond
	if queueTick <= 0 {
		queueTick = time.Second
	}
	go runOverflowDispatcher(ctx, queueTick, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg)
	regRefreshTick := time.Duration(cfg.SlotRegistrationRefreshMS) * time.Millisecond
	if regRefreshTick <= 0 {
		regRefreshTick = 5 * time.Second
	}
	go runSlotRegistrationRefresher(ctx, regRefreshTick, slotRegistry, eslClient)

	probeInterval := time.Duration(cfg.PBXProbeIntervalSec) * time.Second
	if probeInterval <= 0 {
		probeInterval = 30 * time.Second
	}
	freshnessTTL := time.Duration(cfg.PBXHealthTTLSec) * time.Second
	if freshnessTTL <= 0 {
		freshnessTTL = 90 * time.Second
	}

	if cfg.PBXInterconnectEnabled {
		refreshGatewaySnapshots(ctx, eslClient, gatewayStore, cfg, freshnessTTL)
	} else {
		metrics.SipRegistered.Set(0)
		metrics.SipRegistrationAlarm.Set(0)
	}

	// P-15 + R-04 + S-03 + Phase 3: gateway health snapshot monitor with consecutive failure alarm
	go func() {
		if !cfg.PBXInterconnectEnabled {
			slog.Info("PBX interconnect monitor disabled")
			return
		}

		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()

		alarmThreshold := max(1, int((3*time.Minute)/probeInterval))
		consecutiveFails := 0

		updateSnapshots := func() bool {
			return refreshGatewaySnapshots(ctx, eslClient, gatewayStore, cfg, freshnessTTL)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				primaryHealthy := updateSnapshots()

				// S-03: Consecutive failure alarm logic
				if primaryHealthy {
					if consecutiveFails >= alarmThreshold {
						slog.Info("ALARM CLEARED: PBX gateway re-registered",
							"was_down_checks", consecutiveFails)
					}
					consecutiveFails = 0
					metrics.SipRegistrationAlarm.Set(0)
				} else {
					consecutiveFails++
					if consecutiveFails == alarmThreshold {
						slog.Error("ALARM: PBX gateway registration lost for 3+ minutes",
							"consecutive_fails", consecutiveFails,
							"action", "All outbound calls will fail. Check PBX connectivity.")
						metrics.SipRegistrationAlarm.Set(1)
					} else if consecutiveFails > alarmThreshold && consecutiveFails%10 == 0 {
						slog.Error("ALARM ONGOING: PBX gateway still unregistered",
							"consecutive_fails", consecutiveFails)
					}
				}
			}
		}
	}()

	// HTTP server
	router, err := api.NewRouter(cfg, routeRuntime, capacityMgr, overflowMgr, gatewayStore, gatewaySelector, handoffMgr, clusterMgr, eslClient, sessionMgr, nodeID, cdrStore)
	if err != nil {
		slog.Error("Failed to build HTTP router", "err", err, "routing_config_path", cfg.RoutingConfigPath)
		os.Exit(1)
	}
	httpServer := &http.Server{
		Addr:    ":" + strconv.Itoa(cfg.HTTPPort),
		Handler: router,
	}

	go func() {
		slog.Info("HTTP API listening", "port", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "err", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	if clusterMgr != nil {
		_ = clusterMgr.SetState(context.Background(), cluster.NodeStateDraining, "process shutdown")
	}
	slog.Info("[Shutdown 1/5] HTTP server: rejecting new requests")
	httpServer.SetKeepAlivesEnabled(false)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	httpServer.Shutdown(shutdownCtx)

	// T-19: Notify Bridge to prepare for shutdown (stop accepting new streams)
	shutdownBridgeReq, _ := http.NewRequest("POST", bridgeURL+"/internal/shutdown", nil)
	if cfg.InternalAPISecret != "" {
		shutdownBridgeReq.Header.Set("X-Internal-Secret", cfg.InternalAPISecret)
	}
	shutdownBridgeClient := &http.Client{Timeout: 5 * time.Second}
	if resp, err := shutdownBridgeClient.Do(shutdownBridgeReq); err != nil {
		slog.Warn("Bridge shutdown notification failed (may be already down)", "err", err)
	} else {
		resp.Body.Close()
	}

	slog.Info("[Shutdown 2/5] ESL: fsctl pause sent")
	eslClient.Pause(context.Background())

	remaining := sessionMgr.Count(context.Background())
	slog.Info("[Shutdown 3/5] Draining active sessions", "count", remaining, "timeout", "30s")
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer drainCancel()
	// P-04: Kill FS channels on drain timeout to send BYE to PBX
	sessionMgr.WaitAllDrained(drainCtx, func(fsUUID string) {
		slog.Info("Killing channel during shutdown", "fs_uuid", fsUUID)
		eslClient.Kill(drainCtx, fsUUID)
	})

	slog.Info("[Shutdown 4/5] Bridge gRPC streams closed")

	slog.Info("[Shutdown 5/5] ESL connection closed")
	eslClient.Close()
	metrics.ESLConnected.Set(0)

	cancel()
	slog.Info("Orchestrator shutdown complete")
}

func runActiveCallReconciler(ctx context.Context, interval time.Duration, sessions session.Store, clusterMgr *cluster.Manager) {
	if sessions == nil {
		return
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := reconcileActiveCallCounter(ctx, sessions, clusterMgr)
			if err != nil {
				slog.Error("Active call counter reconciliation failed", "err", err)
				continue
			}
			if result.Corrected {
				slog.Warn("Active call counter reconciled", "previous", result.Previous, "actual", result.Actual)
			}
		}
	}
}

func reconcileActiveCallCounter(ctx context.Context, sessions session.Store, clusterMgr *cluster.Manager) (session.ReconcileResult, error) {
	actual, err := expectedActiveCalls(ctx, sessions, clusterMgr)
	if err != nil {
		return session.ReconcileResult{}, err
	}
	return sessions.ReconcileActiveCalls(ctx, actual)
}

func expectedActiveCalls(ctx context.Context, sessions session.Store, clusterMgr *cluster.Manager) (int64, error) {
	if clusterMgr != nil {
		nodes, err := clusterMgr.ListNodes(ctx)
		if err != nil {
			return 0, err
		}
		local := localActiveSessions(sessions)
		now := time.Now()
		var total int64
		var fresh int
		for _, node := range nodes {
			if node.HeartbeatAt.IsZero() {
				continue
			}
			ttl := time.Duration(node.HeartbeatTTLMS) * time.Millisecond
			if ttl <= 0 {
				ttl = 8 * time.Second
			}
			if now.Sub(node.HeartbeatAt) > ttl {
				continue
			}
			total += node.LocalActiveSessions
			fresh++
		}
		if fresh > 0 {
			if local > total {
				return local, nil
			}
			return total, nil
		}
	}
	return localActiveSessions(sessions), nil
}

func localActiveSessions(sessions session.Store) int64 {
	var local int64
	if sessions != nil {
		sessions.ForEachLocal(func(s *session.SessionState) {
			local++
		})
	}
	return local
}

// handleESLEvent dispatches incoming ESL events to the appropriate handler.
func handleESLEvent(evt *esl.Event, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, cfg *config.Config, eslClient esl.Commander, nodeID string) {
	ctx := context.Background()
	switch evt.Name() {
	case "CHANNEL_CREATE":
		onChannelCreate(ctx, evt, sessionMgr, capacityMgr, handoffMgr, eslClient, nodeID)

	case "CHANNEL_ANSWER":
		onChannelAnswer(ctx, evt, sessionMgr, handoffMgr)

	case "CHANNEL_PARK":
		onChannelPark(ctx, evt, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, nodeID)

	case "DTMF":
		onDtmf(ctx, evt, sessionMgr)

	case "CHANNEL_HANGUP_COMPLETE":
		onChannelHangup(ctx, evt, sessionMgr, overflowMgr, handoffMgr, cfg)

	// P-12: SIP Hold/Resume (Re-INVITE) → pause/resume AI streaming
	case "CHANNEL_HOLD":
		onChannelHold(ctx, evt, sessionMgr, cfg)

	case "CHANNEL_UNHOLD":
		onChannelUnhold(ctx, evt, sessionMgr, cfg)

	case "CUSTOM":
		switch evt.SubClass() {
		case "sofia::register":
			slog.Info("SIP registered")
			metrics.SipRegistered.Set(1)
		case "sofia::unregister":
			slog.Info("SIP unregistered")
			metrics.SipRegistered.Set(0)
		case "mod_audio_fork::play_audio":
			onAudioForkPlayAudio(evt, eslClient)
		case "mod_audio_fork::kill_audio":
			onAudioForkKillAudio(evt, eslClient)
		}
	}
}

func onChannelCreate(ctx context.Context, evt *esl.Event, sessionMgr session.Store, capacityMgr *capacity.Manager, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, nodeID string) {
	fsUUID := evt.UUID()
	slog.Info("CHANNEL_CREATE", "fs_uuid", fsUUID, "caller_id", evt.CallerID())

	ensureSessionForEvent(ctx, evt, sessionMgr, capacityMgr, handoffMgr, eslClient, nodeID, false)
}

func onChannelAnswer(ctx context.Context, evt *esl.Event, sessionMgr session.Store, handoffMgr *interconnect.HandoffManager) {
	if handoffMgr != nil && handoffMgr.ResolveAnswered(evt.UUID()) {
		slog.Info("Resolved handoff answer", "fs_uuid", evt.UUID())
		return
	}

	s, ok := sessionMgr.GetByFSUUID(ctx, evt.UUID())
	if !ok {
		return
	}
	now := time.Now()
	s.SetAnsweredAt(now)

	// S-02: Record PDD (Post Dial Delay) — time from CREATE to ANSWER
	pdd := now.Sub(s.CreatedAt).Seconds()
	metrics.CallSetupDuration.Observe(pdd)

	slog.Info("CHANNEL_ANSWER", "session_id", s.SessionID, "pdd_ms", int(pdd*1000))
}

func onChannelPark(ctx context.Context, evt *esl.Event, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config, nodeID string) {
	s, ok := ensureSessionForEvent(ctx, evt, sessionMgr, capacityMgr, handoffMgr, eslClient, nodeID, true)
	if !ok {
		return
	}

	s.SetRoutingMetadata(evt.RouteEntryNumber(), evt.RouteServiceName(), evt.SourceGateway(), evt.RouteIngressStage(), evt.RouteType(), evt.RoutingConfigVersion())
	if activated := tryActivateService(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, s, s.ServiceName, map[string]struct{}{}); activated {
		slog.Info("CHANNEL_PARK — IVR started", "session_id", s.SessionID, "service", s.ServiceName)
	}
}

func ensureSessionForEvent(ctx context.Context, evt *esl.Event, sessionMgr session.Store, capacityMgr *capacity.Manager, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, nodeID string, synthesized bool) (*session.SessionState, bool) {
	fsUUID := evt.UUID()
	if fsUUID == "" {
		return nil, false
	}

	if handoffMgr != nil && handoffMgr.IsPending(fsUUID) {
		slog.Info("Ignoring transient handoff channel event", "fs_uuid", fsUUID, "event", evt.Name())
		return nil, false
	}

	if s, exists := sessionMgr.GetByFSUUID(ctx, fsUUID); exists {
		return s, true
	}

	sessionID := uuid.New().String()
	s := session.NewSession(nodeID, sessionID, fsUUID, evt.CallerID(), evt.DestNumber())
	if capacityMgr != nil {
		s.SetOnRelease(capacityMgr.Release)
	}
	s.SetRoutingMetadata(evt.RouteEntryNumber(), evt.RouteServiceName(), evt.SourceGateway(), evt.RouteIngressStage(), evt.RouteType(), evt.RoutingConfigVersion())
	if s.EntryNumber == "" {
		s.SetRoutingMetadata(evt.DestNumber(), s.ServiceName, evt.SourceGateway(), s.IngressStage, s.RouteType, s.RoutingConfigVersion)
	}
	if !sessionMgr.AddIfUnderCapacity(ctx, s) {
		slog.Warn("Session capacity exceeded, rejecting inbound call", "fs_uuid", fsUUID, "event", evt.Name())
		s.Cancel()
		if err := eslClient.Kill(ctx, fsUUID); err != nil {
			slog.Error("Failed to kill over-capacity channel", "fs_uuid", fsUUID, "err", err)
		}
		return nil, false
	}
	saveSessionState(ctx, sessionMgr, s)
	metrics.ActiveCalls.Set(float64(sessionMgr.Count(ctx)))

	if synthesized {
		slog.Warn("Session synthesized from late event", "session_id", sessionID, "fs_uuid", fsUUID, "event", evt.Name())
	} else {
		slog.Info("Session created", "session_id", sessionID, "fs_uuid", fsUUID)
	}
	return s, true
}

func startIVRSession(ctx context.Context, sessionMgr session.Store, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config, s *session.SessionState) {
	if s == nil {
		return
	}
	if s.IvrEventCh != nil {
		slog.Debug("IVR already active for session", "session_id", s.SessionID)
		return
	}

	ivrMachine := ivr.NewMachine(s.SessionID, nil, ivr.Callbacks{
		OnRepeatMenu:  func() { slog.Info("IVR: menu repeat", "session", s.SessionID) },
		OnEnterAiChat: func() { slog.Info("IVR: entering AI chat", "session", s.SessionID) },
		OnTransfer: func() {
			slog.Info("IVR: transfer requested", "session", s.SessionID)
			if cfg.IVRTransferTarget != "" {
				confirmed, err := transferViaPolicy(s.Ctx, eslClient, gatewaySelector, handoffMgr, s, cfg.IVRTransferTarget)
				if err != nil {
					slog.Error("IVR transfer failed", "session", s.SessionID, "target", cfg.IVRTransferTarget, "err", err)
				} else {
					if confirmed {
						s.SetAIPaused(true)
						bridgeURL := fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort)
						notifyBridgeHold(bridgeURL, cfg.InternalAPISecret, "ai-pause", s.FSUUID)
						if released, releaseErr := session.ReleaseServiceOwnership(s.Ctx, sessionMgr, s); releaseErr != nil {
							slog.Error("Failed to persist service ownership release after confirmed IVR handoff", "session", s.SessionID, "err", releaseErr)
						} else if released {
							slog.Info("Released AI slot after confirmed IVR handoff", "session", s.SessionID)
						}
					} else {
						slog.Info("IVR transfer accepted; retaining AI slot until channel lifecycle confirms release", "session", s.SessionID)
					}
					slog.Info("IVR: transferred to agent", "session", s.SessionID, "target", cfg.IVRTransferTarget)
				}
			} else {
				slog.Warn("IVR: transfer target not configured (IVR_TRANSFER_TARGET env)", "session", s.SessionID)
			}
		},
		OnDisconnect: func() {
			slog.Info("IVR: disconnect requested", "session", s.SessionID)
			eslClient.Kill(s.Ctx, s.FSUUID)
		},
		OnForwardDtmf: func(digit string) {
			slog.Info("IVR: DTMF forwarded to AI", "session", s.SessionID, "digit", digit)
		},
	})

	s.IvrEventCh = make(chan any, 16)
	go func() {
		for {
			select {
			case <-s.Ctx.Done():
				return
			case evt, ok := <-s.IvrEventCh:
				if !ok {
					return
				}
				if ivrEvt, ok := evt.(ivr.IvrEvent); ok {
					select {
					case ivrMachine.EventCh <- ivrEvt:
					case <-s.Ctx.Done():
						return
					}
				}
			}
		}
	}()

	go ivrMachine.Run(s.Ctx)
	ivrMachine.EventCh <- ivr.IvrEvent{Type: ivr.ActivateMenuEvent}
	saveSessionState(ctx, sessionMgr, s)
}

func transferToAIStart(ctx context.Context, commander esl.Commander, s *session.SessionState) error {
	if commander == nil || s == nil {
		return fmt.Errorf("ai start transfer dependencies are nil")
	}
	if err := commander.Break(ctx, s.FSUUID); err != nil {
		slog.Warn("Failed to break media before AI start", "session_id", s.SessionID, "err", err)
	}
	return commander.Transfer(ctx, s.FSUUID, vbgwAIStartExtension)
}

func transferToExtensionSlot(ctx context.Context, commander esl.Commander, s *session.SessionState, extension string) error {
	if commander == nil || s == nil {
		return fmt.Errorf("extension slot transfer dependencies are nil")
	}
	extension = strings.TrimSpace(extension)
	if extension == "" {
		return fmt.Errorf("extension slot target missing")
	}
	if err := commander.Break(ctx, s.FSUUID); err != nil {
		slog.Warn("Failed to break media before extension slot transfer", "session_id", s.SessionID, "extension", extension, "err", err)
	}
	return commander.Transfer(ctx, s.FSUUID, extension)
}

func transferToQueueHold(ctx context.Context, commander esl.Commander, s *session.SessionState, announcement string) error {
	if commander == nil || s == nil {
		return fmt.Errorf("queue hold transfer dependencies are nil")
	}
	announcement = strings.TrimSpace(announcement)
	if announcement == "" {
		announcement = vbgwDefaultQueueAnnouncement
	}
	if err := commander.SetVar(ctx, s.FSUUID, "vbgw_queue_announcement", announcement); err != nil {
		return err
	}
	if err := commander.SetVar(ctx, s.FSUUID, "vbgw_queue_hold_music", vbgwDefaultQueueHoldMusic); err != nil {
		return err
	}
	if err := commander.Break(ctx, s.FSUUID); err != nil {
		slog.Warn("Failed to break media before queue hold transfer", "session_id", s.SessionID, "err", err)
	}
	return commander.Transfer(ctx, s.FSUUID, vbgwQueueHoldExtension)
}

func isCallcenterQueueTarget(target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	return strings.HasPrefix(target, "callcenter:")
}

func callcenterQueueRef(target string) string {
	const prefix = "callcenter:"
	target = strings.TrimSpace(target)
	if len(target) < len(prefix) {
		return ""
	}
	if !strings.EqualFold(target[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(target[len(prefix):])
}

func transferToCallcenterQueue(ctx context.Context, commander esl.Commander, s *session.SessionState, target string) error {
	if commander == nil || s == nil {
		return fmt.Errorf("callcenter transfer dependencies are nil")
	}
	queueRef := callcenterQueueRef(target)
	if queueRef == "" {
		return fmt.Errorf("callcenter target missing queue reference")
	}
	if err := commander.SetVar(ctx, s.FSUUID, "vbgw_human_queue", queueRef); err != nil {
		return err
	}
	if err := commander.Break(ctx, s.FSUUID); err != nil {
		slog.Warn("Failed to break media before callcenter transfer", "session_id", s.SessionID, "err", err)
	}
	return commander.Transfer(ctx, s.FSUUID, vbgwHumanCallcenterExt)
}

func tryActivateService(ctx context.Context, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config, s *session.SessionState, serviceName string, visited map[string]struct{}) bool {
	if s == nil {
		return false
	}
	if visited == nil {
		visited = make(map[string]struct{})
	}
	if serviceName != "" {
		if _, exists := visited[serviceName]; exists {
			slog.Warn("Overflow recursion detected", "session_id", s.SessionID, "service", serviceName)
			s.SetLifecycleState(session.StateOverflowed, "", "overflow recursion detected", time.Time{}, time.Time{})
			saveSessionState(ctx, sessionMgr, s)
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill channel after overflow recursion", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		visited[serviceName] = struct{}{}
	}

	if serviceName != "" && serviceName != s.ServiceName {
		s.SetRoutingMetadata(s.EntryNumber, serviceName, s.SourceGateway, s.IngressStage, s.RouteType, s.RoutingConfigVersion)
	}

	decision := capacity.Decision{
		Allowed:     true,
		Configured:  false,
		ServiceName: serviceName,
	}
	if capacityMgr != nil {
		decision = capacityMgr.AdmitRequest(capacity.AdmitRequest{
			SessionID:   s.SessionID,
			ServiceName: serviceName,
			CallerID:    s.CallerID,
		})
	}

	if decision.Configured {
		s.SetCapacityMetadata(decision.SlotID, session.AllocationLeased, "", "")
		if !decision.Allowed {
			return handleOverflowDecision(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, s, decision, visited)
		}
	}

	if overflowMgr != nil {
		overflowMgr.Remove(s.SessionID)
	}
	s.SetLifecycleState(session.StateActive, "", "", time.Time{}, time.Time{})
	saveSessionState(ctx, sessionMgr, s)

	switch decision.Backend {
	case routing.BackendSIPExtension:
		s.SetAIPaused(true)
		notifyBridgeHold(fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort), cfg.InternalAPISecret, "ai-pause", s.FSUUID)
		if err := transferToExtensionSlot(ctx, eslClient, s, decision.Extension); err != nil {
			slog.Error("Failed to transfer admitted call into SIP extension slot", "session_id", s.SessionID, "service", serviceName, "extension", decision.Extension, "err", err)
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to release service ownership after extension slot transfer failure", "session_id", s.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released slot after extension slot transfer failure", "session_id", s.SessionID)
			}
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill channel after extension slot transfer failure", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		return true
	default:
		s.SetAIPaused(false)
		if err := transferToAIStart(ctx, eslClient, s); err != nil {
			slog.Error("Failed to transfer admitted call into AI media path", "session_id", s.SessionID, "service", serviceName, "err", err)
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to release service ownership after AI start failure", "session_id", s.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released AI slot after AI start failure", "session_id", s.SessionID)
			}
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill channel after AI start failure", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		startIVRSession(ctx, sessionMgr, gatewaySelector, handoffMgr, eslClient, cfg, s)
		return true
	}
}

func handleOverflowDecision(ctx context.Context, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config, s *session.SessionState, decision capacity.Decision, visited map[string]struct{}) bool {
	s.SetCapacityMetadata("", session.AllocationOverflowed, decision.OverflowPolicy, overflowTargetFromDecision(decision))
	s.SetLifecycleState(session.StateOverflowed, "", "service capacity exceeded", time.Time{}, time.Time{})
	saveSessionState(ctx, sessionMgr, s)

	slog.Warn("Service capacity exceeded",
		"session_id", s.SessionID,
		"service", decision.ServiceName,
		"overflow_policy", decision.OverflowPolicy,
		"transfer_target", decision.TransferTarget,
		"overflow_service", decision.OverflowService,
	)

	switch decision.OverflowPolicy {
	case routing.OverflowBusy:
		if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
			slog.Error("Failed to kill over-capacity channel", "session_id", s.SessionID, "err", err)
		}
		return false

	case routing.OverflowDirectTransfer:
		if decision.TransferTarget == "" {
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill over-capacity channel without direct transfer target", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		confirmed, err := transferViaPolicy(s.Ctx, eslClient, gatewaySelector, handoffMgr, s, decision.TransferTarget)
		if err != nil {
			slog.Error("Overflow direct transfer failed", "session_id", s.SessionID, "target", decision.TransferTarget, "err", err)
			if killErr := eslClient.Kill(s.Ctx, s.FSUUID); killErr != nil {
				slog.Error("Failed to kill over-capacity channel after transfer failure", "session_id", s.SessionID, "err", killErr)
			}
			return false
		}
		if confirmed {
			s.SetLifecycleState(session.StateTransferring, "", "overflow direct transfer confirmed", time.Time{}, time.Time{})
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to release service ownership after confirmed overflow direct transfer", "session_id", s.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released AI slot after confirmed overflow direct transfer", "session_id", s.SessionID)
			}
		} else {
			s.SetLifecycleState(session.StateTransferring, "", "overflow direct transfer accepted", time.Time{}, time.Time{})
		}
		saveSessionState(ctx, sessionMgr, s)
		return false

	case routing.OverflowFallbackHuman:
		if !cfg.HumanFallbackEnabled || decision.TransferTarget == "" {
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill over-capacity channel without enabled human fallback", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		confirmed, err := transferViaPolicy(s.Ctx, eslClient, gatewaySelector, handoffMgr, s, decision.TransferTarget)
		if err != nil {
			slog.Error("Overflow human fallback failed", "session_id", s.SessionID, "target", decision.TransferTarget, "err", err)
			if killErr := eslClient.Kill(s.Ctx, s.FSUUID); killErr != nil {
				slog.Error("Failed to kill over-capacity channel after human fallback failure", "session_id", s.SessionID, "err", killErr)
			}
			return false
		}
		metrics.HumanFallbackTotal.WithLabelValues(decision.ServiceName, decision.TransferTarget).Inc()
		s.SetAIPaused(true)
		notifyBridgeHold(fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort), cfg.InternalAPISecret, "ai-pause", s.FSUUID)
		if confirmed {
			s.SetLifecycleState(session.StateTransferring, "", "overflow human fallback confirmed", time.Time{}, time.Time{})
			if released, releaseErr := session.ReleaseServiceOwnership(ctx, sessionMgr, s); releaseErr != nil {
				slog.Error("Failed to release service ownership after confirmed overflow human fallback", "session_id", s.SessionID, "err", releaseErr)
			} else if released {
				slog.Info("Released AI slot after confirmed overflow human fallback", "session_id", s.SessionID)
			}
		} else {
			s.SetLifecycleState(session.StateTransferring, "", "overflow human fallback accepted", time.Time{}, time.Time{})
		}
		saveSessionState(ctx, sessionMgr, s)
		return false

	case routing.OverflowFailoverSvc:
		if decision.OverflowService == "" {
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill over-capacity channel without failover service", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		return tryActivateService(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, s, decision.OverflowService, visited)

	case routing.OverflowQueue:
		if overflowMgr == nil {
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill over-capacity channel without overflow manager", "session_id", s.SessionID, "err", err)
			}
			return false
		}
		now := time.Now()
		timeoutAt := now.Add(time.Duration(decision.QueueMaxWaitSec) * time.Second)
		queued := overflowMgr.Enqueue(overflow.QueueEntry{
			SessionID:         s.SessionID,
			ServiceName:       decision.ServiceName,
			OverflowPolicy:    decision.OverflowPolicy,
			TransferTarget:    decision.TransferTarget,
			OverflowService:   decision.OverflowService,
			QueueAnnouncement: decision.QueueAnnouncement,
			QueueOnTimeout:    decision.QueueOnTimeout,
			EnqueuedAt:        now,
			TimeoutAt:         timeoutAt,
		})
		if !queued {
			slog.Warn("Session already queued; skipping duplicate enqueue", "session_id", s.SessionID, "service", decision.ServiceName)
			return false
		}
		s.SetAIPaused(true)
		notifyBridgeHold(fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort), cfg.InternalAPISecret, "ai-pause", s.FSUUID)
		s.SetLifecycleState(session.StateQueued, decision.ServiceName, "service capacity exceeded", now, timeoutAt)
		saveSessionState(ctx, sessionMgr, s)
		if err := transferToQueueHold(ctx, eslClient, s, decision.QueueAnnouncement); err != nil {
			slog.Error("Failed to transfer queued call into hold media path", "session_id", s.SessionID, "service", decision.ServiceName, "err", err)
			overflowMgr.Remove(s.SessionID)
			metrics.QueueAbandonTotal.WithLabelValues(decision.ServiceName, "queue_hold_transfer_failed").Inc()
			if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
				slog.Error("Failed to kill queued channel after hold transfer failure", "session_id", s.SessionID, "err", err)
			}
		}
		return false
	}

	if err := eslClient.Kill(s.Ctx, s.FSUUID); err != nil {
		slog.Error("Failed to kill over-capacity channel after unsupported overflow policy", "session_id", s.SessionID, "policy", decision.OverflowPolicy, "err", err)
	}
	return false
}

func processOverflowQueuesOnce(ctx context.Context, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config) {
	if overflowMgr == nil {
		return
	}

	now := time.Now()
	for _, serviceName := range overflowMgr.ServiceNames() {
		for {
			entry, ok := overflowMgr.ClaimNext(serviceName, now)
			if !ok {
				break
			}

			s, exists := sessionMgr.Get(ctx, entry.SessionID)
			if !exists {
				overflowMgr.Remove(entry.SessionID)
				metrics.QueueAbandonTotal.WithLabelValues(serviceName, "missing_session").Inc()
				continue
			}

			if !entry.TimeoutAt.IsZero() && !entry.TimeoutAt.After(now) {
				overflowMgr.Remove(entry.SessionID)
				metrics.QueueAbandonTotal.WithLabelValues(serviceName, "timeout").Inc()
				timeoutDecision := capacity.Decision{
					Allowed:           false,
					Configured:        true,
					ServiceName:       entry.ServiceName,
					OverflowPolicy:    entry.QueueOnTimeout,
					TransferTarget:    entry.TransferTarget,
					OverflowService:   entry.OverflowService,
					QueueOnTimeout:    entry.QueueOnTimeout,
					QueueAnnouncement: entry.QueueAnnouncement,
				}
				if timeoutDecision.OverflowPolicy == "" {
					timeoutDecision.OverflowPolicy = routing.OverflowBusy
				}
				handleOverflowDecision(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, s, timeoutDecision, map[string]struct{}{serviceName: struct{}{}})
				continue
			}

			if capacityMgr != nil {
				if decision := capacityMgr.Preview(serviceName); decision.Configured && !decision.Allowed {
					overflowMgr.ReleaseClaim(entry.SessionID)
					break
				}
			}

			overflowMgr.Remove(entry.SessionID)
			if activated := tryActivateService(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg, s, serviceName, map[string]struct{}{}); !activated {
				overflowMgr.ReleaseClaim(entry.SessionID)
				break
			}
		}
	}
}

func runOverflowDispatcher(ctx context.Context, tick time.Duration, sessionMgr session.Store, capacityMgr *capacity.Manager, overflowMgr *overflow.Manager, gatewaySelector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, eslClient esl.Commander, cfg *config.Config) {
	if overflowMgr == nil {
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			processOverflowQueuesOnce(ctx, sessionMgr, capacityMgr, overflowMgr, gatewaySelector, handoffMgr, eslClient, cfg)
		}
	}
}

func runSlotRegistrationRefresher(ctx context.Context, tick time.Duration, registry *slots.Registry, eslClient esl.Commander) {
	if registry == nil || eslClient == nil {
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := registry.RefreshFromESL(ctx, eslClient); err != nil {
				slog.Warn("Failed to refresh SIP registration inventory", "err", err)
			}
		}
	}
}

func runLeaseRenewalLoop(ctx context.Context, tick time.Duration, capacityMgr *capacity.Manager) {
	if capacityMgr == nil {
		return
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			capacityMgr.RenewLeases()
		}
	}
}

func overflowTargetFromDecision(decision capacity.Decision) string {
	if decision.TransferTarget != "" {
		return decision.TransferTarget
	}
	return decision.OverflowService
}

func onDtmf(ctx context.Context, evt *esl.Event, sessionMgr session.Store) {
	s, ok := sessionMgr.GetByFSUUID(ctx, evt.UUID())
	if !ok {
		return
	}
	digit := evt.DtmfDigit()
	slog.Info("DTMF received", "session_id", s.SessionID, "digit", digit)

	// P-02: Forward DTMF to IVR state machine
	// T-03: Use session context to avoid sending to closed channel after Release()
	if s.IvrEventCh != nil {
		select {
		case <-s.Ctx.Done():
			slog.Warn("DTMF dropped: session context cancelled", "session", s.SessionID, "digit", digit)
		case s.IvrEventCh <- ivr.IvrEvent{Type: ivr.DtmfEvent, Digit: digit}:
		default:
			slog.Warn("IVR event channel full, DTMF dropped", "session", s.SessionID, "digit", digit)
		}
	}
}

func onChannelHangup(ctx context.Context, evt *esl.Event, sessionMgr session.Store, overflowMgr *overflow.Manager, handoffMgr *interconnect.HandoffManager, cfg *config.Config) {
	if handoffMgr != nil && handoffMgr.ResolveFailed(evt.UUID(), evt.SourceGateway(), evt.HangupCause(), evt.SipTermStatus()) {
		slog.Info("Resolved handoff failure", "fs_uuid", evt.UUID(), "cause", evt.HangupCause(), "sip_code", evt.SipTermStatus())
		return
	}

	s, ok := sessionMgr.GetByFSUUID(ctx, evt.UUID())
	if !ok {
		return
	}

	s.SetRoutingMetadata(evt.RouteEntryNumber(), evt.RouteServiceName(), evt.SourceGateway(), evt.RouteIngressStage(), evt.RouteType(), evt.RoutingConfigVersion())
	if overflowPolicy := evt.RouteOverflowPolicy(); overflowPolicy != "" && s.AllocationState == "" {
		s.SetCapacityMetadata("", session.AllocationOverflowed, overflowPolicy, evt.RouteOverflowTarget())
		if s.ServiceName != "" {
			metrics.OverflowTotal.WithLabelValues(s.ServiceName, overflowPolicy).Inc()
		}
	}

	hangupCause := evt.HangupCause()
	sipCode := evt.SipTermStatus()

	s.SetHangupAt(time.Now())
	s.SetLifecycleState(session.StateEnded, "", "", time.Time{}, time.Time{})
	if overflowMgr != nil && overflowMgr.Remove(s.SessionID) {
		metrics.QueueAbandonTotal.WithLabelValues(s.ServiceName, "hangup").Inc()
	}

	// A-03: Asynchronous CDR Webhook Delivery (Fire & Forget)
	cdr.SendAsync(cfg, s, hangupCause)

	// Standard LogHangup
	cdr.LogHangup(s, hangupCause)

	sessionMgr.Release(ctx, s.SessionID)
	metrics.ActiveCalls.Set(float64(sessionMgr.Count(ctx)))

	// S-01: SIP hangup cause + code breakdown metric
	if sipCode == "" {
		sipCode = "unknown"
	}
	if hangupCause == "" {
		hangupCause = "UNKNOWN"
	}
	metrics.CallHangupTotal.WithLabelValues(hangupCause, sipCode).Inc()

	slog.Info("CHANNEL_HANGUP", "session_id", s.SessionID, "cause", hangupCause, "sip_code", sipCode)
}

// P-12: Pause AI streaming when PBX puts call on hold (Re-INVITE sendonly)
func onChannelHold(ctx context.Context, evt *esl.Event, sessionMgr session.Store, cfg *config.Config) {
	s, ok := sessionMgr.GetByFSUUID(ctx, evt.UUID())
	if !ok {
		return
	}
	s.SetAIPaused(true)
	slog.Info("CHANNEL_HOLD — AI paused (PBX hold)", "session_id", s.SessionID)

	// Notify Bridge to pause gRPC send
	bridgeURL := fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort)
	notifyBridgeHold(bridgeURL, cfg.InternalAPISecret, "ai-pause", s.FSUUID)
}

// P-12: Resume AI streaming when PBX takes call off hold
func onChannelUnhold(ctx context.Context, evt *esl.Event, sessionMgr session.Store, cfg *config.Config) {
	s, ok := sessionMgr.GetByFSUUID(ctx, evt.UUID())
	if !ok {
		return
	}
	s.SetAIPaused(false)
	slog.Info("CHANNEL_UNHOLD — AI resumed (PBX resume)", "session_id", s.SessionID)

	bridgeURL := fmt.Sprintf("http://%s:%d", cfg.BridgeHost, cfg.BridgeInternalPort)
	notifyBridgeHold(bridgeURL, cfg.InternalAPISecret, "ai-resume", s.FSUUID)
}

func notifyBridgeHold(bridgeURL, internalSecret, action, uuid string) {
	url := fmt.Sprintf("%s/internal/%s/%s", bridgeURL, action, uuid)
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	req.Header.Set("Content-Type", "application/json")
	if internalSecret != "" {
		req.Header.Set("X-Internal-Secret", internalSecret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Error("Bridge hold notification failed", "action", action, "uuid", uuid, "err", err)
		return
	}
	resp.Body.Close()
}

func probeGateway(ctx context.Context, eslClient *esl.Client, gatewayStore *interconnect.Store, gatewayName string, registerMode bool, freshnessTTL time.Duration) bool {
	if gatewayName == "" || gatewayStore == nil {
		return false
	}

	now := time.Now()
	resp := ""
	if !eslClient.IsConnected() {
		resp = "-ERR ESL disconnected"
		metrics.GatewayProbeFailuresTotal.WithLabelValues(gatewayName).Inc()
	} else {
		sofiaCtx, sofiaCancel := context.WithTimeout(ctx, 5*time.Second)
		defer sofiaCancel()

		resultCh := make(chan string, 1)
		go func() {
			response, err := eslClient.SendAPI(sofiaCtx, fmt.Sprintf("sofia status gateway %s", gatewayName))
			if err != nil {
				resultCh <- fmt.Sprintf("-ERR %v", err)
				return
			}
			resultCh <- response
		}()

		select {
		case response := <-resultCh:
			resp = response
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(response)), "-ERR") {
				metrics.GatewayProbeFailuresTotal.WithLabelValues(gatewayName).Inc()
			}
		case <-sofiaCtx.Done():
			if ctx.Err() != nil {
				return false
			}
			resp = "-ERR probe timeout"
			metrics.GatewayProbeFailuresTotal.WithLabelValues(gatewayName).Inc()
			slog.Warn("Sofia status check timeout (5s)", "gateway", gatewayName)
		}
	}

	state := interconnect.BuildGatewayState(gatewayName, registerMode, resp, "sofia_status", now, freshnessTTL)
	gatewayStore.Update(state)
	if state.HealthClass != interconnect.HealthHealthy && strings.TrimSpace(resp) != "" {
		slog.Warn("Gateway health degraded", "gateway", gatewayName, "mode", state.Mode, "class", state.HealthClass, "status", strings.TrimSpace(resp))
	}
	return state.HealthClass == interconnect.HealthHealthy
}

func refreshGatewaySnapshots(ctx context.Context, eslClient *esl.Client, gatewayStore *interconnect.Store, cfg *config.Config, freshnessTTL time.Duration) bool {
	primaryHealthy := probeGateway(ctx, eslClient, gatewayStore, cfg.PBXMainGateway, cfg.PBXMainRegister, freshnessTTL)
	if cfg.PBXStandbyEnabled {
		probeGateway(ctx, eslClient, gatewayStore, cfg.PBXStandbyGateway, cfg.PBXStandbyRegister, freshnessTTL)
	}
	if primaryHealthy {
		metrics.SipRegistered.Set(1)
	} else {
		metrics.SipRegistered.Set(0)
	}
	return primaryHealthy
}

func transferViaPolicy(ctx context.Context, commander esl.Commander, selector *interconnect.Selector, handoffMgr *interconnect.HandoffManager, s *session.SessionState, target string) (bool, error) {
	if isCallcenterQueueTarget(target) {
		return true, transferToCallcenterQueue(ctx, commander, s, target)
	}
	if selector != nil && handoffMgr != nil && interconnect.ShouldUseGatewayTransfer(target) {
		outcome, err := interconnect.ExecuteGatewayHandoff(ctx, commander, selector, handoffMgr, s.FSUUID, s.CallerID, target)
		if err != nil {
			return false, err
		}
		return outcome.Confirmed, nil
	}
	return false, commander.Transfer(ctx, s.FSUUID, target)
}

func runtimeConfig(rt *routing.Runtime) *routing.Config {
	if rt == nil {
		return nil
	}
	return rt.Config
}

func saveSessionState(ctx context.Context, sessionMgr session.Store, s *session.SessionState) {
	if s == nil {
		return
	}
	if err := sessionMgr.SaveSession(ctx, s); err != nil {
		slog.Error("Failed to persist session state", "session_id", s.SessionID, "err", err)
	}
}

func setupLogging(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})))
}

func validateProdConfig(cfg *config.Config) {
	failed := false

	if len(cfg.AdminAPIKey) < 32 {
		slog.Error("Production: ADMIN_API_KEY must be at least 32 characters", "current_len", len(cfg.AdminAPIKey))
		failed = true
	}
	if cfg.AdminAPIKey == "changeme-admin-key" || strings.Contains(cfg.AdminAPIKey, "changeme") {
		slog.Error("Production: ADMIN_API_KEY must not contain default value 'changeme'")
		failed = true
	}
	if len(cfg.JWTSecret) < 32 {
		slog.Error("Production: JWT_SECRET must be at least 32 characters", "current_len", len(cfg.JWTSecret))
		failed = true
	}
	if strings.Contains(strings.ToLower(cfg.JWTSecret), "changeme") || strings.Contains(strings.ToLower(cfg.JWTSecret), "dev-") {
		slog.Error("Production: JWT_SECRET must not contain placeholder or dev values")
		failed = true
	}
	if cfg.ESLPassword == "ClueCon" || cfg.ESLPassword == "" {
		slog.Error("Production: ESL_PASSWORD must not be default 'ClueCon' or empty")
		failed = true
	}
	if len(cfg.AdminControlKey) < 32 {
		slog.Error("Production: ADMIN_CONTROL_KEY must be at least 32 characters", "current_len", len(cfg.AdminControlKey))
		failed = true
	}
	if strings.Contains(strings.ToLower(cfg.AdminControlKey), "changeme") || strings.Contains(strings.ToLower(cfg.AdminControlKey), "placeholder") {
		slog.Error("Production: ADMIN_CONTROL_KEY must not contain placeholder values")
		failed = true
	}
	if len(cfg.InternalAPISecret) < 32 {
		slog.Error("Production: INTERNAL_API_SECRET must be at least 32 characters", "current_len", len(cfg.InternalAPISecret))
		failed = true
	}
	if strings.Contains(strings.ToLower(cfg.InternalAPISecret), "changeme") || strings.Contains(strings.ToLower(cfg.InternalAPISecret), "placeholder") {
		slog.Error("Production: INTERNAL_API_SECRET must not contain placeholder values")
		failed = true
	}

	if failed {
		slog.Error("Production security validation FAILED — refusing to start")
		os.Exit(1)
	}
	slog.Info("Production security validation passed")
}
