package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type claudeOptions struct {
	name       string
	offline    bool
	hidden     bool
	parent     string
	env        string
	runner     string
	endpoint   string
	specs      []string
	claudeArgs []string
	// started, when set, runs after the session is recorded and before the
	// result is printed (ADR 0050).
	started func(ws *workspace.Workspace, session string)
	// result, when set, is what --json prints instead of r.
	result func(r claudeResult) any
}

func (a *App) newClaudeCmd() *cobra.Command {
	var opts claudeOptions
	cmd := &cobra.Command{
		Use:   "claude [OPTIONS] [<SPEC>...] [-- <CLAUDE ARGS>...]",
		Short: "New workspace with a Claude Code background agent",
		Long:  "New workspace with a Claude Code background agent.\n\nFor people and the desktop app. Agents use ws machine spawn instead; see `ws llm machine spawn`.",
		Args:  allArgs(specsBeforeDash, claudeArgsAfterDash, claudeRunnerArgs(&opts)),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs, opts.claudeArgs = splitAtDash(cmd, args)
			opts.result = claudeAgentResult(opts.claudeArgs)
			return a.runClaude(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	cmd.Flags().BoolVar(&opts.hidden, "hidden", false, "hide the workspace from ws list and the desktop app's default views")
	cmd.Flags().StringVar(&opts.parent, "parent", "", "the workspace this one is removed with, or none; default: the one ws runs in")
	cmd.Flags().StringVar(&opts.env, "env", "", "run it on this environment (ws env list); default: local, or the parent's")
	cmd.Flags().StringVar(&opts.runner, "runner", config.RunnerClaude, "claude or ws: which agent runs it; a ws agent needs a prompt and can't be attached")
	cmd.Flags().StringVar(&opts.endpoint, "endpoint", "", "with --runner ws, another endpoint from config.toml")
	return cmd
}

// claudeResult is ws claude's output (docs/tools/ws-claude.md).
type claudeResult struct {
	ID      string `json:"id"`
	Session string `json:"session"`
	Path    string `json:"path"`
	// Parent is the workspace this one is removed with (ADR 0044).
	Parent   *string      `json:"parent"`
	Repos    []claudeRepo `json:"repos"`
	Warnings []string     `json:"warnings"`
	// Runner is what runs the agent: claude, or ws (ADR 0070).
	Runner string `json:"runner"`
	// Model is a Claude agent's --model, or a ws agent's model id; null when none.
	Model *string `json:"model"`
	// WSRunner is a ws agent's runner details; null for a Claude agent.
	WSRunner *spawnWSRunnerJSON `json:"ws_runner"`
}

// claudeAgentResult is ws claude's JSON for a Claude agent: runner "claude",
// the --model after "--" or null, and no ws_runner (ADR 0070).
func claudeAgentResult(claudeArgs []string) func(claudeResult) any {
	return func(r claudeResult) any {
		r.Runner = lifetime.RunnerClaude
		r.Model = optional(claudeModel(claudeArgs))
		return r
	}
}

// claudeRunnerArgs checks ws claude's arguments before anything is created.
func claudeRunnerArgs(opts *claudeOptions) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		_, after := splitAtDash(cmd, args)
		return checkClaudeRunner(opts.runner, opts.endpoint, after)
	}
}

// checkClaudeRunner is usage unless the runner's arguments fit it: a ws agent
// takes a prompt and then only --model, and --endpoint needs a ws agent (ADR 0070).
func checkClaudeRunner(runner, endpoint string, after []string) error {
	switch runner {
	case "", config.RunnerClaude:
		if endpoint != "" {
			return usageErrorf("--endpoint needs --runner ws")
		}
		return nil
	case config.RunnerWS:
		_, _, err := runnerPromptArgs(after)
		return err
	}
	return usageErrorf("--runner must be claude or ws, not %q", runner)
}

// runnerPromptArgs reads a ws agent's arguments after "--": the prompt, which
// is required, then only --model <m>.
func runnerPromptArgs(after []string) (prompt, model string, err error) {
	if len(after) == 0 || strings.TrimSpace(after[0]) == "" {
		return "", "", usageErrorf("a ws runner agent needs a prompt; it can't be attached")
	}
	model, err = runnerModelArgs(after[1:])
	return after[0], model, err
}

type claudeRepo struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	RefKind string `json:"ref_kind"`
	Commit  string `json:"commit"`
}

