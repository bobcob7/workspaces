package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestRun_dispatch(t *testing.T) {
	t.Parallel()
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
}

func TestFrameResult(t *testing.T) {
	t.Run("frames a built-in result by tool name", func(t *testing.T) {
		assert.Equal(t, `<tool_result tool="run">exit 0</tool_result>`, frameResult("run", "", "exit 0"))
	})
	t.Run("frames an MCP result with its server", func(t *testing.T) {
		assert.Equal(t, `<tool_result tool="mcp__logs__q" server="logs">rows</tool_result>`, frameResult("mcp__logs__q", "logs", "rows"))
	})
}
