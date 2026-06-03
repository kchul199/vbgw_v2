# Stage 3 스테이징 통합 검증 증적

> 실행일: 2026-06-01  
> 실행 조합: `docker-compose.yml` + `docker-compose.prod.yml` + `docker-compose.stage3.yml`  
> 목적: production overlay 기준으로 포털, 제어면, SIP 콜 플로우, CDR/trace, 실제 TTS 경로를 확인

## 1. 결론

Stage 3는 **통과**입니다. 2026-06-01 1차 실행에서는 OpenAI TTS quota 문제로 보류했지만, 2026-06-02 credential 복구 후 같은 external-profile SIPp 콜을 재실행해 실제 TTS greeting과 gRPC 수신을 확인했습니다.

- production 설정 검증: 통과
- production portal 검증: 통과
- orchestrator live/ready/health: 통과
- read/control auth 경계: 통과
- SIPp 단일 콜: external profile `5080` 경로로 통과
- CDR/trace/session 조회: 통과
- 실제 TTS: 2026-06-02 재검증에서 fallback 주입 없이 성공
- Bridge gRPC receive: 2026-06-02 재검증에서 `grpc_recvs=181`

따라서 Stage 3 종료 기준 중 로컬 staging/external-profile 기반 검증은 충족했습니다. 실제 PBX/SBC register/trunk 경로는 Stage 4에서 별도 검증합니다.

## 2. 설정 및 기동

### Production validation

`scripts/validate_prod.sh`

- `PASS: 29`
- `FAIL: 0`
- `WARN: 7`

남은 warning:

- 로컬 `.env` 기준 `SRTP_MODE=optional`
- 로컬 `.env` 기준 `SIP_TLS_ONLY=false`
- 로컬 `.env` 기준 `AI_GRPC_TLS=false`
- host port `5060`, `5061`, `8080`, `50051` 사용 중

production compose overlay 자체는 다음 값을 강제합니다.

- `SIP_TLS_ONLY=true`
- `SRTP_MODE=mandatory`
- `AI_LOAD_TEST_GREETING_BYPASS=false`

### Docker stack

최종 healthy 상태:

- `vbgw-freeswitch`: healthy
- `vbgw-orchestrator`: healthy
- `vbgw-freeswitch-portal-1`: healthy
- `vbgw-redis`: healthy
- `vbgw-bridge`: healthy
- `vbgw-ai`: running

참고: 이전에 남아 있던 `docker compose ... up freeswitch` 프로세스 때문에 FreeSWITCH port publish가 부분 적용된 상태가 있었고, 이를 종료한 뒤 compose stack을 clean restart했습니다.

## 3. Portal Production Validation

`scripts/validate_portal_prod.sh`

- `PASS: 22`
- `WARN: 0`
- `FAIL: 0`

확인 항목:

- portal service production overlay 포함
- API upstream wiring
- `/healthz`
- security headers
- read-token proxy health check
- control-token auth endpoint

## 4. Orchestrator API / Auth

컨테이너 내부 기준:

- `/live`: `OK`
- `/ready`: `OK`
- read token `/health`: `200`
- control token `/api/v1/admin/auth/control`: `200`
- read token `/api/v1/admin/auth/control`: `403`

판정:

- 조회 토큰과 제어 토큰 경계가 의도대로 분리됨

## 5. SIP 단일 콜

### 실패한 경로

Host에서 직접 SIPp를 실행한 경로는 Docker Desktop host/container networking 때문에 실패했습니다.

- `127.0.0.1:5060`: FreeSWITCH가 INVITE를 보지만 응답 경로가 host SIPp로 돌아오지 않음
- `192.168.219.144:5060`: host route 문제로 실패
- Docker network 내부 `vbgw-freeswitch:5060`: `407 Proxy Authentication Required`
- custom authenticated UAC: 계속 `407` 재challenge 발생

### 성공한 경로

Docker network 내부에서 SIPp를 실행하고 FreeSWITCH external profile로 진입:

```bash
docker run --rm --network vbgw-freeswitch_vbgw-net \
  -v "$PWD:/work" -w /work alpine:3.19 \
  sh -lc 'apk add --no-cache sipp >/tmp/apk.log && \
    sipp -sn uac -p 5070 -s 9296 -d 8000 -l 1 -m 1 \
    vbgw-freeswitch:5080 -timeout 25 \
    -trace_msg -trace_err \
    -message_file logs/stage3_20260601/sipp_9296_messages_external.log \
    -error_file logs/stage3_20260601/sipp_9296_errors_external.log'
```

결과:

- Total calls: `1`
- Successful call: `1`
- Failed call: `0`
- SIP flow: `INVITE -> 100 -> 200 -> ACK -> pause -> BYE -> 200`
- Call length: 약 `8.338s`

## 6. CDR / Trace / Session

콜 식별자:

- FreeSWITCH UUID: `4bab4e6a-0759-4cc9-a2a6-7bf76055a5bf`
- CDR session ID: `651eaf3a-d06a-4cac-b6f2-005fbbc291ab`
- caller: `sipp`
- destination: `9296`
- service: `bot-main-dev`
- hangup: `NORMAL_CLEARING`

API 조회 결과:

- `/api/v1/admin/cdr`: `200`
- `/api/v1/admin/cdr/4bab4e6a-0759-4cc9-a2a6-7bf76055a5bf`: `200`
- `/api/v1/admin/traces/4bab4e6a-0759-4cc9-a2a6-7bf76055a5bf`: `200`
- `/api/v1/admin/sessions/4bab4e6a-0759-4cc9-a2a6-7bf76055a5bf`: `200`

CDR sample:

```json
{
  "fs_uuid": "4bab4e6a-0759-4cc9-a2a6-7bf76055a5bf",
  "caller_id": "sipp",
  "dest_number": "9296",
  "entry_number": "9296",
  "service_name": "bot-main-dev",
  "duration_seconds": 8.249640087,
  "hangup_code": "NORMAL_CLEARING"
}
```

## 7. Bridge / AI / TTS

Bridge evidence:

- metadata chunk sent to trigger greeting
- AI greeting timeout after `1500ms`
- local fallback greeting injected
- WS session summary:
  - duration: `7914ms`
  - rx frames: `391`
  - tx frames: `37`
  - gRPC sends: `392`
  - gRPC receives: `0`
  - close reason: normal websocket close

AI evidence:

- New AI session started
- Initial greeting triggered
- Initial greeting text generated
- TTS retried
- Initial greeting TTS failed with OpenAI `429 Too Many Requests`

판정:

- `AI_LOAD_TEST_GREETING_BYPASS=false` 상태에서 실제 TTS 경로가 호출됨
- 외부 quota 문제로 TTS 음성 생성은 실패
- bridge fallback greeting이 정상 동작해 통화는 정상 종료

## 8. 1차 실행 후 조치 상태

1. OpenAI TTS quota/billing 또는 대체 TTS credential 복구: 완료
2. 같은 external-profile SIPp 콜 재실행: 완료
3. `grpc_recvs > 0` 및 fallback 미사용 로그 확인: 완료
4. internal profile authenticated SIPp 시나리오 개선 또는 Stage 3 공식 검증 경로를 external profile로 고정: Stage 4 전 운영 테스트 경로 정리 시 후속 검토
5. 실제 PBX/SBC register/trunk 경로: Stage 4에서 별도 검증

## 9. 2026-06-02 TTS Credential 재확인

키 변경 전, fallback 없는 Stage 3 재검증을 진행할 수 있는지 OpenAI TTS credential 상태를 다시 확인했습니다.

확인 방식:

- `../vbgw-ai/.env`의 `OPENAI_API_KEY`로 `/v1/audio/speech` probe
- `../vbgw-ai/.env.example`의 `OPENAI_API_KEY` 후보로 `/v1/audio/speech` probe
- key 값은 출력하지 않고 fingerprint와 HTTP status만 확인

결과:

- `.env` 후보: `429 insufficient_quota`
- `.env.example` 후보: `429 insufficient_quota`

중간 판정:

- 당시 로컬에서 확인 가능한 OpenAI credential 후보로는 실제 TTS audio 생성이 불가능했습니다.
- 사용자가 quota가 살아 있는 key를 `../vbgw-ai/.env`에 반영한 뒤 아래 재검증을 수행했습니다.

