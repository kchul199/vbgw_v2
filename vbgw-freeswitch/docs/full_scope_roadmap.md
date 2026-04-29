# VBGW Full Scope 개발 로드맵

이 문서는 VBGW를 PBX/SBC 연동형 AI 콜봇 게이트웨이로 확장하기 위한 full scope 개발 로드맵을 정리한다.
목표는 외부 연동 방식을 `register`, `trunk`, `hunt`, `pilot`, `DID`까지 폭넓게 수용하면서,
내부에서는 일관된 `service -> slot pool -> routing policy -> overflow/failover` 모델로 운영하는 것이다.

> 구현 범위 주의:
> full scope의 최종 목표에는 `hunt`, `pilot`, `DID`까지 포함되지만,
> 초기 구현과 운영 계약은 `register + trunk`를 우선 대상으로 한다.
> `hunt`와 `pilot`은 번호 ownership과 SIP 계약이 정리된 뒤 후속 단계에서 확장한다.

## 1. 배경

현재 저장소는 다음 수준까지 구현되어 있다.

- FreeSWITCH `pbx-main`, `pbx-standby` 게이트웨이 bootstrap
- `register` / `trunk` 기반 PBX interconnect 최소 지원
- Orchestrator 기반 AI dynamic dialplan
- Redis 기반 세션 수 제한
- Bridge / AI 연계 오디오 스트리밍

반면 아직 full scope 관점에서 부족한 부분은 다음과 같다.

- 대표 진입번호와 논리 서비스의 분리가 없음
- `AI_ROUTE_NUMBERS` 기반의 단순 AI 진입만 지원
- 서비스별 slot pool, routing policy, overflow 정책이 없음
- gateway health가 메트릭 위주이며 실제 라우팅 결정에 깊게 반영되지 않음
- queue / human fallback / service failover가 운영형으로 구현되지 않음

## 2. 로드맵 원칙

- FreeSWITCH는 SIP/미디어 실행 계층에 집중한다.
- Orchestrator는 정책 판단과 상태 관리의 단일 진실 원천이 된다.
- 외부 연동 방식과 내부 처리 방식은 분리한다.
- 1차 릴리스는 단순하고 확실한 경로부터 구현한다.
- queue, extension slot, 다중 노드 HA는 단계적으로 추가한다.

## 3. 목표 상태

최종 목표 상태는 아래와 같다.

- 외부 연동
  - `register`, `trunk`, `standby gateway` 공존
  - 필요 시 `hunt pilot`, DID, 대표번호 기반 진입
- 내부 처리
  - `entry number -> logical service -> slot pool -> routing policy`
  - overflow는 `busy`, `queue`, `failover_service`, `fallback_to_human`
- 운영
  - 정책은 `.env`가 아닌 `routing.yaml` 중심으로 관리
  - gateway health, service capacity, queue depth, overflow 현황 관측 가능
  - 장애 시 `pbx-main -> pbx-standby` 전환과 service overflow 정책이 일관되게 적용

## 4. 단계별 개발 로드맵

### Phase 0. Baseline 정리와 정책 계층 도입 준비

목표:

- 대표 진입번호 ownership을 명확히 정리한다.
- 정책 파일 도입을 위한 골격을 만든다.
- 레거시 dialplan 충돌을 제거할 준비를 한다.

핵심 작업:

- 현재 번호 체계 정리
  - `1000`, `2000`, `5551212`의 기존 의미와 충돌 분석
- `routing.yaml` 스키마 초안 정의
- `orchestrator/internal/routing` 패키지 설계
- `AI_ROUTE_NUMBERS` 기반 구조에서 정책 기반 구조로 전환 계획 수립
- gateway health 정보를 향후 라우팅 판단에 연결할 데이터 모델 정의

주요 산출물:

- `docs/phase0_detailed_design.md`
- `routing.yaml` 초안 스키마
- 대표 진입번호 ownership 표
- 파일별 변경 계획

완료 기준:

- 대표번호와 레거시 dialplan 충돌 제거 방안이 문서화되어 있다.
- Phase 1 구현에 필요한 패키지 구조와 책임 분리가 합의되어 있다.
- `routing.yaml`과 env의 ownership 경계가 문서화되어 있다.
- gateway health는 Phase 0에서 read-only 관측값이며, 라우팅 결정 입력은 Phase 3부터라는 점이 명시되어 있다.

