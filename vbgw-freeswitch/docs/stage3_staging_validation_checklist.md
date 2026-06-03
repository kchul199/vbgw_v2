# Stage 3 스테이징 통합 검증 체크리스트

> 작성일: 2026-05-13
> 목적: 운영과 동일한 프로파일로 VBGW 전체 스택을 통합 검증할 때 사용할 실행 체크리스트와 증적 수집 기준

> 2026-06-01 1차 실행 결과: 포털/API/CDR/trace/SIP external-profile 콜은 통과, 실제 TTS는 OpenAI quota `429`로 실패해 bridge fallback greeting으로 대체됨.
> 2026-06-02 재검증 결과: quota가 살아 있는 OpenAI key 반영 후 같은 SIPp external-profile 콜에서 fallback 없이 실제 TTS greeting 성공, `grpc_recvs=181` 확인. 상세 증적은 [stage3_staging_validation_evidence_20260601.md](stage3_staging_validation_evidence_20260601.md) 참고.

## 1. 사전 조건

- `docker compose -f docker-compose.yml -f docker-compose.prod.yml config` 렌더링이 정상
- `.env`에 아래 항목이 운영형 값으로 채워짐
  - `RUNTIME_PROFILE=production`
  - `ADMIN_API_KEY`
  - `ADMIN_CONTROL_KEY`
  - `JWT_SECRET`
  - `INTERNAL_API_SECRET`
  - `CORS_ALLOWED_ORIGINS`
  - `WS_ALLOWED_ORIGINS`
  - `REDIS_ADDR=vbgw-redis:16379`
  - `REDIS_PASS`
- `scripts/validate_prod.sh`가 통과
- 포탈 로그인용 조회 토큰 / 제어 토큰 준비 완료
- 대상 번호와 게이트웨이 경로 확정
  - 테스트 번호
  - 대표번호
  - DID
  - register 경로
  - trunk 경로

## 2. 기동 확인

- FreeSWITCH health 정상
- bridge internal health 정상
- orchestrator `/live`, `/ready`, `/health` 정상
- Redis health 정상
- vbgw-ai health 또는 연결 상태 정상
- 포탈 대시보드 진입 가능

## 3. 기본 콜 플로우

### 시나리오 A. 테스트 번호 직접 진입

- 발신: `9196` 또는 현재 테스트 번호
- 기대 결과:
  - SIP 200 OK
  - 초기 무음 없음
  - greeting 정상 재생
  - 세션 생성
  - CDR 저장

증적:

- FreeSWITCH 로그
- orchestrator 로그
- bridge 로그
- 포탈 세션 화면 캡처
- 포탈 CDR 화면 캡처

### 시나리오 B. 대표번호 진입

- 발신: 대표번호 또는 DID
- 기대 결과:
  - `entry_number -> service` 라우팅 정상
  - queue / fallback 정책이 의도대로 반영
  - gateway/source 정보가 CDR에 반영

증적:

- routing config 조회 결과
- 세션 상세
- CDR 레코드

## 4. 제어면 검증

- 조회 토큰만으로:
  - 세션 목록 조회 가능
  - 서비스/게이트웨이/큐/채널 조회 가능
  - DTMF/전환/녹음 시작은 거부되어야 함
- 제어 토큰으로:
  - DTMF 전송
  - blind transfer
  - record start/stop
  - 서비스 pause/resume
  - gateway standby 전환

증적:

- 403 응답 로그 또는 화면
- 성공 응답 로그 또는 화면
- operation history 기록

## 5. 오디오 검증

- 초기 greeting 음성 정상
- one-way audio 없음
- 무음 지속 없음
- 링백/안내 tone 정책이 의도대로 재생
- barge-in 시 재생 중단과 입력 인식 동작 확인

증적:

- 통화 녹취
- bridge 오디오 관련 로그
- 필요 시 RTP 캡처

## 6. 장애/정책 검증

- 서비스 pause 후 신규 진입 거부 또는 안내
- queue depth 증가 시 포탈 반영
- gateway standby 전환 후 신규콜 경로 반영
- 슬롯 부족 시 busy/queue/fallback 정책 확인

## 7. 종료 판정

- 아래가 모두 확인되면 Stage 3 완료
  - 대표번호와 테스트 번호 모두 정상 처리
  - 조회/제어 토큰 경계 확인
  - 포탈의 세션/채널/CDR/operation 화면 정합성 확인
  - greeting, audio, queue, routing, gateway 상태가 모두 기대값과 일치

2026-06-02 기준 local staging external-profile 경로의 Stage 3 종료 조건은 충족했습니다. 실제 PBX/SBC register/trunk 대표번호 경로는 Stage 4에서 계속 검증합니다.
