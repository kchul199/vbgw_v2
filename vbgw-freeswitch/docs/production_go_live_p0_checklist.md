# 상용 전환 P0 체크리스트

> 갱신일: 2026-06-03  
> 범위: `vbgw-freeswitch`, `vbgw-ai`, PBX/SBC 연동, 상용 전환  
> 목적: 현재 빌드를 "개발/실험 검증 완료" 상태에서 "상용 투입 가능" 상태로 끌어올리기

## 1. 현재 상태

2026-06-03 기준으로 현재 프로젝트는 **Stage 3 스테이징 통합 검증 완료, Stage 4 PBX/SBC 연동 실검증 진행 중 상태**입니다.

P0 보안/설정 하드닝은 코드와 compose 기준으로 상당 부분 반영되었고, `scripts/validate_prod.sh`는 현재 로컬 `.env` 기준으로 `FAIL 0` 상태입니다. 2026-06-02에는 quota가 살아 있는 OpenAI key 반영 후 Stage 3 SIPp external-profile 콜을 재실행해 fallback 없이 실제 TTS greeting과 `grpc_recvs=181`을 확인했습니다. 2026-06-03에는 Stage 4 preflight에서 실제 PBX/SBC 접속 정보 미구성을 확인했고, 이후 Kamailio SBC surrogate로 trunk/register 대역 검증을 통과했습니다. 다만 아래 실환경 검증 증적은 아직 상용 Go 판정의 남은 조건입니다.

- 실제 PBX/SBC trunk / register E2E 검증
- Redis/AI/Bridge 장애 주입과 2-node orchestrator HA 검증
- rollback rehearsal 및 컷오버 증적 패키지 정리

이번 동기화 기준 확인 결과:

- `scripts/validate_prod.sh`: `PASS 29 / FAIL 0 / WARN 7`
- `go test ./...`: orchestrator, bridge, vbgw-ai 모두 통과
- `npm run lint`, `npm run build`: portal 통과
- production compose 기준 제어면 포트 `8021`, `8090`, `16379` host publish 없음
- production compose 기준 `AI_LOAD_TEST_GREETING_BYPASS=false` 강제
- Stage 3 external-profile SIPp 콜: `grpc_recvs=181`, 실제 TTS greeting 성공, fallback 주입 없음
- Stage 4 PBX/SBC preflight: `PBX_INTERCONNECT_ENABLED=false`, `PBX_MAIN_PROXY` empty, `pbx-main` gateway 없음
- Stage 4 Kamailio surrogate: trunk gateway healthy, SIPp -> Kamailio -> VBGW 콜 성공, `grpc_recvs=186`, register `REGED`
- Redis auth: `REDIS_PASS` env, Redis `--requirepass`, orchestrator `REDIS_PASS` wiring 반영
- ESL secret: FreeSWITCH entrypoint가 `ESL_PASSWORD`를 `vars.xml` 런타임 값으로 rewrite
- Phase 7 기능은 개발 기준으로는 상당 부분 구현됐지만, 실운영 HA 검증은 아직 남아 있습니다.
  - lease renew 실패 반복 시의 운영 판정
  - node drain 수렴 보장
  - cluster 전체 active session 가시성
  - 2노드 live failover 검증

## 2. Go / No-Go 기준

아래 조건이 **모두** 충족되기 전까지는 상용 전환을 진행하면 안 됩니다.

- `P0` 보안 및 네트워크 노출 항목이 모두 종료되고 `validate_prod.sh`가 실패 없이 통과함
- 부하테스트 전용 토글이 상용 overlay에서 비활성화됨
- 실제 TTS 경로를 포함한 부하 검증이 완료됨
- 실제 PBX/SBC trunk / register 경로가 end-to-end 검증됨
- 2노드 orchestrator + Redis HA 동작이 검증됨
- rollback 절차를 실제로 리허설함

## 3. P0 체크리스트

### 3.1 보안 경계

