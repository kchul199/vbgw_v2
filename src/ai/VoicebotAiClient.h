#pragma once
#include <grpcpp/grpcpp.h>
#include "voicebot.grpc.pb.h"
#include <memory>
#include <string>
#include <vector>
#include <thread>
#include <mutex>
#include <condition_variable>
#include <queue>
#include <functional>
#include <atomic>

class VoicebotAiClient {
public:
    VoicebotAiClient(std::shared_ptr<grpc::Channel> channel);
    ~VoicebotAiClient();
    
    // AI 수신 (TTS 버퍼) 콜백 등록
    void setTtsCallback(std::function<void(const uint8_t*, size_t)> cb);

    // Barge-in (말끊기) 플러시 콜백 등록
    void setTtsClearCallback(std::function<void()> cb);

    // AI 스트림 끊김(장애/종료) 에러 콜백 등록
    void setErrorCallback(std::function<void(const std::string&)> cb);

    // 세션(통화) 시작 시 양방향 스트리밍 파이프 오픈
    void startSession(const std::string& session_id);
    
    // RTP에서 추출한 PCM 오디오 프레임 전송 (Rx -> STT)
    void sendAudio(const std::vector<uint8_t>& pcm_data, bool is_speaking);
    
    // 세션 종료
    void endSession();

private:
    void streamWorker(); // 비동기 워커 스레드 (Tx)
    void readWorker();   // 비동기 수신 스레드 (Rx) - 지수 백오프 재연결 포함

    // 재연결을 포함한 실제 스트리밍 수행
    bool tryConnectAndRead();

    std::unique_ptr<voicebot::ai::VoicebotAiService::Stub> stub_;
    std::shared_ptr<grpc::ClientReaderWriter<voicebot::ai::AudioChunk, voicebot::ai::AiResponse>> stream_;
    grpc::ClientContext context_;
    std::string current_session_id_;
    std::function<void(const uint8_t*, size_t)> on_tts_received_;
    std::function<void()> on_tts_clear_;
    std::function<void(const std::string&)> on_error_;

    // 비동기 큐 관리용
    struct AudioItem {
        std::vector<uint8_t> pcm_data;
        bool is_speaking;
    };
    std::queue<AudioItem> audio_queue_;
    std::mutex queue_mutex_;
    std::condition_variable queue_cv_;
    std::atomic<bool> is_running_;
    std::thread worker_thread_;
    std::thread read_thread_;

    // 재연결 정책
    static constexpr int kMaxReconnectRetries = 5;
    std::atomic<int> reconnect_attempts_{0};
};
