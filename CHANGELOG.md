# CHANGELOG

## [v2.7.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 버그수정 + 기능추가 — PBX/SBC 연동 3차 심층 검토 R-01~R-07 반영

#### Fixed
- `dialplan/default.xml`: R-01 multi-condition AND 평가 문제 → 단일 condition + execute_extension 분기로 재구성
- `esl/client.go`: R-02 Connect() 시 기존 conn 미닫기 → `c.conn.Close()` 선행 호출 (eventLoop goroutine 중첩 방지)
- `vad/silero.go`: encoding/binary 미사용 import 제거 (빌드 에러 수정)

#### Changed
- `vars.xml`: R-03 빈 proxy 방어 — PBX_STANDBY_HOST 기본값 `127.0.0.2` (불가능 주소)로 FS 로드 에러 방지
- `cmd/main.go`: R-04 Sofia 모니터 비동기화 — 별도 goroutine + 5초 타임아웃으로 mu Lock 경합 최소화
- `cdr/logger.go`: R-05 CDR에 `bridged_with` 필드 추가 — 브릿지 상대방 세션 ID 기록
- `cmd/main.go`: R-06 notifyBridgeHold Content-Type `application/json` 명시
- `cmd/main.go`: R-07 IVR OnTransfer 콜백에서 실제 `eslClient.Transfer()` 실행 (IVR_TRANSFER_TARGET 환경변수)
- `config/config.go`: R-07 `IVRTransferTarget` 설정 추가
- `.env.example`: R-07 `IVR_TRANSFER_TARGET` 가이드 추가

---

## [v2.6.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 버그수정 + 기능추가 — PBX/SBC 연동 2차 심층 검토 Q-01~Q-10 반영

#### Fixed (즉시 수정)
- `dialplan/default.xml`: Q-01 Early Media를 voicebot-inbound 내부 anti-action 분기로 통합 (이전: 도달 불가 extension)
- `sip_profiles/internal.xml`: Q-02 Sofia `<settings>` 래퍼 추가 (FS 1.10 Docker 이미지 호환성)
- `esl/commands.go`: Q-03 Originate에 `useStandby` 파라미터 — Standby 미설정 시 단일 GW만 사용
- `session/manager.go`: Q-05 Release 시 `close(IvrEventCh)` — goroutine leak 방지

#### Changed (단기 개선)
- `esl/client.go`: Q-04 SendAPI/SendBgAPI를 eventLoop 경유 방식으로 변경 — apiRespCh 채널로 응답 라우팅 (reader 경합 해소)
- `api/control.go`: Q-06 Transfer 성공 후 IVR HangupEvent 전송 — IVR 상태 정리
- `dialplan/default.xml`: Q-07 start_dtmf를 audio_fork 앞으로 이동 — DTMF in-band 톤 AI 오탐 방지
- `esl/commands.go`: Q-09 `Resume()` 메서드 추가 — Orchestrator 시작 시 fsctl resume 호출
- `cmd/main.go`: Q-09 ESL 연결 직후 fsctl resume 실행 (이전 셧다운 pause 상태 복구)

#### Added (문서화)
- `internal.xml`: `inbound-codec-negotiation=generous`, `liberal-dtmf=true`, `user-agent-string` 추가
- `operations_runbook.md`: Q-08 SBC별 Answer Delay 타이머 가이드 (Genesys, Oracle, Ribbon, AudioCodes, 삼성 SCM)
- `operations_runbook.md`: Q-10 게이트웨이 ping+REGISTER 동시 사용 주의사항
- `operations_runbook.md`: PBX 기종별 호환성 테스트 체크리스트 10항목

---

## [v2.5.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 기능추가 + 버그수정 — PBX/SBC 연동 안정성 P-01~P-15 전량 반영

#### Fixed (즉시 수정 P-01~P-05)
- `dialplan/default.xml`: P-01 ring_ready → sleep → answer 순서 교정 (PBX CANCEL 방지)
- `cmd/main.go`: P-02 onDtmf → IVR Machine DTMF 이벤트 전달 구현 (IvrEventCh)
- `cmd/main.go`: P-03 용량 초과 시 eslClient.Kill(fsUUID) 호출 (좀비 park 방지)
- `session/manager.go`: P-04 WaitAllDrained에 killFn 콜백 — shutdown 시 BYE 전송
- `internal.xml` + `vars.xml`: P-05 코덱 순서 환경변수화 (기본값 G.711 우선)

