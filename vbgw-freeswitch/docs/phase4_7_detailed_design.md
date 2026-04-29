# VBGW Phase 4~7 상세 설계 초안

이 문서는 현재 `vbgw-freeswitch` 코드베이스를 기준으로 Phase 4~7의 상세 설계 초안을 정리한다.
대상 런타임은 아래 세 축이다.

- FreeSWITCH: SIP admission, media execution, PBX/SBC interconnect, human queue primitive
- Orchestrator: 라우팅 정책, 세션/슬롯/큐 상태, Admin API, 운영 제어
- Bridge: AI gRPC 세션, 오디오 fork, barge-in, internal control endpoint

본 문서는 "지금 있는 코드에서 어디를 어떻게 확장해야 하는가"를 중심으로 작성한다.
즉, greenfield 설계가 아니라 현재 구현물과 운영 제약을 반영한 현실적인 다음 단계 설계다.

## 1. 현재 베이스라인 요약

현재 코드에서 이미 갖춘 기반은 아래와 같다.

- FreeSWITCH external gateway bootstrap
  - `scripts/freeswitch-entrypoint.sh`
  - `pbx-main`, `pbx-standby` gateway를 env 기반으로 생성
- Dynamic AI ingress routing
  - `orchestrator/internal/api/dialplan.go`
  - `orchestrator/internal/routing/*`
  - 현재는 `routing.yaml v1` 또는 `AI_ROUTE_NUMBERS` 기반으로 entry number를 AI service로 매핑
- Session runtime state
  - `orchestrator/internal/session/model.go`
  - `orchestrator/internal/session/repository.go`
  - `service_name`, `entry_number`, `source_gateway`, `ingress_stage`까지는 이미 세션 메타데이터로 보유 가능
- Distributed command routing
  - `orchestrator/internal/session/pubsub.go`
  - `control.go`의 일부 명령은 Redis Pub/Sub로 owning node에 전달 가능
- Basic operations and graceful shutdown
  - `orchestrator/cmd/main.go`
  - `fsctl pause`, session drain, bridge shutdown notify, Redis-backed active call count
- Bridge internal control endpoints
  - `bridge/internal/ws/server.go`
  - `/internal/health`, `/internal/ai-pause/{uuid}`, `/internal/ai-resume/{uuid}`, `/internal/shutdown`, `/internal/dtmf/{uuid}`
- Human transfer primitive
  - `orchestrator/internal/api/control.go`
  - `orchestrator/cmd/main.go`
  - 현재도 `IVRTransferTarget` 기반 단일 상담원 전환은 가능

반면 아직 없는 핵심 기능은 아래다.

- service별 slot allocator
- overflow policy 실행기
- AI queue와 human fallback의 운영형 상태 모델
- SIP extension slot 모드
- service/slot/queue 중심 관측성과 관리 API
- multi-node allocator lease, drain orchestration, mixed-version rollout contract

## 2. 공통 설계 원칙

Phase 4~7 전체에 공통으로 적용할 설계 원칙은 아래와 같다.

### 2.1 정책 판단은 Orchestrator가 소유한다

- FreeSWITCH는 SIP/media executor다.
- queue, overflow, slot, fallback, service state의 단일 진실 원천은 Orchestrator다.
- `routing.yaml`은 Phase 4부터 ingress mapping만이 아니라 capacity/overflow까지 확장된다.

### 2.2 FreeSWITCH primitive는 실행 계층으로만 사용한다

- `callcenter.conf.xml`은 human queue 실행 primitive로 사용한다.
- `dialplan/default.xml`은 helper extension과 queue/AI start trampoline 역할로 제한한다.
- XML-defined `agents`/`tiers`는 multi-node 공유 DB에 적합하지 않으므로, production shared truth로 쓰지 않는다.

### 2.3 Bridge는 "AI media session endpoint"로 유지한다

- Bridge는 queue/slot policy를 판단하지 않는다.
- Bridge는 admitted된 call에 대해서만 audio fork와 gRPC 세션을 유지한다.
- Phase 4부터는 "admission 전에 audio_fork를 무조건 시작"하는 현재 흐름을 줄이는 방향으로 조정한다.

### 2.4 Redis는 phase 4 이후의 runtime coordination plane이다

- active session count뿐 아니라 slot lease, queue ticket, drain flag, service pause flag를 Redis가 가진다.
- 단, 장기 영속 설정은 Redis가 아니라 `routing.yaml` 또는 별도 config 파일이 소유한다.

## 3. Phase 4. Overflow / Queue / Human Fallback

### 3.1 목표

- slot 부족 시 현재의 "capacity exceeded -> kill" 또는 단일 `IVRTransferTarget`에 의존하지 않고,
  서비스별 정책에 따라 `busy`, `queue`, `failover_service`, `fallback_to_human` 중 하나를 실행한다.
- human fallback은 현재의 `control.go`/`onChannelPark()`의 transfer primitive를 재사용하면서,
  운영형 human queue는 FreeSWITCH `mod_callcenter` 기반으로 수용한다.
- AI queue는 `callcenter`가 아니라 Orchestrator-managed queue를 기본으로 한다.

### 3.2 선행 조건

