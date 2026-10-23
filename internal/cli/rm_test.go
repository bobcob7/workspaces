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
	e.lifetime.RemoveFunc = func(_ context.Context, ws *workspace.Workspace, _ bool) (lifetime.Removed, error) {
		return lifetime.Removed{Workspace: *ws, SessionID: "s-" + ws.ID}, nil
	}
	return e
}

func TestApp_newRmCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRmCmd()
	t.Run("has --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
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
		require.Len(t, e.lifetime.RemoveCalls(), 1)
		assert.True(t, e.lifetime.RemoveCalls()[0].DryRun)
	})
}

func TestApp_runRm(t *testing.T) {
	t.Run("removes each workspace and prints it with its session", func(t *testing.T) {
		e := newRmApp("a", "b")
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a", "b"}}))
		assert.Equal(t, "removed  a  s-a\nremoved  b  s-b\n", e.stdout.String())
		require.Len(t, e.lifetime.RemoveCalls(), 2)
		assert.False(t, e.lifetime.RemoveCalls()[0].DryRun)
	})
	t.Run("--json", func(t *testing.T) {
		e := newRmApp("a")
		e.app.global.json = true
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a","removed":true,"error":null}],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--dry-run", func(t *testing.T) {
		e := newRmApp("a")
		e.app.global.json = true
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.JSONEq(t, `{"workspaces":[{"id":"a","removed":false,"error":null}],"warnings":[]}`, e.stdout.String())
		assert.True(t, e.lifetime.RemoveCalls()[0].DryRun)
	})
	t.Run("--dry-run text", func(t *testing.T) {
		e := newRmApp("a")
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}, dryRun: true}))
		assert.Equal(t, "would remove  a  s-a\n", e.stdout.String())
	})
	t.Run("a failure doesn't stop the others; the exit code is the first failure's", func(t *testing.T) {
		e := newRmApp("busy", "running", "ok")
		e.lifetime.RemoveFunc = func(_ context.Context, ws *workspace.Workspace, _ bool) (lifetime.Removed, error) {
			switch ws.ID {
			case "busy":
				return lifetime.Removed{}, lifetime.ErrBusy
			case "running":
				return lifetime.Removed{}, lifetime.ErrShellRunning
			}
			return lifetime.Removed{Workspace: *ws}, nil
		}
		e.app.global.json = true
		err := e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"missing", "busy", "running", "ok"}})
		requireCode(t, codeWorkspaceNotFound, err)
		assert.JSONEq(t, `{"workspaces":[
			{"id":"missing","removed":false,"error":{"code":"workspace_not_found","message":"workspace not found"}},
			{"id":"busy","removed":false,"error":{"code":"workspace_busy","message":"the workspace is still being created or handed to its session"}},
			{"id":"running","removed":false,"error":{"code":"shell_running","message":"the workspace's shell is still running; exit it to remove the workspace"}},
			{"id":"ok","removed":true,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("failures go to stderr as text", func(t *testing.T) {
		e := newRmApp("a")
		e.lifetime.RemoveFunc = func(context.Context, *workspace.Workspace, bool) (lifetime.Removed, error) {
			return lifetime.Removed{}, &claude.CommandError{Args: []string{"rm", "s-a"}, ExitCode: 1, Stderr: "nope", Err: errTest}
		}
		requireCode(t, codeClaudeFailed, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"a"}}))
		assert.Empty(t, e.stdout.String())
		assert.Equal(t, "ws: claude rm s-a: boom: nope\n", e.stderr.String())
	})
	t.Run("a workspace the removal pass just removed counts as removed", func(t *testing.T) {
		e := newRmApp()
		e.app.swept = []lifetime.Removed{{Workspace: workspace.Workspace{ID: "a", Path: "/cfg/workspaces/a"}, SessionID: "e8cdf5d8"}}
		require.NoError(t, e.app.runRm(t.Context(), &rmOptions{workspaces: []string{"e8cdf5d8"}, dryRun: true}))
		assert.Equal(t, "removed  a  e8cdf5d8\n", e.stdout.String())
		assert.Empty(t, e.lifetime.RemoveCalls())
	})
	t.Run("end to end: an agent workspace by its session id", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "e8cdf5d8"), "stderr: %s", e.stderr.String())
		assert.Equal(t, "removed  fix-login  e8cdf5d8\n", e.stdout.String())
		assert.Equal(t, []string{"stop_e8cdf5d8", "rm_e8cdf5d8"}, e.calls(t))
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, filepath.Join(e.paths.State, "fix-login"))
	})
	t.Run("end to end: removing an agent takes back the trust ws added for its repo", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.Equal(t, []string{e.paths.Workspaces, filepath.Join(path, "n")}, trustedDirsIn(t, e.claudeJSON))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", filepath.Join(path, "n"))+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "fix-login"), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: a repo trusted before ws stays trusted", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		repoDir := filepath.Join(e.paths.Workspaces, "fix-login", "n")
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces, repoDir)
		e.start(t, "fix-login", "e8cdf5d8")
		assert.NoFileExists(t, filepath.Join(e.paths.State, "fix-login", "trust"), "ws added nothing")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", repoDir)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "fix-login"), "stderr: %s", e.stderr.String())
		assert.Equal(t, []string{e.paths.Workspaces, repoDir}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: an unrecorded workspace's trust goes with it", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.Remove(filepath.Join(e.paths.State, "fix-login", "session")))
		require.Equal(t, exitOK, e.run(t, "rm", "fix-login"), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.Equal(t, []string{e.paths.Workspaces}, trustedDirsIn(t, e.claudeJSON))
	})
	t.Run("end to end: trust that can't be taken back is a warning, not a kept workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		writeClaudeJSON(t, e.claudeJSON, e.paths.Workspaces)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.WriteFile(e.claudeJSON, []byte("{"), 0o600))
		require.Equal(t, exitOK, e.run(t, "rm", "fix-login"), "stderr: %s", e.stderr.String())
		assert.NoDirExists(t, path)
		assert.NoDirExists(t, filepath.Join(e.paths.State, "fix-login"))
		assert.Contains(t, e.stderr.String(), "couldn't remove the Claude Code trust ws added for "+filepath.Join(path, "n"))
	})
	t.Run("end to end: a dry run changes nothing", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitOK, e.run(t, "rm", "--dry-run", path))
		assert.Equal(t, "would remove  fix-login  e8cdf5d8\n", e.stdout.String())
		assert.Empty(t, e.calls(t))
		assert.DirExists(t, path)
	})
	t.Run("end to end: a session listed in another directory keeps both", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", e.scratch)+"]")
		assert.Equal(t, exitRefused, e.run(t, "--json", "rm", "fix-login"))
		assert.Contains(t, e.stdout.String(), `"code":"session_mismatch"`)
		assert.Empty(t, e.calls(t))
		assert.DirExists(t, path)
	})
	t.Run("end to end: a session Claude Code no longer lists is already gone", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.MkdirAll(filepath.Join(e.jobs, "e8cdf5d8"), 0o750))
		require.Equal(t, exitOK, e.run(t, "rm", "fix-login"), "stderr: %s", e.stderr.String())
		assert.Equal(t, []string{"stop_e8cdf5d8", "rm_e8cdf5d8"}, e.calls(t))
		assert.NoDirExists(t, path)
	})
	t.Run("end to end: claude failing keeps the workspace", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "calls"), nil, 0o400))
		assert.Equal(t, exitFailed, e.run(t, "--json", "rm", "fix-login"))
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

func TestApp_rmOne(t *testing.T) {
	t.Run("a failed lookup", func(t *testing.T) {
		e := newRmApp()
		_, err := e.app.rmOne(t.Context(), &rmTarget{err: errTest}, false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, e.lifetime.RemoveCalls())
	})
	t.Run("already removed by the pass", func(t *testing.T) {
		e := newRmApp()
		r := lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}
		got, err := e.app.rmOne(t.Context(), &rmTarget{swept: &r}, false)
		require.NoError(t, err)
		assert.Equal(t, r, got)
		assert.Empty(t, e.lifetime.RemoveCalls())
	})
	t.Run("removes", func(t *testing.T) {
		e := newRmApp()
		got, err := e.app.rmOne(t.Context(), &rmTarget{ws: workspace.Workspace{ID: "a"}}, true)
		require.NoError(t, err)
		assert.Equal(t, "s-a", got.SessionID)
		require.Len(t, e.lifetime.RemoveCalls(), 1)
		assert.True(t, e.lifetime.RemoveCalls()[0].DryRun)
	})
}

func TestRmLine(t *testing.T) {
	t.Run("an agent", func(t *testing.T) {
		assert.Equal(t, "removed  a  e8cdf5d8", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}, SessionID: "e8cdf5d8"}, true))
	})
	t.Run("without a session id", func(t *testing.T) {
		assert.Equal(t, "removed  a", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}, true))
	})
	t.Run("not removed", func(t *testing.T) {
		assert.Equal(t, "would remove  a", rmLine(&lifetime.Removed{Workspace: workspace.Workspace{ID: "a"}}, false))
	})
}