#### Added (단기 개선 P-06~P-10)
- `default.xml`: P-06 audio_fork URL 변수화 (실패 감지 기반)
- `esl/commands.go`: P-07 Originate에 callerID 파라미터 추가 (전기통신사업법 CID)
- `esl/event.go`: P-08 SipTermStatus() 메서드 — SIP 응답 코드(486/603/408) 파싱
- `default.xml`: P-09 voicebot-early-media extension (183 Session Progress + pre_answer)
- `.env.example`: P-10 Jitter Buffer 기본값 변경 (전용선 최적: 60:20:200)

#### Added (중기 개선 P-11~P-15)
- `internal.xml`: P-11 다중 게이트웨이 (pbx-main + pbx-standby) + SIP OPTIONS ping failover
- `esl/commands.go`: P-11 Originate 파이프 failover (`pbx-main|pbx-standby`)
- `esl/client.go`: P-12 CHANNEL_HOLD/CHANNEL_UNHOLD 이벤트 구독
- `cmd/main.go`: P-12 onChannelHold/onChannelUnhold 핸들러 — AI 스트리밍 pause/resume
- `vars.xml`: P-13 PRACK enable-100rel 기본값 false (PBX 호환성)
- `internal.xml`: P-14 SRTP crypto-suite 명시 (AEAD_AES_256_GCM, AES_CM_128 등)
- `cmd/main.go`: P-15 Sofia 게이트웨이 등록 상태 30초 주기 모니터링 (SipRegistered 메트릭)
- `.env.example`: 다중 PBX, 코덱 프리셋, PRACK, JB 가이드 추가

---

## [v2.4.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 보안수정 + 버그수정 — 코드 리뷰 CRITICAL/HIGH 이슈 전량 반영

#### Fixed (CRITICAL)
- `esl/client.go`: ESL Connect() auth 실패 시 TCP 연결 미닫기 → defer close 패턴 적용 (fd leak 방지)
- `esl/client.go`: ESL 재연결 후 `show channels` → orphan 세션 자동 정리 (onReconnect 콜백)
- `session/manager.go`: TryAcquire+Add 분리 연산 → `AddIfUnderCapacity` 단일 원자 연산 (race condition 해소)
- `session/model.go`: SessionState 필드 뮤텍스 없이 접근 → `sync.RWMutex` + accessor 메서드 추가
- `ws/session.go`: `context.Background()` → `parentCtx` 전달 (graceful shutdown 전파)
- `ws/session.go`: Run()에서 `pcmCh`/`ttsCh` 채널 미닫기 → `defer close()` 추가 (goroutine leak 방지)
- `vad/silero.go`: infer()에서 매번 새 ONNX session 생성 → 기존 `e.session` 재사용 (메모리 누수 + 성능)
- `grpc/client.go`: GetStream() 동시 호출 시 중복 스트림 → mutex + double-check + stale 감지

#### Security
- `api/server.go`: pprof 엔드포인트 프로덕션 비활성화 (`RuntimeProfile != production`일 때만 + auth)
- `api/server.go`: `/metrics` 인증 없이 노출 → auth 그룹 내로 이동
- `api/server.go`: `/internal/barge-in` 무인증 → `LoopbackOnlyMiddleware` (127.0.0.1만 허용)
- `api/middleware.go`: `LoopbackOnlyMiddleware` 신규 추가
- `api/control.go`: DTMF digits 정규식 검증 (`^[0-9*#A-D]{1,20}$`) — ESL 인젝션 방지
- `api/control.go`: Transfer target 정규식 검증 (`^[a-zA-Z0-9@._:\-/]{1,256}$`)
- `api/calls.go`: target_uri 로그 PII 마스킹 (`maskURI()`)
- `ws/server.go`: WebSocket CheckOrigin `true` → loopback IP만 허용
- `event_socket.conf.xml`: ESL listen-ip `0.0.0.0` → `127.0.0.1`
- `cmd/main.go`: validateProdConfig 강화 (ESL 비밀번호 기본값 거부, API key 32자 최소, changeme 패턴 거부)
- `.env.example`: ESL_PASSWORD/ADMIN_API_KEY 기본값 → 플레이스홀더로 변경

