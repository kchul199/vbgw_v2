# Phase 1 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 1 상세 설계를 정의한다.
Phase 1의 목적은 대표 진입번호를 `logical service`로 해석하는 최소 실행 경로를 완성하는 것이다.

## 1. 목적

Phase 1의 목표는 아래 다섯 가지다.

- `1000`, `2000`, `5551212`와 같은 대표 진입번호를 `service`로 매핑한다.
- `AI_ROUTE_NUMBERS` 중심의 임시 AI 라우팅을 `routing.yaml` 중심 구조로 전환한다.
- FreeSWITCH static dialplan과 Orchestrator dynamic dialplan의 ownership 충돌을 제거한다.
- 서비스 해석 결과를 세션 메타데이터와 운영 관측에 남긴다.
- Phase 2의 slot allocator가 붙을 수 있도록 ingress resolution을 안정화한다.

## 2. 범위

### 포함

- `routing.yaml v1` 기반 ingress routing
- `entry_number -> service` 해석
- `source_gateway`, `ingress_stage` 조건 매칭
- unknown entry에 대한 static fallback 유지
- dynamic dialplan XML에 routing metadata 주입
- 대표 진입번호 ownership cutover 계획 수립과 적용

### 제외

- slot pool / allocator
- queue / human fallback
- gateway health-aware failover
- SIP extension slot
- multi-node HA

즉, Phase 1은 "번호를 어떤 서비스가 소유하는가"를 결정하는 단계다.

## 3. 선행 조건

- Phase 0 산출물의 ownership migration 표가 승인되어 있어야 한다.
- `pbx-main`, `pbx-standby`의 canonical gateway ID가 확정되어 있어야 한다.
- `ROUTING_CONFIG_PATH`가 배포 환경별로 일관되게 주입되어 있어야 한다.
- `config/freeswitch/dialplan/public.xml`, `config/freeswitch/dialplan/public/00_inbound_did.xml`, `config/freeswitch/dialplan/default.xml`의 레거시 번호 의미가 정리되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 ingress routing의 핵심은 아래 세 경로다.

- `orchestrator/internal/routing/*`
- `orchestrator/internal/api/dialplan.go`
- `config/freeswitch/dialplan/public.xml`

현재 상태의 문제는 아래와 같다.

- 대표번호 ownership이 env와 static XML에 분산되어 있다.
- `1000`, `2000`, `5551212`가 이미 각기 다른 legacy 의미를 가진다.
- dynamic dialplan은 현재 안전한 AI 테스트 번호만 우선 소유하고 있다.
- 어떤 콜이 어떤 서비스로 해석되었는지 운영에서 일관되게 보기가 어렵다.

## 5. 핵심 설계 결정

### 5.1 정책의 단일 진실 원천은 Orchestrator다

- `routing.yaml`은 대표번호와 logical service의 관계를 소유한다.
- FreeSWITCH는 trusted ingress admission과 미디어 실행에 집중한다.

### 5.2 `public`은 admission, `default-policy`는 resolution 단계다

- `public` 컨텍스트는 trusted gateway만 `default`로 넘긴다.
- 실제 routing policy 해석은 Orchestrator가 `default-policy` ingress stage에서만 수행한다.

### 5.3 unknown entry는 즉시 static fallback으로 돌린다

- Phase 1에서는 aggressive ownership expansion을 하지 않는다.
- policy에 없는 번호는 기존 static dialplan으로 넘겨 backward compatibility를 유지한다.

### 5.4 서비스 해석 결과는 세션 메타데이터에 남긴다

- `entry_number`
- `service_name`
- `source_gateway`
- `ingress_stage`
- `route_type`
- `routing_config_version`

이 필드는 이후 slot allocator, overflow, 감사 추적의 공통 기반이 된다.

## 6. 설정 모델

Phase 1에서 `routing.yaml`은 아래 영역만 소유한다.

- version
- unknown entry fallback 정책
- service route 목록
- service별 대표번호
- ingress stage
- source gateway filter
- route type
- priority

예시:

