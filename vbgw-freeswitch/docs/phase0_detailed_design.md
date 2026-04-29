# Phase 0 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 0 상세 설계를 정의한다.
Phase 0의 목적은 새로운 정책 계층을 바로 운영에 투입하는 것이 아니라,
대표 진입번호 ownership을 정리하고 `routing.yaml` 기반 라우팅 계층을 도입할 준비를 완료하는 것이다.

## 1. 목적

Phase 0의 목표는 아래 네 가지다.

- 대표 진입번호를 누가 소유하는지 명확히 한다.
- 현재 env 기반의 단순 라우팅 구조를 정책 기반 구조로 전환할 토대를 만든다.
- 레거시 FreeSWITCH dialplan과 새 logical service 모델의 충돌을 제거한다.
- Phase 1부터 구현 가능한 코드 구조와 설정 구조를 확정한다.

## 2. 범위

### 포함

- current-state 분석
- 대표 진입번호 ownership 설계
- `routing.yaml` 초기 스키마 설계
- Orchestrator `internal/routing` 패키지 구조 설계
- FreeSWITCH / Orchestrator 책임 분리 정의
- 세션 모델 확장 방향 정의
- 테스트 및 검증 기준 정의

### 제외

- slot allocator 구현
- queue 구현
- human fallback 구현
- SIP extension slot 구현
- gateway health-aware failover의 실행 로직 구현

즉, Phase 0은 "정책 엔진 골격"과 "번호 ownership 정리"까지다.

## 3. 현재 상태 진단

### 3.1 Dynamic AI 진입 구조

현재 Orchestrator는 `AI_ROUTE_NUMBERS` 기반으로 AI 진입 번호를 관리한다.

- 파일: `orchestrator/internal/config/config.go`
- 파일: `orchestrator/internal/api/dialplan.go`

특징:

- `AI_ROUTE_NUMBERS`에 포함된 번호만 dynamic AI dialplan으로 응답
- 나머지는 `not found`를 반환하여 static XML로 fallback
- 서비스명, slot pool, overflow 정책 개념이 없음

문제:

- 번호 추가/삭제가 env 변경으로 귀결됨
- 서비스 단위 정책을 표현할 수 없음
- 대표번호 ownership을 명확히 관리하기 어려움
- ingress 식별 키가 사실상 `destination_number + Hunt-Context + sip_gateway_name`인데,
  문서화된 정책 모델이 아직 이를 반영하지 못함

### 3.2 FreeSWITCH 진입 경로

현재 FreeSWITCH는 아래 경로로 외부 콜을 받아들인다.

- `config/freeswitch/dialplan/public.xml`
- `config/freeswitch/dialplan/public/00_inbound_did.xml`
- `config/freeswitch/dialplan/default.xml`

현재 충돌 지점:

- `5551212`는 static DID 예제로 `1000`으로 transfer
- `2000`은 `group_dial_sales`
- `1000`은 실제 local extension user

즉, `1000`, `2000`, `5551212`를 logical service의 대표 진입번호로 쓰려면
현재 static dialplan ownership과 충돌이 발생한다.

### 3.2.1 대표번호 ownership 현황과 권장 컷오버

| 번호 | 현재 owner | 현재 동작 | 목표 owner | Phase 1 컷오버 권장 |
|------|------------|-----------|------------|---------------------|
| `1000` | local extension | `user/1000@default` | `bot-main` | 대표번호로 승격하고, 개발용 로컬 내선 의미는 별도 legacy/dev 경로로 분리 |
| `2000` | sample group dial | `group_dial_sales` | `vip-bot` 또는 별도 서비스 | sample group dial은 legacy sample 번호/문맥으로 이동 |
| `5551212` | static DID sample | `1000`으로 transfer | `bot-main` alias DID | static DID transfer 삭제, 정책 파일 alias로 이전 |

컷오버 원칙:

- 새 service entry는 Orchestrator ownership으로 일원화한다.
- `public`은 admission만 담당하고, 실제 정책 해석은 `default-policy` 단계에서 수행한다.
- static dialplan sample rule은 production 대표번호 ownership에서 제거한다.

Phase 0 필수 산출물:

- 번호별 최종 owner
- legacy 대체 번호 또는 대체 문맥
- cutover 순서
- rollback 조건

이 산출물이 승인되기 전에는 Phase 1 구현을 시작하지 않는다.

### 3.3 세션 상태 모델

현재 세션 모델은 아래 정보만 갖는다.

- `session_id`
- `fs_uuid`
- `caller_id`
- `dest_num`
- `answered_at`
- `hangup_at`

파일:

- `orchestrator/internal/session/model.go`

문제:

- 어떤 logical service로 처리되었는지 남지 않음
- entry number와 resolved service를 구분하지 않음
- 향후 slot allocator와 overflow 정책 연계를 위한 최소 필드가 부족함

### 3.4 PBX / SBC interconnect

현재 gateway bootstrap은 env 기반 entrypoint에서 수행된다.

- 파일: `scripts/freeswitch-entrypoint.sh`
- 문서: `docs/pbx_sbc_interconnect.md`

현재 상태:

- `pbx-main`, `pbx-standby` 생성 가능
- `register` / `trunk` 모드 최소 지원
- 정책 엔진과는 분리되어 있지 않음

Phase 0 판단:

- interconnect 자체는 그대로 유지
- 단, Phase 0부터 routing 계층이 interconnect health/state를 읽을 수 있는 인터페이스는 설계해야 함

### 3.5 Phase 0에서 지원하는 외부 연동 계약

Phase 0/1의 운영 계약 범위는 아래로 제한한다.

- `register`
- `trunk`

아직 범위에 넣지 않는 항목:

- `hunt pilot`
- vendor-specific DID rewrite
- SIP extension pool

이유:

- 현재 저장소의 interconnect 구현은 gateway bootstrap과 minimal route admission 수준이기 때문
- `hunt`와 `pilot`는 번호 ownership과 SIP 헤더 해석 계약이 먼저 정리되어야 하기 때문

## 4. Phase 0 설계 목표

Phase 0 완료 시점에 다음 상태를 목표로 한다.

- 대표번호 ownership 표가 존재한다.
- `routing.yaml` 초기 스키마가 확정된다.
- Orchestrator가 정책 파일을 읽을 수 있는 구조가 설계된다.
- dynamic dialplan이 정책 계층을 통해 번호를 조회하는 구조로 전환 가능한 상태가 된다.
- static dialplan에서 새 정책 계층과 충돌하는 번호를 분리할 계획이 정해진다.

## 5. 핵심 설계 결정

### 5.1 정책의 단일 진실 원천은 Orchestrator가 가진다

결정:

- 서비스 라우팅 정책은 Orchestrator가 소유한다.
- FreeSWITCH는 SIP/미디어 실행 계층으로 제한한다.

이유:

- FreeSWITCH static XML만으로는 service / slot / overflow 정책을 표현하기 어렵다.
- Redis 기반 상태 저장, API, metrics는 Orchestrator에 이미 집중되어 있다.
- 정책 변경을 PBX/FS XML 수정에서 분리할 수 있다.

### 5.2 Phase 0에서 `routing.yaml`은 ingress routing만 표현한다

Phase 0의 `routing.yaml`은 full scope 전체를 담지 않는다.
우선 다음 정보만 관리한다.

- version
- representative entry number
- service name
- ingress stage
- source gateway filter
- route type
- enabled flag
- optional priority

Phase 0 예시:

```yaml
version: 1

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

  - name: vip-bot
    enabled: true
    route_type: ai
    entry_numbers: ["2000"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]
```

정의:

- `public-admission`: trusted gateway admission 단계
- `default-policy`: 실제 정책 해석 단계

Phase 1 resolver는 `default-policy`만 지원한다.
즉, `public`은 직접적인 정책 매칭 대상이 아니라 `default-policy`로 넘기기 위한 admission 단계다.

