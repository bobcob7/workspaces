package agent

import (
	"encoding/json/v2"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

// Event kinds (reference.md, "transcript.jsonl").
const (
	kindStart            = "start"
	kindRequest          = "request"
	kindResponse         = "response"
	kindToolResult       = "tool_result"
	kindInvalidCall      = "invalid_call"
	kindNudge            = "nudge"
	kindCompaction       = "compaction"
	kindTruncated        = "truncated"
	kindRetry            = "retry"
	kindEndpointChanged  = "endpoint_changed"
	kindEndpointRejected = "endpoint_rejected"
	kindConfigWarning    = "config_warning"
	kindExit             = "exit"
)

// Transcript appends redacted events to a run's transcript.jsonl.
type Transcript struct {
	file     *os.File
	redactor *tools.Redactor
}

// Event is one transcript line. Payload is one of the kind's structs; Tail
// and ReadHistory return pointers to them.
type Event struct {
	Time    time.Time
	Step    int
	Kind    string
	Payload any
}

// OpenTranscript opens path for appending, mode 0o600.
func OpenTranscript(path string, r *tools.Redactor) (*Transcript, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening transcript: %w", err)
	}
	if r == nil {
		r = tools.NewRedactor()
	}
	return &Transcript{file: f, redactor: r}, nil
}

// Write appends ev, with every string field redacted.
func (t *Transcript) Write(ev Event) error {
	line, err := t.encode(ev)
	if err != nil {
		return err
	}
	if _, err := t.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing transcript: %w", err)
	}
	return nil
}

// Close closes the file.
func (t *Transcript) Close() error {
	return t.file.Close()
}

type eventHead struct {
	Time time.Time `json:"time"`
	Step int       `json:"step"`
	Kind string    `json:"kind"`
}

func (t *Transcript) encode(ev Event) ([]byte, error) {
	head, err := json.Marshal(eventHead{Time: ev.Time, Step: ev.Step, Kind: ev.Kind})
	if err != nil {
		return nil, fmt.Errorf("encoding event head: %w", err)
	}
	body, err := json.Marshal(ev.Payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", ev.Kind, err)
	}
	var generic any
	if err = json.Unmarshal(body, &generic); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", ev.Kind, err)
	}
	body, err = json.Marshal(t.redact(generic))
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", ev.Kind, err)
	}
	return joinObjects(head, body), nil
}

// MarshalJSON is ev as one transcript object, the head then the payload's
// fields. Events are redacted when they are written, so this doesn't again.
func (ev Event) MarshalJSON() ([]byte, error) {
	head, err := json.Marshal(eventHead{Time: ev.Time, Step: ev.Step, Kind: ev.Kind})
	if err != nil {
		return nil, fmt.Errorf("encoding event head: %w", err)
	}
	body, err := json.Marshal(ev.Payload)
	if err != nil {
		return nil, fmt.Errorf("encoding %s: %w", ev.Kind, err)
	}
	return joinObjects(head, body), nil
}

// redact replaces the strings in a decoded JSON value, recursively.
func (t *Transcript) redact(v any) any {
	switch x := v.(type) {
	case string:
		return t.redactor.Redact(x)
	case []any:
		for i := range x {
			x[i] = t.redact(x[i])
		}
		return x
	case map[string]any:
		for k, val := range x {
			x[k] = t.redact(val)
		}
		return x
	}
	return v
}

// joinObjects merges two JSON objects into one.
func joinObjects(head, body []byte) []byte {
	if len(body) <= 2 {
		return head
	}
	merged := append([]byte{}, head[:len(head)-1]...)
	merged = append(merged, ',')
	return append(merged, body[1:]...)
}

// ReadHistory rebuilds the conversation from a transcript, for --resume, and
// returns the last step. Compaction is not replayed. The transcript never
// stores ReasoningOpaque, so a resumed run sends its history without it.
func ReadHistory(path string) ([]llm.Message, int, error) {
	events, err := readEvents(path)
	if err != nil {
		return nil, 0, err
	}
	var history []llm.Message
	step := 0
	for _, ev := range events {
		step = ev.Step
		history = replay(history, ev)
	}
	return history, step, nil
}

func replay(history []llm.Message, ev Event) []llm.Message {
	switch p := ev.Payload.(type) {
	case *StartEvent:
		if len(history) == 0 && len(p.Messages) > 1 {
			history = append(history, llm.Message{Role: llm.RoleUser, Content: p.Messages[1].Content})
		}
	case *ResponseEvent:
		history = append(history, responseMessage(p))
	case *ToolResultEvent:
		history = append(history, llm.Message{Role: llm.RoleTool, Content: p.Text, ToolCallID: p.ID, ToolName: p.Name})
	case *InvalidCallEvent:
		history = append(history, llm.Message{Role: llm.RoleTool, Content: p.Error, ToolCallID: p.ID, ToolName: p.Name})
	case *NudgeEvent:
		history = append(history, llm.Message{Role: llm.RoleUser, Content: nudgeMessage(p.Reason)})
	}
	return history
}

func responseMessage(p *ResponseEvent) llm.Message {
	msg := llm.Message{Role: llm.RoleAssistant, Content: p.Content, Reasoning: p.Reasoning}
	for _, tc := range p.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return msg
}

// Tail returns the last n events, and whether there were more before them.
func Tail(path string, n int) ([]Event, bool, error) {
	events, err := readEvents(path)
	if err != nil {
		return nil, false, err
	}
	if len(events) <= n {
		return events, false, nil
	}
	return events[len(events)-n:], true, nil
}

