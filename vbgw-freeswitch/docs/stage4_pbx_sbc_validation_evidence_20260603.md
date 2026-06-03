# Stage 4 PBX/SBC 연동 실검증 증적

> 실행일: 2026-06-03  
> 실행 조합: `docker-compose.yml` + `docker-compose.prod.yml` + `docker-compose.stage3.yml`로 기동된 현재 로컬 production/staging stack  
> 목적: 실제 PBX/SBC `trunk` / `register` 경로 검증 가능 여부와 런타임 gateway 상태 확인

## 1. 결론

Stage 4는 두 단계로 판정합니다.

- 실제 고객/운영 PBX/SBC 대상 검증: **대상 미구성으로 보류**
- 오픈소스 SBC surrogate 검증: **Kamailio 기반 trunk/register 대역 검증 통과**

현재 런타임은 Stage 3 통합 스택으로 정상 기동 중이지만, PBX/SBC interconnect가 꺼져 있고 실제 PBX/SBC proxy, register 계정, standby proxy가 비어 있습니다. 따라서 `pbx-main` / `pbx-standby` gateway XML이 생성되지 않았고, FreeSWITCH Sofia와 orchestrator gateway API 모두 실제 gateway를 보유하지 않습니다.

이후 오픈소스 SIP server/SBC인 Kamailio를 `stage4-kamailio`로 같은 Docker network에 기동해 `pbx-main` trunk/register 경로를 검증했습니다.

## 2. 현재 컨테이너 상태

확인 결과:

- `vbgw-freeswitch`: healthy
- `vbgw-orchestrator`: healthy
- `vbgw-bridge`: healthy
- `vbgw-ai`: running
- `vbgw-redis`: healthy
- `vbgw-freeswitch-portal-1`: healthy

## 3. PBX/SBC 환경 변수 상태

비밀값은 출력하지 않고 길이 또는 공란 여부만 확인했습니다.

현재 `.env`에는 구형 PBX 키가 남아 있습니다.

- `PBX_HOST=`: empty
- `PBX_USERNAME=`: empty
- `PBX_PASSWORD=`: empty
- `PBX_REGISTER=false`

현재 코드와 entrypoint가 실제로 읽는 신규 interconnect 키는 아래 상태입니다.

- `PBX_INTERCONNECT_ENABLED=false`
- `PBX_MAIN_GATEWAY=pbx-main`
- `PBX_MAIN_REGISTER=true`
- `PBX_MAIN_PROXY=`: empty
- `PBX_MAIN_USERNAME=`: empty
- `PBX_MAIN_PASSWORD=`: empty
- `PBX_STANDBY_ENABLED=false`
- `PBX_STANDBY_GATEWAY=pbx-standby`
- `PBX_STANDBY_PROXY=`: empty

판정:

- 현재 `.env`는 Stage 4 실제 PBX/SBC 검증용 값이 채워져 있지 않습니다.
- `PBX_HOST` / `PBX_USERNAME` / `PBX_PASSWORD` / `PBX_REGISTER`는 현재 gateway 생성 로직에서 사용되지 않습니다.
- 실제 검증에는 `PBX_INTERCONNECT_ENABLED=true`와 `PBX_MAIN_PROXY` 계열 값이 필요합니다.

## 4. FreeSWITCH Gateway 상태

생성 파일 확인:

```text
/etc/freeswitch/sip_profiles/external/pbx-*.xml: 없음
```

Sofia 상태:

```text
external              profile  sip:mod_sofia@192.168.219.144:5080  RUNNING (0)
internal              profile  sip:mod_sofia@192.168.219.144:5060  RUNNING (0)
external::example.com gateway  sip:joeuser@example.com             NOREG
```

Gateway 조회:

```text
sofia status gateway pbx-main
Invalid Gateway!

sofia status gateway pbx-standby
Invalid Gateway!
```

판정:

- FreeSWITCH는 정상 기동 중입니다.
- 실제 PBX/SBC용 `pbx-main` / `pbx-standby` gateway가 없습니다.
- 이 상태에서는 register 상태 `REGED` 또는 trunk 상태 `NOREG`를 검증할 수 없습니다.

## 5. Orchestrator Gateway API

조회:

```http
GET /api/v1/admin/gateways
```

응답:

```json
{
  "count": 0,
  "data": [],
  "decisions": [],
  "status": "success"
}
```

판정:

- Orchestrator에도 gateway health snapshot이 없습니다.
- `PBX_INTERCONNECT_ENABLED=false` 상태라 probe/health producer가 gateway를 만들지 않습니다.

## 6. Stage 4 실검증을 위한 필요 값

### Register 방식

외부 PBX/SBC가 REGISTER 인증을 요구하는 경우:

```env
PBX_INTERCONNECT_ENABLED=true
PBX_MAIN_REGISTER=true
PBX_MAIN_PROXY=<sbc-host-or-ip>:5060
PBX_MAIN_REALM=<realm>
PBX_MAIN_USERNAME=<username>
PBX_MAIN_PASSWORD=<password>
PBX_MAIN_FROM_DOMAIN=<domain>
PBX_MAIN_REGISTER_TRANSPORT=udp
PBX_MAIN_PING_SECONDS=25
```

기대 증적:

- `/etc/freeswitch/sip_profiles/external/pbx-main.xml` 생성
- `sofia status gateway pbx-main`에 `REGED`
- `GET /api/v1/admin/gateways`에 `pbx-main`, `mode=register`, `health_class=healthy`
- 실제 PBX/SBC에서 대표번호 또는 DID 인입
- CDR에 gateway/source, destination, service routing 기록

### Trunk 방식

외부 PBX/SBC가 정적 IP peer/trunk 방식인 경우:

```env
PBX_INTERCONNECT_ENABLED=true
PBX_MAIN_REGISTER=false
PBX_MAIN_PROXY=<pbx-or-sbc-ip>:5060
PBX_MAIN_REALM=<pbx-or-sbc-ip>:5060
PBX_MAIN_FROM_DOMAIN=<pbx-or-sbc-ip>
PBX_MAIN_FROM_USER=vbgw
PBX_MAIN_PING_SECONDS=15
```

기대 증적:

- `/etc/freeswitch/sip_profiles/external/pbx-main.xml` 생성
- `sofia status gateway pbx-main`에 `NOREG` 또는 유효한 trunk gateway 상태
- `GET /api/v1/admin/gateways`에 `pbx-main`, `mode=trunk`, `health_class=healthy`
- 실제 PBX/SBC에서 VBGW `external` profile로 INVITE 인입
- 대표번호/DID가 `bot-main` 계열 service로 라우팅
- CDR/trace/session 조회 성공

## 7. 다음 실행 절차

1. 실제 PBX/SBC 방식 선택: `register` 또는 `trunk`
2. `.env`에 신규 `PBX_MAIN_*` 키 입력
3. 필요 시 standby도 `PBX_STANDBY_*` 키 입력
4. `freeswitch`와 `orchestrator` 재기동
5. `pbx-main.xml` 생성 확인
6. `sofia status gateway pbx-main` 확인
7. `GET /api/v1/admin/gateways` 확인
8. 실제 PBX/SBC에서 대표번호 또는 DID 콜 인입
9. CDR/trace/session 증적 수집

## 8. 현재 Stage 4 판정

- trunk gateway 검증: 보류, `PBX_MAIN_PROXY` 미구성
- register gateway 검증: 보류, `PBX_MAIN_PROXY` / 계정 미구성
- standby failover 검증: 보류, `PBX_STANDBY_ENABLED=false`
- gateway health API 검증: 보류, gateway snapshot 없음
- 실제 대표번호/DID 인입: 보류, 외부 PBX/SBC 대상 미구성

Stage 4는 PBX/SBC 접속 정보가 반영된 뒤 재실행해야 합니다.

## 9. 2026-06-03 Kamailio SBC Surrogate 검증

실제 PBX/SBC 접속 정보가 아직 없으므로, 오픈소스 SIP server/SBC인 Kamailio를 Docker network 내부에 기동해 Stage 4 대역 검증을 수행했습니다.

추가한 하네스:

- [docker-compose.stage4-kamailio.yml](/Users/kchul199/vbgw_v2/vbgw-freeswitch/docker-compose.stage4-kamailio.yml)
- [tests/kamailio-stage4/kamailio.cfg](/Users/kchul199/vbgw_v2/vbgw-freeswitch/tests/kamailio-stage4/kamailio.cfg)
- [scripts/run_stage4_kamailio.sh](/Users/kchul199/vbgw_v2/vbgw-freeswitch/scripts/run_stage4_kamailio.sh)

실행:

```bash
./scripts/run_stage4_kamailio.sh
```

증적 디렉터리:

```text
logs/stage4_kamailio_20260603_055948
```

### Trunk 모드

구성:

- `PBX_INTERCONNECT_ENABLED=true`
- `PBX_MAIN_REGISTER=false`
- `PBX_MAIN_PROXY=stage4-kamailio:5060`
- `PBX_MAIN_GATEWAY=pbx-main`

FreeSWITCH gateway:

```text
Name    	pbx-main
Proxy   	sip:stage4-kamailio:5060
State   	NOREG
Status  	UP
```

Orchestrator gateway API:

```json
{
  "gateway_name": "pbx-main",
  "mode": "trunk",
  "health_class": "healthy"
}
```

SIPp 경로:

```text
SIPp -> stage4-kamailio:5060 -> vbgw-freeswitch:5080 -> default routing -> bridge -> vbgw-ai
```

SIPp 결과:

- Successful call: `1`
- Failed call: `0`
- INVITE: `1`
- 200 OK: `1`
- BYE: `1`
- BYE 200 OK: `1`
- Call Length: `15.257s`

Bridge / AI:

- FreeSWITCH UUID: `56aaa14f-ce67-4586-9666-f8ca2868f09d`
- `Sent playAudio message to FreeSWITCH`
- `frames=185`
- `pcm_bytes=118000`
- `grpc_sends=748`
- `grpc_recvs=186`
- `close_reason=ws_closed_normally`
- AI `Initial greeting sent`
- fallback greeting injection 없음

CDR:

```json
{
  "session_id": "5670943c-e37d-465d-b795-3e180841596b",
  "fs_uuid": "56aaa14f-ce67-4586-9666-f8ca2868f09d",
  "caller_id": "sipp",
  "dest_number": "9296",
  "entry_number": "9296",
  "service_name": "bot-main-dev",
  "duration_seconds": 15.13811984,
  "hangup_code": "NORMAL_CLEARING"
}
```

API 조회:

- `/api/v1/admin/cdr/56aaa14f-ce67-4586-9666-f8ca2868f09d`: `200`
- `/api/v1/admin/traces/56aaa14f-ce67-4586-9666-f8ca2868f09d`: `200`
- `/api/v1/admin/sessions/56aaa14f-ce67-4586-9666-f8ca2868f09d`: `200`

### Register 모드

구성:

- `PBX_INTERCONNECT_ENABLED=true`
- `PBX_MAIN_REGISTER=true`
- `PBX_MAIN_PROXY=stage4-kamailio:5060`
- `PBX_MAIN_REGISTER_PROXY=stage4-kamailio:5060`
- `PBX_MAIN_USERNAME=pbx-main`
- `PBX_MAIN_PASSWORD=<stage4 test secret>`

FreeSWITCH gateway:

```text
Name    	pbx-main
Proxy   	sip:stage4-kamailio:5060
State   	REGED
Status  	UP
```

Kamailio REGISTER 로그:

```text
stage4 REGISTER from sip:pbx-main@stage4-kamailio contact=<sip:pbx-main@192.168.219.144:5080;transport=udp;gw=pbx-main>
```

Orchestrator gateway API:

```json
{
  "gateway_name": "pbx-main",
  "mode": "register",
  "health_class": "healthy"
}
```

판정:

- trunk gateway health: 통과
- trunk inbound call through SBC surrogate: 통과
- actual TTS/gRPC path through trunk: 통과
- register gateway `REGED`: 통과
- register gateway API health: 통과
- 실제 고객/운영 PBX/SBC trunk/register: 아직 별도 검증 필요

## 10. 발견 및 수정

Kamailio surrogate 검증 중 trunk gateway health classifier 버그를 발견해 수정했습니다.

문제:

- FreeSWITCH `sofia status gateway pbx-main` 출력은 trunk 정상 상태에서 `State=NOREG`, `Status=UP`를 반환합니다.
- 같은 출력에 `FailedCallsIN=0`, `FailedCallsOUT=0` 필드가 포함됩니다.
- 기존 classifier가 문자열 전체에서 `FAILED`를 검색해 정상 trunk를 `unhealthy`로 오판했습니다.

수정:

- `State` / `Status` / `PingState` 행만 파싱해 `DOWN` / `FAILED`를 판단하도록 변경
- `FailedCallsIN/OUT` 카운터는 health 판정에서 제외
- 회귀 테스트 추가

검증:

```bash
cd orchestrator
go test ./internal/interconnect ./cmd -run 'TestClassifyGatewayHealth|TestGatewayHealthy'
```

결과:

```text
ok  	vbgw-orchestrator/internal/interconnect
ok  	vbgw-orchestrator/cmd
```

## 11. 2026-06-03 Post-P0 Stage 4 재검증

Stage 5 P0 수정 후 현재 Kamailio surrogate register 경로를 다시 검증했습니다.

증적 디렉터리:

```text
logs/stage4_post_p0_20260603_122152
```

Gateway API:

```json
{
  "gateway_name": "pbx-main",
  "mode": "register",
  "health_class": "healthy",
  "State": "REGED",
  "Status": "UP"
}
```

SIPp 결과:

```text
Successful call: 1
Failed call: 0
```

Bridge summary:

```json
{
  "grpc_sends": 588,
  "grpc_recvs": 180,
  "tx_frames": 179,
  "close_reason": "ws_closed_normally"
}
```

AI greeting:

```text
Sending initial greeting text="안녕하세요, 보이스봇입니다. 무엇을 도와드릴까요?"
Initial greeting sent
```

최종 health:

```json
{
  "status": "healthy",
  "active_calls": 0,
  "esl": "connected",
  "redis": "healthy",
  "bridge": "healthy"
}
```

판정:

- Kamailio surrogate register inbound 경로는 post-P0 기준 재통과
- 실제 TTS/gRPC path는 `grpc_recvs=180`, fallback injection 없음으로 통과
- active call counter는 통화 후 0으로 정상 복귀
- 실제 고객/운영 PBX/SBC endpoint 값은 `.env`에 아직 없으므로 실제 고객망 Stage 4 검증은 보류