Phase 0에서는 아래 항목을 아직 넣지 않는다.

- slot pool
- queue policy
- failover_service
- weighted routing

이유:

- current codebase와의 차이가 너무 커져 Phase 0의 목적이 흐려짐
- 우선은 번호 ownership과 정책 로더를 안정화하는 것이 선행되어야 함

스키마 버전 원칙:

- `routing.yaml v1`
  - ingress routing only
  - service mapping
  - source gateway filter
  - route type
  - unknown entry 기본 동작
- `routing.yaml v2`
  - admission policy
  - overflow policy
  - capacity / slot metadata
  - future failover hints

Backward compatibility 원칙:

- `v1` loader는 `v1` 문서 범위를 넘는 필드를 거부한다.
- `v2` 도입 전에는 queue / capacity / overflow 필드를 넣지 않는다.
- `on_unknown_entry`는 ingress resolver의 fallback 동작만 정의하며, overflow 정책을 의미하지 않는다.

### 5.3 대표 진입번호 ownership은 명시적으로 재정의한다

Phase 0에서 반드시 정리할 번호:

- `1000`
- `2000`
- `5551212`

결정:

- 새 정책 계층이 소유할 번호는 static dialplan의 레거시 의미에서 분리한다.
- Phase 1 구현 전에 아래 둘 중 하나를 선택해야 한다.

옵션 A:

- `1000`, `2000`, `5551212`를 새 서비스용 대표번호로 승격
- 기존 local extension / group dial 의미는 다른 번호로 이동

옵션 B:

- 레거시 번호는 유지
- 새 service entry는 별도 신규 번호로 시작

권장:

- 요구사항상 대표번호를 실제로 `1000`, `2000`, DID로 운영하려면 옵션 A가 맞다.
- 단, Phase 0 문서에서 ownership migration을 명시적으로 선언해야 한다.

추가 원칙:

- ownership은 번호 단독이 아니라 아래 키 기준으로 정의한다.

```text
normalized_called_number + ingress_stage + source_gateway
```

- 같은 번호라도 `source_gateway`가 다르면 공존 가능하다.
- 같은 번호와 같은 ingress stage에서 `source_gateway` 집합이 겹치면 overlap 오류다.
- 구현 시 `중복 entry number` 검증이 아니라 `overlap / shadow rule` 검증으로 정의한다.

Phase 1 게이트:

- `1000`, `2000`, `5551212` 각각에 대해
  - final owner
  - legacy relocation
  - cutover order
  - rollback rule
  가 표로 확정되지 않으면 구현 시작 금지

### 5.4 Phase 0에서 세션 모델은 최소 확장만 설계한다

Phase 0에서 필요한 최소 추가 필드:

- `entry_number`
- `service_name`
- `source_gateway`
- `ingress_stage`
- `route_type`
- `routing_config_version`

후속 Phase에서 추가할 필드:

- `slot_id`
- `overflow_state`
- `queue_name`

이유:

- 현재 세션 모델은 목적지 번호와 실제 논리 서비스가 분리되지 않는다.
- Phase 1부터 metrics, debug, audit 용도로 `service_name`이 필요하다.

## 6. 책임 분리

### 6.1 FreeSWITCH 책임

- SIP 수신 / 응답
- gateway bootstrap
- dynamic dialplan 조회
- Bridge와의 오디오 fork 실행
- static fallback path 유지

### 6.2 Orchestrator 책임

- `routing.yaml` 로딩
- entry number -> logical service 해석
- context / gateway 필터 판단
- AI route 여부 결정
- service metadata를 session state에 연결

### 6.3 향후 allocator 책임

Phase 2 이후 Orchestrator가 추가로 담당한다.

- service -> slot pool 선택
- overflow 판단
- busy / transfer / queue 정책 적용

## 7. 제안 패키지 구조

Phase 0에서 아래 패키지 도입을 목표로 한다.

