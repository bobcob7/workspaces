package claude

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// notRunningLine is the first line of `claude daemon status`'s output, which
// then exits 1, when no background service is running (ADR 0024).
const notRunningLine = "not running"

// DaemonStatus is what `claude daemon status` reports about Claude Code's
// background service.
type DaemonStatus struct {
	Running bool
	// PID is the service's process.
	PID     int
	Version string
	Uptime  time.Duration
	// Origin says what started the service: "foreground" for `claude
	// daemon run`, or "transient — started on-demand by `claude` (pid N)
	// in <dir>".
	Origin string
}

// DaemonStatus runs `claude daemon status`. No running service is a
// DaemonStatus with Running false, not an error.
func (c *Client) DaemonStatus(ctx context.Context) (DaemonStatus, error) {
	out, err := c.run(ctx, "daemon", "status")
	if cmdErr, ok := errors.AsType[*CommandError](err); ok && reportsNotRunning([]byte(cmdErr.Stdout)) {
		return DaemonStatus{}, nil
	}
	if err != nil {
		return DaemonStatus{}, err
	}
	return parseDaemonStatus(out)
}

// StopDaemon stops Claude Code's background service with `claude daemon
// stop --any --keep-workers`. Running sessions keep running, and each waits
// about a minute for a new service to reconnect to it (ADR 0018).
func (c *Client) StopDaemon(ctx context.Context) error {
	_, err := c.run(ctx, "daemon", "stop", "--any", "--keep-workers")
	return err
}

// RunDaemon starts `claude daemon run` detached, in its own session, with
// this Client's environment, stdio on /dev/null, and HOME as its working
// directory, as Claude Code starts its own, and returns its pid without
// waiting for it (ADR 0018). It outlives ctx and ws.
func (c *Client) RunDaemon(ctx context.Context) (int, error) {
	if err := c.ensureVersion(ctx); err != nil {
		return 0, err
	}
	//nolint:gosec // G204: running claude is this package's purpose.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), c.path, "daemon", "run")
	cmd.Env = append([]string{}, c.environ...)
	cmd.Dir = c.daemonDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		if notInstalled(err) {
			return 0, fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return 0, fmt.Errorf("starting claude daemon run: %w", err)
	}
	pid := cmd.Process.Pid
	c.log.DebugContext(ctx, "claude daemon run started", slog.Int("pid", pid))
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// daemonDir is where a detached service runs: HOME, so it doesn't hold on
// to ws's working directory, which may be a workspace that is removed.
func (c *Client) daemonDir() string {
	if home := lookup(c.environ, "HOME"); home != "" {
		return home
	}
	return "/"
}

// reportsNotRunning reports whether `claude daemon status` printed that no
// service is running.
func reportsNotRunning(out []byte) bool {
	line, _, _ := bytes.Cut(out, []byte("\n"))
	return strings.TrimSpace(string(line)) == notRunningLine
}

// parseDaemonStatus reads the unindented "key: value" lines of `claude
// daemon status`.
func parseDaemonStatus(out []byte) (DaemonStatus, error) {
	fields := statusFields(out)
	pidText, ok := fields["pid"]
	if !ok {
		if reportsNotRunning(out) {
			return DaemonStatus{}, nil
		}
		return DaemonStatus{}, fmt.Errorf("%w: claude daemon status has no pid", ErrUnexpectedOutput)
	}
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("%w: claude daemon status pid %q", ErrUnexpectedOutput, pidText)
	}
	st := DaemonStatus{Running: true, PID: pid, Version: fields["version"], Origin: fields["origin"]}
	if up, ok := fields["uptime"]; ok {
		if st.Uptime, err = time.ParseDuration(up); err != nil {
			return DaemonStatus{}, fmt.Errorf("%w: claude daemon status uptime %q", ErrUnexpectedOutput, up)
		}
	}
	return st, nil
}

// statusFields maps each unindented "key: value" line's key to its value.
func statusFields(out []byte) map[string]string {
	fields := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' || line[0] == '\t' {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return fields
}
