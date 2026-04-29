# CLAUDE.md — VoiceBot Gateway (vbgw_v2)

> 이 파일은 매 세션 시작 시 자동으로 읽히는 프로젝트 지시서.
> 마지막 갱신: 2026-04-26 — 실 아키텍처 (Go + FreeSwitch) 반영. 옛 C++ / PJSIP 내용은 `legacy/` 에 보존.

---

## 1. 프로젝트 개요

**VoiceBot Gateway (vbgw)** — AI 콜봇 인프라의 핵심 통화 제어 및 미디어 게이트웨이.

| 항목 | 내용 |
|------|------|
| 역할 | PBX/SBC 의 SIP/RTP → FreeSwitch → orchestrator/bridge → AI gRPC 서비스 |
| 언어 | **Go 1.23** (orchestrator + bridge + ai engine), C/Lua (FreeSwitch dialplan) |
| 빌드 | Go modules + Docker (component 별 Dockerfile) |
| 핵심 라이브러리 | grpc-go, gorilla/websocket, ONNX Runtime Go, sashabaranov/go-openai |
| 프로토콜 | SIP/RTP (FS), WebSocket (FS↔bridge), gRPC bidi stream (bridge↔AI), ESL (FS↔orchestrator), HTTP REST (admin) |
| VAD 모델 | Silero VAD v4 (ONNX Runtime Go) |
| 세션 상태 | Redis (Lua atomic) + 로컬 fallback |

**관련 프로젝트:** `~/AgenticOE_v2/` (Python FastAPI backend) — proto contract owner. cross-project 통합은 그쪽 `skeleton/docs/guide/cross-project-integration.md` 참고.

---

## 2. 디렉토리 구조 (실제)

```
vbgw_v2/
├── README.md, CHANGELOG.md, AGENTS.md, GEMINI.md
├── CLAUDE.md                       # 이 파일
├── run_local.sh                    # 로컬 dev 부트
├── charts/vbgw/                    # 통합 Helm chart
│   ├── Chart.yaml, values.yaml
│   └── templates/
│       ├── deployment-orchestrator.yaml
│       ├── deployment-bridge.yaml      # canary block 추가됨 (2026-04-26)
│       ├── deployment-freeswitch.yaml
│       ├── ingress.yaml
│       ├── _helpers.tpl, NOTES.txt
├── docs/performance/sla_baseline.md
├── vbgw-ai/                        # AI engine (Go)
│   ├── cmd/main.go                 # gRPC server 진입점 (port 50051)
│   ├── internal/ai/{server,engine,openai,utils}.go
│   ├── internal/config/config.go
│   ├── proto/voicebot.proto        # ★ DUPLICATED — canonical 은 AgenticOE_v2
│   ├── proto/voicebot/voicebot{.pb.go,_grpc.pb.go}
│   └── Dockerfile
├── vbgw-freeswitch/                # FS + orchestrator + bridge
│   ├── Dockerfile.builder, Dockerfile.freeswitch
│   ├── docker-compose.yml, docker-compose.{prod,canary}.yml
│   ├── Makefile
│   ├── config/                     # FS dialplan, vars
│   ├── models/                     # silero_vad.onnx
│   ├── nginx/                      # 옵션 reverse proxy
│   ├── scripts/                    # 운영 스크립트
│   ├── orchestrator/
│   │   ├── cmd/{main,pbx_health}.go
│   │   ├── internal/{esl,recording,metrics,config}/
│   │   └── go.mod
│   ├── bridge/
│   │   ├── cmd/main.go             # WS↔gRPC bridge
│   │   ├── internal/
│   │   │   ├── grpc/{client,retry}.go    # AI 엔진 gRPC client
│   │   │   ├── tts/buffer.go             # TTS 출력 버퍼링
│   │   │   ├── barge/controller.go        # Barge-in 처리
│   │   │   ├── vad/silero.go             # Silero VAD
│   │   │   └── config/config.go          # ★ env: AI_GRPC_ADDR (GRPC_AI_ADDR 아님!)
│   │   ├── proto/voicebot/voicebot{.pb.go,_grpc.pb.go}
│   │   └── Dockerfile
│   ├── protos/voicebot.proto       # ★ DUPLICATED
│   ├── docs/                       # 컴포넌트 별 운영 문서
│   ├── tests/, recordings/, redis_data/
└── legacy/                         # 옛 C++ / PJSIP 구현 (참조만 — 빌드 안 함)
    ├── src/, protos/, history/development_history.md
    └── docs/freeswitch_migration_*  # 왜 Go+FS 로 옮겼는지 사유 기록
```

