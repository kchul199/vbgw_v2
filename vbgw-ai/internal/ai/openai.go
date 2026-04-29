package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"time"
	"vbgw-ai/internal/config"

	"github.com/sashabaranov/go-openai"
)

const (
	maxRetries    = 3
	baseBackoffMs = 1000
)

type OpenAIProvider struct {
	client *openai.Client
	config *config.Config
}

func NewOpenAIProvider(cfg *config.Config) *OpenAIProvider {
	return &OpenAIProvider{
		client: openai.NewClient(cfg.OpenAI.APIKey),
		config: cfg,
	}
}

// Transcribe: PCM 오디오 데이터를 Whisper MP3/WAV 형식 세그먼트로 변환하여 텍스트 추출
func (p *OpenAIProvider) Transcribe(ctx context.Context, audioData []byte) (string, error) {
	if len(audioData) == 0 {
		return "", nil
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(baseBackoffMs)*math.Pow(2, float64(attempt-1))) * time.Millisecond
			slog.Warn("STT retry", "attempt", attempt+1, "backoff_ms", backoff.Milliseconds())
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		// OpenAI API는 파일 형식을 요구하므로 메모리 버퍼를 파일처럼 래핑합니다.
		// Whisper API용 더미 파일명 제공 (확장자가 중요함)
		reader := bytes.NewReader(audioData)

		req := openai.AudioRequest{
			Model:    p.config.OpenAI.STTModel,
			FilePath: "input.wav", // 실제 파일이 아닌 형식 힌트
			Reader:   reader,
		}

		resp, err := p.client.CreateTranscription(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("OpenAI STT error (attempt %d): %w", attempt+1, err)
			continue
		}
		return resp.Text, nil
	}

	return "", lastErr
}

// GenerateResponse: GPT-4o를 이용한 단발 응답 생성
func (p *OpenAIProvider) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	history := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: p.systemPrompt(),
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: prompt,
		},
	}
	return p.GenerateResponseWithHistory(ctx, history)
}

// GenerateResponseWithHistory: 대화 이력 기반 응답 생성 (멀티턴)
func (p *OpenAIProvider) GenerateResponseWithHistory(ctx context.Context, history []openai.ChatCompletionMessage) (string, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(baseBackoffMs)*math.Pow(2, float64(attempt-1))) * time.Millisecond
			slog.Warn("LLM retry", "attempt", attempt+1, "backoff_ms", backoff.Milliseconds())
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
			}
		}

		req := openai.ChatCompletionRequest{
			Model:     p.config.OpenAI.LLMModel,
			Messages:  history,
			MaxTokens: 500,
		}

		resp, err := p.client.CreateChatCompletion(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("OpenAI LLM error (attempt %d): %w", attempt+1, err)
			continue
		}

		if len(resp.Choices) == 0 {
			return "죄송합니다. 말씀을 이해하지 못했습니다.", nil
		}
		return resp.Choices[0].Message.Content, nil
	}

	// 모든 retry 실패 시 fallback 메시지 반환
	slog.Error("LLM all retries exhausted", "err", lastErr)
	return "일시적인 문제가 발생했습니다. 잠시 후 다시 말씀해 주세요.", lastErr
}

// Synthesize: 텍스트를 PCM 데이터로 변환 (브릿지 전달용)
func (p *OpenAIProvider) Synthesize(ctx context.Context, text string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(float64(baseBackoffMs)*math.Pow(2, float64(attempt-1))) * time.Millisecond
			slog.Warn("TTS retry", "attempt", attempt+1, "backoff_ms", backoff.Milliseconds())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		req := openai.CreateSpeechRequest{
			Model:          openai.SpeechModel(p.config.OpenAI.TTSModel),
			Input:          text,
			Voice:          openai.SpeechVoice(p.config.OpenAI.TTSVoice),
			ResponseFormat: openai.SpeechResponseFormatPcm, // Raw PCM 16kHz 16bit Mono
		}

		resp, err := p.client.CreateSpeech(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("OpenAI TTS error (attempt %d): %w", attempt+1, err)
			continue
		}
		defer resp.Close()

		data, err := io.ReadAll(resp)
		if err != nil {
			lastErr = fmt.Errorf("OpenAI TTS read error (attempt %d): %w", attempt+1, err)
			continue
		}
		return data, nil
	}

	return nil, lastErr
}

func (p *OpenAIProvider) systemPrompt() string {
	if p.config.OpenAI.SystemPrompt != "" {
		return p.config.OpenAI.SystemPrompt
	}
	return "당신은 지능형 음성봇 고객 응대 상담원입니다. 친절하고 간결하게 응답하세요."
}
