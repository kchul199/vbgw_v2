# Phase 4 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 4 상세 설계를 정의한다.
Phase 4의 목적은 slot 부족 상황에서 `busy`, `queue`, `failover_service`, `fallback_to_human`을 운영 가능한 정책으로 만드는 것이다.

## 1. 목적

Phase 4의 목표는 아래 다섯 가지다.

- slot 부족 시 단순 드랍이 아니라 정책 기반 응답을 제공한다.
- AI 대기열과 human fallback을 분리된 운영 개념으로 정리한다.
- queue 상태를 Redis와 admin API에서 관측 가능하게 만든다.
- human fallback 시 현재 제어 API와 bridge pause/resume을 재사용한다.
- FreeSWITCH `callcenter`는 선택적 어댑터로 제한한다.

## 2. 범위

### 포함

- overflow policy: `busy`, `queue`, `failover_service`, `fallback_to_human`
- Orchestrator-managed queue
- queue timeout / abandon / 안내 멘트 정책
- human fallback 시 transfer / bridge 제어
- queue depth / wait time 관측

### 제외

- SIP extension slot
- multi-node distributed fair queue
- 고급 ACD 전략 전체

## 3. 선행 조건

- Phase 2의 slot lease가 안정화되어 있어야 한다.
- Phase 3의 gateway selector가 human fallback에서 재사용 가능해야 한다.
- 상담원 연결 대상 또는 human queue target이 사전에 정의되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 queue와 human fallback 관련 코드 단서는 아래에 흩어져 있다.

- `config/freeswitch/autoload_configs/callcenter.conf.xml`
- `orchestrator/internal/api/control.go`
- `orchestrator/internal/api/calls.go`
- `bridge/internal/ws/server.go`

현재 문제는 아래와 같다.

- `callcenter.conf.xml`은 template 수준이며 multi-node 기준 운영 계약이 없다.
- overflow 상태를 저장하는 런타임 모델이 없다.
- human fallback은 transfer API로 가능하지만 정책 엔진과 연결되어 있지 않다.
- queue 상태를 운영에서 확인할 수 없다.

## 5. 핵심 설계 결정

### 5.1 기본 queue는 Orchestrator-managed queue로 구현한다

- 이유: AI slot queue는 service state와 강하게 결합되므로 Orchestrator가 소유하는 편이 자연스럽다.
- `callcenter`는 human 상담원 큐가 필요할 때 선택적으로 연결한다.

### 5.2 AI queue와 human queue를 같은 개념으로 보지 않는다

- AI queue는 slot이 비면 다시 AI service로 admit되는 대기열이다.
- human fallback은 특정 외부 target 또는 PBX queue로 전환되는 정책이다.

### 5.3 queue에 들어간 콜은 세션을 유지한 채 상태만 바꾼다

- queue는 새 콜을 만들지 않는다.
- 세션 상태를 `queued`로 바꾸고, wait timer와 안내 재생 상태를 추가한다.

### 5.4 queue의 media owner를 명시적으로 정의한다

queue는 통화 연결 여부와 미디어 소유권을 먼저 정의해야 한다.

- `pre-answer queue`: 통화 응답 전 queue. 과금 전 대기 또는 early media 정책이 필요할 때 사용한다.
- `post-answer queue`: 통화 응답 후 queue. 현재 AI dialplan처럼 `answer -> audio_fork`가 먼저 일어나는 경로에서 사용한다.

Phase 4의 기본 계약은 아래와 같다.

- AI service queue는 `post-answer queue`를 기본값으로 둔다.
- queue 진입 순간 AI stream은 pause 또는 미생성 상태여야 한다.
- queue 안내 멘트와 hold audio의 owner는 FreeSWITCH다.
- queue에서 AI service로 재진입하는 시점에만 `audio_fork` / AI session을 활성화한다.

이 계약 없이 queue를 구현하면 묵음, 중복 과금, slot 누수, barge-in 충돌이 발생할 수 있다.

## 6. 설정 모델

Phase 4에서 `routing.yaml`은 아래 필드를 추가한다.

```yaml
services:
  - name: bot-main
    capacity:
      max_concurrent: 10
      allocator: round_robin
      overflow:
        policy: queue
        max_wait_seconds: 20
        announcement: "ivr/queue-wait.wav"
        on_timeout: busy

  - name: vip-bot
    capacity:
      max_concurrent: 2
      allocator: round_robin
      overflow:
        policy: fallback_to_human
        target: "3000"
        announce_before_transfer: true
```

ownership 원칙은 아래와 같다.

- `routing.yaml`: overflow policy와 service-level timeout
- `.env`: default queue tick, max queue scan size, human fallback feature gate
- FreeSWITCH XML: hold music, optional human queue adapter