---

## 3. 런타임 데이터 흐름

```text
PBX/SBC ──SIP/RTP──▶ FreeSwitch
                       │
                       ├─ ESL ──▶ Orchestrator (Go, REST :8080)
                       │           ├─ Redis (Lua atomic 세션 상태)
                       │           └─ Admin Dashboard (JWT, REST API)
                       │
                       └─ WebSocket(audio_fork) ──▶ Bridge (Go)
                                                     │
                                                     │ gRPC StreamSession (VoicebotAiService)
                                                     │ env AI_GRPC_ADDR
                                                     │
                                                     ▼
                                        ┌────────────────────────┐
                                        │ AI Engine endpoint:    │
                                        │  · vbgw-ai (자체)       │
                                        │  · agentoe-backend (★) │
                                        └────────────────────────┘
```

**★ AgentOE backend 도 동일 gRPC contract (`voicebot.ai.VoicebotAiService`) 구현 (2026-04-26).** Bridge 가 어느 endpoint 호출하느냐는 `bridge.grpcAiAddr` (Helm) → `AI_GRPC_ADDR` (env) 로 결정.

cutover 절차: AgenticOE_v2 의 `skeleton/docs/runbook/vbgw-ai-cutover.md`.

---

## 4. 빌드 및 실행

### 로컬 (docker-compose)
```bash
cd vbgw-freeswitch
cp .env.example .env
docker-compose up -d
# FS, orchestrator, bridge, vbgw-ai, redis 모두 기동
```

### 컴포넌트 단독 빌드
```bash
# AI engine
cd vbgw-ai && go build -o ./ai_engine ./cmd

# Orchestrator
cd vbgw-freeswitch/orchestrator && go build -o ./orch_app ./cmd

# Bridge
cd vbgw-freeswitch/bridge && go build -o ./bridge ./cmd
```

### Proto stub 재생성 — **AgenticOE_v2 가 owner**
```bash
# AgenticOE_v2 에서 sync
cd ~/AgenticOE_v2/skeleton/contracts
make sync-vbgw VBGW=$HOME/vbgw_v2

# vbgw_v2 측에서 Go stub 재생성 (각 컴포넌트)
cd vbgw-ai && protoc -I=proto --go_out=proto/voicebot --go-grpc_out=proto/voicebot proto/voicebot.proto
cd vbgw-freeswitch/bridge && protoc -I=proto --go_out=proto/voicebot --go-grpc_out=proto/voicebot proto/voicebot.proto
```

### Helm
```bash
helm -n vbgw-staging upgrade --install vbgw ./charts/vbgw \
  -f charts/vbgw/values-staging.yaml
```

---

## 5. gRPC 인터페이스 (CANONICAL)

`proto/voicebot.proto` 는 vbgw_v2 안에 있지만 **canonical 은 AgenticOE_v2/skeleton/contracts/proto/voicebot.proto**. 변경 시 AgenticOE_v2 PR 우선 + sync 스크립트.

```protobuf
service VoicebotAiService {
    rpc StreamSession(stream AudioChunk) returns (stream AiResponse);
}
```

자세한 정의는 canonical 파일.

---

## 6. 환경 변수 — bridge

**중요**: chart 의 `bridge.grpcAiAddr` 는 env `AI_GRPC_ADDR` 로 매핑. 옛 `GRPC_AI_ADDR` 은 코드가 안 읽음 (2026-04-26 수정).