```text
orchestrator/internal/routing/
├── model.go
├── loader.go
├── resolver.go
├── validate.go
└── errors.go
```

### `model.go`

- `RoutingConfig`
- `ServiceRoute`
- `MatchRule`
- `Defaults`

### `loader.go`

- YAML 로딩
- 파일 watch는 Phase 0 범위 밖
- 초기에는 프로세스 시작 시 1회 로딩

### `validate.go`

- overlap / shadow rule 검증
- 빈 service name 검증
- unsupported route type 검증
- ingress stage / gateway 값 검증
- priority 충돌 검증

### `resolver.go`

입력:

- `destination_number`
- `ingress_stage`
- `sip_gateway_name`

출력:

- `ResolvedRoute`
  - `service_name`
  - `route_type`
  - `entry_number`
  - `routing_config_version`
  - `matched`

## 8. 설정 구조 설계

### 8.1 env는 인프라 bootstrap만 담당

env가 계속 맡을 영역:

- ESL
- Redis
- PBX interconnect
- Bridge endpoint
- logging / runtime profile

새로 추가할 env:

```env
ROUTING_CONFIG_PATH=/app/config/routing.yaml
```

권장 배포 규칙:

- production에서는 `ROUTING_CONFIG_PATH`가 설정되어 있으면 파일 누락/파싱 실패 시 startup fail-fast
- cutover 기간에만 legacy env fallback 허용
- cutover 완료 후 `AI_ROUTE_NUMBERS`는 제거 또는 deprecated 모드로 제한

### 8.2 `routing.yaml`은 정책만 담당

`routing.yaml`에 넣을 영역:

- representative entry number
- logical service mapping
- ingress filter

`routing.yaml`에 넣지 않을 영역:

- PBX password
- Redis address
- Bridge host
- TLS key path

즉, 정책과 인프라 설정을 분리한다.

### 8.3 env vs policy ownership 표

| 영역 | 소유자 | 예시 |
|------|--------|------|
| SIP gateway bootstrap | env + FreeSWITCH | `PBX_MAIN_PROXY`, `PBX_MAIN_REGISTER`, `PBX_STANDBY_PROXY` |
| Bridge / Redis / ESL bootstrap | env + Orchestrator | `BRIDGE_HOST`, `REDIS_ADDR`, `ESL_HOST` |
| ingress service mapping | `routing.yaml` | `1000 -> bot-main`, `5551212 -> bot-main` |
| source gateway filter | `routing.yaml` | `pbx-main`, `pbx-standby` |
| route type | `routing.yaml` | `ai`, future `human-transfer` |

원칙:

- `routing.yaml`은 bootstrap credential을 소유하지 않는다.
- env는 서비스 정책을 소유하지 않는다.
- 새 env 추가는 `ROUTING_CONFIG_PATH` 1개로 제한한다.

### 8.4 Gateway identity 계약

정책 파일이 참조하는 gateway는 접속 파라미터가 아니라 안정적인 logical ID다.

Phase 0 canonical gateway ID:

- `pbx-main`
- `pbx-standby`

원칙:

- `routing.yaml`은 logical ID만 참조한다.
- env는 logical ID의 접속 파라미터를 제공한다.
- validator는 `routing.yaml`의 `source_gateways`가 canonical ID 목록 안에 있는지 검증한다.
- rename은 migration 이벤트이며, 정책 파일과 env를 별도로 바꾸지 않는다.

## 9. Dynamic dialplan 변경 설계

현재 구조:

- `AI_ROUTE_NUMBERS` 포함 여부만 확인

Phase 0 이후 목표 구조:

1. FreeSWITCH가 dialplan lookup 호출
2. Orchestrator가 `routing resolver`로 entry 해석
3. route type이 `ai`면 dynamic AI XML 생성
4. 아니면 `not found` 또는 향후 다른 route type으로 확장

의사 흐름:

```text
FS dialplan request
  -> ParseForm
  -> Extract destination/context/gateway
  -> ResolveRoute()
    -> match found?
      yes -> ai route XML
      no  -> static fallback
```

