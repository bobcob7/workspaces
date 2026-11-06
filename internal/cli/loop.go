package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/loop"
)

func (a *App) newLoopCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "loop <OP>",
		Short:   "Start an agent in a new workspace every period",
		GroupID: peopleGroup,
		Args:    cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newLoopAddCmd(),
		a.newLoopEditCmd(),
		a.newLoopListCmd(),
		a.newLoopPauseCmd(),
		a.newLoopResumeCmd(),
		a.newLoopRmCmd(),
		a.newLoopRunCmd(),
		a.newLoopTickCmd(),
	)
	return cmd
}

type loopAddOptions struct {
	every, prompt, model, permissionMode string
	script, scriptTimeout                string
	vars                                 []string
	keep                                 int
	paused                               bool
}

func (a *App) newLoopAddCmd() *cobra.Command {
	var opts loopAddOptions
	cmd := &cobra.Command{
		Use:   "add [OPTIONS] <NAME> [<SPEC>...]",
		Short: "Add a loop that starts an agent every period",
		Args:  cobra.MinimumNArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopAdd(cmd, args, &opts)
		})),
	}
	cmd.Flags().StringVar(&opts.every, "every", "", "how often a run starts: whole minutes from 5m to 168h, such as 15m or 24h (required)")
	cmd.Flags().StringVar(&opts.prompt, "prompt", "", "the prompt each run starts with (required)")
	cmd.Flags().IntVar(&opts.keep, "keep", loop.DefaultKeep, "how many runs' workspaces to keep, 1 to 50")
	cmd.Flags().StringVar(&opts.model, "model", "", "passed to claude --model")
	cmd.Flags().StringVar(&opts.permissionMode, "permission-mode", "", "passed to claude --permission-mode")
	cmd.Flags().BoolVar(&opts.paused, "paused", false, "add the loop paused")
	cmd.Flags().StringVar(&opts.script, "script", "", "a shell script, run with /bin/sh -c, that must exit 0 for a run to start; its output is {{.Stdout}} and {{.Stderr}} in the prompt")
	cmd.Flags().StringVar(&opts.scriptTimeout, "script-timeout", "", "how long the script may run, 1s to 10m (default 1m)")
	cmd.Flags().StringArrayVar(&opts.vars, "var", nil, "KEY=VALUE for {{.Vars.KEY}} in the prompt and $WS_LOOP_VAR_KEY in the script; repeatable")
	return cmd
}

func (a *App) newLoopListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List every loop",
		Args:  cobra.NoArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runLoopList(cmd.Context())
		})),
	}
}

func (a *App) newLoopPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause <NAME>",
		Short: "Stop a loop from starting runs",
		Args:  cobra.ExactArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopPause(cmd.Context(), args[0])
		})),
	}
}

func (a *App) newLoopResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <NAME>",
		Short: "Start a paused loop again",
		Args:  cobra.ExactArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopResume(cmd.Context(), args[0])
		})),
	}
}

func (a *App) newLoopRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <NAME>",
		Short: "Delete a loop; its runs' workspaces stay",
		Args:  cobra.ExactArgs(1),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runLoopRm(cmd.Context(), args[0])
		})),
	}
}

// loopJSON is a loop in ws loop's output (docs/tools/ws-loop.md).
type loopJSON struct {
	Name           string            `json:"name"`
	Every          string            `json:"every"`
	Prompt         string            `json:"prompt"`
	Specs          []string          `json:"specs"`
	Model          *string           `json:"model"`
	PermissionMode *string           `json:"permission_mode"`
	Keep           int               `json:"keep"`
	Paused         bool              `json:"paused"`
	CreatedAt      string            `json:"created_at"`
	NextRunAt      *string           `json:"next_run_at"`
	LastRun        *loopRunJSON      `json:"last_run"`
	Script         *string           `json:"script"`
	ScriptTimeout  *string           `json:"script_timeout"`
	Vars           map[string]string `json:"vars"`
	LastGate       *gateJSON         `json:"last_gate"`
}

// gateJSON is a loop's last_gate, and a tick item's gate.
type gateJSON struct {
	At       string  `json:"at"`
	Outcome  string  `json:"outcome"`
	ExitCode *int    `json:"exit_code"`
	Message  *string `json:"message"`
}

type loopRunJSON struct {
	StartedAt string         `json:"started_at"`
	Workspace *string        `json:"workspace"`
	Session   *string        `json:"session"`
	Error     *itemErrorJSON `json:"error"`
}

// loopResult is add, pause, resume, and rm's output: the loop's fields and
// warnings.
type loopResult struct {
	loopJSON
	Warnings []string `json:"warnings"`
}

