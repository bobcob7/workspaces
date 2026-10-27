// Package git runs the git binary. It is the only package that does
// (ADR 0003).
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

var errNoDir = errors.New("git command without a directory")

// waitDelay bounds the wait for git's output once ctx has killed it. Killing
// git doesn't close the pipes a descendant inherited (a hook, a credential
// helper), and without a bound the call outlives its context for as long as
// that descendant lives.
const waitDelay = 5 * time.Second

// Runner runs git commands with a fixed environment.
type Runner struct {
	log      *slog.Logger
	gitPath  string
	environ  []string
	mu       sync.Mutex
	checked  bool
	checkErr error
}

// NewRunner returns a Runner that runs gitPath, normally "git", with
// environ, normally os.Environ(), plus GIT_TERMINAL_PROMPT=0.
func NewRunner(log *slog.Logger, gitPath string, environ []string) *Runner {
	return &Runner{log: log, gitPath: gitPath, environ: environ}
}

// Run runs `git -C dir args...` and returns its stdout. dir is required, so
// no command depends on the process's working directory. The first call
// checks the git version.
func (r *Runner) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: git %s", errNoDir, strings.Join(args, " "))
	}
	if err := r.ensureVersion(ctx); err != nil {
		return nil, err
	}
	return r.exec(ctx, dir, args)
}

// ensureVersion checks the git version once per Runner. A check cut short
// by ctx is retried on the next call.
func (r *Runner) ensureVersion(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checked {
		return r.checkErr
	}
	err := r.checkVersion(ctx)
	if ctx.Err() == nil {
		r.checked, r.checkErr = true, err
	}
	return err
}

func (r *Runner) checkVersion(ctx context.Context) error {
	out, err := r.exec(ctx, "", []string{"--version"})
	if err != nil {
		return err
	}
	v, err := parseVersion(string(out))
	if err != nil {
		return err
	}
	if minimum := minVersion(); !v.atLeast(minimum) {
		return fmt.Errorf("%w: found %s, need %s or newer", ErrTooOld, v, minimum)
	}
	return nil
}

// exec runs git with args, prefixed by -C dir when dir isn't empty, and
// logs the command and its duration at debug level.
func (r *Runner) exec(ctx context.Context, dir string, args []string) ([]byte, error) {
	full := args
	if dir != "" {
		full = append([]string{"-C", dir}, args...)
	}
	//nolint:gosec // G204: running git with arguments is this package's purpose.
	cmd := exec.CommandContext(ctx, r.gitPath, full...)
	cmd.Env = append(slices.Clone(r.environ), "GIT_TERMINAL_PROMPT=0")
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	r.log.DebugContext(ctx, "git",
		slog.String("dir", dir),
		slog.Any("args", args),
		slog.Duration("took", time.Since(start)),
		slog.Int("exit", cmd.ProcessState.ExitCode()),
	)
	if err == nil {
		return stdout.Bytes(), nil
	}
	if cmd.ProcessState == nil && (errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)) {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	return nil, &CommandError{
		Args:     args,
		Dir:      dir,
		ExitCode: cmd.ProcessState.ExitCode(),
		Stderr:   strings.TrimSpace(stderr.String()),
		Err:      err,
	}
}
