/**
 * @file interface.go
 * @description ESL Commander 인터페이스 — 테스트 mock 지원을 위한 추상화
 *
 * 변경 이력
 * ─────────────────────────────────────────
 * v1.0.0 | 2026-04-09 | [Implementer] | 최초 생성 | api 패키지 테스트용 인터페이스 추출
 * ─────────────────────────────────────────
 */

package esl

// Commander abstracts ESL commands for testability.
// *Client satisfies this interface.
type Commander interface {
	Originate(uuid, target, callerID string, useStandby bool) (string, error)
	SendDtmf(uuid, digits string) error
	Transfer(uuid, target string) error
	Bridge(uuidA, uuidB string) error
	Unbridge(uuid string) error
	RecordStart(uuid, path string) error
	RecordStop(uuid string) error
	Break(uuid string) error
	Kill(uuid string) error
	Dump(uuid string) (map[string]string, error)
	Pause() error
	Resume() error
	IsConnected() bool
}

// Verify *Client implements Commander at compile time.
var _ Commander = (*Client)(nil)
