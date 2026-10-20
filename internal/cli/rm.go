package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type rmOptions struct {
	dryRun     bool
	workspaces []string
}

func (a *App) newRmCmd() *cobra.Command {
	var opts rmOptions
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] <WORKSPACE>...",
		Short: "Remove workspaces and their sessions now",
		Args:  atLeastOneArg("a workspace"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.workspaces = args
			return a.runRm(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be removed, change nothing")
	return cmd
}

// rmJSON is one workspace in ws rm's output (docs/tools/ws-rm.md).
type rmJSON struct {
	ID      string         `json:"id"`
	Removed bool           `json:"removed"`
	Error   *itemErrorJSON `json:"error"`
}

// rmTarget is a WORKSPACE argument: the workspace it names, one the
// removal pass already removed, or why it names none.
type rmTarget struct {
	arg   string
	ws    workspace.Workspace
	swept *lifetime.Removed
	err   error
}

// runRm removes each workspace named, with its session. A failure doesn't
// stop the others; the exit code is the first failure's (ADR 0016).
func (a *App) runRm(ctx context.Context, opts *rmOptions) error {
	var items itemResults
	results := []rmJSON{}
	targets := a.rmTargets(opts.workspaces)
	for i := range targets {
		t := &targets[i]
		r, err := a.rmOne(ctx, t, opts.dryRun)
		id := t.arg
		if err == nil {
			id = r.Workspace.ID
		}
		removed := err == nil && (t.swept != nil || !opts.dryRun)
		results = append(results, rmJSON{ID: id, Removed: removed, Error: items.add(rmLine(&r, removed), err)})
	}
	return a.finishItems("workspaces", results, &items)
}

// rmTargets resolves every argument before anything is removed, so two
// arguments naming one workspace remove it once.
func (a *App) rmTargets(args []string) []rmTarget {
	var targets []rmTarget
	seen := map[string]bool{}
	for _, arg := range args {
		t := a.rmTarget(arg)
		key := t.key()
		if key != "" && seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, t)
	}
	return targets
}

func (t *rmTarget) key() string {
	switch {
	case t.err != nil:
		return ""
	case t.swept != nil:
		return t.swept.Workspace.ID
	}
	return t.ws.ID
}

// rmTarget finds the workspace arg names. One the removal pass removed
// just before counts as removed (ADR 0015).
func (a *App) rmTarget(arg string) rmTarget {
	ws, err := a.workspaces.Find(arg)
	if err == nil {
		return rmTarget{arg: arg, ws: ws}
	}
	if errors.Is(err, workspace.ErrNotFound) {
		if r, ok := a.sweptMatch(arg); ok {
			return rmTarget{arg: arg, swept: &r}
		}
	}
	return rmTarget{arg: arg, err: err}
}

// sweptMatch finds arg among the workspaces the removal pass removed: by
// id, path, or Claude session id.
func (a *App) sweptMatch(arg string) (lifetime.Removed, bool) {
	path, _ := filepath.Abs(arg)
	for _, r := range a.swept {
		if arg == r.Workspace.ID || path == filepath.Clean(r.Workspace.Path) || (r.SessionID != "" && arg == r.SessionID) {
			return r, true
		}
	}
	return lifetime.Removed{}, false
}

// rmOne removes one target, or with dryRun checks that it could. A
// workspace the removal pass already removed needs nothing more.
func (a *App) rmOne(ctx context.Context, t *rmTarget, dryRun bool) (lifetime.Removed, error) {
	switch {
	case t.err != nil:
		return lifetime.Removed{}, t.err
	case t.swept != nil:
		return *t.swept, nil
	}
	return a.lifetime.Remove(ctx, &t.ws, dryRun)
}

// rmLine is the text output for a workspace removed, or that would be.
func rmLine(r *lifetime.Removed, removed bool) string {
	verb := "removed"
	if !removed {
		verb = "would remove"
	}
	if r.SessionID == "" {
		return fmt.Sprintf("%s  %s", verb, r.Workspace.ID)
	}
	return fmt.Sprintf("%s  %s  %s", verb, r.Workspace.ID, r.SessionID)
}
