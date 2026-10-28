package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newRmApp is a test App whose workspace store finds workspaces by id,
// and whose lifetime manager removes each with session "s-<id>".
func newRmApp(ids ...string) *mockedApp {
	e := newMockedApp()
	known := map[string]bool{}
	for _, id := range ids {
		known[id] = true
	}
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		if known[arg] {
			return workspace.Workspace{ID: arg, Path: "/cfg/workspaces/" + arg}, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	removeEach(e, func(ws *workspace.Workspace) (lifetime.Removed, error) {
		return lifetime.Removed{Workspace: *ws, SessionID: "s-" + ws.ID}, nil
	})
	return e
}

// removeEach makes e's lifetime manager remove each workspace alone, as
// remove says.
func removeEach(e *mockedApp, remove func(ws *workspace.Workspace) (lifetime.Removed, error)) {
	e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, _ lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
		r, err := remove(ws)
		if err != nil {
			r = lifetime.Removed{Workspace: *ws}
		}
		return []lifetime.TreeItem{{Removed: r, Err: err}}, nil
	}
}

func TestApp_newRmCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRmCmd()
	t.Run("has --dry-run and --keep-children", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
		assertFlag(t, cmd, "keep-children", "false")
	})
	t.Run("needs a workspace", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("runs the removal pass, then removes", func(t *testing.T) {
		e := newRmApp("fix-login")
		cmd := e.app.newRmCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--dry-run", "fix-login"}))
		require.NoError(t, cmd.RunE(cmd, cmd.Flags().Args()))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		require.Len(t, e.lifetime.RemoveTreeCalls(), 1)
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
	})
}

