package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type claudeOptions struct {
	name       string
	offline    bool
	specs      []string
	claudeArgs []string
}

func (a *App) newClaudeCmd() *cobra.Command {
	var opts claudeOptions
	cmd := &cobra.Command{
		Use:   "claude [OPTIONS] [<SPEC>...] [-- <CLAUDE ARGS>...]",
		Short: "New workspace with a Claude Code background agent",
		Args:  allArgs(specsBeforeDash, claudeArgsAfterDash),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs, opts.claudeArgs = splitAtDash(cmd, args)
			return a.runClaude(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	return cmd
}

// claudeResult is ws claude's output (docs/tools/ws-claude.md).
type claudeResult struct {
	ID       string       `json:"id"`
	Session  string       `json:"session"`
	Path     string       `json:"path"`
	Repos    []claudeRepo `json:"repos"`
	Warnings []string     `json:"warnings"`
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
func (a *App) runClaude(ctx context.Context, opts *claudeOptions) error {
	if err := a.claude.Check(ctx); err != nil {
		return domainError(err)
	}
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: opts.name, Specs: parseSpecs(opts.specs), Offline: opts.offline})
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
	if a.shouldAttach(ctx, started, hungUp) && a.attach(ctx, &ws, started.ID) {
		return nil
	}
	return a.printClaudeResult(&ws, started.ID)
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
// workspace path, or with --json the result object.
func (a *App) printClaudeResult(ws *workspace.Workspace, session string) error {
	if a.global.json {
		return a.writeJSON(a.claudeResultOf(ws, session))
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
	return claudeResult{ID: ws.ID, Session: session, Path: ws.Path, Repos: repos, Warnings: a.warnings()}
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
