package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// opsFor is the agent's operations running in app, an App whose mocks stand
// in for this machine and whose streams are the ones each operation is given.
func opsFor(app *App) *agentOperations {
	app.paths = testPaths()
	return &agentOperations{app: app, name: "linux-server", newApp: func(stdout, stderr io.Writer) *App {
		app.stdout, app.stderr = stdout, stderr
		app.environment = "linux-server"
		return app
	}, newAppWithStdin: func(stdin io.Reader, stdout, stderr io.Writer) *App {
		app.stdin, app.stdout, app.stderr = stdin, stdout, stderr
		app.environment = "linux-server"
		return app
	}}
}

// claudeOps is the agent's operations in e's App, whose stores are e's, since
// withConfig rebuilds them from newStores.
func claudeOps(e *claudeEnv) *agentOperations {
	e.app.newStores = func(config.Paths, config.Config) stores {
		return stores{cache: e.app.cache, workspaces: e.store, lifetime: e.app.lifetime, loopData: e.app.loopData}
	}
	return opsFor(e.app)
}

func TestCreateArgv(t *testing.T) {
	prefix := []string{"--json", "--config-dir", "/cfg"}
	with := func(rest ...string) []string { return append(append([]string{}, prefix...), rest...) }
	tests := []struct {
		name string
		args envproto.CreateArgs
		want []string
	}{
		{
			name: "a claude agent with a prompt and model",
			args: envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"api", "web@dev"}, Prompt: new("fix it"), Model: new("opus")},
			want: with("claude", "--parent", "none", "api", "web@dev", "--", "fix it", "--model", "opus"),
		},
		{
			name: "a claude agent with no prompt has no --",
			args: envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"api"}},
			want: with("claude", "--parent", "none", "api"),
		},
		{
			name: "a runner with an endpoint",
			args: envproto.CreateArgs{Kind: envproto.KindRunner, Specs: []string{"api"}, Prompt: new("go"), Endpoint: new("local")},
			want: with("claude", "--runner", "ws", "--endpoint", "local", "--parent", "none", "api", "--", "go"),
		},
		{
			name: "a parent",
			args: envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{}, Parent: new("fix-login-d8efb24f"), Prompt: new("go")},
			want: with("claude", "--parent", "fix-login-d8efb24f", "--", "go"),
		},
		{
			name: "hidden, offline and named",
			args: envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"api"}, Name: new("Fix"), Hidden: true, Offline: true},
			want: with("claude", "--name", "Fix", "--hidden", "--offline", "--parent", "none", "api"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, createArgv("/cfg", &tt.args))
		})
	}
}

