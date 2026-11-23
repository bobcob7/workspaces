package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// progressJSON is one workspace's own progress, or null fields when it has
// reported none (ADR 0077).
type progressJSON struct {
	Name      *string `json:"name"`
	Total     int     `json:"total"`
	Done      int     `json:"done"`
	Note      *string `json:"note"`
	UpdatedAt *string `json:"updated_at"`
}

// rollupJSON is the steps in a workspace and everything below it.
type rollupJSON struct {
	Total    int      `json:"total"`
	Done     int      `json:"done"`
	Fraction *float64 `json:"fraction"`
}

// progressNodeJSON is one workspace of ws machine progress show's tree.
type progressNodeJSON struct {
	ID       string             `json:"id"`
	Progress *progressJSON      `json:"progress"`
	Rollup   rollupJSON         `json:"rollup"`
	Children []progressNodeJSON `json:"children"`
}

// machineProgressJSON is the output of ws machine progress.
type machineProgressJSON struct {
	ID       string           `json:"id"`
	Progress progressJSON     `json:"progress"`
	Tree     progressNodeJSON `json:"tree"`
	Warnings []string         `json:"warnings"`
}

func (a *App) newMachineProgressCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "progress <OP>",
		Short: "Report how many steps your task has and has finished",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newMachineProgressSetCmd(), a.newMachineProgressStepCmd(), a.newMachineProgressShowCmd())
	return cmd
}

type progressSetOptions struct {
	name  string
	total int
	done  int
	note  string
}

func (a *App) newMachineProgressSetCmd() *cobra.Command {
	var opts progressSetOptions
	cmd := &cobra.Command{
		Use:   "set [--name <name>] [--total <n>] [--done <n>] [--note <text>]",
		Short: "Name your task, and set its steps and note",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			if !flags.Changed("name") && !flags.Changed("total") && !flags.Changed("done") && !flags.Changed("note") {
				return usageErrorf("%s needs at least one of --name, --total, --done, --note", commandName(cmd))
			}
			return a.updateProgress(func(p *machine.Progress) {
				if flags.Changed("name") {
					p.Name = opts.name
				}
				if flags.Changed("total") {
					p.Total = opts.total
				}
				if flags.Changed("done") {
					p.Done = opts.done
				}
				if flags.Changed("note") {
					p.Note = opts.note
				}
			})
		})),
	}
	cmd.Flags().StringVar(&opts.name, "name", "", "a short name for the task, up to 40 characters; empty clears it")
	cmd.Flags().IntVar(&opts.total, "total", 0, "how many steps the task has; raise or lower it as you learn more")
	cmd.Flags().IntVar(&opts.done, "done", 0, "how many steps are finished; at most --total")
	cmd.Flags().StringVar(&opts.note, "note", "", "what you are doing now, up to 200 characters; empty clears it")
	return cmd
}

func (a *App) newMachineProgressStepCmd() *cobra.Command {
	var by int
	var note string
	cmd := &cobra.Command{
		Use:   "step [--by <n>] [--note <text>]",
		Short: "Finish steps of your task",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			if by < 1 {
				return usageErrorf("--by must be at least 1")
			}
			return a.updateProgress(func(p *machine.Progress) {
				p.Done += by
				if cmd.Flags().Changed("note") {
					p.Note = note
				}
			})
		})),
	}
	cmd.Flags().IntVar(&by, "by", 1, "how many steps finished")
	cmd.Flags().StringVar(&note, "note", "", "what you are doing now; empty clears it")
	return cmd
}

func (a *App) newMachineProgressShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show your progress and your descendants' progress",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			caller, root, err := a.progressCaller()
			if err != nil {
				return err
			}
			return a.printProgress(caller, root)
		})),
	}
}

// progressCaller is the caller's id and the root of its tree.
func (a *App) progressCaller() (caller, root string, err error) {
	caller, err = a.caller()
	if err != nil {
		return "", "", err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return "", "", fmt.Errorf("listing workspaces: %w", err)
	}
	return caller, machine.Root(list, caller), nil
}