- [x] 상용 환경에서 FreeSWITCH ESL `8021` 호스트 publish 제거
  - 상태: [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml) 기준 host publish 제거 완료, `expose`만 유지
  - 목표: orchestrator 컨테이너/네트워크에서만 접근 가능
  - 검증 기준: `docker compose -f docker-compose.yml -f docker-compose.prod.yml config` 결과에 `published: "8021"` 이 없어야 함

- [x] 상용 환경에서 Bridge 오디오 WebSocket `8090` 호스트 publish 제거
  - 상태: [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml) 기준 host publish 제거 완료, 내부 네트워크 `expose`만 유지
  - 목표: FreeSWITCH 또는 내부 네트워크에서만 접근 가능
  - 검증 기준: `docker compose -f docker-compose.yml -f docker-compose.prod.yml config` 결과에 `published: "8090"` 이 없어야 함

- [x] 상용 환경에서 Redis `16379` 호스트 publish 제거
  - 상태: [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml) 및 [docker-compose.canary.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.canary.yml) 기준 host publish 제거 완료, 내부 `expose`만 유지
  - 목표: orchestrator/cluster 네트워크 내부 전용
  - 검증 기준: `docker compose -f docker-compose.yml -f docker-compose.prod.yml config` 결과에 `published: "16379"` 이 없어야 함

- [x] 하드코딩된 ESL 비밀번호를 env 기반 값으로 교체
  - 상태: [freeswitch-entrypoint.sh](/Users/kchul199/vbgw_v2/vbgw-freeswitch/scripts/freeswitch-entrypoint.sh)가 컨테이너 기동 시 `ESL_PASSWORD`를 `vars.xml`의 `default_password`, `esl_password`에 반영
  - 주의: 체크인된 [event_socket.conf.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/autoload_configs/event_socket.conf.xml)과 [vars.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/vars.xml)에는 dev 기본값이 남아 있으므로 운영 판정은 런타임 렌더링 결과로 확인
  - 검증 기준: FreeSWITCH 런타임과 orchestrator가 동일한 회전된 `ESL_PASSWORD`로 접속

- [x] 상용 환경에서 Redis 인증 활성화
  - 상태: [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml)이 `REDIS_PASS`가 있으면 Redis를 `--requirepass`로 기동하고, orchestrator에 같은 `REDIS_PASS`를 전달
  - 상태: [.env.example](/Users/kchul199/vbgw_v2/vbgw-freeswitch/.env.example)에 32자 이상 `REDIS_PASS` 기준 반영
  - 검증 기준: 인증 없는 `PING` 실패, 인증 포함 시 정상, 애플리케이션 health 유지

- [x] 상용 `WS_ALLOWED_ORIGINS` 명시
  - 상태: [.env.example](/Users/kchul199/vbgw_v2/vbgw-freeswitch/.env.example)에 운영 allowlist 예시 반영, `validate_prod.sh`가 빈 값 또는 `*`를 실패 처리
  - 목표: 허용할 웹/관리 Origin만 allowlist에 등록
  - 완료 기준: 미허용 Origin은 차단되고 허용 Origin만 통과

- [~] TLS / SRTP 상용값 및 인증서 점검
  - 상태: production overlay는 `SIP_TLS_ONLY=true`, `SRTP_MODE=mandatory`를 강제하고 TLS 인증서 파일은 존재
  - 현재 경고: 로컬 `.env` 기준 `SRTP_MODE=optional`, `SIP_TLS_ONLY=false`, `AI_GRPC_TLS=false`가 `validate_prod.sh`에서 WARN으로 남음
  - 완료 기준:
    - `SIP_TLS_ONLY=true`
    - `SRTP_MODE=mandatory`
    - 내부 구조상 필요하면 `AI_GRPC_TLS=true`
    - `config/freeswitch/tls` 내 인증서 유효

### 3.2 상용 트래픽 정확성

