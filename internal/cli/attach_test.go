package cli

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// attachApp is a mocked App where workspace a has the Claude session s-a and
// workspace sh a shell, and a claude mock that records Attach.
func attachApp(t *testing.T) (*mockedApp, *claudeClientMock) {
	t.Helper()
	e, _ := newStopApp(t, nil)
	e.app.stdinTerminal = func() bool { return true }
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		switch arg {
		case "a", "sh", "rn":
			return workspace.Workspace{ID: arg, Path: "/cfg/workspaces/" + arg}, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		return lifetime.Listing{Workspaces: []lifetime.Entry{
			{Workspace: workspace.Workspace{ID: "a"}, Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "s-a", Runner: lifetime.RunnerClaude}},
			{Workspace: workspace.Workspace{ID: "sh"}, Session: &lifetime.Session{Kind: lifetime.KindShell}},
			{Workspace: workspace.Workspace{ID: "rn"}, Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "rn-1", Runner: lifetime.RunnerWS}},
		}}, nil
	}
	mock := &claudeClientMock{AttachFunc: func(context.Context, string) error { return nil }}
	e.app.claude = mock
	return e, mock
}

func TestApp_runAttach(t *testing.T) {
	t.Run("a local Claude workspace execs claude attach", func(t *testing.T) {
		e, mock := attachApp(t)
		require.NoError(t, e.app.runAttach(t.Context(), "", "a"))
		calls := mock.AttachCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, "s-a", calls[0].ID)
	})
	t.Run("a shell workspace is refused", func(t *testing.T) {
		e, mock := attachApp(t)
		err := e.app.runAttach(t.Context(), "", "sh")
		requireUsageError(t, err)
		assert.Contains(t, err.Error(), "only a Claude agent can be attached")
		assert.Empty(t, mock.AttachCalls())
	})
	t.Run("a ws runner workspace is refused", func(t *testing.T) {
		e, mock := attachApp(t)
		requireUsageError(t, e.app.runAttach(t.Context(), "", "rn"))
		assert.Empty(t, mock.AttachCalls())
	})
	t.Run("stdin that isn't a terminal is refused", func(t *testing.T) {
		e, mock := attachApp(t)
		e.app.stdinTerminal = func() bool { return false }
		err := e.app.runAttach(t.Context(), "", "a")
		requireUsageError(t, err)
		assert.Contains(t, err.Error(), "ws attach needs a terminal")
		assert.Empty(t, mock.AttachCalls())
	})
	t.Run("--env opens an attach terminal", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		te := newTermEnv(t)
		te.app.paths = e.app.paths
		te.app.configs = e.app.configs
		te.app.environ = e.app.environ
		te.frames <- termFrame{data: []byte("claude\r\n")}
		te.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "closed"}}
		require.NoError(t, te.app.runAttach(t.Context(), "linux-server", "api-bump"))
		require.Len(t, te.opened, 1)
		assert.Equal(t, envproto.TermAttach, te.opened[0].Kind)
		require.NotNil(t, te.opened[0].Workspace)
		assert.Equal(t, "api-bump", *te.opened[0].Workspace)
		assert.Equal(t, "claude\r\n", te.stdout.String())
		raw, err := json.Marshal(te.opened[0])
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"specs":[]`)
	})
	t.Run("an unknown environment is environment_unknown", func(t *testing.T) {
		e, _ := attachApp(t)
		e.app.paths = testPaths()
		requireCode(t, codeEnvironmentUnknown, e.app.runAttach(t.Context(), "nowhere", "a"))
	})
	t.Run("a remote id without --env names its environment", func(t *testing.T) {
		e, _, rec := newEnvOpApp(t, `{}`)
		e.app.stdinTerminal = func() bool { return true }
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		err := e.app.runAttach(t.Context(), "", "api-bump")
		requireCode(t, codeWorkspaceNotFound, err)
		assert.Contains(t, err.Error(), "it is on linux-server; pass --env linux-server")
	})
	t.Run("the agent going offline is environment_offline", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		te := newTermEnv(t)
		te.app.paths = e.app.paths
		te.app.configs = e.app.configs
		te.frames <- termFrame{exit: &envproto.TerminalExit{Reason: "disconnected"}}
		err := te.app.runAttach(t.Context(), "linux-server", "api-bump")
		requireCode(t, codeEnvironmentOffline, err)
	})
}

func TestApp_newAttachCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newAttachCmd()
	t.Run("has --env", func(t *testing.T) {
		assertFlag(t, cmd, "env", "")
	})
	t.Run("takes one workspace", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
		assert.NoError(t, cmd.Args(cmd, []string{"a"}))
	})
	t.Run("refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		requireUsageError(t, cmd.PreRunE(cmd, nil))
	})
}