// runClaude creates the workspace and hands it to a new Claude Code
// background session, started in the repo's directory when there is
// exactly one and Claude Code can trust it (ADRs 0015, 0025, 0028).
// Creation's lock stays held until the session record, with the session's
// id, is written, so the workspace never looks abandoned while
// `claude --bg` runs. If the session can't start, or can't be recorded,
// the workspace is removed. A session started without a prompt is
// attached to when ws runs on a terminal without --json and wasn't
// interrupted or hung up on; otherwise ws prints the result and exits.
// With --runner ws it starts a runner agent instead (runWSClaude).
func (a *App) runClaude(ctx context.Context, opts *claudeOptions) error {
	runOn, parentID, err := a.remoteParent(ctx, opts.env, opts.parent)
	if err != nil {
		return err
	}
	if runOn != "" {
		return a.runRemoteClaude(ctx, opts, runOn, parentID)
	}
	if opts.runner == config.RunnerWS {
		return a.runWSClaude(ctx, opts)
	}
	if err = a.claude.Check(ctx); err != nil {
		return domainError(err)
	}
	parent, err := a.enclosingOr(opts.parent)
	if err != nil {
		return err
	}
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: opts.name, Specs: parseSpecs(opts.specs), Offline: opts.offline, Hidden: opts.hidden, Parent: parent, Environment: a.environment})
	if err != nil {
		a.printWarnings()
		return domainError(err)
	}
	defer lock.Unlock()
	started, hungUp, err := a.handOff(ctx, &ws, opts.claudeArgs)
	if err != nil {
		return err
	}
	lock.Unlock()
	if opts.started != nil {
		opts.started(&ws, started.ID)
	}
	if a.shouldAttach(ctx, started, hungUp) && a.attach(ctx, &ws, started.ID) {
		return nil
	}
	return a.printClaudeResult(&ws, started.ID, opts.result)
}

// runRemoteClaude builds envproto.CreateArgs from opts (kind runner for
// --runner ws; the prompt and --model from after "--"), calls
// workspace.create on environment env, and prints the agent's result with
// "environment" added (D9). It never attaches.
func (a *App) runRemoteClaude(ctx context.Context, opts *claudeOptions, env, parent string) error {
	args, err := remoteCreateArgs(opts, parent)
	if err != nil {
		return err
	}
	result, err := a.callEnvironment(ctx, env, envproto.OpWorkspaceCreate, args)
	if err != nil {
		return err
	}
	object, err := remoteObject(result, env)
	if err != nil {
		return err
	}
	if !a.global.json {
		return a.printRemoteClaude(object, env)
	}
	name, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encoding the environment name: %w", err)
	}
	object["environment"] = name
	if err := addWarnings(object, a.warned); err != nil {
		return err
	}
	return a.writeJSON(object)
}

// remoteObject reads an agent's result, which must be a JSON object.
func remoteObject(result json.RawMessage, env string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(result, &object); err != nil {
		return nil, newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	if object == nil {
		return nil, newError(codeInternal, fmt.Errorf("reading the result of %s: not a JSON object", env))
	}
	return object, nil
}

// addWarnings appends warnings to the "warnings" list of the agent's result,
// which is always a list.
func addWarnings(object map[string]json.RawMessage, warnings []string) error {
	all := []string{}
	if raw, ok := object["warnings"]; ok {
		if err := json.Unmarshal(raw, &all); err != nil {
			return newError(codeInternal, fmt.Errorf("reading the warnings of the result: %w", err))
		}
	}
	merged := slices.Concat(all, warnings)
	if merged == nil {
		merged = []string{}
	}
	data, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("encoding the warnings: %w", err)
	}
	object["warnings"] = data
	return nil
}

// printRemoteClaude prints "<id>  <session>  <path>  (on <name>)", with the
// agent's warnings and ws's own on stderr.
func (a *App) printRemoteClaude(object map[string]json.RawMessage, env string) error {
	var shown struct {
		ID       string   `json:"id"`
		Session  string   `json:"session"`
		Path     string   `json:"path"`
		Warnings []string `json:"warnings"`
	}
	raw, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("encoding the result: %w", err)
	}
	if err := json.Unmarshal(raw, &shown); err != nil {
		return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	for _, w := range slices.Concat(a.warned, shown.Warnings) {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	fmt.Fprintf(a.stdout, "%s  %s  %s  (on %s)\n", shown.ID, shown.Session, shown.Path, env)
	return nil
}

// remoteCreateArgs is workspace.create's arguments for opts: kind runner for
// --runner ws, with the prompt and --model from after "--". Any other
// argument there is usage.
func remoteCreateArgs(opts *claudeOptions, parent string) (*envproto.CreateArgs, error) {
	args := &envproto.CreateArgs{
		Kind: envproto.KindClaude, Specs: slices.Clone(opts.specs), Name: optional(opts.name),
		Parent: optional(parent), Hidden: opts.hidden, Offline: opts.offline,
	}
	if args.Specs == nil {
		args.Specs = []string{}
	}
	if opts.runner == config.RunnerWS {
		prompt, model, err := runnerPromptArgs(opts.claudeArgs)
		if err != nil {
			return nil, err
		}
		args.Kind, args.Prompt, args.Model, args.Endpoint = envproto.KindRunner, &prompt, optional(model), optional(opts.endpoint)
		return args, nil
	}
	prompt, model, err := remotePromptArgs(opts.claudeArgs)
	if err != nil {
		return nil, err
	}
	args.Prompt, args.Model = optional(prompt), optional(model)
	return args, nil
}

// remotePromptArgs reads the arguments after "--" for an agent on an
// environment: an optional prompt, then an optional --model <m>.
func remotePromptArgs(after []string) (prompt, model string, err error) {
	rest := after
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		prompt, rest = rest[0], rest[1:]
	}
	switch {
	case len(rest) == 0:
		return prompt, "", nil
	case len(rest) == 2 && rest[0] == "--model" && rest[1] != "":
		return prompt, rest[1], nil
	}
	return "", "", usageErrorf("only a prompt and --model can be passed to an agent on an environment")
}

