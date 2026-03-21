#pragma once
#include <pjsua2.hpp>
#include <memory>

class VoicebotAiClient;
class VoicebotMediaPort;

class VoicebotCall : public pj::Call {
public:
    VoicebotCall(pj::Account &acc, int call_id = PJSUA_INVALID_ID);
    ~VoicebotCall(); // unique_ptr 소멸자는 cpp에서 완전 타입 필요

    virtual void onCallState(pj::OnCallStateParam &prm) override;
    virtual void onCallMediaState(pj::OnCallMediaStateParam &prm) override;

private:
    std::unique_ptr<VoicebotMediaPort> media_port_; // RAII - raw pointer 제거
    std::shared_ptr<VoicebotAiClient> ai_client_;
};
