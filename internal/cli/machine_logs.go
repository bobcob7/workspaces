package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/runner"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// defaultMachineLogsTail is ws machine logs' --tail default.
const defaultMachineLogsTail = 200

type machineLogsOptions struct {
	tail int
}

// machineLogsJSON is ws machine logs' output (docs/design reference.md).
type machineLogsJSON struct {
	ID      string        `json:"id"`
	Session *string       `json:"session"`
	Runner  *string       `json:"runner"`
	Model   *string       `json:"model"`
	Events  []agent.Event `json:"events"`
	Text    *string       `json:"text"`
	// Truncated is whether --tail left older events out.
	Truncated bool     `json:"truncated"`
	Warnings  []string `json:"warnings"`
}

// logChild is a child ws machine logs reads: a listed workspace, or a
// removed one whose runner state is kept.
type logChild struct {
	id    string
	entry *lifetime.Entry
	// runDir is the runner's directory of a removed child, else "".
	runDir string
}

func (a *App) newMachineLogsCmd() *cobra.Command {
	var opts machineLogsOptions
	cmd := &cobra.Command{
		Use:   "logs [--tail <N>] <WORKSPACE>",
		Short: "Print a child's log: a ws runner's transcript, or Claude's log",
		Args:  exactlyOneArg("a workspace, or a session id"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runMachineLogs(cmd.Context(), args[0], &opts)
		})),
	}
	cmd.Flags().IntVar(&opts.tail, "tail", defaultMachineLogsTail, "the last n events; 0 for all")
	return cmd
}

// runMachineLogs prints the log of the child arg names, which must be one of
// the caller's descendants.
func (a *App) runMachineLogs(ctx context.Context, arg string, opts *machineLogsOptions) error {
	if opts.tail < 0 {
		return usageErrorf("--tail must not be negative, got %d", opts.tail)
	}
	caller, err := a.caller()
	if err != nil {
		return err
	}
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	root := machine.Root(entryWorkspaces(listing.Workspaces), caller)
	child, err := a.logChildOf(arg, listing.Workspaces, caller, root)
	if err != nil {
		return err
	}
	out, err := a.machineLog(ctx, &child, opts.tail)
	if err != nil {
		return err
	}
	return a.writeJSON(out)
}

// logChildOf resolves arg, a workspace (by id, name or path) or a session id,
// that must be a descendant of caller. A removed child is named by its id
// while its runner state is kept; any other unknown id is
// workspace_not_found.
func (a *App) logChildOf(arg string, entries []lifetime.Entry, caller, root string) (logChild, error) {
	below := descendants(entryWorkspaces(entries), caller)
	ws, err := a.workspaces.Find(arg)
	switch {
	case err == nil:
		return listedLogChild(ws.ID, entries, below)
	case !errors.Is(err, workspace.ErrNotFound):
		return logChild{}, domainError(err)
	}
	if i := slices.IndexFunc(entries, func(e lifetime.Entry) bool { return e.Session != nil && e.Session.ID == arg }); i >= 0 {
		return listedLogChild(entries[i].Workspace.ID, entries, below)
	}
	return a.removedLogChild(arg, caller, root)
}

// listedLogChild is the listed workspace id, if it's a descendant of caller.
func listedLogChild(id string, entries []lifetime.Entry, below map[string]bool) (logChild, error) {
	if !below[id] {
		return logChild{}, newError(codeNotDescendant, fmt.Errorf("%s is not a workspace this one started", id))
	}
	i := slices.IndexFunc(entries, func(e lifetime.Entry) bool { return e.Workspace.ID == id })
	return logChild{id: id, entry: &entries[i]}, nil
}

// removedLogChild is the removed child id, by its machine record and its
// runner state, which must still exist.
func (a *App) removedLogChild(id, caller, root string) (logChild, error) {
	if !plainID(id) {
		return logChild{}, newError(codeWorkspaceNotFound, fmt.Errorf("no workspace %q", id))
	}
	rec, err := machine.NewStore(a.paths.State).Read(root, id)
	if err != nil {
		return logChild{}, fmt.Errorf("reading %s's machine record: %w", id, err)
	}
	runDir := runner.FilesFor(a.paths.State, id).Dir
	if info, statErr := os.Stat(runDir); statErr != nil || !info.IsDir() {
		return logChild{}, newError(codeWorkspaceNotFound, fmt.Errorf("no workspace %q", id))
	}
	if !rec.Found() || !slices.Contains(rec.Ancestors(), caller) {
		return logChild{}, newError(codeNotDescendant, fmt.Errorf("%s is not a workspace this one started", id))
	}
	return logChild{id: id, runDir: runDir}, nil
}

// plainID reports whether id is one path element, so it can name a state
// directory.
func plainID(id string) bool {
	return id != "" && id == filepath.Base(id) && !strings.HasPrefix(id, ".")
}

// machineLog is the log of child: a ws runner's transcript tail, or the text
// of Claude's log.
func (a *App) machineLog(ctx context.Context, child *logChild, tail int) (machineLogsJSON, error) {
	out := machineLogsJSON{ID: child.id, Events: []agent.Event{}, Warnings: a.warnings()}
	if child.entry == nil {
		err := a.removedRunnerLog(child, tail, &out)
		return out, err
	}
	s := child.entry.Session
	if s == nil {
		return out, nil
	}
	out.Session = optional(s.ID)
	out.Runner = optional(s.Runner)
	out.Model = optional(s.Model)
	var err error
	switch s.Runner {
	case lifetime.RunnerWS:
		err = runnerLog(runner.FilesFor(a.paths.State, child.id), tail, &out)
	case lifetime.RunnerClaude:
		err = a.claudeLog(ctx, s.ID, &out)
	}
	return out, err
}

// removedRunnerLog fills out with a removed ws child's log, its session and
// model from run.toml.
func (a *App) removedRunnerLog(child *logChild, tail int, out *machineLogsJSON) error {
	files := runner.FilesFor(a.paths.State, child.id)
	run, err := files.ReadRun()
	if err != nil {
		return fmt.Errorf("reading %s's run: %w", child.id, err)
	}
	out.Session = optional(run.Session)
	out.Runner = optional(lifetime.RunnerWS)
	out.Model = optional(run.Current().Model)
	return runnerLog(files, tail, out)
}

// runnerLog fills out with the last tail events of a ws runner's transcript.
// A transcript that doesn't exist yet has no events.
func runnerLog(files runner.Files, tail int, out *machineLogsJSON) error {
	n := tail
	if n == 0 {
		n = math.MaxInt
	}
	events, more, err := agent.Tail(files.Transcript(), n)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("reading %s's transcript: %w", files.ID, err)
	}
	out.Events = append(out.Events, events...)
	out.Truncated = more
	return nil
}

// claudeLog fills out with the text of Claude Code session id's log.
func (a *App) claudeLog(ctx context.Context, id string, out *machineLogsJSON) error {
	text, err := a.claude.Logs(ctx, id)
	if err != nil {
		return newError(codeClaudeFailed, err)
	}
	out.Text = &text
	return nil
}
