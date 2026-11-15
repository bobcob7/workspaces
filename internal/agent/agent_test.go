package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestAgent_Run(t *testing.T) {
	t.Parallel()
	t.Run("runs write_file, run and record_result to done", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "write_file", `{"path":"a.go","content":"x"}`)),
			reply(call("2", "run", `{"command":"go test"}`)),
			reply(call("3", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, "PASS", out.Verdict)
		assert.Equal(t, 3, out.Steps)
		assert.Equal(t, Usage{Requests: 3}, out.Usage)
		assert.Equal(t, []string{
			"start", "request", "response", "tool_result",
			"request", "response", "tool_result",
			"request", "response", "tool_result", "exit",
		}, kindsOf(events))
	})
	t.Run("ends at max_steps when the step budget is used up", func(t *testing.T) {
		chat := scripted(t, reply(call("1", "run", `{"command":"true"}`)))
		cfg := testConfig(chat)
		cfg.Budget = Budget{MaxSteps: 1}
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitMaxSteps, out.Exit)
		assert.Equal(t, 1, out.Steps)
	})
	t.Run("ends at timeout when the wall clock passes the budget", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), reply(call("1", "run", `{"command":"true"}`)))
		cfg := testConfig(chat)
		cfg.Budget = Budget{Timeout: time.Second}
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitTimeout, out.Exit)
	})
	t.Run("ends at max_tokens when the token budget is used up", func(t *testing.T) {
		chat := scripted(t, step{resp: llm.Response{
			Message:    llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("1", "run", `{"command":"true"}`)}},
			StopReason: llm.StopToolCalls,
			Usage:      llm.Usage{InputTokens: 60, OutputTokens: 50, Reported: true},
		}})
		cfg := testConfig(chat)
		cfg.Budget = Budget{MaxTokens: 100}
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitMaxTokens, out.Exit)
		assert.Equal(t, Usage{Requests: 1, PromptTokens: 60, CompletionTokens: 50}, out.Usage)
	})
	t.Run("retries three times with 2, 8 and 30 seconds, then ends endpoint_down", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), fail(retryable()))
		clk := advancingClock()
		var waits []time.Duration
		clk.SleepFunc = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}
		out, _ := runWithTranscript(t, testConfig(chat), clk, nil)
		assert.Equal(t, ExitEndpointDown, out.Exit)
		assert.Equal(t, []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}, waits)
		assert.Len(t, chat.ChatCalls(), 3)
	})
	t.Run("recovers after two retryable errors", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
	})
	t.Run("ends model_no_tools and names the endpoint", func(t *testing.T) {
		chat := scripted(t, fail(&llm.NoToolsError{Model: "qwen/qwen3.5-9b"}))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitModelNoTools, out.Exit)
		assert.Contains(t, out.Message, "gpu:")
	})
	t.Run("ends model_missing when the endpoint doesn't serve the model", func(t *testing.T) {
		chat := scripted(t, fail(&llm.ModelMissingError{Model: "qwen/qwen3.5-9b"}))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitModelMissing, out.Exit)
	})
	t.Run("forces a compaction and ends context_overflow on a context error", func(t *testing.T) {
		chat := scripted(t, fail(llm.ErrContextExceeded))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitContextOverflow, out.Exit)
		assert.Len(t, chat.ChatCalls(), 1)
	})
	t.Run("ends malformed_tool_calls after three invalid calls in a row", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "nope", `{}`)),
			reply(call("2", "run", `{"command":3}`)),
			reply(call("3", "run", `not json`)),
		)
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitMalformed, out.Exit)
		invalid := eventsOf(events, kindInvalidCall)
		require.Len(t, invalid, 3)
		assert.Contains(t, payloadOf[*InvalidCallEvent](t, invalid[1]).Error, "command must be of type string")
		assert.Contains(t, payloadOf[*InvalidCallEvent](t, invalid[1]).Error, "the arguments must match: ")
	})
	t.Run("recovers after one malformed call", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "write_file", `{"path":3}`)),
			reply(call("2", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
	})
	t.Run("nudges a reply without a tool call, then ends no_result after the third", func(t *testing.T) {
		chat := scripted(t, say("hello"), say("still talking"), say("again"))
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitNoResult, out.Exit)
		nudges := eventsOf(events, kindNudge)
		require.Len(t, nudges, 2)
		assert.Equal(t, nudgeNoToolCall, payloadOf[*NudgeEvent](t, nudges[0]).Reason)
		last := chat.ChatCalls()[2].Req.Messages
		assert.Equal(t, nudgeMessage(nudgeNoToolCall), last[len(last)-1].Content)
	})
	t.Run("nudges a tool call written as text", func(t *testing.T) {
		chat := scripted(t,
			say(`<tool_call>{"name":"run"}</tool_call>`),
			reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		nudges := eventsOf(events, kindNudge)
		require.Len(t, nudges, 1)
		assert.Equal(t, nudgeTextToolCall, payloadOf[*NudgeEvent](t, nudges[0]).Reason)
	})
	t.Run("counts a reply with only reasoning and max_tokens as no tool call", func(t *testing.T) {
		chat := scripted(t,
			step{resp: llm.Response{Message: llm.Message{Role: llm.RoleAssistant, Reasoning: "thinking"}, StopReason: llm.StopMaxTokens}},
			reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
	})
	t.Run("ends looping after five identical calls, nudging from the third", func(t *testing.T) {
		same := call("1", "run", `{"command":"true"}`)
		chat := scripted(t, reply(same), reply(same), reply(same), reply(same), reply(same))
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitLooping, out.Exit)
		assert.Len(t, chat.ChatCalls(), 5)
		nudges := eventsOf(events, kindNudge)
		require.NotEmpty(t, nudges)
		assert.Equal(t, nudgeRepeat, payloadOf[*NudgeEvent](t, nudges[0]).Reason)
	})
	t.Run("ends stopped without asking the model when stop was requested", func(t *testing.T) {
		chat := scripted(t)
		cfg := testConfig(chat)
		cfg.Stop = func() bool { return true }
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitStopped, out.Exit)
		assert.Empty(t, chat.ChatCalls())
	})
	t.Run("ends done without asking the model when the result is written", func(t *testing.T) {
		chat := scripted(t)
		cfg := testConfig(chat)
		cfg.ResultWritten = func() bool { return true }
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Empty(t, out.Verdict)
		assert.Empty(t, chat.ChatCalls())
	})
	t.Run("resumes from history without a new task message", func(t *testing.T) {
		chat := scripted(t, reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)))
		history := []llm.Message{{Role: llm.RoleUser, Content: "fix it"}}
		out, events := runWithTranscript(t, testConfig(chat), advancingClock(), history)
		assert.Equal(t, ExitDone, out.Exit)
		start := payloadOf[*StartEvent](t, eventsOf(events, kindStart)[0])
		assert.True(t, start.Resume)
	})
	t.Run("ends stopped when the context is cancelled during a request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		chat := scripted(t, fail(context.Canceled))
		path := t.TempDir() + "/transcript.jsonl"
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tr.Close() })
		out := New(advancingClock(), tr, testConfig(chat), discardLog()).Run(ctx, nil, 0, nil)
		assert.Equal(t, ExitStopped, out.Exit)
	})
}
