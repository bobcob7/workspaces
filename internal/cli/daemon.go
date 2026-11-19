package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/proc"
)

// daemonStopWait is how long ws daemon stop waits for the daemon to exit
// (ADR 0068).
const daemonStopWait = 30 * time.Second

func (a *App) newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon <OP>",
		Short: "Run, check, and stop ws's background service for runners",
		Args:  cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newDaemonRunCmd(), a.newDaemonStatusCmd(), a.newDaemonStopCmd())
	return cmd
}

func (a *App) newDaemonRunCmd() *cobra.Command {
	var origin string
	cmd := &cobra.Command{
		Use:     "run [--origin transient|managed|app]",
		Short:   "Run the daemon in the foreground",
		Args:    noArgs,
		PreRunE: a.noJSON,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runDaemonRun(cmd.Context(), origin)
		})),
	}
	cmd.Flags().StringVar(&origin, "origin", daemon.OriginManaged, "transient, managed, or app")
	return cmd
}

// runDaemonRun serves the daemon until it's told to stop. Its logs go to
// stderr and daemon.log (ADR 0068).
func (a *App) runDaemonRun(ctx context.Context, origin string) error {
	if !validDaemonOrigin(origin) {
		return usageErrorf("--origin must be transient, managed, or app, not %q", origin)
	}
	paths := a.daemonPaths()
	logFile, err := openDaemonLog(paths)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	log := slog.New(slog.NewJSONHandler(io.MultiWriter(a.stderr, logFile), nil))
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	exe, err := os.Executable()
	if err != nil {
		return newError(codeInternal, err)
	}
	sup := daemon.NewSupervisor(a.supervisorConfig(exe), proc.Reader{}, daemon.OSStarter{}, log)
	err = daemon.Serve(daemon.WithVersion(ctx, a.version), paths, origin, sup, log)
	return daemonRunError(err)
}

// supervisorConfig is the daemon's supervisor settings for this config
// directory: its runners are resumed at start when config.toml says so.
func (a *App) supervisorConfig(exe string) daemon.SupervisorConfig {
	return daemon.SupervisorConfig{
		StateDir:      a.paths.State,
		ConfigDir:     a.paths.Root,
		Exe:           exe,
		BaseEnv:       os.Environ(),
		LoadConfig:    func() (config.Config, error) { return config.ReadFile(a.paths.ConfigFile) },
		ResumeOnStart: a.config.Daemon.ResumesOnStart(),
		Store:         runnerRecords{Store: machine.NewStore(a.paths.State), workspaces: a.workspaces},
	}
}

func validDaemonOrigin(origin string) bool {
	switch origin {
	case daemon.OriginTransient, daemon.OriginManaged, daemon.OriginApp:
		return true
	}
	return false
}

//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func openDaemonLog(paths daemon.Paths) (*os.File, error) {
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", paths.Dir, err)
	}
	logFile, err := os.OpenFile(paths.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", paths.Log, err)
	}
	return logFile, nil
}

// daemonRunError turns a daemon that already runs for this config directory
// into daemon_running, with its pid.
func daemonRunError(err error) error {
	running, ok := errors.AsType[*daemon.RunningError](err)
	if !ok {
		return err
	}
	ce := newError(codeDaemonRunning, err)
	ce.details = map[string]any{"pid": running.PID}
	return ce
}

func (a *App) newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status [--json]",
		Short: "Show whether the daemon runs, since when, and its runners",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runDaemonStatus(cmd.Context())
		})),
	}
}

// runDaemonStatus asks the daemon for its status. It never starts one.
func (a *App) runDaemonStatus(ctx context.Context) error {
	paths := a.daemonPaths()
	st, err := daemon.Dial(paths).Status(ctx)
	switch {
	case errors.Is(err, daemon.ErrNotRunning):
		st = a.notRunningStatus(paths)
	case err != nil:
		return daemonUnavailable(err, paths)
	}
	st.Warnings = append(st.Warnings, a.daemonVersionWarnings(st)...)
	if a.global.json {
		return a.writeJSON(st)
	}
	a.printDaemonStatus(st)
	return nil
}

