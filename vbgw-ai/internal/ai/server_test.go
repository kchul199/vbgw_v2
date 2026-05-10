package ai

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/sashabaranov/go-openai"
	pb "vbgw-ai/proto/voicebot"

	"vbgw-ai/internal/config"
)

// ── Mock SpeechEngine ──

type mockEngine struct {
	transcribeFunc   func(ctx context.Context, data []byte) (string, error)
	generateFunc     func(ctx context.Context, prompt string) (string, error)
	generateHistFunc func(ctx context.Context, history []openai.ChatCompletionMessage) (string, error)
	synthesizeFunc   func(ctx context.Context, text string) ([]byte, error)
}

func (m *mockEngine) Transcribe(ctx context.Context, data []byte) (string, error) {
	if m.transcribeFunc != nil {
		return m.transcribeFunc(ctx, data)
	}
	return "안녕하세요", nil
}

func (m *mockEngine) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	if m.generateFunc != nil {
		return m.generateFunc(ctx, prompt)
	}
	return "도움을 드리겠습니다.", nil
}

func (m *mockEngine) GenerateResponseWithHistory(ctx context.Context, history []openai.ChatCompletionMessage) (string, error) {
	if m.generateHistFunc != nil {
		return m.generateHistFunc(ctx, history)
	}
	return "도움을 드리겠습니다.", nil
}

func (m *mockEngine) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if m.synthesizeFunc != nil {
		return m.synthesizeFunc(ctx, text)
	}
	// Return 640 bytes (20ms at 16kHz mono 16-bit) — no resample needed if already 16kHz
	// For test, return 24kHz sized data (3:2 ratio → 960 bytes input = 640 bytes output)
	return make([]byte, 960), nil
}

// ── Mock gRPC Stream ──

type mockStream struct {
	recvCh   chan *pb.AudioChunk
	sendCh   chan *pb.AiResponse
	recvOnce sync.Once
}

func newMockStream() *mockStream {
	return &mockStream{
		recvCh: make(chan *pb.AudioChunk, 100),
		sendCh: make(chan *pb.AiResponse, 100),
	}
}

func (m *mockStream) Send(resp *pb.AiResponse) error {
	select {
	case m.sendCh <- resp:
		return nil
	default:
		return nil
	}
}

func (m *mockStream) Recv() (*pb.AudioChunk, error) {
	chunk, ok := <-m.recvCh
	if !ok {
		return nil, io.EOF
	}
	return chunk, nil
}

func (m *mockStream) SetHeader(_ interface{}) error  { return nil }
func (m *mockStream) SendHeader(_ interface{}) error { return nil }
func (m *mockStream) SetTrailer(_ interface{})       {}
func (m *mockStream) Context() context.Context       { return context.Background() }
func (m *mockStream) SendMsg(_ interface{}) error    { return nil }
func (m *mockStream) RecvMsg(_ interface{}) error    { return nil }

// ── Tests ──

func TestNewServer(t *testing.T) {
	engine := &mockEngine{}
	server := NewServer(engine)
	if server == nil {
		t.Fatal("NewServer returned nil")
	}
	if server.store == nil {
		t.Fatal("Server store is nil")
	}
}

func TestSessionStore_AppendAndGet(t *testing.T) {
	store := newSessionStore("")
	sessionID := "test-session-1"

	// Initially empty
	history := store.getHistory(sessionID)
	if history != nil {
		t.Fatalf("Expected nil history, got %d messages", len(history))
	}

	// Append first message — should auto-add system prompt
	store.appendMessage(sessionID, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: "hello",
	})

	history = store.getHistory(sessionID)
	if len(history) != 2 { // system + user
		t.Fatalf("Expected 2 messages, got %d", len(history))
	}
	if history[0].Role != openai.ChatMessageRoleSystem {
		t.Fatalf("Expected system message first, got %s", history[0].Role)
	}
	if history[1].Content != "hello" {
		t.Fatalf("Expected 'hello', got '%s'", history[1].Content)
	}
}