- Phase 0/1 수준의 `routing.yaml` ownership이 정리되어 있어야 한다.
- `service_name`, `entry_number`, `source_gateway` 메타데이터가 `SessionState`에 정상 반영되어야 한다.
- PBX gateway bootstrap과 dynamic dialplan이 안정화되어 있어야 한다.
- service별 logical capacity를 정의할 수 있는 정책 스키마 초안이 있어야 한다.

### 3.3 포함 범위

- `routing.yaml v2`에 overflow/capacity 필드 추가
- service별 logical slot pool과 queue ticket 모델 추가
- overflow action 4종 지원
  - `busy`
  - `queue`
  - `failover_service`
  - `fallback_to_human`
- human fallback mode 2종 지원
  - direct transfer
  - callcenter queue transfer
- queue timeout, abandon, max depth, drain-aware dequeue

### 3.4 제외 범위

- SIP extension slot mode 자체 구현
- slot을 실제 SIP REGISTER endpoint처럼 보이게 하는 기능
- cross-node active call migration
- external ACD와의 deep integration
- queue callback, scheduled callback, CRM sync

### 3.5 핵심 설계 결정

#### 결정 A. AI wait queue는 Orchestrator-managed queue를 사용한다

이유:

- `config/freeswitch/autoload_configs/callcenter.conf.xml`의 주석대로 XML agents/tiers는 multi-FS shared DB에 부적합하다.
- `callcenter`는 사람 agent 분배에는 적합하지만, AI slot lease/queue fairness/source-of-truth를 맡기기엔 현재 구조와 맞지 않는다.
- Orchestrator는 이미 Redis-backed state와 route metadata를 갖고 있으므로 queue ownership을 이어받기 쉽다.

결론:

- AI 대기열의 진실 원천은 Redis queue다.
- FreeSWITCH는 대기 중 caller media 유지, 안내 멘트 재생, transfer execution만 담당한다.

#### 결정 B. human fallback queue는 `mod_callcenter`를 기본 primitive로 사용한다

이유:

- 현재 코드에는 direct transfer primitive만 있고 human queue는 없다.
- `callcenter.conf.xml`에 이미 `support@default` queue 골격이 있다.
- 상담원 분배, wait music, abandon timeout은 FreeSWITCH native queue가 더 빠르게 운영형에 도달한다.

결론:

- Phase 4의 human fallback은 두 모드로 제공한다.
  - `direct_transfer`: 기존 `esl.Transfer()` 재사용
  - `callcenter_queue`: FreeSWITCH helper extension으로 `callcenter support@default` 실행

#### 결정 C. admission 전에 무조건 `uuid_audio_fork`를 시작하는 현재 구조를 분리한다

현재:

- `orchestrator/internal/api/dialplan.go`가 dynamic AI match 시 즉시
  - `answer`
  - `uuid_audio_fork start`
  - `playback silence_stream://-1`
  를 넣는다.

문제:

- queue 진입 call도 bridge/gRPC stream을 먼저 열게 된다.
- slot이 없어도 bridge capacity와 AI engine 세션을 점유한다.
- human fallback으로 바로 넘길 call도 needless media path를 생성한다.

결론:

- Phase 4부터 dynamic dialplan은 "park for policy decision"과 "AI media start"를 분리한다.
- recommended flow:
  1. dynamic dialplan은 route metadata만 세팅하고 call을 park
  2. `onChannelPark()`가 slot allocator/overflow policy를 실행
  3. admitted된 경우에만 dedicated AI start extension으로 transfer

#### 결정 D. human fallback target은 routing policy가 소유하고 `.env`의 `IVRTransferTarget`은 fallback default로 축소한다

- 현재 `IVRTransferTarget`은 global singleton이다.
- Phase 4 이후 human fallback target은 service별로 다를 수 있어야 한다.
- `.env`의 `IVRTransferTarget`은 "routing policy에 target이 없을 때의 안전 기본값"으로만 사용한다.

### 3.6 제안 스키마와 ownership

#### `routing.yaml v2` ownership

Owner:

- Orchestrator / Routing policy owner

필드 초안:

```yaml
version: 2

services:
  - name: bot-main
    enabled: true
    route_type: ai
    entry_numbers: ["1000", "5551212"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]
    capacity:
      slot_pool: "bot-main"
      max_active_calls: 10
      allocator: "least-busy"
    overflow:
      policy: "queue"
      queue_name: "bot-main-wait"
      max_queue_depth: 30
      max_wait_seconds: 120
      on_timeout: "fallback_to_human"
      fallback_target: "hf_support"
    human_fallback:
      mode: "callcenter_queue"
      queue_ref: "support@default"
```

#### FreeSWITCH config ownership

Owner:

- SIP/Voice platform owner

대상:

- `config/freeswitch/autoload_configs/callcenter.conf.xml`
- `config/freeswitch/dialplan/default.xml`
- 필요 시 `config/freeswitch/dialplan/default/10_vbgw_control.xml` 신규 분리

소유 책임:

- queue name, MOH, queue wait behavior, helper extension, callcenter application wiring

#### Redis runtime ownership

Owner:

- Orchestrator runtime

예상 keyspace:

- `vbgw:slot_pool:{service}`
- `vbgw:slot_lease:{service}:{slot_id}`
- `vbgw:queue:{queue_name}`
- `vbgw:queue_ticket:{ticket_id}`
- `vbgw:service_state:{service}`

### 3.7 코드 변경 지점

#### Orchestrator

- `orchestrator/internal/routing/model.go`
  - `capacity`, `overflow`, `human_fallback` 구조체 추가
- `orchestrator/internal/routing/validate.go`
  - `version: 2` 허용
  - queue/fallback target/slot_pool validation 추가
- `orchestrator/internal/api/dialplan.go`
  - immediate audio fork 대신 staged park dialplan 출력
  - AI start extension 메타데이터 export 방식 추가
- `orchestrator/cmd/main.go`
  - `onChannelPark()`에 allocator/overflow execution 삽입
  - queue timeout/background dequeue worker 시작
- `orchestrator/internal/session/model.go`
  - `SlotPool`, `SlotID`, `QueueName`, `QueueTicketID`, `OverflowPolicy`, `HumanFallbackMode`, `QueueEnteredAt` 추가
- `orchestrator/internal/session/repository.go`
  - slot lease, queue ticket persistence helper 추가
- 신규 패키지 권장
  - `orchestrator/internal/allocator`
  - `orchestrator/internal/queue`
  - `orchestrator/internal/service`
- `orchestrator/internal/esl/interface.go`
  - `TransferWithContext` 또는 `ExecuteExtension` 추가 검토
- `orchestrator/internal/esl/commands.go`
  - dedicated control extension transfer helper 추가
- `orchestrator/internal/api/admin.go`
  - queue/slot/service 조회 endpoint 추가 기반

#### FreeSWITCH

- `config/freeswitch/autoload_configs/modules.conf.xml`
  - `mod_callcenter` 로드 추가
- `config/freeswitch/autoload_configs/callcenter.conf.xml`
  - human fallback queue 정의 확장
  - 예: `support@default`, `sales@default`, `afterhours@default`
- `config/freeswitch/dialplan/default.xml`
  - AI start helper extension
  - human fallback helper extension
  - queue hold announcement helper extension

#### Bridge

- `bridge/internal/ws/server.go`
  - queued/non-admitted call에서는 WS/gRPC stream을 열지 않도록 phase 4 flow에 맞춘 contract 조정
- `bridge/internal/config/config.go`
  - internal auth token 추가 시 env load

### 3.8 런타임 흐름

#### 3.8.1 normal admit path

1. PBX call이 FreeSWITCH `public` 또는 `default` context로 진입한다.
2. `orchestrator/internal/api/dialplan.go`가 service metadata를 set하고 call을 policy park 상태로 보낸다.
3. `CHANNEL_CREATE`에서 `onChannelCreate()`가 세션을 만들고 source gateway를 기록한다.
4. `CHANNEL_PARK`에서 `onChannelPark()`가 route metadata를 읽는다.
5. allocator가 slot lease를 잡는다.
6. 성공 시 Orchestrator가 AI start helper extension으로 transfer한다.
7. helper extension이 `uuid_audio_fork`를 시작하고 `Bridge -> AI` path를 연다.

#### 3.8.2 overflow queue path

1. `CHANNEL_PARK`에서 slot lease 실패
2. service의 `overflow.policy=queue` 확인
3. Orchestrator가 Redis queue에 ticket enqueue
4. caller는 FreeSWITCH queue-hold helper extension으로 이동
5. background dequeue worker가 slot availability 감시
6. slot 할당 시 caller를 AI start helper extension으로 transfer
7. timeout 또는 abandon 시 policy에 따라
  - `fallback_to_human`
  - `busy`
  - `failover_service`
  중 하나 실행

#### 3.8.3 human fallback path

1. queue timeout 또는 immediate fallback policy 발생
2. routing policy에서 `human_fallback.mode` 확인
3. `direct_transfer`면 `esl.Transfer()`로 PBX target 전송
4. `callcenter_queue`면 helper extension으로 transfer
5. helper extension이 `callcenter <queue>@default` 실행

### 3.9 운영/관측 포인트

신규 메트릭 제안:

- `vbgw_service_active_calls{service}`
- `vbgw_slot_in_use{service,slot_pool}`
- `vbgw_service_queue_depth{service,queue}`
- `vbgw_overflow_total{service,policy,result}`
- `vbgw_human_fallback_total{service,mode,result}`
- `vbgw_queue_wait_seconds_bucket{service,queue}`

운영 로그 포인트:

- slot lease acquire/release
- queue enter/dequeue/timeout/abandon
- fallback target resolution
- callcenter transfer success/failure

운영 주의:

- 현재 저장소에는 `callcenter.conf.xml`이 있지만 `mod_callcenter`는 아직 로드되지 않는다.
- 따라서 Phase 4 구현 착수 시 `modules.conf.xml` 변경이 선행되어야 한다.
- `callcenter.conf.xml` XML agents/tiers는 phase 7 이전까지 shared cluster truth로 쓰지 않는다.
- human fallback queue name은 `routing.yaml`이 참조하지만 실제 queue semantics는 FreeSWITCH config owner가 유지한다.

### 3.10 테스트 전략

