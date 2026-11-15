package fakeollama

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

func TestPromptTokens(t *testing.T) {
	t.Run("counts the words of every message", func(t *testing.T) {
		msgs := []openaiwire.Message{msg("system", "be brief"), msg("user", "one two  three")}
		assert.Equal(t, 5, promptTokens(msgs))
	})
	t.Run("counts nothing without messages", func(t *testing.T) {
		assert.Zero(t, promptTokens(nil))
	})
}

func TestOwnedBy(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"qwen3:8b", "library"},
		{"jmorgan/model:latest", "jmorgan"},
		{"registry.example.com/team/model", "team"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ownedBy(tt.name))
		})
	}
}

func TestPieces(t *testing.T) {
	tests := []struct {
		name string
		text string
		n    int
		want []string
	}{
		{"empty", "", 2, nil},
		{"one word", "hi", 2, []string{"hi"}},
		{"pairs keep spaces", "a b c d e", 2, []string{"a b ", "c d ", "e"}},
		{"single words", "a b", 1, []string{"a ", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pieces(tt.text, tt.n))
		})
	}
}

func TestReply_finishReason(t *testing.T) {
	t.Run("stop for content", func(t *testing.T) {
		assert.Equal(t, "stop", *reply{content: "x"}.finishReason())
	})
	t.Run("tool_calls for tool calls", func(t *testing.T) {
		assert.Equal(t, "tool_calls", *reply{toolCalls: []openaiwire.ToolCall{{}}}.finishReason())
	})
}

func TestUsageFor(t *testing.T) {
	req := openaiwire.ChatRequest{Messages: []openaiwire.Message{msg("system", "be brief"), msg("user", "one two three")}}
	rep := reply{reasoning: "a b", content: "c", toolCalls: []openaiwire.ToolCall{{Function: openaiwire.FunctionCall{Arguments: `{"x":"y"}`}}}}
	assert.Equal(t, openaiwire.Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10}, usageFor(req, rep))
}

func TestDeltas(t *testing.T) {
	t.Run("orders reasoning, content, and tool calls with the role first", func(t *testing.T) {
		calls := []openaiwire.ToolCall{{ID: "call_1"}}
		got := deltas(reply{reasoning: "r1 r2", content: "c", toolCalls: calls}, 1)
		want := []openaiwire.Delta{
			{Role: "assistant", Content: new(""), Reasoning: "r1 "},
			{Content: new(""), Reasoning: "r2"},
			{Content: new("c")},
			{Content: new(""), ToolCalls: calls},
		}
		assert.Equal(t, want, got)
	})
	t.Run("sends a role chunk for an empty reply", func(t *testing.T) {
		assert.Equal(t, []openaiwire.Delta{{Role: "assistant", Content: new("")}}, deltas(reply{}, 1))
	})
}

func TestStreamChunks(t *testing.T) {
	t.Run("ends with finish and usage chunks", func(t *testing.T) {
		req := openaiwire.ChatRequest{Model: "m", StreamOptions: &openaiwire.StreamOptions{IncludeUsage: true}, Messages: []openaiwire.Message{msg("user", "hi")}}
		chunks := streamChunks(req, reply{content: "a b"}, 1)
		require.Len(t, chunks, 4)
		assert.Equal(t, "stop", *chunks[2].Choices[0].FinishReason)
		assert.Equal(t, openaiwire.Delta{}, chunks[2].Choices[0].Delta)
		assert.Empty(t, chunks[3].Choices)
		assert.Equal(t, &openaiwire.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3}, chunks[3].Usage)
		for _, c := range chunks {
			assert.Equal(t, chunks[0].ID, c.ID)
			assert.Equal(t, "m", c.Model)
		}
	})
	t.Run("omits usage unless requested", func(t *testing.T) {
		chunks := streamChunks(openaiwire.ChatRequest{StreamOptions: &openaiwire.StreamOptions{}}, reply{content: "a"}, 1)
		require.Len(t, chunks, 2)
		assert.Nil(t, chunks[1].Usage)
	})
}

func TestCompletionFor(t *testing.T) {
	calls := []openaiwire.ToolCall{{ID: "call_1", Type: "function"}}
	c := completionFor(openaiwire.ChatRequest{Model: "m"}, reply{reasoning: "r", toolCalls: calls})
	assert.Equal(t, "chat.completion", c.Object)
	assert.Equal(t, systemFingerprint, c.SystemFingerprint)
	require.Len(t, c.Choices, 1)
	assert.Equal(t, openaiwire.ResponseMessage{Role: "assistant", Reasoning: "r", ToolCalls: calls}, c.Choices[0].Message)
	assert.Equal(t, "tool_calls", *c.Choices[0].FinishReason)
}
