package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lifetime"
)

// restartReturns makes the mocked lifetime manager's Restart record
// sessions, wait for those of them that are working, report results, and
// return err.
func restartReturns(e *mockedApp, sessions, results []lifetime.Restarted, err error) {
	e.lifetime.RestartFunc = func(_ context.Context, opts lifetime.RestartOptions) ([]lifetime.Restarted, error) {
		opts.Recorded(sessions)
		var working []lifetime.Restarted
		for _, s := range sessions {
			if s.Working {
				working = append(working, s)
			}
		}
		if len(working) > 0 {
			opts.Waiting(working)
		}
		for _, r := range results {
			opts.Done(r)
		}
		return results, err
	}
}

// restartedIn is session id of workspace ws ("" for none), restarted.
func restartedIn(ws, id string) lifetime.Restarted {
	return lifetime.Restarted{SessionID: id, Workspace: ws, Restarted: true}
}

// busyAgentJSON is agentJSON in the middle of a turn.
func busyAgentJSON(id, cwd string) string {
	return strings.Replace(agentJSON(id, cwd), `"status":"idle"`, `"status":"busy"`, 1)
}

// respawnedAgentJSON is agentJSON with the new process a respawn gives it.
func respawnedAgentJSON(id, cwd string) string {
	return strings.Replace(agentJSON(id, cwd), `"pid":4242`, `"pid":5151`, 1)
}

func TestApp_newRestartCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRestartCmd()
	t.Run("has --no-wait and --force", func(t *testing.T) {
		assertFlag(t, cmd, "no-wait", "false")
		assertFlag(t, cmd, "force", "false")
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("runs the removal pass, then restarts with the options", func(t *testing.T) {
		e := newMockedApp()
		cmd := e.app.newRestartCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.ParseFlags([]string{"--force"}))
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		require.Len(t, e.lifetime.RestartCalls(), 1)
		opts := e.lifetime.RestartCalls()[0].Opts
		assert.True(t, opts.Force)
		assert.False(t, opts.NoWait)
	})
	t.Run("--no-wait and --force together are a usage error", func(t *testing.T) {
		e := newMockedApp()
		assert.Equal(t, exitUsage, e.app.Run(t.Context(), []string{"restart", "--no-wait", "--force"}))
		assert.Equal(t, "ws: --no-wait and --force can't be used together\n", e.stderr.String())
		assert.Empty(t, e.lifetime.SweepCalls())
		assert.Empty(t, e.lifetime.RestartCalls())
	})
}