// runWSClaude starts ws's own runner agent in a new workspace, with the
// parent ws claude chooses (ADR 0070). It never attaches a terminal.
func (a *App) runWSClaude(ctx context.Context, opts *claudeOptions) error {
	prompt, model, err := runnerPromptArgs(opts.claudeArgs)
	if err != nil {
		return err
	}
	parent, err := a.enclosingOr(opts.parent)
	if err != nil {
		return err
	}
	started, err := a.startRunnerAgent(ctx, runnerStart{
		Name: opts.name, Parent: parent, Endpoint: opts.endpoint, Model: model,
		Specs: opts.specs, Hidden: opts.hidden, Offline: opts.offline, Prompt: prompt,
	})
	if err != nil {
		return err
	}
	return a.printClaudeResult(&started.Workspace, started.Session, func(r claudeResult) any {
		r.Runner = lifetime.RunnerWS
		r.Model = optional(started.Resolved.ModelID)
		r.WSRunner = wsRunnerJSON(&started.Resolved)
		return r
	})
}

// handOff runs startAgent while catching SIGHUP, so closing the terminal
// can't kill ws between starting `claude --bg` and writing the session
// record (ADR 0025). hungUp reports whether SIGHUP arrived meanwhile.
// SIGQUIT isn't caught: it stays the way out of a `claude --bg` that hangs.
func (a *App) handOff(ctx context.Context, ws *workspace.Workspace, args []string) (started claude.Started, hungUp bool, err error) {
	hangups := make(chan os.Signal, 1)
	a.notify(hangups, syscall.SIGHUP)
	started, err = a.startAgent(ctx, ws, args)
	a.stopNotify(hangups)
	return started, len(hangups) > 0, err
}

// startAgent starts the session in the workspace and records it, while
// the caller holds the workspace's lock. On failure the workspace, and a
// session that started but couldn't be recorded, are removed. Once
// started, `claude --bg` runs to its end whatever happens to ctx, so an
// interruption before then removes the workspace instead of starting it.
func (a *App) startAgent(ctx context.Context, ws *workspace.Workspace, args []string) (claude.Started, error) {
	if err := ctx.Err(); err != nil {
		a.removeWorkspace(ws.ID)
		return claude.Started{}, fmt.Errorf("creating workspace %s: %w", ws.ID, err)
	}
	started, err := a.claude.Start(ctx, claude.StartOptions{Dir: a.agentDir(ctx, ws), Name: ws.ID, Args: args})
	if err != nil {
		a.removeWorkspace(ws.ID)
		return claude.Started{}, a.startError(err)
	}
	if err := a.workspaces.RecordSession(ws.ID, workspace.SessionClaude, started.ID); err != nil {
		a.removeSession(ctx, started.ID)
		a.removeWorkspace(ws.ID)
		return claude.Started{}, err
	}
	return started, nil
}

// agentDir is the directory the session starts in (ADR 0028): the one
// repo's directory, once ws has trusted it in Claude Code below the
// trusted workspace root, else the workspace's. A trust ws adds is
// recorded first, so removing the workspace, on any path, takes it back. A fresh clone is a new
// repository to Claude Code, which the root's trust doesn't cover. With
// the root untrusted, the session starts in the workspace, so the trust
// error names the root; when trusting fails otherwise, ws warns and does
// the same.
func (a *App) agentDir(ctx context.Context, ws *workspace.Workspace) string {
	if ws.SessionSubdir() == "" {
		return ws.Path
	}
	dir := ws.SessionDir()
	record := func(dir string, created bool) error { return a.workspaces.RecordTrust(ws.ID, dir, created) }
	err := a.claude.Trust(ctx, dir, a.paths.Workspaces, record)
	if err == nil {
		return dir
	}
	if !errors.Is(err, claude.ErrParentUntrusted) {
		a.warned = append(a.warned, fmt.Sprintf("starting the agent in the workspace, not in its repo: %v", err))
	}
	return ws.Path
}