- [x] 상용에서 `AI_LOAD_TEST_GREETING_BYPASS` 비활성화
  - 상태: [docker-compose.prod.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.prod.yml)이 `AI_LOAD_TEST_GREETING_BYPASS=false`를 강제
  - 목표: production overlay에서 반드시 `false`
  - 완료 기준: staging/prod 검증 시 실제 TTS greeting 사용

- [~] `9196` load-test 전용 튜닝을 상용 경로와 분리
  - 현재:
    - [default.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/dialplan/default.xml)에 SIPp용 RTP timeout 완화
    - [10_load_test.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/dialplan/public/10_load_test.xml)에 public load-test 진입점 존재
  - 상태: production overlay는 greeting bypass를 끄지만, 실제 운영 DID/대표번호가 테스트 확장에 의존하지 않는다는 Stage 3/4 증적이 필요
  - 완료 기준: 실제 운영 번호 흐름에서 테스트 전용 확장이 필요하지 않음

- [ ] `9196/9296/9297/9298/9390` 외 실제 서비스 번호 검증
  - 완료 기준:
    - 실제 대표번호
    - service routing
    - queue
    - human fallback
    - SIP extension slot
    가 모두 운영 번호 기준으로 확인됨

- [ ] 실제 PBX/SBC 기준 trunk mode, register mode 모두 검증
  - 2026-06-03 preflight: 실제 PBX/SBC 대상 미구성 확인
  - 2026-06-03 Kamailio surrogate: trunk/register 대역 검증 통과. 증적은 [stage4_pbx_sbc_validation_evidence_20260603.md](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docs/stage4_pbx_sbc_validation_evidence_20260603.md)
  - 남은 범위: 실제 고객/운영 PBX/SBC endpoint 기준 trunk/register 검증
  - 완료 기준:
    - inbound
    - outbound / handoff
    - standby failover
    - gateway health 상태 전이
    가 모두 확인됨

### 3.3 운영 / HA

- [x] Redis 장애를 readiness/health에 반영
  - 현재: Stage 5에서 Redis down 중 `/ready=200`, `/health.status=healthy`로 남던 문제가 관찰됨
  - 목표: Redis-backed session/capacity store가 unavailable이면 readiness와 health가 즉시 degraded/not-ready로 반영
  - 완료 기준: Redis down 중 `/ready=503`, `/health.redis=unreachable`; Redis 복구 후 healthy로 회복
  - 2026-06-03 P0 수정: `/ready`가 Redis down 중 `503`, `/health`가 `status=degraded`, `redis=unreachable`을 반환하도록 수정
  - 2026-06-03 검증: Redis 복구 후 `/ready=200`, `/health.status=healthy`, `active_calls=0`

- [x] Redis-backed active call counter reconciler 추가
  - 현재: Stage 5 시작 전 `/health.active_calls=98`, service active 합계 0, lease count 0인 stale counter drift가 관찰됨
  - 목표: process crash, Redis 장애, 비정상 call 종료 후 `vbgw:active_calls`가 session/lease inventory와 자동 수렴
  - 완료 기준: stale counter 주입 후 reconciler 또는 운영 API가 drift를 탐지/정정하고 신규 admit이 잘못 차단되지 않음
  - 2026-06-03 P0 수정: startup/periodic reconciler 추가, Redis session TTL 잔존분 대신 fresh cluster heartbeat + local active count 기준 사용
  - 2026-06-03 추가 수정: release/rollback decrement를 floor-decrement Lua로 변경해 `active_calls < 0` 방지
  - 2026-06-03 검증: post-P0 Stage 4 콜 후 `/health.active_calls=0`

- [x] distributed lease renew 실패를 degraded/admit 정책에 반영
  - 현재: [manager.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/capacity/manager.go) 에서 renew 에러를 사실상 무시
  - 목표: renew 실패가 반복되면 node/service를 degraded 처리하고 신규 admit 차단
  - 완료 기준: 2-node Redis 장애/복구 시험에서 slot 중복 할당이 발생하지 않음
  - 2026-06-03 P0 수정: renew 실패 또는 renew miss 발생 시 capacity manager가 `distributed lease renew degraded` 상태로 신규 admit 차단
  - 2026-06-03 검증: `TestManagerRenewFailureBlocksNewAdmits` 통과

