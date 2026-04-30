# VoiceBot Gateway (VBGW) — FreeSWITCH Edition v4.0

AI 콜봇 인프라를 위한 SIP/RTP 게이트웨이입니다.  
FreeSWITCH가 PBX/SBC와의 SIP/RTP를 담당하고, Orchestrator가 콜 제어와 정책을 담당하며, Bridge가 AI 엔진과의 실시간 오디오 스트리밍을 처리합니다.

현재 저장소는 단순 “AI 콜 연결” 수준을 넘어, **서비스 라우팅, 슬롯 기반 수용량 제어, queue/human fallback, SIP extension slot, 운영 제어 plane, 다중 노드 cluster lease**까지 포함하는 구조로 확장되었습니다.

## 무엇이 달라졌나

### Phase 0 ~ 7 개선 요약

| Phase | 핵심 개선 |
|------|------|
| Phase 0 | `routing.yaml` 기반 정책 계층 도입 준비, 번호 ownership 정리, routing runtime 골격 추가 |
| Phase 1 | 대표번호 `1000 / 2000 / 5551212`를 logical service로 매핑하는 service routing MVP 추가 |
| Phase 2 | service별 logical slot pool, allocator, overflow policy, capacity snapshot 구현 |
| Phase 3 | PBX/SBC interconnect health-aware routing, standby failover, confirmed handoff 보강 |
| Phase 4 | `queue`, `failover_service`, `fallback_to_human`, queue announcement, callcenter fallback 구현 |
| Phase 5 | `sip_extension` backend, registration inventory, extension slot transfer 경로 구현 |
| Phase 6 | 운영 조회/제어 분리, admin control API, bridge internal secret, production fail-fast 강화 |
| Phase 7 | Redis 기반 distributed slot lease, node heartbeat/state, stale lease recovery, node drain/resume, compatibility gate 구현 |

## 아키텍처

```text
PBX / SBC / Softphone
        |
        | SIP INVITE / RTP
        v
+-----------------------+
| FreeSWITCH            |
| - mod_sofia           |
| - mod_event_socket    |
| - mod_audio_fork      |
| - mod_xml_curl        |
+-----------+-----------+
            |
            | ESL / XML-Curl / WebSocket Audio Fork
            v
+-----------------------+        Redis
| Orchestrator          | <------------------+
| - service routing     |                    |
| - slot allocator      |                    |
| - queue / fallback    |                    |
| - admin control plane |                    |
| - cluster lease       |                    |
+-----------+-----------+                    |
            |                                |
            | HTTP / internal control        |
            v                                |
+-----------------------+                    |
| Bridge                | -------------------+
| - WebSocket ingest    |
| - VAD                 |
| - gRPC streaming      |
| - TTS playout         |
+-----------+-----------+
            |
            | gRPC bi-dir
            v
+-----------------------+
| AI Engine             |
| - STT / TTS / NLU     |
+-----------------------+
```

## 현재 지원 기능

- SIP `register` / `trunk` 기반 PBX/SBC interconnect
- 대표번호 `entry number -> service -> slot pool -> overflow policy` 라우팅
- Logical slot pool과 `sip_extension` slot backend 동시 지원
- Overflow 정책
  - `busy`
  - `direct_transfer`
  - `queue`
  - `failover_service`
  - `fallback_to_human`
- PBX main/standby health-aware gateway 선택
- Admin read/control API 분리
- Distributed lease 기반 multi-node 신규콜 분산
- Node drain/resume 및 routing compatibility gate

## 주요 런타임 포트

| 컴포넌트 | 포트 | 설명 |
|------|------|------|
| FreeSWITCH | `5060/udp`, `5060/tcp` | SIP |
| FreeSWITCH | `5061/tcp` | SIP TLS |
| FreeSWITCH | `16384-16584/udp` | RTP |
| Orchestrator | `8080` | REST API / health / metrics |
| Bridge | `8090` | WebSocket audio ingress |
| Bridge internal | `8091` | internal control/health, 호스트 publish 안 함 |
| Redis | `16379` | session / queue / cluster lease |
| AI container | host `50051` -> container `8091` | AI gRPC 진입 |

## 빠른 시작

### 1. 환경 파일 준비

```bash
cd vbgw_v2/vbgw-freeswitch
cp .env.example .env
```

