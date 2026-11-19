package agent

import (
	json "encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

func TestTranscript_Write(t *testing.T) {
	t.Parallel()
	writeChangedLine := func(t *testing.T, payload EndpointChangedEvent) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		require.NoError(t, tr.Write(Event{Time: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), Step: 1, Kind: kindEndpointChanged, Payload: payload}))
		require.NoError(t, tr.Close())
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		return strings.TrimSpace(string(raw))
	}
	t.Run("redacts a registered secret in a tool result and in reasoning", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, tools.NewRedactor("sk-live-secret-value"))
		require.NoError(t, err)
		now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
		require.NoError(t, tr.Write(Event{Time: now, Step: 1, Kind: kindToolResult, Payload: ToolResultEvent{
			ID: "1", Name: "run", OK: true, Text: "token sk-live-secret-value printed",
		}}))
		require.NoError(t, tr.Write(Event{Time: now, Step: 1, Kind: kindResponse, Payload: ResponseEvent{
			Reasoning: "use sk-live-secret-value", ToolCalls: []toolCallView{},
		}}))
		require.NoError(t, tr.Close())
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "sk-live-secret-value")
		assert.Equal(t, 2, strings.Count(string(raw), "[redacted]"))
	})
	t.Run("writes one object per line with time, step and kind first", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		require.NoError(t, tr.Write(Event{Time: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), Step: 2, Kind: kindNudge, Payload: NudgeEvent{Reason: "repeat"}}))
		require.NoError(t, tr.Close())
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.JSONEq(t, `{"time":"2026-11-14T12:00:00Z","step":2,"kind":"nudge","reason":"repeat"}`, strings.TrimSpace(string(raw)))
	})
	t.Run("writes version and capabilities as null for an endpoint_changed without a probe", func(t *testing.T) {
		line := writeChangedLine(t, EndpointChangedEvent{Reason: "retry", Changed: []string{}})
		assert.Contains(t, line, `"version":null`)
		assert.Contains(t, line, `"capabilities":null`)
	})
	t.Run("writes version and capabilities from the probe for an endpoint_changed", func(t *testing.T) {
		version := "0.9.0"
		capabilities := []string{"completion", "tools"}
		line := writeChangedLine(t, EndpointChangedEvent{Reason: "retry", Changed: []string{}, Version: &version, Capabilities: &capabilities})
		assert.Contains(t, line, `"version":"0.9.0"`)
		assert.Contains(t, line, `"capabilities":["completion","tools"]`)
	})
	t.Run("creates the file with mode 0o600", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		require.NoError(t, tr.Close())
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
}

func TestReadHistory(t *testing.T) {
	t.Parallel()
	t.Run("equals the messages of the next request", func(t *testing.T) {
		chat := scripted(t,
			reply(call("1", "write_file", `{"path":"a.go","content":"x"}`)),
			reply(call("2", "run", `{"command":"go test"}`)),
			fail(&llm.NoToolsError{Model: "qwen/qwen3.5-9b"}),
		)
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tr.Close() })
		New(advancingClock(), tr, testConfig(chat), discardLog()).Run(t.Context(), nil, 0, nil)
		history, step, err := ReadHistory(path)
		require.NoError(t, err)
		assert.Equal(t, 3, step)
		require.Len(t, chat.ChatCalls(), 3)
		assert.Equal(t, chat.ChatCalls()[2].Req.Messages, history)
	})
}

func TestTail(t *testing.T) {
	t.Parallel()
	t.Run("returns the last events and whether more came before", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "transcript.jsonl")
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		for i := range 3 {
			require.NoError(t, tr.Write(Event{Step: i, Kind: kindNudge, Payload: NudgeEvent{Reason: nudgeRepeat}}))
		}
		require.NoError(t, tr.Close())
		events, more, err := Tail(path, 2)
		require.NoError(t, err)
		assert.True(t, more)
		require.Len(t, events, 2)
		assert.Equal(t, 1, events[0].Step)
		assert.Equal(t, nudgeRepeat, payloadOf[*NudgeEvent](t, events[1]).Reason)
	})
}

func TestEvent_MarshalJSON(t *testing.T) {
	t.Parallel()
	ev := Event{Step: 2, Kind: kindNudge, Payload: NudgeEvent{Reason: nudgeRepeat}}
	raw, err := json.Marshal(ev)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, kindNudge, got["kind"])
	assert.InDelta(t, 2, got["step"], 0)
	assert.Equal(t, nudgeRepeat, got["reason"])
}
