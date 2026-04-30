/**
 * @file server.go
 * @description WebSocket 서버 — HTTP 업그레이드 + per-UUID 라우팅
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | /audio/{uuid} WS 엔드포인트
 * v1.0.1 | 2026-04-09 | [Implementer] | T-18 | DTMF 에러 반환, shutdown 엔드포인트
 * ─────────────────────────────────────────
 */

package ws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	"vbgw-bridge/internal/barge"
	grpcclient "vbgw-bridge/internal/grpc"
	"vbgw-bridge/internal/vad"
)

// Server manages WebSocket connections from FreeSWITCH mod_audio_fork.
type Server struct {
	ctx      context.Context
	sessions sync.Map // map[string]*Session (key: uuid)

	vadEngine       *vad.Engine
	grpcClientPool  *grpcclient.Pool
	bargeController *barge.Controller

	// Buffer pool for audio chunks (standard 32ms/20ms sizes)
	bufferPool sync.Pool

	upgrader websocket.Upgrader
}

// NewServer creates a WS server with the given parent context.
func NewServer(ctx context.Context, vadEngine *vad.Engine, grpcPool *grpcclient.Pool, bargeCtrl *barge.Controller, allowedOrigins []string) *Server {
	return &Server{
		ctx:             ctx,
		vadEngine:       vadEngine,
		grpcClientPool:  grpcPool,
		bargeController: bargeCtrl,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  8192,
			WriteBufferSize: 8192,
			CheckOrigin:     makeOriginChecker(allowedOrigins),
		},
		bufferPool: sync.Pool{
			New: func() any {
				// Allocate 2KB to cover G.711/PCM16 frames comfortably
				return make([]byte, 2048)
			},
		},
	}
}

func makeOriginChecker(allowedOrigins []string) func(r *http.Request) bool {
	allowedOriginSet := make(map[string]struct{}, len(allowedOrigins))
	allowedHostSet := map[string]struct{}{
		"localhost":       {},
		"127.0.0.1":       {},
		"::1":             {},
		"bridge":          {},
		"freeswitch":      {},
		"vbgw-bridge":     {},
		"vbgw-freeswitch": {},
	}

	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(strings.ToLower(origin))
		if origin == "" {
			continue
		}
		if parsed, err := url.Parse(origin); err == nil && parsed.Host != "" && parsed.Scheme != "" {
			allowedOriginSet[parsed.Scheme+"://"+parsed.Host] = struct{}{}
			allowedHostSet[strings.ToLower(parsed.Hostname())] = struct{}{}
			continue
		}
		allowedHostSet[origin] = struct{}{}
	}

	return func(r *http.Request) bool {
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			// FreeSWITCH mod_audio_fork connects server-to-server and does not need browser origins.
			return true
		}

		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || parsed.Scheme == "" {
			return false
		}

		normalizedOrigin := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
		if _, ok := allowedOriginSet[normalizedOrigin]; ok {
			return true
		}
		_, ok := allowedHostSet[strings.ToLower(parsed.Hostname())]
		return ok
	}
}

func (s *Server) sessionCount() int {
	count := 0
	s.sessions.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// HandleAudio handles the WS upgrade for /audio/{uuid}.
func (s *Server) HandleAudio(w http.ResponseWriter, r *http.Request) {
	// Extract UUID from path: /audio/{uuid}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.Error(w, "missing uuid", http.StatusBadRequest)
		return
	}
	uuid := parts[len(parts)-1]

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS upgrade failed", "uuid", uuid, "err", err)
		return
	}

	slog.Info("WS connection established", "uuid", uuid)

	vadInst := s.vadEngine.NewInstance()
	sess := NewSession(s.ctx, uuid, conn, vadInst, s.grpcClientPool, s.bargeController, &s.bufferPool)
	s.sessions.Store(uuid, sess)

	go func() {
		sess.Run()
		s.sessions.Delete(uuid)
		slog.Info("WS session ended", "uuid", uuid)
	}()
}

// GetSession returns an active session by UUID.
func (s *Server) GetSession(uuid string) (*Session, bool) {
	v, ok := s.sessions.Load(uuid)
	if !ok {
		return nil, false
	}
	return v.(*Session), true
}

// PauseAI pauses the AI gRPC send for a session (bridge mode).
func (s *Server) PauseAI(uuid string) {
	if sess, ok := s.GetSession(uuid); ok {
		sess.SetAIPaused(true)
		slog.Info("AI gRPC send PAUSED", "uuid", uuid)
	}
}

// ResumeAI resumes the AI gRPC send for a session.
func (s *Server) ResumeAI(uuid string) {
	if sess, ok := s.GetSession(uuid); ok {
		sess.SetAIPaused(false)
		slog.Info("AI gRPC send RESUMED", "uuid", uuid)
	}
}

// InternalHandler creates an HTTP handler for internal Bridge API.
func (s *Server) InternalHandler(internalSecret string) http.Handler {
	mux := http.NewServeMux()

	requireSecret := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if internalSecret == "" || subtle.ConstantTimeCompare([]byte(internalSecret), []byte(r.Header.Get("X-Internal-Secret"))) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next(w, r)
		}
	}

	mux.HandleFunc("/internal/health", requireSecret(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status":          "healthy",
			"grpc_connected":  s.grpcClientPool != nil && s.grpcClientPool.IsConnected(),
			"active_sessions": s.sessionCount(),
		}
		if connected, _ := resp["grpc_connected"].(bool); !connected {
			resp["status"] = "degraded"
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))

	mux.HandleFunc("/internal/ai-pause/", requireSecret(func(w http.ResponseWriter, r *http.Request) {
		uuid := extractUUID(r.URL.Path, "/internal/ai-pause/")
		s.PauseAI(uuid)
		w.WriteHeader(http.StatusOK)
	}))

	mux.HandleFunc("/internal/ai-resume/", requireSecret(func(w http.ResponseWriter, r *http.Request) {
		uuid := extractUUID(r.URL.Path, "/internal/ai-resume/")
		s.ResumeAI(uuid)
		w.WriteHeader(http.StatusOK)
	}))

	// T-19: Shutdown notification from Orchestrator
	mux.HandleFunc("/internal/shutdown", requireSecret(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("Shutdown notification received from Orchestrator")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"shutting_down"}`))
	}))

	mux.HandleFunc("/internal/dtmf/", requireSecret(func(w http.ResponseWriter, r *http.Request) {
		uuid := extractUUID(r.URL.Path, "/internal/dtmf/")

		var body struct {
			Digit string `json:"digit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Digit == "" {
			http.Error(w, `{"error":"digit required"}`, http.StatusBadRequest)
			return
		}

		if sess, ok := s.GetSession(uuid); ok {
			// T-18: Return error to caller if DTMF forwarding fails
			if err := sess.ForwardDtmf(body.Digit); err != nil {
				http.Error(w, `{"error":"dtmf forward failed"}`, http.StatusInternalServerError)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))

	return mux
}

func extractUUID(path, prefix string) string {
	return strings.TrimPrefix(path, prefix)
}
