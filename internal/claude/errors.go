package claude

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNotInstalled means claude isn't on PATH, or not at the given path.
	ErrNotInstalled = errors.New("claude not found")
	// ErrTooOld means claude is older than the oldest version ws was
	// checked against (ADR 0024).
	ErrTooOld = errors.New("claude is too old")
	// ErrUntrusted means Claude Code refused to start a session in a
	// directory whose trust prompt hasn't been accepted (ADR 0015).
	ErrUntrusted = errors.New("directory not trusted by Claude Code")
	// ErrNoSession means Claude Code has no background session with the id.
	ErrNoSession = errors.New("no such Claude Code session")
	// ErrUnexpectedOutput means claude's output couldn't be read.
	ErrUnexpectedOutput = errors.New("unexpected output from claude")
)

// Prefixes of claude's stderr that identify a failure (ADR 0024).
const (
	untrustedPrefix = "Workspace not trusted."
	noSessionPrefix = "No job matching "
)

// CommandError is a claude command that ran and failed.
type CommandError struct {
	Args     []string
	Dir      string
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
}

// Error reads "claude <args>: <err>: <stderr>".
func (e *CommandError) Error() string {
	msg := fmt.Sprintf("claude %s: %v", strings.Join(e.Args, " "), e.Err)
	if e.Stderr == "" {
		return msg
	}
	return msg + ": " + e.Stderr
}

func (e *CommandError) Unwrap() error {
	return e.Err
}

// classify wraps a failed command's error in the sentinel its stderr
// identifies, if any, keeping the CommandError reachable with errors.As.
func classify(e *CommandError) error {
	switch {
	case strings.HasPrefix(e.Stderr, untrustedPrefix):
		return fmt.Errorf("%w: %w", ErrUntrusted, e)
	case strings.HasPrefix(e.Stderr, noSessionPrefix):
		return fmt.Errorf("%w: %w", ErrNoSession, e)
	}
	return e
}
