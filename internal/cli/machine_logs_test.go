package cli

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/runner"
)

// logsApp is a machine test App whose listed workspaces carry the sessions
// in sessions, and whose Claude client is claude.
func logsApp(t *testing.T, sessions map[string]*lifetime.Session, claude *claudeClientMock) *mockedApp {
	t.Helper()
	e := newMachineApp(t)
	e.app.global.json = true
	e.app.claude = claude
	tree := machineTree()
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		entries := make([]lifetime.Entry, 0, len(tree))
		for i := range tree {
			entries = append(entries, lifetime.Entry{Workspace: tree[i], Session: sessions[tree[i].ID]})
		}
		return lifetime.Listing{Workspaces: entries}, nil
	}
	return e
}

// writeTranscript writes n nudge events to child's runner transcript.
func writeTranscript(t *testing.T, e *mockedApp, child string, n int) {
	t.Helper()
	files := runner.FilesFor(e.app.paths.State, child)
	require.NoError(t, os.MkdirAll(files.Dir, 0o700))
	tr, err := agent.OpenTranscript(files.Transcript(), nil)
	require.NoError(t, err)
	for i := range n {
		require.NoError(t, tr.Write(agent.Event{Step: i + 1, Kind: "nudge", Payload: agent.NudgeEvent{Reason: "repeat"}}))
	}
	require.NoError(t, tr.Close())
}

// logsOutput is ws machine logs' output, as the tests read it.
type logsOutput struct {
	ID        string           `json:"id"`
	Session   *string          `json:"session"`
	Runner    *string          `json:"runner"`
	Model     *string          `json:"model"`
	Events    []map[string]any `json:"events"`
	Text      *string          `json:"text"`
	Truncated bool             `json:"truncated"`
}

func parseLogs(t *testing.T, out string) logsOutput {
	t.Helper()
	var got logsOutput
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	return got
}

func runnerSession() *lifetime.Session {
	return &lifetime.Session{Kind: lifetime.KindAgent, ID: "rn-3f2a9c1e", State: lifetime.StateIdle, Runner: lifetime.RunnerWS, Model: "qwen/qwen3.5-9b"}
}