단위 테스트:

- `routing v2` validation
- allocator/queue Lua atomicity
- fallback target resolution
- `onChannelPark()` policy matrix

통합 테스트:

- queue depth 0/1/N
- queue timeout -> direct transfer
- queue timeout -> callcenter queue transfer
- failover_service 전환
- drain 중 신규 queue admission 차단

E2E 테스트:

- `docker compose` 기반 3-service stack + mock human fallback dial target
- `9196` style AI route를 queue-enabled service로 바꾼 시나리오
- voice path 미사용 queue caller가 bridge gRPC session을 점유하지 않는지 확인

### 3.11 Acceptance Criteria

- slot full 상황에서 서비스별 overflow policy가 일관되게 적용된다.
- queue 진입 caller가 admitted 전 bridge/gRPC capacity를 소모하지 않는다.
- human fallback이 direct transfer와 callcenter queue 두 방식 모두 동작한다.
- Redis/metrics/admin log에서 queue depth와 timeout 원인을 추적할 수 있다.
- queue 없는 서비스는 Phase 2 동작을 깨지 않고 그대로 유지된다.

## 4. Phase 5. SIP Extension Slot Mode

### 4.1 목표

- logical slot pool 외에 "실제 SIP extension처럼 보이는 slot" 운영 모델을 지원한다.
- 고객이 `1000 -> 1001~1010` hunt/pilot 스타일을 요구하는 경우, 동일 service를 logical slot이 아니라 SIP slot exposure로 운영할 수 있게 한다.
- current codebase의 `directory/default/*.xml`, `public.xml`, `default.xml`, gateway bootstrap과 양립 가능해야 한다.

### 4.2 선행 조건

- Phase 4의 slot allocator/overflow model이 먼저 안정화되어 있어야 한다.
- extension ownership migration이 완료되어야 한다.
  - 현재 `1000~1019`는 dev/local extension 의미가 남아 있다.
  - 실제 SIP slot으로 재사용하려면 기존 sample/Zoiper 내선 ownership 정리가 필수다.
- 대표번호 owner와 slot extension owner가 분리 문서화되어 있어야 한다.

### 4.3 포함 범위

- slot mode 2종 지원
  - `logical`
  - `sip_extension`
- service -> slot extension mapping
- pilot number / hunt group 스타일 ingress 지원
- slot drain/pause/resume API
- slot occupancy와 human fallback interplay

### 4.4 제외 범위

- per-slot REGISTER를 외부 carrier/provider에 따로 수행하는 구조
- external PBX user provisioning 자동화
- BLF/presence full feature
- shared line appearance

### 4.5 핵심 설계 결정

#### 결정 A. slot abstraction은 하나이고 exposure mode만 다르게 한다

즉:

- logical slot과 SIP extension slot은 서로 다른 시스템이 아니라 동일한 allocator contract를 공유한다.
- 차이는 "slot identifier가 internal logical ID인가, 외부에 노출되는 extension number인가"에 있다.

권장 모델:

```yaml
slot_mode:
  type: sip_extension
  slot_pool: bot-main-ext
  pilot_number: "1000"
  slot_extensions: ["1001", "1002", "1003", "1004"]
```

#### 결정 B. Phase 5의 SIP extension slot은 trunk ingress exposure가 기본이고 multi-register pool은 제외한다

이유:

- 현재 `scripts/freeswitch-entrypoint.sh`의 gateway bootstrap은 `pbx-main`/`pbx-standby` gateway 단위다.
- per-slot outbound REGISTER pool은 현재 bootstrap 모델과 맞지 않는다.
- trunk/register interconnect는 유지하고, PBX가 FS trunk로 slot extension DID를 라우팅하는 방식을 먼저 지원하는 것이 현실적이다.

결론:

- Phase 5의 "SIP extension slot mode"는 "external PBX가 extension 번호를 FS로 라우팅할 수 있는 운영 모델"까지를 목표로 한다.
- "slot마다 별도 REGISTER 계정"은 후속 phase 또는 별도 프로젝트로 분리한다.

#### 결정 C. slot extension dialplan과 local directory user는 분리한다

현재:

- `config/freeswitch/directory/default/1000.xml` 등 sample user가 존재한다.
- `config/freeswitch/dialplan/public.xml`도 `10xx`를 default로 넘긴다.

문제:

- `1001~1010`을 AI slot으로 쓰면 softphone dev user와 충돌한다.

결론:

- SIP slot range는 dedicated reserved range를 도입하는 것이 기본안이다.
  - 예: `1101~1110`
- 만약 고객 계약상 `1001~1010`이 꼭 필요하면, 기존 dev directory user는 legacy context/range로 이관해야 한다.

### 4.6 설정/스키마 ownership

#### `routing.yaml v3` ownership

Owner:

- Orchestrator / Routing owner

추가 필드:

```yaml
slot_mode:
  type: "sip_extension"
  pilot_number: "1000"
  slot_extensions: ["1101", "1102", "1103"]
  inbound_match: "exact_extension"
```

#### FreeSWITCH directory/dialplan ownership

Owner:

- SIP platform owner

대상:

- `config/freeswitch/directory/default/*.xml`
- `config/freeswitch/dialplan/public.xml`
- `config/freeswitch/dialplan/default.xml`

책임:

- reserved extension range
- public/default context에서 slot extension admission rule
- dev softphone extension과 slot extension 충돌 제거

### 4.7 코드 변경 지점

#### Orchestrator

- `orchestrator/internal/routing/model.go`
  - `slot_mode`, `pilot_number`, `slot_extensions` 추가
- `orchestrator/internal/routing/validate.go`
  - extension uniqueness와 overlap validation
- `orchestrator/internal/session/model.go`
  - `SlotMode`, `SlotExtension`, `PilotNumber` 추가
- 신규 권장 패키지
  - `orchestrator/internal/slotmode`
  - `orchestrator/internal/allocator`
- `orchestrator/internal/api/admin.go`
  - slot inventory, drain state, extension occupancy 조회 추가
- `orchestrator/internal/api/control.go`
  - slot drain/pause/resume management API 추가

#### FreeSWITCH

- `config/freeswitch/dialplan/public.xml`
  - reserved slot extension range admission
- `config/freeswitch/dialplan/default.xml`
  - slot extension -> AI trampoline route
- `config/freeswitch/directory/default/*.xml`
  - dev user와 slot range 정리

#### Gateway bootstrap

- `scripts/freeswitch-entrypoint.sh`
  - slot mode 자체보다는 contact parameter consistency 유지
  - register mode에서 PBX가 slot extension을 caller/callee identity로 사용할 때 contact extension formatting 확인

### 4.8 런타임 흐름

#### pilot number mode

1. PBX가 pilot number `1000`으로 FS에 호출
2. Orchestrator가 service를 resolve
3. allocator가 available slot extension `1102`를 선택
4. session에 `slot_extension=1102` 기록
5. AI media start 시 channel var/export에 slot extension을 넣어 CDR/ops 식별 가능하게 함

#### direct slot mode

1. PBX가 `1102`로 직접 호출
2. `public.xml`/`default.xml`이 slot extension을 service `bot-main`으로 매핑
3. Orchestrator는 logical service + concrete slot extension을 같이 기록
4. drain 상태인 slot이면 next slot 또는 overflow policy로 전환

### 4.9 운영/관측 포인트

신규 메트릭:

- `vbgw_slot_extension_in_use{service,slot_extension}`
- `vbgw_slot_extension_drained{service,slot_extension}`
- `vbgw_pilot_resolution_total{service,result}`

운영 포인트:

- dev softphone extension과 production AI slot range를 혼용하지 않는다.
- slot drain은 service drain보다 우선순위가 높다.
- slot extension 기반 운영 시 CDR와 admin API에서 logical service와 concrete slot extension을 동시에 보여줘야 한다.

### 4.10 테스트 전략

단위 테스트:

- slot mode schema validation
- extension overlap detection
- drained slot selection 회피

통합 테스트:

- pilot -> slot selection
- direct slot ingress
- slot drain 후 next slot selection
- overflow와 slot mode 동시 적용

운영 리허설:

- Zoiper 또는 SIPp로 reserved slot extension range 실제 호출
- existing dev extension과 충돌하지 않는지 검증

### 4.11 Acceptance Criteria

- logical mode와 sip_extension mode를 service 단위로 선택할 수 있다.
- pilot number ingress와 direct slot ingress가 모두 예측 가능하게 동작한다.
- slot drain/pause/resume이 routing result에 반영된다.
- extension ownership 충돌이 문서와 설정에서 제거된다.

## 5. Phase 6. 운영 관측성 / 관리 도구 / 보안

### 5.1 목표

- 운영자가 service, slot, queue, gateway, node 상태를 API/대시보드/로그로 즉시 파악할 수 있게 한다.
- 현재의 `/api/v1/admin/sessions/active` 단일 뷰를 service-centric control plane으로 확장한다.
- bridge internal endpoint, admin API, interconnect 설정, routing config 변경 경로를 운영 가능한 수준으로 hardening한다.

### 5.2 선행 조건

- Phase 4 queue/overflow model과 Phase 5 slot metadata가 세션에 반영되어 있어야 한다.
- Redis를 control plane으로 계속 사용할지 운영적으로 합의되어 있어야 한다.
- `JWT_SECRET`, `ADMIN_API_KEY`, ACL/TLS 운영 원칙이 정리되어 있어야 한다.

### 5.3 포함 범위

- service/slot/queue/gateway/node 수준 메트릭
- Admin API 확장
- drain/pause/resume/disable API
- routing config 조회 및 validation endpoint
- bridge internal auth
- FreeSWITCH interconnect hardening
- 감사 로그와 변경 이력

### 5.4 제외 범위

- full RBAC portal UI
- external IAM/SSO integration
- billing, charging, CRM console

### 5.5 핵심 설계 결정

#### 결정 A. Admin API는 "세션 조회"에서 "운영 제어 plane"으로 확장한다

현재:

- `orchestrator/internal/api/admin.go`는 active sessions list만 제공한다.

Phase 6 이후:

- service 상태 조회
- slot 상태 조회
- queue depth 조회
- node drain/pause
- service drain/pause
- routing config validation/diff 조회

를 제공한다.