func TestSessionStore_MaxHistory(t *testing.T) {
	store := newSessionStore("")
	sessionID := "test-history-cap"

	// Add 50 messages — should be capped to 41 (system + 40 recent)
	for i := 0; i < 50; i++ {
		store.appendMessage(sessionID, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: fmt.Sprintf("msg-%d", i),
		})
	}

	history := store.getHistory(sessionID)
	if len(history) > 41 {
		t.Fatalf("Expected max 41 messages, got %d", len(history))
	}
	if history[0].Role != openai.ChatMessageRoleSystem {
		t.Fatalf("System prompt should be preserved, got %s", history[0].Role)
	}
}

func TestSessionStore_RemoveSession(t *testing.T) {
	store := newSessionStore("")
	sessionID := "test-remove"

	store.appendMessage(sessionID, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: "test",
	})

	store.removeSession(sessionID)
	history := store.getHistory(sessionID)
	if history != nil {
		t.Fatal("Session should be removed")
	}
}

func TestSafeStreamConcurrency(t *testing.T) {
	ms := newMockStream()
	ss := &safeStream{stream: &mockPBStream{ms: ms}}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = ss.Send(&pb.AiResponse{
				Type:      pb.AiResponse_TTS_AUDIO,
				AudioData: []byte{byte(n)},
			})
		}(i)
	}
	wg.Wait()
	// No race panic = success
}

func TestSendPCMChunks(t *testing.T) {
	ms := newMockStream()
	ss := &safeStream{stream: &mockPBStream{ms: ms}}

	// 1920 bytes = 3 chunks of 640
	audio := make([]byte, 1920)
	sendPCMChunks(ss, audio)

	count := 0
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case <-ms.sendCh:
			count++
		case <-timeout:
			if count != 3 {
				t.Fatalf("Expected 3 chunks, got %d", count)
			}
			return
		}
	}
}

func TestSendPCMChunksPartial(t *testing.T) {
	ms := newMockStream()
	ss := &safeStream{stream: &mockPBStream{ms: ms}}

	// 700 bytes = 1 full chunk (640) + 1 partial chunk (60)
	audio := make([]byte, 700)
	sendPCMChunks(ss, audio)

	count := 0
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case <-ms.sendCh:
			count++
		case <-timeout:
			if count != 2 {
				t.Fatalf("Expected 2 chunks, got %d", count)
			}
			return
		}
	}
}

func TestSendInitialGreeting_BypassMode(t *testing.T) {
	originalCfg := config.AppConfig
	t.Cleanup(func() {
		config.AppConfig = originalCfg
	})

	config.AppConfig = &config.Config{}
	config.AppConfig.LoadTest.GreetingBypass = true
	config.AppConfig.LoadTest.GreetingToneMs = 120
	config.AppConfig.LoadTest.GreetingToneHz = 550
	config.AppConfig.LoadTest.GreetingAmpInt16 = 2000

	engineCalled := false
	engine := &mockEngine{
		synthesizeFunc: func(ctx context.Context, text string) ([]byte, error) {
			engineCalled = true
			return nil, fmt.Errorf("should not call synthesize in bypass mode")
		},
	}
	server := NewServer(engine)
	ms := newMockStream()
	ss := &safeStream{stream: &mockPBStream{ms: ms}}

	server.sendInitialGreeting(ss, "bypass-session")

	timeout := time.After(100 * time.Millisecond)
	frameCount := 0
	for {
		select {
		case resp := <-ms.sendCh:
			if resp == nil {
				t.Fatal("expected greeting frame")
			}
			if len(resp.AudioData) == 0 {
				t.Fatal("expected greeting audio data")
			}
			frameCount++
		case <-timeout:
			if frameCount == 0 {
				t.Fatal("expected at least one bypass greeting frame")
			}
			if engineCalled {
				t.Fatal("expected synthesize to be bypassed")
			}
			return
		}
	}
}

// mockPBStream adapts our mockStream to the pb interface
type mockPBStream struct {
	ms *mockStream
	pb.VoicebotAiService_StreamSessionServer
}

func (m *mockPBStream) Send(resp *pb.AiResponse) error {
	return m.ms.Send(resp)
}

func (m *mockPBStream) Recv() (*pb.AudioChunk, error) {
	return m.ms.Recv()
}

func (m *mockPBStream) Context() context.Context {
	return context.Background()
}