func TestApp_runRm(t *testing.T) {
	t.Run("removes each workspace and prints it with its session", func(t *testing.T) {
		e := newRmApp("a", "b")
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a", "b"}}))
		assert.Equal(t, "removed  a  s-a\nremoved  b  s-b\n", e.stdout.String())
		require.Len(t, e.lifetime.RemoveTreeCalls(), 2)
		assert.False(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
	})
	t.Run("--json", func(t *testing.T) {
		e := newRmApp("a")
		e.app.global.json = true
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a","parent":null,"removed":true,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--dry-run", func(t *testing.T) {
		e := newRmApp("a")
		e.app.global.json = true
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a","parent":null,"removed":false,"error":null}],"warnings":[]}`, e.stdout.String())
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.DryRun)
	})
	t.Run("--dry-run text", func(t *testing.T) {
		e := newRmApp("a")
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.Equal(t, "would remove  a  s-a\n", e.stdout.String())
	})
	t.Run("a failure doesn't stop the others; the exit code is the first failure's", func(t *testing.T) {
		e := newRmApp("busy", "running", "ok")
		removeEach(e, func(ws *workspace.Workspace) (lifetime.Removed, error) {
			switch ws.ID {
			case "busy":
				return lifetime.Removed{}, lifetime.ErrBusy
			case "running":
				return lifetime.Removed{}, lifetime.ErrShellRunning
			}
			return lifetime.Removed{Workspace: *ws}, nil
		})
		e.app.global.json = true
		err := e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"missing", "busy", "running", "ok"}})
		requireCode(t, codeWorkspaceNotFound, err)
		assert.JSONEq(t, `{"workspaces":[
			{"id":"missing","parent":null,"removed":false,"error":{"code":"workspace_not_found","message":"workspace not found"}},
			{"id":"busy","parent":null,"removed":false,"error":{"code":"workspace_busy","message":"the workspace is still being created or handed to its session"}},
			{"id":"running","parent":null,"removed":false,"error":{"code":"shell_running","message":"the workspace's shell is still running; exit it to remove the workspace"}},
			{"id":"ok","parent":null,"removed":true,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("failures go to stderr as text", func(t *testing.T) {
		e := newRmApp("a")
		removeEach(e, func(*workspace.Workspace) (lifetime.Removed, error) {
			return lifetime.Removed{}, &claude.CommandError{Args: []string{"rm", "s-a"}, ExitCode: 1, Stderr: "nope", Err: errTest}
		})
		requireCode(t, codeClaudeFailed, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}}))
		assert.Empty(t, e.stdout.String())
		assert.Equal(t, "ws: claude rm s-a: boom: nope\n", e.stderr.String())
	})
	t.Run("descendants are items too, marked in the text", func(t *testing.T) {
		e := newRmApp("root")
		e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, _ lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			child := workspace.Workspace{ID: "child", Parent: ws.ID}
			return []lifetime.TreeItem{{Workspace: child, SessionID: "s-child"}, {Workspace: *ws, SessionID: "s-root"}}, nil
		}
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root"}}))
		assert.Equal(t, "removed  └ child  s-child\nremoved  root  s-root\n", e.stdout.String())
		e.stdout.Reset()
		e.app.global.json = true
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root"}}))
		assert.JSONEq(t, `{"workspaces":[{"id":"child","parent":"root","removed":true,"error":null},{"id":"root","parent":null,"removed":true,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a workspace in an earlier tree is reported once", func(t *testing.T) {
		e := newRmApp("root", "child")
		e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, _ lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			if ws.ID == "child" {
				return []lifetime.TreeItem{{Workspace: *ws}}, nil
			}
			child := workspace.Workspace{ID: "child", Parent: ws.ID}
			return []lifetime.TreeItem{{Workspace: child}, {Workspace: *ws}}, nil
		}
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root", "child"}}))
		assert.Equal(t, "removed  └ child\nremoved  root\n", e.stdout.String())
		assert.Len(t, e.lifetime.RemoveTreeCalls(), 1)
	})
	t.Run("a kept child is refused", func(t *testing.T) {
		e := newRmApp("root")
		e.lifetime.RemoveTreeFunc = func(_ context.Context, ws *workspace.Workspace, _ lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			child := workspace.Workspace{ID: "sh", Parent: ws.ID}
			return []lifetime.TreeItem{
				{Workspace: child, Err: lifetime.ErrShellRunning},
				{Workspace: *ws, Err: fmt.Errorf("%w: sh", lifetime.ErrChildKept)},
			}, nil
		}
		e.app.global.json = true
		requireCode(t, codeShellRunning, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root"}}))
		assert.Contains(t, e.stdout.String(), `"code":"child_kept"`)
	})
	t.Run("--keep-children is passed on", func(t *testing.T) {
		e := newRmApp("root")
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root"}, keepChildren: true}))
		assert.True(t, e.lifetime.RemoveTreeCalls()[0].Opts.KeepChildren)
	})
	t.Run("a tree that can't be read is the workspace's error", func(t *testing.T) {
		e := newRmApp("root")
		e.lifetime.RemoveTreeFunc = func(context.Context, *workspace.Workspace, lifetime.TreeOptions) ([]lifetime.TreeItem, error) {
			return nil, errTest
		}
		assert.ErrorIs(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"root"}}), errTest)
	})
	t.Run("a workspace the removal pass just removed counts as removed", func(t *testing.T) {
		e := newRmApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "a", Path: "/cfg/workspaces/a"}, SessionID: "e8cdf5d8"}}
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"e8cdf5d8"}, dryRun: true}))
		assert.Equal(t, "removed  a  e8cdf5d8\n", e.stdout.String())
		assert.Empty(t, e.lifetime.RemoveTreeCalls())
	})
	t.Run("end to end: an agent workspace by its session id", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "e8cdf5d8"), "stderr: %s", e.stderr.String())
		assert.Equal(t, "removed  "+filepath.Base(path)+"  e8cdf5d8\n", e.stdout.String())
		assert.Equal(t, []string{"stop_e8cdf5d8", "rm_e8cdf5d8"}, e.calls(t))
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, filepath.Join(e.paths.State, filepath.Base(path)))
	})
	t.Run("end to end: removing an agent takes back the trust ws added for its repo", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.Equal(t, []string{e.paths.Workspaces, filepath.Join(path, "n")}, trustedDirsIn(t, e.claudeJSON))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", filepath.Join(path, "n"))+"]")
		require.Equal(t, exitOK, e.run(t, "rm", filepath.Base(path)), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: a repo trusted before ws stays trusted", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		repoDir := filepath.Join(path, "n")
		// As if the repo's directory was trusted before ws trusted it: ws
		// records only trust it added.
		require.NoError(t, os.Remove(filepath.Join(e.paths.State, filepath.Base(path), "trust")))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", repoDir)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", filepath.Base(path)), "stderr: %s", e.stderr.String())
		assert.Equal(t, []string{e.paths.Workspaces, repoDir}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: an unrecorded workspace's trust goes with it", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.Remove(filepath.Join(e.paths.State, filepath.Base(path), "session")))
		require.Equal(t, exitOK, e.run(t, "rm", filepath.Base(path)), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: trust that can't be taken back is a warning, not a kept workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.WriteFile(e.claudeJSON, []byte("{"), 0o600))
		require.Equal(t, exitOK, e.run(t, "rm", filepath.Base(path)), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, filepath.Join(e.paths.State, filepath.Base(path)))
		assert.Contains(t, e.stderr.String(), "couldn't remove the Claude Code trust ws added for "+filepath.Join(path, "n"))
	})
	t.Run("end to end: a dry run changes nothing", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "--dry-run", path))
		assert.Equal(t, "would remove  "+filepath.Base(path)+"  e8cdf5d8\n", e.stdout.String())
		assert.Empty(t, e.calls(t))
		assert.DirExists(t, path)
	})
	t.Run("end to end: a session listed in another directory keeps both", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", e.scratch)+"]")
		assert.Equal(t, exitRefused, e.run(t, "--json", "rm", filepath.Base(path)))
		assert.Contains(t, e.stdout.String(), `"code":"session_mismatch"`)
		assert.Empty(t, e.calls(t))
		assert.DirExists(t, path)
	})
	t.Run("end to end: a session Claude Code no longer lists is already gone", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.MkdirAll(filepath.Join(e.jobs, "e8cdf5d8"), 0o750))
		require.Equal(t, exitOK, e.run(t, "rm", filepath.Base(path)), "stderr: %s", e.stderr.String())
		assert.Equal(t, []string{"stop_e8cdf5d8", "rm_e8cdf5d8"}, e.calls(t))
		assert.NoDirExists(t, path)
	})
	t.Run("end to end: claude failing keeps the workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "calls"), nil, 0o400))
		assert.Equal(t, exitFailed, e.run(t, "--json", "rm", filepath.Base(path)))
		assert.Contains(t, e.stdout.String(), `"code":"claude_failed"`)
		assert.DirExists(t, path)
	})
}