필수로 바꿔야 하는 값:

```bash
ESL_PASSWORD=<strong-secret>
ADMIN_API_KEY=<strong-secret>
ADMIN_CONTROL_KEY=<strong-secret>
JWT_SECRET=<strong-secret>
INTERNAL_API_SECRET=<strong-secret>
```

### 2. 서비스 기동

```bash
docker compose up -d
docker compose ps
```

### 3. 기본 상태 확인

```bash
curl -s http://127.0.0.1:8080/live
curl -s http://127.0.0.1:8080/ready
curl -s -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/health | jq .
```

## 개발/실콜 테스트 번호

현재 dev routing 예시는 [orchestrator/config/routing.yaml](orchestrator/config/routing.yaml)에 정의되어 있습니다.

| 번호 | 용도 |
|------|------|
| `9196` | legacy AI route fallback |
| `9296` | 기본 AI greeting 확인 |
| `9297` | queue overflow 검증 |
| `9298` | human fallback 검증 |
| `9390` | SIP extension slot demo |

### 권장 확인 순서

1. `9296`으로 AI greeting 확인
2. `9297`로 queue hold / timeout 확인
3. `9298`로 human fallback 확인
4. `9390`으로 registered extension slot ringing 확인

Zoiper 설정 가이드는 [docs/zoiper_setup_guide.md](docs/zoiper_setup_guide.md)를 참고하세요.

## 라우팅과 수용량 모델

현재 시스템의 핵심 모델은 아래와 같습니다.

```text
entry number
  -> logical service
  -> slot pool
  -> allocator
  -> overflow policy
```

예시:

- `1000`, `5551212` -> `bot-main`
- `2000` -> `vip-bot`
- `9297` -> `bot-queue-dev`
- `9390` -> `ext-pool-demo`

지원 allocator:

- `round_robin`
- `priority`
- `sticky_by_caller`
- `least_recently_used`

지원 backend:

- `logical`
- `sip_extension`

## PBX / SBC 연동

현재 구조는 `register`와 `trunk` 모두 지원합니다.

- `PBX_INTERCONNECT_ENABLED=false`
  - PBX interconnect 비활성
  - dev/lab 기준
- `PBX_INTERCONNECT_ENABLED=true`
  - `pbx-main`, `pbx-standby` gateway 활성
  - gateway health, standby, failover 동작

상세 연동 가이드는 [docs/pbx_sbc_interconnect.md](docs/pbx_sbc_interconnect.md)를 참고하세요.

## 운영 제어 Plane

### 인증 수준

- Read API: `ADMIN_API_KEY` 또는 readonly/admin scope JWT
- Control API: `ADMIN_CONTROL_KEY` 또는 control/admin scope JWT

### 대표 조회 API

```bash
curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/services/capacity | jq

curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/slots | jq

curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/queues | jq

curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/gateways | jq

curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/cluster/nodes | jq
```

### 대표 제어 API

```bash
# 서비스 pause
curl -X POST http://127.0.0.1:8080/api/v1/admin/services/bot-main/pause \
  -H "Authorization: Bearer ${ADMIN_CONTROL_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"maintenance"}'

# queue flush
curl -X POST http://127.0.0.1:8080/api/v1/admin/queues/bot-main/flush \
  -H "Authorization: Bearer ${ADMIN_CONTROL_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"queue reset"}'

# gateway standby
curl -X POST http://127.0.0.1:8080/api/v1/admin/gateways/pbx-main/standby \
  -H "Authorization: Bearer ${ADMIN_CONTROL_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"pbx maintenance","enabled":true}'

# node drain
curl -X POST http://127.0.0.1:8080/api/v1/admin/cluster/nodes/${NODE_ID}/drain \
  -H "Authorization: Bearer ${ADMIN_CONTROL_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"reason":"rolling deploy"}'
```

모든 control 응답은 operation ID를 포함하며, 조회는 아래로 가능합니다.

```bash
curl -H "Authorization: Bearer ${ADMIN_API_KEY}" \
  http://127.0.0.1:8080/api/v1/admin/operations | jq
```

## Multi-Node / HA

Phase 7 기준으로 신규콜 분산과 운영 drain을 위한 cluster runtime이 들어가 있습니다.

