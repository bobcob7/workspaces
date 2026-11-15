package agent

import (
	"fmt"
	"strings"
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
	t.Run("compacts once on a context error and sends again, then ends context_overflow on the second", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "run", `{"command":"step 1"}`)),
			reply(call("2", "run", `{"command":"step 2"}`)),
			reply(call("3", "run", `{"command":"step 3"}`)),
			reply(call("4", "run", `{"command":"step 4"}`)),
			fail(llm.ErrContextExceeded),
			fail(llm.ErrContextExceeded),
		)
		cfg := testConfig(chat)
		cfg.Target.ContextTokens = 1000
		cfg.Target.MaxOutput = 0
		cfg.Tools = StaticToolset(withRun(runTool(strings.Repeat("x", 440))))
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitContextOverflow, out.Exit)
		assert.Len(t, chat.ChatCalls(), 6)
		compactions := eventsOf(events, kindCompaction)
		require.Len(t, compactions, 1)
		assert.Equal(t, 1, payloadOf[*CompactionEvent](t, compactions[0]).Elided)
	})
	t.Run("compacts a context error the estimate missed, though the estimate is far under the window", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "run", `{"command":"step 1"}`)),
			reply(call("2", "run", `{"command":"step 2"}`)),
			reply(call("3", "run", `{"command":"step 3"}`)),
			reply(call("4", "run", `{"command":"step 4"}`)),
			fail(llm.ErrContextExceeded),
			reply(call("5", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		cfg := testConfig(chat)
		cfg.Tools = StaticToolset(withRun(runTool(strings.Repeat("x", 440))))
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		requests := chat.ChatCalls()
		require.Len(t, requests, 6)
		assert.True(t, strings.HasPrefix(requests[5].Req.Messages[2].Content, elidedPrefix))
		compactions := eventsOf(events, kindCompaction)
		require.Len(t, compactions, 1)
		assert.Equal(t, 1, payloadOf[*CompactionEvent](t, compactions[0]).Elided)
	})
	t.Run("counts every attempt as a request, the failed ones too", func(t *testing.T) {
		chat := scripted(t,
			fail(retryable()), fail(retryable()), fail(retryable()),
			reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, 4, out.Usage.Requests)
		require.Len(t, out.Endpoints, 1)
		assert.Equal(t, 4, out.Endpoints[0].Usage.Requests)
	})
	t.Run("keeps the written files and the run exit codes in the progress note", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "write_file", fmt.Sprintf(`{"path":"a.go","content":%q}`, strings.Repeat("x", 2400)))),
			reply(call("2", "run", `{"command":"go test ./..."}`)),
			reply(call("3", "run", `{"command":"go vet ./..."}`)),
			reply(call("4", "record_result", `{"verdict":"PASS","report":"ok"}`)),
		)
		cfg := testConfig(chat)
		cfg.Target.ContextTokens = 1000
		cfg.Target.MaxOutput = 0
		cfg.Tools = StaticToolset(withRun(runTool("exit 3\n--- stdout ---\nFAIL", "exit 0\n--- stdout ---\nok")))
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		requests := chat.ChatCalls()
		require.Len(t, requests, 4)
		assert.Equal(t, progressNoteOpening+"\n- write_file a.go\n- run go test ./... (exit 3)\n- run go vet ./... (exit 0)", requests[3].Req.Messages[1].Content)
		compactions := eventsOf(events, kindCompaction)
		require.Len(t, compactions, 1)
		assert.Equal(t, 1, payloadOf[*CompactionEvent](t, compactions[0]).DroppedTurns)
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
}
