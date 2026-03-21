#include <spdlog/spdlog.h>
#include "VoicebotCall.h"
#include "VoicebotMediaPort.h"
#include "SessionManager.h"
#include "../ai/VoicebotAiClient.h"
#include <grpcpp/grpcpp.h>

using namespace pj;

VoicebotCall::VoicebotCall(Account &acc, int call_id)
    : Call(acc, call_id), media_port_(nullptr), ai_client_(nullptr) {}

// unique_ptr<VoicebotMediaPort> 소멸자는 완전한 타입이 필요하므로 .cpp에서 정의
VoicebotCall::~VoicebotCall() {}

void VoicebotCall::onCallState(OnCallStateParam &prm) {
    CallInfo ci = getInfo();
    spdlog::info("[Call] ID={} State={} Reason={}", ci.id, ci.stateText, ci.lastReason);

    if (ci.state == PJSIP_INV_STATE_DISCONNECTED) {
        // unique_ptr이 있으므로 media_port_ 는 자동 해제됨
        SessionManager::getInstance().removeCall(ci.id);
        spdlog::info("[Call] ID={} Removed from SessionManager.", ci.id);
    }
}

void VoicebotCall::onCallMediaState(OnCallMediaStateParam &prm) {
    CallInfo ci = getInfo();
    for (unsigned i = 0; i < ci.media.size(); ++i) {
        if (ci.media[i].type == PJMEDIA_TYPE_AUDIO && getMedia(i)) {
            AudioMedia *aud_med = (AudioMedia *)getMedia(i);
            
            // [P2 Fix] AI 엔진 주소를 환경 변수로 외부화 (기본값: localhost:50051)
            if (!ai_client_) {
                const char* ai_addr_env = std::getenv("AI_ENGINE_ADDR");
                std::string ai_addr = ai_addr_env ? ai_addr_env : "localhost:50051";
                spdlog::info("[Call] Connecting to AI Engine at: {}", ai_addr);

                auto channel = grpc::CreateChannel(ai_addr, grpc::InsecureChannelCredentials());
                ai_client_ = std::make_shared<VoicebotAiClient>(channel);
                
                ai_client_->setTtsCallback([this](const uint8_t* data, size_t len) {
                    if (media_port_) {
                        media_port_->writeTtsAudio(data, len);
                    }
                });
                
                ai_client_->setTtsClearCallback([this]() {
                    if (media_port_) {
                        media_port_->clearTtsAudio();
                        media_port_->resetVad();
                    }
                });
                
                ai_client_->setErrorCallback([this](const std::string& err) {
                    spdlog::error("🚨 [Call] Hanging up due to permanent AI Error: {}", err);
                    try {
                        pj::CallOpParam prm;
                        prm.statusCode = PJSIP_SC_SERVICE_UNAVAILABLE;
                        hangup(prm);
                    } catch (const pj::Error& e) {
                        spdlog::warn("[Call] Error during hangup: {}", e.info());
                    }
                });
                
                char session_id_str[32];
                snprintf(session_id_str, sizeof(session_id_str), "%d", getInfo().id);
                ai_client_->startSession(session_id_str);
            }

            // [P1 Fix] unique_ptr로 자동 메모리 관리
            if (!media_port_) {
                media_port_ = std::make_unique<VoicebotMediaPort>();
                media_port_->setAiClient(ai_client_);
            }
            
            aud_med->startTransmit(*media_port_);
            media_port_->startTransmit(*aud_med);
            
            spdlog::info("[Call] AI Media Port connected. RTP Stream converting to PCM.");
        }
    }
}
