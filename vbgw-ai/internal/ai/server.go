package ai

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"time"
	"vbgw-ai/internal/config"

	"github.com/sashabaranov/go-openai"
	pb "vbgw-ai/proto/voicebot"
)

// safeStream wraps gRPC stream with mutex to prevent concurrent Send() calls.
type safeStream struct {
	stream pb.VoicebotAiService_StreamSessionServer
	mu     sync.Mutex
}

func (s *safeStream) Send(resp *pb.AiResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream.Send(resp)
}

func (s *safeStream) Recv() (*pb.AudioChunk, error) {
	return s.stream.Recv()
}

// sessionStore manages per-session conversation history.
type sessionStore struct {
	mu           sync.RWMutex
	sessions     map[string][]openai.ChatCompletionMessage
	systemPrompt string
}

func newSessionStore(systemPrompt string) *sessionStore {
	if systemPrompt == "" {
		systemPrompt = "당신은 지능형 음성봇 고객 응대 상담원입니다. 친절하고 간결하게 응답하세요."
	}
	return &sessionStore{
		sessions:     make(map[string][]openai.ChatCompletionMessage),
		systemPrompt: systemPrompt,
	}
}

func (s *sessionStore) getHistory(sessionID string) []openai.ChatCompletionMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	history, ok := s.sessions[sessionID]
	if !ok {
		return nil
	}
	// Return a copy to avoid race
	out := make([]openai.ChatCompletionMessage, len(history))
	copy(out, history)
	return out
}

func (s *sessionStore) appendMessage(sessionID string, msg openai.ChatCompletionMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.sessions[sessionID]

	// 히스토리가 없으면 시스템 프롬프트부터 시작
	if len(history) == 0 {
		history = []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: s.systemPrompt},
		}
	}

	history = append(history, msg)

	// 최대 20턴(40 메시지 + system) 유지 — 오래된 대화 제거
	const maxMessages = 41
	if len(history) > maxMessages {
		// System prompt 유지 + 최근 메시지만
		history = append(history[:1], history[len(history)-maxMessages+1:]...)
	}

	s.sessions[sessionID] = history
}

