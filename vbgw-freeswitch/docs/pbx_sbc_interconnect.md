# PBX / SBC Interconnect Guide

이 문서는 VBGW가 외부 PBX 또는 SBC와 연동할 때
`register` 방식과 `trunk` 방식 모두를 설정하는 최소 절차를 설명합니다.

## 개요

- 논리 게이트웨이 이름은 고정입니다.
  - Primary: `pbx-main`
  - Standby: `pbx-standby`
- Orchestrator는 항상 위 게이트웨이 이름으로 발신합니다.
- FreeSWITCH는 컨테이너 시작 시 env 값을 읽어 실제 gateway XML을 생성합니다.
- 인바운드 콜은 `sip_gateway_name`이 `pbx-main|pbx-standby`인 경우에만 `default` 컨텍스트로 넘깁니다.

## 공통 설정

`.env`에 아래 공통 키를 넣습니다.

```env
PBX_INTERCONNECT_ENABLED=true
PBX_MAIN_GATEWAY=pbx-main
PBX_STANDBY_ENABLED=false
PBX_STANDBY_GATEWAY=pbx-standby
```

적용:

```bash
docker compose up -d --build freeswitch orchestrator
```

생성 확인:

```bash
docker exec vbgw-freeswitch ls -la /etc/freeswitch/sip_profiles/external
docker exec vbgw-freeswitch fs_cli -H 127.0.0.1 -P 8021 -p "$ESL_PASSWORD" -x "sofia status gateway pbx-main"
```

## 1. Register 방식

외부 PBX/SBC가 REGISTER 인증을 요구하면 아래처럼 설정합니다.

```env
PBX_INTERCONNECT_ENABLED=true
PBX_MAIN_REGISTER=true
PBX_MAIN_PROXY=sbc.example.com:5060
PBX_MAIN_REALM=sbc.example.com
PBX_MAIN_USERNAME=gwuser
PBX_MAIN_PASSWORD=gwpass
PBX_MAIN_FROM_DOMAIN=sbc.example.com
PBX_MAIN_REGISTER_TRANSPORT=udp
PBX_MAIN_PING_SECONDS=25
```

기대 상태:

- `sofia status gateway pbx-main` 결과에 `REGED`
- Orchestrator는 등록 상태를 정상으로 판단

## 2. Trunk 방식

외부 PBX/SBC가 정적 IP 기반 SIP peer/trunk라면 아래처럼 설정합니다.

```env
PBX_INTERCONNECT_ENABLED=true
PBX_MAIN_REGISTER=false
PBX_MAIN_PROXY=10.10.10.5:5060
PBX_MAIN_PING_SECONDS=15
```

선택 값:

```env
PBX_MAIN_REALM=10.10.10.5:5060
PBX_MAIN_FROM_DOMAIN=10.10.10.5
PBX_MAIN_FROM_USER=vbgw
```

기대 상태:

- `sofia status gateway pbx-main` 결과에 `NOREG` 또는 유효한 gateway 상태
- Orchestrator는 trunk 모드에서 `NOREG`를 정상으로 판단

## 3. Standby 게이트웨이

```env
PBX_STANDBY_ENABLED=true
PBX_STANDBY_REGISTER=false
PBX_STANDBY_PROXY=10.10.10.6:5060
PBX_STANDBY_PING_SECONDS=15
```

그러면 outbound originate는 `pbx-main|pbx-standby` 순서로 발신합니다.

## 4. 주의사항

- 현재 구조는 `external` 프로필 아래에 gateway를 생성합니다.
- `internal` 프로필은 여전히 소프트폰/개발 테스트용으로 보는 것이 안전합니다.
- trunk 보안 하드닝은 별도로 진행하는 것이 좋습니다.
  - `accept-blind-reg/auth` 제거
  - ACL 기반 trusted peer 제한
  - 필요 시 TLS subject pinning
