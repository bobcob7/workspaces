package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// linuxRef and winRef are two enrolled environments the scope tests use.
var (
	linuxRef = envscope.Ref{ID: "env-1a2b3c4d", Name: "linux"}
	winRef   = envscope.Ref{ID: "env-2b3c4d5e", Name: "win-rig"}
)

// scopeCall is one scope operation an environment host was asked for.
type scopeCall struct {
	caller string
	op     string
	args   json.RawMessage
}

// scopeHost is an environment host that records scope operations and answers
// each with reply.
type scopeHost struct {
	daemon.NopEnvironments
	reply envproto.Reply
	mu    sync.Mutex
	calls []scopeCall
}

// Scope records the call and answers it with the host's reply.
func (h *scopeHost) Scope(_ context.Context, caller, op string, args json.RawMessage) envproto.Reply {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, scopeCall{caller: caller, op: op, args: args})
	return h.reply
}

// Calls is the scope operations the host was asked for, oldest first.
func (h *scopeHost) Calls() []scopeCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

// serveScopeHost serves a scopeHost that answers with reply until the test ends.
func serveScopeHost(t *testing.T, app *App, reply envproto.Reply) *scopeHost {
	t.Helper()
	host := &scopeHost{reply: reply}
	serveDaemonWith(t, app, host)
	return host
}

// scopeOK is a successful scope reply whose result is result as JSON.
func scopeOK(t *testing.T, result any) envproto.Reply {
	t.Helper()
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	return envproto.Reply{OK: true, Result: raw}
}

// writeCallerScope writes the scope file of the caller plan on this machine,
// granted, holding refs.
func writeCallerScope(t *testing.T, state string, refs ...envscope.Ref) {
	t.Helper()
	scope, err := envscope.New(localRef(), refs)
	require.NoError(t, err)
	require.NoError(t, envscope.Write(state, "plan", envscope.FileFor(scope, "granted", nil, time.Now())))
}

// scopeFileOf reads the scope of workspace id on this machine, and its file.
func scopeFileOf(t *testing.T, state, id string) (envscope.Scope, *envscope.File) {
	t.Helper()
	scope, file, err := envscope.Read(state, id, localRef())
	require.NoError(t, err)
	require.NotNil(t, file)
	return scope, file
}

// addEnvironmentForScope adds name to app's environment store, so it can be
// named by --env-scope.
func addEnvironmentForScope(t *testing.T, app *App, name string) {
	t.Helper()
	_, _, _, err := app.envStore().Add(t.Context(), name, "127.0.0.1:8884")
	require.NoError(t, err)
}

// errorDetails is the details of err, which must be a cliError's.
func errorDetails(t *testing.T, err error) map[string]any {
	t.Helper()
	ce, ok := errors.AsType[*cliError](err)
	require.True(t, ok, "not a cliError: %v", err)
	return ce.details
}

func TestApp_grantScope(t *testing.T) {
	t.Run("grants this machine alone with no names and no default_scope", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		scope, err := e.app.grantScope(nil)
		require.NoError(t, err)
		assert.Equal(t, []string{envscope.Local}, scope.Names())
		assert.False(t, scope.WiderThanSelf())
	})
	t.Run("grants the names, after this machine", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		addEnvironmentForScope(t, e.app, linuxRef.Name)
		scope, err := e.app.grantScope([]string{linuxRef.Name, envscope.Local})
		require.NoError(t, err)
		assert.Equal(t, []string{envscope.Local, linuxRef.Name}, scope.Names())
	})
	t.Run("a name that is neither local nor enrolled is environment_unknown", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.app.paths = config.NewPaths(t.TempDir())
		_, err := e.app.grantScope([]string{"nope"})
		requireCode(t, codeEnvironmentUnknown, err)
	})
}

