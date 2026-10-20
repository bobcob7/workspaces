// Package claude runs the Claude Code CLI to start, list, stop, remove, and
// respawn background sessions, and to manage Claude Code's background
// service. It is the only package that runs claude (ADRs 0015, 0018, 0024).
package claude

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Client runs claude with a fixed environment.
type Client struct {
	log      *slog.Logger
	path     string
	environ  []string
	jobsDir  string
	mu       sync.Mutex
	checked  bool
	checkErr error
	// replace is syscall.Exec: Attach replaces ws with claude through it.
	replace func(argv0 string, argv, envv []string) error
}

// New returns a Client that runs path, normally "claude", with environ,
// normally os.Environ(). Claude Code's per-session directories are looked
// up in jobsDir(environ).
func New(log *slog.Logger, path string, environ []string) *Client {
	return &Client{log: log, path: path, environ: environ, jobsDir: jobsDir(environ), replace: syscall.Exec}
}

// jobsDir is where Claude Code keeps a directory per session:
// $CLAUDE_CONFIG_DIR/jobs when CLAUDE_CONFIG_DIR is set and not empty, else
// $HOME/.claude/jobs, or "" when neither is set (ADR 0024).
func jobsDir(environ []string) string {
	if dir := lookup(environ, "CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "jobs")
	}
	if home := lookup(environ, "HOME"); home != "" {
		return filepath.Join(home, ".claude", "jobs")
	}
	return ""
}

// run runs `claude args...` in ws's working directory and returns its
// stdout. The first call checks the claude version.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	if err := c.ensureVersion(ctx); err != nil {
		return nil, err
	}
	return c.exec(ctx, "", args)
}

// runApart is run with claude in a process group of its own, so that a
// Ctrl-C at ws's terminal doesn't interrupt it: only ctx ending does. ws
// restart relies on this to finish replacing the background service and
// each respawn it began (ADR 0018).
func (c *Client) runApart(ctx context.Context, args ...string) ([]byte, error) {
	if err := c.ensureVersion(ctx); err != nil {
		return nil, err
	}
	return c.execWith(ctx, "", args, &syscall.SysProcAttr{Setpgid: true})
}

// Check checks, once per Client, that claude is on PATH and new enough
// (ADR 0024), so a command can fail before doing work that needs it.
func (c *Client) Check(ctx context.Context) error {
	return c.ensureVersion(ctx)
}

// ensureVersion checks the claude version once per Client. A check cut
// short by ctx is retried on the next call.
func (c *Client) ensureVersion(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.checked {
		return c.checkErr
	}
	err := c.checkVersion(ctx)
	if ctx.Err() == nil {
		c.checked, c.checkErr = true, err
	}
	return err
}

func (c *Client) checkVersion(ctx context.Context) error {
	out, err := c.exec(ctx, "", []string{"--version"})
	if err != nil {
		return err
	}
	v, err := parseVersion(string(out))
	if err != nil {
		return err
	}
	if minimum := minVersion(); !v.atLeast(minimum) {
		return fmt.Errorf("%w: %s is %s, need %s or newer", ErrTooOld, c.resolvedPath(), v, minimum)
	}
	return nil
}

// pipeWait is how long exec waits for claude's output to close after
// claude exits or ctx ends, in case a process claude started kept it open.
const pipeWait = 2 * time.Second

// resolvedPath is the claude that runs, as found on ws's PATH, for
// messages: an older claude earlier on PATH is a common cause of ErrTooOld.
func (c *Client) resolvedPath() string {
	if p, err := exec.LookPath(c.path); err == nil {
		return p
	}
	return c.path
}

// exec runs claude with args in dir, with no stdin, and logs the command
// and its duration at debug level.
func (c *Client) exec(ctx context.Context, dir string, args []string) ([]byte, error) {
	return c.execWith(ctx, dir, args, nil)
}

// execWith is exec with the process attributes attrs, such as a process
// group of its own.
func (c *Client) execWith(ctx context.Context, dir string, args []string, attrs *syscall.SysProcAttr) ([]byte, error) {
	//nolint:gosec // G204: running claude with arguments is this package's purpose.
	cmd := exec.CommandContext(ctx, c.path, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = attrs
	cmd.Env = append([]string{}, c.environ...)
	cmd.WaitDelay = pipeWait
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState.Success() {
		c.log.WarnContext(ctx, "claude exited but its output stayed open", slog.Any("args", args))
		err = nil
	}
	c.log.DebugContext(ctx, "claude",
		slog.String("dir", dir),
		slog.Any("args", args),
		slog.Duration("took", time.Since(start)),
		slog.Int("exit", cmd.ProcessState.ExitCode()),
	)
	if err == nil {
		return stdout.Bytes(), nil
	}
	if cmd.ProcessState == nil && notInstalled(err) {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, classify(&CommandError{
		Args:     args,
		Dir:      dir,
		ExitCode: cmd.ProcessState.ExitCode(),
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		Err:      err,
	})
}

// notInstalled reports whether err, from starting claude, means there is
// no claude to run. A missing working directory doesn't count.
func notInstalled(err error) bool {
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	pathErr, ok := errors.AsType[*fs.PathError](err)
	return ok && pathErr.Op != "chdir" && errors.Is(pathErr.Err, fs.ErrNotExist)
}

// lookup returns the last value of key in environ.
func lookup(environ []string, key string) string {
	value := ""
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v
		}
	}
	return value
}