### Phase 1. Service Routing MVP

목표:

- 대표번호를 logical service로 매핑하는 최소 실행 경로를 구현한다.

착수 전제:

- Phase 0 산출물로 `ownership migration 표`가 승인되어 있어야 함
- `1000`, `2000`, `5551212`의 최종 owner와 rollback 조건이 확정되어 있어야 함
- `routing.yaml`이 참조할 stable gateway ID 목록이 확정되어 있어야 함

핵심 작업:

- `routing.yaml` 로더 구현
- `entry_number -> service` 매핑 구현
- `AI_ROUTE_NUMBERS` 제거 또는 호환 래퍼로 축소
- Orchestrator dynamic dialplan이 대표번호 ownership을 갖도록 전환
- FreeSWITCH static dialplan에서 충돌 구간 정리

권장 범위:

- `1000`, `2000`, `5551212`만 우선 지원
- 정책은 `admit / busy / direct transfer`까지만
- queue는 제외
- `public` 컨텍스트는 trusted gateway admission만 담당하고, 정책 해석은 `default` 단계에서만 수행

완료 기준:

- 지정된 대표번호가 모두 Orchestrator 경유로 일관되게 처리된다.
- static XML과 dynamic XML 간 우선순위 충돌이 없다.
- legacy 번호와 대표번호의 이중 ownership이 남아 있지 않다.

### Phase 2. Slot Pool / Capacity Control

목표:

- 서비스별 동시 처리량을 logical slot pool로 제어한다.

핵심 작업:

- `service -> logical slot pool` 모델 추가
- allocator 구현
  - `round-robin`
  - `least-busy`
  - `sticky-by-caller` 중 일부 우선
- 세션 저장소 확장
  - `service_name`
  - `entry_number`
  - `slot_id`
  - `state`
- overflow 시 `busy` 또는 `direct transfer` 처리

완료 기준:

- 예: `bot-main` 10슬롯이면 11번째 콜은 정책적으로 busy 또는 transfer 된다.

### Phase 3. PBX/SBC Interconnect 고도화

목표:

- `pbx-main -> pbx-standby` 전환을 health-aware 하게 만든다.

핵심 작업:

- gateway health state 모델 추가
- originate 이전 라우팅 결정에 health 반영
- trunk/register 공통 추상화 보완
- failure cause별 fallback 정책 정의
- standby 전환 기준 문서화

완료 기준:

- 주 게이트웨이 장애 시 단순 timeout 의존이 아니라 정책적으로 standby 전환이 가능하다.
- `register` / `trunk` 모드별 health source와 freshness 기준이 문서화되어 있다.

### Phase 4. Overflow / Queue / Human Fallback

목표:

- slot 부족 시 운영 가능한 응답 경로를 제공한다.

핵심 작업:

- overflow policy 추가
  - `busy`
  - `queue`
  - `failover_service`
  - `fallback_to_human`
- queue 구현 전략 결정
  - Orchestrator-managed queue
  - 또는 FreeSWITCH native queue / callcenter 활용
- 대기 멘트, timeout, abandon 처리 추가

완료 기준:

- slot 부족 시 단순 드랍 없이 정책에 따라 안내, 대기, 전환이 가능하다.

### Phase 5. SIP Extension Slot 모드

목표:

- 필요 시 `1001~1010`을 실제 SIP slot처럼 보이게 하는 운영 모델을 지원한다.

핵심 작업:

- `logical slot`과 `sip extension slot` 동시 지원
- 대표번호 `1000 -> 1001~1010` hunt 스타일 매핑 옵션 추가
- 다계정 register pool 또는 trunk-backed extension exposure 모델 정리

완료 기준:

- 고객 요구에 따라 logical pool / SIP extension pool 중 하나를 선택할 수 있다.

### Phase 6. 운영 관측성 / 관리 도구 / 보안

목표:

- 운영 전환에 필요한 가시성과 관리 도구를 추가한다.

핵심 작업:

- 메트릭
  - `vbgw_service_active_calls`
  - `vbgw_slot_in_use`
  - `vbgw_service_queue_depth`
  - `vbgw_gateway_health`
  - `vbgw_routing_selection_failures_total`
  - `vbgw_overflow_total`
- Admin API
  - 현재 routing config
  - service 상태
  - slot 상태
  - drain / pause / resume
