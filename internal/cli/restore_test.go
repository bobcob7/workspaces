package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newRestoreApp is a test App whose workspace store finds workspaces by
// id, and whose lifetime manager restores each with session "s-<id>",
// stopped and respawned unless a dry run.
func newRestoreApp(ids ...string) *mockedApp {
	e := newRmApp(ids...)
	e.lifetime.RestoreFunc = func(_ context.Context, opts lifetime.RestoreOptions) ([]lifetime.Restored, error) {
		var out []lifetime.Restored
		for _, ws := range opts.Workspaces {
			out = append(out, lifetime.Restored{Workspace: ws, SessionID: "s-" + ws.ID, State: lifetime.StateStopped, Respawned: !opts.DryRun})
		}
		return out, nil
	}
	return e
}

// restoredAs is a workspace Restore found, in state, with session s-<id>.
func restoredAs(id string, state lifetime.State, respawned bool) lifetime.Restored {
	return lifetime.Restored{Workspace: workspace.Workspace{ID: id}, SessionID: "s-" + id, State: state, Respawned: respawned}
}

func TestApp_newRestoreCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRestoreCmd()
	t.Run("has --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
	})
	t.Run("workspaces are optional", func(t *testing.T) {
		assert.Nil(t, cmd.Args)
	})
	t.Run("runs the removal pass, then restores", func(t *testing.T) {
		e := newRestoreApp("fix-login")
		cmd := e.app.newRestoreCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--dry-run", "fix-login"}))
		require.NoError(t, cmd.RunE(cmd, cmd.Flags().Args()))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		require.Len(t, e.lifetime.RestoreCalls(), 1)
		assert.True(t, e.lifetime.RestoreCalls()[0].Opts.DryRun)
	})
}

