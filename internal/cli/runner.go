package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/runner"
)

// newRunnerCmd is ws runner, for the daemon's use: hidden, and absent from
// the agent-facing guides (ADR 0063).
func (a *App) newRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "runner <OP>",
		Short:  "Run a runner session's agent loop",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newRunnerRunCmd())
	return cmd
}

func (a *App) newRunnerRunCmd() *cobra.Command {
	var resume bool
	cmd := &cobra.Command{
		Use:    "run [--resume] <id>",
		Short:  "Run a runner session's agent loop until it exits",
		Hidden: true,
		Args:   exactlyOneArg("a workspace"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runRunner(cmd.Context(), args[0], resume)
		})),
	}
	cmd.Flags().BoolVar(&resume, "resume", false, "continue the run from its transcript")
	return cmd
}

// runRunner runs workspace id's runner session until it exits. It stops
// the run on SIGTERM or SIGINT, which the daemon sends to interrupt it.
func (a *App) runRunner(ctx context.Context, id string, resume bool) error {
	ws, err := a.workspaces.Find(id)
	if err != nil {
		return fmt.Errorf("finding workspace %s: %w", id, err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	in := runner.Input{
		Files:     runner.FilesFor(a.paths.State, ws.ID),
		Resume:    resume,
		Workspace: ws.Path,
		ConfigDir: a.paths.Root,
		Environ:   a.environ,
		Store:     runnerRecords{Store: machine.NewStore(a.paths.State), workspaces: a.workspaces},
	}
	if err := runner.Execute(ctx, &in); err != nil {
		return newError(codeInternal, err)
	}
	return nil
}

// runnerRecords is machine.Store for a runner, with the lineage ws machine
// result computes from the workspace list.
type runnerRecords struct {
	*machine.Store
	workspaces workspaceStore
}

// Lineage is id's root and its ancestors, as ws machine result computes them.
func (r runnerRecords) Lineage(id string) (root string, ancestors []string, err error) {
	list, err := r.workspaces.List()
	if err != nil {
		return "", nil, fmt.Errorf("listing workspaces: %w", err)
	}
	return machine.Root(list, id), machine.Ancestors(list, id), nil
}