func TestAgentOperations_Run(t *testing.T) {
	t.Run("workspace.list returns ws list --json --all's object", func(t *testing.T) {
		hidden := listedWorkspace("api-bump")
		hidden.Hidden = true
		entries := []lifetime.Entry{{Workspace: listedWorkspace("fix-login")}, {Workspace: hidden}}
		want := newListApp(entries, nil)
		want.app.global.json = true
		require.NoError(t, want.app.runList(t.Context(), &listOptions{all: true}))
		e := newListApp(entries, nil)
		got, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceList, &envproto.ListArgs{})
		require.NoError(t, err)
		assert.JSONEq(t, want.stdout.String(), string(got))
		assert.Contains(t, string(got), "api-bump", "list --all includes the hidden workspace")
	})
	t.Run("workspace.rename returns renameJSON", func(t *testing.T) {
		e := newRenameApp()
		got, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRename, &envproto.RenameArgs{Workspace: renamedID, Name: "Fix login"})
		require.NoError(t, err)
		assert.JSONEq(t, `{"id":"fix-login-d8efb24f","name":"Fix login","path":"/cfg/workspaces/fix-login-d8efb24f","warnings":[]}`, string(got))
	})
	t.Run("workspace.hide with hidden false runs unhide", func(t *testing.T) {
		e := newHideApp()
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceHide, &envproto.HideArgs{Workspace: "fix-login-d8efb24f", Hidden: false})
		require.NoError(t, err)
		require.Len(t, e.store.SetHiddenCalls(), 1)
		assert.False(t, e.store.SetHiddenCalls()[0].Hidden)
	})
	t.Run("workspace.rm passes --keep-children", func(t *testing.T) {
		e := newRmApp("fix-login-d8efb24f")
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: "fix-login-d8efb24f", KeepChildren: true})
		require.NoError(t, err)
		require.Len(t, e.lifetime.RemoveTreeCalls(), 1)
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.KeepChildren)
	})
	t.Run("a CLI error comes back with its code", func(t *testing.T) {
		e := newMockedApp()
		e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRename, &envproto.RenameArgs{Workspace: "gone", Name: "x"})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "workspace_not_found", re.Code)
	})
	t.Run("a rm of a missing workspace comes back workspace_not_found", func(t *testing.T) {
		e := newMockedApp()
		e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: "gone"})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "workspace_not_found", re.Code)
	})
	t.Run("workspace.create stamps the environment", func(t *testing.T) {
		e := newClaudeEnv(t)
		var got workspace.CreateOptions
		create := e.store.CreateFunc
		e.store.CreateFunc = func(ctx context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			got = opts
			return create(ctx, opts)
		}
		_, err := claudeOps(e).Run(t.Context(), envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"buff-bear"}, Prompt: new("go")})
		require.NoError(t, err)
		assert.Equal(t, "linux-server", got.Environment)
		assert.Empty(t, got.Parent)
	})
	t.Run("a spec that doesn't parse is spec_invalid and runs nothing", func(t *testing.T) {
		e := newClaudeEnv(t)
		_, err := claudeOps(e).Run(t.Context(), envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"a b c@@"}, Prompt: new("go")})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "spec_invalid", re.Code)
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("the spec pre-check refuses before any App is built", func(t *testing.T) {
		built := false
		ops := &agentOperations{app: &App{paths: testPaths()}, name: "linux-server", newApp: func(io.Writer, io.Writer) *App {
			built = true
			return nil
		}}
		_, err := ops.Run(t.Context(), envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindClaude, Specs: []string{"a b c@@"}})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok)
		assert.Equal(t, "spec_invalid", re.Code)
		assert.False(t, built)
	})
	t.Run("a kind other than claude and runner is usage and runs nothing", func(t *testing.T) {
		for _, kind := range []string{"shell", "other", ""} {
			ops := &agentOperations{app: &App{paths: testPaths()}, name: "linux-server", newApp: func(io.Writer, io.Writer) *App {
				t.Fatal("no App should be built")
				return nil
			}}
			_, err := ops.Run(t.Context(), envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: kind})
			re, ok := errors.AsType[*envproto.ReplyError](err)
			require.True(t, ok)
			assert.Equal(t, "usage", re.Code)
		}
	})
	t.Run("workspace.create for a runner stamps the environment", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		var got workspace.CreateOptions
		create := e.store.CreateFunc
		e.store.CreateFunc = func(ctx context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
			got = opts
			return create(ctx, opts)
		}
		e.app.newStores = func(config.Paths, config.Config) stores {
			return stores{cache: e.app.cache, workspaces: e.store, lifetime: e.app.lifetime, loopData: e.app.loopData}
		}
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceCreate, &envproto.CreateArgs{Kind: envproto.KindRunner, Specs: []string{}, Prompt: new("go")})
		require.NoError(t, err)
		assert.Equal(t, "linux-server", got.Environment)
	})
	t.Run("a dash-prefixed name is a name, not a flag", func(t *testing.T) {
		e := newRenameApp()
		got, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRename, &envproto.RenameArgs{Workspace: renamedID, Name: "-x"})
		require.NoError(t, err)
		assert.Contains(t, string(got), `"name":"-x"`)
	})
	t.Run("a dash-prefixed workspace is a workspace, not a flag", func(t *testing.T) {
		e := newRmApp("--dry-run")
		_, err := opsFor(e.app).Run(t.Context(), envproto.OpWorkspaceRm, &envproto.RmArgs{Workspace: "--dry-run", KeepChildren: true})
		require.NoError(t, err)
		require.Len(t, e.lifetime.RemoveTreeCalls(), 1)
		assert.False(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
		for _, hidden := range []bool{true, false} {
			h := newHideApp()
			h.store.FindFunc = func(arg string) (workspace.Workspace, error) {
				return workspace.Workspace{ID: arg, Path: "/cfg/workspaces/x"}, nil
			}
			_, err := opsFor(h.app).Run(t.Context(), envproto.OpWorkspaceHide, &envproto.HideArgs{Workspace: "--help", Hidden: hidden})
			require.NoError(t, err)
			require.Len(t, h.store.SetHiddenCalls(), 1)
			assert.Equal(t, "--help", h.store.SetHiddenCalls()[0].ID)
		}
	})
}

