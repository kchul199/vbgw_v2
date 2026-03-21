#pragma once
#include <unordered_map>
#include <memory>
#include <mutex>
#include <cstdlib>
#include "VoicebotCall.h"

// 싱글톤 기반의 쓰레드 세이프 콜 라이프사이클 관리자
class SessionManager {
public:
    static SessionManager& getInstance() {
        static SessionManager instance;
        return instance;
    }

    void addCall(int call_id, std::shared_ptr<VoicebotCall> call) {
        std::lock_guard<std::mutex> lock(mutex_);
        calls_[call_id] = call;
    }

    void removeCall(int call_id) {
        std::lock_guard<std::mutex> lock(mutex_);
        calls_.erase(call_id);
    }

    std::shared_ptr<VoicebotCall> getCall(int call_id) {
        std::lock_guard<std::mutex> lock(mutex_);
        auto it = calls_.find(call_id);
        if (it != calls_.end()) {
            return it->second;
        }
        return nullptr;
    }

    // [P5 Fix] 최대 동시 채널 수를 환경 변수(MAX_CONCURRENT_CALLS)에서 읽음
    bool canAcceptCall() {
        std::lock_guard<std::mutex> lock(mutex_);
        return calls_.size() < static_cast<size_t>(getMaxCalls());
    }

    size_t getActiveCallCount() {
        std::lock_guard<std::mutex> lock(mutex_);
        return calls_.size();
    }

    // 데몬 종료 시 모든 활성 통화 일괄 종료 (Deadlock 방지 위해 복사 후 순회)
    void hangupAllCalls() {
        std::vector<std::shared_ptr<VoicebotCall>> active_calls;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            for (auto& pair : calls_) {
                active_calls.push_back(pair.second);
            }
        }
        for (auto& call : active_calls) {
            try {
                pj::CallOpParam prm;
                prm.statusCode = PJSIP_SC_DECLINE;
                call->hangup(prm);
            } catch(...) {}
        }
    }

private:
    SessionManager() = default;
    ~SessionManager() = default;
    SessionManager(const SessionManager&) = delete;
    SessionManager& operator=(const SessionManager&) = delete;

    // 환경 변수에서 최대 채널 수 읽기 (기본값: 100)
    static int getMaxCalls() {
        const char* env = std::getenv("MAX_CONCURRENT_CALLS");
        if (env) {
            try { return std::stoi(env); } catch (...) {}
        }
        return 100;
    }

    std::unordered_map<int, std::shared_ptr<VoicebotCall>> calls_;
    std::mutex mutex_;
};