#### 결정 B. bridge internal endpoint는 shared secret 또는 loopback+signed header로 보호한다

현재:

- `bridge/internal/ws/server.go`의 internal endpoints는 사실상 무인증이다.
- Orchestrator의 `notifyBridge()`와 `notifyBridgeHold()`도 인증 헤더를 보내지 않는다.

문제:

- same network 내 임의 호출 가능성이 남아 있다.

결론:

- `BRIDGE_INTERNAL_TOKEN` 도입
- Orchestrator -> Bridge 내부 요청에 `Authorization: Bearer <token>` 또는 `X-VBGW-Internal-Token` 부여
- Bridge는 loopback/docker-bridge restriction + token 둘 다 확인

#### 결정 C. gateway health는 read-only gauge에서 routing-affecting state로 승격하되, source provenance를 남긴다

현재:

- `orchestrator/cmd/main.go`의 monitor는 `vbgw_sip_registered`와 alarm gauge를 갱신한다.
- phase 3 수준의 local monitor다.

Phase 6:

- gateway state를 Admin API와 metrics로 노출
- observed source, timestamp, TTL, mode(register/trunk)를 함께 표준화
- phase 7에서 routing input으로 재사용할 수 있도록 schema를 안정화

### 5.6 설정/스키마 ownership

#### Orchestrator ownership

- `routing.yaml`
- service/slot/queue policy state
- admin API exposure contract
- Redis runtime state schema

#### SIP/FreeSWITCH ownership

- `config/freeswitch/sip_profiles/internal.xml`
- `config/freeswitch/sip_profiles/external.xml`
- `config/freeswitch/autoload_configs/acl.conf.xml`
- `config/freeswitch/autoload_configs/callcenter.conf.xml`
- TLS/SRTP certs and profile knobs

#### Bridge ownership

- `bridge/internal/config/config.go`
- internal auth token
- gRPC health/session metrics

### 5.7 코드 변경 지점

#### Admin / API

- `orchestrator/internal/api/admin.go`
  - `/api/v1/admin/services`
  - `/api/v1/admin/services/{service}/slots`
  - `/api/v1/admin/queues`
  - `/api/v1/admin/gateways`
  - `/api/v1/admin/nodes`
  - `/api/v1/admin/routing/config`
  - `/api/v1/admin/services/{service}/drain`
  - `/api/v1/admin/slots/{slot}/pause`
- `orchestrator/internal/api/server.go`
  - route registration
  - JWT scope 분리
- `orchestrator/internal/api/health.go`
  - health payload에 service queue, gateway freshness, bridge stream state 포함
- `orchestrator/internal/api/stats.go`
  - per-call stats에 service/slot/queue 메타데이터 노출

#### Metrics

- `orchestrator/internal/metrics/prometheus.go`
  - service/slot/queue/gateway/node 메트릭 추가
- `bridge/cmd/main.go`
  - bridge 자체 metrics endpoint 추가 검토
- `bridge/internal/ws/server.go`
  - active ws sessions, paused sessions, internal auth failures

#### Security

- `bridge/internal/ws/server.go`
  - internal auth middleware
- `orchestrator/internal/api/middleware.go`
  - admin scope/readonly scope 분리
- `scripts/freeswitch-entrypoint.sh`
  - generated gateway XML에 보안 관련 파라미터 검증/기본값 강화
- `.env.example`
  - internal token, routing config path, TLS guidance 추가

### 5.8 런타임 흐름

#### 운영 조회

1. 운영자는 `/api/v1/admin/services` 호출
2. Orchestrator는 Redis runtime state + local session snapshot을 합쳐 service view 생성
3. queue depth, active slots, drained slots, gateway dependencies, current routing config version을 응답

#### 운영 제어

1. 운영자가 `service drain` 요청
2. Orchestrator는 Redis에 drain flag를 기록
3. 새 admission은 차단되고 기존 call은 유지
4. queue dequeue worker도 drain-aware하게 신규 slot assign을 중지

### 5.9 운영/관측 포인트

필수 메트릭:

- `vbgw_service_active_calls`
- `vbgw_service_queue_depth`
- `vbgw_slot_in_use`
- `vbgw_slot_drained`
- `vbgw_gateway_health`
- `vbgw_gateway_health_age_seconds`
- `vbgw_bridge_active_ws_sessions`
- `vbgw_admin_command_total`
- `vbgw_admin_command_failures_total`
- `vbgw_routing_selection_failures_total`

필수 로그:

- admin actor, action, target, result
- routing config version change
- bridge internal auth failure
- gateway health state transition

필수 보안 점검:

- admin API 기본 secret fallback 금지
- bridge internal endpoint token 미설정 시 startup fail in production
- FreeSWITCH ACL/trusted gateway 정의 강제
- `event_socket.conf.xml` 비밀번호 하드코딩 제거 또는 entrypoint/env sync

### 5.10 테스트 전략

단위 테스트:

- admin auth scope
- internal token validation
- service/slot/queue aggregation serializer

통합 테스트:

- drain/pause/resume command propagation
- bridge internal endpoint auth
- degraded gateway health exposure

운영 테스트:

- SLO dashboard query validation
- unauthorized admin/bridge request denial
- config reload/validation dry-run