// loopListResult is ws loop list's output.
type loopListResult struct {
	Loops    []loopJSON `json:"loops"`
	Warnings []string   `json:"warnings"`
}

func loopJSONOf(l *loop.Loop) loopJSON {
	specs := l.Specs
	if specs == nil {
		specs = []string{}
	}
	out := loopJSON{
		Name:           l.Name,
		Every:          l.Every,
		Prompt:         l.Prompt,
		Specs:          specs,
		Model:          optional(l.Model),
		PermissionMode: optional(l.PermissionMode),
		Keep:           l.Keep,
		Paused:         l.Paused,
		CreatedAt:      formatTime(l.CreatedAt),
		LastRun:        loopRunJSONOf(l.LastRun()),
		Script:         optional(l.Script),
		ScriptTimeout:  scriptTimeoutJSONOf(l),
		Vars:           varsJSONOf(l.Vars),
		LastGate:       gateJSONOf(l.LastGate),
	}
	if !l.Paused {
		out.NextRunAt = optionalTime(l.NextRunAt)
	}
	return out
}

// scriptTimeoutJSONOf is nil without a script. Otherwise it is the script's
// timeout as FormatScriptTimeout prints it, or the record's own text when
// that doesn't parse.
func scriptTimeoutJSONOf(l *loop.Loop) *string {
	if l.Script == "" {
		return nil
	}
	d, err := l.Timeout()
	if err != nil {
		s := l.ScriptTimeout
		return &s
	}
	s := loop.FormatScriptTimeout(d)
	return &s
}

// varsJSONOf is never nil.
func varsJSONOf(vars map[string]string) map[string]string {
	if vars == nil {
		return map[string]string{}
	}
	return vars
}

// gateJSONOf is nil for nil.
func gateJSONOf(g *loop.Gate) *gateJSON {
	if g == nil {
		return nil
	}
	out := &gateJSON{At: formatTime(g.At), Outcome: g.Outcome, Message: optional(g.Message)}
	if g.ExitCode >= 0 {
		code := g.ExitCode
		out.ExitCode = &code
	}
	return out
}

func loopRunJSONOf(r *loop.Run) *loopRunJSON {
	if r == nil {
		return nil
	}
	out := &loopRunJSON{
		StartedAt: formatTime(r.StartedAt),
		Workspace: optional(r.Workspace),
		Session:   optional(r.Session),
	}
	if r.ErrorCode != "" {
		out.Error = &itemErrorJSON{Code: errorCode(r.ErrorCode), Message: r.ErrorMessage}
	}
	return out
}

// loopLine is one loop in text output, as docs/tools/ws-loop.md shows it.
func loopLine(l *loop.Loop) string {
	state := "active"
	next := "-"
	if l.Paused {
		state = "paused"
	} else {
		next = formatTime(l.NextRunAt)
	}
	last := "-"
	if r := l.LastRun(); r != nil && r.Workspace != "" {
		last = r.Workspace
	}
	return fmt.Sprintf("%-9s  every %-4s  %-6s  next %-20s  last %s", l.Name, l.Every, state, next, last)
}

func (a *App) runLoopAdd(cmd *cobra.Command, args []string, opts *loopAddOptions) error {
	if opts.every == "" {
		return usageErrorf("--every is required")
	}
	if opts.prompt == "" {
		return usageErrorf("--prompt is required")
	}
	script, scriptTimeout, vars, err := scriptOptions(opts)
	if err != nil {
		return err
	}
	interval, err := loop.ParseEvery(opts.every)
	if err != nil {
		return domainError(err)
	}
	if err := validSpecs(cmd, args[1:]); err != nil {
		return err
	}
	now := a.now().UTC()
	l := loop.Loop{
		Version:        loop.FormatVersion,
		Name:           args[0],
		Every:          loop.FormatEvery(interval),
		Prompt:         opts.prompt,
		Specs:          args[1:],
		Model:          opts.model,
		PermissionMode: opts.permissionMode,
		Keep:           opts.keep,
		Paused:         opts.paused,
		CreatedAt:      now,
		NextRunAt:      now.Add(interval),
		Script:         script,
		ScriptTimeout:  scriptTimeout,
		Vars:           vars,
	}
	if err := l.Validate(); err != nil {
		return domainError(err)
	}
	return a.createLoop(cmd.Context(), &l)
}

