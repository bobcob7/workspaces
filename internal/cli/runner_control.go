package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/runner"
)

// defaultRunnerWait is how long a started runner has to write its status.json
// (reference.md).
const defaultRunnerWait = 10 * time.Second

// defaultRunnerStopWait is how long a runner has to exit after SIGTERM before
// it is killed.
const defaultRunnerStopWait = 10 * time.Second

// runnerPollEvery is how often ws reads a runner's files while it waits.
const runnerPollEvery = 100 * time.Millisecond

// runnerWorking is the state of a runner whose process is alive (ADR 0063).
const runnerWorking = "working"

// dialDaemonClient is a client to this config directory's daemon, started
// when none runs unless WS_DAEMON_AUTOSTART=0.
func (a *App) dialDaemonClient(ctx context.Context) (runnerDaemon, error) {
	client, err := a.daemonClient(ctx)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// daemonRunnerControl stops and resumes ws runners for ws rm and ws restore
// (ADR 0063): through the daemon, and for a stop that the daemon can't do,
// by signal.
type daemonRunnerControl struct {
	app *App
}

// Stop stops runner id. When the daemon can't, the runner is stopped by
// signal, and the daemon's failure is a warning; only a failed signal is an
// error.
func (c daemonRunnerControl) Stop(ctx context.Context, id string) error {
	client, err := c.app.dialDaemon(ctx)
	if err == nil {
		_, err = client.StopRunner(ctx, id)
	}
	if err == nil {
		return nil
	}
	if direct := c.app.stopRunnerDirect(ctx, id); direct != nil {
		return errors.Join(direct, err)
	}
	c.app.warned = append(c.app.warned, fmt.Sprintf("stopped runner %s without the daemon: %v", id, err))
	return nil
}

// Resume starts stopped runner id again from its transcript, through the
// daemon.
func (c daemonRunnerControl) Resume(ctx context.Context, id string) error {
	client, err := c.app.dialDaemon(ctx)
	if err != nil {
		return err
	}
	if _, err := client.StartRunner(ctx, id, true); err != nil {
		return remoteRunnerError(err)
	}
	return nil
}

// stopRunnerDirect stops runner id without the daemon (ADR 0063): it asks the
// runner to stop after its current step, interrupts its process group, and
// kills the group if it still works after runnerStopWait. A runner that
// isn't working needs no signal.
func (a *App) stopRunnerDirect(ctx context.Context, id string) error {
	files := runner.FilesFor(a.paths.State, id)
	if _, err := os.Stat(files.Dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := files.RequestStop(); err != nil {
		return err
	}
	reader := runner.NewReader(a.paths.State, proc.Reader{})
	state, pid, _, _ := reader.State(id, time.Time{})
	if state != runnerWorking {
		return nil
	}
	signals := daemon.OSStarter{}
	if err := signalGroup(signals, pid, syscall.SIGTERM); err != nil {
		return err
	}
	if awaitRunnerExit(ctx, reader, id, a.runnerStopWait) {
		return nil
	}
	return signalGroup(signals, pid, syscall.SIGKILL)
}

// signalGroup sends sig to process group pid. A group that has already gone
// is no error.
func signalGroup(signals daemon.OSStarter, pid int, sig syscall.Signal) error {
	err := signals.Signal(pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// awaitRunnerExit reports whether runner id stops working within wait.
func awaitRunnerExit(ctx context.Context, reader runner.Reader, id string, wait time.Duration) bool {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(runnerPollEvery)
	defer ticker.Stop()
	for {
		if state, _, _, _ := reader.State(id, time.Time{}); state != runnerWorking {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}