//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func (a *App) notRunningStatus(paths daemon.Paths) daemon.Status {
	return daemon.Status{Socket: paths.Socket, ConfigDir: a.paths.Root, Runners: []daemon.RunnerStatus{}, Warnings: []string{}}
}

// daemonVersionWarnings warns when the running daemon is older than this ws,
// which would otherwise keep serving the old version (ADR 0068).
//
//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func (a *App) daemonVersionWarnings(st daemon.Status) []string {
	if !st.Running || st.WSVersion == nil || !daemon.Older(*st.WSVersion, a.version) {
		return nil
	}
	return []string{fmt.Sprintf("the running ws daemon is older (%s); run ws daemon stop --keep-runners", *st.WSVersion)}
}

//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func (a *App) printDaemonStatus(st daemon.Status) {
	if !st.Running {
		fmt.Fprintf(a.stdout, "not running (socket %s)\n", st.Socket)
		return
	}
	fmt.Fprintf(a.stdout, "running: pid %d, %s origin, since %s, ws %s\n", *st.PID, *st.Origin, formatTime(*st.StartedAt), *st.WSVersion)
	fmt.Fprintf(a.stdout, "socket: %s\n", st.Socket)
	for _, r := range st.Runners {
		fmt.Fprintf(a.stdout, "runner %s: %s (pid %d)\n", r.Session, r.State, r.PID)
	}
}

func (a *App) newDaemonStopCmd() *cobra.Command {
	var keepRunners bool
	cmd := &cobra.Command{
		Use:   "stop [--keep-runners] [--json]",
		Short: "Stop the daemon, interrupting its runners or keeping them running",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runDaemonStop(cmd.Context(), keepRunners)
		})),
	}
	cmd.Flags().BoolVar(&keepRunners, "keep-runners", false, "leave the runners running for the next daemon to adopt")
	return cmd
}

// runDaemonStop asks the daemon to exit, then waits for it to go. A daemon
// that isn't running is a result, not an error.
func (a *App) runDaemonStop(ctx context.Context, keepRunners bool) error {
	paths := a.daemonPaths()
	client := daemon.Dial(paths)
	res, err := client.Shutdown(ctx, keepRunners)
	switch {
	case errors.Is(err, daemon.ErrNotRunning):
		return a.printDaemonStop(daemon.ShutdownResult{RunnersStopped: []string{}, RunnersKept: []string{}, Warnings: []string{}})
	case err != nil:
		return daemonUnavailable(err, paths)
	}
	if err := waitForDaemonExit(ctx, client, daemonStopWait); err != nil {
		return daemonUnavailable(err, paths)
	}
	return a.printDaemonStop(res)
}

// waitForDaemonExit polls until the daemon no longer answers, or limit
// passes.
func waitForDaemonExit(ctx context.Context, client *daemon.Client, limit time.Duration) error {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := client.Status(ctx)
		if errors.Is(err, daemon.ErrNotRunning) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for the daemon to exit: %w", ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("the daemon hasn't exited within %s", limit)
		case <-ticker.C:
		}
	}
}

//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func (a *App) printDaemonStop(res daemon.ShutdownResult) error {
	if a.global.json {
		return a.writeJSON(res)
	}
	if !res.Stopped {
		fmt.Fprintln(a.stdout, "not running")
		return nil
	}
	fmt.Fprintf(a.stdout, "stopped the daemon (pid %d)\n", *res.PID)
	return nil
}

// daemonUnavailable is daemon_unavailable, with the socket and the log to
// look at (ADR 0068).
//
//nolint:gocritic // hugeParam: daemon.Paths and Status are passed by value, as the daemon API has them.
func daemonUnavailable(err error, paths daemon.Paths) error {
	ce := newError(codeDaemonUnavailable, err)
	ce.details = map[string]any{"socket": paths.Socket, "log": paths.Log}
	return ce
}

// daemonPaths is where CONFIG_DIR's daemon keeps its files, and its socket.
func (a *App) daemonPaths() daemon.Paths {
	getenv := func(name string) string { return environValue(a.environ, name) }
	return daemon.PathsFor(a.paths.Root, getenv, os.Getuid())
}

// environValue is the value of name in environ, or "" when it isn't set.
func environValue(environ []string, name string) string {
	for _, entry := range environ {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key == name {
			return value
		}
	}
	return ""
}