func readEvents(path string) ([]Event, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading transcript: %w", err)
	}
	var events []Event
	for line := range strings.SplitSeq(string(raw), "\n") {
		if line == "" {
			continue
		}
		ev, err := decodeEvent([]byte(line))
		if err != nil {
			return nil, err
		}
		events = append(events, ev)
	}
	return events, nil
}

func decodeEvent(line []byte) (Event, error) {
	var head eventHead
	if err := json.Unmarshal(line, &head); err != nil {
		return Event{}, fmt.Errorf("decoding transcript line: %w", err)
	}
	payload := newPayload(head.Kind)
	if err := json.Unmarshal(line, payload); err != nil {
		return Event{}, fmt.Errorf("decoding %s: %w", head.Kind, err)
	}
	return Event{Time: head.Time, Step: head.Step, Kind: head.Kind, Payload: payload}, nil
}

func newPayload(kind string) any {
	switch kind {
	case kindStart:
		return new(StartEvent)
	case kindRequest:
		return new(RequestEvent)
	case kindResponse:
		return new(ResponseEvent)
	case kindToolResult:
		return new(ToolResultEvent)
	case kindInvalidCall:
		return new(InvalidCallEvent)
	case kindNudge:
		return new(NudgeEvent)
	case kindCompaction:
		return new(CompactionEvent)
	case kindTruncated:
		return new(TruncatedEvent)
	case kindRetry:
		return new(RetryEvent)
	case kindEndpointChanged:
		return new(EndpointChangedEvent)
	case kindEndpointRejected:
		return new(EndpointRejectedEvent)
	case kindConfigWarning:
		return new(ConfigWarningEvent)
	case kindExit:
		return new(ExitEvent)
	}
	return new(map[string]any)
}

type messageView struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type toolCallView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type tokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// StartEvent is the transcript's start line.
type StartEvent struct {
	Model         string        `json:"model"`
	ModelAlias    string        `json:"model_alias"`
	Endpoint      string        `json:"endpoint"`
	API           string        `json:"api"`
	ContextTokens int           `json:"context_tokens"`
	Reasoning     string        `json:"reasoning"`
	Resume        bool          `json:"resume"`
	TraceID       string        `json:"trace_id"`
	Messages      []messageView `json:"messages"`
	Tools         []string      `json:"tools"`
}

// RequestEvent is one request, with counts.
type RequestEvent struct {
	Messages        int `json:"messages"`
	Tools           int `json:"tools"`
	EstimatedTokens int `json:"estimated_tokens"`
}

// ResponseEvent is one model reply.
type ResponseEvent struct {
	Content    string         `json:"content"`
	Reasoning  string         `json:"reasoning"`
	ToolCalls  []toolCallView `json:"tool_calls"`
	StopReason string         `json:"stop_reason"`
	Usage      *tokenUsage    `json:"usage"`
	DurationMS int64          `json:"duration_ms"`
	SpanID     *string        `json:"span_id"`
}

// ToolResultEvent is one tool's answer, as sent to the model.
type ToolResultEvent struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	OK        bool   `json:"ok"`
	Bytes     int    `json:"bytes"`
	Truncated bool   `json:"truncated"`
	Text      string `json:"text"`
}

// InvalidCallEvent is a tool call the loop refused, with the error sent back.
type InvalidCallEvent struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

// NudgeEvent is a user message the loop added to steer the model.
type NudgeEvent struct {
	Reason string `json:"reason"`
}

// TruncatedEvent is a prompt the server counted much shorter than estimated.
type TruncatedEvent struct {
	EstimatedTokens int `json:"estimated_tokens"`
	PromptTokens    int `json:"prompt_tokens"`
}

// RetryEvent is one failed attempt and its wait.
type RetryEvent struct {
	Attempt  int    `json:"attempt"`
	Error    string `json:"error"`
	WaitMS   int64  `json:"wait_ms"`
	Endpoint string `json:"endpoint"`
}

// EndpointChangedEvent is a switch to another endpoint or model.
type EndpointChangedEvent struct {
	Reason            string    `json:"reason"`
	Attempt           *int      `json:"attempt"`
	FromEndpoint      string    `json:"from_endpoint"`
	ToEndpoint        string    `json:"to_endpoint"`
	FromModel         string    `json:"from_model"`
	ToModel           string    `json:"to_model"`
	FromContextTokens int       `json:"from_context_tokens"`
	ToContextTokens   int       `json:"to_context_tokens"`
	API               string    `json:"api"`
	Changed           []string  `json:"changed"`
	Version           *string   `json:"version"`
	Capabilities      *[]string `json:"capabilities"`
}

// EndpointRejectedEvent is a switch the check refused, or a resume that
// could not reach its endpoint.
type EndpointRejectedEvent struct {
	Reason       string `json:"reason"`
	Attempt      *int   `json:"attempt"`
	Endpoint     string `json:"endpoint"`
	Model        string `json:"model"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	KeptEndpoint string `json:"kept_endpoint"`
}

// ConfigWarningEvent is a configuration the loop could not use.
type ConfigWarningEvent struct {
	Code         string `json:"code"`
	Message      string `json:"message"`
	KeptEndpoint string `json:"kept_endpoint"`
}

// ExitEvent is the run's last line.
type ExitEvent struct {
	Reason  string  `json:"reason"`
	Message string  `json:"message"`
	Verdict *string `json:"verdict"`
}
