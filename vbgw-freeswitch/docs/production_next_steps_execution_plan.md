# 상용 전환 다음 단계 실행 계획

> 작성일: 2026-05-13
> 기준 저장소: `/Users/kchul199/vbgw_v2/vbgw-freeswitch`
> 목적: P0 안정화 이후 상용 전환까지의 후속 작업을 순서대로 고정하고, 각 단계마다 검증 에이전트 검토를 거친 뒤 다음 단계로 진행하기 위한 실행 문서

## 1. 운영 원칙

- 단계는 반드시 순서대로 진행한다.
- 각 단계는 `구현/정리 -> 자체 테스트 -> 검증 에이전트 리뷰 -> 보완 -> 종료 판정` 순서를 따른다.
- 종료 기준을 충족하지 못하면 다음 단계로 넘어가지 않는다.
- 운영 보안 경계를 약화시키는 임시 우회는 허용하지 않는다.

## 2. 단계별 실행 순서

### Stage 1. 권한 경계와 운영 설정 기준 마감

상태: `완료`

목표:

- 조회 토큰과 제어 토큰의 서버 경계를 실제 라우팅 레벨에서 분리한다.
- 포탈의 세션 제어 액션이 제어 토큰을 사용하도록 맞춘다.
- 운영용 설정 기준을 문서로 고정한다.

작업:

- `/api/v1/calls` 계열 `POST` 엔드포인트를 `control` 권한으로 보호
- `/api/v1/calls/{id}/stats`, `/api/v1/admin/*` 조회 계열은 `read` 권한 유지
- 포탈 세션 액션의 `authMode`를 `control`로 전환
- 본 실행 문서를 생성해 이후 단계의 기준 문서로 사용

검증 게이트:

- `go test ./...`
- `npm run lint`
- `npm run build`
- 검증 에이전트 코드 리뷰

종료 기준:

- 조회 토큰만으로는 콜 생성/DTMF/전환/녹음 제어가 되지 않는다.
- 제어 토큰으로는 기존 세션 제어가 정상 동작한다.
- 포탈 빌드와 린트가 모두 통과한다.

### Stage 2. 운영 설정값 확정과 배포 템플릿 정리

상태: `완료`

목표:

- 운영 프로파일에서 반드시 강제되어야 할 설정을 코드/템플릿/문서로 일치시킨다.

작업:

- `RUNTIME_PROFILE=production`, 강한 `ADMIN_API_KEY`, `ADMIN_CONTROL_KEY`, `JWT_SECRET`, `INTERNAL_API_SECRET` 기준 재확인
- `CORS_ALLOWED_ORIGINS`, `WS_ALLOWED_ORIGINS` 운영 allowlist 정리
- `.env.example`, compose overlay, 운영 runbook 간의 기준 값 정합성 점검
- `routing.yaml` 운영 템플릿과 secret 주입 방식을 문서화

검증 게이트:

- 설정 diff 점검
- `validate_prod.sh` 결과 확인
- 검증 에이전트 설정 리뷰

종료 기준:

- 운영용 필수 설정 항목이 문서, 샘플, 실제 검증 스크립트에서 동일하게 표현된다.

### Stage 3. 스테이징 통합 검증

상태: `완료 — 2026-06-02 TTS 재검증 통과`

목표:

- 운영과 동일한 프로파일로 `FreeSWITCH + bridge + orchestrator + AI + Redis + portal`을 통합 검증한다.

작업:

- 스테이징 프로파일 기동
- 인사말, 무음, barge-in, queue, CDR 저장, 포탈 조회 동선 확인
- 대표번호와 테스트 번호 흐름 동시 검증

검증 게이트:

- dev/staging 실콜 시나리오 테스트
- 검증 에이전트 결과 정리
- 2026-06-01 1차 증적: [stage3_staging_validation_evidence_20260601.md](stage3_staging_validation_evidence_20260601.md)
- 2026-06-02 TTS credential 복구 후 재검증: 같은 증적 파일의 `2026-06-02 TTS Credential 복구 후 재검증` 섹션

종료 기준:

- 운영 포탈과 콜 플로우가 같은 데이터 기준으로 동작한다.
- 실제 TTS greeting이 fallback 없이 성공한다.

### Stage 4. PBX/SBC 연동 실검증

상태: `진행 중 — Kamailio surrogate 통과, 실제 PBX/SBC 대상 검증 대기`

