# Phase 6 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 6 상세 설계를 정의한다.
Phase 6의 목적은 운영 관측성, 관리 도구, 보안을 운영 투입 수준으로 끌어올리는 것이다.

## 1. 목적

Phase 6의 목표는 아래 다섯 가지다.

- 서비스, 슬롯, queue, gateway health를 운영에서 바로 볼 수 있게 한다.
- admin API를 읽기/제어 면에서 체계화한다.
- audit log와 운영 이벤트 추적을 일관되게 만든다.
- trunk ACL, TLS/SRTP, internal API 보호를 정리한다.
- 운영 자동화와 runbook의 기준 데이터를 제공한다.

## 2. 범위

### 포함

- Prometheus 메트릭 확장
- admin API 확장
- audit/event log 설계
- 운영용 health/readiness 분리
- 보안 hardening 기준

### 제외

- SIEM 연동 전체
- 외부 IAM 통합 전체
- 장기 보관형 분석 파이프라인

## 3. 선행 조건

- Phase 1~5에서 service, slot, queue, gateway 모델이 정리되어 있어야 한다.
- production profile과 secrets 관리 기준이 존재해야 한다.
- Bridge, Orchestrator, FreeSWITCH 간 internal trust boundary가 정의되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 운영 관련 핵심 파일은 아래와 같다.

- `orchestrator/internal/metrics/prometheus.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/api/health.go`
- `bridge/internal/ws/server.go`
- `config/freeswitch/sip_profiles/internal.xml`

현재 문제는 아래와 같다.

- 메트릭이 세션 전역 중심이며 service/slot/queue 단위가 부족하다.
- admin API가 active session 조회 위주다.
- internal API와 WS origin 제한은 일부 개선되었지만 end-to-end 운영 기준이 문서화되지 않았다.
- gateway health, queue depth, overflow 이벤트를 하나의 운영 관점에서 묶기 어렵다.

## 5. 핵심 설계 결정

### 5.1 운영 메트릭은 logical resource 중심으로 정의한다

- call count보다 service, slot, queue, gateway를 1급 지표로 본다.

### 5.2 admin API는 read path와 control path를 분리한다

- read API: 현황 조회
- control API: pause, resume, drain, force-release, reroute

### 5.3 보안 ownership을 명확히 분리한다

- `.env`: secret, key, TLS toggle, internal allowlist
- `routing.yaml`: service policy
- FreeSWITCH XML: SIP profile, ACL, codec, transport

## 6. 관측 설계

필수 메트릭은 아래와 같다.

- `vbgw_service_active_calls{service=...}`
- `vbgw_slot_in_use{service=...,slot=...}`
- `vbgw_service_queue_depth{service=...}`
- `vbgw_queue_wait_seconds{service=...}`
- `vbgw_gateway_health{gateway=...,mode=...,class=...}`
- `vbgw_failover_decisions_total{reason=...,selected_gateway=...}`
- `vbgw_overflow_total{service=...,policy=...}`
- `vbgw_routing_config_loaded`
- `vbgw_route_resolution_total{result=...}`

권장 대시보드 영역:

- ingress / route resolution
- service capacity / slot occupancy
- queue / overflow
- gateway health / failover
- bridge / AI / barge-in 상태

## 7. Admin API 설계

권장 read API:

- `GET /admin/sessions/active`
- `GET /admin/services`
- `GET /admin/services/{name}`
- `GET /admin/slots`
- `GET /admin/queues`
- `GET /admin/gateways`
- `GET /admin/routing/config`

권장 control API:

- `POST /admin/services/{name}/pause`
- `POST /admin/services/{name}/resume`
- `POST /admin/services/{name}/drain`
- `POST /admin/queues/{name}/flush`
- `POST /admin/slots/{slot_id}/force-release`
- `POST /admin/gateways/{name}/standby`

기존 `orchestrator/internal/api/admin.go`는 read path의 출발점으로 재구성한다.

### 7.1 Destructive control API 거버넌스

아래 제어 API는 destructive control로 분류한다.

- queue flush
- force-release
- gateway standby/disable
- drain

모든 destructive control은 아래 4개 계약을 가져야 한다.

- `권한`: read API와 별도 scope 또는 stronger auth 필요
- `감사`: 요청자, 대상, 사유, timestamp를 audit log에 남김
- `ack`: accepted / completed / failed 상태를 비동기적으로 추적 가능
- `rollback`: 가능한 경우 원복 경로 또는 operator runbook 제공

즉, Phase 6 문서에서 control API는 "엔드포인트 이름"보다 "권한/감사/ack/rollback"이 먼저 정의되어야 한다.

## 8. 보안 hardening 설계

### Orchestrator

- `RUNTIME_PROFILE=production` 강제
- 강한 `ADMIN_API_KEY`, `JWT_SECRET`
- admin API rate limit 유지
- internal-only route는 allowlist 또는 shared secret 추가

### Bridge

- `allowedOrigins`를 명시된 서버 간 연결과 운영 콘솔 Origin으로 제한
- `/internal/*` 엔드포인트에 shared secret 또는 mTLS 고려

### FreeSWITCH

- `accept-blind-reg`, `accept-blind-auth` 금지
- trusted trunk ACL 유지
- TLS/SRTP는 단계별 feature gate로 도입
- 운영용 external SIP/RTP 광고값을 startup 시 자동 동기화

## 9. 코드 변경 지점

- `orchestrator/internal/metrics/prometheus.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/api/health.go`
- `orchestrator/internal/api/middleware.go`
- `bridge/internal/ws/server.go`
- `bridge/internal/config/config.go`
- `config/freeswitch/sip_profiles/internal.xml`
- `config/freeswitch/vars.xml`
- `operations_runbook.md`

## 10. 런타임 흐름

### 10.1 운영 조회

1. 운영 콘솔이 admin read API 호출
2. service / slot / queue / gateway 상태 취합
3. 현재 cluster-local 정보와 Redis-backed 정보 조합

### 10.2 제어 명령

1. 운영자가 drain 또는 pause 요청
2. 해당 서비스/노드에 command 전파
3. 현재 세션은 유지하고 신규 admit만 막거나, 필요한 경우 graceful drain 수행

### 10.3 보안 검증

1. startup 시 production profile validation 수행
2. secret/TLS/ACL 누락 시 fail-fast

## 11. 테스트 전략

### 단위 테스트

- metric registration
- admin serialization
- middleware auth / rate limit
- origin / allowlist validation

### 통합 테스트

- `/admin/services`, `/admin/queues`, `/admin/gateways` 응답 검증
- pause/drain control 후 신규 admit 차단 확인
- TLS/ACL misconfiguration 시 startup failure 확인

### 운영 검증

- 대시보드 샘플 쿼리 검증
- alert rule dry-run
- runbook 단계별 smoke test

## 12. Acceptance Criteria

- 운영팀이 서비스, 슬롯, queue, gateway 상태를 API와 메트릭으로 확인할 수 있다.
- admin read path와 control path가 분리되어 있다.
- production profile에서 필수 보안 항목 누락 시 fail-fast 한다.
- internal API와 WS 연결이 최소한의 trust boundary를 가진다.
- runbook과 실제 API/메트릭 이름이 서로 일치한다.
- destructive control API가 권한/감사/ack/rollback 계약 없이 노출되지 않는다.
