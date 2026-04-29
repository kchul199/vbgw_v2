# Phase 3 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 3 상세 설계를 정의한다.
Phase 3의 목적은 `pbx-main -> pbx-standby` 전환을 health-aware하게 만들고, interconnect를 운영형 구조로 고도화하는 것이다.

## 1. 목적

Phase 3의 목표는 아래 다섯 가지다.

- gateway health를 read-only 관측값에서 실제 라우팅 입력으로 승격한다.
- register / trunk 모드의 health 기준을 명확히 분리한다.
- outbound originate와 transfer가 동일한 gateway 선택 정책을 사용하게 한다.
- stale health 정보와 timeout 의존 fallback을 줄인다.
- interconnect 상태를 운영 메트릭과 admin API에서 추적 가능하게 만든다.

## 2. 범위

### 포함

- gateway health snapshot store
- register / trunk별 health classification
- health freshness TTL
- gateway selector
- originate / attended transfer / human fallback에서의 standby 선택

### 제외

- queue
- hunt / pilot
- SIP extension slot
- 다중 노드 health consensus

## 3. 선행 조건

- Phase 1의 service routing이 안정화되어 있어야 한다.
- `pbx-main`, `pbx-standby`의 canonical ID가 env와 runtime에서 일치해야 한다.
- gateway 생성 책임이 `scripts/freeswitch-entrypoint.sh`에 고정되어 있어야 한다.
- register 모드와 trunk 모드의 health source가 합의되어 있어야 한다.
- Phase 1의 ownership matrix가 운영 기준으로 확정되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 interconnect 관련 핵심 파일은 아래와 같다.

- `scripts/freeswitch-entrypoint.sh`
- `orchestrator/internal/esl/commands.go`
- `orchestrator/cmd/pbx_health.go`
- `orchestrator/internal/api/calls.go`

현재 상태의 문제는 아래와 같다.

- `calls.go`는 `UseStandbyGW bool`만 보고 dial string을 만든다.
- `commands.go`는 `pbx-main|pbx-standby`를 단순 문자열로 붙인다.
- health는 관측용에 가깝고, freshness 개념이 없다.
- register/trunk의 상태 해석이 동일 수준으로 취급된다.

## 5. 핵심 설계 결정

### 5.1 gateway health DTO와 selector를 분리한다

- `routing.GatewayState`는 snapshot DTO로 유지한다.
- 실제 상태 수집과 판단은 신규 `interconnect` 패키지가 담당한다.

### 5.2 health source는 모드별로 다르게 본다

- register 모드: `REGED`, retry 상태, registration freshness
- trunk 모드: `UP/NOREG`, OPTIONS 응답, gateway ping freshness

### 5.3 policy 파일은 gateway health를 소유하지 않는다

- gateway identity와 probe 간격은 `.env`와 bootstrap이 소유한다.
- `routing.yaml`은 health state를 직접 담지 않는다.

이렇게 해야 runtime truth와 static policy가 뒤섞이지 않는다.

### 5.4 ingress failover와 outbound failover를 구분한다

- inbound 라우팅의 우선권은 upstream PBX/SBC가 가진다.
- Phase 3의 primary/standby selector는 outbound originate, human fallback transfer, attended transfer에 우선 적용한다.
- inbound failover는 `source_gateway` 관측과 ownership matrix 검증의 대상이지, Orchestrator가 직접 결정하는 범위를 넘지 않는다.

## 6. 설계 모델

권장 패키지는 아래와 같다.

- `orchestrator/internal/interconnect/model.go`
- `orchestrator/internal/interconnect/store.go`
- `orchestrator/internal/interconnect/probe.go`
- `orchestrator/internal/interconnect/selector.go`

핵심 모델:

- `GatewayHealth`
  - `gateway_name`
  - `mode`
  - `observed_status`
  - `health_class`
  - `observed_at`
  - `freshness_ttl`
  - `reason`

- `SelectionPolicy`
  - `prefer_primary`
  - `allow_standby`
  - `fail_fast_when_stale`

## 7. 코드 변경 지점

