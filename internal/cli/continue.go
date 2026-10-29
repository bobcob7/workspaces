package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// defaultContinuePoll is how often ws continue --watch runs a pass.
const defaultContinuePoll = time.Minute

type continueOptions struct {
	dryRun     bool
	watch      bool
	workspaces []string
}

func (a *App) newContinueCmd() *cobra.Command {
	var opts continueOptions
	cmd := &cobra.Command{
		Use:   "continue [OPTIONS] [<WORKSPACE>...]",
		Short: "Tell agents stopped by a usage limit to carry on once it resets",
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.workspaces = args
			if opts.watch && a.global.json {
				return usageErrorf("--watch prints text only; run ws continue --json without --watch")
			}
			if opts.watch {
				return a.watchContinue(cmd.Context(), &opts)
			}
			return a.runContinue(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be continued, send nothing")
	cmd.Flags().BoolVar(&opts.watch, "watch", false, "run a pass every minute until interrupted, printing what changed")
	return cmd
}

// continueJSON is one workspace in ws continue's output
// (docs/tools/ws-continue.md).
type continueJSON struct {
	ID      string           `json:"id"`
	Session *string          `json:"session"`
	State   *lifetime.State  `json:"state"`
	Limit   *limitJSON       `json:"limit"`
	Action  *lifetime.Action `json:"action"`
	Error   *itemErrorJSON   `json:"error"`
}

type limitJSON struct {
	Type     string `json:"type"`
	ResetsAt string `json:"resets_at"`
}

// continueItem is one workspace ws continue reports: what continuing it
// found, or why arg names no workspace to continue.
type continueItem struct {
	arg string
	c   lifetime.Continued
	err error
}

// runContinue runs one pass: it continues the agents, of every agent
// workspace or of those named, whose usage limit has reset (ADR 0047). A
// failure doesn't stop the others; the exit code is the first failure's
// (ADR 0016). If Claude Code's sessions can't be listed, nothing is sent
// and the command fails.
func (a *App) runContinue(ctx context.Context, opts *continueOptions) error {
	items, err := a.continueItems(ctx, opts.workspaces, opts.dryRun)
	if err != nil {
		return domainError(err)
	}
	var results itemResults
	out := make([]continueJSON, 0, len(items))
	verbs, ids := continueWidths(items)
	for i := range items {
		it := &items[i]
		out = append(out, it.json(results.add(it.line(verbs, ids), it.err)))
	}
	return a.finishItems("workspaces", out, &results)
}

// watchContinue runs a pass every continuePoll until ctx ends. Each pass
// prints only the sessions it continued or gave up on, and its failures,
// on stderr; a failed pass doesn't end the watch.
func (a *App) watchContinue(ctx context.Context, opts *continueOptions) error {
	warned := 0
	for {
		items, err := a.continueItems(ctx, opts.workspaces, opts.dryRun)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err != nil {
			fmt.Fprintf(a.stderr, "ws: %v\n", domainError(err))
		}
		a.printChanged(items)
		warned = a.printNewWarnings(warned)
		t := time.NewTimer(a.continuePoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-t.C:
		}
	}
}

// printChanged prints, for --watch, the items a pass acted on or failed.
func (a *App) printChanged(items []continueItem) {
	for i := range items {
		it := &items[i]
		switch {
		case it.err != nil:
			fmt.Fprintf(a.stderr, "ws: %v\n", asCLIError(domainError(it.err)))
		case it.c.Action == lifetime.ActionContinued, it.c.Action == lifetime.ActionWouldContinue, it.c.Action == lifetime.ActionGaveUp:
			fmt.Fprintf(a.stdout, "%s  %s\n", formatTime(a.now()), it.line(0, 0))
		}
	}
}

// printNewWarnings prints the warnings after the first printed ones, and
// returns how many have been printed.
func (a *App) printNewWarnings(printed int) int {
	w := a.warnings()
	for _, msg := range w[min(printed, len(w)):] {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", msg)
	}
	return len(w)
}

// continueItems continues every agent workspace, or each workspace named.
func (a *App) continueItems(ctx context.Context, args []string, dryRun bool) ([]continueItem, error) {
	if len(args) == 0 {
		continued, err := a.lifetime.Continue(ctx, lifetime.ContinueOptions{All: true, DryRun: dryRun})
		if err != nil {
			return nil, err
		}
		items := make([]continueItem, 0, len(continued))
		for i := range continued {
			items = append(items, continueItem{c: continued[i], err: continued[i].Err})
		}
		return items, nil
	}
	restoreItems := a.restoreArgs(args)
	items := make([]continueItem, 0, len(restoreItems))
	var named []workspace.Workspace
	for i := range restoreItems {
		items = append(items, continueItem{arg: restoreItems[i].arg, c: lifetime.Continued{Workspace: restoreItems[i].r.Workspace}, err: restoreItems[i].err})
		if restoreItems[i].err == nil {
			named = append(named, restoreItems[i].r.Workspace)
		}
	}
	continued, err := a.lifetime.Continue(ctx, lifetime.ContinueOptions{Workspaces: named, DryRun: dryRun})
	if err != nil {
		return nil, err
	}
	return mergeContinued(items, continued)
}

// mergeContinued puts what Continue found for each named workspace, in
// order, into the item that named it.
func mergeContinued(items []continueItem, continued []lifetime.Continued) ([]continueItem, error) {
	j := 0
	for i := range items {
		if items[i].err != nil {
			continue
		}
		if j == len(continued) {
			return nil, fmt.Errorf("continuing: only %d results for the workspaces named", len(continued))
		}
		items[i].c, items[i].err = continued[j], continued[j].Err
		j++
	}
	return items, nil
}

func (it *continueItem) id() string {
	if it.c.Workspace.ID == "" {
		return it.arg
	}
	return it.c.Workspace.ID
}

// verb is the first column of the text output: what continue did, or
// would do, or the session's state when it isn't limited.
func (it *continueItem) verb() string {
	switch it.c.Action {
	case lifetime.ActionWouldContinue:
		return "would continue"
	case lifetime.ActionGaveUp:
		return "gave up"
	case lifetime.ActionContinued, lifetime.ActionWaiting, lifetime.ActionAsleep:
		return string(it.c.Action)
	}
	return string(it.c.State)
}

// line is the text output's line for the item, its first two columns
// padded to verbs and ids.
func (it *continueItem) line(verbs, ids int) string {
	line := fmt.Sprintf("%-*s  %-*s  %s", verbs, it.verb(), ids, it.id(), it.c.SessionID)
	if l := it.c.Limit; l != nil {
		tense := "resets"
		if it.c.Action != lifetime.ActionWaiting {
			tense = "reset"
		}
		line += fmt.Sprintf("  %s limit %s %s", l.Type, tense, formatTime(l.ResetsAt))
	}
	return line
}

func (it *continueItem) json(itemErr *itemErrorJSON) continueJSON {
	out := continueJSON{ID: it.id(), Session: optional(it.c.SessionID), Error: itemErr}
	if it.c.State != "" {
		out.State = &it.c.State
	}
	if it.c.Action != "" {
		out.Action = &it.c.Action
	}
	if l := it.c.Limit; l != nil {
		out.Limit = &limitJSON{Type: l.Type, ResetsAt: formatTime(l.ResetsAt)}
	}
	return out
}

// continueWidths are the widths of the text output's first two columns,
// over the items printed on stdout.
func continueWidths(items []continueItem) (verbs, ids int) {
	for i := range items {
		if items[i].err != nil {
			continue
		}
		verbs = max(verbs, len(items[i].verb()))
		ids = max(ids, len(items[i].id()))
	}
	return verbs, ids
}
