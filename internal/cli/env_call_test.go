package cli

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// callerReturning is an environmentCaller that answers every call with reply, err.
func callerReturning(reply string, err error) *environmentCallerMock {
	return &environmentCallerMock{CallEnvironmentFunc: func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
		return daemon.CallReply{Reply: json.RawMessage(reply)}, err
	}}
}

// useCaller makes app call environments through caller.
func useCaller(app *App, caller environmentCaller) {
	app.dialEnvironments = func(context.Context) (environmentCaller, error) { return caller, nil }
}

func TestNewRequestID(t *testing.T) {
	t.Run("is a valid request id", func(t *testing.T) {
		id, err := newRequestID()
		require.NoError(t, err)
		assert.True(t, envproto.ValidRequestID(id), id)
	})
	t.Run("differs between calls", func(t *testing.T) {
		a, err := newRequestID()
		require.NoError(t, err)
		b, err := newRequestID()
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})
}

func TestApp_callEnvironment(t *testing.T) {
	t.Run("returns the result of an ok reply", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning(`{"ok":true,"result":{"id":"x"},"error":null}`, nil))
		got, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"x"}`, string(got))
	})
	t.Run("an unknown operation is usage", func(t *testing.T) {
		app := newRemoteApp(t)
		_, err := app.callEnvironment(t.Context(), "linux-server", "files.read", struct{}{})
		requireCode(t, codeUsage, err)
	})
	t.Run("a daemon error keeps its code and details", func(t *testing.T) {
		for _, code := range []errorCode{codeEnvironmentUnknown, codeEnvironmentOffline, codeEnvironmentOutdated, codeUsage} {
			app := newRemoteApp(t)
			useCaller(app, callerReturning("", &daemon.RemoteError{Code: string(code), Message: "no", Details: map[string]any{"k": "v"}}))
			_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
			requireCode(t, code, err)
			assert.Equal(t, map[string]any{"k": "v"}, asCLIError(err).details)
		}
	})
	t.Run("an unknown daemon code is internal", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning("", &daemon.RemoteError{Code: "from_the_future", Message: "no"}))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeInternal, err)
	})
	t.Run("another error is returned as it is", func(t *testing.T) {
		app := newRemoteApp(t)
		boom := errors.New("boom")
		useCaller(app, callerReturning("", boom))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		assert.ErrorIs(t, err, boom)
	})
	t.Run("a failed dial is returned", func(t *testing.T) {
		app := newRemoteApp(t)
		app.dialEnvironments = func(context.Context) (environmentCaller, error) {
			return nil, newError(codeDaemonUnavailable, errors.New("down"))
		}
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeDaemonUnavailable, err)
	})
	t.Run("an agent error keeps its code and gains the environment", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning(`{"ok":false,"result":null,"error":{"code":"repo_not_found","message":"no repo","details":{"spec":"x"}}}`, nil))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeRepoNotFound, err)
		assert.Equal(t, map[string]any{"spec": "x", "environment": "linux-server"}, asCLIError(err).details)
		assert.Equal(t, exitNotFound, asCLIError(err).exitCode())
	})
	t.Run("an unknown agent code is internal with remote_code", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning(`{"ok":false,"result":null,"error":{"code":"from_the_future","message":"no","details":null}}`, nil))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeInternal, err)
		assert.Equal(t, map[string]any{"remote_code": "from_the_future", "environment": "linux-server"}, asCLIError(err).details)
	})
	t.Run("an unknown daemon code is internal with remote_code", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning("", &daemon.RemoteError{Code: "from_the_future", Message: "no"}))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeInternal, err)
		assert.Equal(t, "from_the_future", asCLIError(err).details["remote_code"])
	})
	t.Run("an error reply without an error is internal", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning(`{"ok":false,"result":null,"error":null}`, nil))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeInternal, err)
	})
	t.Run("a reply that isn't JSON is internal", func(t *testing.T) {
		app := newRemoteApp(t)
		useCaller(app, callerReturning(`nope`, nil))
		_, err := app.callEnvironment(t.Context(), "linux-server", envproto.OpWorkspaceList, envproto.ListArgs{})
		requireCode(t, codeInternal, err)
	})
}

// remoteParentApp is an App with environments linux-server (snapshot entry
// "api-bump") and mac-mini (snapshot entry "api-bump" too when both is set),
// and the local workspace "local-ws".
func remoteParentApp(t *testing.T, both bool) *App {
	t.Helper()
	app := newRemoteApp(t)
	app.workspaces = &workspaceStoreMock{
		FindFunc: func(arg string) (workspace.Workspace, error) {
			if arg == "local-ws" {
				return workspace.Workspace{ID: arg}, nil
			}
			return workspace.Workspace{}, workspace.ErrNotFound
		},
		ContainingFunc: func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound },
		WarningsFunc:   func() []string { return nil },
	}
	app.getwd = func() (string, error) { return "/somewhere", nil }
	linux := addLinuxServer(t, app)
	writeRemoteSnapshot(t, app, linux, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
	if both {
		rec, _, _, err := app.envStore().Add(t.Context(), "mac-mini", "127.0.0.1:8884")
		require.NoError(t, err)
		writeRemoteSnapshot(t, app, &rec, testNow(), remoteWorkspaces(remoteWorkspace("api-bump", "github.com/bobcob7/buff-bear", "", false)))
	}
	return app
}

func TestApp_remoteParent(t *testing.T) {
	t.Run("no --env and no parent is local", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "", "")
		require.NoError(t, err)
		assert.Empty(t, runOn)
		assert.Empty(t, parent)
	})
	t.Run("a local parent stays local", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "", "local-ws")
		require.NoError(t, err)
		assert.Empty(t, runOn)
		assert.Equal(t, "local-ws", parent)
	})
	t.Run("a parent found nowhere is left to the local lookup", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "", "nope")
		require.NoError(t, err)
		assert.Empty(t, runOn)
		assert.Equal(t, "nope", parent)
	})
	t.Run("a parent on one environment runs there", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "", "api-bump")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", runOn)
		assert.Equal(t, "api-bump", parent)
	})
	t.Run("a parent on two environments is usage", func(t *testing.T) {
		_, _, err := remoteParentApp(t, true).remoteParent(t.Context(), "", "api-bump")
		requireCode(t, codeUsage, err)
		assert.ErrorContains(t, err, "--env")
	})
	t.Run("--env local with a remote parent is a mismatch", func(t *testing.T) {
		_, _, err := remoteParentApp(t, false).remoteParent(t.Context(), "local", "api-bump")
		requireCode(t, codeEnvironmentMismatch, err)
	})
	t.Run("--env local is the same as no --env", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "local", "local-ws")
		require.NoError(t, err)
		assert.Empty(t, runOn)
		assert.Equal(t, "local-ws", parent)
	})
	t.Run("--env unknown is environment_unknown", func(t *testing.T) {
		_, _, err := remoteParentApp(t, false).remoteParent(t.Context(), "nowhere", "")
		requireCode(t, codeEnvironmentUnknown, err)
	})
	t.Run("--env with a parent on it passes the parent", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, true).remoteParent(t.Context(), "linux-server", "api-bump")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", runOn)
		assert.Equal(t, "api-bump", parent)
	})
	t.Run("--env with a local parent is a mismatch", func(t *testing.T) {
		_, _, err := remoteParentApp(t, false).remoteParent(t.Context(), "linux-server", "local-ws")
		requireCode(t, codeEnvironmentMismatch, err)
	})
	t.Run("--env with a parent on another environment is a mismatch", func(t *testing.T) {
		app := remoteParentApp(t, false)
		rec, _, _, err := app.envStore().Add(t.Context(), "mac-mini", "127.0.0.1:8884")
		require.NoError(t, err)
		writeRemoteSnapshot(t, app, &rec, testNow(), remoteWorkspaces(remoteWorkspace("other", "github.com/bobcob7/buff-bear", "", false)))
		_, _, err = app.remoteParent(t.Context(), "mac-mini", "api-bump")
		requireCode(t, codeEnvironmentMismatch, err)
	})
	t.Run("--env with a parent found nowhere is workspace_not_found", func(t *testing.T) {
		_, _, err := remoteParentApp(t, false).remoteParent(t.Context(), "linux-server", "nope")
		requireCode(t, codeWorkspaceNotFound, err)
	})
	t.Run("--env with --parent none has no parent", func(t *testing.T) {
		runOn, parent, err := remoteParentApp(t, false).remoteParent(t.Context(), "linux-server", "none")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", runOn)
		assert.Empty(t, parent)
	})
	t.Run("--env with no parent has no parent and no warning", func(t *testing.T) {
		app := remoteParentApp(t, false)
		runOn, parent, err := app.remoteParent(t.Context(), "linux-server", "")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", runOn)
		assert.Empty(t, parent)
		assert.Empty(t, app.warned)
	})
}
