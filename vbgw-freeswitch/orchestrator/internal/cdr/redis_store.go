/**
 * @file redis_store.go
 * @description Redis LIST 기반 CDR 영구 저장소 — 통합 운영 포탈 통화이력 조회용
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-05-11 | Portal | 최초 생성 | Redis LIST + LTRIM 방식
 * ─────────────────────────────────────────
 */

package cdr

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisCDRKey     = "vbgw:cdr"
	maxCDRRecords   = 50000
	cdrQueryTimeout = 5 * time.Second
)

// CDRStore provides persistent CDR storage backed by Redis LIST.
type CDRStore struct {
	client *redis.Client
	mu     sync.Mutex
}

// CDRFilter defines query parameters for CDR retrieval.
type CDRFilter struct {
	CallerID    string
	ServiceName string
	From        time.Time
	To          time.Time
	Limit       int
	Offset      int
}

// CDRQueryResult contains paginated CDR results.
type CDRQueryResult struct {
	Records    []Record `json:"records"`
	Total      int      `json:"total"`
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
	HasMore    bool     `json:"has_more"`
}

// NewCDRStore creates a CDR store using the provided Redis client.
func NewCDRStore(client *redis.Client) *CDRStore {
	if client == nil {
		return nil
	}
	return &CDRStore{client: client}
}

// Push appends a CDR record to the Redis LIST.
// LPUSH + LTRIM ensures the list doesn't exceed maxCDRRecords.
func (s *CDRStore) Push(rec Record) {
	if s == nil || s.client == nil {
		return
	}

	data, err := json.Marshal(rec)
	if err != nil {
		slog.Error("CDR store marshal failed", "err", err, "session_id", rec.SessionID)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	s.mu.Lock()
	defer s.mu.Unlock()

	pipe := s.client.Pipeline()
	pipe.LPush(ctx, redisCDRKey, data)
	pipe.LTrim(ctx, redisCDRKey, 0, int64(maxCDRRecords-1))
	if _, err := pipe.Exec(ctx); err != nil {
		slog.Error("CDR store push failed", "err", err, "session_id", rec.SessionID)
	}
}

// Query retrieves CDR records matching the filter criteria.
func (s *CDRStore) Query(filter CDRFilter) CDRQueryResult {
	if s == nil || s.client == nil {
		return CDRQueryResult{Records: []Record{}}
	}

	if filter.Limit <= 0 {
		filter.Limit = 50
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}

	ctx, cancel := context.WithTimeout(context.Background(), cdrQueryTimeout)
	defer cancel()

	// Fetch a larger window to account for filtering
	fetchSize := int64((filter.Offset + filter.Limit) * 3)
	if fetchSize < 300 {
		fetchSize = 300
	}
	if fetchSize > int64(maxCDRRecords) {
		fetchSize = int64(maxCDRRecords)
	}

	rawRecords, err := s.client.LRange(ctx, redisCDRKey, 0, fetchSize-1).Result()
	if err != nil {
		slog.Error("CDR store query failed", "err", err)
		return CDRQueryResult{Records: []Record{}}
	}

	// Parse and filter
	var matched []Record
	hasFilter := filter.CallerID != "" || filter.ServiceName != "" || !filter.From.IsZero() || !filter.To.IsZero()

	for _, raw := range rawRecords {
		var rec Record
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}

		if hasFilter && !matchesFilter(rec, filter) {
			continue
		}
		matched = append(matched, rec)
	}

	total := len(matched)

	// Apply pagination
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}

	page := matched[start:end]
	if page == nil {
		page = []Record{}
	}

	return CDRQueryResult{
		Records: page,
		Total:   total,
		Limit:   filter.Limit,
		Offset:  filter.Offset,
		HasMore: end < total,
	}
}

// Count returns the total number of CDR records in Redis.
func (s *CDRStore) Count() int64 {
	if s == nil || s.client == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	count, err := s.client.LLen(ctx, redisCDRKey).Result()
	if err != nil {
		return 0
	}
	return count
}

func matchesFilter(rec Record, f CDRFilter) bool {
	if f.CallerID != "" && !strings.Contains(strings.ToLower(rec.CallerID), strings.ToLower(f.CallerID)) {
		return false
	}
	if f.ServiceName != "" && !strings.EqualFold(rec.ServiceName, f.ServiceName) {
		return false
	}

	// Parse record time for date range filtering
	if !f.From.IsZero() || !f.To.IsZero() {
		recTime, err := time.Parse(time.RFC3339, rec.EndTime)
		if err != nil {
			return false
		}
		if !f.From.IsZero() && recTime.Before(f.From) {
			return false
		}
		if !f.To.IsZero() && recTime.After(f.To) {
			return false
		}
	}
	return true
}
