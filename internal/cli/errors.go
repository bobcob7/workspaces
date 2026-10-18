package cli

import (
	"errors"
	"fmt"
)

// Exit codes from ADR 0016. Adding one, or moving an error code to another,
// needs a new ADR.
const (
	exitOK       = 0
	exitFailed   = 1
	exitUsage    = 2
	exitNotFound = 3
	exitRefused  = 4
)

// errorCode names a failure in --json error output (ADR 0016). Codes are only
// ever added.
type errorCode string

const (
	codeFetchFailed       errorCode = "fetch_failed"
	codeCloneFailed       errorCode = "clone_failed"
	codeGitFailed         errorCode = "git_failed"
	codeClaudeMissing     errorCode = "claude_missing"
	codeClaudeUntrusted   errorCode = "claude_untrusted"
	codeClaudeFailed      errorCode = "claude_failed"
	codeInternal          errorCode = "internal"
	codeUsage             errorCode = "usage"
	codeSpecInvalid       errorCode = "spec_invalid"
	codeNameInvalid       errorCode = "name_invalid"
	codeRepoNotFound      errorCode = "repo_not_found"
	codeRepoAmbiguous     errorCode = "repo_ambiguous"
	codeRefNotFound       errorCode = "ref_not_found"
	codeWorkspaceNotFound errorCode = "workspace_not_found"
	codeWorkspaceExists   errorCode = "workspace_exists"
	codeShellRunning      errorCode = "shell_running"
)

var errNotImplemented = errors.New("not implemented yet")

// cliError is a failure that carries its error code and optional details
// for --json output.
type cliError struct {
	code    errorCode
	err     error
	details map[string]any
}

func (e *cliError) Error() string {
	return e.err.Error()
}

func (e *cliError) Unwrap() error {
	return e.err
}

func (e *cliError) exitCode() int {
	return e.code.exitCode()
}

// exitCode lists every code, so the exhaustive linter flags a new code that
// hasn't been given an exit code.
func (c errorCode) exitCode() int {
	switch c {
	case codeFetchFailed, codeCloneFailed, codeGitFailed, codeClaudeMissing,
		codeClaudeUntrusted, codeClaudeFailed, codeInternal:
		return exitFailed
	case codeUsage, codeSpecInvalid, codeNameInvalid:
		return exitUsage
	case codeRepoNotFound, codeRepoAmbiguous, codeRefNotFound, codeWorkspaceNotFound:
		return exitNotFound
	case codeWorkspaceExists, codeShellRunning:
		return exitRefused
	}
	return exitFailed
}

func newError(code errorCode, err error) *cliError {
	return &cliError{code: code, err: err}
}

func usageErrorf(format string, args ...any) *cliError {
	return newError(codeUsage, fmt.Errorf(format, args...))
}

// asCLIError gives every error a code: errors that already have one keep
// it, anything else is internal.
func asCLIError(err error) *cliError {
	if ce, ok := errors.AsType[*cliError](err); ok {
		return ce
	}
	return newError(codeInternal, err)
}
