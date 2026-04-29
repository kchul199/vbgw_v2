# Phase 2 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 2 상세 설계를 정의한다.
Phase 2의 목적은 logical service별 동시 처리량을 `slot pool`로 제어하는 것이다.

## 1. 목적

Phase 2의 목표는 아래 네 가지다.

- 전역 세션 수 제한을 서비스 단위 용량 제어로 확장한다.
- logical service마다 독립적인 slot pool을 둔다.
- 과부하 시 `busy` 또는 `direct transfer`처럼 결정적인 동작을 제공한다.
- slot 점유 상태를 운영 화면과 메트릭에서 추적 가능하게 만든다.

## 2. 범위

### 포함

- service별 logical slot pool
- allocator (`round_robin` 우선)
- authoritative slot lease / release
- overflow 정책 `busy`, `direct_transfer`
- slot occupancy 메트릭과 admin 조회

### 제외

- queue
- human fallback
- gateway health-aware failover
- SIP extension slot backend
- multi-node distributed lease

## 3. 선행 조건

- Phase 1의 대표번호 ownership cutover가 안정화되어 있어야 한다.
- 세션에 `service_name`, `entry_number`가 일관되게 기록되어 있어야 한다.
- 각 서비스의 예상 동시호 수와 overflow 정책이 사전에 합의되어 있어야 한다.
- Phase 1에서 정의한 `routing.yaml delivery / rollback` 계약을 그대로 상속해야 한다.

## 4. 현재 상태와 문제점

현재 저장소는 `orchestrator/internal/session/repository.go`에서 전역 `vbgw:active_calls`를 기준으로만 capacity를 관리한다.

문제는 아래와 같다.

- `bot-main`과 `vip-bot`의 용량을 분리할 수 없다.
- 어떤 세션이 어떤 slot을 점유하는지 알 수 없다.
- 다섯 번째 `vip` 콜과 다섯 번째 `bot-main` 콜을 다르게 처리할 방법이 없다.
- 과부하 시 운영정책이 아니라 단순 capacity exceeded에 가깝다.

## 5. 핵심 설계 결정

### 5.1 slot은 우선 logical token이다

- Phase 2의 slot은 실제 SIP extension이 아니다.
- slot은 service별 동시 처리 허용 토큰이며, `bot-main-01` 같은 논리 ID만 가진다.

### 5.2 두 단계 admission을 사용한다

- dynamic dialplan 단계에서는 "현재 capacity hint"만 참조한다.
- authoritative lease는 세션이 로컬 store에 생성된 직후에 수행한다.

이 구조를 쓰는 이유는 아래와 같다.

- dialplan 단계에서만 capacity를 보면 race를 피하기 어렵다.
- 세션이 실제로 만들어진 뒤 lease를 잡아야 release 경로도 일관된다.

### 5.3 전역 cap은 safety net으로 유지한다

- 현재 `MAX_SESSIONS` 기반 전역 cap은 제거하지 않는다.
- 서비스별 slot pool이 1차 정책이고, 전역 cap은 시스템 보호용 최후 제한으로 남긴다.

## 6. 설정 모델

Phase 2에서 `routing.yaml`은 v2로 확장한다.

```yaml
version: 2

defaults:
  on_unknown_entry: static_fallback

services:
  - name: bot-main
    enabled: true
    route_type: ai
    entry_numbers: ["1000", "5551212"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]
    capacity:
      max_concurrent: 10
      allocator: round_robin
      overflow_policy: busy

  - name: vip-bot
    enabled: true
    route_type: ai
    entry_numbers: ["2000"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]
    capacity:
      max_concurrent: 2
      allocator: round_robin
      overflow_policy: direct_transfer
      transfer_target: "3000"
```

ownership 원칙은 아래와 같다.

- `routing.yaml`: service capacity와 overflow policy
- `.env`: global max, feature gate, legacy fallback enable 여부
- Redis: live slot occupancy와 lease 상태

### 6.1 Schema migration 계약

