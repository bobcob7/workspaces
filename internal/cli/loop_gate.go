package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/loop"
)

// loopSkipped is the tick action for a due loop whose script exited non-zero.
const loopSkipped loopAction = "skipped"

// maxGateMessage is how many bytes of stderr a skipped gate keeps.
const maxGateMessage = 200

// gateEnv is the environment a loop's script gets beyond ws's own:
// WS_LOOP_NAME=<name>, then WS_LOOP_VAR_<KEY>=<value> per var, by key.
func gateEnv(l *loop.Loop) []string {
	env := []string{"WS_LOOP_NAME=" + l.Name}
	for _, key := range slices.Sorted(maps.Keys(l.Vars)) {
		env = append(env, "WS_LOOP_VAR_"+key+"="+l.Vars[key])
	}
	return env
}

// runGate runs l's script, if it has one, sets l.LastGate to a new Gate
// with the outcome, and returns the prompt data for the run. With no
// script it returns empty data and nil, and leaves LastGate alone.
//   - exit 0: GatePassed, ExitCode 0, data from the result, nil error.
//   - other exit: GateSkipped, the exit code, Message skipMessage(stderr);
//     returns fmt.Errorf("%w: exit %d", loop.ErrSkipped, code), with
//     ": <message>" appended when there is one.
//   - Run's error: GateTimedOut when errors.Is(err, gate.ErrTimedOut),
//     else GateFailed; ExitCode -1; Message err.Error(); returns
//     fmt.Errorf("%w: %w", loop.ErrScriptFailed, err).
//
// A truncated stdout or stderr adds a warning.
func (a *App) runGate(ctx context.Context, l *loop.Loop) (loop.PromptData, error) {
	if l.Script == "" {
		return loop.PromptData{}, nil
	}
	req, err := a.gateRequest(l)
	if err != nil {
		return loop.PromptData{}, a.gateError(l, loop.GateFailed, err)
	}
	res, err := a.gate.Run(ctx, req)
	if err != nil {
		outcome := loop.GateFailed
		if errors.Is(err, gate.ErrTimedOut) {
			outcome = loop.GateTimedOut
		}
		return loop.PromptData{}, a.gateError(l, outcome, err)
	}
	if res.StdoutTruncated {
		a.warned = append(a.warned, fmt.Sprintf("loop %s: script stdout over 64 KiB was cut", l.Name))
	}
	if res.StderrTruncated {
		a.warned = append(a.warned, fmt.Sprintf("loop %s: script stderr over 64 KiB was cut", l.Name))
	}
	if res.ExitCode == 0 {
		l.LastGate = a.newGate(loop.GatePassed, 0, "")
		return loop.PromptData{Stdout: res.Stdout, Stderr: res.Stderr}, nil
	}
	msg := skipMessage(res.Stderr)
	l.LastGate = a.newGate(loop.GateSkipped, res.ExitCode, msg)
	skipErr := fmt.Errorf("%w: exit %d", loop.ErrSkipped, res.ExitCode)
	if msg != "" {
		skipErr = fmt.Errorf("%w: %s", skipErr, msg)
	}
	return loop.PromptData{}, skipErr
}

// gateRequest is the script run for l, in the home directory.
func (a *App) gateRequest(l *loop.Loop) (gate.Request, error) {
	dir, err := a.homeDir()
	if err != nil {
		return gate.Request{}, err
	}
	timeout, err := l.Timeout()
	if err != nil {
		return gate.Request{}, err
	}
	return gate.Request{Script: l.Script, Dir: dir, Env: gateEnv(l), Timeout: timeout}, nil
}

// gateError records a script that didn't finish as l's gate and returns
// the error for the run.
func (a *App) gateError(l *loop.Loop, outcome string, err error) error {
	l.LastGate = a.newGate(outcome, -1, err.Error())
	return fmt.Errorf("%w: %w", loop.ErrScriptFailed, err)
}

// newGate is a gate outcome, stamped with the current time.
func (a *App) newGate(outcome string, exitCode int, message string) *loop.Gate {
	return &loop.Gate{At: a.now(), Outcome: outcome, ExitCode: exitCode, Message: message}
}

// skipMessage is the last non-blank line of stderr, trimmed, cut to at
// most maxGateMessage bytes at a rune boundary.
func skipMessage(stderr string) string {
	for _, l := range slices.Backward(strings.Split(stderr, "\n")) {
		if line := strings.TrimSpace(l); line != "" {
			return cutUTF8(line, maxGateMessage)
		}
	}
	return ""
}

// cutUTF8 is s cut to at most n bytes, not splitting a rune.
func cutUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
