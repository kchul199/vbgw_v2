package cdr

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"time"

	"vbgw-orchestrator/internal/metrics"
	"vbgw-orchestrator/internal/session"
)

// Record represents a Call Detail Record.
type Record struct {
	SessionID      string  `json:"session_id"`
	FSUUID         string  `json:"fs_uuid"`
	CallerID       string  `json:"caller_id"`
	DestNum        string  `json:"dest_number"`
	EntryNumber    string  `json:"entry_number,omitempty"`
	ServiceName    string  `json:"service_name,omitempty"`
	SourceGateway  string  `json:"source_gateway,omitempty"`
	StartTime      string  `json:"start_time"`
	AnswerTime     string  `json:"answer_time,omitempty"`
	EndTime        string  `json:"end_time"`
	DurationS      float64 `json:"duration_seconds"`
	HangupCode     string  `json:"hangup_code,omitempty"`
	BridgedWith    string  `json:"bridged_with,omitempty"`
}

var webhookURL string

func init() {
	webhookURL = os.Getenv("CDR_WEBHOOK_URL")
}

// LogHangup writes a CDR entry for a completed call.
func LogHangup(s *session.SessionState, hangupCode string) {
	now := time.Now()
	s.SetHangupAt(now)

	duration := now.Sub(s.CreatedAt).Seconds()
	metrics.SessionDuration.Observe(duration)

	cdr := Record{
		SessionID:     s.SessionID,
		FSUUID:        s.FSUUID,
		CallerID:      s.CallerID,
		DestNum:       s.DestNum,
		EntryNumber:   s.EntryNumber,
		ServiceName:   s.ServiceName,
		SourceGateway: s.SourceGateway,
		StartTime:     s.CreatedAt.Format(time.RFC3339),
		EndTime:       now.Format(time.RFC3339),
		DurationS:     duration,
		HangupCode:    hangupCode,
		BridgedWith:   s.BridgedWith(),
	}

	if !s.AnsweredAt.IsZero() {
		cdr.AnswerTime = s.AnsweredAt.Format(time.RFC3339)
	}

	slog.Info("CDR",
		"session_id", cdr.SessionID,
		"fs_uuid", cdr.FSUUID,
		"caller_id", cdr.CallerID,
		"dest_number", cdr.DestNum,
		"entry_number", cdr.EntryNumber,
		"service_name", cdr.ServiceName,
		"start_time", cdr.StartTime,
		"answer_time", cdr.AnswerTime,
		"end_time", cdr.EndTime,
		"duration_seconds", cdr.DurationS,
		"hangup_code", cdr.HangupCode,
		"bridged_with", cdr.BridgedWith,
	)

	// O3: CDR Webhook — fire-and-forget POST to external system
	if webhookURL != "" {
		go dispatchWebhook(cdr)
	}
}

func dispatchWebhook(cdr Record) {
	body, err := json.Marshal(cdr)
	if err != nil {
		slog.Error("CDR webhook marshal failed", "err", err)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		slog.Warn("CDR webhook dispatch failed", "url", webhookURL, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		slog.Warn("CDR webhook returned error", "url", webhookURL, "status", resp.StatusCode)
	}
}