func TestApp_rmTargets(t *testing.T) {
	t.Run("one per workspace, in order", func(t *testing.T) {
		e := newRmApp("a", "b")
		targets := e.app.rmTargets([]string{"b", "a", "b"})
		require.Len(t, targets, 2)
		assert.Equal(t, "b", targets[0].ws.ID)
		assert.Equal(t, "a", targets[1].ws.ID)
	})
	t.Run("every failure is kept", func(t *testing.T) {
		e := newRmApp()
		assert.Len(t, e.app.rmTargets([]string{"x", "x"}), 2)
	})
}

func TestRmTarget_key(t *testing.T) {
	t.Run("a workspace", func(t *testing.T) {
		assert.Equal(t, "a", (&rmTarget{ws: workspace.Workspace{ID: "a"}}).key())
	})
	t.Run("one the pass removed", func(t *testing.T) {
		assert.Equal(t, "a", (&rmTarget{swept: &lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}}).key())
	})
	t.Run("a failure has none", func(t *testing.T) {
		assert.Empty(t, (&rmTarget{err: errTest}).key())
	})
}

func TestApp_rmTarget(t *testing.T) {
	t.Run("found", func(t *testing.T) {
		e := newRmApp("a")
		got := e.app.rmTarget("a")
		require.NoError(t, got.err)
		assert.Equal(t, "a", got.ws.ID)
	})
	t.Run("removed by the pass", func(t *testing.T) {
		e := newRmApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "a"}}}
		got := e.app.rmTarget("a")
		require.NotNil(t, got.swept)
		assert.Equal(t, "a", got.swept.Workspace.ID)
	})
	t.Run("not found", func(t *testing.T) {
		e := newRmApp()
		assert.ErrorIs(t, e.app.rmTarget("a").err, workspace.ErrNotFound)
	})
	t.Run("any other failure isn't matched against the pass", func(t *testing.T) {
		e := newRmApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "a"}}}
		e.store.FindFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, errTest }
		assert.ErrorIs(t, e.app.rmTarget("a").err, errTest)
	})
}

func TestApp_sweptMatch(t *testing.T) {
	e := newRmApp()
	e.app.swept = []lifetime.Removed{
		{Workspace: workspace.Workspace{ID: "shell", Path: "/cfg/workspaces/shell"}},
		{Workspace: workspace.Workspace{ID: "agent", Path: "/cfg/workspaces/agent"}, SessionID: "e8cdf5d8"},
	}
	t.Run("by id", func(t *testing.T) {
		r, ok := e.app.sweptMatch("agent")
		require.True(t, ok)
		assert.Equal(t, "agent", r.Workspace.ID)
	})
	t.Run("by path", func(t *testing.T) {
		r, ok := e.app.sweptMatch("/cfg/workspaces/shell/")
		require.True(t, ok)
		assert.Equal(t, "shell", r.Workspace.ID)
	})
	t.Run("by session id", func(t *testing.T) {
		r, ok := e.app.sweptMatch("e8cdf5d8")
		require.True(t, ok)
		assert.Equal(t, "agent", r.Workspace.ID)
	})
	t.Run("an empty session id matches nothing", func(t *testing.T) {
		_, ok := e.app.sweptMatch("")
		assert.False(t, ok)
	})
	t.Run("no match", func(t *testing.T) {
		_, ok := e.app.sweptMatch("other")
		assert.False(t, ok)
	})
}

func TestRmTarget_unremovable(t *testing.T) {
	t.Run("a failed lookup", func(t *testing.T) {
		_, err := (&rmTarget{err: errTest}).unremovable()
		require.ErrorIs(t, err, errTest)
	})
	t.Run("already removed by the pass", func(t *testing.T) {
		r := lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}
		got, err := (&rmTarget{swept: &r}).unremovable()
		require.NoError(t, err)
		assert.Equal(t, r, got)
	})
}

func TestRmLine(t *testing.T) {
	t.Run("an agent", func(t *testing.T) {
		assert.Equal(t, "removed  a  e8cdf5d8", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}, SessionID: "e8cdf5d8"}, true, false))
	})
	t.Run("without a session id", func(t *testing.T) {
		assert.Equal(t, "removed  a", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}, true, false))
	})
	t.Run("not removed", func(t *testing.T) {
		assert.Equal(t, "would remove  a", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}, false, false))
	})
	t.Run("a descendant", func(t *testing.T) {
		assert.Equal(t, "removed  └ a", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}, true, true))
	})
}
