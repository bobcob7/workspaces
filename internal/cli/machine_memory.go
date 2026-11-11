package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
)

// errNotLoopRun is ws machine memory's refusal in a workspace that no loop
// run started.
var errNotLoopRun = errors.New("this workspace isn't a loop run")

// newMachineMemoryCmd reads and changes the memory of the loop the caller's
// run belongs to (ADR 0056).
func (a *App) newMachineMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory <OP>",
		Short: "Read and change your loop's memory",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newMachineMemoryListCmd(),
		a.newMachineMemoryGetCmd(),
		a.newMachineMemorySetCmd(),
		a.newMachineMemoryRmCmd(),
	)
	return cmd
}

func (a *App) newMachineMemoryListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List your loop's memory",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			name, _, err := a.callerLoop()
			if err != nil {
				return err
			}
			return a.memoryList(name)
		})),
	}
}

func (a *App) newMachineMemoryGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <KEY>",
		Short: "Print one memory value of your loop",
		Args:  exactArgs(1, "key"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			name, _, err := a.callerLoop()
			if err != nil {
				return err
			}
			return a.memoryGet(name, args[0])
		})),
	}
}

func (a *App) newMachineMemorySetCmd() *cobra.Command {
	var opts memorySetOptions
	cmd := &cobra.Command{
		Use:   "set [--expect-rev <N>] [--file <PATH> | --file -] <KEY> [<VALUE>]",
		Short: "Set one memory value of your loop",
		Args:  argsBetween(1, 2, "a key, and optionally a value"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			value, err := a.memoryValue(args[1:], opts.file)
			if err != nil {
				return err
			}
			name, caller, err := a.callerLoop()
			if err != nil {
				return err
			}
			return a.memorySet(cmd.Context(), name, args[0], value, caller, opts.expectRev)
		})),
	}
	addMemorySetFlags(cmd, &opts)
	return cmd
}

func (a *App) newMachineMemoryRmCmd() *cobra.Command {
	var opts memorySetOptions
	cmd := &cobra.Command{
		Use:   "rm [--expect-rev <N>] <KEY>",
		Short: "Remove one memory value of your loop",
		Args:  exactArgs(1, "key"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			name, _, err := a.callerLoop()
			if err != nil {
				return err
			}
			return a.memoryRm(cmd.Context(), name, args[0], opts.expectRev)
		})),
	}
	cmd.Flags().IntVar(&opts.expectRev, "expect-rev", loop.AnyRev, expectRevUsage)
	return cmd
}

// callerLoop is the loop of the caller's root, which must have a run mark
// naming a loop that still exists, and the caller's workspace id. Otherwise
// it is not_loop_run.
func (a *App) callerLoop() (name, caller string, err error) {
	caller, err = a.caller()
	if err != nil {
		return "", "", err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return "", "", fmt.Errorf("listing workspaces: %w", err)
	}
	mark, found, err := a.loopData.ReadRunMark(machine.Root(list, caller))
	if err != nil {
		return "", "", domainError(err)
	}
	if !found {
		return "", "", newError(codeNotLoopRun, errNotLoopRun)
	}
	if _, err := a.loops.Get(mark.Loop); err != nil {
		if errors.Is(err, loop.ErrNotFound) {
			return "", "", newError(codeNotLoopRun, fmt.Errorf("loop %s was removed", mark.Loop))
		}
		return "", "", domainError(err)
	}
	return mark.Loop, caller, nil
}