- [ ] cluster 전체 active session 관제 제공 또는 local-only임을 명확히 표기
  - 현재: `/api/v1/admin/sessions/active` 는 [admin.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/api/admin.go) 에서 `ForEachLocal`만 사용
  - 목표: 운영자가 cluster 전체 active call 수를 정확히 파악 가능
  - 완료 기준: 2노드 검증에서 aggregate count 정확

- [ ] node drain을 “수렴형 drain workflow”로 강화
  - 현재: state 전환은 있으나 drained completion이 운영 계약의 중심은 아님
  - 목표: drain 요청 → 진행률 → 완료 상태가 분명해야 함
  - 완료 기준: rolling deploy 시 신규콜 유입 없이 기존콜 자연 소진
  - 2026-06-03 Stage 5: 단일 노드 drain 중 신규 SIPp call은 `486 Busy Here`로 차단되고 resume 후 `grpc_recvs=184`로 복구됨

- [x] 2-node stale lease recovery / standby admit 정책 검증
  - 완료 기준:
    - slot 중복 할당 없음
    - 한 노드 장애 시 stale lease 회수
    - standby 노드에서 신규 admit 계속 처리
    - compatibility gate 정상 동작
  - 2026-06-03 Stage 5: fake stale lease는 API에서 `owner_heartbeat_missing`으로 식별되고 reaper가 회수함을 확인
  - 2026-06-03 P0 검증: `TestManagerDistributedLeaseStaleReapAllowsStandbyAdmit`에서 node-a heartbeat loss + stale lease aging 후 node-b 신규 admit 성공 확인

- [ ] live 2-container orchestrator failover 리허설 수행
  - 현재: 코드/테스트 레벨 2-node 정책은 통과했지만 실제 compose 2-container 동시 운영 리허설은 별도 필요
  - 완료 기준:
    - 두 orchestrator container heartbeat가 `/cluster/nodes`에 동시에 표시
    - 한 container 중지 후 stale lease 회수
    - standby container API에서 신규 admit 계속 처리
    - compatibility gate 정상 동작

### 3.4 모니터링 / 컷오버 준비

- [x] `validate_prod.sh`에 신규 차단 항목 반영
  - 반영된 항목:
    - `AI_LOAD_TEST_GREETING_BYPASS=false`
    - 상용에서 `8021`, `8090`, `16379` host publish 금지
    - Redis 인증 활성화
    - secret 길이 및 placeholder 금지
    - CORS/WS origin allowlist 필수화
  - 남은 항목:
    - PBX/SBC 실검증 flag와 Stage 3/4 증적 파일 연동

- [ ] 컷오버 증적 묶음 준비
  - 필수 산출물:
    - 최종 `validate_prod.sh` 결과
    - 실제 TTS 포함 부하테스트 결과
    - PBX trunk/register 실콜 로그
    - HA failover 검증 결과
    - rollback rehearsal 기록

## 4. 실제 수정 순서

실제 작업은 아래 순서가 가장 안전합니다.

### 4.0 P0 수정 패키지

`P0`는 아래 네 묶음입니다.

- `P0-A`: 제어면 포트 노출 제거
- `P0-B`: ESL secret source of truth 통일
- `P0-C`: 부하테스트 전용 동작을 상용에서 분리
- `P0-D`: Redis 인증 end-to-end 연결

이 네 가지는 현재 코드/compose 기준으로 대부분 반영되었습니다. 상용 판정의 남은 핵심은 Stage 3~7 실검증 증적입니다.

### Step 1. 테스트 전용 동작이 상용으로 새지 않게 분리

수정 파일:

- [.env.example](/Users/kchul199/vbgw_v2/vbgw-freeswitch/.env.example)
- [docker-compose.prod.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.prod.yml)
- [default.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/dialplan/default.xml)
- [10_load_test.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/dialplan/public/10_load_test.xml)
- [/Users/kchul199/vbgw_v2/vbgw-ai/internal/config/config.go](/Users/kchul199/vbgw_v2/vbgw-ai/internal/config/config.go)

작업:

- production overlay에서 `AI_LOAD_TEST_GREETING_BYPASS=false` 강제
- `9196`과 public load-test 경로를 dev/lab 전용으로 분리
- `9196`의 `rtp_timeout_sec=60`이 SIPp 전용임을 문서화

즉시 확인:

- `docker compose -f docker-compose.yml -f docker-compose.prod.yml config | rg AI_LOAD_TEST_GREETING_BYPASS`
- 상용 번호가 `9196`을 참조하지 않아야 함
- public load-test dialplan이 dev/lab 외 환경에서 비활성

메모:

- 이 항목은 `P0-C`
- 현재 production overlay에는 bypass off 강제가 반영됨

### Step 2. 제어면 포트 노출 제거

수정 파일:

- [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml)
- [docker-compose.prod.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.prod.yml)
- 실제 운영에서 쓰는 ingress / deployment overlay

작업:

- 상용에서 `8021`, `8090`, `16379` host publish 제거
- PBX/SBC 또는 운영 ingress에 꼭 필요한 포트만 외부 오픈
- healthcheck는 내부 네트워크 기준으로 계속 동작하도록 유지

즉시 확인:

- `docker compose -f docker-compose.yml -f docker-compose.prod.yml ps`
- 호스트에서 `8021`, `8090`, `16379` 접속 실패
- 컨테이너 간 health endpoint는 정상

메모:

- 이 항목은 `P0-A`
- [docker-compose.canary.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.canary.yml) 도 함께 정리해야 함. 현재 `REDIS_ADDR=redis:6379` 같은 예전 가정이 남아 있음

### Step 3. FreeSWITCH ESL 비밀번호 단일화

수정 파일:

- [event_socket.conf.xml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/config/freeswitch/autoload_configs/event_socket.conf.xml)
- [freeswitch-entrypoint.sh](/Users/kchul199/vbgw_v2/vbgw-freeswitch/scripts/freeswitch-entrypoint.sh)
- [.env.example](/Users/kchul199/vbgw_v2/vbgw-freeswitch/.env.example)

작업:

- 컨테이너 시작 시 `ESL_PASSWORD`로 event socket 비밀번호 rewrite
- 체크인된 정적 dev 비밀번호 의존 제거
- 상용 배포 전 `ESL_PASSWORD` rotate

즉시 확인:

- FreeSWITCH 런타임 config에 회전된 비밀번호 반영
- orchestrator가 동일 비밀번호로 정상 접속
- 기존 static 비밀번호는 더 이상 통하지 않음

메모:

- 이 항목은 `P0-B`
- `vars.xml`은 이미 `ESL_PASSWORD`를 받고 있지만 `event_socket.conf.xml`은 아직 아님

### Step 4. Redis 인증 end-to-end 연결

수정 파일:

- [docker-compose.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.yml)
- [docker-compose.prod.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.prod.yml)
- [.env.example](/Users/kchul199/vbgw_v2/vbgw-freeswitch/.env.example)
- [config.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/config/config.go)
- [repository.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/session/repository.go)

작업:

- env/compose에 `REDIS_PASS` 추가
- Redis를 `--requirepass`로 기동
- orchestrator에 같은 secret 전달
- session store, queue, leases, pub/sub 정상 동작 확인

즉시 확인:

- 인증 없는 `redis-cli -p 16379 ping` 실패
- 인증 포함 `redis-cli -p 16379 -a <pass> ping` 성공
- orchestrator health 및 queue/lease 동작 정상

메모:

- 이 항목은 `P0-D`
- 런타임 코드와 compose/env wiring이 반영됨. 남은 작업은 스테이징에서 인증 없는 Redis 접근 실패와 앱 health 유지 증적 수집