#### Changed (PBX/SBC 연동 안정성)
- `sip_profiles/internal.xml`: `rtp-timeout-sec=30` 추가 (고스트 콜 방지)
- `sip_profiles/internal.xml`: `rtp-hold-timeout-sec=60` 추가 (보류 타임아웃)
- `sip_profiles/internal.xml`: `max-sessions=110` 추가 (SIP 레벨 503 거부)
- `.env.example`: `SESSION_TIMEOUT=0` (장기 통화 강제 종료 방지)
- `.env.example`: `RTP_PORT_MAX=32768` (200→16384 포트 확장)
- `esl/commands.go`: Dump() API 오류 응답 `-ERR`/`-USAGE` 체크 추가

---

## [v2.3.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 기능추가 — Phase 4 Blue/Green Cutover 인프라

#### Added
- `docker-compose.prod.yml`: Production 오버레이 (TLS/SRTP 강제, 로그 로테이션, 리소스 제한)
- `scripts/cutover.sh`: Blue/Green 단계별 전환 (10→25→50→100%) + SLO 감시 + 자동 롤백
- `scripts/validate_prod.sh`: Production 환경 사전 검증 (TLS cert, SRTP, API key, 포트, 디스크)
- `docs/operations_runbook.md`: 운영 런북 (전환 절차, 모니터링 대시보드, 롤백 체크리스트, 문제 해결)

---

## [v2.2.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 기능추가 — Phase 3 부하 테스트 인프라