func TestApp_writeChildScope(t *testing.T) {
	t.Run("a scope of this machine alone writes no scope.json", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.paths = config.NewPaths(t.TempDir())
		app.writeChildScope("child", childScope{scope: envscope.Only(localRef()), source: "granted"})
		_, err := os.Stat(envscope.Path(app.paths.State, "child"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
	t.Run("a wider scope is written with its source", func(t *testing.T) {
		app, _, _ := newTestApp()
		app.paths = config.NewPaths(t.TempDir())
		scope, err := envscope.New(localRef(), []envscope.Ref{linuxRef})
		require.NoError(t, err)
		app.writeChildScope("child", childScope{scope: scope, source: "inherited"})
		got, file := scopeFileOf(t, app.paths.State, "child")
		assert.Equal(t, "inherited", file.Source)
		assert.Equal(t, []string{envscope.Local, linuxRef.Name}, got.Names())
	})
}

func TestApp_narrowedScope(t *testing.T) {
	t.Run("without names the caller's scope is inherited", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef, winRef)
		cs, err := e.app.narrowedScope("plan", nil)
		require.NoError(t, err)
		assert.Equal(t, "inherited", cs.source)
		assert.Equal(t, []string{envscope.Local, linuxRef.Name, winRef.Name}, cs.scope.Names())
	})
	t.Run("a name outside the caller's scope is environment_out_of_scope", func(t *testing.T) {
		e := newMachineApp(t)
		writeCallerScope(t, e.app.paths.State, linuxRef)
		_, err := e.app.narrowedScope("plan", []string{winRef.Name})
		requireCode(t, codeEnvironmentOutOfScope, err)
		assert.Equal(t, winRef.Name, errorDetails(t, err)["environment"])
	})
}

func TestApp_workspaceScope(t *testing.T) {
	t.Run("a missing scope file is this machine alone, with no warning", func(t *testing.T) {
		e := newMachineApp(t)
		scope := e.app.workspaceScope("plan")
		assert.Equal(t, []string{envscope.Local}, scope.Names())
		assert.Empty(t, e.app.warned)
	})
	t.Run("an invalid scope file is this machine alone, with a warning", func(t *testing.T) {
		e := newMachineApp(t)
		require.NoError(t, os.MkdirAll(filepath.Dir(envscope.Path(e.app.paths.State, "plan")), 0o755))
		require.NoError(t, os.WriteFile(envscope.Path(e.app.paths.State, "plan"), []byte("{not json"), 0o600))
		scope := e.app.workspaceScope("plan")
		assert.Equal(t, []string{envscope.Local}, scope.Names())
		assert.Len(t, e.app.warned, 1)
	})
}

func TestApp_refuseScopeFlagsInWorkspace(t *testing.T) {
	t.Run("no flag is never refused", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.app.refuseScopeFlagsInWorkspace("ws claude", "", nil))
	})
	t.Run("outside a workspace --env and --env-scope are allowed", func(t *testing.T) {
		e := newClaudeEnv(t)
		require.NoError(t, e.app.refuseScopeFlagsInWorkspace("ws claude", "linux", []string{"linux"}))
	})
	t.Run("inside a workspace they are usage", func(t *testing.T) {
		e := newClaudeEnv(t)
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
		requireUsageError(t, e.app.refuseScopeFlagsInWorkspace("ws claude", "", []string{"linux"}))
	})
}

func TestScopeError(t *testing.T) {
	t.Run("an out-of-scope error is environment_out_of_scope with its details", func(t *testing.T) {
		err := scopeError(&envscope.OutOfScopeError{Environment: "linux", Allowed: []string{envscope.Local}})
		requireCode(t, codeEnvironmentOutOfScope, err)
		assert.Equal(t, map[string]any{"environment": "linux", "allowed": []string{envscope.Local}}, errorDetails(t, err))
	})
	t.Run("any other error is returned as it is", func(t *testing.T) {
		plain := errors.New("plain")
		assert.Equal(t, plain, scopeError(plain))
	})
}

func TestSpawnsRemotely(t *testing.T) {
	local := localRef()
	linux := envscope.Ref{ID: "env-1a2b3c4d", Name: "linux"}
	t.Run("no target is this machine", func(t *testing.T) {
		remote, err := spawnsRemotely("", local)
		require.NoError(t, err)
		assert.False(t, remote)
	})
	t.Run("this machine's own name is this machine", func(t *testing.T) {
		remote, err := spawnsRemotely("linux", linux)
		require.NoError(t, err)
		assert.False(t, remote)
	})
	t.Run("another environment is remote", func(t *testing.T) {
		remote, err := spawnsRemotely("win-rig", local)
		require.NoError(t, err)
		assert.True(t, remote)
	})
	t.Run("local from a remote environment is usage", func(t *testing.T) {
		_, err := spawnsRemotely("local", linux)
		requireUsageError(t, err)
	})
}

func TestApp_callScope(t *testing.T) {
	t.Run("an error reply keeps its code and details", func(t *testing.T) {
		e := newMachineApp(t)
		serveScopeHost(t, e.app, envproto.Reply{Error: &envproto.ReplyError{
			Code: "environment_out_of_scope", Message: "nope", Details: map[string]any{"environment": "linux"},
		}})
		_, err := e.app.callScope(t.Context(), localRef(), "spawn", struct{}{})
		requireCode(t, codeEnvironmentOutOfScope, err)
		assert.Equal(t, "linux", errorDetails(t, err)["environment"])
	})
	t.Run("an unknown error code is internal with its remote code", func(t *testing.T) {
		e := newMachineApp(t)
		serveScopeHost(t, e.app, envproto.Reply{Error: &envproto.ReplyError{Code: "from_the_future", Message: "later"}})
		_, err := e.app.callScope(t.Context(), localRef(), "spawn", struct{}{})
		requireCode(t, codeInternal, err)
		assert.Equal(t, "from_the_future", errorDetails(t, err)["remote_code"])
	})
}