func TestNewAgentOperations_newApp(t *testing.T) {
	app, _, _ := newTestApp()
	ops := newAgentOperations(app, &envagent.Identity{ID: "env-1a2b3c4d", Name: "linux-server"})
	var out, errb bytes.Buffer
	got := ops.newApp(&out, &errb)
	require.NotSame(t, app, got)
	assert.Equal(t, "linux-server", got.environment)
	assert.Empty(t, app.environment)
	assert.False(t, isTerminal(got.stdin))
	data, err := io.ReadAll(got.stdin)
	require.NoError(t, err)
	assert.Empty(t, data)
	_, _ = got.stdout.Write([]byte("out"))
	_, _ = got.stderr.Write([]byte("err"))
	assert.Equal(t, "out", out.String())
	assert.Equal(t, "err", errb.String())
	assert.NotSame(t, ops.newApp(&out, &errb), got)
}

func TestAgentOperations_concurrent(t *testing.T) {
	app, _, _ := newTestApp()
	ops := newAgentOperations(app, &envagent.Identity{ID: "env-1a2b3c4d", Name: "linux-server"})
	var wg sync.WaitGroup
	var outs [2]bytes.Buffer
	var errs [2]bytes.Buffer
	for i := range outs {
		wg.Go(func() {
			n := ops.newApp(&outs[i], &errs[i])
			n.Run(t.Context(), []string{"--json", "--config-dir", testPaths().Root, "rename", "--", "no-such-" + string(rune('a'+i)), "x"})
		})
	}
	wg.Wait()
	assert.Contains(t, errs[0].String(), "no-such-a")
	assert.NotContains(t, errs[0].String(), "no-such-b")
	assert.Contains(t, errs[1].String(), "no-such-b")
	assert.NotContains(t, errs[1].String(), "no-such-a")
	assert.Empty(t, outs[0].String()+outs[1].String())
}

func TestCommandFailure(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		code    string
		message string
	}{
		{"a JSON error", `{"error":{"code":"workspace_not_found","message":"no"}}`, "workspace_not_found", "no"},
		{"a warning line before the JSON line", "ws: couldn't remove workspace x: busy\n" + `{"error":{"code":"claude_untrusted","message":"untrusted","details":{"root":"/r"}}}` + "\n", "claude_untrusted", "untrusted"},
		{"the last JSON error wins", `{"error":{"code":"a","message":"first"}}` + "\n" + `{"error":{"code":"b","message":"last"}}`, "b", "last"},
		{"plain text is internal with its first line", "\nws: boom\nmore\n", "internal", "ws: boom"},
		{"a JSON error with no code is internal", `{"error":{"message":"x"}}`, "internal", `{"error":{"message":"x"}}`},
		{"empty stderr is internal", "", "internal", "the command failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, ok := errors.AsType[*envproto.ReplyError](commandFailure(nil, []byte(tt.stderr)))
			require.True(t, ok)
			assert.Equal(t, tt.code, re.Code)
			assert.Equal(t, tt.message, re.Message)
		})
	}
	t.Run("details pass through", func(t *testing.T) {
		re, _ := errors.AsType[*envproto.ReplyError](commandFailure(nil, []byte("ws: warn\n"+`{"error":{"code":"c","message":"m","details":{"k":"v"}}}`)))
		assert.Equal(t, map[string]any{"k": "v"}, re.Details)
	})
	t.Run("a failed item on stdout is its error when stderr has none", func(t *testing.T) {
		stdout := `{"warnings":[],"workspaces":[{"id":"x","removed":false,"error":{"code":"workspace_not_found","message":"no"}}]}`
		re, ok := errors.AsType[*envproto.ReplyError](commandFailure([]byte(stdout), nil))
		require.True(t, ok)
		assert.Equal(t, "workspace_not_found", re.Code)
		assert.Equal(t, "no", re.Message)
	})
	t.Run("a stderr error beats a failed item on stdout", func(t *testing.T) {
		stdout := `{"workspaces":[{"id":"x","removed":false,"error":{"code":"workspace_not_found","message":"no"}}]}`
		re, ok := errors.AsType[*envproto.ReplyError](commandFailure([]byte(stdout), []byte(`{"error":{"code":"usage","message":"bad"}}`)))
		require.True(t, ok)
		assert.Equal(t, "usage", re.Code)
	})
}