### Step 5. HA 동작 보강

수정 파일:

- [manager.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/capacity/manager.go)
- [admin.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/api/admin.go)
- [admin_control.go](/Users/kchul199/vbgw_v2/vbgw-freeswitch/orchestrator/internal/api/admin_control.go)
- [operations_runbook.md](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docs/operations_runbook.md)

작업:

- lease renew 실패를 무시하지 않도록 변경
- cluster 전체 active session 집계 제공
- node drain 완료 기준과 운영 흐름 정리

### Step 6. 실제 의존성 포함 상용급 재검증

검증 순서:

1. 실제 TTS 포함 단일 노드 staging 검증: 완료, 2026-06-02 Stage 3 재검증 통과
2. PBX trunk mode E2E
3. PBX register mode E2E
4. 실제 운영 번호 기준 queue / human fallback / SIP extension slot 검증
5. 2-node orchestrator + Redis HA 검증

완료 기준:

- 제어면 host 노출 없음
- 2노드에서 slot duplication 없음
- 실제 TTS 부하테스트가 목표 성공률 충족
- `pbx-main`, `pbx-standby` failover 동작 확인

### Step 7. 자동화와 릴리즈 게이트 반영

수정 파일:

- [validate_prod.sh](/Users/kchul199/vbgw_v2/vbgw-freeswitch/scripts/validate_prod.sh)
- [operations_runbook.md](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docs/operations_runbook.md)
- 실제 운영 release / cutover 문서

작업:

- 신규 P0 항목을 자동 검증으로 승격
- `validate_prod.sh`를 참고 스크립트가 아니라 컷오버 게이트로 사용
- rollback 명령과 증적 수집 절차 추가

## 5. 최종 검증 매트릭스

상용 전환 전 아래 항목이 모두 녹색이어야 합니다.

| 영역 | 최소 증적 |
|------|-----------|
| 보안 | `8021`, `8090`, `16379` host publish 없음, Redis auth 활성, secret rotate 완료 |
| SIP | trunk inbound/outbound OK, register inbound/outbound OK |
| 미디어 | one-way audio 없음, 실제 TTS greeting 정상, queue/fallback 오디오 정상 |
| 성능 | 실제 TTS 포함 대표 경로 부하 검증 통과 |
| HA | 2-node orchestrator failover, stale lease recovery, drain/resume 검증. 2026-06-03 단일 노드 Stage 5는 drain/resume, bridge/AI/Redis recovery, stale lease reaping 통과 |
| 운영 | `validate_prod.sh` 통과, 대시보드/알람 정상, rollback rehearsal 완료 |

## 6. 권장 실행 플랜

가장 빠르고 안전한 순서는 아래입니다.

1. 제어면 보안 하드닝
2. 테스트 전용 설정 정리
3. Redis auth 연결
4. HA 보강
5. 실제 PBX staging 검증
6. 2-node 리허설
7. 상용 승인

Step 1 ~ 4는 코드/compose 기준으로 종료되었고, Stage 3 실제 TTS 단일 콜 검증도 통과했습니다. 이후 권고 판정은 Stage 4~7 검증 증적에 따라 결정합니다.

## 7. 다음 작업 세션 권장 순서

다음 코딩 세션에서는 아래 순서로 가는 것이 가장 효율적입니다.

1. Stage 3 스테이징 통합 검증 수행
2. 실제 TTS greeting 경로 증적 수집
3. 포털 조회/제어 토큰 경계, CDR, trace 증적 수집
4. PBX/SBC trunk/register 실검증 일정 확정
5. 2-node HA와 장애 주입 리허설 수행

1~3은 2026-06-02 기준 완료되었습니다. 다음 세션의 우선순위는 4~5입니다.

이 세션의 기대 산출물:

- 갱신된 P0 체크리스트
- `validate_prod.sh` 재실행 결과
- Stage 3 스테이징 검증 증적
