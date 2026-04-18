/**
 * @file webhook.go
 * @description 백오피스 연동을 위한 CDR Webhook 비동기 전송 모듈 (HMAC 지원)
 */

package cdr

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"vbgw-orchestrator/internal/config"
	"vbgw-orchestrator/internal/session"
)

type WebhookPayload struct {
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	FSUUID    string `json:"fs_uuid"`
	CallerID  string `json:"caller_id"`
	DestNum   string `json:"dest_num"`
	Duration  int    `json:"duration_sec"`
	Cause     string `json:"hangup_cause"`
	Timestamp string `json:"timestamp"`
}

var httpCli = &http.Client{Timeout: 5 * time.Second}

// SendAsync pushes the Call Detail Record to an external CRM.
// It is fully non-blocking and executes in a separate goroutine.
func SendAsync(cfg *config.Config, s *session.SessionState, cause string) {
	if cfg.CDRWebhookURL == "" {
		return
	}

	// Calculate duration in seconds
	dur := 0
	if !s.AnsweredAt.IsZero() {
		hangup := s.HangupAt
		if hangup.IsZero() {
			hangup = time.Now()
		}
		dur = int(hangup.Sub(s.AnsweredAt).Seconds())
	}

	payload := WebhookPayload{
		SessionID: s.SessionID,
		NodeID:    s.NodeID,
		FSUUID:    s.FSUUID,
		CallerID:  s.CallerID,
		DestNum:   s.DestNum,
		Duration:  dur,
		Cause:     cause,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	go func() {
		body, _ := json.Marshal(payload)
		req, err := http.NewRequest("POST", cfg.CDRWebhookURL, bytes.NewBuffer(body))
		if err != nil {
			slog.Error("Failed to create CDR webhook request", "err", err)
			return
		}

		req.Header.Set("Content-Type", "application/json")

		// Apply HMAC-SHA256 Signature for authenticity validation
		if cfg.CDRWebhookSecret != "" {
			mac := hmac.New(sha256.New, []byte(cfg.CDRWebhookSecret))
			mac.Write(body)
			signature := hex.EncodeToString(mac.Sum(nil))
			req.Header.Set("X-VBGW-Signature", signature)
		}

		// Fire and forget
		resp, err := httpCli.Do(req)
		if err != nil {
			slog.Warn("CDR Webhook delivery failed (Fire & Forget)", "url", cfg.CDRWebhookURL, "err", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			slog.Warn("CDR Webhook rejected by server", "status", resp.StatusCode)
		} else {
			slog.Debug("CDR Webhook delivered successfully", "session_id", payload.SessionID)
		}
	}()
}