// startError is the error for a failed `claude --bg`. An untrusted
// workspace says which directory to trust: the workspace root, which
// covers every workspace (ADR 0015).
func (a *App) startError(err error) error {
	if !errors.Is(err, claude.ErrUntrusted) {
		return domainError(err)
	}
	root := a.paths.Workspaces
	ce := newError(codeClaudeUntrusted, fmt.Errorf(
		"the workspace was removed: Claude Code doesn't trust it\n"+
			"Trust the workspace root once: run `claude` in %q, accept the trust prompt, then retry", root))
	ce.details = map[string]any{"directory": root, "stderr": commandStderr(err)}
	return ce
}

// removeSession deletes a session whose workspace is being removed, even
// if ctx has ended. A failure is reported, with the command that finishes
// the job.
func (a *App) removeSession(ctx context.Context, id string) {
	if err := a.claude.Remove(context.WithoutCancel(ctx), id); err != nil {
		fmt.Fprintf(a.stderr, "ws: couldn't remove Claude Code session %s; run `claude rm %s`: %v\n", id, id, err)
	}
}

// shouldAttach reports whether ws attaches to the session instead of
// printing: the session is waiting for a prompt, a person is at the
// terminal, --json wasn't asked for, and ws hasn't been interrupted or
// hung up on.
func (a *App) shouldAttach(ctx context.Context, started claude.Started, hungUp bool) bool {
	return started.Idle && a.interactive && !a.global.json && ctx.Err() == nil && !hungUp
}

// attach says on stderr which workspace the session is in, then hands
// the terminal to `claude attach`, which replaces ws, so it returns only
// if that couldn't happen. Then ws says so and returns false, to print its
// result instead: the session and workspace exist either way.
func (a *App) attach(ctx context.Context, ws *workspace.Workspace, id string) bool {
	if !a.global.quiet {
		fmt.Fprintf(a.stderr, "ws: workspace %s at %s; attaching to Claude Code session %s\n", ws.ID, ws.Path, id)
	}
	err := a.claude.Attach(ctx, id)
	if err == nil {
		return true
	}
	fmt.Fprintf(a.stderr, "ws: warning: couldn't attach to session %s; run `claude attach %s`: %v\n", id, id, err)
	return false
}

// printClaudeResult prints the workspace id, the session id, and the
// workspace path, or with --json the result object, which result, when
// set, turns into the object to print.
func (a *App) printClaudeResult(ws *workspace.Workspace, session string, result func(claudeResult) any) error {
	if a.global.json {
		r := a.claudeResultOf(ws, session)
		if result != nil {
			return a.writeJSON(result(r))
		}
		return a.writeJSON(r)
	}
	for _, w := range a.warnings() {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	fmt.Fprintf(a.stdout, "%s  %s  %s\n", ws.ID, session, ws.Path)
	return nil
}

// claudeResultOf is ws claude's JSON result for session in ws.
func (a *App) claudeResultOf(ws *workspace.Workspace, session string) claudeResult {
	repos := make([]claudeRepo, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		repos = append(repos, claudeRepo{ID: r.ID, Path: r.Path, Ref: r.Ref, RefKind: r.RefKind, Commit: r.Commit})
	}
	return claudeResult{ID: ws.ID, Session: session, Path: ws.Path, Parent: optional(ws.Parent), Repos: repos, Warnings: a.warnings(), Runner: lifetime.RunnerClaude}
}

// specsBeforeDash checks the repo specs before "--". There may be none,
// for an empty workspace.
func specsBeforeDash(cmd *cobra.Command, args []string) error {
	specs, _ := splitAtDash(cmd, args)
	return validSpecs(cmd, specs)
}

// claudeArgsAfterDash refuses --settings among the arguments for claude,
// before anything is created (ADR 0024).
func claudeArgsAfterDash(cmd *cobra.Command, args []string) error {
	_, after := splitAtDash(cmd, args)
	if err := claude.CheckArgs(after); err != nil {
		return newError(codeUsage, err)
	}
	return nil
}

// splitAtDash separates the arguments before "--" from those after it.
func splitAtDash(cmd *cobra.Command, args []string) (before, after []string) {
	n := cmd.ArgsLenAtDash()
	if n < 0 {
		return args, nil
	}
	return args[:n], args[n:]
}

// addCreateFlags adds the flags shared by commands that create a workspace.
func addCreateFlags(cmd *cobra.Command, name *string, offline *bool) {
	cmd.Flags().StringVar(name, "name", "", "derive the workspace id from this name")
	cmd.Flags().BoolVar(offline, "offline", false, "don't fetch; use the cache as-is")
}
