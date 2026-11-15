package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestEstimateTokens(t *testing.T) {
	assert.Equal(t, 1100, EstimateTokens(1000, 300))
	assert.Equal(t, 2, EstimateTokens(0, 4))
	assert.Equal(t, 0, EstimateTokens(0, 0))
}

// toolTurns is a task followed by n run calls, each answered with body.
func toolTurns(n int, body string) []llm.Message {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "task"}}
	for i := range n {
		id := "c" + string(rune('a'+i))
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "run", Arguments: `{"command":"c` + string(rune('a'+i)) + `"}`}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, ToolName: "run", Content: body},
		)
	}
	return msgs
}

func TestCompact(t *testing.T) {
	t.Run("returns the messages unchanged under 70 percent", func(t *testing.T) {
		msgs := toolTurns(2, "ok")
		out, ev, ok := Compact(msgs, "sys", 100, 1000, nil)
		assert.True(t, ok)
		assert.Equal(t, msgs, out)
		assert.Equal(t, CompactionEvent{BeforeTokens: 100, AfterTokens: 100}, ev)
	})
	t.Run("elides the oldest tool results first, keeping the last six messages", func(t *testing.T) {
		body := strings.Repeat("z", 3000)
		msgs := toolTurns(10, body)
		out, ev, ok := Compact(msgs, "sys", 4200, 4000, nil)
		require.True(t, ok)
		assert.Positive(t, ev.Elided)
		assert.Equal(t, 0, ev.DroppedTurns)
		assert.Equal(t, `[elided by ws: run {"command":"ca"}, 1 lines; run it again if you need it]`, out[2].Content)
		assert.Equal(t, msgs[len(msgs)-6:], out[len(out)-6:])
		assert.Less(t, ev.AfterTokens, 2000)
	})
	t.Run("drops whole turns and inserts a progress note when eliding is not enough", func(t *testing.T) {
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "task"}}
		for i := range 6 {
			id := "c" + string(rune('a'+i))
			msgs = append(msgs,
				llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 3000), ToolCalls: []llm.ToolCall{{ID: id, Name: "run", Arguments: "{}"}}},
				llm.Message{Role: llm.RoleTool, ToolCallID: id, ToolName: "run", Content: "ok"},
			)
		}
		exit := 0
		done := []ToolRecord{{Name: "write_file", Summary: `{"path":"a.go"}`}, {Name: "run", Summary: "go test", ExitCode: &exit}}
		out, ev, ok := Compact(msgs, "sys", 6000, 1100, done)
		require.True(t, ok)
		assert.Positive(t, ev.DroppedTurns)
		assert.Equal(t, llm.RoleUser, out[1].Role)
		assert.True(t, isProgressNote(&out[1]))
		assert.Contains(t, out[1].Content, "- write_file {\"path\":\"a.go\"}")
		assert.Contains(t, out[1].Content, "- run go test (exit 0)")
		assert.Equal(t, msgs[len(msgs)-2:], out[len(out)-2:])
	})
	t.Run("drops turns down to the last turn when that is what fits", func(t *testing.T) {
		msgs := []llm.Message{{Role: llm.RoleUser, Content: "task"}}
		for _, id := range []string{"a", "b"} {
			msgs = append(msgs,
				llm.Message{Role: llm.RoleAssistant, Content: strings.Repeat("a", 1200), ToolCalls: []llm.ToolCall{{ID: id, Name: "run", Arguments: "{}"}}},
				llm.Message{Role: llm.RoleTool, ToolCallID: id, ToolName: "run", Content: strings.Repeat("t", 1200)},
			)
		}
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "run", Arguments: "{}"}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: "c", ToolName: "run", Content: "ok"},
		)
		out, ev, ok := Compact(msgs, "sys", 1604, 1000, nil)
		require.True(t, ok)
		assert.LessOrEqual(t, ev.AfterTokens, 1000)
		assert.Equal(t, 2, ev.DroppedTurns)
		assert.Equal(t, msgs[len(msgs)-2:], out[len(out)-2:])
	})
	t.Run("reports ok=false when the task and last turn alone don't fit", func(t *testing.T) {
		msgs := []llm.Message{{Role: llm.RoleUser, Content: strings.Repeat("t", 10000)}}
		_, _, ok := Compact(msgs, "sys", 3334, 1000, nil)
		assert.False(t, ok)
	})
}

func TestLimit(t *testing.T) {
	t.Run("is the window less the output", func(t *testing.T) {
		r := &run{target: Target{ContextTokens: 65536, MaxOutput: 4096}}
		assert.Equal(t, 61440, r.limit())
	})
	t.Run("drops to the last truncated prompt once lowered", func(t *testing.T) {
		r := &run{target: Target{ContextTokens: 65536, MaxOutput: 4096}, truncatedTo: 900}
		assert.Equal(t, 900, r.limit())
	})
}