func (s *sessionStore) removeSession(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

type Server struct {
	pb.UnimplementedVoicebotAiServiceServer
	engine  SpeechEngine
	store   *sessionStore
}

func NewServer(engine SpeechEngine) *Server {
	systemPrompt := ""
	if config.AppConfig != nil {
		systemPrompt = config.AppConfig.OpenAI.SystemPrompt
	}
	return &Server{
		engine: engine,
		store:  newSessionStore(systemPrompt),
	}
}

func (s *Server) StreamSession(stream pb.VoicebotAiService_StreamSessionServer) error {
	var sessionID string
	var lastSpeaking bool = false
	var audioBuffer bytes.Buffer
	var greetingSent bool = false
	var sessionStartTime time.Time

	ss := &safeStream{stream: stream}

	for {
		chunk, err := ss.Recv()
		if err == io.EOF {
			if sessionID != "" {
				s.store.removeSession(sessionID)
			}
			return nil
		}
		if err != nil {
			slog.Error("Stream recv error", "err", err)
			if sessionID != "" {
				s.store.removeSession(sessionID)
			}
			return err
		}

		if sessionID == "" && chunk.SessionId != "" {
			sessionID = chunk.SessionId
			sessionStartTime = time.Now()
			slog.Info("New AI session started", "session_id", sessionID)
		}

		if !greetingSent && sessionID != "" {
			greetingSent = true
			slog.Info("Triggering initial greeting", "session_id", sessionID)
			go s.sendInitialGreeting(ss, sessionID)
		}

		// 1. 발화 중(IsSpeaking=true)이면 오디오를 버퍼에 수집
		if len(chunk.AudioData) > 0 {
			// 초기 2초간은 VAD 무시 (라인 노이즈 방어)
			isSpeaking := chunk.IsSpeaking
			if time.Since(sessionStartTime) < 2*time.Second {
				isSpeaking = false
			}

			if isSpeaking {
				audioBuffer.Write(chunk.AudioData)
			}

			// 2. 발화 종료(Fall-edge) 감지 — AI 파이프라인 가동
			if lastSpeaking && !isSpeaking {
				capturedAudio := make([]byte, audioBuffer.Len())
				copy(capturedAudio, audioBuffer.Bytes())
				audioBuffer.Reset()

				if len(capturedAudio) > 0 {
					slog.Info("End of speech detected — processing pipeline", "session_id", sessionID, "pcm_size", len(capturedAudio))
					s.sendCushionPhrase(ss, sessionID)
					go s.processAIResponse(ss, sessionID, capturedAudio)
				}
			}
			lastSpeaking = isSpeaking
		}
	}
}

// sendInitialGreeting: 세션 시작 시 최초 인사말 송출
func (s *Server) sendInitialGreeting(ss *safeStream, sessionID string) {
	ctx := context.Background()
	greetingText := config.AppConfig.OpenAI.GreetingMsg
	if greetingText == "" {
		greetingText = "안녕하세요, 보이스봇입니다. 무엇을 도와드릴까요?"
	}

	slog.Info("Sending initial greeting", "session_id", sessionID, "text", greetingText)

	// TTS 합성
	raw24kAudio, err := s.engine.Synthesize(ctx, greetingText)
	if err != nil {
		slog.Error("Initial greeting TTS failed", "session_id", sessionID, "err", err)
		return
	}

	audioResponse := Resample24To16(raw24kAudio)
	sendPCMChunks(ss, audioResponse)
	slog.Info("Initial greeting sent", "session_id", sessionID)
}

// sendCushionPhrase: 대기 안내 메시지 송출 (실제 TTS 합성)
func (s *Server) sendCushionPhrase(ss *safeStream, sessionID string) {
	ss.Send(&pb.AiResponse{
		Type:        pb.AiResponse_STT_RESULT,
		TextContent: "...", // 인식 중 표시
	})

	cushionMsg := config.AppConfig.OpenAI.CushionMsg
	if cushionMsg == "" {
		cushionMsg = "잠시만 기다려주세요..."
	}

	// 실제 TTS로 쿠션 오디오 합성
	ctx := context.Background()
	raw24kAudio, err := s.engine.Synthesize(ctx, cushionMsg)
	if err != nil {
		slog.Warn("Cushion TTS synthesis failed, sending silence", "session_id", sessionID, "err", err)
		// Fallback: 200ms silence
		ss.Send(&pb.AiResponse{
			Type:      pb.AiResponse_TTS_AUDIO,
			AudioData: make([]byte, 6400),
		})
		return
	}

	audioResponse := Resample24To16(raw24kAudio)
	sendPCMChunks(ss, audioResponse)
}

// processAIResponse: STT -> LLM -> TTS 파이프라인 실행 (대화 이력 포함)
func (s *Server) processAIResponse(ss *safeStream, sessionID string, pcmData []byte) {
	ctx := context.Background()

	slog.Info("Starting AI pipeline", "session_id", sessionID, "pcm_size", len(pcmData))

	// Step 1: STT (WAV 헤더 추가 필수)
	wavData := AddWAVHeader(pcmData)
	recognizedText, err := s.engine.Transcribe(ctx, wavData)
	if err != nil {
		slog.Error("STT failed", "session_id", sessionID, "err", err)
		return
	}
	if recognizedText == "" {
		slog.Warn("STT result is empty", "session_id", sessionID)
		return
	}
	slog.Info("STT Result Success", "session_id", sessionID, "text", recognizedText)

	ss.Send(&pb.AiResponse{
		Type:        pb.AiResponse_STT_RESULT,
		TextContent: recognizedText,
	})

	// Step 2: LLM (대화 이력 기반)
	s.store.appendMessage(sessionID, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: recognizedText,
	})

	history := s.store.getHistory(sessionID)
	slog.Info("Requesting LLM response", "session_id", sessionID, "history_len", len(history))

	responseText, err := s.engine.GenerateResponseWithHistory(ctx, history)
	if err != nil {
		slog.Error("LLM failed", "session_id", sessionID, "err", err)
		// Retry 후에도 실패했으면 fallback 메시지가 반환됨
		if responseText == "" {
			return
		}
	}

	// 어시스턴트 응답을 이력에 추가
	s.store.appendMessage(sessionID, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: responseText,
	})
	slog.Info("LLM Response Success", "session_id", sessionID, "response", responseText)

	// Step 3: TTS
	slog.Info("Synthesizing TTS audio", "session_id", sessionID)
	raw24kAudio, err := s.engine.Synthesize(ctx, responseText)
	if err != nil {
		slog.Error("TTS failed", "session_id", sessionID, "err", err)
		return
	}

	// OpenAI 24kHz -> Bridge 16kHz Resampling
	audioResponse := Resample24To16(raw24kAudio)
	slog.Info("TTS Synthesis Success", "session_id", sessionID, "original_size", len(raw24kAudio), "resampled_size", len(audioResponse))

	// Step 4: 스트리밍 응답 (PCM 데이터 브릿지 규격인 20ms 단위로 쪼개서 전송)
	sendPCMChunks(ss, audioResponse)

	// 전송 완료 신호
	ss.Send(&pb.AiResponse{
		Type: pb.AiResponse_END_OF_TURN,
	})
	slog.Info("AI Turn completed and audio sent", "session_id", sessionID)
}

// sendPCMChunks sends PCM audio in 20ms chunks (640 bytes at 16kHz mono 16-bit).
func sendPCMChunks(ss *safeStream, audio []byte) {
	// 16kHz Mono 16bit = 32000 bytes/sec. 20ms = 640 bytes.
	chunkSize := 640
	for i := 0; i < len(audio); i += chunkSize {
		end := i + chunkSize
		if end > len(audio) {
			end = len(audio)
		}
		ss.Send(&pb.AiResponse{
			Type:      pb.AiResponse_TTS_AUDIO,
			AudioData: audio[i:end],
		})
	}
}