func TestApp_runRestore(t *testing.T) {
	t.Run("restores every agent workspace without arguments", func(t *testing.T) {
		e := newRestoreApp()
		e.lifetime.RestoreFunc = func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return []lifetime.Restored{
				restoredAs("fix-login", lifetime.StateStopped, true),
				restoredAs("docs-pass", lifetime.StateIdle, false),
				restoredAs("ws-20261018-k3f9qa", lifetime.StateWorking, false),
				restoredAs("api-bump", lifetime.StateStarting, false),
				restoredAs("old", lifetime.StateUnknown, false),
			}, nil
		}
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{}))
		assert.Equal(t, "restored  fix-login           s-fix-login\n"+
			"running   docs-pass           s-docs-pass\n"+
			"running   ws-20261018-k3f9qa  s-ws-20261018-k3f9qa\n"+
			"starting  api-bump            s-api-bump\n"+
			"unknown   old                 s-old\n", e.stdout.String())
		require.Len(t, e.lifetime.RestoreCalls(), 1)
		assert.Equal(t, lifetime.RestoreOptions{All: true}, e.lifetime.RestoreCalls()[0].Opts)
	})
	t.Run("--json", func(t *testing.T) {
		e := newRestoreApp()
		e.lifetime.RestoreFunc = func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return []lifetime.Restored{restoredAs("a", lifetime.StateStopped, true), restoredAs("b", lifetime.StateIdle, false)}, nil
		}
		e.app.global.json = true
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{}))
		assert.JSONEq(t, `{"workspaces":[
			{"id":"a","session":"s-a","state":"stopped","restored":true,"error":null},
			{"id":"b","session":"s-b","state":"idle","restored":false,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("nothing to restore", func(t *testing.T) {
		e := newRestoreApp()
		e.app.global.json = true
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{}))
		assert.JSONEq(t, `{"workspaces":[],"warnings":[]}`, e.stdout.String())
	})
	t.Run("named workspaces", func(t *testing.T) {
		e := newRestoreApp("a", "b")
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"b", "a", "b"}}))
		assert.Equal(t, "restored  b  s-b\nrestored  a  s-a\n", e.stdout.String())
		require.Len(t, e.lifetime.RestoreCalls(), 1)
		opts := e.lifetime.RestoreCalls()[0].Opts
		assert.False(t, opts.All)
		require.Len(t, opts.Workspaces, 2)
		assert.Equal(t, "b", opts.Workspaces[0].ID)
		assert.Equal(t, "a", opts.Workspaces[1].ID)
	})
	t.Run("--dry-run", func(t *testing.T) {
		e := newRestoreApp("a")
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.Equal(t, "would restore  a  s-a\n", e.stdout.String())
		assert.True(t, e.lifetime.RestoreCalls()[0].Opts.DryRun)
	})
	t.Run("--dry-run --json", func(t *testing.T) {
		e := newRestoreApp("a")
		e.app.global.json = true
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a","session":"s-a","state":"stopped","restored":false,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a failure doesn't stop the others; the exit code is the first failure's", func(t *testing.T) {
		e := newRestoreApp("sh", "elsewhere", "ok")
		e.lifetime.RestoreFunc = func(_ context.Context, opts lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return []lifetime.Restored{
				{Workspace: opts.Workspaces[0], Err: lifetime.ErrNotAgent},
				{Workspace: opts.Workspaces[1], SessionID: "s-elsewhere", Err: lifetime.ErrSessionElsewhere},
				restoredAs("ok", lifetime.StateStopped, true),
			}, nil
		}
		e.app.global.json = true
		err := e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"missing", "sh", "elsewhere", "ok"}})
		requireCode(t, codeWorkspaceNotFound, err)
		assert.JSONEq(t, `{"workspaces":[
			{"id":"missing","session":null,"state":null,"restored":false,"error":{"code":"workspace_not_found","message":"workspace not found"}},
			{"id":"sh","session":null,"state":null,"restored":false,"error":{"code":"workspace_not_found","message":"not an agent workspace"}},
			{"id":"elsewhere","session":"s-elsewhere","state":null,"restored":false,"error":{"code":"session_mismatch","message":"the workspace's Claude Code session is in another directory"}},
			{"id":"ok","session":"s-ok","state":"stopped","restored":true,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a failed respawn is claude_failed, on stderr as text", func(t *testing.T) {
		e := newRestoreApp("a", "b")
		e.lifetime.RestoreFunc = func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			failed := restoredAs("a", lifetime.StateStopped, false)
			failed.Err = &claude.CommandError{Args: []string{"respawn", "s-a"}, ExitCode: 1, Stderr: "nope", Err: errTest}
			return []lifetime.Restored{failed, restoredAs("b", lifetime.StateStopped, true)}, nil
		}
		requireCode(t, codeClaudeFailed, e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"a", "b"}}))
		assert.Equal(t, "restored  b  s-b\n", e.stdout.String())
		assert.Equal(t, "ws: claude respawn s-a: boom: nope\n", e.stderr.String())
	})
	t.Run("claude agents failing fails the command with nothing on stdout", func(t *testing.T) {
		e := newRestoreApp("a")
		e.lifetime.RestoreFunc = func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return nil, &claude.CommandError{Args: []string{"agents", "--json", "--all"}, ExitCode: 1, Err: errTest}
		}
		requireCode(t, codeClaudeFailed, e.app.runRestore(t.Context(), &restoreOptions{}))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("claude agents failing with named workspaces", func(t *testing.T) {
		e := newRestoreApp("a")
		e.lifetime.RestoreFunc = func(context.Context, lifetime.RestoreOptions) ([]lifetime.Restored, error) {
			return nil, claude.ErrNotInstalled
		}
		requireCode(t, codeClaudeMissing, e.app.runRestore(t.Context(), &restoreOptions{workspaces: []string{"a"}}))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("warnings", func(t *testing.T) {
		e := newRestoreApp()
		e.lifetime.WarningsFunc = func() []string { return []string{"skipped workspace x: boom"} }
		require.NoError(t, e.app.runRestore(t.Context(), &restoreOptions{}))
		assert.Equal(t, "ws: warning: skipped workspace x: boom\n", e.stderr.String())
	})
	t.Run("end to end: respawns a stopped agent and leaves a running one", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		stopped := e.start(t, "a-login", "e8cdf5d8")
		e.setAgents(t, "["+stoppedAgentJSON("e8cdf5d8", stopped)+"]")
		running := e.start(t, "b-docs", "3c11e0f2")
		e.setAgents(t, "["+stoppedAgentJSON("e8cdf5d8", stopped)+","+agentJSON("3c11e0f2", running)+"]")
		e.setRespawned(t, "["+agentJSON("e8cdf5d8", stopped)+","+agentJSON("3c11e0f2", running)+"]")
		require.Equal(t, exitOK, e.run(t, "restore"), "stderr: %s", e.stderr.String())
		assert.Equal(t, "restored  a-login  e8cdf5d8\nrunning   b-docs   3c11e0f2\n", e.stdout.String())
		assert.Empty(t, e.stderr.String())
		assert.Equal(t, []string{"respawn_e8cdf5d8"}, e.calls(t))
		require.Equal(t, exitOK, e.run(t, "restore"), "stderr: %s", e.stderr.String())
		assert.Equal(t, "running  a-login  e8cdf5d8\nrunning  b-docs   3c11e0f2\n", e.stdout.String())
		assert.Equal(t, []string{"respawn_e8cdf5d8"}, e.calls(t))
	})
	t.Run("end to end: a dry run respawns nothing", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+stoppedAgentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "--json", "restore", "--dry-run", "e8cdf5d8"), "stderr: %s", e.stderr.String())
		assert.JSONEq(t, `{"workspaces":[{"id":"fix-login","session":"e8cdf5d8","state":"stopped","restored":false,"error":null}],"warnings":[]}`, e.stdout.String())
		assert.Empty(t, e.calls(t))
	})
	t.Run("end to end: a session in another directory is refused", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		e.start(t, "fix-login", "71d1ce51")
		e.setAgents(t, "["+stoppedAgentJSON("71d1ce51", e.scratch)+"]")
		require.Equal(t, exitRefused, e.run(t, "--json", "restore"))
		assert.Contains(t, e.stdout.String(), `"code":"session_mismatch"`)
		assert.Empty(t, e.calls(t))
	})
	t.Run("end to end: a session Claude Code doesn't list is left alone", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.MkdirAll(filepath.Join(e.jobs, "e8cdf5d8"), 0o750))
		require.Equal(t, exitOK, e.run(t, "--json", "restore", "fix-login"), "stderr: %s", e.stderr.String())
		assert.JSONEq(t, `{"workspaces":[{"id":"fix-login","session":"e8cdf5d8","state":"unknown","restored":false,"error":null}],"warnings":[]}`, e.stdout.String())
		assert.Empty(t, e.calls(t))
		assert.DirExists(t, path)
	})
	t.Run("end to end: a named workspace that doesn't exist", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		require.Equal(t, exitNotFound, e.run(t, "restore", "nope"))
		assert.Equal(t, "ws: workspace not found: \"nope\"\n", e.stderr.String())
		assert.Empty(t, e.calls(t))
	})
}