Phase 0에서는 아직 slot allocator를 넣지 않는다.
즉, service resolution까지만 책임진다.

추가 규칙:

- Phase 1까지는 `public-admission -> default-policy` 한 방향만 지원
- `public` 단계에서 직접 service resolution 하지 않음

## 10. Static dialplan 정리 설계

Phase 0의 중요한 산출물은 "충돌 번호 정리 계획"이다.

대상 파일:

- `config/freeswitch/dialplan/public.xml`
- `config/freeswitch/dialplan/public/00_inbound_did.xml`
- `config/freeswitch/dialplan/default.xml`

정리 방향:

- `1000`, `2000`, `5551212`를 새 service entry로 사용할 경우
  - static route에서 제거하거나 다른 번호로 이전
- `public.xml`은 trusted PBX gateway에서 들어온 대표 진입번호를 Orchestrator ownership으로 넘기도록 정리
- static `group_dial_sales` 예제는 legacy / sample 영역으로 격리

권장 구현 원칙:

- service entry number는 Orchestrator ownership
- sample / legacy local extension은 별도 그룹이나 별도 문맥으로 격리
- static fallback은 유지하되, ownership이 이관된 번호에는 더 이상 적용하지 않음

## 11. 세션 모델 확장 설계

Phase 0에서 설계만 확정하고, 실제 구현은 Phase 1에서 수행한다.

추가 필드:

```text
ServiceName string
EntryNumber string
```

활용 목적:

- metrics 태깅
- debug / admin 조회
- overflow / allocator 도입 준비

호환성:

- Redis serialization은 backward-compatible 필드 추가 방식으로 유지

## 12. 운영 관측 설계

Phase 0에서 추가할 최소 관측 항목:

- route resolution success/fail
- unknown entry number count
- config load success/fail
- duplicate entry detection on startup

후보 메트릭:

- `vbgw_routing_config_loaded{status="ok|error"}`
- `vbgw_route_resolution_total{result="matched|fallback|error"}`
- `vbgw_route_unknown_entry_total`
- `vbgw_route_overlap_total`

## 13. Gateway health 계약

Phase 0에서는 gateway health를 read-only 관측값으로만 사용한다.

아직 하지 않는 것:

- health 기반 라우팅 결정
- standby 선제 전환

Phase 0에서 정의만 해두는 구조:

```text
GatewayState
- gateway_name
- mode            # register | trunk
- health_class    # healthy | degraded | draining | unreachable | unknown
- observed_status # REGED | NOREG | DOWN | UNKNOWN
- source          # sofia-status | manual | future options-probe
- observed_at
- freshness_ttl
- producer        # health monitor component
```

원칙:

- `register` 모드와 `trunk` 모드는 상태 해석이 다르다.
- stale health는 route decision input으로 사용하지 않는다.
- actual failover decision은 Phase 3 책임이다.
- Phase 0과 Phase 1에서는 health를 라우팅 필터나 선택 우선순위로 사용하지 않는다.
- Phase 0 문서의 health 목적은 관측 모델 고정과 후속 구현 재설계 방지다.

상태 해석 기본값:

- `healthy`: 최근 TTL 내 정상 관측
- `degraded`: 응답은 있으나 경고 상태
- `draining`: 수동 또는 운영 정책에 의해 신규 콜 비권장
- `unreachable`: TTL 내 정상 관측 실패
- `unknown`: 아직 관측 정보 없음

## 14. SIP interconnect 체크리스트

Phase 0 문서에 아래 항목을 운영 계약으로 함께 보관한다.

- called number normalization 기준
- trusted peer / ACL 기준
- transport (`udp`, `tcp`, `tls`) 기준
- trunk vs register health probe 기준
- failover를 유발하는 SIP failure cause 후보
- timeout / ping / retry 기본값
- ingress context precedence
- caller ID / Diversion / History-Info 처리 원칙

