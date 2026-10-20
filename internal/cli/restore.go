package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type restoreOptions struct {
	dryRun     bool
	workspaces []string
}

func (a *App) newRestoreCmd() *cobra.Command {
	var opts restoreOptions
	cmd := &cobra.Command{
		Use:   "restore [OPTIONS] [<WORKSPACE>...]",
		Short: "Restart agents that aren't running, after a reboot or crash",
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.workspaces = args
			return a.runRestore(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be restarted, change nothing")
	return cmd
}

// restoreJSON is one workspace in ws restore's output
// (docs/tools/ws-restore.md).
type restoreJSON struct {
	ID       string          `json:"id"`
	Session  *string         `json:"session"`
	State    *lifetime.State `json:"state"`
	Restored bool            `json:"restored"`
	Error    *itemErrorJSON  `json:"error"`
}

// restoreItem is one workspace ws restore reports: what restoring it
// found, or why arg names no workspace to restore.
type restoreItem struct {
	arg string
	r   lifetime.Restored
	err error
}

// runRestore respawns the agents that aren't running, of every agent
// workspace or of those named, after the removal pass (ADRs 0015, 0026,
// 0027). A failure doesn't stop the others; the exit code is the first
// failure's (ADR 0016). If Claude Code's sessions can't be listed, nothing
// is restored and the command fails.
func (a *App) runRestore(ctx context.Context, opts *restoreOptions) error {
	items, err := a.restoreItems(ctx, opts)
	if err != nil {
		return domainError(err)
	}
	var results itemResults
	out := make([]restoreJSON, 0, len(items))
	verbs, ids := restoreWidths(items, opts.dryRun)
	for i := range items {
		it := &items[i]
		line := fmt.Sprintf("%-*s  %-*s  %s", verbs, it.verb(opts.dryRun), ids, it.id(), it.r.SessionID)
		out = append(out, it.json(results.add(line, it.err)))
	}
	return a.finishItems("workspaces", out, &results)
}

// restoreItems restores every agent workspace, or each workspace named.
func (a *App) restoreItems(ctx context.Context, opts *restoreOptions) ([]restoreItem, error) {
	if len(opts.workspaces) == 0 {
		restored, err := a.lifetime.Restore(ctx, lifetime.RestoreOptions{All: true, DryRun: opts.dryRun})
		if err != nil {
			return nil, err
		}
		items := make([]restoreItem, 0, len(restored))
		for i := range restored {
			items = append(items, restoreItem{r: restored[i], err: restored[i].Err})
		}
		return items, nil
	}
	items := a.restoreArgs(opts.workspaces)
	var named []workspace.Workspace
	for i := range items {
		if items[i].err == nil {
			named = append(named, items[i].r.Workspace)
		}
	}
	restored, err := a.lifetime.Restore(ctx, lifetime.RestoreOptions{Workspaces: named, DryRun: opts.dryRun})
	if err != nil {
		return nil, err
	}
	return mergeRestored(items, restored)
}

// mergeRestored puts what Restore found for each named workspace, in
// order, into the item that named it.
func mergeRestored(items []restoreItem, restored []lifetime.Restored) ([]restoreItem, error) {
	j := 0
	for i := range items {
		if items[i].err != nil {
			continue
		}
		if j == len(restored) {
			return nil, fmt.Errorf("restoring: only %d results for the workspaces named", len(restored))
		}
		items[i].r, items[i].err = restored[j], restored[j].Err
		j++
	}
	return items, nil
}

// restoreArgs resolves every argument before anything is restored, so two
// arguments naming one workspace restore it once.
func (a *App) restoreArgs(args []string) []restoreItem {
	var items []restoreItem
	seen := map[string]bool{}
	for _, arg := range args {
		it := a.restoreArg(arg)
		if it.err == nil {
			if seen[it.r.Workspace.ID] {
				continue
			}
			seen[it.r.Workspace.ID] = true
		}
		items = append(items, it)
	}
	return items
}

// restoreArg finds the workspace arg names. One the removal pass removed
// just before isn't found, and the error says why.
func (a *App) restoreArg(arg string) restoreItem {
	ws, err := a.workspaces.Find(arg)
	if err == nil {
		return restoreItem{arg: arg, r: lifetime.Restored{Workspace: ws}}
	}
	if r, ok := a.sweptMatch(arg); ok && errors.Is(err, workspace.ErrNotFound) {
		err = fmt.Errorf("%w: %s was removed just now, as its Claude Code session is gone", workspace.ErrNotFound, r.Workspace.ID)
	}
	return restoreItem{arg: arg, err: err}
}

// id is the workspace's id, or the argument that named none.
func (it *restoreItem) id() string {
	if it.r.Workspace.ID == "" {
		return it.arg
	}
	return it.r.Workspace.ID
}

// verb is the first column of the text output: what restore did, or
// would do, or why it left the session alone.
func (it *restoreItem) verb(dryRun bool) string {
	switch {
	case it.r.Respawned:
		return "restored"
	case it.r.State == lifetime.StateStopped && dryRun:
		return "would restore"
	case it.r.State == lifetime.StateIdle, it.r.State == lifetime.StateWorking:
		return "running"
	}
	return string(it.r.State)
}

func (it *restoreItem) json(itemErr *itemErrorJSON) restoreJSON {
	out := restoreJSON{ID: it.id(), Session: optional(it.r.SessionID), Restored: it.r.Respawned, Error: itemErr}
	if it.r.State != "" {
		out.State = &it.r.State
	}
	return out
}

// restoreWidths are the widths of the text output's first two columns,
// over the items printed on stdout.
func restoreWidths(items []restoreItem, dryRun bool) (verbs, ids int) {
	for i := range items {
		if items[i].err != nil {
			continue
		}
		verbs = max(verbs, len(items[i].verb(dryRun)))
		ids = max(ids, len(items[i].id()))
	}
	return verbs, ids
}