현재 로더는 `version: 1`만 허용하므로, Phase 2의 capacity 필드는 아래 두 방식 중 하나로만 도입한다.

1. `version: 1`을 유지한 채 optional field를 허용하도록 loader/validator를 먼저 확장
2. `version: 2`를 도입하되 dual-loader와 rollback 아티팩트를 함께 배포

이행 전에는 아래를 금지한다.

- policy 파일만 먼저 배포하는 방식
- Orchestrator binary와 `routing.yaml` schema를 별도로 버전 업하는 방식

즉, Phase 2 구현은 "capacity manager 구현"과 "schema migration"이 한 릴리스 단위여야 한다.

## 7. 데이터 모델

세션 모델에는 아래 필드를 추가한다.

- `slot_id`
- `allocation_state` (`pending`, `leased`, `released`, `overflowed`)
- `overflow_policy`

추가 패키지는 아래를 권장한다.

- `orchestrator/internal/capacity/model.go`
- `orchestrator/internal/capacity/allocator.go`
- `orchestrator/internal/capacity/store.go`
- `orchestrator/internal/capacity/service_state.go`

## 8. 코드 변경 지점

- `orchestrator/internal/session/model.go`
- `orchestrator/internal/session/repository.go`
- `orchestrator/internal/api/dialplan.go`
- `orchestrator/cmd/main.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/metrics/prometheus.go`
- 신규 `orchestrator/internal/capacity/*`

구성요소 책임은 아래와 같다.

- `routing`: service와 정적 capacity 정책 제공
- `capacity`: lease / release / overflow 판단
- `session`: 세션과 slot의 결합 기록
- `admin`: 서비스별 점유 현황 노출

## 9. 런타임 흐름

### 9.1 정상 admit

1. Phase 1 resolver가 service를 결정
2. 세션 생성 직후 capacity manager가 authoritative lease 시도
3. lease 성공 시 `slot_id`를 session에 기록
4. AI bridge/IVR 흐름을 계속 진행

### 9.2 overflow

1. capacity manager가 free slot을 찾지 못함
2. `overflow_policy=busy`면 즉시 busy XML 또는 hangup cause로 종료
3. `overflow_policy=direct_transfer`면 지정된 target으로 transfer
4. `overflow` 결과를 메트릭과 세션 이벤트에 기록

### 9.3 release

1. `CHANNEL_HANGUP_COMPLETE` 또는 강제 종료 발생
2. 세션 종료와 함께 slot lease를 해제
3. 운영 메트릭을 갱신

## 10. 운영과 관측 기준

필수 메트릭은 아래와 같다.

- `vbgw_service_active_calls{service=...}`
- `vbgw_slot_in_use{service=...,slot=...}`
- `vbgw_overflow_total{service=...,policy=...}`
- `vbgw_routing_selection_failures_total`

admin API는 아래 조회를 제공해야 한다.

- 서비스별 현재 점유 수
- 서비스별 max_concurrent
- 현재 점유 slot 목록
- 최근 overflow 이벤트

## 11. 테스트 전략

### 단위 테스트

- allocator round-robin 순환
- slot lease / release
- same service concurrency race
- overflow policy branching

### 통합 테스트

- `bot-main` 10콜 admit 후 11번째 콜 overflow
- `vip-bot`의 direct transfer 동작 확인
- hangup 후 slot 반환 확인

### 회귀 테스트

- global `MAX_SESSIONS` safety net이 여전히 동작하는지 확인
- session metadata와 admin API가 일치하는지 확인

## 12. Acceptance Criteria

- 서비스별 동시호 수 제한이 실제로 분리되어 동작한다.
- 과부하 시 `busy` 또는 `direct transfer`가 deterministic하게 실행된다.
- hangup 이후 slot 누수가 없다.
- 어떤 콜이 어떤 서비스/slot을 사용했는지 audit 가능하다.
- 전역 cap과 서비스별 cap이 충돌 없이 동작한다.
- schema migration 실패 시 직전 정책 파일로 안전하게 rollback 가능하다.
