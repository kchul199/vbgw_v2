# Zoiper (SIP Softphone) 설정 가이드

VBGW 시스템과 연결하여 AI 보이스봇과 통화하기 위한 Zoiper 앱 설정 방법입니다.

현재 개발환경의 실제 접속 정보는 아래 스크립트로 바로 확인할 수 있습니다.

```bash
./scripts/softphone_test_info.sh
```

## 1. 계정 기본 정보

| 항목 | 설정값 | 비고 |
| :--- | :--- | :--- |
| **Account Type** | SIP | |
| **Domain (Host)** | 개발 PC의 reachable LAN IP | macOS Docker 환경에서는 `127.0.0.1`보다 실제 host LAN IP 사용을 권장 |
| **User ID / Extension** | `1000` | (또는 1000 ~ 1019 중 하나) |
| **Password** | `.env`의 `ESL_PASSWORD` 값 | FreeSWITCH 내선 비밀번호와 동일 |
| **Port** | `5060` | SIP UDP/TCP 포트 |
| **Transport** | `UDP` 권장 | 로컬 테스트는 UDP가 가장 단순하고 재현성이 높음 |
| **Preferred Codec** | `PCMU`, `PCMA` 우선 | 로컬 테스트 시 Opus보다 문제 분리가 쉬움 |

## 2. 기기별 설정 단계

### [PC/Desktop 버전]
1. **계정 추가**: `Settings` > `Accounts` > `Add Account` 클릭.
2. **유형 선택**: `SIP` 선택.
3. **정보 입력**:
   - **User / User ID**: `1000`
   - **Password**: `.env`의 `ESL_PASSWORD`
   - **Domain / Outbound Proxy**: 개발 PC의 reachable LAN IP + `:5060`
4. **연결 확인**: 계정 이름 옆의 상태 표시등이 **초록색(Registered)**으로 바뀌는지 확인합니다.
5. **권장 고급 설정**:
   - **Transport**: `UDP`
   - **STUN / ICE / TURN**: `Off`
   - **SRTP / TLS**: `Off`
   - **Preferred Codec**: `PCMU`, `PCMA` 우선

### [모바일/Mobile 버전]
1. **계정 생성**: 앱 실행 후 `Config` > `Accounts` > `+` (Add Account).
2. **메뉴 선택**: `Yes` > `Select a provider` > `Manual configuration` > `SIP account`.
3. **정보 입력**:
   - **Account name**: `VBGW-Test`
   - **Host / Domain**: 개발 PC의 LAN IP
   - **Username**: `1000`
   - **Password**: `.env`의 `ESL_PASSWORD`
4. **저장 및 등록**: 상단의 `Register` 버튼을 눌러 상태가 **OK** 또는 **Registered**가 되는지 확인합니다.

## 3. 네트워크 유의사항

> [!IMPORTANT]
> - **동일 네트워크**: 모바일 기기가 서버(PC)와 동일한 Wi-Fi 네트워크에 연결되어 있어야 합니다.
> - **방화벽 해제**: PC의 방화벽에서 `5060 (UDP)` 및 `16384-16484 (UDP)` 포트가 허용되어 있어야 소리가 정상적으로 들립니다.
> - **STUN 설정**: 만약 외부망에서 접근 시 `STUN Settings`를 비활성화하거나 서버 IP로 고정해야 할 수 있습니다.
> - **같은 Mac/PC에서 Zoiper 사용 시**: macOS Docker 환경에서는 `127.0.0.1` 대신 개발 PC의 실제 host LAN IP로 수동 등록하세요. `sip tcp/udp not found`는 자동 검색 실패 메시지일 수 있으니 `Manual configuration`으로 직접 `Domain`, `Port 5060`, `UDP`를 넣으면 됩니다.
> - **다른 기기에서 Zoiper 사용 시**: `./scripts/sync_external_ip.sh lan`으로 `.env`를 LAN 모드로 바꾸고 `docker compose up -d freeswitch` 후, 그때 표시되는 reachable LAN IP를 사용하세요.
> - **Zoiper의 `sip tcp/udp not found` 메시지**: 보통 서비스 검색 실패나 자동 설정 실패입니다. `Manual configuration`으로 직접 `SIP`, `Domain`, `Port 5060`, `UDP`를 넣고 진행하면 됩니다.

## 4. AI 테스트 방법

Zoiper 숫자 패드에서 **`9196`**번을 누르고 전화를 겁니다.

1. **연결 확인**: 통화가 성공적으로 시작되는지 확인합니다.
2. **발화 테스트**: "안녕하세요" 또는 "반가워"라고 말씀해 보세요.
3. **AI 응답 확인**: 약 1~3초 내에 AI의 답변이 들려야 합니다.

> [!NOTE]
> `9196`은 단순 에코 테스트가 아니라 `FreeSWITCH -> Bridge -> AI` 경로를 모두 거치는 테스트 번호입니다.
> 그래서 SIP 등록은 정상이어도 `bridge` 또는 `vbgw-ai`가 비정상이면 통화가 연결된 채로 묵음이 될 수 있습니다.

---
*문서 작성일: 2026-04-20 | VBGW 기술 지원*