| Env                       | Default                | 설명                                       |
|---------------------------|------------------------|--------------------------------------------|
| `AI_GRPC_ADDR`            | `127.0.0.1:50051`      | AI 엔진 endpoint. cutover 시 `agentoe-backend...` |
| `AI_GRPC_TLS`             | `false`                | mTLS 활성 (현재는 plaintext)                |
| `BRIDGE_TRACK`            | `stable`               | canary 추적 라벨 (`stable` / `canary`)      |
| `WS_PORT`                 | `8090`                 | FS audio_fork 가 connect 하는 WS 포트      |
| `INTERNAL_PORT`           | `8091`                 | orchestrator → bridge HTTP                 |
| `ONNX_MODEL_PATH`         | `/models/silero_vad.onnx` | VAD 모델                                |
| `GRPC_STREAM_DEADLINE_SECS`| `7200`                | gRPC stream 최대 길이 (2h, T-28 fix)       |
| `WS_ALLOWED_ORIGINS`      | (empty = all)          | WS handshake origin 제한                   |

---

## 7. 코딩 컨벤션 (Go)

- **표준**: Go 1.23. 모듈 단위 분리.
- **에러**: `fmt.Errorf("%w", err)` wrap. log 만 하고 삼키지 말 것.
- **컨텍스트**: 모든 공개 함수 첫 인자 `context.Context`. timeout / cancel 전파.
- **로그**: 표준 `log/slog` (구조화 JSON). secret 절대 로그 X.
- **테스트**: `_test.go` 표준. 테이블 드리븐. integration 테스트는 `_integration_test.go` 분리.
- **gRPC**: server-streaming 은 client cancel 즉시 cleanup. goroutine leak 주의.

### 금지
- 하드코딩 IP/포트 — `internal/config/config.go` 의 env loader 사용
- `panic()` — recover 없이 (orchestrator/bridge 둘 다 supervisor 없음)
- `legacy/` 안 파일 수정 — 참조만, 빌드 대상 아님
- `silero_vad.onnx` 수정 — 바이너리

---

## 8. 알려진 정합성 이슈

- **proto 중복**: `vbgw-ai/proto/`, `vbgw-freeswitch/protos/`, `vbgw-freeswitch/bridge/proto/` 세 곳에 같은 .proto. canonical 은 AgenticOE_v2. drift 검증: `cd ~/AgenticOE_v2/skeleton/contracts && make verify-vbgw VBGW=$HOME/vbgw_v2`.
- **Chart env 키 버그 (FIXED 2026-04-26)**: 이전 chart 가 `GRPC_AI_ADDR` 로 export 했지만 Go 가 `AI_GRPC_ADDR` 만 읽어서 무시. canary cutover 작업 중 발견 + 수정.
- **메트릭 시리즈 정합성**: AgenticOE_v2 의 SLO doc 이 정의한 시리즈 (`agentoe_call_setup_total{result}`, `agentoe_call_terminations_total{reason}`, `agentoe_call_duration_seconds`) 를 vbgw 측에서 노출하는지 미검증 — orchestrator/bridge 의 prometheus exporter 를 audit 해야 함.

---

## 9. 운영 페이지

- 통화 트러블슈팅: `vbgw-freeswitch/docs/operations_runbook.md`
- 부하 테스트: `vbgw-freeswitch/scripts/` + `docs/performance/sla_baseline.md`
- cutover (vbgw-ai → backend): `~/AgenticOE_v2/skeleton/docs/runbook/vbgw-ai-cutover.md`

---

## 10. 새 세션 진입 시

1. 이 파일 (CLAUDE.md) 끝까지 읽기.
2. 작업이 cross-project 면 `~/AgenticOE_v2/skeleton/docs/HANDOFF.md` 와 `cross-project-integration.md` 도 같이 읽기.
3. proto / API 변경은 절대 vbgw_v2 안에서 단독 수정 금지 — AgenticOE_v2 PR 우선.
