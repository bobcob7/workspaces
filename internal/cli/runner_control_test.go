package cli

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	"github.com/bobcob7/workspaces/internal/machine"
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
	t.Run("a runner stopped by ws rm is resumed by ws restore, and runs again with its stop file cleared", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		procs := newFakeRunnerProcesses(e.app.paths.State)
		sup := newTestSupervisor(e, procs)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return supervisorDaemon{sup: sup}, nil }
		started, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		require.NoError(t, err)
		id := started.Workspace.ID
		require.NoError(t, daemonRunnerControl{app: e.app}.Stop(t.Context(), id))
		require.True(t, runner.FilesFor(e.app.paths.State, id).StopRequested(), "the stop left its stop file")
		require.NoError(t, daemonRunnerControl{app: e.app}.Resume(t.Context(), id))
		require.Len(t, procs.starts, 2)
		assert.Contains(t, procs.starts[1].args, "--resume")
		assert.False(t, procs.starts[1].stopRequested, "the resumed runner starts unstopped")
		running := sup.Runners()
		require.Len(t, running, 1)
		assert.Equal(t, "working", running[0].State)
		assert.Equal(t, started.Session, running[0].Session)
	})
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

// fakeRunnerProcesses stands in for the operating system under the
// supervisor. A started runner is a live pid that writes the status a runner
// writes first; SIGTERM or SIGKILL ends it. Each start records its arguments
// and whether its stop file was there.
type fakeRunnerProcesses struct {
	state  string
	alive  map[int]bool
	starts []fakeRunnerStart
}

// fakeRunnerStart is one start of a fake runner.
type fakeRunnerStart struct {
	args          []string
	stopRequested bool
}

// fakeStartedAt is when every fake runner process started.
var fakeStartedAt = time.Date(2026, time.November, 15, 9, 0, 0, 0, time.UTC)

func newFakeRunnerProcesses(state string) *fakeRunnerProcesses {
	return &fakeRunnerProcesses{state: state, alive: map[int]bool{}}
}

// Start records the start and writes the runner's first status.
func (f *fakeRunnerProcesses) Start(_ string, args []string, _, _ string, _ []string) (int, error) {
	id := args[len(args)-1]
	files := runner.FilesFor(f.state, id)
	pid := 7000 + len(f.starts)
	f.starts = append(f.starts, fakeRunnerStart{args: args, stopRequested: files.StopRequested()})
	f.alive[pid] = true
	return pid, files.WriteStatus(&runner.Status{PID: pid, ProcessStartedAt: fakeStartedAt})
}

// Signal ends the process group pid on a termination signal.
func (f *fakeRunnerProcesses) Signal(pid int, sig syscall.Signal) error {
	if sig == syscall.SIGTERM || sig == syscall.SIGKILL {
		f.alive[pid] = false
	}
	return nil
}

// StartTime is fakeStartedAt for a live fake process, else an error.
func (f *fakeRunnerProcesses) StartTime(pid int) (time.Time, error) {
	if f.alive[pid] {
		return fakeStartedAt, nil
	}
	return time.Time{}, errors.New("no such process")
}

// newTestSupervisor is the daemon's supervisor over the fake processes, for
// e's state directory and config.
func newTestSupervisor(e *runnerEnv, procs *fakeRunnerProcesses) *daemon.Supervisor {
	cfg := daemon.SupervisorConfig{
		StateDir:   e.app.paths.State,
		ConfigDir:  e.app.paths.Root,
		Exe:        "ws",
		LoadConfig: func() (config.Config, error) { return e.app.config, nil },
		StartWait:  time.Second,
		StopWait:   time.Second,
		Store:      runnerRecords{Store: machine.NewStore(e.app.paths.State), workspaces: e.store},
	}
	return daemon.NewSupervisor(cfg, procs, procs, slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

// supervisorDaemon answers the runner requests as the daemon does, with its
// supervisor.
type supervisorDaemon struct {
	sup *daemon.Supervisor
}

// Preflight is not used by these tests; the model is checked elsewhere.
func (supervisorDaemon) Preflight(context.Context, daemon.PreflightRequest) (daemon.PreflightResponse, error) {
	return daemon.PreflightResponse{}, nil
}

// StartRunner starts runner id through the supervisor.
func (d supervisorDaemon) StartRunner(ctx context.Context, id string, resume bool) (int, error) {
	return d.sup.Start(ctx, id, resume)
}

// StopRunner stops runner id through the supervisor.
func (d supervisorDaemon) StopRunner(ctx context.Context, id string) (bool, error) {
	return d.sup.Stop(ctx, id)
}
