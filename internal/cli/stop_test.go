package cli

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newStopApp is a mocked App where workspace a has the Claude Code session
// s-a, which stopErr answers to claude stop.
func newStopApp(t *testing.T, stopErr error) (*mockedApp, *claudeClientMock) {
	t.Helper()
	e := newMockedApp()
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		if arg == "a" {
			return workspace.Workspace{ID: "a", Path: "/cfg/workspaces/a"}, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		return lifetime.Listing{Workspaces: []lifetime.Entry{{
			Workspace: workspace.Workspace{ID: "a"},
			Session:   &lifetime.Session{Kind: lifetime.KindAgent, ID: "s-a", Runner: lifetime.RunnerClaude},
		}}}, nil
	}
	mock := &claudeClientMock{StopFunc: func(context.Context, string) error { return stopErr }}
	e.app.claude = mock
	return e, mock
}

func TestApp_runStop(t *testing.T) {
	t.Run("stops a local agent", func(t *testing.T) {
		e, mock := newStopApp(t, nil)
		require.NoError(t, e.app.runStop(t.Context(), "", []string{"a"}))
		assert.Equal(t, "a  stopped\n", e.stdout.String())
		calls := mock.StopCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, "s-a", calls[0].ID)
	})
	t.Run("a local workspace without a session answers stopped false", func(t *testing.T) {
		e, mock := newStopApp(t, nil)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		e.app.global.json = true
		require.NoError(t, e.app.runStop(t.Context(), "", []string{"a"}))
		assert.JSONEq(t, `{"stopped":[{"workspace":"a","session":null,"stopped":false,"environment":"local"}]}`, e.stdout.String())
		assert.Empty(t, mock.StopCalls())
	})
	t.Run("a session that has ended answers stopped false", func(t *testing.T) {
		e, _ := newStopApp(t, claude.ErrNoSession)
		e.app.global.json = true
		require.NoError(t, e.app.runStop(t.Context(), "", []string{"a"}))
		assert.JSONEq(t, `{"stopped":[{"workspace":"a","session":"s-a","stopped":false,"environment":"local"}]}`, e.stdout.String())
	})
	t.Run("a failed claude stop is an error", func(t *testing.T) {
		boom := errors.New("boom")
		e, _ := newStopApp(t, boom)
		assert.ErrorIs(t, e.app.runStop(t.Context(), "", []string{"a"}), boom)
	})
	t.Run("--env stops through agent.stop", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, `{"ok":true,"result":{"workspace":"a","session":"s-a","stopped":true},"error":null}`)
		e.app.global.json = true
		require.NoError(t, e.app.runStop(t.Context(), "linux-server", []string{"a"}))
		assert.JSONEq(t, `{"stopped":[{"workspace":"a","session":"s-a","stopped":true,"environment":"linux-server"}]}`, e.stdout.String())
		require.Len(t, caller.CallEnvironmentCalls(), 1)
		assert.Equal(t, "agent.stop", caller.CallEnvironmentCalls()[0].Req.Op)
	})
	t.Run("a remote id without --env names its environment", func(t *testing.T) {
		e, _, rec := newEnvOpApp(t, `{"ok":true,"result":{},"error":null}`)
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		err := e.app.runStop(t.Context(), "", []string{"api-bump"})
		requireCode(t, codeWorkspaceNotFound, err)
		assert.Contains(t, err.Error(), "pass --env linux-server")
	})
	t.Run("the environment must be enrolled", func(t *testing.T) {
		e := newMockedApp()
		e.app.paths = config.NewPaths(t.TempDir())
		requireCode(t, codeEnvironmentUnknown, e.app.runStop(t.Context(), "nowhere", []string{"a"}))
	})
}

func TestApp_newStopCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newStopCmd()
	t.Run("needs a workspace", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
		require.NoError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
	t.Run("has --env", func(t *testing.T) {
		assert.NotNil(t, cmd.Flags().Lookup("env"))
	})
}
