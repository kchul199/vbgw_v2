#include <spdlog/spdlog.h>
#include "VoicebotAiClient.h"
#include <chrono>
#include <thread>

using grpc::Channel;
using grpc::Status;
using voicebot::ai::AudioChunk;
using voicebot::ai::AiResponse;

VoicebotAiClient::VoicebotAiClient(std::shared_ptr<Channel> channel)
    : stub_(voicebot::ai::VoicebotAiService::NewStub(channel)), is_running_(false) {
}

VoicebotAiClient::~VoicebotAiClient() {
    endSession();
}

void VoicebotAiClient::setTtsCallback(std::function<void(const uint8_t*, size_t)> cb) {
    on_tts_received_ = cb;
}

void VoicebotAiClient::setTtsClearCallback(std::function<void()> cb) {
    on_tts_clear_ = cb;
}

void VoicebotAiClient::setErrorCallback(std::function<void(const std::string&)> cb) {
    on_error_ = cb;
}

void VoicebotAiClient::startSession(const std::string& session_id) {
    current_session_id_ = session_id;
    stream_ = stub_->StreamSession(&context_);
    
    is_running_ = true;
    reconnect_attempts_ = 0;
    worker_thread_ = std::thread(&VoicebotAiClient::streamWorker, this);
    read_thread_ = std::thread(&VoicebotAiClient::readWorker, this);

    spdlog::info("[gRPC] AI Stream Session started for: {}", session_id);
}

// [P9 Fix] is_speaking 파라미터를 AudioChunk에 올바르게 반영
void VoicebotAiClient::sendAudio(const std::vector<uint8_t>& pcm_data, bool is_speaking) {
    if (!is_running_) return;

    {
        std::lock_guard<std::mutex> lock(queue_mutex_);
        audio_queue_.push({pcm_data, is_speaking});
    }
    queue_cv_.notify_one();
}

void VoicebotAiClient::streamWorker() {
    while (is_running_) {
        AudioItem item;
        {
            std::unique_lock<std::mutex> lock(queue_mutex_);
            queue_cv_.wait(lock, [this] { return !audio_queue_.empty() || !is_running_; });
            
            if (!is_running_ && audio_queue_.empty()) break;
            
            item = std::move(audio_queue_.front());
            audio_queue_.pop();
        }

        if (stream_) {
            AudioChunk chunk;
            chunk.set_session_id(current_session_id_);
            chunk.set_audio_data(item.pcm_data.data(), item.pcm_data.size());
            chunk.set_is_speaking(item.is_speaking); // [P9 Fix] VAD 결과 올바르게 반영

            if (!stream_->Write(chunk)) {
                spdlog::warn("[gRPC] Stream write failed for session: {}", current_session_id_);
            }
        }
    }
}

void VoicebotAiClient::endSession() {
    is_running_ = false;
    queue_cv_.notify_all();

    if (stream_) {
        stream_->WritesDone();
    }
    
    if (worker_thread_.joinable()) worker_thread_.join();
    if (read_thread_.joinable()) read_thread_.join();

    if (stream_) {
        Status status = stream_->Finish();
        if (status.ok()) {
            spdlog::info("[gRPC] Stream closed successfully for session: {}", current_session_id_);
        } else {
            spdlog::warn("[gRPC] Stream finished with status: {}", status.error_message());
        }
        stream_.reset();
    }
}

// [P3 Fix] 지수 백오프 재연결 로직: 최대 kMaxReconnectRetries 회 시도
bool VoicebotAiClient::tryConnectAndRead() {
    AiResponse response;
    while (is_running_ && stream_ && stream_->Read(&response)) {
        if (response.type() == AiResponse::TTS_AUDIO) {
            const std::string& audio = response.audio_data();
            if (on_tts_received_ && !audio.empty()) {
                on_tts_received_(reinterpret_cast<const uint8_t*>(audio.data()), audio.size());
            }
        }
        else if (response.type() == AiResponse::STT_RESULT) {
            spdlog::info("[AI STT] User ({}): {}", current_session_id_, response.text_content());
        }
        else if (response.type() == AiResponse::END_OF_TURN) {
            if (response.clear_buffer() && on_tts_clear_) {
                spdlog::warn("🚨 [Barge-In] Flushed Gateway TTS RingBuffer! Session: {}", current_session_id_);
                on_tts_clear_();
            }
        }
        // 정상 수신마다 재연결 카운터 초기화
        reconnect_attempts_ = 0;
    }
    return is_running_; // true = 비정상 종료 (재연결 필요), false = 정상 종료
}

void VoicebotAiClient::readWorker() {
    while (is_running_) {
        bool needs_reconnect = tryConnectAndRead();

        if (needs_reconnect && is_running_) {
            if (reconnect_attempts_ >= kMaxReconnectRetries) {
                spdlog::error("[gRPC] Max reconnect retries ({}) exceeded. Triggering error callback.", kMaxReconnectRetries);
                if (on_error_) {
                    on_error_("gRPC STT/TTS Stream permanently disconnected after retries");
                }
                return;
            }

            // 지수 백오프: 500ms * 2^n (최대 16초)
            int wait_ms = 500 * (1 << reconnect_attempts_);
            wait_ms = std::min(wait_ms, 16000);
            reconnect_attempts_++;
            spdlog::warn("[gRPC] Stream disconnected. Reconnecting in {}ms (attempt {}/{})",
                         wait_ms, reconnect_attempts_.load(), kMaxReconnectRetries);

            std::this_thread::sleep_for(std::chrono::milliseconds(wait_ms));
            
            // 스트림 재생성 시도: ClientContext는 copy 불가, 멤버 교체로 처리
            context_.TryCancel();
            // 새 스트리밍 세션 오픈 (기존 컨텍스트 취소 후 새 스텁 스트림으로 교체)
            stream_ = stub_->StreamSession(&context_);
            if (!stream_) {
                spdlog::error("[gRPC] Failed to create new stream. Aborting reconnect.");
                if (on_error_) on_error_("gRPC stream re-creation failed");
                return;
            }
            spdlog::info("[gRPC] Reconnected stream for session: {}", current_session_id_);
        }
    }
}
