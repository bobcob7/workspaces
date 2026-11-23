package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentWS is the workspace whose Claude Code agent the session tests use.
const agentWS = "fix-login-d8efb24f"

// agentStarted is when the session's process started, in the fixtures.
var agentStarted = time.Date(2026, 11, 22, 9, 30, 0, 0, time.UTC)

// claudeAgent is the Claude Code session of agentWS, as claude agents lists it.
func claudeAgent() claude.Session {
	return claude.Session{
		ID:        "ab12cd34",
		SessionID: "5f1e2d3c-0000-4000-8000-000000000001",
		Name:      "Fix login",
		Cwd:       "/cfg/workspaces/" + agentWS,
		State:     claude.StateWorking,
		Status:    "busy",
		PID:       4321,
		StartedAt: agentStarted,
	}
}

// agentEntries is the workspace agentWS with session, as lifetime lists it.
func agentEntries(session *lifetime.Session) []lifetime.Entry {
	return []lifetime.Entry{{Workspace: listedWorkspace(agentWS), Session: session}}
}

// claudeSession is the lifetime session of a Claude Code agent with id ab12cd34.
func claudeSession() *lifetime.Session {
	return &lifetime.Session{Kind: lifetime.KindAgent, ID: "ab12cd34", Runner: lifetime.RunnerClaude, State: lifetime.StateWorking}
}

// agentTestOps is the agent's operations over a mocked App whose lifetime
// lists entries and whose claude is client.
func agentTestOps(entries []lifetime.Entry, client *claudeClientMock) *agentOperations {
	e := newMockedApp()
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		return lifetime.Listing{Workspaces: entries}, nil
	}
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		if arg == agentWS {
			return listedWorkspace(agentWS), nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.app.claude = client
	return opsFor(e.app)
}

// listingAgents is a claude mock whose agents list is sessions.
func listingAgents(sessions ...claude.Session) *claudeClientMock {
	return &claudeClientMock{AgentsFunc: func(context.Context, claude.ListOptions) ([]claude.Session, error) {
		return sessions, nil
	}}
}

func TestAgentOperations_agentStatus(t *testing.T) {
	t.Run("returns the session and its Claude Code agent", func(t *testing.T) {
		ops := agentTestOps(agentEntries(claudeSession()), listingAgents(claudeAgent()))
		got, err := ops.Run(t.Context(), envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: agentWS})
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","agent":{"id":"ab12cd34","session_id":"5f1e2d3c-0000-4000-8000-000000000001","name":"Fix login","state":"working","status":"busy","pid":4321,"started_at":"2026-11-22T09:30:00Z"}}`, string(got))
	})
	t.Run("an agent Claude Code doesn't list is null", func(t *testing.T) {
		ops := agentTestOps(agentEntries(claudeSession()), listingAgents())
		got, err := ops.Run(t.Context(), envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: agentWS})
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","agent":null}`, string(got))
	})
	t.Run("a shell workspace has no Claude Code agent", func(t *testing.T) {
		shell := &lifetime.Session{Kind: lifetime.KindShell}
		ops := agentTestOps(agentEntries(shell), listingAgents(claudeAgent()))
		_, err := ops.Run(t.Context(), envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: agentWS})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
		assert.Equal(t, agentWS+" has no Claude Code agent", re.Message)
	})
	t.Run("a ws runner workspace has no Claude Code agent", func(t *testing.T) {
		runner := &lifetime.Session{Kind: lifetime.KindAgent, ID: "rn-1", Runner: lifetime.RunnerWS}
		_, err := agentTestOps(agentEntries(runner), listingAgents()).Run(t.Context(), envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: agentWS})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
	})
	t.Run("an unknown workspace is workspace_not_found", func(t *testing.T) {
		_, err := agentTestOps(nil, listingAgents()).Run(t.Context(), envproto.OpAgentStatus, &envproto.WorkspaceArgs{Workspace: "gone"})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "workspace_not_found", re.Code)
	})
}

func TestAgentOperations_agentLogs(t *testing.T) {
	logsOf := func(text string) *claudeClientMock {
		client := listingAgents(claudeAgent())
		client.LogsFunc = func(context.Context, string) (string, error) { return text, nil }
		return client
	}
	numbered := func(n int) string {
		var b strings.Builder
		for i := range n {
			fmt.Fprintf(&b, "line %d\n", i)
		}
		return b.String()
	}
	lineCount := func(t *testing.T, got json.RawMessage) int {
		t.Helper()
		var result envproto.AgentLogsResult
		require.NoError(t, json.Unmarshal(got, &result))
		return len(result.Lines)
	}
	t.Run("tail 0 keeps the default last 200 lines", func(t *testing.T) {
		ops := agentTestOps(agentEntries(claudeSession()), logsOf(numbered(3000)))
		got, err := ops.Run(t.Context(), envproto.OpAgentLogs, &envproto.LogsArgs{Workspace: agentWS, Tail: 0})
		require.NoError(t, err)
		assert.Equal(t, 200, lineCount(t, got))
		assert.Contains(t, string(got), "line 2999")
	})
	t.Run("a tail keeps that many lines", func(t *testing.T) {
		ops := agentTestOps(agentEntries(claudeSession()), logsOf(numbered(3000)))
		got, err := ops.Run(t.Context(), envproto.OpAgentLogs, &envproto.LogsArgs{Workspace: agentWS, Tail: 5})
		require.NoError(t, err)
		assert.Equal(t, 5, lineCount(t, got))
	})
	t.Run("a log over MaxLogBytes is cut from the front and marked truncated", func(t *testing.T) {
		long := strings.Repeat("x", 1500)
		text := strings.Repeat(long+"\n", 200)
		ops := agentTestOps(agentEntries(claudeSession()), logsOf(text))
		got, err := ops.Run(t.Context(), envproto.OpAgentLogs, &envproto.LogsArgs{Workspace: agentWS, Tail: 0})
		require.NoError(t, err)
		assert.LessOrEqual(t, len(got), envproto.MaxLogBytes)
		assert.Contains(t, string(got), `"truncated":true`)
	})
	t.Run("a short log isn't truncated", func(t *testing.T) {
		ops := agentTestOps(agentEntries(claudeSession()), logsOf("one\ntwo\n"))
		got, err := ops.Run(t.Context(), envproto.OpAgentLogs, &envproto.LogsArgs{Workspace: agentWS})
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","lines":["one","two"],"truncated":false}`, string(got))
	})
}