## 10. 2026-06-02 TTS Credential 복구 후 재검증

요청에 따라 quota가 살아 있는 OpenAI key를 `../vbgw-ai/.env`에 반영한 뒤 `vbgw-ai`를 재기동하고 Stage 3 SIPp external-profile 콜을 재실행했습니다. key 값은 증적에 남기지 않았고, `/v1/audio/speech` probe는 `200`과 audio bytes 생성을 반환했습니다.

### Runtime 변경

- `vbgw-ai` 재기동: 완료
- `bridge` 재기동: 완료
- Stage 3 overlay: `GREETING_FALLBACK_DELAY_MS=10000`

`GREETING_FALLBACK_DELAY_MS=10000`은 실제 TTS greeting이 도착할 시간을 확보하기 위한 Stage 3 검증 설정입니다. 최종 콜에서 fallback 주입 로그는 발생하지 않았습니다.

### SIPp external-profile 콜

```bash
docker run --rm --network vbgw-freeswitch_vbgw-net \
  -v "$PWD:/work" -w /work alpine:3.19 \
  sh -lc 'apk add --no-cache sipp >/tmp/apk.log && \
    sipp -sn uac -p 5070 -s 9296 -d 15000 -l 1 -m 1 \
    vbgw-freeswitch:5080 -timeout 40 \
    -trace_msg -trace_err -trace_screen \
    -message_file logs/stage3_20260602/sipp_9296_messages_external_tts_nofallback.log \
    -error_file logs/stage3_20260602/sipp_9296_errors_external_tts_nofallback.log \
    -screen_file logs/stage3_20260602/sipp_9296_screen_external_tts_nofallback.log'
```

결과:

- Total calls: `1`
- Successful call: `1`
- Failed call: `0`
- Call length: 약 `15.063s`
- SIP flow: `INVITE -> 100 -> 200 -> ACK -> pause -> BYE -> 200`

### Bridge 증적

콜 식별자:

- FreeSWITCH UUID: `a775f738-c71d-4fa7-82ad-4e6c96b233ef`

주요 로그:

- initial metadata chunk sent
- `Sent playAudio message to FreeSWITCH`
- `frames=180`
- `pcm_bytes=115200`
- `WS session summary`
- `grpc_sends=751`
- `grpc_recvs=181`
- `tx_frames=180`
- `close_reason=ws_closed_normally`

부정 증적:

- 해당 UUID에서 `AI greeting timeout` 없음
- 해당 UUID에서 `local fallback greeting injected` 없음

### AI/TTS 증적

주요 로그:

- `New AI session started`
- `Triggering initial greeting`
- `Sending initial greeting text="안녕하세요, 보이스봇입니다. 무엇을 도와드릴까요?"`
- `Initial greeting sent`

부정 증적:

- 해당 UUID에서 `Initial greeting TTS failed` 없음
- 해당 UUID에서 `TTS retry` 없음
- 해당 UUID에서 `429` / `insufficient_quota` 없음

### CDR / Trace / Session 조회

API 조회 결과:

- `/api/v1/admin/cdr/a775f738-c71d-4fa7-82ad-4e6c96b233ef`: `200`
- `/api/v1/admin/traces/a775f738-c71d-4fa7-82ad-4e6c96b233ef`: `200`
- `/api/v1/admin/sessions/a775f738-c71d-4fa7-82ad-4e6c96b233ef`: `200`
- `/api/v1/admin/sessions/active`: `count=0`

CDR sample:

```json
{
  "fs_uuid": "a775f738-c71d-4fa7-82ad-4e6c96b233ef",
  "session_id": "d371a260-89b4-44a8-968b-ff426fdc80e5",
  "caller_id": "sipp",
  "dest_number": "9296",
  "entry_number": "9296",
  "service_name": "bot-main-dev",
  "duration_seconds": 15.041771091,
  "hangup_code": "NORMAL_CLEARING"
}
```

### 최종 판정

- `grpc_recvs > 0`: 통과 (`181`)
- 실제 TTS greeting 성공: 통과
- bridge fallback 미사용: 통과
- CDR/trace/session 증적 수집: 통과

Stage 3는 2026-06-02 재검증 기준으로 통과 처리합니다.