```yaml
version: 1

defaults:
  on_unknown_entry: static_fallback

services:
  - name: bot-main
    enabled: true
    route_type: ai
    priority: 100
    entry_numbers: ["1000", "5551212"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]

  - name: vip-bot
    enabled: true
    route_type: ai
    priority: 90
    entry_numbers: ["2000"]
    match:
      ingress_stages: ["default-policy"]
      source_gateways: ["pbx-main", "pbx-standby"]
```

ownership 원칙은 아래와 같다.

- `routing.yaml`: 대표번호와 서비스 매핑
- `.env`: 경로, 포트, gateway ID, feature gate
- FreeSWITCH XML: SIP ingress admission, static fallback, 레거시 sample rule 제거

### 6.1 Schema compatibility / delivery / rollback 계약

현재 구현 기준으로 `orchestrator/internal/routing/validate.go`는 `version: 1`만 허용한다.
따라서 Phase 1 시점의 운영 계약은 아래를 반드시 따른다.

- 새 필드 추가만으로는 안 되고, 로더/검증기/패키징이 함께 배포되어야 한다.
- validation 실패 시 Orchestrator는 startup 단계에서 즉시 종료될 수 있으므로, schema 변경은 canary 배포와 rollback 파일을 반드시 동반한다.
- `routing.yaml` 전달 경로는 운영 기준 `/app/config/routing.yaml`을 canonical path로 고정한다.
- 정책 파일은 이미지 bake 또는 volume mount 중 한 방식만 팀 표준으로 정하고, 혼합 사용을 금지한다.
- runtime reload는 Phase 1 범위에 넣지 않으며, 변경 반영은 재기동 기반으로 정의한다.

rollback 원칙:

1. 직전 `routing.yaml` 아티팩트를 항상 보관한다.
2. schema 불일치 또는 startup failure 발생 시 직전 정책 파일로 즉시 되돌린다.
3. rollback 동안 legacy static fallback이 유지되도록 대표번호 ownership을 단계적으로 전환한다.

## 7. 대표 진입번호 컷오버 규칙

| 번호 | 기존 의미 | Phase 1 목표 | 컷오버 원칙 |
|------|-----------|--------------|-------------|
| `1000` | local extension | `bot-main` 대표번호 | 1차 rollout에서는 `pbx-main/pbx-standby` ingress에 한해 `bot-main`으로 승격하고, no-gateway 로컬 호출은 backward compatibility를 위해 임시 유지 |
| `2000` | sample group dial | `vip-bot` 또는 별도 logical service | sample group dial 예제는 다른 sample 번호로 이동 |
| `5551212` | static DID sample | `bot-main` DID alias | static transfer rule을 제거하고 policy alias로 대체 |

컷오버는 아래 순서로 진행한다.

1. policy에 대표번호를 먼저 등록한다.
2. static XML에서 동일 번호 rule을 legacy 번호로 이관한다.
3. canary 콜 테스트 후 대표번호 ownership을 Orchestrator로 고정한다.
4. rollback 시에는 policy 비활성화 후 legacy XML을 다시 활성화한다.

### 7.1 Ownership matrix

Phase 1에서 실제 owner 판단은 번호 하나만으로 하지 않고 아래 3개 키로 고정한다.

`(entry_number, ingress_stage, source_gateway) -> owner`

초기 운영 매트릭스:

| entry_number | ingress_stage | source_gateway | owner |
|--------------|---------------|----------------|-------|
| `1000` | `default-policy` | `pbx-main`, `pbx-standby` | `bot-main` |
| `2000` | `default-policy` | `pbx-main`, `pbx-standby` | `vip-bot` |
| `5551212` | `default-policy` | `pbx-main`, `pbx-standby` | `bot-main` |
| 기타 번호 | `default-policy` | any | static fallback |
| any | `public-admission` | any | FreeSWITCH admission only |

운영 원칙:

- 같은 번호라도 ingress stage가 다르면 owner가 다를 수 있음을 문서화한다.
- Phase 1 이후 문서는 반드시 위 ownership matrix를 전제로 작성한다.
- matrix에 없는 번호를 새 service에 붙일 때는 Phase 1 문서의 cutover 절차를 다시 따라야 한다.
- 1차 rollout에서는 `1000`의 no-gateway 로컬 호출을 static fallback으로 남겨 softphone/dev 경로를 보존한다.