func TestApp_runRestart(t *testing.T) {
	t.Run("prints each session as it is restarted", func(t *testing.T) {
		e := newMockedApp()
		waiting := lifetime.Restarted{SessionID: "5d8d652c", Workspace: "ws-20261018-k3f9qa", Working: true}
		restartReturns(e,
			[]lifetime.Restarted{{SessionID: "e8cdf5d8", Workspace: "fix-login"}, {SessionID: "7a40c2d9"}, waiting},
			[]lifetime.Restarted{restartedIn("fix-login", "e8cdf5d8"), restartedIn("", "7a40c2d9"), restartedIn("ws-20261018-k3f9qa", "5d8d652c")}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.Equal(t, "restarted  fix-login           e8cdf5d8\n"+
			"restarted  -                   7a40c2d9\n"+
			"restarted  ws-20261018-k3f9qa  5d8d652c\n", e.stdout.String())
		assert.Empty(t, e.stderr.String())
	})
	t.Run("shows progress on a terminal", func(t *testing.T) {
		e := newMockedApp()
		e.app.progress = true
		waiting := lifetime.Restarted{SessionID: "5d8d652c", Workspace: "k3f9qa", Working: true}
		restartReturns(e, []lifetime.Restarted{waiting}, []lifetime.Restarted{restartedIn("k3f9qa", "5d8d652c")}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.Equal(t, "ws: restarting Claude Code's background service and its 1 running background sessions, including any ws didn't start\n"+
			"waiting    k3f9qa  5d8d652c  working\n", e.stderr.String())
		assert.Equal(t, "restarted  k3f9qa  5d8d652c\n", e.stdout.String())
	})
	t.Run("no progress with --quiet", func(t *testing.T) {
		e := newMockedApp()
		e.app.progress, e.app.global.quiet = true, true
		waiting := lifetime.Restarted{SessionID: "5d8d652c", Working: true}
		restartReturns(e, []lifetime.Restarted{waiting}, []lifetime.Restarted{restartedIn("", "5d8d652c")}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.Empty(t, e.stderr.String())
	})
	t.Run("--no-wait, a stale and a gone session", func(t *testing.T) {
		e := newMockedApp()
		stale := lifetime.Restarted{SessionID: "5d8d652c", Workspace: "k3f9qa", Working: true}
		gone := lifetime.Restarted{SessionID: "3c11e0f2", Gone: true}
		restartReturns(e, []lifetime.Restarted{stale, gone}, []lifetime.Restarted{gone, stale}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{noWait: true}))
		assert.Equal(t, "gone       -       3c11e0f2\n"+
			"stale      k3f9qa  5d8d652c  working; run `ws restart` again later\n", e.stdout.String())
		assert.True(t, e.lifetime.RestartCalls()[0].Opts.NoWait)
	})
	t.Run("--json", func(t *testing.T) {
		e := newMockedApp()
		e.app.global.json = true
		stale := lifetime.Restarted{SessionID: "5d8d652c", Workspace: "k3f9qa", Working: true}
		restartReturns(e, []lifetime.Restarted{stale}, []lifetime.Restarted{restartedIn("fix-login", "e8cdf5d8"), restartedIn("", "7a40c2d9"), stale}, nil)
		e.lifetime.WarningsFunc = func() []string { return []string{"w"} }
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{noWait: true}))
		assert.JSONEq(t, `{"sessions":[
			{"id":"e8cdf5d8","workspace":"fix-login","outcome":"restarted","restarted":true,"error":null},
			{"id":"7a40c2d9","workspace":null,"outcome":"restarted","restarted":true,"error":null},
			{"id":"5d8d652c","workspace":"k3f9qa","outcome":"stale","restarted":false,"error":null}
		],"warnings":["w"]}`, e.stdout.String())
		assert.Empty(t, e.stderr.String())
	})
	t.Run("--json tells a gone session from a stale one", func(t *testing.T) {
		e := newMockedApp()
		e.app.global.json = true
		restartReturns(e, nil, []lifetime.Restarted{{SessionID: "s1", Gone: true}, {SessionID: "s2", Working: true}}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{noWait: true}))
		assert.JSONEq(t, `{"sessions":[
			{"id":"s1","workspace":null,"outcome":"gone","restarted":false,"error":null},
			{"id":"s2","workspace":null,"outcome":"stale","restarted":false,"error":null}
		],"warnings":[]}`, e.stdout.String())
	})
	t.Run("--json with nothing running", func(t *testing.T) {
		e := newMockedApp()
		e.app.global.json = true
		restartReturns(e, nil, nil, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.JSONEq(t, `{"sessions":[],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a failed respawn is claude_failed, on stderr as text, and the others still print", func(t *testing.T) {
		e := newMockedApp()
		failed := lifetime.Restarted{SessionID: "s1", Err: fmt.Errorf("respawning Claude Code session s1: %w", &claude.CommandError{Args: []string{"respawn", "s1"}, ExitCode: 1, Stderr: "nope", Err: errTest})}
		restartReturns(e, nil, []lifetime.Restarted{failed, restartedIn("", "s2")}, nil)
		err := e.app.runRestart(t.Context(), &restartOptions{})
		requireCode(t, codeClaudeFailed, err)
		assert.True(t, asCLIError(err).reported)
		assert.Equal(t, "restarted  -  s2\n", e.stdout.String())
		assert.Equal(t, "ws: respawning Claude Code session s1: claude respawn s1: boom: nope\n", e.stderr.String())
	})
	t.Run("a failure before any respawn prints nothing on stdout", func(t *testing.T) {
		e := newMockedApp()
		restartReturns(e, nil, nil, fmt.Errorf("%w: `claude daemon stop` failed: %w", lifetime.ErrServiceFailed, &claude.CommandError{Args: []string{"daemon", "stop"}, ExitCode: 1, Stderr: "nope", Err: errTest}))
		err := e.app.runRestart(t.Context(), &restartOptions{})
		requireCode(t, codeClaudeFailed, err)
		assert.False(t, asCLIError(err).reported)
		assert.Equal(t, map[string]any{"stderr": "nope"}, asCLIError(err).details)
		assert.Empty(t, e.stdout.String())
	})
	t.Run("claude missing", func(t *testing.T) {
		e := newMockedApp()
		restartReturns(e, nil, nil, claude.ErrNotInstalled)
		requireCode(t, codeClaudeMissing, e.app.runRestart(t.Context(), &restartOptions{}))
	})
	t.Run("a session left stale by a service failure carries the failure, and the others still print", func(t *testing.T) {
		e := newMockedApp()
		stale := lifetime.Restarted{SessionID: "s2", Working: true, Err: fmt.Errorf("%w, so session s2, which was working, was left stale: run ws restart again", lifetime.ErrServiceFailed)}
		restartReturns(e, nil, []lifetime.Restarted{restartedIn("", "s1"), stale}, nil)
		err := e.app.runRestart(t.Context(), &restartOptions{})
		requireCode(t, codeClaudeFailed, err)
		assert.True(t, asCLIError(err).reported)
		assert.Equal(t, "restarted  -  s1\n", e.stdout.String())
		assert.Equal(t, "ws: couldn't replace Claude Code's background service, so session s2, which was working, was left stale: run ws restart again\n", e.stderr.String())
	})
	t.Run("interrupted, the sessions left carry the interruption with --json", func(t *testing.T) {
		e := newMockedApp()
		e.app.global.json = true
		stale := lifetime.Restarted{SessionID: "s1", Err: fmt.Errorf("interrupted before session s1 was restarted: %w", context.Canceled)}
		restartReturns(e, nil, []lifetime.Restarted{restartedIn("", "s2"), stale}, nil)
		err := e.app.runRestart(t.Context(), &restartOptions{})
		requireCode(t, codeInternal, err)
		assert.True(t, asCLIError(err).reported)
		assert.JSONEq(t, `{"sessions":[
			{"id":"s2","workspace":null,"outcome":"restarted","restarted":true,"error":null},
			{"id":"s1","workspace":null,"outcome":"stale","restarted":false,"error":{"code":"internal","message":"interrupted before session s1 was restarted: context canceled"}}
		],"warnings":[]}`, e.stdout.String())
		assert.Empty(t, e.stderr.String())
	})
	t.Run("an idle session left stale says to run ws restart again", func(t *testing.T) {
		e := newMockedApp()
		restartReturns(e, nil, []lifetime.Restarted{{SessionID: "s1"}}, nil)
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.Equal(t, "stale      -  s1  run `ws restart` again later\n", e.stdout.String())
	})
	t.Run("warnings follow the sessions", func(t *testing.T) {
		e := newMockedApp()
		restartReturns(e, nil, []lifetime.Restarted{restartedIn("", "s1")}, nil)
		e.lifetime.WarningsFunc = func() []string { return []string{"not ws's service"} }
		require.NoError(t, e.app.runRestart(t.Context(), &restartOptions{}))
		assert.Equal(t, "ws: warning: not ws's service\n", e.stderr.String())
	})
	t.Run("end to end: replaces the service and respawns every running session", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "daemon-pid"), []byte("1\n"), 0o600))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+","+agentJSON("7a40c2d9", e.scratch)+","+stoppedAgentJSON("3c11e0f2", e.scratch)+"]")
		e.setRespawned(t, "["+respawnedAgentJSON("e8cdf5d8", path)+","+respawnedAgentJSON("7a40c2d9", e.scratch)+","+stoppedAgentJSON("3c11e0f2", e.scratch)+"]")
		require.Equal(t, exitOK, e.run(t, "restart"), "stderr: %s", e.stderr.String())
		assert.Equal(t, "restarted  fix-login  e8cdf5d8\nrestarted  -          7a40c2d9\n", e.stdout.String())
		assert.Empty(t, e.stderr.String())
		assert.Equal(t, []string{"daemon_stop", "daemon_run", "respawn_e8cdf5d8", "respawn_7a40c2d9"}, e.calls(t))
		takeLock(t, filepath.Join(e.paths.State, ".respawn.lock")).Unlock()
	})
	t.Run("end to end: --no-wait --json leaves a working session stale", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		e.setAgents(t, "["+busyAgentJSON("e8cdf5d8", path)+","+agentJSON("7a40c2d9", e.scratch)+"]")
		e.setRespawned(t, "["+busyAgentJSON("e8cdf5d8", path)+","+respawnedAgentJSON("7a40c2d9", e.scratch)+"]")
		require.Equal(t, exitOK, e.run(t, "--json", "restart", "--no-wait"), "stderr: %s", e.stderr.String())
		assert.JSONEq(t, `{"sessions":[
			{"id":"7a40c2d9","workspace":null,"outcome":"restarted","restarted":true,"error":null},
			{"id":"e8cdf5d8","workspace":"fix-login","outcome":"stale","restarted":false,"error":null}
		],"warnings":[]}`, e.stdout.String())
		assert.Equal(t, []string{"daemon_stop", "daemon_run", "respawn_7a40c2d9"}, e.calls(t))
	})
	t.Run("end to end: a failed stop with the old service running restarts nothing", func(t *testing.T) {
		t.Parallel()
		e := newLifetimeE2E(t)
		path := e.start(t, "fix-login", "e8cdf5d8")
		require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "daemon-pid"), []byte("1\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(e.scratch, "fail-stop"), nil, 0o600))
		e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
		require.Equal(t, exitFailed, e.run(t, "--json", "restart"))
		assert.Empty(t, e.stdout.String())
		assert.Contains(t, e.stderr.String(), `"code":"claude_failed"`)
		assert.Contains(t, e.stderr.String(), "is still running, so no session was restarted")
		assert.Equal(t, []string{"daemon_stop"}, e.calls(t))
	})
}