#### Added
- `orchestrator/internal/api/server.go`: pprof 엔드포인트 8개 추가 (/debug/pprof/heap, goroutine, allocs 등)
- `tests/sipp/load_test_uac.xml`: SIPp 부하 테스트 시나리오 (100 동시콜, 5분 유지, SDP + BYE)
- `tests/sipp/dtmf_scenario.xml`: SIPp DTMF IVR 테스트 시나리오 (1→2→0→*→# 5-state FSM 전이)
- `scripts/run_load_test.sh`: 자동 부하 테스트 (SIPp + SLO 모니터 + heap 수집 + 결과 리포트)
- `scripts/slo_monitor.sh`: 실시간 SLO 모니터링 (health/active_calls/dropped_frames/errors CSV 기록)
- `scripts/memory_monitor.sh`: 72h 메모리 누수 감지 (pprof heap 주기적 수집 + diff 명령 생성)
- `scripts/run_tc_all.sh`: TC-01~TC-08 자동 테스트 스위트 (PASS/FAIL 카운터 + 결과 저장)

---

## [v2.1.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 기능추가 — Phase 2 gRPC Proto 연동 + VAD ONNX + Stub 제거

#### Added
- `bridge/proto/voicebot/voicebot.pb.go`: protoc-gen-go 생성 코드
- `bridge/proto/voicebot/voicebot_grpc.pb.go`: protoc-gen-go-grpc 생성 코드
- `bridge/internal/vad/constants.go`: VAD 공통 상수 + bytesToInt16 유틸
- `bridge/internal/vad/silero_stub.go`: CGO 없는 환경용 energy-based VAD (`!cgo` 빌드 태그)

#### Changed
- `bridge/internal/grpc/client.go`: stub send/recv → 실제 voicebot.proto StreamSession 양방향 스트리밍
  - `streamSendLoop()`: AudioChunk 실제 전송
  - `streamRecvLoop()`: AiResponse 실제 수신
  - `SendDtmf()` 메서드 추가
  - Pool.Connect()에서 VoicebotAiServiceClient 생성
- `bridge/internal/vad/silero.go`: `cgo` 빌드 태그 + onnxruntime-go ONNX 추론 구현
  - `NewEngine()`: ONNX 세션 초기화 (Silero v4 input: audio, sr, h, c)
  - `infer()`: int16→float32 정규화 → ONNX → 확률 > 0.5 = speech
  - ONNX 로드 실패 시 energy-based fallback
- `bridge/internal/ws/server.go`: `/internal/dtmf/` 핸들러에서 JSON body 파싱 구현
- `bridge/internal/ws/session.go`: `ForwardDtmf()` → gRPC `AudioChunk.dtmf_digit` 실제 전송
- `bridge/Dockerfile`: `CGO_ENABLED=1` + onnxruntime 1.17.1 다운로드/설치
- `bridge/go.mod`: onnxruntime-go v1.27.0 의존성 추가
- `protos/voicebot.proto`: `go_package` 옵션 추가

---

## [v2.0.0] - 2026-04-07
### 페르소나: [Implementer]
### 작업 유형: 신규생성 — FreeSWITCH Migration Phase 1 PoC

#### Added
- `vbgw-freeswitch/docker-compose.yml`: 3-tier Docker Compose (FS + Orchestrator + Bridge)
- `vbgw-freeswitch/.env.example`: 40+ 환경변수 템플릿
- `vbgw-freeswitch/config/freeswitch/vars.xml`: FreeSWITCH 환경변수 매핑
- `vbgw-freeswitch/config/freeswitch/sip_profiles/internal.xml`: Sofia SIP 프로파일 (SRTP, PRACK, 코덱 우선순위, PBX gateway)
- `vbgw-freeswitch/config/freeswitch/dialplan/default.xml`: 인바운드 콜 다이얼플랜 (AGC + mod_audio_fork + park)
- `vbgw-freeswitch/config/freeswitch/autoload_configs/event_socket.conf.xml`: ESL 설정
- `vbgw-freeswitch/orchestrator/`: Go Orchestrator 전체 구현
  - `cmd/main.go`: 진입점, DI, 5단계 graceful shutdown
  - `internal/config/config.go`: 환경변수 로더
  - `internal/esl/client.go`: ESL TCP 클라이언트 (auth, event loop, reconnect)
  - `internal/esl/event.go`: ESL 이벤트 파서
  - `internal/esl/commands.go`: 12개 uuid_* ESL 명령 래퍼
  - `internal/session/manager.go`: sync.Map + atomic CAS 세션 매니저
  - `internal/session/model.go`: SessionState 모델
  - `internal/ivr/machine.go`: channel-based 5-state IVR FSM
  - `internal/api/server.go`: chi 라우터, 12 엔드포인트
  - `internal/api/middleware.go`: ConstantTimeCompare 인증 + Token Bucket rate limit
  - `internal/api/calls.go`: POST /api/v1/calls (outbound originate)
  - `internal/api/control.go`: DTMF, transfer, record, bridge/unbridge, barge-in
  - `internal/api/stats.go`: GET /api/v1/calls/{id}/stats (uuid_dump)
  - `internal/api/health.go`: /live, /ready, /health (3-way health check)
  - `internal/cdr/logger.go`: JSON CDR 로거
  - `internal/recording/cleaner.go`: hourly 녹음 정리 (age + quota)
  - `internal/metrics/prometheus.go`: 20+ Prometheus 메트릭 (C++ 호환)
  - `Dockerfile`: multi-stage Alpine 빌드
- `vbgw-freeswitch/bridge/`: Go WebSocket Bridge 전체 구현
  - `cmd/main.go`: 진입점, WS + Internal HTTP 서버
  - `internal/config/config.go`: 환경변수 로더
  - `internal/ws/server.go`: WS 업그레이드 + per-UUID 세션 관리 + Internal API
  - `internal/ws/session.go`: per-session 4-goroutine 파이프라인 (rx, vad+grpc, ai-response, tx)
  - `internal/vad/silero.go`: Silero VAD stub (Phase 1: energy-based, Phase 2: ONNX)
  - `internal/grpc/client.go`: gRPC 양방향 스트리밍 클라이언트 + Pool
  - `internal/grpc/retry.go`: 지수 백오프 재연결 (5회, C++ 정책 미러링)
  - `internal/tts/buffer.go`: TTS 버퍼 (cap=200, oldest-drop)
  - `internal/barge/controller.go`: Barge-in 2단계 (TTS 드레인 + HTTP → uuid_break)
  - `Dockerfile`: multi-stage Alpine 빌드
- `vbgw-freeswitch/protos/voicebot.proto`: 기존 proto 복사 (인터페이스 변경 없음)
