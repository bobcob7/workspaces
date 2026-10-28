package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentsGroup is ws --help's group for the commands agents run (ADR 0046).
const agentsGroup = "agents"

var errOutsideWorkspace = errors.New("not inside a workspace: ws machine acts for the workspace containing the working directory; cd back into yours")

func (a *App) newMachineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "machine <OP>",
		Short:   "Start, list, and remove the workspaces yours started",
		GroupID: agentsGroup,
		Args:    knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newMachineSpawnCmd(),
		a.newMachineChildrenCmd(),
		a.newMachineRmCmd(),
	)
	return cmd
}

func (a *App) newMachineSpawnCmd() *cobra.Command {
	var opts claudeOptions
	cmd := &cobra.Command{
		Use:   "spawn [OPTIONS] [<SPEC>...] -- <PROMPT> [<CLAUDE ARGS>...]",
		Short: "New child workspace with a Claude Code background agent",
		Args:  allArgs(specsBeforeDash, claudeArgsAfterDash, promptAfterDash),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs, opts.claudeArgs = splitAtDash(cmd, args)
			return a.runMachineSpawn(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	cmd.Flags().BoolVar(&opts.hidden, "hidden", false, "hide the workspace from ws list and the desktop app's default views")
	return cmd
}

func (a *App) newMachineChildrenCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "children [--all]",
		Short: "List the workspaces yours started",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runMachineChildren(cmd.Context(), all)
		})),
	}
	cmd.Flags().BoolVar(&all, "all", false, "every descendant, not only direct children")
	return cmd
}

type machineRmOptions struct {
	rmOptions
	all bool
}

func (a *App) newMachineRmCmd() *cobra.Command {
	var opts machineRmOptions
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] (<WORKSPACE>... | --all)",
		Short: "Remove workspaces yours started, and theirs",
		Args: func(cmd *cobra.Command, args []string) error {
			return checkMachineRmArgs(cmd, opts.all, args)
		},
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.workspaces = args
			return a.runMachineRm(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVar(&opts.all, "all", false, "remove every direct child, and with them their descendants")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be removed, change nothing")
	cmd.Flags().BoolVar(&opts.keepChildren, "keep-children", false, "make the target's children this workspace's instead of removing them")
	return cmd
}

// isAgentCommand reports whether cmd is in ws --help's group for agents,
// or under a command that is. Such commands always print JSON, as if
// --json were given: the root's PersistentPreRun sets it, and execute for
// errors cobra finds before then (ADR 0046).
func isAgentCommand(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.GroupID == agentsGroup {
			return true
		}
	}
	return false
}

// promptAfterDash requires a prompt after "--", so ws machine spawn never
// attaches a terminal (ADR 0046).
func promptAfterDash(cmd *cobra.Command, args []string) error {
	_, after := splitAtDash(cmd, args)
	if len(after) == 0 || strings.TrimSpace(after[0]) == "" {
		return usageErrorf(`%s needs a prompt after "--"`, commandName(cmd))
	}
	return nil
}

// checkMachineRmArgs requires workspaces or --all, not both.
func checkMachineRmArgs(cmd *cobra.Command, all bool, args []string) error {
	switch {
	case all && len(args) > 0:
		return usageErrorf("%s takes workspaces or --all, not both", commandName(cmd))
	case !all && len(args) == 0:
		return usageErrorf("%s needs a workspace, or --all", commandName(cmd))
	}
	return nil
}

// caller is the id of the workspace containing the working directory,
// which every ws machine command acts for, or outside_workspace.
func (a *App) caller() (string, error) {
	id, err := a.enclosingOr("")
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", newError(codeOutsideWorkspace, errOutsideWorkspace)
	}
	return id, nil
}

// runMachineSpawn is ws claude --json --parent <caller>.
func (a *App) runMachineSpawn(ctx context.Context, opts *claudeOptions) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	opts.parent = caller
	return a.runClaude(ctx, opts)
}

// runMachineChildren prints ws list --json -a's object for the caller's
// direct children, or with all every descendant, oldest first.
func (a *App) runMachineChildren(ctx context.Context, all bool) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	entries := childrenOf(listing.Workspaces, caller)
	if all {
		entries = descendantEntries(listing.Workspaces, caller)
	}
	return a.writeJSON(listJSON{Workspaces: a.listWorkspaces(entries, nil), Warnings: a.warnings()})
}

// descendantEntries keeps the entries that descend from workspace id, in
// their order.
func descendantEntries(entries []lifetime.Entry, id string) []lifetime.Entry {
	list := make([]workspace.Workspace, 0, len(entries))
	for i := range entries {
		list = append(list, entries[i].Workspace)
	}
	below := descendants(list, id)
	var kept []lifetime.Entry
	for i := range entries {
		if below[entries[i].Workspace.ID] {
			kept = append(kept, entries[i])
		}
	}
	return kept
}

// descendants is the set of ids of workspace id's descendants in list.
// id itself is never among them, even in a cycle a hand-edited record
// could make.
func descendants(list []workspace.Workspace, id string) map[string]bool {
	kids := map[string][]string{}
	for i := range list {
		kids[list[i].Parent] = append(kids[list[i].Parent], list[i].ID)
	}
	below := map[string]bool{}
	queue := kids[id]
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if c == id || below[c] {
			continue
		}
		below[c] = true
		queue = append(queue, kids[c]...)
	}
	return below
}

// runMachineRm is ws rm, refusing every target that isn't the caller's
// descendant with not_descendant, and giving --keep-children's children
// to the caller.
func (a *App) runMachineRm(ctx context.Context, opts *machineRmOptions) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	for i := range a.swept {
		list = append(list, a.swept[i].Workspace)
	}
	if opts.all {
		opts.workspaces = directChildren(list, caller)
	}
	opts.caller = caller
	targets := a.rmTargets(opts.workspaces)
	refuseOutside(targets, descendants(list, caller))
	return a.removeTargets(ctx, targets, &opts.rmOptions)
}

// directChildren is the ids of workspace id's children in list, in order.
func directChildren(list []workspace.Workspace, id string) []string {
	ids := []string{}
	for i := range list {
		if list[i].Parent == id && list[i].ID != id {
			ids = append(ids, list[i].ID)
		}
	}
	return ids
}

// refuseOutside makes every target that names a workspace outside below
// a not_descendant error.
func refuseOutside(targets []rmTarget, below map[string]bool) {
	for i := range targets {
		t := &targets[i]
		if t.err != nil || below[t.key()] {
			continue
		}
		t.err = newError(codeNotDescendant, fmt.Errorf("%s is not a workspace this one started", t.key()))
	}
}
