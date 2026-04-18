/**
 * @file manager.go
 * @description 세션 매니저 인터페이스 (Redis/Memory 구현체 추상화)
 */

package session

import (
	"context"
)

// Manager manages active call sessions.
type Manager interface {
	TryAcquire(ctx context.Context) bool
	AddIfUnderCapacity(ctx context.Context, s *SessionState) bool
	Get(ctx context.Context, sessionID string) (*SessionState, bool)
	GetByFSUUID(ctx context.Context, fsUUID string) (*SessionState, bool)
	Release(ctx context.Context, sessionID string)
	Count(ctx context.Context) int64
	WaitAllDrained(ctx context.Context, killFn func(fsUUID string))
	ForEachLocal(fn func(s *SessionState))
	PublishCommand(ctx context.Context, targetNodeID, sessionID, action string, payload interface{}) error
	SubscribeCommands(ctx context.Context, handler func(msg CommandMsg))
}
