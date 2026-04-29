package ai

import (
	"context"

	"github.com/sashabaranov/go-openai"
)

// SpeechEngine은 AI 음성 비서의 핵심 기능을 정의합니다.
// 향후 OpenAI 외에도 다른 제공자(Provider)를 통해 구현될 수 있습니다.
type SpeechEngine interface {
	// Transcribe는 오디오 바이트 데이터를 텍스트로 변환합니다. (STT)
	Transcribe(ctx context.Context, audioData []byte) (string, error)

	// GenerateResponse는 입력된 텍스트를 바탕으로 답변을 생성합니다. (LLM — 단발)
	GenerateResponse(ctx context.Context, prompt string) (string, error)

	// GenerateResponseWithHistory는 대화 이력을 포함하여 답변을 생성합니다. (LLM — 멀티턴)
	GenerateResponseWithHistory(ctx context.Context, history []openai.ChatCompletionMessage) (string, error)

	// Synthesize는 텍스트를 오디오 데이터로 합성합니다. (TTS)
	Synthesize(ctx context.Context, text string) ([]byte, error)
}