### 5.11 Acceptance Criteria

- 운영자가 service/slot/queue/gateway/node 상태를 API와 metrics로 동시에 볼 수 있다.
- drain/pause/resume 같은 운영 명령이 audit log를 남기며 성공/실패가 추적된다.
- bridge internal endpoint와 admin API가 production baseline에서 무인증으로 열리지 않는다.
- gateway health와 queue/overflow 지표가 장애 원인 분리에 충분한 수준으로 노출된다.

## 6. Phase 7. 다중 노드 / HA / 무중단 운영

### 6.1 목표

- Orchestrator와 media node를 여러 개 운영해도 slot/queue/service 상태가 일관되게 유지되도록 한다.
- rolling deploy, drain, node loss 상황에서 신규콜 분산과 기존 콜 유지가 가능해야 한다.
- 기존 call migration은 하지 않되, "기존 콜은 origin node에 남기고 신규콜만 재분산" 모델을 명확히 한다.

### 6.2 선행 조건

- Phase 4 queue/slot state가 Redis에 모델링되어 있어야 한다.
- Phase 6 admin/drain/health API가 준비되어 있어야 한다.
- current graceful shutdown (`fsctl pause`, bridge shutdown notify, WaitAllDrained)가 안정적으로 검증되어 있어야 한다.

### 6.3 포함 범위

- multi-orchestrator active-active
- node drain/cordon/uncordon
- slot lease TTL and fencing
- queue worker leaderless 또는 sharded consumer model
- rolling deploy during active traffic
- mixed-version compatibility contract

### 6.4 제외 범위

- active call live migration between FreeSWITCH nodes
- geo-distributed RTP anchoring
- cross-region Redis failover architecture

### 6.5 핵심 설계 결정

#### 결정 A. HA 단위는 "media node + owning orchestrator view"다

현 구조에서 active call은 FreeSWITCH channel UUID와 bridge WS session에 묶여 있다.
따라서 Phase 7에서도 기존 call을 다른 node로 옮기지 않는다.

결론:

- node failure 이전에 생성된 call은 해당 media node에서 종료까지 유지
- 신규콜만 healthy node로 분산
- allocator와 queue는 new assignment만 재조정

#### 결정 B. slot lease는 TTL 기반 distributed lease로 승격한다

현재 Redis store는 `active_calls`만 원자적으로 관리한다.

Phase 7:

- slot assign은 `SET NX PX` 또는 Lua 기반 fencing token을 사용
- owner node id와 lease expiry를 함께 저장
- queue dequeue 시 stale lease reclaim 가능해야 한다

#### 결정 C. `callcenter` shared truth는 phase 7에서도 node-local primitive로만 사용한다

이유:

- `callcenter.conf.xml` XML agents/tiers와 local DB는 shared multi-FS ACD로 적합하지 않다.
- 따라서 human fallback queue를 cluster-wide primary truth로 사용하면 안 된다.

결론:

- HA 환경의 human fallback 기본 권장 경로는 external PBX/ACD transfer다.
- `mod_callcenter`는 single media node local queue 또는 lab deployment에 한정한다.

#### 결정 D. bootstrap/generated config는 node deterministic 해야 한다

대상:

- `scripts/freeswitch-entrypoint.sh`
- gateway XML generation

원칙:

- node마다 gateway logical name contract는 동일해야 한다.
- node-specific 값은 env 또는 explicit node label로만 주입한다.
- mixed-version deploy 시 generated XML field set이 backward compatible 해야 한다.

### 6.6 설정/스키마 ownership

#### Redis runtime ownership

Owner:

- Orchestrator cluster runtime

key 예시:

- `vbgw:node:{node_id}:state`
- `vbgw:node:{node_id}:drain`
- `vbgw:slot_lease:{service}:{slot}`
- `vbgw:queue_ticket:{ticket}`
- `vbgw:service_worker:{service}`

#### Deployment ownership

Owner:

- Platform / SRE

대상:

- `docker-compose.prod.yml` 또는 후속 Helm/K8s manifest
- node labels, readiness/liveness policy, rollout order, drain hooks

### 6.7 코드 변경 지점

#### Orchestrator

- `orchestrator/internal/session/repository.go`
  - lease-aware operations
  - node state storage
- `orchestrator/internal/session/pubsub.go`
  - node broadcast, drain command, retry/ack contract
- 신규 권장 패키지
  - `orchestrator/internal/lease`
  - `orchestrator/internal/cluster`
  - `orchestrator/internal/worker`
- `orchestrator/cmd/main.go`
  - startup node register
  - drain hook
  - shutdown fencing
  - stale lease cleanup
- `orchestrator/internal/api/admin.go`
  - cluster/node operations
- `orchestrator/internal/api/health.go`
  - local vs cluster health 구분

#### FreeSWITCH / Bootstrap

- `scripts/freeswitch-entrypoint.sh`
  - node id/contact params/health tuning injection
- `config/freeswitch/autoload_configs/modules.conf.xml`
  - `mod_callcenter`를 사용하는 배포와 사용하지 않는 배포를 profile/feature flag로 분리 검토
- `config/freeswitch/sip_profiles/external.xml`
  - multi-node interconnect contract 확인
