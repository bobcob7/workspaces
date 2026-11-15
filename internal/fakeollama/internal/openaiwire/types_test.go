package openaiwire

import (
	"encoding/json/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatRequest_IncludeUsage(t *testing.T) {
	t.Run("false without stream options", func(t *testing.T) {
		assert.False(t, ChatRequest{}.IncludeUsage())
	})
	t.Run("follows stream options", func(t *testing.T) {
		assert.False(t, ChatRequest{StreamOptions: &StreamOptions{}}.IncludeUsage())
		assert.True(t, ChatRequest{StreamOptions: &StreamOptions{IncludeUsage: true}}.IncludeUsage())
	})
	t.Run("decodes from a request body", func(t *testing.T) {
		var req ChatRequest
		require.NoError(t, json.Unmarshal([]byte(`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[]}`), &req))
		assert.True(t, req.IncludeUsage())
	})
}

func TestTextMessage(t *testing.T) {
	t.Run("encodes content as a JSON string", func(t *testing.T) {
		m := TextMessage(RoleUser, `say "hi"`)
		assert.Equal(t, RoleUser, m.Role)
		assert.JSONEq(t, `"say \"hi\""`, string(m.Content))
	})
}

func TestMessage_Text(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"string", `"hello"`, "hello"},
		{"text parts", `[{"type":"text","text":"hel"},{"type":"text","text":"lo"}]`, "hello"},
		{"null", `null`, ""},
		{"absent", ``, ""},
		{"object", `{"x":1}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Message{Content: []byte(tt.content)}.Text())
		})
	}
}

func TestDelta(t *testing.T) {
	t.Run("keeps empty content when set", func(t *testing.T) {
		b, err := json.Marshal(Delta{Content: new(""), Reasoning: "r"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"content":"","reasoning":"r"}`, string(b))
	})
	t.Run("omits unset fields", func(t *testing.T) {
		b, err := json.Marshal(Delta{})
		require.NoError(t, err)
		assert.JSONEq(t, `{}`, string(b))
	})
}

func TestToolCall(t *testing.T) {
	t.Run("continuation deltas carry only index and arguments", func(t *testing.T) {
		b, err := json.Marshal(ToolCall{Index: 1, Function: FunctionCall{Arguments: `"}`}})
		require.NoError(t, err)
		assert.JSONEq(t, `{"index":1,"function":{"arguments":"\"}"}}`, string(b))
	})
}

func TestChunk(t *testing.T) {
	t.Run("sends null finish reasons and omits absent usage", func(t *testing.T) {
		b, err := json.Marshal(Chunk{ID: "c", Object: ObjectChatCompletionChunk, Choices: []ChunkChoice{{}}})
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"c","object":"chat.completion.chunk","created":0,"model":"","choices":[{"index":0,"delta":{},"finish_reason":null}]}`, string(b))
	})
	t.Run("sends an empty choices array with usage", func(t *testing.T) {
		b, err := json.Marshal(Chunk{Choices: []ChunkChoice{}, Usage: &Usage{TotalTokens: 3}})
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"","object":"","created":0,"model":"","choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":3}}`, string(b))
	})
}
