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
	codeClaudeSignedOut   errorCode = "claude_signed_out"
	codeClaudeFailed      errorCode = "claude_failed"
	codeInternal          errorCode = "internal"
	codeConfigInvalid     errorCode = "config_invalid"
	codeUsage             errorCode = "usage"
	codeSpecInvalid       errorCode = "spec_invalid"
	codeNameInvalid       errorCode = "name_invalid"
	codeRepoNotFound      errorCode = "repo_not_found"
	codeRepoAmbiguous     errorCode = "repo_ambiguous"
	codeRefNotFound       errorCode = "ref_not_found"
	codeWorkspaceNotFound errorCode = "workspace_not_found"
	codeWorkspaceExists   errorCode = "workspace_exists"
	codeShellRunning      errorCode = "shell_running"
	codeWorkspaceBusy     errorCode = "workspace_busy"
	codeSessionMismatch   errorCode = "session_mismatch"
	// Review codes from ADR 0039.
	codeReviewInvalid      errorCode = "review_invalid"
	codeReviewUnverifiable errorCode = "review_unverifiable"
	codeProfileUnknown     errorCode = "profile_unknown"
	codeReviewNotFound     errorCode = "review_not_found"
	codeReviewTimeout      errorCode = "review_timeout"
	codeGhMissing          errorCode = "gh_missing"
	codeGhAuth             errorCode = "gh_auth"
	codePRNotFound         errorCode = "pr_not_found"
	// From ADR 0040.
	codeFindingNotFound errorCode = "finding_not_found"
	// From ADR 0042.
	codeProfileInvalid errorCode = "profile_invalid"
	codeProfileExists  errorCode = "profile_exists"
	codeProfileBuiltin errorCode = "profile_builtin"
	// From ADR 0044.
	codeChildKept errorCode = "child_kept"
	// From ADR 0046.
	codeOutsideWorkspace errorCode = "outside_workspace"
	codeNotDescendant    errorCode = "not_descendant"
	// From ADR 0048.
	codeLoopInvalid  errorCode = "loop_invalid"
	codeLoopNotFound errorCode = "loop_not_found"
	codeLoopExists   errorCode = "loop_exists"
	// From ADR 0049.
	codeLoopSkipped      errorCode = "loop_skipped"
	codeLoopScriptFailed errorCode = "loop_script_failed"
	// Machine codes from ADR 0050.
	codeResultInvalid errorCode = "result_invalid"
	codeChildFailed   errorCode = "child_failed"
	codeWaitTimeout   errorCode = "wait_timeout"
	codeRoleInvalid   errorCode = "role_invalid"
	codeRoleUnknown   errorCode = "role_unknown"
	codeRoleExists    errorCode = "role_exists"
	codeRoleBuiltin   errorCode = "role_builtin"
	// From ADR 0054.
	codeConfigKeyUnknown   errorCode = "config_key_unknown"
	codeConfigValueInvalid errorCode = "config_value_invalid"
	codeConfigUneditable   errorCode = "config_uneditable"
	// From ADR 0056.
	codeMemoryInvalid  errorCode = "memory_invalid"
	codeMemoryNotFound errorCode = "memory_not_found"
	codeMemoryConflict errorCode = "memory_conflict"
	codeMemoryFull     errorCode = "memory_full"
	codeNotLoopRun     errorCode = "not_loop_run"
	// From ADR 0077.
	codeProgressInvalid errorCode = "progress_invalid"
	// From ADR 0068.
	codeDaemonUnavailable errorCode = "daemon_unavailable"
	codeDaemonRunning     errorCode = "daemon_running"
	// From ADR 0064: a runner's preflight and start.
	codeEndpointUnknown     errorCode = "endpoint_unknown"
	codeEndpointUnreachable errorCode = "endpoint_unreachable"
	codeModelNotServed      errorCode = "model_not_served"
	codeModelWithoutTools   errorCode = "model_without_tools"
	codeRunnerFailed        errorCode = "runner_failed"
	// From ADRs 0072 and 0075.
	codeEnvironmentUnknown errorCode = "environment_unknown" // exit 3
	codeEnvironmentExists  errorCode = "environment_exists"  // exit 4
	codeEnrollmentFailed   errorCode = "enrollment_failed"   // exit 1
	codeEnrollmentUsed     errorCode = "enrollment_used"     // exit 4
	codeCAInUse            errorCode = "ca_in_use"           // exit 4
	// From ADR 0072 (PR 3).
	codeEnvironmentOffline    errorCode = "environment_offline"      // exit 1
	codeEnvironmentOutdated   errorCode = "environment_outdated"     // exit 1
	codeEnvironmentMismatch   errorCode = "environment_mismatch"     // exit 4
	codeEnvironmentOutOfScope errorCode = "environment_out_of_scope" // exit 4 (ADR 0082)
	// From PR 4 (ADR 0074).
	codeTerminalLimit errorCode = "terminal_limit" // exit 4
	// From ADR 0080.
	codeOperationLimited errorCode = "operation_limited" // exit 1
	// ADR 0078: artifacts.
	codeArtifactInvalid     errorCode = "artifact_invalid"     // exit 2
	codeArtifactUnknown     errorCode = "artifact_unknown"     // exit 3
	codeArtifactConflict    errorCode = "artifact_conflict"    // exit 4
	codeArtifactFull        errorCode = "artifact_full"        // exit 4
	codeArtifactForbidden   errorCode = "artifact_forbidden"   // exit 4
	codeArtifactUnreachable errorCode = "artifact_unreachable" // exit 1
	// ADR 0079: comment anchors.
	codeAnchorNotFound errorCode = "anchor_not_found" // exit 3
	// ADR 0081: the environment runs an OS the feature isn't built for yet.
	codePlatformUnsupported errorCode = "platform_unsupported" // exit 1
)