목표:

- 외부 PBX/SBC와의 `register` / `trunk` 연동을 운영 시나리오 기준으로 확인한다.

작업:

- `pbx-main`, `pbx-standby` 게이트웨이 검증
- DID / 대표번호 / 내선 매핑 확인
- failover, standby, gateway health 상태 전이 확인

검증 게이트:

- 실제 SIP 연동 로그 검증
- 검증 에이전트 인터커넥트 리뷰
- 2026-06-03 preflight 증적: [stage4_pbx_sbc_validation_evidence_20260603.md](stage4_pbx_sbc_validation_evidence_20260603.md)
- 2026-06-03 Kamailio surrogate 증적: 같은 증적 파일의 `Kamailio SBC Surrogate 검증` 섹션

종료 기준:

- 두 연동 방식 모두 inbound/outbound/standby 전환이 확인된다.
- 현재 Kamailio surrogate 기준 trunk inbound 및 register health는 통과했다.
- 2026-06-03 운영 결정: 실제 고객/운영 PBX/SBC endpoint 기준 검증은 별도 리스크로 남기고 Stage 4는 pass 처리한다.

### Stage 5. 장애 대응 및 HA 검증

상태: `HA P0 코드/테스트 통과`

목표:

- 단일 노드/Redis/AI/Bridge 장애 시 운영 정책대로 수렴하는지 검증한다.

작업:

- Redis 장애/복구
- bridge 재기동
- AI 지연 또는 실패
- node drain / resume / stale lease 회수

검증 게이트:

- 장애 주입 테스트: 2026-06-03 단일 노드/Kamailio surrogate 기준 통과
- 2-node capacity/cluster 테스트: stale lease 회수, standby 신규 admit, renew failure admit 차단 통과
- 검증 에이전트 HA 리뷰
- 증적: [stage5_ha_fault_injection_evidence_20260603.md](stage5_ha_fault_injection_evidence_20260603.md)

종료 기준:

- 신규 admit, 기존 세션, queue, standby 전환이 정책대로 동작한다.
- 현재 단일 노드 기준 신규 admit 차단/복구, bridge/AI/Redis 복구, stale lease 회수는 확인됐다.
- 2-node 테스트 기준 한 노드 heartbeat loss 후 stale lease reaper가 lease를 회수하고 standby node 신규 admit이 성공함을 확인했다.
- distributed lease renew failure는 `distributed lease renew degraded` 사유로 신규 admit 차단에 반영된다.
- 단, 실제 2-container/live orchestrator failover와 queue/human fallback 포함 drain 정책은 아직 별도 리허설이 필요하다.

### Stage 6. 성능 및 용량 검증

상태: `대기`

목표:

- 상용 기준 CPS, 동시콜, greeting on/off, RTP 포트, 480/BYE 이슈를 포함한 실효 한계치를 확정한다.

작업:

- 운영 조건 부하 테스트
- greeting bypass/off 비교
- RTP 포트 풀 / unexpected BYE 계측

검증 게이트:

- 부하 테스트 결과 리포트
- 검증 에이전트 성능 리뷰

종료 기준:

- 목표 용량과 안전 여유치가 수치로 정리된다.

### Stage 7. 상용 전환 승인

상태: `대기`

목표:

- 컷오버, 롤백, 알람, 당직, 증적을 묶어 상용 투입 승인 가능 상태로 만든다.

작업:

- cutover checklist 정리
- rollback rehearsal
- 운영 sign-off 자료 정리

검증 게이트:

- 최종 검증 에이전트 종합 리뷰

종료 기준:

- Go/No-Go 판정 자료가 완성된다.

## 3. 현재 진행 기준

- 현재 완료 단계: `Stage 1`, `Stage 2`, `Stage 3`
- 현재 진행 단계: `Stage 4`
- 다음 착수 단계: `Stage 4 PBX/SBC 연동 실검증`
- 다음 단계 진입 조건:
  - Stage 2 템플릿/스크립트 정합성 확인
  - 스테이징 실콜 검증 일정과 대상 번호 확정
  - OpenAI TTS quota/billing 또는 대체 TTS credential 복구
  - Stage 3 external-profile SIPp 콜에서 fallback 없이 실제 TTS greeting 확인
  - 실제 PBX/SBC `PBX_MAIN_*` 또는 `PBX_STANDBY_*` 접속 정보 반영