func TestAgentOperations_agentSend(t *testing.T) {
	t.Run("the message reaches the session from the agents list", func(t *testing.T) {
		var sent []string
		var sentTo claude.Session
		client := listingAgents(claudeAgent())
		client.SendFunc = func(_ context.Context, s *claude.Session, message string) error {
			sentTo = *s
			sent = append(sent, message)
			return nil
		}
		got, err := agentTestOps(agentEntries(claudeSession()), client).Run(t.Context(), envproto.OpAgentSend, &envproto.SendArgs{Workspace: agentWS, Message: "continue"})
		require.NoError(t, err)
		assert.Equal(t, []string{"continue"}, sent)
		assert.Equal(t, "ab12cd34", sentTo.ID)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","sent":true}`, string(got))
	})
	t.Run("a message that isn't recorded is claude_failed", func(t *testing.T) {
		client := listingAgents(claudeAgent())
		client.SendFunc = func(context.Context, *claude.Session, string) error {
			return fmt.Errorf("%w: session ab12cd34", claude.ErrNotDelivered)
		}
		_, err := agentTestOps(agentEntries(claudeSession()), client).Run(t.Context(), envproto.OpAgentSend, &envproto.SendArgs{Workspace: agentWS, Message: "hi"})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "claude_failed", re.Code)
	})
}

func TestAgentOperations_agentStop(t *testing.T) {
	t.Run("stops the session", func(t *testing.T) {
		var stopped string
		client := listingAgents(claudeAgent())
		client.StopFunc = func(_ context.Context, id string) error {
			stopped = id
			return nil
		}
		got, err := agentTestOps(agentEntries(claudeSession()), client).Run(t.Context(), envproto.OpAgentStop, &envproto.WorkspaceArgs{Workspace: agentWS})
		require.NoError(t, err)
		assert.Equal(t, "ab12cd34", stopped)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","stopped":true}`, string(got))
	})
	t.Run("a session that already ended is stopped false", func(t *testing.T) {
		client := listingAgents(claudeAgent())
		client.StopFunc = func(context.Context, string) error { return claude.ErrNoSession }
		got, err := agentTestOps(agentEntries(claudeSession()), client).Run(t.Context(), envproto.OpAgentStop, &envproto.WorkspaceArgs{Workspace: agentWS})
		require.NoError(t, err)
		assert.JSONEq(t, `{"workspace":"fix-login-d8efb24f","session":"ab12cd34","stopped":false}`, string(got))
	})
}

func TestAgentSnapshots_Agents(t *testing.T) {
	t.Run("maps each Claude Code session", func(t *testing.T) {
		second := claudeAgent()
		second.ID = "ef56ab78"
		second.PID = 0
		second.StartedAt = time.Time{}
		second.Status = ""
		app := newMockedApp().app
		app.claude = listingAgents(claudeAgent(), second)
		agents, err := agentSnapshots{app: app}.Agents(t.Context())
		require.NoError(t, err)
		require.Len(t, agents, 2)
		assert.Equal(t, "ab12cd34", agents[0].ID)
		require.NotNil(t, agents[0].PID)
		assert.Equal(t, 4321, *agents[0].PID)
		assert.Equal(t, "ef56ab78", agents[1].ID)
		assert.Nil(t, agents[1].PID, "a session without a process has a null pid")
		assert.Nil(t, agents[1].StartedAt, "a zero start time is null")
	})
	t.Run("no sessions is an empty list", func(t *testing.T) {
		app := newMockedApp().app
		app.claude = listingAgents()
		agents, err := agentSnapshots{app: app}.Agents(t.Context())
		require.NoError(t, err)
		assert.NotNil(t, agents)
		assert.Empty(t, agents)
	})
	t.Run("a claude failure is an error", func(t *testing.T) {
		app := newMockedApp().app
		app.claude = &claudeClientMock{AgentsFunc: func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			return nil, errors.New("claude is gone")
		}}
		_, err := agentSnapshots{app: app}.Agents(t.Context())
		assert.Error(t, err)
	})
}
