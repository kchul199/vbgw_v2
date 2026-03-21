# Voicebot Gateway (VBGW)

Voicebot Gateway (VBGW)는 C++와 PJSUA2(PJSIP)를 기반으로 구축된 고성능 AI 콜봇 게이트웨이 솔루션입니다. PBX 연동, 실시간 STT/TTS 양방향 스트리밍, 그리고 Edge AI(Silero VAD)를 통한 저지연 음성 활동 감지를 지원합니다.

## 🚀 주요 기능 (Key Features)

- **SIP & Media Core**: PJSIP(PJSUA2)를 이용한 안정적인 SIP 시그널링 및 RTP 미디어 처리.
- **AI Streaming**: gRPC Bi-directional Streaming을 통한 STT/TTS 엔진과의 초고속 양방향 통신.
- **Edge VAD**: ONNX Runtime 기반 Silero VAD 엔진을 탑재하여 게이트웨이 단에서 실시간 음성 감지 및 노이즈 필터링.
- **Barge-in (말끊기)**: 화자가 말을 하는 도중(TTS 재생 중) 사용자가 말을 시작하면(VAD 감지), 즉시 TTS 재생을 중단하고 리스닝 상태로 전환.
- **PBX 연동**: Asterisk, FreePBX 등 엔터프라이즈 PBX 시스템 자동 등록 및 관중 호 제어.

## 📁 프로젝트 구조 (Project Structure)

- `src/`: C++ 소스 코드 (Core Engine, AI Client, VAD 등)
- `protos/`: gRPC 프로토콜 정의 파일 (`voicebot.proto`)
- `models/`: ONNX VAD 모델 파일 (`silero_vad.onnx`)
- `config/`: 설정 파일
- `docs/`: 아키텍처 및 상세 설계 문서
- `src/emulator/`: 테스팅용 Python gRPC 에뮬레이터 (Mock Server)

## 🛠️ 설치 방법 (Installation)

### 1. 필수 의존성 설치 (macOS / Homebrew 기준)

본 프로젝트는 macOS (Apple Silicon 권장) 환경에서 최적화되어 있습니다. 아래 명령어로 필요한 라이브러리를 설치하십시오.

```bash
brew install cmake pjproject grpc protobuf openssl spdlog boost onnxruntime
```

### 2. C++ 게이트웨이 빌드

CMake를 사용하여 프로젝트를 빌드합니다.

```bash
mkdir build && cd build
cmake ..
make -j$(nproc)
```

빌드가 완료되면 `build/vbgw` 실행 파일이 생성됩니다.

### 3. Python 에뮬레이터 설정

```bash
cd src/emulator
python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
```

## ⚙️ 설정 (Configuration)

게이트웨이는 환경 변수를 통해 PBX 등록 정보를 설정할 수 있습니다. 설정하지 않을 경우 로컬 모드(Direct IP Call)로 동작합니다.

| 환경 변수 | 설명 | 예시 |
| :--- | :--- | :--- |
| `PBX_URI` | PBX 서버 Registrar URI | `sip:192.168.1.100` |
| `PBX_ID_URI` | PBX 등록 ID URI | `sip:1001@192.168.1.100` |
| `PBX_USERNAME` | PBX 인증 사용자 아이디 | `1001` |
| `PBX_PASSWORD` | PBX 인증 비밀번호 | `password123` |

## 🧪 테스트 방법 (Testing Guide)

이 섹션에서는 에뮬레이터를 사용하여 시스템을 테스트하는 상세한 방법을 설명합니다.

### 1단계: AI 에뮬레이터 실행 (Mock Server)
STT/TTS 역할을 가상으로 수행할 에뮬레이터를 먼저 실행합니다.

```bash
cd src/emulator
source venv/bin/activate

# 방법 A: 단순 Mock 서버 (삐- 소리 응답)
python mock_server.py

# 방법 B: 고급 에뮬레이터 (WAV 파일 재생 및 사용자 음성 캡처)
python emulator.py
```
*에뮬레이터는 `:50051` 포트에서 gRPC 요청을 대기합니다.*

### 2단계: Voicebot Gateway 실행
게이트웨이를 실행하여 에뮬레이터와 연결하고 SIP 호를 대기합니다.

```bash
# 로컬 모드 실행 (직접 IP 호출 대기)
./build/vbgw
```
*실행 시 `[VBGW] Local Mode Enabled (No PBX). Direct IP calls: sip:voicebot@127.0.0.1` 로그가 확인되어야 합니다.*

### 3단계: SIP Softphone으로 전화 걸기
실제 전화기 대신 사용 중인 PC에 SIP Softphone(예: **Linphone**, **MicroSIP**)을 설치하여 테스트합니다.

1.  **호출 대상**: `sip:voicebot@127.0.0.1:5060` (또는 실제 설치된 PC의 IP)
2.  **연결 확인**: 전화가 연결되면 에뮬레이터 로그에 `New Session Started` 또는 `New SIP Call connected` 메시지가 나타납니다.
3.  **VAD 테스트**: 마이크에 대고 말을 시작하면 게이트웨이 로그에 `[VAD] Speaking started`가 찍히며 AI로 음성 데이터가 전달됩니다.
4.  **TTS 테스트**: 말을 멈추면 에뮬레이터가 모의 답변(삐- 소리 또는 샘플 음원)을 게이트웨이로 전송하고, 스피커를 통해 들리는지 확인합니다.
5.  **말끊기(Barge-in) 테스트**: AI가 답변(소리 재생)을 하는 도중에 말을 해보십시오. AI 소리가 즉시 끊어지고 다시 리스닝 모드로 들어가는지 확인합니다.

## 🏗️ 시스템 아키텍처 (Architecture)

```mermaid
graph TD
    User[고객 📱] <--> |Voice| PBX[PBX / SBC]
    
    subgraph Callbot Gateway [VBGW (C++)]
        SIP_End[SIP Endpoint]
        Media_Port[RTP Media Port]
        VAD[Silero VAD (ONNX)]
        AI_Connector[gRPC Stream Connector]
        
        SIP_End <--> Media_Port
        Media_Port <--> VAD
        Media_Port <--> AI_Connector
    end
    
    PBX <--> |SIP/RTP| SIP_End
    AI_Connector <--> |gRPC Bi-Stream| AI_Engines[AI Engines (STT/TTS)]
```

---
© 2026 Voicebot Gateway Team. All rights reserved.
