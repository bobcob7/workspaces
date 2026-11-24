package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newEnvOpApp is a mocked App whose environments live in a temp directory,
// with linux-server enrolled and every call answered with reply.
func newEnvOpApp(t *testing.T, reply string) (*mockedApp, *environmentCallerMock, *environment.Record) {
	t.Helper()
	e := newMockedApp()
	e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
	paths := config.NewPaths(t.TempDir())
	e.app.paths = paths
	e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
		return paths, testConfig(), nil
	}}
	rec := addLinuxServer(t, e.app)
	caller := callerReturning(reply, nil)
	useCaller(e.app, caller)
	return e, caller, rec
}

func TestApp_remoteTarget(t *testing.T) {
	t.Run("no environment runs here", func(t *testing.T) {
		e := newMockedApp()
		name, remote, err := e.app.remoteTarget("")
		require.NoError(t, err)
		assert.False(t, remote)
		assert.Empty(t, name)
	})
	t.Run("local runs here", func(t *testing.T) {
		e := newMockedApp()
		_, remote, err := e.app.remoteTarget(localEnvironment)
		require.NoError(t, err)
		assert.False(t, remote)
	})
	t.Run("an enrolled environment runs there", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{"ok":true,"result":{},"error":null}`)
		name, remote, err := e.app.remoteTarget("linux-server")
		require.NoError(t, err)
		assert.True(t, remote)
		assert.Equal(t, "linux-server", name)
	})
	t.Run("an unknown environment is environment_unknown", func(t *testing.T) {
		e := newMockedApp()
		e.app.paths = config.NewPaths(t.TempDir())
		_, _, err := e.app.remoteTarget("nowhere")
		requireCode(t, codeEnvironmentUnknown, err)
	})
}

func TestApp_remoteHint(t *testing.T) {
	t.Run("names the one environment whose snapshot lists the id", func(t *testing.T) {
		e, _, rec := newEnvOpApp(t, `{"ok":true,"result":{},"error":null}`)
		writeRemoteSnapshot(t, e.app, rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
		err := e.app.remoteHint("api-bump")
		requireCode(t, codeWorkspaceNotFound, err)
		assert.Contains(t, err.Error(), "it is on linux-server; pass --env linux-server")
	})
	t.Run("nil when no environment lists the id", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{"ok":true,"result":{},"error":null}`)
		assert.NoError(t, e.app.remoteHint("api-bump"))
	})
}

func TestApp_withRemoteHint(t *testing.T) {
	t.Run("keeps an error that isn't not-found", func(t *testing.T) {
		e := newMockedApp()
		boom := errors.New("boom")
		assert.ErrorIs(t, e.app.withRemoteHint("a", boom), boom)
	})
	t.Run("keeps not-found when no environment lists the id", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{"ok":true,"result":{},"error":null}`)
		err := e.app.withRemoteHint("a", workspace.ErrNotFound)
		assert.ErrorIs(t, err, workspace.ErrNotFound)
	})
}

func TestApp_remoteWorkspaceOp(t *testing.T) {
	argsFor := func(id string) any { return envproto.WorkspaceArgs{Workspace: id} }
	t.Run("calls once per id", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, `{"ok":true,"result":{"workspace":"a","session":"s-a","stopped":true},"error":null}`)
		require.NoError(t, e.app.remoteWorkspaceOp(t.Context(), "linux-server", envproto.OpAgentStop, argsFor, []string{"a", "b"}))
		calls := caller.CallEnvironmentCalls()
		require.Len(t, calls, 2)
		assert.Equal(t, "linux-server", calls[0].Name)
		assert.Equal(t, envproto.OpAgentStop, calls[1].Req.Op)
		assert.JSONEq(t, `{"workspace":"b"}`, string(calls[1].Req.Args))
	})
	t.Run("adds environment to each result", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{"ok":true,"result":{"id":"a","name":"x","path":"/p","warnings":[]},"error":null}`)
		e.app.global.json = true
		require.NoError(t, e.app.remoteWorkspaceOp(t.Context(), "linux-server", envproto.OpWorkspaceRename, func(id string) any {
			return envproto.RenameArgs{Workspace: id, Name: "x"}
		}, []string{"a"}))
		assert.JSONEq(t, `{"id":"a","name":"x","path":"/p","warnings":[],"environment":"linux-server"}`, e.stdout.String())
	})
	t.Run("stops at the first error", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, "")
		caller.CallEnvironmentFunc = func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
			return daemon.CallReply{}, &daemon.RemoteError{Code: string(codeUsage), Message: "no"}
		}
		err := e.app.remoteWorkspaceOp(t.Context(), "linux-server", envproto.OpAgentStop, argsFor, []string{"a", "b"})
		require.Error(t, err)
		assert.Len(t, caller.CallEnvironmentCalls(), 1)
	})
}

func TestApp_printRemoteResult(t *testing.T) {
	t.Run("a removal the agent failed is its error, with the environment", func(t *testing.T) {
		e := newMockedApp()
		raw := json.RawMessage(`{"workspaces":[{"id":"a","parent":null,"removed":false,"error":{"code":"usage","message":"busy"}}],"warnings":[]}`)
		err := e.app.printRemoteResult(envproto.OpWorkspaceRm, raw, "linux-server")
		requireCode(t, codeUsage, err)
		assert.Equal(t, "linux-server", asCLIError(err).details["environment"])
	})
	t.Run("a child removed with its parent is marked", func(t *testing.T) {
		e := newMockedApp()
		raw := json.RawMessage(`{"workspaces":[{"id":"c","parent":"p","removed":true,"error":null},{"id":"p","parent":null,"removed":true,"error":null}],"warnings":[]}`)
		require.NoError(t, e.app.printRemoteResult(envproto.OpWorkspaceRm, raw, "linux-server"))
		assert.Equal(t, "removed  └ c\nremoved  p\n", e.stdout.String())
	})
	t.Run("warnings go to stderr in text", func(t *testing.T) {
		e := newMockedApp()
		raw := json.RawMessage(`{"id":"a","hidden":true,"name":null,"path":"/p","warnings":["careful"]}`)
		require.NoError(t, e.app.printRemoteResult(envproto.OpWorkspaceHide, raw, "linux-server"))
		assert.Equal(t, "a  hidden\n", e.stdout.String())
		assert.Equal(t, "ws: warning: careful\n", e.stderr.String())
	})
}
