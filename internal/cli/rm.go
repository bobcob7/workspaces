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
	dryRun       bool
	keepChildren bool
	workspaces   []string
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
	cmd.Flags().BoolVar(&opts.keepChildren, "keep-children", false, "make the workspaces it started top-level instead of removing them")
	return cmd
}

// rmJSON is one workspace in ws rm's output (docs/tools/ws-rm.md).
type rmJSON struct {
	ID string `json:"id"`
	// Parent is the workspace it was started from, for a descendant
	// removed with it (ADR 0044).
	Parent  *string        `json:"parent"`
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

// runRm removes each workspace named, with its session and its
// descendants, deepest first (ADR 0044), each reported as an item. A
// failure doesn't stop the others; the exit code is the first failure's
// (ADR 0016).
func (a *App) runRm(ctx context.Context, opts *rmOptions) error {
	var items itemResults
	results := []rmJSON{}
	done := map[string]bool{}
	targets := a.rmTargets(opts.workspaces)
	for i := range targets {
		t := &targets[i]
		if t.err != nil || t.swept != nil {
			r, err := t.unremovable()
			id := t.arg
			if err == nil {
				id = r.Workspace.ID
			}
			removed := err == nil && t.swept != nil
			results = append(results, rmJSON{ID: id, Removed: removed, Error: items.add(rmLine(&r, removed, false), err)})
			continue
		}
		if !done[t.ws.ID] {
			results = append(results, a.rmTree(ctx, &t.ws, opts, done, &items)...)
		}
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
	for i := range a.swept {
		r := &a.swept[i]
		if arg == r.Workspace.ID || path == filepath.Clean(r.Workspace.Path) || (r.SessionID != "" && arg == r.SessionID) {
			return *r, true
		}
	}
	return lifetime.Removed{}, false
}

// rmTree removes ws and its descendants and returns an item for each that
// done doesn't already hold, adding it to done.
func (a *App) rmTree(ctx context.Context, ws *workspace.Workspace, opts *rmOptions, done map[string]bool, items *itemResults) []rmJSON {
	tree, err := a.lifetime.RemoveTree(ctx, ws, lifetime.TreeOptions{DryRun: opts.dryRun, KeepChildren: opts.keepChildren})
	if err != nil {
		tree = []lifetime.TreeItem{{Workspace: *ws, Err: err}}
	}
	var results []rmJSON
	for j := range tree {
		item := &tree[j]
		if done[item.Workspace.ID] {
			continue
		}
		done[item.Workspace.ID] = true
		child := item.Workspace.ID != ws.ID
		removed := item.Err == nil && !opts.dryRun
		r := rmJSON{ID: item.Workspace.ID, Removed: removed, Error: items.add(rmLine(&item.Removed, removed, child), item.Err)}
		if child {
			r.Parent = &item.Workspace.Parent
		}
		results = append(results, r)
	}
	return results
}

// unremovable is a target that names no workspace, or one the removal
// pass already removed, which needs nothing more.
func (t *rmTarget) unremovable() (lifetime.Removed, error) {
	if t.err != nil {
		return lifetime.Removed{}, t.err
	}
	return *t.swept, nil
}

// rmLine is the text output for a workspace removed, or that would be; a
// descendant removed with the one named is marked "└ ".
func rmLine(r *lifetime.Removed, removed, child bool) string {
	verb := "removed"
	if !removed {
		verb = "would remove"
	}
	id := r.Workspace.ID
	if child {
		id = "└ " + id
	}
	if r.SessionID == "" {
		return fmt.Sprintf("%s  %s", verb, id)
	}
	return fmt.Sprintf("%s  %s  %s", verb, id, r.SessionID)
}
