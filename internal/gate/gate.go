// Package gate runs a loop's gate script: /bin/sh -c with a timeout, its
// own process group, and capped output (ADR 0049).
package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/procgroup"
)

// MaxOutput is how many bytes of stdout, and of stderr, Run keeps.
const MaxOutput = 64 << 10

// waitDelay bounds the wait for a killed script's pipes to close.
const waitDelay = 5 * time.Second

// ErrTimedOut is a script that ran past its Request.Timeout.
var ErrTimedOut = errors.New("script timed out")

// ErrNoShell is a Windows host with no POSIX sh to run scripts with.
var ErrNoShell = errors.New("loop scripts need a POSIX sh: put sh on PATH or install Git for Windows (ADR 0081)")

// Request is one script to run.
type Request struct {
	Script string
	Dir    string
	// Env holds KEY=VALUE pairs added to the runner's environment,
	// replacing any of the same name.
	Env     []string
	Timeout time.Duration
}

// Result is how a script that ran to the end exited, and what it printed.
type Result struct {
	// ExitCode is the exit status, or -1 when a signal killed it.
	ExitCode int
	// Stdout and Stderr are at most MaxOutput bytes each, invalid UTF-8
	// replaced with U+FFFD, and trailing '\n' and '\r' trimmed.
	Stdout, Stderr                   string
	StdoutTruncated, StderrTruncated bool
}

// Runner runs scripts with an environment, normally os.Environ().
type Runner struct{ environ []string }

// New returns a Runner that runs scripts with environ.
func New(environ []string) *Runner {
	return &Runner{environ: environ}
}

// Run runs req.Script as sh -c req.Script in req.Dir, with stdin
// empty. A non-zero exit is a Result, not an error. The error is
// ErrTimedOut (wrapped) when it ran past req.Timeout, ctx's error
// (wrapped) when ctx ended first, and otherwise why it couldn't run.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error) {
	tctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

	cmd, err := scriptCommand(tctx, req.Script)
	if err != nil {
		return Result{}, fmt.Errorf("running script: %w", err)
	}
	cmd.Dir = req.Dir
	cmd.Env = mergeEnv(r.environ, req.Env)
	cmd.SysProcAttr = procgroup.OwnGroup()
	cmd.Cancel = func() error { return procgroup.KillTree(cmd.Process.Pid) }
	cmd.WaitDelay = waitDelay
	stdout := &capWriter{limit: MaxOutput}
	stderr := &capWriter{limit: MaxOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	err = cmd.Run()
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("running script: %w", ctx.Err())
	}
	if errors.Is(tctx.Err(), context.DeadlineExceeded) {
		return Result{}, fmt.Errorf("%w after %s", ErrTimedOut, req.Timeout)
	}

	var exitErr *exec.ExitError
	exitCode := 0
	switch {
	case errors.As(err, &exitErr):
		exitCode = exitErr.ExitCode()
	case err != nil:
		return Result{}, fmt.Errorf("running script: %w", err)
	}
	return Result{
		ExitCode:        exitCode,
		Stdout:          clean(stdout.buf.Bytes()),
		Stderr:          clean(stderr.buf.Bytes()),
		StdoutTruncated: stdout.truncated,
		StderrTruncated: stderr.truncated,
	}, nil
}

// capWriter keeps the first limit bytes written to it and drops the rest.
type capWriter struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	keep := min(len(p), w.limit-w.buf.Len())
	if keep > 0 {
		w.buf.Write(p[:keep])
	}
	if keep < len(p) {
		w.truncated = true
	}
	return len(p), nil
}

// clean makes captured output valid UTF-8 and drops trailing newlines.
func clean(b []byte) string {
	return strings.TrimRight(strings.ToValidUTF8(string(b), "�"), "\r\n")
}

// mergeEnv returns base without the variables extra sets, then extra.
func mergeEnv(base, extra []string) []string {
	set := make(map[string]bool, len(extra))
	for _, kv := range extra {
		set[envName(kv)] = true
	}
	merged := make([]string, 0, len(base)+len(extra))
	for _, kv := range base {
		if !set[envName(kv)] {
			merged = append(merged, kv)
		}
	}
	return append(merged, extra...)
}

// envName is the name in a KEY=VALUE pair.
func envName(kv string) string {
	name, _, _ := strings.Cut(kv, "=")
	return name
}