// scriptOptions checks --script, --script-timeout, and --var. A blank script
// is no script, and then the timeout is "" too.
func scriptOptions(opts *loopAddOptions) (script, scriptTimeout string, vars map[string]string, err error) {
	script = opts.script
	if strings.TrimSpace(script) == "" {
		script = ""
	}
	if opts.scriptTimeout != "" && script == "" {
		return "", "", nil, usageErrorf("--script-timeout needs --script")
	}
	vars, err = loop.ParseVars(opts.vars)
	if err != nil {
		return "", "", nil, domainError(err)
	}
	if script == "" {
		return script, "", vars, nil
	}
	d := loop.DefaultScriptTimeout
	if opts.scriptTimeout != "" {
		d, err = loop.ParseScriptTimeout(opts.scriptTimeout)
		if err != nil {
			return "", "", nil, domainError(err)
		}
	}
	return script, loop.FormatScriptTimeout(d), vars, nil
}

// createLoop adds l under the loops lock and prints it.
func (a *App) createLoop(ctx context.Context, l *loop.Loop) error {
	unlock, err := a.loops.Lock(ctx)
	if err != nil {
		return domainError(err)
	}
	defer unlock()
	if err := a.loops.Create(l); err != nil {
		return domainError(err)
	}
	return a.writeLoop(l)
}

func (a *App) runLoopList(ctx context.Context) error {
	loops, err := a.readLoops(ctx)
	if err != nil {
		return err
	}
	if a.global.json {
		return a.writeJSON(loopListResult{Loops: loopsJSONOf(loops), Warnings: a.warnings()})
	}
	for i := range loops {
		fmt.Fprintln(a.stdout, loopLine(&loops[i]))
	}
	a.printWarnings()
	return nil
}

// readLoops reads every loop under the shared lock, and keeps the warnings
// about the loops it skipped.
func (a *App) readLoops(ctx context.Context) ([]loop.Loop, error) {
	unlock, err := a.loops.RLock(ctx)
	if err != nil {
		return nil, domainError(err)
	}
	defer unlock()
	loops, warnings, err := a.loops.List()
	if err != nil {
		return nil, domainError(err)
	}
	a.warned = append(a.warned, warnings...)
	return loops, nil
}

func loopsJSONOf(loops []loop.Loop) []loopJSON {
	out := make([]loopJSON, 0, len(loops))
	for i := range loops {
		out = append(out, loopJSONOf(&loops[i]))
	}
	return out
}

func (a *App) runLoopPause(ctx context.Context, name string) error {
	l, err := a.changeLoop(ctx, name, func(l *loop.Loop) (bool, error) {
		if l.Paused {
			return false, nil
		}
		l.Paused = true
		return true, nil
	})
	if err != nil {
		return err
	}
	return a.writeLoop(&l)
}

func (a *App) runLoopResume(ctx context.Context, name string) error {
	l, err := a.changeLoop(ctx, name, func(l *loop.Loop) (bool, error) {
		if !l.Paused {
			return false, nil
		}
		return true, l.Resume(a.now())
	})
	if err != nil {
		return err
	}
	return a.writeLoop(&l)
}

// changeLoop reads the loop name under the lock, applies change, and saves
// it when change reports a change. It returns the loop either way.
func (a *App) changeLoop(ctx context.Context, name string, change func(l *loop.Loop) (bool, error)) (loop.Loop, error) {
	unlock, err := a.loops.Lock(ctx)
	if err != nil {
		return loop.Loop{}, domainError(err)
	}
	defer unlock()
	l, err := a.loops.Get(name)
	if err != nil {
		return loop.Loop{}, domainError(err)
	}
	changed, err := change(&l)
	if err != nil {
		return loop.Loop{}, domainError(err)
	}
	if !changed {
		return l, nil
	}
	if err := a.loops.Save(&l); err != nil {
		return loop.Loop{}, domainError(err)
	}
	return l, nil
}

func (a *App) runLoopRm(ctx context.Context, name string) error {
	l, err := a.removeLoop(ctx, name)
	if err != nil {
		return err
	}
	return a.writeLoop(&l)
}

// removeLoop deletes the loop name under the lock and returns it as it was.
func (a *App) removeLoop(ctx context.Context, name string) (loop.Loop, error) {
	unlock, err := a.loops.Lock(ctx)
	if err != nil {
		return loop.Loop{}, domainError(err)
	}
	defer unlock()
	l, err := a.loops.Get(name)
	if err != nil {
		return loop.Loop{}, domainError(err)
	}
	if err := a.loops.Remove(name); err != nil {
		return loop.Loop{}, domainError(err)
	}
	return l, nil
}

// writeLoop prints one loop: its JSON, or its text line.
func (a *App) writeLoop(l *loop.Loop) error {
	if a.global.json {
		return a.writeJSON(loopResult{loopJSON: loopJSONOf(l), Warnings: a.warnings()})
	}
	fmt.Fprintln(a.stdout, loopLine(l))
	a.printWarnings()
	return nil
}
