package git

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotInstalled means git isn't on PATH, or not at the given path.
	ErrNotInstalled = errors.New("git not found")
	// ErrTooOld means git is older than the minimum version (ADR 0003).
	ErrTooOld = errors.New("git is too old")
)

// CommandError is a git command that ran and failed.
type CommandError struct {
	Args     []string
	Dir      string
	ExitCode int
	Stderr   string
	Err      error
}

// Error reads "git <args>: <err>: <stderr>" (ADR 0003).
func (e *CommandError) Error() string {
	msg := fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	if e.Stderr == "" {
		return msg
	}
	return msg + ": " + e.Stderr
}

func (e *CommandError) Unwrap() error {
	return e.Err
}
