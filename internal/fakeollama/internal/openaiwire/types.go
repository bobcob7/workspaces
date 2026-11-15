// Package openaiwire defines the JSON wire format of the OpenAI chat
// completions API as Ollama speaks it, and helpers for writing
// it.
//
// Copied from llm-router internal/openaiwire @9789c32 (ADR 0067); maintained here.
package openaiwire

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
)

// BackendHeader names the backend that served a request through the router.
const BackendHeader = "X-Router-Backend"

// Object types.
const (
	ObjectChatCompletion      = "chat.completion"
	ObjectChatCompletionChunk = "chat.completion.chunk"
	ObjectList                = "list"
	ObjectModel               = "model"
)

// Message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Finish reasons.
const (
	FinishReasonStop      = "stop"
	FinishReasonToolCalls = "tool_calls"
)

// ToolTypeFunction is the type of function tools and tool calls.
const ToolTypeFunction = "function"

// ChatRequest is a chat completion request.
type ChatRequest struct {
	Model           string         `json:"model"`
	Messages        []Message      `json:"messages"`
	Stream          bool           `json:"stream,omitzero"`
	StreamOptions   *StreamOptions `json:"stream_options,omitzero"`
	Tools           []Tool         `json:"tools,omitzero"`
	ReasoningEffort string         `json:"reasoning_effort,omitzero"`
}

// IncludeUsage reports whether a streamed response should end with usage.
func (r ChatRequest) IncludeUsage() bool {
	return r.StreamOptions != nil && r.StreamOptions.IncludeUsage
}

// StreamOptions configures a streamed response.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// Message is a conversation message in a request.
type Message struct {
	Role string `json:"role"`
	// Content is a string or an array of content parts.
	Content    jsontext.Value `json:"content,omitzero"`
	ToolCalls  []ToolCall     `json:"tool_calls,omitzero"`
	ToolCallID string         `json:"tool_call_id,omitzero"`
}

// TextMessage returns a message with string content.
func TextMessage(role, content string) Message {
	raw, _ := json.Marshal(content) // Marshaling a string cannot fail.
	return Message{Role: role, Content: raw}
}

// Text returns the message content, joining the text of multi-part content.
func (m Message) Text() string {
	var s string
	if err := json.Unmarshal(m.Content, &s); err == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(m.Content, &parts); err != nil {
		return ""
	}
	texts := make([]string, 0, len(parts))
	for _, p := range parts {
		texts = append(texts, p.Text)
	}
	return strings.Join(texts, "")
}

// Tool is a tool offered to the model.
type Tool struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

// FunctionDefinition describes a function tool.
type FunctionDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description,omitzero"`
	// Parameters is the JSON schema of the function's arguments.
	Parameters map[string]any `json:"parameters,omitzero"`
}

// ChatCompletion is a non-streamed chat completion response.
type ChatCompletion struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	SystemFingerprint string   `json:"system_fingerprint,omitzero"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
}

// Choice is one completion choice.
type Choice struct {
	Index        int             `json:"index"`
	Message      ResponseMessage `json:"message"`
	FinishReason *string         `json:"finish_reason"`
}

// ResponseMessage is the assistant message in a completion.
type ResponseMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Reasoning string     `json:"reasoning,omitzero"`
	ToolCalls []ToolCall `json:"tool_calls,omitzero"`
}

// Chunk is one event of a streamed chat completion.
type Chunk struct {
	ID                string        `json:"id"`
	Object            string        `json:"object"`
	Created           int64         `json:"created"`
	Model             string        `json:"model"`
	SystemFingerprint string        `json:"system_fingerprint,omitzero"`
	Choices           []ChunkChoice `json:"choices"`
	Usage             *Usage        `json:"usage,omitzero"`
}

// ChunkChoice is the part of a choice carried by one chunk.
type ChunkChoice struct {
	Index        int     `json:"index"`
	Delta        Delta   `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// Delta is the change to the assistant message carried by one chunk.
// Content is a pointer so an empty string can be sent, as Ollama does on
// reasoning and tool call chunks.
type Delta struct {
	Role      string     `json:"role,omitzero"`
	Content   *string    `json:"content,omitzero"`
	Reasoning string     `json:"reasoning,omitzero"`
	ToolCalls []ToolCall `json:"tool_calls,omitzero"`
}

// ToolCall is a tool call made by the model. In stream deltas, calls after
// the first chunk of a call carry only Index and argument fragments.
type ToolCall struct {
	ID       string       `json:"id,omitzero"`
	Index    int          `json:"index"`
	Type     string       `json:"type,omitzero"`
	Function FunctionCall `json:"function"`
}

// FunctionCall is the function invocation in a tool call.
type FunctionCall struct {
	Name      string `json:"name,omitzero"`
	Arguments string `json:"arguments"`
}

// Usage counts the tokens of a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelList is the response of GET /v1/models.
type ModelList struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model is one entry of a ModelList.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
