package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/gate"
)

const (
	runDefaultTimeout = 120 * time.Second
	runMaxTimeout     = 900 * time.Second
	runOutputTail     = 8 << 10
)

type runArgs struct {
	Command        string `json:"command"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

func runTool(cfg *Config) Tool {
	return Tool{
		Name:        "run",
		Description: "Run a shell command in the session directory; it reports the exit code and the tail of stdout and stderr.",
		Parameters: objectSchema(map[string]map[string]any{
			"command":         field("string", "the command, run with /bin/sh -c"),
			"timeout_seconds": field("integer", "wall-clock limit, default 120, at most 900"),
		}, "command"),
		Call: typed(func(ctx context.Context, args runArgs) (Result, error) {
			return runCommand(ctx, cfg, args)
		}),
	}
}

func runCommand(ctx context.Context, cfg *Config, args runArgs) (Result, error) {
	if args.Command == "" {
		return Result{}, errors.New("missing command")
	}
	if err := CheckCommand(args.Command, cfg.Branch); err != nil {
		return Result{}, err
	}
	timeout := runTimeout(args.TimeoutSeconds)
	res, err := cfg.Shell.Run(ctx, gate.Request{Script: args.Command, Dir: cfg.Dir, Timeout: timeout})
	if errors.Is(err, gate.ErrTimedOut) {
		return Result{Text: fmt.Sprintf("timed out after %ds", int(timeout.Seconds()))}, nil
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Text: formatRun(res)}, nil
}

// runTimeout is the requested limit, or the default when none is asked
// for, capped at runMaxTimeout.
func runTimeout(seconds int) time.Duration {
	switch {
	case seconds <= 0:
		return runDefaultTimeout
	case seconds > int(runMaxTimeout/time.Second):
		return runMaxTimeout
	}
	return time.Duration(seconds) * time.Second
}

func formatRun(res gate.Result) string {
	return strings.Join([]string{
		fmt.Sprintf("exit %d", res.ExitCode),
		"--- stdout ---",
		outputTail(res.Stdout, res.StdoutTruncated),
		"--- stderr ---",
		outputTail(res.Stderr, res.StderrTruncated),
	}, "\n")
}

// outputTail keeps the last runOutputTail bytes of s, marking any cut.
func outputTail(s string, truncated bool) string {
	if len(s) <= runOutputTail && !truncated {
		return s
	}
	kept := strings.ToValidUTF8(s[max(0, len(s)-runOutputTail):], "")
	return "[truncated]\n" + kept
}