## 8. 코드 변경 지점

Phase 1의 주요 변경 지점은 아래와 같다.

- `orchestrator/internal/routing/model.go`
- `orchestrator/internal/routing/loader.go`
- `orchestrator/internal/routing/validate.go`
- `orchestrator/internal/routing/resolver.go`
- `orchestrator/internal/api/dialplan.go`
- `orchestrator/internal/api/server.go`
- `orchestrator/internal/session/model.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/metrics/prometheus.go`
- `config/freeswitch/dialplan/public.xml`
- `config/freeswitch/dialplan/public/00_inbound_did.xml`
- `config/freeswitch/dialplan/default.xml`

우선순위 규칙:

- 현재 resolver는 `priority` 필드를 실제로 해석하지 않는다.
- 따라서 Phase 1 구현 기준의 precedence는 `YAML service 선언 순서`다.
- `priority`는 문서 예약 필드로만 두고, 실제 precedence를 바꾸려면 resolver 확장과 회귀 테스트가 먼저 필요하다.

각 구성요소의 책임은 아래와 같다.

- `routing`: 로드, 검증, 해석
- `api/dialplan`: policy match 시 dynamic XML 생성
- `session/model`: routing metadata 보존
- `api/admin`: active session에서 service metadata 노출
- `metrics`: config load 및 resolution 결과 관측
- FreeSWITCH XML: trusted ingress와 static fallback 유지

## 9. 런타임 흐름

### 9.1 Inbound AI 진입

1. PBX/SBC가 `1000` 또는 `5551212`로 VBGW 호출
2. FreeSWITCH `public`이 trusted source를 `default`로 전달
3. `mod_xml_curl`이 `/api/v1/fs/dialplan` 호출
4. Orchestrator resolver가 `destination_number + ingress_stage + source_gateway`로 service 해석
5. match 성공 시 AI용 XML을 반환하고 routing metadata를 channel variable로 주입
6. 이후 세션이 생성되면 metadata를 session store에 기록

### 9.2 Unknown entry

1. resolver가 match 실패
2. `defaults.on_unknown_entry=static_fallback`
3. Orchestrator는 `not found`를 반환
4. FreeSWITCH는 기존 static XML을 계속 사용

## 10. 관측과 운영 기준

Phase 1에서 반드시 필요한 메트릭은 아래와 같다.

- `vbgw_routing_config_loaded`
- `vbgw_route_resolution_total{result}`
- `unknown entry` 로그 카운트
- active session의 `service_name`, `entry_number`, `source_gateway`

운영 기준은 아래와 같다.

- startup 시 shadow rule, overlap, invalid gateway는 즉시 오류로 간주한다.
- 대표번호 ownership 변경은 반드시 canary 테스트와 함께 수행한다.
- 대표번호가 static XML과 이중으로 정의되면 배포 금지다.

## 11. 테스트 전략

### 단위 테스트

- `routing/loader_test.go`: malformed policy, overlap, invalid gateway
- `routing/resolver_test.go`: ingress stage normalization, source gateway filter, priority
- `api/dialplan_test.go`: policy match, legacy fallback, unknown entry fallback

### 통합 테스트

- `1000`, `2000`, `5551212` 각각에 대한 dynamic resolution
- policy 비활성 상태에서 static fallback 유지
- trusted / untrusted gateway admission 분리

### 회귀 테스트

- 기존 `9196` 테스트 번호가 여전히 의도한 policy로 동작하는지 확인
- route metadata가 `/admin/sessions/active`에 노출되는지 확인

## 12. Acceptance Criteria

- `1000`, `2000`, `5551212`가 의도한 logical service로만 resolve된다.
- 대표번호와 static XML 사이의 ownership 충돌이 없다.
- unknown entry는 deterministic하게 static fallback으로 떨어진다.
- active session과 로그에서 service metadata를 확인할 수 있다.
- invalid policy는 startup 시점에 차단된다.
- 배포 산출물 안에서 `routing.yaml` delivery 경로와 rollback 절차가 문서와 일치한다.
