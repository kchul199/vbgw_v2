# Stage 5 HA / 장애 대응 검증 증적

> 실행일: 2026-06-03  
> 실행 스택: 현재 로컬 production/staging compose + Stage 4 Kamailio surrogate  
> 실행 스크립트: [run_stage5_faults.sh](/Users/kchul199/vbgw_v2/vbgw-freeswitch/scripts/run_stage5_faults.sh)  
> 최종 PASS 증적: `/Users/kchul199/vbgw_v2/vbgw-freeswitch/logs/stage5_faults_20260603_112835`

## 1. 결론

Stage 5 단일 노드 장애 주입 검증은 **부분 통과**입니다.

- 통과: node drain/resume, bridge restart 후 복구, AI engine down/recovery, Redis down/recovery, stale lease 회수
- 제한: 현재는 orchestrator 1대 구성이라 2-node failover, standby node 신규 admit, mixed-version compatibility는 미검증
- go-live 전 조치 필요: Redis 장애 중 `/health`와 `/ready`가 OK로 남는 상태, Redis `vbgw:active_calls` 드리프트 복구 정책

## 2. 실행 결과 요약

```text
PASS: Stage 5 single-node fault injection completed; drain/resume, bridge restart, AI recovery, Redis recovery, and stale lease reaping verified.
Evidence: /Users/kchul199/vbgw_v2/vbgw-freeswitch/logs/stage5_faults_20260603_112835
```

최종 상태:

```json
{
  "status": "healthy",
  "active_calls": 0,
  "esl": "connected",
  "bridge": "healthy"
}
```

Cluster node 최종 상태:

```json
{
  "node_id": "vbgw-orchestrator",
  "state": "active",
  "local_active_sessions": 0,
  "orchestrator_version": "7.0.0",
  "routing_schema_version": 1,
  "lease_schema_version": 1
}
```

최종 lease:

```json
{
  "count": 0,
  "data": [],
  "status": "success"
}
```

## 3. 검증 항목

| 항목 | 결과 | 증적 |
|------|------|------|
| node drain 신규 admit 차단 | 통과 | node state `draining`, SIPp `486 Busy Here`, successful 0 / failed 1 |
| node resume 후 신규 admit 복구 | 통과 | SIPp successful 1 / failed 0, bridge `grpc_recvs=184`, fallback injection 없음 |
| bridge restart 후 복구 | 통과 | SIPp successful 1 / failed 0, bridge `grpc_recvs=171`, fallback injection 없음 |
| AI down 중 실패 양상 캡처 | 통과 | bridge `grpc_stream_recv_unavailable`, `grpc_recvs=0`, `tx_frames=0` |
| AI restart 후 복구 | 통과 | SIPp successful 1 / failed 0, bridge `grpc_recvs=189`, 실제 initial greeting sent |
| Redis down 중 실패 양상 캡처 | 통과 | SIPp failed 1, orchestrator `Redis Lua TryAcquire failed` |
| Redis restart 후 복구 | 통과 | SIPp successful 1 / failed 0, bridge `grpc_recvs=186`, 실제 initial greeting sent |
| stale lease API 식별 | 통과 | `stale=true`, `stale_reason=owner_heartbeat_missing` |
| stale lease reaper 회수 | 통과 | reaper 후 `/api/v1/admin/cluster/leases` count 0 |

## 4. 주요 증적

### 4.1 Drain / Resume

Drain 후 node:

```json
{
  "node_id": "vbgw-orchestrator",
  "state": "draining",
  "reason": "stage5 drain admission test",
  "local_active_sessions": 0
}
```

Drain 중 SIPp:

```text
Successful call: 0
Failed call: 1
received 'SIP/2.0 486 Busy Here'
```

Resume 후 SIPp:

```text
Successful call: 1
Failed call: 0
```

Resume 후 bridge summary:

```json
{
  "grpc_sends": 594,
  "grpc_recvs": 184,
  "tx_frames": 183,
  "close_reason": "ws_closed_normally"
}
```

### 4.2 Bridge Restart

Bridge restart 후 SIPp:

```text
Successful call: 1
Failed call: 0
```

Bridge summary:

```json
{
  "grpc_sends": 578,
  "grpc_recvs": 171,
  "tx_frames": 170,
  "close_reason": "ws_closed_normally"
}
```

### 4.3 AI 장애 / 복구

AI down 중 bridge:

```text
gRPC StreamSession failed: rpc error: code = Unavailable
close_reason: grpc_stream_recv_unavailable
grpc_sends: 0
grpc_recvs: 0
tx_frames: 0
```

AI 복구 후:

```json
{
  "grpc_sends": 601,
  "grpc_recvs": 189,
  "tx_frames": 188,
  "close_reason": "ws_closed_normally"
}
```

AI log:

```text
Triggering initial greeting
Sending initial greeting text="안녕하세요, 보이스봇입니다. 무엇을 도와드릴까요?"
Initial greeting sent
```

### 4.4 Redis 장애 / 복구

Redis down 중 orchestrator:

```text
Redis Lua TryAcquire failed
dial tcp: lookup vbgw-redis on 127.0.0.11:53: no such host
```

Redis down 중 SIPp:

```text
Successful call: 0
Failed call: 1
received 'SIP/2.0 481 Call Does Not Exist'
```

Redis 복구 후:

```json
{
  "grpc_sends": 601,
  "grpc_recvs": 186,
  "tx_frames": 185,
  "close_reason": "ws_closed_normally"
}
```

### 4.5 Stale Lease

주입한 stale lease:

```json
{
  "service_name": "bot-main",
  "slot_id": "stage5-stale",
  "session_id": "stage5-stale-session",
  "owner_node_id": "stage5-missing-node",
  "stale": true,
  "stale_reason": "owner_heartbeat_missing"
}
```

Reaper 후:

```json
{
  "count": 0,
  "data": [],
  "status": "success"
}
```

## 5. 발견 이슈

### HA-1. Redis 장애 중 readiness/health가 Redis 장애를 반영하지 않음

Redis를 중지한 상태에서 신규 call admission은 실패했고 orchestrator 로그에 `Redis Lua TryAcquire failed`가 남았지만, `/ready`는 `OK`, `/health`는 `status=healthy`로 응답했습니다.

운영 영향:

- 로드밸런서나 Kubernetes readiness가 Redis 장애 노드를 계속 정상으로 볼 수 있습니다.
- 실제 신규 통화는 실패하는데 관제 화면은 정상으로 보일 수 있습니다.

권고:

- Redis-backed capacity/session/lease store를 사용하는 production profile에서는 Redis ping 또는 최근 Redis error budget을 `/ready`와 `/health`에 반영해야 합니다.
- Redis 장애 시 신규 admit 차단 사유를 API와 metric에 명확히 노출해야 합니다.

### HA-2. Redis `vbgw:active_calls` 드리프트 발견

첫 실행 시작 시 `/health.active_calls=98`인데 `/api/v1/admin/services`의 service active 합계는 0, `/api/v1/admin/cluster/leases`는 0이었습니다. 로컬 테스트 지속 중 누적된 stale counter로 판단되어 증적 저장 후 `vbgw:active_calls=0`으로 정리하고 장애 주입을 진행했습니다.

운영 영향:

- 전역 capacity가 실제보다 높게 계산되어 신규 admit이 불필요하게 막힐 수 있습니다.
- Release가 누락되거나 process crash 후 복구될 때 counter 재조정 로직이 필요합니다.

권고:

- active counter를 source of truth로 단독 사용하지 말고 session/lease inventory와 reconciler를 둡니다.
- orchestrator 기동 시 local session 없음 + lease 없음이면 stale global counter를 경고/정정하는 운영 절차를 추가합니다.

### HA-3. AI down 중 SIP dialog는 성공처럼 보일 수 있음

AI engine이 내려간 상태에서 bridge는 `grpc_stream_recv_unavailable`로 즉시 닫혔지만 SIPp 기본 UAC 관점에서는 통화가 성공으로 기록될 수 있습니다. `tx_frames=0`, `grpc_recvs=0`을 함께 봐야 실제 음성 서비스 실패를 판정할 수 있습니다.

권고:

- AI unavailable 시 SIP 응답 정책을 명확히 정해야 합니다. 즉시 busy/fail로 거절할지, 짧은 안내 후 종료할지 운영 정책이 필요합니다.
- Stage 6 부하 검증에서는 SIP 성공률과 media/gRPC 성공률을 별도 SLI로 집계합니다.

## 6. 남은 검증

- 2-node orchestrator 구성에서 node hard failure 후 stale lease 회수와 standby 신규 admit 검증
- Redis HA 또는 managed Redis 장애 전환 검증
- 실제 고객/운영 PBX/SBC endpoint 기준 동일 장애 시나리오 재실행
- queue/human fallback이 활성화된 서비스에서 drain 중 기존 세션 유지와 신규 queue 정책 검증

## 7. 2026-06-03 P0 후속 수정 및 재검증

Stage 5에서 발견된 Redis readiness 및 active counter drift 이슈를 우선 수정했습니다.

수정:

- `/ready`가 Redis-backed session store `PING` 실패 시 `503`을 반환
- `/health` 응답에 `redis` 필드 추가
- Redis 장애 시 `/health.status=degraded`, `redis=unreachable`
- `vbgw:active_calls` reconciler 추가
  - cluster heartbeat의 fresh `local_active_sessions` 합계를 우선 사용
  - 현재 node local session count를 하한으로 사용해 통화 중 heartbeat 지연 race 방지
  - Redis session key TTL 잔존분은 active source of truth로 사용하지 않음
- release/rollback decrement를 Lua floor-decrement로 변경해 counter가 음수로 내려가지 않도록 보강

검증:

```text
READY_DOWN: 503 {"status":"not_ready","reason":"session store unavailable"}
HEALTH_DOWN: 503 {"status":"degraded","active_calls":0,"redis":"unreachable","bridge":"healthy"}
READY_RECOVERED: 200 OK
HEALTH_RECOVERED: {"status":"healthy","active_calls":0,"redis":"healthy","bridge":"healthy"}
```

추가로 post-P0 Stage 4 surrogate 콜 중 reconciler/release race로 `active_calls=-1`이 한 차례 재현되어 floor-decrement와 local-active 하한 보정 후 재검증했습니다.

최종 post-P0 상태:

```json
{
  "status": "healthy",
  "active_calls": 0,
  "esl": "connected",
  "redis": "healthy",
  "bridge": "healthy"
}
```

## 8. 2026-06-03 HA P0 2-node 정책 검증

남은 HA P0 중 아래 항목을 코드와 테스트로 보강했습니다.

수정:

- `capacity.Manager.RenewLeases()`가 distributed lease renew 실패 또는 renew miss를 더 이상 무시하지 않음
- active slot의 lease renew 실패가 관찰되면 capacity manager가 `distributed lease renew degraded` 상태로 신규 admit 차단
- renew가 정상화되거나 active slot이 없으면 degraded 상태 해제

검증:

```bash
cd orchestrator
go test ./internal/capacity ./internal/cluster ./cmd
```

결과:

```text
ok  	vbgw-orchestrator/internal/capacity
ok  	vbgw-orchestrator/internal/cluster
ok  	vbgw-orchestrator/cmd
```

검증한 시나리오:

- 2-node distributed lease가 동일 slot 중복 할당을 차단
- node-a가 slot lease를 가진 상태에서 heartbeat loss 발생
- stale lease를 reaper가 회수
- standby node-b가 같은 service/slot에 신규 admit 성공
- Redis/lease renew failure 후 신규 admit이 `distributed lease renew degraded` 사유로 차단

남은 리허설:

- 실제 compose 2-container orchestrator 동시 운영
- live `/api/v1/admin/cluster/nodes`에서 두 node heartbeat 확인
- 한 container stop 후 standby API로 신규 admit 확인
- queue/human fallback 포함 drain 정책 리허설

## 9. 2026-06-03 Live 2-container HA 리허설

실제 compose network에 standby orchestrator container를 추가 기동해 live HA 리허설을 수행했습니다.

실행 스크립트:

```bash
./scripts/run_stage5_live_ha.sh
```

증적 디렉터리:

```text
logs/stage5_live_ha_20260603_192430
```

결과:

```text
PASS: Stage 5 live 2-node HA rehearsal completed; standby heartbeat, primary loss, stale lease reap, and primary recovery verified.
```

검증 내용:

- primary + standby 동시 기동 시 `/cluster/nodes` count 2
- standby node 상태 `active`
- primary orchestrator stop 후 standby `/ready` 유지
- primary stop 후 `/cluster/nodes` count 1, standby만 active
- primary-owned stale lease 주입 후 standby reaper가 lease 회수
- primary restart 후 `/cluster/nodes` count 2 회복
- cleanup 후 현재 live stack은 primary 단일 노드 healthy 상태로 복귀

초기 2-node 상태:

```json
{
  "count": 2,
  "data": [
    {"node_id": "vbgw-orchestrator", "state": "active"},
    {"node_id": "vbgw-orchestrator-standby", "state": "active"}
  ]
}
```

Primary stop 후 standby 상태:

```json
{
  "count": 1,
  "data": [
    {"node_id": "vbgw-orchestrator-standby", "state": "active"}
  ]
}
```

Stale lease 회수 후:

```json
{
  "count": 0,
  "data": [],
  "status": "success"
}
```

Primary recovery:

```json
{
  "count": 2,
  "data": [
    {"node_id": "vbgw-orchestrator", "state": "active"},
    {"node_id": "vbgw-orchestrator-standby", "state": "active"}
  ]
}
```

최종 live 상태:

```json
{
  "status": "healthy",
  "active_calls": 0,
  "esl": "connected",
  "redis": "healthy",
  "bridge": "healthy"
}
```