- Redis authoritative distributed lease
- node heartbeat / state (`active`, `draining`, `paused`, `offline`)
- stale lease reaper
- node drain / resume API
- routing reload / node resume 시 compatibility gate

핵심 cluster key 예시는 아래와 같습니다.

- `vbgw:node:{node}:heartbeat`
- `vbgw:node:{node}:state`
- `vbgw:lease:service:{service}:slot:{slot}`
- `vbgw:lease:session:{session}`

참고: 현재 구조는 **기존 active call migration**이 아니라 **신규콜 재분산과 rolling deploy**에 초점을 맞춥니다.

## 보안 및 운영 가드

- production에서 Redis 연결 실패 시 in-memory fallback 금지
- Bridge internal API는 `INTERNAL_API_SECRET` 없이는 접근 차단
- admin read/control key 분리
- service pause/drain과 node drain 분리
- routing reload는 cluster compatibility gate 통과 후 적용

## 모니터링

대표 메트릭:

- `vbgw_active_calls`
- `vbgw_service_control_state`
- `vbgw_service_queue_depth`
- `vbgw_slot_in_use`
- `vbgw_gateway_health`
- `vbgw_node_heartbeat_age_seconds`
- `vbgw_node_state`
- `vbgw_distributed_lease_total`
- `vbgw_distributed_lease_stale_total`
- `vbgw_drain_sessions_remaining`

운영 상세는 [docs/operations_runbook.md](docs/operations_runbook.md)를 참고하세요.

## 테스트

자주 쓰는 검증 명령:

```bash
cd vbgw_v2/vbgw-freeswitch/orchestrator
go test ./...
go vet ./...

cd ../bridge
go test ./...
```

overflow / extension slot 보조 스크립트:

- [scripts/prime_dev_overflow.sh](scripts/prime_dev_overflow.sh)
- [scripts/test_extension_pool.sh](scripts/test_extension_pool.sh)
- [scripts/softphone_test_info.sh](scripts/softphone_test_info.sh)

## 문서 인덱스

설계/운영 문서는 `docs/` 아래에 정리되어 있습니다.

- 전체 로드맵: [docs/full_scope_roadmap.md](docs/full_scope_roadmap.md)
- Phase 0: [docs/phase0_detailed_design.md](docs/phase0_detailed_design.md)
- Phase 1: [docs/phase1_detailed_design.md](docs/phase1_detailed_design.md)
- Phase 2: [docs/phase2_detailed_design.md](docs/phase2_detailed_design.md)
- Phase 3: [docs/phase3_detailed_design.md](docs/phase3_detailed_design.md)
- Phase 4: [docs/phase4_detailed_design.md](docs/phase4_detailed_design.md)
- Phase 5: [docs/phase5_detailed_design.md](docs/phase5_detailed_design.md)
- Phase 6: [docs/phase6_detailed_design.md](docs/phase6_detailed_design.md)
- Phase 7: [docs/phase7_detailed_design.md](docs/phase7_detailed_design.md)
- 운영 가이드: [docs/operations_runbook.md](docs/operations_runbook.md)
- 로컬 테스트: [docs/local_testing_guide.md](docs/local_testing_guide.md)
- 트러블슈팅: [docs/troubleshooting.md](docs/troubleshooting.md)

## 디렉토리 구조

```text
vbgw-freeswitch/
├── config/                 # FreeSWITCH 설정
├── bridge/                 # WebSocket/VAD/gRPC bridge
├── orchestrator/           # control plane, routing, capacity, cluster
├── docs/                   # 설계/운영 문서
├── scripts/                # 검증/운영 보조 스크립트
├── recordings/             # 녹취 저장
├── docker-compose.yml
└── README.md
```

## 상태 요약

현재 README 기준으로 이 저장소는 아래 범위를 다룹니다.

- 개발 실콜 경로: 완료
- queue / human fallback: 완료
- SIP extension slot: 완료
- 운영 control plane: 완료
- cluster lease / node drain / compatibility gate: 완료

다만 실제 운영 투입 전에는 아래를 별도로 확인하는 것을 권장합니다.

- 실제 PBX/SBC interconnect E2E
- 2개 이상 Orchestrator 노드 실환경 분산 검증
- TLS/SRTP / 인증서 운영 점검
- 고객사 번호 플랜과 service ownership 최종 매핑
