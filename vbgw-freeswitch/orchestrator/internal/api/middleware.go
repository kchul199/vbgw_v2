/**
 * @file middleware.go
 * @description HTTP 미들웨어 — CORS + ConstantTimeCompare 인증 + Token Bucket 속도 제한
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | OWASP 준수 인증 + rate limit
 * v1.1.0 | 2026-04-09 | [Implementer] | T-26 | IP별 rate limiter + 전역 limiter 병행
 * v1.2.0 | 2026-05-11 | [Implementer] | Portal | CORS 미들웨어 추가
 * ─────────────────────────────────────────
 */

package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"vbgw-orchestrator/internal/metrics"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"
)

var tracer = otel.Tracer("vbgw-api")

// AuthMiddleware validates the X-Admin-Key header using constant-time comparison.
func AuthMiddleware(expectedKey string) func(http.Handler) http.Handler {
	expected := []byte(expectedKey)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := []byte(r.Header.Get("X-Admin-Key"))
			if subtle.ConstantTimeCompare(expected, provided) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func SharedSecretMiddleware(expectedSecret string) func(http.Handler) http.Handler {
	expected := []byte(expectedSecret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(expected) == 0 {
				http.Error(w, `{"error":"internal secret not configured"}`, http.StatusServiceUnavailable)
				return
			}
			provided := []byte(r.Header.Get("X-Internal-Secret"))
			if subtle.ConstantTimeCompare(expected, provided) != 1 {
				http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitMiddleware applies per-IP + global token bucket rate limiters.
// T-26: Per-IP limiter prevents a single client from exhausting the global budget.
func RateLimitMiddleware(rps float64, burst int) func(http.Handler) http.Handler {
	globalLimiter := rate.NewLimiter(rate.Limit(rps), burst)
	var ipLimiters sync.Map // map[string]*rate.Limiter

	// Per-IP: each IP gets rps/2 rate, burst/2 capacity
	perIPRate := rate.Limit(rps / 2)
	perIPBurst := burst / 2
	if perIPBurst < 1 {
		perIPBurst = 1
	}

	getIPLimiter := func(ip string) *rate.Limiter {
		v, ok := ipLimiters.Load(ip)
		if ok {
			return v.(*rate.Limiter)
		}
		l := rate.NewLimiter(perIPRate, perIPBurst)
		actual, _ := ipLimiters.LoadOrStore(ip, l)
		return actual.(*rate.Limiter)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := r.RemoteAddr
			if idx := strings.LastIndex(ip, ":"); idx >= 0 {
				ip = ip[:idx]
			}
			ip = strings.Trim(ip, "[]")

			if !globalLimiter.Allow() || !getIPLimiter(ip).Allow() {
				metrics.ApiRateLimited.Inc()
				w.Header().Set("Retry-After", "1")
				http.Error(w, `{"error":"too many requests"}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MetricsMiddleware records request latency per endpoint.
func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		duration := time.Since(start).Seconds()
		metrics.ApiLatency.WithLabelValues(
			r.Method,
			r.URL.Path,
			fmt.Sprintf("%d", rw.statusCode),
		).Observe(duration)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
	}
	rw.ResponseWriter.WriteHeader(code)
}

var dockerBridgePrefix = netip.MustParsePrefix("172.16.0.0/12")

// LoopbackOnlyMiddleware restricts access to loopback and Docker bridge clients.
// Used for internal endpoints (Bridge/FreeSWITCH → Orchestrator) that must not be externally accessible.
func LoopbackOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteIP := r.RemoteAddr
		// Extract IP from "ip:port" format
		if idx := strings.LastIndex(remoteIP, ":"); idx >= 0 {
			remoteIP = remoteIP[:idx]
		}
		// Remove brackets for IPv6
		remoteIP = strings.Trim(remoteIP, "[]")

		if remoteIP == "127.0.0.1" || remoteIP == "::1" || remoteIP == "localhost" {
			next.ServeHTTP(w, r)
			return
		}

		addr, err := netip.ParseAddr(remoteIP)
		if err == nil && dockerBridgePrefix.Contains(addr) {
			next.ServeHTTP(w, r)
			return
		}

		if remoteIP != "127.0.0.1" && remoteIP != "::1" && remoteIP != "localhost" {
			http.Error(w, `{"error":"forbidden: internal only"}`, http.StatusForbidden)
			return
		}
	})
}

// TracingMiddleware injects OpenTelemetry span into the request context.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

		spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)
		ctx, span := tracer.Start(ctx, spanName,
			trace.WithAttributes(
				semconv.HTTPMethod(r.Method),
				semconv.HTTPTarget(r.URL.Path),
				semconv.HTTPURL(r.URL.String()),
				semconv.HTTPUserAgent(r.UserAgent()),
				attribute.String("http.remote_addr", r.RemoteAddr),
			),
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Update request with new context
		r = r.WithContext(ctx)

		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)

		span.SetAttributes(semconv.HTTPStatusCode(rw.statusCode))
		if rw.statusCode >= 400 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", rw.statusCode))
		}
	})
}

// CORSMiddleware handles Cross-Origin Resource Sharing for the operations portal.
// Processes OPTIONS preflight requests and sets appropriate CORS headers.
func CORSMiddleware(allowedOrigins string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			if allowedOrigins == "*" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				allowed = true
			} else {
				for _, ao := range strings.Split(allowedOrigins, ",") {
					ao = strings.TrimSpace(ao)
					if ao != "" && ao == origin {
						w.Header().Set("Access-Control-Allow-Origin", origin)
						allowed = true
						break
					}
				}
			}

			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Admin-Key, X-Internal-Secret")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Max-Age", "3600")
			w.Header().Set("Vary", "Origin")

			// Handle preflight
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

