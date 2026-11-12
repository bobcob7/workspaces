package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/machine/role"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// agentsGroup is ws --help's group for the commands agents run (ADR 0046).
const agentsGroup = "agents"

// peopleGroup is ws --help's group for the commands people and the desktop
// app run, which agents are told to leave alone (ADR 0046).
const peopleGroup = "people"

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
		a.newMachineResultCmd(),
		a.newMachineWaitCmd(),
		a.newMachineChildrenCmd(),
		a.newMachineRmCmd(),
		a.newMachineRolesCmd(),
		a.newMachineMemoryCmd(),
	)
	return cmd
}

// machineSpawnOptions is ws machine spawn's: ws claude's plus a role.
type machineSpawnOptions struct {
	claudeOptions
	role string
	vars []string
}

// machineSpawnJSON is ws claude --json's object plus the role.
type machineSpawnJSON struct {
	claudeResult
	Role *string `json:"role"`
}

func (a *App) newMachineSpawnCmd() *cobra.Command {
	var opts machineSpawnOptions
	cmd := &cobra.Command{
		Use:   "spawn [OPTIONS] [<SPEC>...] -- <PROMPT> [<CLAUDE ARGS>...]",
		Short: "New child workspace with a Claude Code background agent",
		Args:  machineSpawnArgs(&opts.role),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs, opts.claudeArgs = splitAtDash(cmd, args)
			return a.runMachineSpawn(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	cmd.Flags().BoolVar(&opts.hidden, "hidden", false, "hide the workspace from ws list and the desktop app's default views")
	cmd.Flags().StringVar(&opts.role, "role", "", "start the child from this role (ws machine roles)")
	cmd.Flags().StringArrayVar(&opts.vars, "var", nil, "KEY=VALUE for the role's prompt; repeatable, with --role")
	return cmd
}

// machineSpawnArgs is specsBeforeDash and claudeArgsAfterDash, and
// promptAfterDash only when --role isn't given.
func machineSpawnArgs(roleName *string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if *roleName != "" {
			return allArgs(specsBeforeDash, claudeArgsAfterDash)(cmd, args)
		}
		return allArgs(specsBeforeDash, claudeArgsAfterDash, promptAfterDash)(cmd, args)
	}
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

// runMachineSpawn is ws claude --json --parent <caller>, from the role
// when there is one, and records the role for the child (ADR 0050).
func (a *App) runMachineSpawn(ctx context.Context, opts *machineSpawnOptions) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	if opts.role == "" && len(opts.vars) > 0 {
		return usageErrorf("--var needs --role")
	}
	opts.parent = caller
	opts.result = func(r claudeResult) any {
		return machineSpawnJSON{claudeResult: r, Role: optional(opts.role)}
	}
	if opts.role != "" {
		ro, vars, err := a.spawnRole(opts)
		if err != nil {
			return err
		}
		opts.started = a.recordRole(&ro, vars)
	}
	return a.runClaude(ctx, &opts.claudeOptions)
}

// spawnRole loads the role, checks and renders the prompt, and rewrites
// opts.claudeArgs to role.Args(prompt, <args after the prompt>). It runs
// before runClaude, so nothing is created on an error.
func (a *App) spawnRole(opts *machineSpawnOptions) (role.Role, map[string]string, error) {
	ro, ok, err := role.NewSet(a.paths.MachineRoles).Load(opts.role)
	if err != nil {
		return role.Role{}, nil, newError(codeConfigInvalid, err)
	}
	if !ok {
		return role.Role{}, nil, newError(codeRoleUnknown, fmt.Errorf("no role named %q; ws machine roles lists them", opts.role))
	}
	vars, err := loop.ParseVars(opts.vars)
	if err != nil {
		return role.Role{}, nil, newError(codeUsage, err)
	}
	var extra string
	var after []string
	if len(opts.claudeArgs) > 0 {
		extra, after = opts.claudeArgs[0], opts.claudeArgs[1:]
	}
	prompt, err := ro.Render(vars, extra)
	if err != nil {
		return role.Role{}, nil, newError(codeUsage, err)
	}
	opts.claudeArgs = ro.Args(prompt, after)
	return ro, vars, nil
}

// recordRole is the started hook that writes the child's spawn.toml, so
// ws machine wait and children can judge its result by the role's verdicts
// (ADR 0050). A failure is a warning: the child is already running.
func (a *App) recordRole(ro *role.Role, vars map[string]string) func(*workspace.Workspace, string) {
	return func(ws *workspace.Workspace, _ string) {
		if err := a.writeSpawn(ro, vars, ws.ID); err != nil {
			a.warned = append(a.warned, fmt.Sprintf("couldn't record role %s for %s: %v", ro.Name, ws.ID, err))
		}
	}
}

// writeSpawn writes child's spawn.toml under the root of its workspace tree.
func (a *App) writeSpawn(ro *role.Role, vars map[string]string, child string) error {
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	spawn := &machine.Spawn{
		Role:      ro.Name,
		Model:     ro.Model,
		Pass:      ro.Pass,
		Fail:      ro.Fail,
		Vars:      vars,
		Ancestors: machine.Ancestors(list, child),
		SpawnedAt: a.now().UTC().Truncate(time.Second),
	}
	return machine.NewStore(a.paths.State).WriteSpawn(machine.Root(list, child), child, spawn)
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
	root := machine.Root(entryWorkspaces(listing.Workspaces), caller)
	store := machine.NewStore(a.paths.State)
	listed := a.listWorkspaces(entries, nil, nil)
	children := make([]machineChildJSON, 0, len(listed))
	for i := range listed {
		children = append(children, a.machineChild(store, root, &listed[i]))
	}
	return a.writeJSON(machineChildrenJSON{Workspaces: children, Warnings: a.warnings()})
}

// entryWorkspaces is the workspaces in entries, in their order.
func entryWorkspaces(entries []lifetime.Entry) []workspace.Workspace {
	list := make([]workspace.Workspace, 0, len(entries))
	for i := range entries {
		list = append(list, entries[i].Workspace)
	}
	return list
}

// descendantEntries keeps the entries that descend from workspace id, in
// their order.
func descendantEntries(entries []lifetime.Entry, id string) []lifetime.Entry {
	below := descendants(entryWorkspaces(entries), id)
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