func TestApp_runMachineLogs(t *testing.T) {
	t.Run("a ws child prints the last events of its transcript", func(t *testing.T) {
		e := logsApp(t, map[string]*lifetime.Session{"a": runnerSession()}, &claudeClientMock{})
		writeTranscript(t, e, "a", 3)
		require.NoError(t, e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: 2}))
		got := parseLogs(t, e.stdout.String())
		assert.Equal(t, "a", got.ID)
		assert.Equal(t, "rn-3f2a9c1e", *got.Session)
		assert.Equal(t, "ws", *got.Runner)
		assert.Equal(t, "qwen/qwen3.5-9b", *got.Model)
		require.Len(t, got.Events, 2)
		assert.Equal(t, "nudge", got.Events[0]["kind"])
		assert.InDelta(t, 2, got.Events[0]["step"], 0)
		assert.Equal(t, "repeat", got.Events[1]["reason"])
		assert.True(t, got.Truncated)
		assert.Nil(t, got.Text)
	})
	t.Run("a tail of 0 is every event", func(t *testing.T) {
		e := logsApp(t, map[string]*lifetime.Session{"a": runnerSession()}, &claudeClientMock{})
		writeTranscript(t, e, "a", 3)
		require.NoError(t, e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: 0}))
		got := parseLogs(t, e.stdout.String())
		assert.Len(t, got.Events, 3)
		assert.False(t, got.Truncated)
	})
	t.Run("a ws child without a transcript has no events", func(t *testing.T) {
		e := logsApp(t, map[string]*lifetime.Session{"a": runnerSession()}, &claudeClientMock{})
		require.NoError(t, e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: 200}))
		assert.Contains(t, e.stdout.String(), `"events":[]`)
	})
	t.Run("a session id finds the same child", func(t *testing.T) {
		e := logsApp(t, map[string]*lifetime.Session{"a": runnerSession()}, &claudeClientMock{})
		writeTranscript(t, e, "a", 1)
		require.NoError(t, e.app.runMachineLogs(t.Context(), "rn-3f2a9c1e", &machineLogsOptions{tail: 200}))
		assert.Equal(t, "a", parseLogs(t, e.stdout.String()).ID)
	})
	t.Run("a Claude child prints claude logs", func(t *testing.T) {
		claude := &claudeClientMock{LogsFunc: func(_ context.Context, id string) (string, error) {
			assert.Equal(t, "e8cdf5d8", id)
			return "the log\n", nil
		}}
		sess := &lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateIdle, Runner: lifetime.RunnerClaude}
		e := logsApp(t, map[string]*lifetime.Session{"a": sess}, claude)
		require.NoError(t, e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: 200}))
		got := parseLogs(t, e.stdout.String())
		require.NotNil(t, got.Text)
		assert.Equal(t, "the log\n", *got.Text)
		assert.Equal(t, "claude", *got.Runner)
		assert.Empty(t, got.Events)
	})
	t.Run("a Claude failure is claude_failed", func(t *testing.T) {
		claude := &claudeClientMock{LogsFunc: func(context.Context, string) (string, error) { return "", errTest }}
		sess := &lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: lifetime.StateIdle, Runner: lifetime.RunnerClaude}
		e := logsApp(t, map[string]*lifetime.Session{"a": sess}, claude)
		err := e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: 200})
		assertCliCode(t, err, codeClaudeFailed)
	})
	t.Run("a sibling is not_descendant", func(t *testing.T) {
		e := logsApp(t, nil, &claudeClientMock{})
		err := e.app.runMachineLogs(t.Context(), "other", &machineLogsOptions{tail: 200})
		assertCliCode(t, err, codeNotDescendant)
	})
	t.Run("an unknown id is workspace_not_found", func(t *testing.T) {
		e := logsApp(t, nil, &claudeClientMock{})
		err := e.app.runMachineLogs(t.Context(), "nope", &machineLogsOptions{tail: 200})
		assertCliCode(t, err, codeWorkspaceNotFound)
	})
	t.Run("a removed ws child is read from its runner state", func(t *testing.T) {
		e := logsApp(t, nil, &claudeClientMock{})
		files := runner.FilesFor(e.app.paths.State, "gone1")
		require.NoError(t, os.MkdirAll(files.Dir, 0o700))
		require.NoError(t, files.WriteRun(&runner.Run{Session: "rn-3f2a9c1e", Model: "qwen/qwen3.5-9b", Endpoint: "fake"}))
		writeTranscript(t, e, "gone1", 2)
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", "gone1", &machine.Result{
			Verdict:    "FAIL",
			Ancestors:  []string{"plan", "boss"},
			RecordedAt: testNow(),
		}, nil))
		require.NoError(t, e.app.runMachineLogs(t.Context(), "gone1", &machineLogsOptions{tail: 200}))
		got := parseLogs(t, e.stdout.String())
		assert.Equal(t, "gone1", got.ID)
		assert.Equal(t, "rn-3f2a9c1e", *got.Session)
		assert.Equal(t, "qwen/qwen3.5-9b", *got.Model)
		assert.Len(t, got.Events, 2)
	})
	t.Run("a removed child whose state is gone is workspace_not_found", func(t *testing.T) {
		e := logsApp(t, nil, &claudeClientMock{})
		require.NoError(t, machine.NewStore(e.app.paths.State).WriteResult("boss", "gone2", &machine.Result{
			Verdict:    "FAIL",
			Ancestors:  []string{"plan", "boss"},
			RecordedAt: testNow(),
		}, nil))
		err := e.app.runMachineLogs(t.Context(), "gone2", &machineLogsOptions{tail: 200})
		assertCliCode(t, err, codeWorkspaceNotFound)
	})
	t.Run("a negative tail is a usage error", func(t *testing.T) {
		e := logsApp(t, nil, &claudeClientMock{})
		err := e.app.runMachineLogs(t.Context(), "a", &machineLogsOptions{tail: -1})
		assertCliCode(t, err, codeUsage)
	})
}

// assertCliCode asserts err is a cliError with code.
func assertCliCode(t *testing.T, err error, code errorCode) {
	t.Helper()
	var ce *cliError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, code, ce.code)
}
