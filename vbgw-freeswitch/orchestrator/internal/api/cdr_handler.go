/**
 * @file cdr_handler.go
 * @description CDR 조회 API 핸들러 — 통합 운영 포탈 통화이력 화면 연동
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-05-11 | Portal | 최초 생성 | GET /api/v1/admin/cdr
 * ─────────────────────────────────────────
 */

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"vbgw-orchestrator/internal/cdr"
)

// CDRHandler serves CDR query requests for the operations portal.
type CDRHandler struct {
	Store *cdr.CDRStore
}

// GetCDRRecords handles GET /api/v1/admin/cdr with query params:
//   caller_id, service_name, from, to (RFC3339), limit, offset
func (h *CDRHandler) GetCDRRecords(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": "CDR store unavailable",
		})
		return
	}

	filter := cdr.CDRFilter{
		CallerID:    r.URL.Query().Get("caller_id"),
		ServiceName: r.URL.Query().Get("service_name"),
		Limit:       parseIntParam(r, "limit", 50),
		Offset:      parseIntParam(r, "offset", 0),
	}

	if fromStr := r.URL.Query().Get("from"); fromStr != "" {
		if t, err := time.Parse(time.RFC3339, fromStr); err == nil {
			filter.From = t
		}
	}
	if toStr := r.URL.Query().Get("to"); toStr != "" {
		if t, err := time.Parse(time.RFC3339, toStr); err == nil {
			filter.To = t
		}
	}

	result := h.Store.Query(filter)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":   "success",
		"total":    result.Total,
		"limit":    result.Limit,
		"offset":   result.Offset,
		"has_more": result.HasMore,
		"data":     result.Records,
	})
}

func parseIntParam(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
