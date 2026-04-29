# Phase 5 상세 설계서

이 문서는 VBGW full scope 개발의 Phase 5 상세 설계를 정의한다.
Phase 5의 목적은 `logical slot`과 `sip extension slot`을 모두 지원하는 유연한 슬롯 모델을 완성하는 것이다.

## 1. 목적

Phase 5의 목표는 아래 네 가지다.

- service별 capacity backend를 `logical` 또는 `sip_extension`으로 선택 가능하게 한다.
- 대표번호 `1000`을 `1001~1010` 같은 extension pool로 분산하는 운영 모델을 지원한다.
- SIP 등록 상태를 slot 선택의 입력으로 사용한다.
- PBX/SBC 연동 요구에 따라 trunk형과 extension exposure형을 같이 지원한다.

## 2. 범위

### 포함

- slot backend abstraction
- logical slot backend
- SIP extension slot backend
- extension registration state tracking
- 대표번호 -> extension pool 분산

### 제외

- vendor-specific multi-tenant provisioning 전체
- 상담원 스케줄링/근무표
- WebRTC endpoint pool

## 3. 선행 조건

- Phase 2의 slot allocator와 overflow가 안정화되어 있어야 한다.
- 내부 extension directory 구조와 등록 정책이 정리되어 있어야 한다.
- `1001~1010`이 실제로 어떤 의미를 갖는지 운영 정책이 확정되어 있어야 한다.
- 기존 `10xx` default directory를 slot inventory로 재활용할지, 별도 range/profile/domain을 쓸지 먼저 확정되어 있어야 한다.

## 4. 현재 상태와 문제점

현재 저장소는 `config/freeswitch/directory/default*.xml`에 개별 내선이 존재하지만, 이것을 slot backend로 다루는 추상화는 없다.

문제는 아래와 같다.

- logical service와 SIP extension이 같은 자원 모델이 아니다.
- extension 등록 상태를 라우팅에 반영하지 못한다.
- `1000 -> 1001~1010` hunt 운영모델을 정책 파일로 표현할 수 없다.

## 5. 핵심 설계 결정

### 5.1 slot backend를 명시적으로 분리한다

- `logical` backend: AI 동시 처리 토큰
- `sip_extension` backend: 실제 SIP endpoint 또는 PBX-visible slot

### 5.2 allocator는 backend-neutral하게 유지한다

- allocator는 `available slot inventory`만 본다.
- slot을 실제로 어떻게 소유/검증하는지는 backend driver가 결정한다.

### 5.3 extension slot은 registration freshness를 요구한다

- 단순히 extension 번호가 존재하는 것만으로 available로 보지 않는다.
- 최근 등록 상태, contact, last seen을 기준으로 availability를 계산한다.

### 5.4 기본 권장안은 별도 extension range 또는 domain 분리다

현재 저장소의 `1000~1019`는 이미 local directory user와 sample dialplan 의미를 갖는다.
따라서 Phase 5의 기본 권장안은 아래 중 하나다.

- 별도 extension range 사용
- 별도 SIP profile 또는 domain 사용
- 기존 `10xx`를 재사용해야 한다면 legacy default directory 의미를 먼저 제거

이 선행 정리 없이 `1000 -> 1001~1010`을 slot inventory로 바로 쓰는 것은 금지한다.

## 6. 설정 모델

Phase 5에서 `routing.yaml`은 service별 slot backend를 명시한다.

```yaml
services:
  - name: bot-main
    capacity:
      backend: logical
      max_concurrent: 10
      allocator: round_robin

  - name: frontdesk-bot
    capacity:
      backend: sip_extension
      allocator: round_robin
      extensions:
        - "1001"
        - "1002"
        - "1003"
        - "1004"
        - "1005"
        - "1006"
        - "1007"
        - "1008"
        - "1009"
        - "1010"
      require_registered: true
```

ownership 원칙은 아래와 같다.

- `routing.yaml`: 어떤 service가 어떤 slot backend를 쓰는지
- `.env`: registration freshness TTL, backend feature gate
- FreeSWITCH directory / profile XML: 실제 extension 계정과 등록 파라미터

## 7. 데이터 모델

slot inventory 모델 예:

- `slot_id`
- `backend_type`
- `extension`
- `registered`
- `last_seen_at`
- `contact_uri`
- `health_class`

신규 패키지는 아래를 권장한다.

- `orchestrator/internal/slots/backend.go`
- `orchestrator/internal/slots/logical.go`
- `orchestrator/internal/slots/sip_extension.go`
- `orchestrator/internal/slots/registry.go`

## 8. 코드 변경 지점

- `orchestrator/internal/capacity/*`
- 신규 `orchestrator/internal/slots/*`
- `orchestrator/internal/session/model.go`
- `orchestrator/internal/api/admin.go`
- `orchestrator/internal/metrics/prometheus.go`
- `config/freeswitch/directory/default.xml`
- `config/freeswitch/directory/default/*.xml`
- 필요 시 `scripts/freeswitch-entrypoint.sh`

변경 방향은 아래와 같다.

- capacity manager가 backend driver를 호출해 inventory를 조회
- extension slot은 registration snapshot이 healthy할 때만 available
- admin API가 logical / sip_extension backend를 구분해 보여줌

## 9. 런타임 흐름

### 9.1 logical backend

1. service resolve
2. logical token lease
3. AI 처리

### 9.2 sip extension backend

1. service resolve
2. extension inventory에서 registered slot 조회
3. allocator가 slot 하나 선택
4. 해당 extension으로 bridge 또는 transfer
5. call 종료 시 slot release

### 9.3 extension 미등록

1. require_registered=true
2. 사용 가능한 extension이 없으면 overflow 정책 실행

## 10. 운영과 관측 기준

필수 메트릭은 아래와 같다.

- `vbgw_slot_backend_available{service=...,backend=...}`
- `vbgw_extension_registered{extension=...}`
- `vbgw_extension_slot_in_use{service=...,extension=...}`
- `vbgw_extension_registration_stale_total`

admin API는 아래를 제공해야 한다.

- 서비스별 backend type
- extension inventory 목록
- 등록 상태와 last seen
- 현재 점유된 extension slot

## 11. 테스트 전략

### 단위 테스트

- backend selection
- registration freshness 판정
- logical vs sip_extension allocator parity

### 통합 테스트

- `1000 -> 1001~1010` 분산
- 등록된 extension만 선택되는지 확인
- extension이 중간에 unregister될 때 overflow로 전환되는지 확인

### 회귀 테스트

- logical backend 서비스가 Phase 2 동작을 유지하는지 확인
- extension inventory가 비어 있어도 시스템 전체가 깨지지 않는지 확인

## 12. Acceptance Criteria

- service별로 logical / sip_extension backend를 선택할 수 있다.
- extension slot은 등록 상태가 healthy할 때만 사용된다.
- 대표번호를 extension pool로 유연하게 분산할 수 있다.
- logical backend 서비스는 기존 동작과 호환된다.
- 운영 API와 메트릭에서 extension inventory와 점유 상태를 볼 수 있다.
- extension range/profile/domain ownership이 기존 default directory와 충돌하지 않는다.
