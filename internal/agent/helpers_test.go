package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

// step is one scripted answer from the model: a reply, or an error.
type step struct {
	resp llm.Response
	err  error
}

func reply(calls ...llm.ToolCall) step {
	return step{resp: llm.Response{Message: llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}, StopReason: llm.StopToolCalls}}
}

func say(content string) step {
	return step{resp: llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Content: content}, StopReason: llm.StopEnd}}
}

func fail(err error) step {
	return step{err: err}
}

func retryable() error {
	return &llm.RetryableError{Cause: errors.New("503 service unavailable")}
}

func call(id, name, arguments string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Arguments: arguments}
}

// scripted answers each request with the next step. It fails the test when
// the loop asks for more than the script holds.
func scripted(t *testing.T, steps ...step) *chatterMock {
	t.Helper()
	next := 0
	return &chatterMock{ChatFunc: func(context.Context, llm.Request) (llm.Response, error) {
		if next >= len(steps) {
			t.Fatalf("request %d has no scripted answer", next+1)
		}
		s := steps[next]
		next++
		return s.resp, s.err
	}}
}

func testTarget(chat chatter) Target {
	return Target{
		Chat:          chat,
		API:           "openai",
		Endpoint:      "gpu",
		Model:         "qwen/qwen3.5-9b",
		ContextTokens: 65536,
		MaxOutput:     4096,
		Temperature:   0.2,
		Reasoning:     "none",
	}
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": properties, "required": required}
}

func stringField() map[string]any {
	return map[string]any{"type": "string"}
}

// fakeTools are write_file, run and record_result, with no effects.
func fakeTools() []tools.Tool {
	return []tools.Tool{
		{
			Name:       "write_file",
			Parameters: objectSchema(map[string]any{"path": stringField(), "content": stringField()}, "path", "content"),
			Call: func(context.Context, string) (tools.Result, error) {
				return tools.Result{Text: "wrote"}, nil
			},
		},
		{
			Name:       "run",
			Parameters: objectSchema(map[string]any{"command": stringField()}, "command"),
			Call: func(context.Context, string) (tools.Result, error) {
				return tools.Result{Text: "exit 0"}, nil
			},
		},
		{
			Name:       "record_result",
			Parameters: objectSchema(map[string]any{"verdict": stringField(), "report": stringField()}, "verdict", "report"),
			Call: func(context.Context, string) (tools.Result, error) {
				return tools.Result{Text: "recorded PASS", Done: true, Verdict: "PASS"}, nil
			},
		},
	}
}

func testConfig(chat chatter) Config {
	return Config{
		Target: testTarget(chat),
		Tools:  StaticToolset(fakeTools()),
		System: "system",
		Task:   "fix it",
		Budget: Budget{MaxSteps: 80},
	}
}

// advancingClock keeps its own time: sleeps move it forward.
func advancingClock() *clockMock {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	return &clockMock{
		NowFunc: func() time.Time { return now },
		SleepFunc: func(_ context.Context, d time.Duration) error {
			now = now.Add(d)
			return nil
		},
	}
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// runWithTranscript runs the loop with a transcript in a temp directory and
// returns the outcome and the transcript's events.
//
//nolint:gocritic // a fixture copied once per test
func runWithTranscript(t *testing.T, cfg Config, clk clock, history []llm.Message) (Outcome, []Event) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	tr, err := OpenTranscript(path, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })
	outcome := New(clk, tr, cfg, discardLog()).Run(t.Context(), history, 0, nil)
	events, _, err := Tail(path, 10000)
	require.NoError(t, err)
	return outcome, events
}

func kindsOf(events []Event) []string {
	kinds := make([]string, 0, len(events))
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	return kinds
}

func eventsOf(events []Event, kind string) []Event {
	var out []Event
	for _, ev := range events {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// payloadOf returns ev's payload as T, which must be the kind's pointer type.
func payloadOf[T any](t *testing.T, ev Event) T {
	t.Helper()
	payload, ok := ev.Payload.(T)
	require.True(t, ok, "payload of %s event has type %T", ev.Kind, ev.Payload)
	return payload
}
