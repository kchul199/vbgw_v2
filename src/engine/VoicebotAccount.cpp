#include <spdlog/spdlog.h>
#include "VoicebotAccount.h"
#include "VoicebotCall.h"
#include "SessionManager.h"
#include <thread>
#include <chrono>

using namespace pj;

VoicebotAccount::VoicebotAccount() {}
VoicebotAccount::~VoicebotAccount() {}

void VoicebotAccount::onRegState(OnRegStateParam &prm) {
    AccountInfo ai = getInfo();
    if (ai.regIsActive) {
        spdlog::info("[Account] Registered: {} (status={})", ai.uri, static_cast<int>(prm.code));
    } else {
        spdlog::warn("[Account] Unregistered: {} (status={})", ai.uri, static_cast<int>(prm.code));
    }
}

void VoicebotAccount::onIncomingCall(OnIncomingCallParam &iprm) {
    spdlog::info("[Account] Incoming SIP call, Call-ID: {}", iprm.callId);
    
    // 1. 최대 채널 수 제한 확인 (OOM 및 리소스 낭비 방지)
    if (!SessionManager::getInstance().canAcceptCall()) {
        spdlog::warn("[Account] Max call limit reached. Rejecting call {} with 486 Busy Here.", iprm.callId);
        VoicebotCall rejected_call(*this, iprm.callId);
        CallOpParam prm;
        prm.statusCode = PJSIP_SC_BUSY_HERE;
        try { rejected_call.hangup(prm); } catch(...) {}
        return;
    }

    // 2. 새로운 통화 세션 등록
    auto call = std::make_shared<VoicebotCall>(*this, iprm.callId);
    SessionManager::getInstance().addCall(iprm.callId, call);

    // [P4 Fix] 먼저 180 Ringing을 전송하여 PBX에 수신 알림
    try {
        CallOpParam ringing_prm;
        ringing_prm.statusCode = PJSIP_SC_RINGING; // 180 Ringing
        call->answer(ringing_prm);
        spdlog::info("[Account] Sent 180 Ringing for Call-ID: {}", iprm.callId);
    } catch (Error& err) {
        spdlog::error("[Account] Failed to send 180 Ringing: {}", err.info());
    }

    // [P4 Fix] 별도 스레드에서 AI 준비 대기 후 200 OK 응답 (메인 스레드 블로킹 방지)
    // (실제 구현에서는 VoicebotAiClient의 onReady 콜백 이후 answer() 호출이 권장됨)
    // 현재는 짧은 딜레이(200ms)를 두어 미디어 포트 초기화 시간 확보
    std::thread([call, iprm]() mutable {
        std::this_thread::sleep_for(std::chrono::milliseconds(200));
        try {
            CallOpParam ok_prm;
            ok_prm.statusCode = PJSIP_SC_OK; // 200 OK
            call->answer(ok_prm);
            spdlog::info("[Account] Sent 200 OK for Call-ID: {}", iprm.callId);
        } catch (Error& err) {
            spdlog::error("[Account] Failed to answer call: {}", err.info());
            SessionManager::getInstance().removeCall(iprm.callId);
        }
    }).detach();
}
