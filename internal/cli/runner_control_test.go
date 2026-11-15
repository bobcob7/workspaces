package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/runner"
)

func TestDaemonRunnerControl_Stop(t *testing.T) {
	t.Run("stops the runner through the daemon", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.StopRunnerFunc = func(context.Context, string) (bool, error) { return true, nil }
		require.NoError(t, daemonRunnerControl{app: e.app}.Stop(t.Context(), "x"))
		require.Len(t, e.daemon.StopRunnerCalls(), 1)
		assert.Empty(t, e.app.warned)
	})
	t.Run("without the daemon, a runner with no run directory is already gone", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, errTest }
		require.NoError(t, daemonRunnerControl{app: e.app}.Stop(t.Context(), "x"))
		_, err := os.Stat(runner.FilesFor(e.app.paths.State, "x").Dir)
		assert.ErrorIs(t, err, os.ErrNotExist, "nothing is created for a runner that isn't there")
	})
	t.Run("without the daemon, a stopped runner gets its stop file and a warning", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, errTest }
		files := runner.FilesFor(e.app.paths.State, "x")
		require.NoError(t, os.MkdirAll(files.Dir, 0o700))
		require.NoError(t, daemonRunnerControl{app: e.app}.Stop(t.Context(), "x"))
		assert.True(t, files.StopRequested())
		require.Len(t, e.app.warned, 1)
		assert.Contains(t, e.app.warned[0], "without the daemon")
	})
	t.Run("a runner that can be stopped neither way is an error with both causes", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, errTest }
		// A file where the runner directory's parent should be makes the stop file impossible.
		require.NoError(t, os.WriteFile(e.app.paths.State, nil, 0o600))
		err := daemonRunnerControl{app: e.app}.Stop(t.Context(), "x")
		require.ErrorIs(t, err, errTest)
	})
	t.Run("a working runner is interrupted and its exit awaited", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, errTest }
		e.app.runnerStopWait = 5 * time.Second
		exited := startWorkingRunner(t, e.app.paths.State, "x")
		require.NoError(t, daemonRunnerControl{app: e.app}.Stop(t.Context(), "x"))
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			require.FailNow(t, "the runner's process was not interrupted")
		}
	})
}

// startWorkingRunner starts a process that stands for runner id: its own
// process group, recorded in its status as the live runner. It is reaped
// once it exits, which the returned channel signals.
func startWorkingRunner(t *testing.T, state, id string) <-chan struct{} {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, cmd.Start())
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	started, err := proc.Reader{}.StartTime(cmd.Process.Pid)
	require.NoError(t, err)
	require.NoError(t, runner.FilesFor(state, id).WriteStatus(&runner.Status{PID: cmd.Process.Pid, ProcessStartedAt: started}))
	return exited
}

func TestDaemonRunnerControl_Resume(t *testing.T) {
	t.Run("starts the runner again, resumed, through the daemon", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.StartRunnerFunc = func(context.Context, string, bool) (int, error) { return 4242, nil }
		require.NoError(t, daemonRunnerControl{app: e.app}.Resume(t.Context(), "x"))
		require.Len(t, e.daemon.StartRunnerCalls(), 1)
		assert.True(t, e.daemon.StartRunnerCalls()[0].Resume)
	})
	t.Run("the daemon's refusal keeps its code", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.StartRunnerFunc = func(context.Context, string, bool) (int, error) {
			return 0, &daemon.RemoteError{Code: "workspace_not_found", Message: "no runner"}
		}
		requireCode(t, codeWorkspaceNotFound, daemonRunnerControl{app: e.app}.Resume(t.Context(), "x"))
	})
	t.Run("without a daemon it is daemon_unavailable", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, newError(codeDaemonUnavailable, errTest) }
		requireCode(t, codeDaemonUnavailable, daemonRunnerControl{app: e.app}.Resume(t.Context(), "x"))
	})
}

func TestSignalGroup(t *testing.T) {
	t.Run("a group that is gone is no error", func(t *testing.T) {
		assert.NoError(t, signalGroup(daemon.OSStarter{}, 4194303, 0))
	})
}

func TestApp_dialDaemonClient(t *testing.T) {
	t.Run("with WS_DAEMON_AUTOSTART=0 and no daemon it is daemon_unavailable", func(t *testing.T) {
		app, _, _ := newTestApp()
		dir := t.TempDir()
		app.paths = config.NewPaths(filepath.Join(dir, "config"))
		app.environ = []string{"WS_DAEMON_AUTOSTART=0", "XDG_RUNTIME_DIR=" + dir}
		_, err := app.dialDaemonClient(t.Context())
		requireCode(t, codeDaemonUnavailable, err)
	})
}