// shellExitError is ws shell's result once its shell has exited with a non-zero
// status: ws exits with that status, not an exit code from ADR 0016's
// table, and prints nothing more.
type shellExitError struct {
	status int
}

func (e *shellExitError) Error() string {
	return fmt.Sprintf("the shell exited with status %d", e.status)
}

// cliError is a failure that carries its error code and optional details
// for --json output.
type cliError struct {
	code    errorCode
	err     error
	details map[string]any
	// reported means the command already reported the failure in its
	// result, so only the exit code is left to set (ADR 0016).
	reported bool
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
		codeClaudeUntrusted, codeClaudeSignedOut, codeClaudeFailed, codeInternal, codeConfigInvalid,
		codeReviewInvalid, codeReviewUnverifiable, codeReviewTimeout, codeGhMissing, codeGhAuth,
		codeLoopScriptFailed, codeChildFailed, codeWaitTimeout, codeConfigUneditable, codeDaemonUnavailable,
		codeEndpointUnreachable, codeModelWithoutTools, codeRunnerFailed, codeEnrollmentFailed,
		codeEnvironmentOffline, codeEnvironmentOutdated, codeOperationLimited, codeArtifactUnreachable, codePlatformUnsupported:
		return exitFailed
	case codeUsage, codeSpecInvalid, codeNameInvalid, codeProfileUnknown, codeProfileInvalid, codeOutsideWorkspace,
		codeLoopInvalid, codeResultInvalid, codeRoleInvalid, codeConfigKeyUnknown, codeConfigValueInvalid,
		codeMemoryInvalid, codeProgressInvalid, codeArtifactInvalid:
		return exitUsage
	case codeRepoNotFound, codeRepoAmbiguous, codeRefNotFound, codeWorkspaceNotFound,
		codeReviewNotFound, codePRNotFound, codeFindingNotFound, codeLoopNotFound, codeRoleUnknown,
		codeMemoryNotFound, codeEndpointUnknown, codeModelNotServed, codeEnvironmentUnknown, codeArtifactUnknown,
		codeAnchorNotFound:
		return exitNotFound
	case codeWorkspaceExists, codeShellRunning, codeWorkspaceBusy, codeSessionMismatch,
		codeProfileExists, codeProfileBuiltin, codeChildKept, codeNotDescendant, codeLoopExists,
		codeLoopSkipped, codeRoleExists, codeRoleBuiltin, codeMemoryConflict, codeMemoryFull, codeNotLoopRun,
		codeDaemonRunning, codeEnvironmentExists, codeEnrollmentUsed, codeCAInUse, codeEnvironmentMismatch, codeTerminalLimit,
		codeArtifactConflict, codeArtifactFull, codeArtifactForbidden, codeEnvironmentOutOfScope:
		return exitRefused
	}
	return exitFailed
}

// known reports whether c is one of ws's error codes, as opposed to a code a
// newer remote ws answered with.
func (c errorCode) known() bool {
	switch c {
	case codeFetchFailed, codeCloneFailed, codeGitFailed, codeClaudeMissing, codeClaudeUntrusted, codeClaudeSignedOut,
		codeClaudeFailed, codeInternal, codeConfigInvalid, codeUsage, codeSpecInvalid, codeNameInvalid,
		codeRepoNotFound, codeRepoAmbiguous, codeRefNotFound, codeWorkspaceNotFound, codeWorkspaceExists,
		codeShellRunning, codeWorkspaceBusy, codeSessionMismatch, codeReviewInvalid, codeReviewUnverifiable,
		codeProfileUnknown, codeReviewNotFound, codeReviewTimeout, codeGhMissing, codeGhAuth,
		codePRNotFound, codeFindingNotFound, codeProfileInvalid, codeProfileExists, codeProfileBuiltin,
		codeChildKept, codeOutsideWorkspace, codeNotDescendant, codeLoopInvalid, codeLoopNotFound,
		codeLoopExists, codeLoopSkipped, codeLoopScriptFailed, codeResultInvalid, codeChildFailed,
		codeWaitTimeout, codeRoleInvalid, codeRoleUnknown, codeRoleExists, codeRoleBuiltin,
		codeConfigKeyUnknown, codeConfigValueInvalid, codeConfigUneditable, codeMemoryInvalid,
		codeMemoryNotFound, codeMemoryConflict, codeMemoryFull, codeNotLoopRun, codeDaemonUnavailable,
		codeDaemonRunning, codeEndpointUnknown, codeEndpointUnreachable, codeModelNotServed,
		codeModelWithoutTools, codeRunnerFailed, codeEnvironmentUnknown, codeEnvironmentExists,
		codeEnrollmentFailed, codeEnrollmentUsed, codeCAInUse, codeEnvironmentOffline,
		codeEnvironmentOutdated, codeEnvironmentMismatch, codeTerminalLimit, codeProgressInvalid, codeOperationLimited,
		codeArtifactInvalid, codeArtifactUnknown, codeArtifactConflict, codeArtifactFull, codeArtifactForbidden,
		codeArtifactUnreachable, codeAnchorNotFound, codePlatformUnsupported, codeEnvironmentOutOfScope:
		return true
	}
	return false
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
