package overflow

import "time"

type QueueEntry struct {
	SessionID         string    `json:"session_id"`
	ServiceName       string    `json:"service_name"`
	OverflowPolicy    string    `json:"overflow_policy"`
	TransferTarget    string    `json:"transfer_target,omitempty"`
	OverflowService   string    `json:"overflow_service,omitempty"`
	QueueAnnouncement string    `json:"queue_announcement,omitempty"`
	QueueOnTimeout    string    `json:"queue_on_timeout"`
	EnqueuedAt        time.Time `json:"enqueued_at"`
	TimeoutAt         time.Time `json:"timeout_at"`
}

type QueueSnapshot struct {
	ServiceName       string               `json:"service_name"`
	Depth             int                  `json:"depth"`
	OldestWaitSeconds float64              `json:"oldest_wait_seconds"`
	Sessions          []QueueEntrySnapshot `json:"sessions"`
}

type QueueEntrySnapshot struct {
	SessionID      string    `json:"session_id"`
	QueueOnTimeout string    `json:"queue_on_timeout"`
	EnqueuedAt     time.Time `json:"enqueued_at"`
	TimeoutAt      time.Time `json:"timeout_at"`
}
