package openai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

//go:generate go tool moq -out moq_test.go . chatCompleter

// chatCompleter streams chat completions. *openai.ChatCompletionService
// satisfies it.
type chatCompleter interface {
	NewStreaming(ctx context.Context, body openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk]
}