- `config/freeswitch/dialplan/public.xml`
  - trusted gateway admission rule가 multi-node에서도 동일하게 유지되는지 점검

#### Bridge

- `bridge/internal/ws/server.go`
  - node-local stream inventory and drain mode
- `bridge/cmd/main.go`
  - readiness semantics: accepting_new_sessions vs alive 분리

### 6.8 런타임 흐름

#### rolling deploy

1. admin 또는 deploy hook가 node drain 요청
2. Orchestrator는 Redis에 node drain flag 기록
3. local node는 `fsctl pause`로 신규콜 거부
4. dynamic dialplan resolver/allocator는 drained node slot을 신규 assign에서 제외
5. 기존 세션이 끝나면 node empty 상태로 전환
6. bridge는 new WS accept를 중단하고 existing session만 유지
7. node 종료 후 새 버전 기동
8. readiness 통과 뒤 drain 해제

#### node failure

1. node heartbeat stale
2. lease cleaner가 expired slot lease와 queue worker ownership reclaim
3. 신규콜은 remaining healthy node로만 배분
4. orphan된 existing call은 media node 손실이므로 복구하지 않음
5. 운영 지표에 `node_lost`, `lease_recovered` 이벤트 기록

### 6.9 운영/관측 포인트

필수 메트릭:

- `vbgw_node_sessions{node}`
- `vbgw_node_drained{node}`
- `vbgw_slot_lease_stale_total`
- `vbgw_queue_reassign_total`
- `vbgw_node_heartbeat_age_seconds`
- `vbgw_cluster_command_latency_seconds`

운영 규칙:

- mixed-version rollout 동안 `routing.yaml` schema backward compatibility 보장
- node drain 완료 전 hard kill 금지
- bridge readiness와 orchestrator readiness를 분리해서 본다

### 6.10 테스트 전략

단위 테스트:

- lease acquire/reclaim fencing
- stale node cleanup
- drain-aware allocator

통합 테스트:

- 2-node orchestrator + shared Redis
- drain node while active sessions exist
- restart one node during queue backlog
- standby gateway health flip under multi-node

카오스/운영 리허설:

- orchestrator process kill
- bridge process kill
- Redis reconnect
- pbx-main unavailable -> pbx-standby only mode

### 6.11 Acceptance Criteria

- 2개 이상의 Orchestrator node에서 신규콜이 race 없이 slot을 할당받는다.
- drain된 node는 신규콜을 받지 않고 기존콜만 종료까지 유지한다.
- rolling deploy 중 active call drop 없이 새 버전 전환이 가능하다.
- stale lease와 queue ownership이 자동으로 회수된다.
- human fallback 경로는 multi-node 환경에서 external PBX/ACD transfer 기준으로 안정 동작한다.

## 7. 단계 간 의존성과 권장 구현 순서

권장 순서는 아래다.

1. Phase 4에서 admission/overflow/queue를 정립한다.
2. Phase 5에서 slot exposure mode를 logical vs sip_extension으로 일반화한다.
3. Phase 6에서 운영 조회/제어/보안을 붙인다.
4. Phase 7에서 lease, drain, multi-node runtime을 올린다.

특히 아래 두 가지는 반드시 지킨다.

- `callcenter.conf.xml`은 Phase 4에서 human fallback primitive로 연결하되, Phase 7 cluster truth로 승격하지 않는다.
- `api/dialplan.go`의 immediate audio fork 구조는 Phase 4에서 staged admission 구조로 먼저 바꾼다.

## 8. 파일 단위 우선순위 제안

실제 구현 착수 우선순위는 아래 순서를 권장한다.

### 8.1 1차 착수

- `orchestrator/internal/routing/*`
- `orchestrator/internal/session/model.go`
- `orchestrator/cmd/main.go`
- `orchestrator/internal/api/dialplan.go`

### 8.2 2차 착수

- `orchestrator/internal/esl/interface.go`
- `orchestrator/internal/esl/commands.go`
- `config/freeswitch/dialplan/default.xml`
- `config/freeswitch/autoload_configs/callcenter.conf.xml`

### 8.3 3차 착수

- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/api/server.go`
- `orchestrator/internal/metrics/prometheus.go`
- `bridge/internal/ws/server.go`

### 8.4 4차 착수

- `orchestrator/internal/session/repository.go`
- `orchestrator/internal/session/pubsub.go`
- `scripts/freeswitch-entrypoint.sh`
- `docker-compose.prod.yml`

## 9. 최종 요약

이 설계 초안의 핵심은 아래 네 줄로 요약된다.

- Phase 4는 Orchestrator가 overflow/queue를 소유하고, FreeSWITCH `callcenter`는 human fallback 실행 primitive로만 연결한다.
- Phase 5는 slot abstraction을 일반화하고, SIP extension exposure를 logical slot 위에 얹는다.
- Phase 6은 세션 중심 API를 service/slot/queue/gateway 중심 운영 plane으로 확장하고 bridge internal 경로를 보안화한다.
- Phase 7은 기존 call migration 없이도 신규콜 재분산, drain, rolling deploy, stale lease recovery가 가능한 cluster runtime을 완성한다.