- 보안
  - trunk ACL
  - TLS/SRTP 옵션 정리
  - interconnect hardening

완료 기준:

- 운영팀이 현재 서비스 상태, capacity, overflow, gateway health를 API/대시보드로 확인할 수 있다.

### Phase 7. 다중 노드 / HA / 무중단 운영

목표:

- 단일 노드 장애와 배포 중단을 견딜 수 있는 분산 운영 구조를 만든다.

핵심 작업:

- 다중 Orchestrator 노드 간 slot ownership 일관성 확보
- Redis 기반 분산 allocator 보강
- rolling deploy / drain / graceful shutdown 시나리오 고도화
- mixed version 환경 검증

완료 기준:

- 노드 장애 시 신규콜은 자동 재분산되고, 기존 콜 영향이 최소화된다.

## 5. 권장 릴리스 컷

### Release 1

포함:

- Phase 0
- Phase 1
- Phase 2 일부

특징:

- 대표번호 -> service
- logical slot pool
- `busy` / `direct transfer`

Release 1 운영 결정표:

| 상황 | 허용 응답 |
|------|-----------|
| unknown entry | `static_fallback` |
| no capacity | `busy` 또는 `direct transfer` |
| gateway down | 신규 outbound는 `pbx-standby` 검토 전까지 fail-fast, inbound service routing은 계속 허용 |
| human fallback unavailable | `direct transfer` 금지, `busy` 또는 안내 후 종료 |

주의:

- Phase 4 전까지 queue / callcenter를 service overflow 용도로 사용하지 않는다.
- Release 1에서는 caller UX를 단순화하고, queue 대체 구현을 금지한다.

### Release 2

포함:

- Phase 2 완료
- Phase 3
- Phase 4 일부

특징:

- health-aware standby
- overflow 정책 확장
- human fallback 초안

### Release 3

포함:

- Phase 4 완료
- Phase 5
- Phase 6
- Phase 7

특징:

- queue 운영화
- SIP extension slot
- 운영 자동화 / HA

## 6. 공통 워크스트림

### Control Plane

- `routing.yaml`
- routing loader
- service resolver
- slot allocator

### SIP Interconnect

- register / trunk
- gateway health
- standby failover
- ACL / TLS / SRTP

### Media / App Flow

- dynamic dialplan
- bridge orchestration
- overflow playback
- transfer / fallback

### Operations

- metrics
- admin API
- alerting
- runbook

### QA

- SIP call flow
- overload
- gateway failover
- soak test
- chaos test

## 7. 주요 리스크

- 레거시 dialplan 번호 충돌
- static XML과 dynamic XML ownership 중복
- queue를 너무 이르게 도입할 경우 원인 분리 어려움
- trunk/register/hunt를 한 번에 묶으면서 SIP 세부정책이 불명확해질 위험
- 다중 노드 도입 전 slot ownership 모델이 불완전할 위험
- `routing.yaml`과 env가 동시에 정책을 소유하는 drift 위험
- gateway health를 관측값 단계에서 너무 일찍 라우팅 입력으로 오해할 위험

## 8. 최소 SIP 운영 계약

full scope 설계가 실제 현장 PBX/SBC와 맞으려면 최소한 아래 항목은 문서화된 계약으로 유지해야 한다.

- called number normalization 기준
- trusted peer / ACL 기준
- trunk와 register의 health probe 방식 차이
- transport (`udp`, `tcp`, `tls`)와 인증 방식
- failover를 유발하는 SIP 실패 cause
- retry / ping / timeout 기본값
- ingress context precedence
- caller ID / Diversion / History-Info 처리 방침

권장:

- Phase 0~1은 `register + trunk`만 계약 범위에 포함
- `hunt`, `pilot`는 후속 단계에서 별도 운영 계약으로 추가

## 9. 성공 판단 기준

다음 항목이 충족되면 full scope 방향이 안정적으로 진행 중이라고 본다.

- 대표 진입번호 ownership이 명확하다.
- 정책 변경이 `.env` 수정과 이미지 재빌드가 아니라 정책 파일 갱신으로 수렴한다.
- 서비스별 capacity와 overflow 정책이 관측 가능하다.
- main gateway 장애 시 standby 전환이 예측 가능하게 동작한다.
- queue / human fallback이 별도 단계로 분리되어 점진적으로 검증된다.