func TestApp_restoreItems(t *testing.T) {
	t.Run("only workspaces found are passed on", func(t *testing.T) {
		e := newRestoreApp("a")
		items, err := e.app.restoreItems(t.Context(), &restoreOptions{workspaces: []string{"missing", "a"}})
		require.NoError(t, err)
		require.Len(t, items, 2)
		require.ErrorIs(t, items[0].err, workspace.ErrNotFound)
		assert.True(t, items[1].r.Respawned)
		require.Len(t, e.lifetime.RestoreCalls()[0].Opts.Workspaces, 1)
	})
	t.Run("every one missing", func(t *testing.T) {
		e := newRestoreApp()
		items, err := e.app.restoreItems(t.Context(), &restoreOptions{workspaces: []string{"missing"}})
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Empty(t, e.lifetime.RestoreCalls()[0].Opts.Workspaces)
	})
}

func TestMergeRestored(t *testing.T) {
	t.Run("fills the items that found a workspace, in order", func(t *testing.T) {
		items := []restoreItem{{arg: "a"}, {arg: "missing", err: errTest}, {arg: "b"}}
		got, err := mergeRestored(items, []lifetime.Restored{restoredAs("a", lifetime.StateIdle, false), {SessionID: "s-b", Err: lifetime.ErrBusy}})
		require.NoError(t, err)
		assert.Equal(t, "s-a", got[0].r.SessionID)
		assert.Equal(t, errTest, got[1].err)
		assert.ErrorIs(t, got[2].err, lifetime.ErrBusy)
	})
	t.Run("too few results", func(t *testing.T) {
		_, err := mergeRestored([]restoreItem{{arg: "a"}}, nil)
		assert.Error(t, err)
	})
}

