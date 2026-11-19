// Package llm is the runner's provider seam (ADR 0062): the neutral
// conversation types the loop uses, and the Provider interface each wire
// protocol implements.
package llm

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// Endpoint is where and how to reach a server; Token is never logged.
type Endpoint struct {
	Name           string
	URL            string
	API            string
	Probe          string
	Token          string
	RequestTimeout time.Duration
	IdleTimeout    time.Duration
}

// Request is one chat completion request.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []Tool
	MaxOutput   int
	Temperature float64
	// Reasoning is "" (server default), none, low, medium or high (ADR 0064).
	Reasoning string
	// TraceParent is a W3C traceparent, or "" when telemetry is off.
	TraceParent string
}

// Role is who wrote a message.
type Role string

// Message roles.
const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn of the conversation.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant
	ToolCallID string     // tool
	ToolName   string     // tool; for protocols keyed by name
	// Reasoning is shown in transcripts; providers that don't need it
	// don't send it back.
	Reasoning string
	// ReasoningOpaque is returned verbatim by the provider that produced it.
	ReasoningOpaque *Opaque
}

// Opaque is provider-specific reasoning data, tagged with the provider's name.
type Opaque struct {
	Provider string
	Data     []byte
}

// ToolCall is a model's request to run a tool. Arguments is JSON text as sent.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// Tool describes a tool the model may call.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// StopReason says why the model stopped.
type StopReason string

// Stop reasons.
const (
	StopEnd       StopReason = "end"
	StopToolCalls StopReason = "tool_calls"
	StopMaxTokens StopReason = "max_tokens"
)

// Usage is the token count of one response. Reported is false when the
// server didn't say.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	Reported         bool
}

// Response is the whole answer to one Request.
type Response struct {
	Message    Message
	StopReason StopReason
	Usage      Usage
	Duration   time.Duration
	// SpanID is the request's chat span, set when telemetry runs; "" otherwise.
	SpanID string
}

// ModelInfo is what a probe learned about a model.
type ModelInfo struct {
	Version      string   // the server's, when probed
	Capabilities []string // e.g. completion, tools, thinking, vision
	// ContextLength is 0 when unknown.
	ContextLength int
}

// TraceParent builds a W3C traceparent from traceID and a random span id.
func TraceParent(traceID string) string {
	return "00-" + traceID + "-" + randomHex(8) + "-01"
}

// NewTraceID returns 32 random lowercase hex digits.
func NewTraceID() string {
	return randomHex(16)
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