// updateProgress applies change to the caller's progress, checks it, writes
// it, and prints the tree. A record that is invalid is replaced, so an agent
// can recover by writing again.
func (a *App) updateProgress(change func(*machine.Progress)) error {
	caller, root, err := a.progressCaller()
	if err != nil {
		return err
	}
	store := machine.NewStore(a.paths.State)
	p, err := store.ReadProgress(root, caller)
	if err != nil && !errors.Is(err, machine.ErrInvalid) {
		return fmt.Errorf("reading %s's progress: %w", caller, err)
	}
	if p == nil {
		p = &machine.Progress{}
	}
	change(p)
	if err = p.Check(); err != nil {
		return newError(codeProgressInvalid, err)
	}
	p.UpdatedAt = a.now().UTC().Truncate(time.Second)
	if err = store.WriteProgress(root, caller, p); err != nil {
		return fmt.Errorf("recording %s's progress: %w", caller, err)
	}
	return a.printProgress(caller, root)
}

// printProgress prints the caller's own progress and the tree below it.
func (a *App) printProgress(caller, root string) error {
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	tree, err := progressTreeOf(machine.NewStore(a.paths.State), root, list, caller)
	if err != nil {
		return err
	}
	node := progressNode(tree)
	return a.writeJSON(machineProgressJSON{ID: caller, Progress: *node.Progress, Tree: node, Warnings: a.warnings()})
}

// progressTreeOf is id and its descendants in list, with the progress and
// the outcome each workspace recorded under root.
func progressTreeOf(store *machine.Store, root string, list []workspace.Workspace, id string) (*machine.ProgressNode, error) {
	ids := []string{id}
	for below := range descendants(list, id) {
		ids = append(ids, below)
	}
	records, err := store.ReadProgressOf(root, ids)
	if err != nil {
		return nil, fmt.Errorf("reading progress: %w", err)
	}
	outcomes := map[string]machine.Outcome{}
	for _, one := range ids {
		rec, err := store.Read(root, one)
		if err != nil {
			return nil, fmt.Errorf("reading %s's machine record: %w", one, err)
		}
		if outcome, ok := rec.Outcome(); ok {
			outcomes[one] = outcome
		}
	}
	return machine.ProgressTree(list, id, records, outcomes), nil
}

// childProgress is a child's own progress and rollup for ws machine wait and
// children. The progress is nil when it has no record and the rollup is nil
// until a step is planned; both are nil when the tree can't be read, with a
// warning.
func (a *App) childProgress(store *machine.Store, root string, list []workspace.Workspace, id string) (*progressJSON, *rollupJSON) {
	tree, err := progressTreeOf(store, root, list, id)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read %s's progress: %v", id, err))
		return nil, nil
	}
	return nodeProgress(tree)
}

// nodeProgress is the node's own progress, nil when it has no record, and its
// rollup, nil when no step is planned.
func nodeProgress(tree *machine.ProgressNode) (*progressJSON, *rollupJSON) {
	var own *progressJSON
	if tree.Progress != nil {
		own = progressInfo(tree.Progress)
	}
	if tree.Rollup.Total == 0 {
		return own, nil
	}
	rollup := rollupInfo(tree.Rollup)
	return own, &rollup
}

func progressNode(n *machine.ProgressNode) progressNodeJSON {
	out := progressNodeJSON{ID: n.ID, Progress: progressInfo(n.Progress), Rollup: rollupInfo(n.Rollup), Children: []progressNodeJSON{}}
	for _, c := range n.Children {
		out.Children = append(out.Children, progressNode(c))
	}
	return out
}

// progressInfo is p, or an empty progress when the workspace reported none.
func progressInfo(p *machine.Progress) *progressJSON {
	if p == nil {
		return &progressJSON{}
	}
	return &progressJSON{
		Name: optional(p.Name), Total: p.Total, Done: p.Done, Note: optional(p.Note),
		UpdatedAt: optional(formatTime(p.UpdatedAt)),
	}
}

func rollupInfo(r machine.Rollup) rollupJSON {
	out := rollupJSON{Total: r.Total, Done: r.Done}
	if f, ok := r.Fraction(); ok {
		out.Fraction = &f
	}
	return out
}