현재 문서에서는 체크리스트만 확정하고, 실제 정책화는 후속 Phase에서 수행한다.

## 15. Release 1 임시 운영 응답 정책

queue가 도입되기 전까지는 아래 응답 정책을 고정한다.

| 상황 | 허용 응답 | 금지 응답 |
|------|-----------|-----------|
| unknown entry | `static_fallback` | queue 진입 |
| no capacity | `busy` 또는 `direct transfer` | callcenter를 queue 대체로 사용 |
| gateway unhealthy | 관측값 기록, Phase 3 전까지 route decision에는 사용 안 함 | health만으로 standby 선전환 |
| human fallback unavailable | `busy` 또는 안내 후 종료 | 임의 queue 유도 |

원칙:

- Phase 4 전까지 queue / callcenter를 overflow 대체 구현으로 사용하지 않는다.
- Release 1의 목적은 정책 ownership과 ingress 정합성 확보이며, caller wait experience 최적화가 아니다.

## 16. 마이그레이션 절차

### Step 1

- 대표번호 ownership 표 작성
- 운영에서 실제 사용할 대표번호 확정

### Step 2

- `routing.yaml` 도입
- Orchestrator가 정책 파일을 읽도록 구현

### Step 3

- static dialplan 충돌 번호 제거 또는 재배치

### Step 4

- dynamic dialplan이 새 resolver를 사용하도록 전환

### Step 5

- `AI_ROUTE_NUMBERS` 폐기 또는 fallback 호환 모드 종료

## 17. 테스트 전략

### 정적 검증

- `routing.yaml` 파싱 성공
- 중복 entry number 검출
- unsupported route type 검출
- empty service name 검출

### 동작 검증

- `1000` 요청이 `bot-main`으로 매핑됨
- `2000` 요청이 `vip-bot`으로 매핑됨
- unknown number는 static fallback
- non-default context는 fallback
- source gateway filter 불일치 시 fallback
- overlap rule 충돌 시 startup 실패
- `public-admission` 단계는 policy resolution을 직접 수행하지 않음

### 회귀 검증

- 기존 `9196` AI 경로가 새 resolver 경유로도 유지 가능
- PBX interconnect 설정은 그대로 동작

## 18. Acceptance Criteria

Phase 0 완료 판정 기준:

- `routing.yaml` 스키마와 ownership 모델이 문서화되어 있다.
- Orchestrator `internal/routing` 구조와 책임이 정의되어 있다.
- `1000`, `2000`, `5551212` 충돌 정리 계획이 확정되어 있다.
- env와 정책 파일의 역할 분리가 명확하다.
- Phase 1 구현이 바로 가능한 수준의 파일 영향 범위와 테스트 계획이 있다.
- gateway health가 Phase 0에서는 read-only 관측값임이 명시되어 있다.
- canonical gateway ID와 validator 책임이 문서화되어 있다.
- ownership migration 표가 Phase 1 게이트로 명시되어 있다.

## 19. 오픈 이슈

- `2000`을 실제 서비스 대표번호로 유지할지, 기존 sales group 예제를 다른 번호로 이동할지 결정 필요
- `1000`을 실제 softphone/local extension과 겸용할지 분리할지 결정 필요
- `5551212` DID 예제를 운영 DID로 승격할지 샘플로만 남길지 결정 필요
- Phase 1에서 unknown entry를 `static_fallback`으로 둘지 `busy`로 둘지 결정 필요

## 20. 권장 결론

Phase 0은 기능 추가보다 "ownership 정리"와 "정책 계층 도입 준비"가 본질이다.

권장 결론은 아래와 같다.

- 정책 엔진의 단일 진실 원천은 Orchestrator가 가진다.
- `routing.yaml`은 ingress routing부터 시작한다.
- 대표번호는 명시적으로 ownership migration을 수행한다.
- queue, slot allocator, extension slot은 다음 Phase로 미룬다.

이 기준을 지키면 Phase 1부터 안전하게 구현을 시작할 수 있다.
