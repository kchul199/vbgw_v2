/**
 * @file middleware.go
 * @description HTTP 미들웨어 — ConstantTimeCompare 인증 + Token Bucket 속도 제한
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-07 | [Implementer] | 최초 생성 | OWASP 준수 인증 + rate limit
 * ─────────────────────────────────────────
 */

package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"vbgw-orchestrator/internal/metrics"

	"golang.org/x/time/rate"
)

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

// RateLimitMiddleware applies a token bucket rate limiter.
func RateLimitMiddleware(rps float64, burst int) func(http.Handler) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
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

// LoopbackOnlyMiddleware restricts access to loopback (127.0.0.1) clients only.
// Used for internal endpoints (Bridge → Orchestrator) that must not be externally accessible.
func LoopbackOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteIP := r.RemoteAddr
		// Extract IP from "ip:port" format
		if idx := strings.LastIndex(remoteIP, ":"); idx >= 0 {
			remoteIP = remoteIP[:idx]
		}
		// Remove brackets for IPv6
		remoteIP = strings.Trim(remoteIP, "[]")

		if remoteIP != "127.0.0.1" && remoteIP != "::1" && remoteIP != "localhost" {
			http.Error(w, `{"error":"forbidden: internal only"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