## 7. 데이터 모델

세션에 아래 필드를 추가한다.

- `state` (`active`, `queued`, `overflowed`, `transferring`)
- `queue_name`
- `queue_entered_at`
- `queue_timeout_at`
- `overflow_reason`

신규 패키지는 아래를 권장한다.

- `orchestrator/internal/overflow/model.go`
- `orchestrator/internal/overflow/queue.go`
- `orchestrator/internal/overflow/dispatcher.go`

Redis 저장 구조 예:

- `vbgw:queue:{service}`: waiting session ID 목록
- `vbgw:queue:{service}:meta`
- `vbgw:queue:wait:{session_id}`

## 8. 코드 변경 지점

- `orchestrator/internal/api/dialplan.go`
- `orchestrator/cmd/main.go`
- `orchestrator/internal/session/model.go`
- `orchestrator/internal/session/repository.go`
- `orchestrator/internal/api/control.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/metrics/prometheus.go`
- 신규 `orchestrator/internal/overflow/*`
- `config/freeswitch/autoload_configs/callcenter.conf.xml`
- `bridge/internal/ws/server.go`

구성요소 책임은 아래와 같다.

- `overflow`: queue admission / dequeue / timeout / fallback dispatch
- `control`: human fallback transfer와 AI pause/resume 재사용
- `bridge`: bridge pause/resume, queue announcement 보조 훅
- `callcenter.conf.xml`: human queue adapter가 필요한 경우에만 사용

## 9. 런타임 흐름

### 9.1 `busy`

1. slot lease 실패
2. overflow policy가 `busy`
3. 즉시 busy cause 또는 안내 후 종료

### 9.2 `queue`

1. slot lease 실패
2. overflow policy가 `queue`
3. 세션 상태를 `queued`로 변경하고 Redis queue에 등록
4. queue media owner를 FreeSWITCH로 전환하고 AI stream을 pause 또는 defer
5. 안내 멘트 재생 후 hold 상태 유지
6. slot이 비면 dequeue 후 AI service로 다시 admit
7. 재진입 시점에만 AI stream과 slot lease를 활성화
8. timeout 시 `on_timeout` 정책 실행

### 9.3 `failover_service`

1. 주 service의 slot 부족
2. target service capacity 확인
3. admit 가능하면 target service로 reroute
4. target도 가득 차면 재정의된 overflow 정책 실행

### 9.4 `fallback_to_human`

1. AI service overflow 또는 명시적 escalation 발생
2. 필요 시 `bridge/internal/ws/server.go`의 AI pause 경로 호출
3. `control.go` / ESL transfer를 사용해 human target으로 전환
4. 성공 시 세션 상태를 `transferring`으로 기록

### 9.5 상태 전이 계약

허용되는 상태 전이는 아래로 제한한다.

- `active -> queued`
- `queued -> active`
- `queued -> overflowed`
- `active -> transferring`
- `queued -> hangup`
- `transferring -> hangup`

각 전이에서 반드시 실행해야 하는 정리 동작:

- slot release
- queue membership 정리
- AI pause/resume 또는 stream close
- timeout timer 정리

## 10. 운영과 관측 기준

필수 메트릭은 아래와 같다.

- `vbgw_service_queue_depth{service=...}`
- `vbgw_queue_wait_seconds{service=...}`
- `vbgw_overflow_total{service=...,policy=...}`
- `vbgw_human_fallback_total{service=...,target=...}`
- `vbgw_queue_abandon_total{service=...}`

admin API는 아래를 제공해야 한다.

- 서비스별 queue depth
- 대기 중 세션 목록
- 예상 wait time
- 최근 fallback 이벤트

## 11. 테스트 전략

### 단위 테스트

- queue enqueue / dequeue
- timeout branching
- failover_service recursion guard
- human fallback target validation

### 통합 테스트

- service full 상태에서 queue 진입과 dequeue
- timeout 후 busy 응답
- AI overflow 후 human fallback transfer
- queue 중 hangup 시 상태 정리

### 회귀 테스트

- queue 상태에서 세션 release가 누수 없이 끝나는지 확인
- `callcenter.conf.xml`을 사용하지 않는 기본 모드에서 정상 동작하는지 확인

## 12. Acceptance Criteria

- slot 부족 시 정책별 동작이 deterministic하다.
- queue 상태를 운영 API와 메트릭으로 추적할 수 있다.
- timeout, abandon, hangup 처리에 세션/slot 누수가 없다.
- human fallback은 AI pause/resume과 transfer 흐름을 깨지 않는다.
- `callcenter`는 선택적 adapter이며 기본 동작을 오염시키지 않는다.
- queue의 media owner와 AI 재진입 시점이 문서와 구현에서 일치한다.