func TestApp_restoreArgs(t *testing.T) {
	t.Run("a workspace named twice is restored once", func(t *testing.T) {
		e := newRestoreApp("a")
		items := e.app.restoreArgs([]string{"a", "a"})
		assert.Len(t, items, 1)
	})
	t.Run("each missing argument is reported", func(t *testing.T) {
		e := newRestoreApp()
		items := e.app.restoreArgs([]string{"x", "y"})
		assert.Len(t, items, 2)
	})
}

func TestApp_restoreArg(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		e := newRestoreApp("a")
		it := e.app.restoreArg("a")
		require.NoError(t, it.err)
		assert.Equal(t, "a", it.r.Workspace.ID)
	})
	t.Run("not found", func(t *testing.T) {
		e := newRestoreApp()
		it := e.app.restoreArg("a")
		assert.ErrorIs(t, it.err, workspace.ErrNotFound)
	})
	t.Run("removed by the removal pass just before", func(t *testing.T) {
		e := newRestoreApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "a", Path: "/cfg/workspaces/a"}, SessionID: "e8cdf5d8"}}
		it := e.app.restoreArg("e8cdf5d8")
		require.ErrorIs(t, it.err, workspace.ErrNotFound)
		assert.EqualError(t, it.err, "workspace not found: a was removed just now, as its Claude Code session is gone")
	})
}

func TestRestoreItem_id(t *testing.T) {
	t.Run("the workspace's", func(t *testing.T) {
		it := restoreItem{arg: "/path/a", r: lifetime.Restored{Workspace: workspace.Workspace{ID: "a"}}}
		assert.Equal(t, "a", it.id())
	})
	t.Run("the argument without one", func(t *testing.T) {
		it := restoreItem{arg: "missing"}
		assert.Equal(t, "missing", it.id())
	})
}

func TestRestoreItem_verb(t *testing.T) {
	verb := func(state lifetime.State, respawned, dryRun bool) string {
		it := restoreItem{r: lifetime.Restored{State: state, Respawned: respawned}}
		return it.verb(dryRun)
	}
	t.Run("restored", func(t *testing.T) {
		assert.Equal(t, "restored", verb(lifetime.StateStopped, true, false))
	})
	t.Run("would restore", func(t *testing.T) {
		assert.Equal(t, "would restore", verb(lifetime.StateStopped, false, true))
	})
	t.Run("idle is running", func(t *testing.T) {
		assert.Equal(t, "running", verb(lifetime.StateIdle, false, false))
	})
	t.Run("working is running", func(t *testing.T) {
		assert.Equal(t, "running", verb(lifetime.StateWorking, false, true))
	})
	t.Run("starting", func(t *testing.T) {
		assert.Equal(t, "starting", verb(lifetime.StateStarting, false, false))
	})
	t.Run("unknown", func(t *testing.T) {
		assert.Equal(t, "unknown", verb(lifetime.StateUnknown, false, false))
	})
}

func TestRestoreItem_json(t *testing.T) {
	t.Run("no state or session is null", func(t *testing.T) {
		it := restoreItem{arg: "missing"}
		assert.Equal(t, restoreJSON{ID: "missing"}, it.json(nil))
	})
	t.Run("a restored session", func(t *testing.T) {
		it := restoreItem{r: restoredAs("a", lifetime.StateStopped, true)}
		got := it.json(nil)
		require.NotNil(t, got.State)
		assert.Equal(t, lifetime.StateStopped, *got.State)
		require.NotNil(t, got.Session)
		assert.Equal(t, "s-a", *got.Session)
		assert.True(t, got.Restored)
	})
}

func TestRestoreWidths(t *testing.T) {
	t.Run("over the items printed", func(t *testing.T) {
		items := []restoreItem{
			{r: restoredAs("a", lifetime.StateStopped, true)},
			{arg: "a-much-longer-missing-one", err: errTest},
			{r: restoredAs("bbb", lifetime.StateStopped, false)},
		}
		verbs, ids := restoreWidths(items, true)
		assert.Equal(t, len("would restore"), verbs)
		assert.Equal(t, 3, ids)
	})
}