func TestCommandObject(t *testing.T) {
	for _, out := range []string{"null", `"x"`, "[]", "7", "", "{"} {
		_, err := commandObject([]byte(out))
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok, out)
		assert.Equal(t, "internal", re.Code, out)
	}
	got, err := commandObject([]byte("  {\"a\":1}\n"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"a":1}`, string(got))
}

func TestAgentRedactor_environment(t *testing.T) {
	app := New("v", strings.NewReader(""), io.Discard, io.Discard, nil, []string{"CUSTOM_API_KEY=env-only-value-123"})
	app.paths = config.NewPaths(t.TempDir())
	app.paths.Root = t.TempDir()
	clean, n := agentRedactor(app).String("leaked env-only-value-123 here")
	assert.NotContains(t, clean, "env-only-value-123")
	assert.Equal(t, 1, n)
}

// agentStateApp is an agent's operations over a fresh state directory.
func agentStateApp(t *testing.T) (*agentOperations, string) {
	t.Helper()
	app, _, _ := newTestApp()
	app.paths = config.NewPaths(t.TempDir())
	return newAgentOperations(app, &envagent.Identity{ID: "env-1a2b3c4d", Name: "linux-server"}), app.paths.State
}

func TestAgentOperations_grantWorkspace(t *testing.T) {
	t.Run("a scope grant writes scope.json with this agent as self and the grant's parent", func(t *testing.T) {
		ops, state := agentStateApp(t)
		grant := &envproto.ScopeGrant{
			Environments: []envproto.EnvRef{{ID: "local", Name: "local"}, {ID: "env-1a2b3c4d", Name: "linux-server"}},
			Parent:       &envproto.WorkspaceAt{Environment: "local", Workspace: "plan-9f8e7d6c"},
		}
		require.NoError(t, ops.grantWorkspace("helper-1a2b3c4d", &envproto.CreateArgs{Scope: grant}))
		scope, file, err := envscope.Read(state, "helper-1a2b3c4d", envscope.Ref{ID: "env-1a2b3c4d", Name: "linux-server"})
		require.NoError(t, err)
		require.NotNil(t, file)
		assert.Equal(t, "granted", file.Source)
		assert.Equal(t, "env-1a2b3c4d", file.Self)
		assert.ElementsMatch(t, []string{"local", "env-1a2b3c4d"}, scope.IDs())
		require.NotNil(t, file.Parent)
		assert.Equal(t, envscope.Parent{Environment: "local", Workspace: "plan-9f8e7d6c"}, *file.Parent)
	})
	t.Run("a spawn writes spawn.toml with the workspace as its own root", func(t *testing.T) {
		ops, state := agentStateApp(t)
		spawn := &envproto.SpawnInfo{Role: "implementer", Model: "opus", Pass: []string{"pass"}, Fail: []string{"fail"}}
		require.NoError(t, ops.grantWorkspace("helper-1a2b3c4d", &envproto.CreateArgs{Spawn: spawn}))
		rec, err := machine.NewStore(state).Read("helper-1a2b3c4d", "helper-1a2b3c4d")
		require.NoError(t, err)
		require.NotNil(t, rec.Spawn)
		assert.Equal(t, "implementer", rec.Spawn.Role)
		assert.Equal(t, "opus", rec.Spawn.Model)
	})
	t.Run("a create without a grant writes neither file", func(t *testing.T) {
		ops, state := agentStateApp(t)
		require.NoError(t, ops.grantWorkspace("helper-1a2b3c4d", &envproto.CreateArgs{}))
		_, err := os.Stat(envscope.Path(state, "helper-1a2b3c4d"))
		require.ErrorIs(t, err, fs.ErrNotExist)
		_, err = os.Stat(machine.NewStore(state).Dir("helper-1a2b3c4d", "helper-1a2b3c4d"))
		require.ErrorIs(t, err, fs.ErrNotExist)
	})
	t.Run("a write that fails is an error", func(t *testing.T) {
		ops, state := agentStateApp(t)
		require.NoError(t, os.MkdirAll(state, 0o700))
		blocker := state + "/helper-1a2b3c4d"
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		grant := &envproto.ScopeGrant{Environments: []envproto.EnvRef{{ID: "env-1a2b3c4d", Name: "linux-server"}}}
		assert.Error(t, ops.grantWorkspace("helper-1a2b3c4d", &envproto.CreateArgs{Scope: grant}))
	})
}

func TestAgentOperations_grantCreated(t *testing.T) {
	const id = "helper-1a2b3c4d"
	grant := &envproto.ScopeGrant{
		Environments: []envproto.EnvRef{{ID: "env-1a2b3c4d", Name: "linux-server"}},
	}
	// createdEnv is an agent whose workspace.create makes id. Its config is
	// rooted in a fresh directory, and its lifetime manager records the
	// workspaces it removes.
	createdEnv := func(t *testing.T) (*agentOperations, *lifetimeManagerMock, config.Paths) {
		t.Helper()
		e := newClaudeEnv(t)
		e.ws.ID = id
		e.store.FindFunc = func(string) (workspace.Workspace, error) {
			return workspace.Workspace{ID: id}, nil
		}
		paths := config.NewPaths(t.TempDir())
		e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
			return paths, testConfig(), nil
		}}
		lt := stubLifetime()
		lt.RemoveTreeFunc = func(context.Context, *workspace.Workspace, lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			return nil, nil
		}
		e.app.lifetime = lt
		ops := claudeOps(e)
		ops.self = envscope.Ref{ID: "env-1a2b3c4d", Name: "linux-server"}
		return ops, lt, paths
	}
	create := func(ops *agentOperations, args *envproto.CreateArgs) (json.RawMessage, error) {
		args.Kind = envproto.KindClaude
		args.Specs = []string{"buff-bear"}
		return ops.Run(t.Context(), envproto.OpWorkspaceCreate, args)
	}
	t.Run("a create with a scope writes its scope file", func(t *testing.T) {
		ops, _, paths := createdEnv(t)
		out, err := create(ops, &envproto.CreateArgs{Scope: grant})
		require.NoError(t, err)
		var created struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal(out, &created))
		assert.Equal(t, id, created.ID)
		_, file, err := envscope.Read(paths.State, id, envscope.Ref{ID: "env-1a2b3c4d", Name: "linux-server"})
		require.NoError(t, err)
		require.NotNil(t, file)
		assert.Equal(t, "granted", file.Source)
	})
	t.Run("a create with a spawn writes its spawn.toml", func(t *testing.T) {
		ops, _, paths := createdEnv(t)
		_, err := create(ops, &envproto.CreateArgs{Spawn: &envproto.SpawnInfo{Role: "implementer", Model: "opus"}})
		require.NoError(t, err)
		rec, err := machine.NewStore(paths.State).Read(id, id)
		require.NoError(t, err)
		require.NotNil(t, rec.Spawn)
		assert.Equal(t, "implementer", rec.Spawn.Role)
	})
	t.Run("a create with neither writes neither file", func(t *testing.T) {
		ops, lt, paths := createdEnv(t)
		_, err := create(ops, &envproto.CreateArgs{})
		require.NoError(t, err)
		_, err = os.Stat(envscope.Path(paths.State, id))
		require.ErrorIs(t, err, fs.ErrNotExist)
		_, err = os.Stat(machine.NewStore(paths.State).Dir(id, id))
		require.ErrorIs(t, err, fs.ErrNotExist)
		assert.Empty(t, lt.RemoveTreeCalls())
	})
	t.Run("a grant that can't be written removes the workspace and is internal", func(t *testing.T) {
		ops, lt, paths := createdEnv(t)
		require.NoError(t, os.WriteFile(paths.State, nil, 0o600))
		_, err := create(ops, &envproto.CreateArgs{Scope: grant})
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, "internal", re.Code)
		removed := lt.RemoveTreeCalls()
		require.Len(t, removed, 1)
		assert.Equal(t, id, removed[0].Ws.ID)
	})
}

func TestAgentOperations_machineRecord(t *testing.T) {
	const id = "helper-1a2b3c4d"
	setup := func(t *testing.T, report string) (*agentOperations, string) {
		t.Helper()
		ops, state := agentStateApp(t)
		wsStore := stubWorkspaces()
		wsStore.ListFunc = func() ([]workspace.Workspace, error) { return []workspace.Workspace{{ID: id}}, nil }
		ops.app.workspaces = wsStore
		store := machine.NewStore(state)
		require.NoError(t, store.WriteSpawn(id, id, &machine.Spawn{Role: "implementer", Pass: []string{"pass"}, Fail: []string{"fail"}}))
		require.NoError(t, store.WriteResult(id, id, &machine.Result{Verdict: "pass", RecordedAt: time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)}, []byte(report)))
		return ops, state
	}
	decode := func(t *testing.T, raw []byte) envproto.MachineRecordResult {
		t.Helper()
		var out envproto.MachineRecordResult
		require.NoError(t, json.Unmarshal(raw, &out))
		return out
	}
	t.Run("returns the result, role and report", func(t *testing.T) {
		ops, _ := setup(t, "the report")
		raw, err := ops.machineRecord(id)
		require.NoError(t, err)
		out := decode(t, raw)
		assert.Equal(t, id, out.Workspace)
		require.NotNil(t, out.Role)
		assert.Equal(t, "implementer", *out.Role)
		require.NotNil(t, out.Result)
		assert.Equal(t, "pass", out.Result.Verdict)
		require.NotNil(t, out.Report)
		assert.Equal(t, "the report", *out.Report)
		assert.False(t, out.ReportTruncated)
	})
	t.Run("cuts a long report on a UTF-8 boundary", func(t *testing.T) {
		ops, _ := setup(t, "a"+strings.Repeat("é", envproto.MaxRecordReportBytes))
		raw, err := ops.machineRecord(id)
		require.NoError(t, err)
		out := decode(t, raw)
		require.NotNil(t, out.Report)
		assert.True(t, out.ReportTruncated)
		assert.Len(t, *out.Report, envproto.MaxRecordReportBytes-1)
		assert.True(t, utf8.ValidString(*out.Report))
	})
	t.Run("a workspace with no progress has progress null", func(t *testing.T) {
		ops, _ := setup(t, "the report")
		raw, err := ops.machineRecord(id)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(raw, &fields))
		assert.Equal(t, "null", string(fields["progress"]))
	})
	t.Run("an unknown workspace is workspace_not_found", func(t *testing.T) {
		ops, _ := setup(t, "the report")
		_, err := ops.machineRecord("plan-9f8e7d6c")
		re, ok := errors.AsType[*envproto.ReplyError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, "workspace_not_found", re.Code)
	})
}
