# Phase 7 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 7 상세 설계를 정의한다.
Phase 7의 목적은 다중 노드, HA, 무중단 배포를 지원하는 분산 운영 구조를 완성하는 것이다.

## 1. 목적

Phase 7의 목표는 아래 다섯 가지다.

- 다중 Orchestrator 노드에서 slot ownership 일관성을 보장한다.
- 노드 장애 시 신규 콜이 자동 재분산되게 한다.
- rolling deploy와 node drain을 안전하게 수행한다.
- Bridge / Orchestrator / FreeSWITCH의 mixed version 배포를 제어한다.
- cluster-level 운영 상태와 lease freshness를 추적 가능하게 만든다.

## 2. 범위

### 포함

- Redis 기반 distributed slot lease
- node heartbeat / liveness
- graceful drain / rolling deploy
- crash recovery
- mixed-version compatibility gate

### 제외

- 다중 region active-active
- 완전한 control plane 분리
- 외부 consensus system 도입

## 3. 선행 조건

- Phase 2의 slot model과 release 규칙이 안정화되어 있어야 한다.
- Phase 3의 gateway selector와 Phase 4의 queue가 node-local/cluster 관점에서 분리되어 있어야 한다.
- Redis가 운영 기준의 durability / availability 정책을 가져야 한다.
- single-node에서 slot acquire/release, queue timeout, drain idempotency가 충분히 검증되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 저장소는 아래 수준까지는 다중 노드 힌트를 가진다.

- `orchestrator/internal/session/repository.go`
- `orchestrator/internal/session/pubsub.go`
- `WaitAllDrained()`
- node-targeted Pub/Sub command routing

하지만 아직 부족한 점은 아래와 같다.

- slot ownership이 node-local 메모리에 가깝다.
- crash 후 orphaned lease를 복구하는 구조가 없다.
- admin API가 cluster-wide 집계를 기본적으로 보장하지 않는다.
- rolling deploy 시 새 버전과 옛 버전의 policy/schema 호환 기준이 없다.

## 5. 핵심 설계 결정

### 5.1 distributed lease는 Redis 원자 연산으로 관리한다

- service slot lease는 Redis key 또는 hash를 단일 truth로 사용한다.
- node-local cache는 성능 최적화일 뿐 authoritative source가 아니다.

### 5.2 lease에는 owner와 TTL이 반드시 있어야 한다

- `owner_node_id`
- `session_id`
- `leased_at`
- `lease_ttl`
- `renewed_at`

이 값이 있어야 crash recovery와 stale lease 정리가 가능하다.

### 5.3 drain은 신규 admit 차단과 기존 세션 유지의 조합이다

- drain 상태에서는 새 lease를 잡지 않는다.
- 기존 세션은 강제로 끊지 않고 종료될 때까지 유지한다.
- timeout 초과 시에만 kill path를 사용한다.

### 5.4 mixed-version은 schema gate를 둔다

- `routing.yaml` version
- slot lease record version
- admin API version

이 세 가지는 호환 범위를 명시해야 한다.

### 5.5 Phase 7은 single-node semantics를 다시 바꾸지 않는다

Phase 7은 Phase 2~4의 의미를 분산 환경으로 확장하는 단계다.
아래 항목이 아직 불안정하면 Phase 7에 진입하지 않는다.

- slot acquire / release 의미
- queue 상태 전이와 media owner
- drain의 종료 조건

즉, multi-node는 single-node semantics가 닫힌 뒤에만 설계/구현한다.

## 6. 데이터 모델

Redis 키 예시:

- `vbgw:lease:service:{service}:slot:{slot_id}`
- `vbgw:lease:session:{session_id}`
- `vbgw:node:{node_id}:heartbeat`
- `vbgw:node:{node_id}:state`
- `vbgw:drain:{node_id}`

node state 예:

- `active`
- `draining`
- `paused`
- `offline`

## 7. 코드 변경 지점

- `orchestrator/internal/session/repository.go`
- `orchestrator/internal/session/pubsub.go`
- `orchestrator/internal/capacity/*`
- `orchestrator/cmd/main.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/api/control.go`
- `orchestrator/internal/metrics/prometheus.go`
- 신규 `orchestrator/internal/cluster/*`

구성요소 책임은 아래와 같다.

- `cluster`: node heartbeat, drain state, version compatibility
- `capacity`: distributed lease acquire / renew / release
- `session`: local ownership과 cross-node command routing
- `admin`: cluster view와 drain control 노출

## 8. 런타임 흐름

### 8.1 distributed lease acquire

1. 서비스 resolve
2. allocator가 Redis에서 candidate slot lease 시도
3. 성공 시 owner node와 session ID 기록
4. node-local 메모리에도 mirror 저장

### 8.2 lease renew

1. active session 동안 주기적으로 lease renew
2. renew 실패가 일정 횟수 이상이면 세션 상태를 degraded로 표시

### 8.3 crash recovery

1. 특정 node heartbeat가 만료
2. stale lease cleanup worker가 orphaned lease 식별
3. queue 또는 신규 admit가 그 slot을 다시 사용할 수 있게 회수

### 8.4 drain / rolling deploy

1. 운영자가 node를 `draining`으로 전환
2. 신규 lease 차단
3. 기존 세션은 `WaitAllDrained()`로 종료 대기
4. timeout 초과 시 정의된 강제 종료 경로 사용

## 9. 운영과 관측 기준

필수 메트릭은 아래와 같다.

- `vbgw_node_heartbeat_age_seconds{node=...}`
- `vbgw_node_state{node=...,state=...}`
- `vbgw_distributed_lease_total{result=...}`
- `vbgw_distributed_lease_stale_total`
- `vbgw_drain_sessions_remaining{node=...}`

admin API는 아래를 제공해야 한다.

- cluster node 목록과 상태
- node별 active session 수
- draining 여부
- stale lease 목록
- version compatibility 상태

## 10. 테스트 전략

### 단위 테스트

- lease acquire / renew / release
- stale lease cleanup
- drain state transition
- version compatibility check

### 통합 테스트

- 2개 이상 Orchestrator에서 same service slot 경쟁
- 한 노드 crash 후 lease 회수
- rolling deploy 중 신규 콜 admit 연속성
- cross-node control command routing 유지

### 카오스/운영 테스트

- Redis 지연 / 일시 disconnect
- node kill -9
- bridge/orchestrator 버전 혼합 배포
- 장시간 soak test

## 11. Acceptance Criteria

- 다중 노드에서 같은 slot이 이중 할당되지 않는다.
- 노드 장애 시 orphaned lease가 회수되고 신규 콜이 재분산된다.
- drain과 rolling deploy가 기존 세션을 가능한 한 유지한다.
- mixed-version 배포 시 지원 범위 밖 조합은 차단된다.
- 운영 API와 메트릭으로 cluster 상태와 stale lease를 확인할 수 있다.
- Phase 2~4의 single-node 의미와 상충하는 distributed shortcut이 도입되지 않는다.