- `orchestrator/internal/esl/commands.go`
- `orchestrator/internal/api/calls.go`
- `orchestrator/internal/api/control.go`
- `orchestrator/internal/api/server.go`
- `orchestrator/internal/metrics/prometheus.go`
- `orchestrator/cmd/pbx_health.go`
- 신규 `orchestrator/internal/interconnect/*`

변경 방향은 아래와 같다.

- `UseStandbyGW bool`를 `GatewaySelector` 기반 선택으로 대체
- outbound originate와 transfer가 동일한 selector를 사용
- `/health`는 snapshot 요약을 계속 제공하되, routing input 여부를 명확히 분리

## 8. 설정 ownership

- `.env`
  - gateway ID
  - register/trunk 모드
  - probe interval
  - freshness TTL
  - standby enable 여부
- `routing.yaml`
  - logical service가 어떤 gateway class를 요구하는지의 논리적 참조만 가능
- FreeSWITCH XML / bootstrap
  - gateway 생성
  - ping / register transport / auth / contact parameter

## 9. 런타임 흐름

### 9.1 health snapshot 수집

1. background poller가 일정 간격으로 gateway 상태 수집
2. register/trunk 모드에 맞는 health class 계산
3. snapshot store에 최신 상태 저장

### 9.2 outbound originate

1. `/api/v1/calls` 요청 수신
2. selector가 primary snapshot freshness와 health class 확인
3. primary가 healthy면 primary dial string 사용
4. primary가 stale 또는 degraded면 standby 허용 정책에 따라 fallback
5. 선택 결과와 reason을 로그/메트릭에 기록

### 9.3 attended transfer / human fallback

1. transfer 대상이 외부 PBX/SBC인 경우 동일 selector 사용
2. 실패 시 defined fallback reason에 따라 standby 재시도 가능

### 9.4 Failure cause matrix

standby 전환의 기준은 최소한 아래 매트릭스를 가져야 한다.

| 상황 | primary 재시도 | standby 전환 |
|------|----------------|--------------|
| health snapshot stale | 없음 | 즉시 standby 검토 |
| `INVALID GATEWAY`, `-ERR` | 없음 | 즉시 standby |
| register mode 미등록 | 없음 | standby |
| trunk mode `DOWN` / `FAILED` | 없음 | standby |
| SIP `NORMAL_TEMPORARY_FAILURE` 계열 | 1회 제한적 | standby 허용 |
| user busy / not found | 없음 | standby 금지 |

실제 cause 분류는 ESL 응답과 hangup cause를 함께 사용해 구현한다.

## 10. 운영과 관측 기준

필수 메트릭은 아래와 같다.

- `vbgw_gateway_health{gateway,mode,class}`
- `vbgw_gateway_health_age_seconds{gateway}`
- `vbgw_failover_decisions_total{reason,selected_gateway}`
- `vbgw_gateway_probe_failures_total{gateway}`

admin API는 아래를 제공해야 한다.

- gateway별 current health snapshot
- freshness age
- selection reason
- 최근 failover 이벤트

## 11. 테스트 전략

### 단위 테스트

- register mode health classification
- trunk mode health classification
- stale snapshot 판단
- primary/standby selector matrix

### 통합 테스트

- main healthy 시 primary originate
- main stale 시 standby 전환
- standby disabled 시 fail-fast
- attended transfer에서 selector 재사용 검증

### 회귀 테스트

- `pbx_health.go`의 기존 `gatewayHealthy()` 판단이 새로운 health class와 정합한지 확인
- health snapshot이 없을 때 보수적으로 동작하는지 확인

## 12. Acceptance Criteria

- primary가 healthy하면 primary가 항상 우선된다.
- primary가 stale 또는 unhealthy면 정의된 정책에 따라 standby로 예측 가능하게 전환된다.
- register와 trunk 모드의 health 기준이 문서와 코드에서 일치한다.
- outbound originate와 transfer가 동일한 gateway 선택기를 사용한다.
- 모든 failover 결정에는 reason과 freshness 근거가 남는다.
- ingress ownership과 outbound failover ownership이 서로 혼동되지 않는다.
