package cli

import (
	"github.com/spf13/cobra"
)

type rmOptions struct {
	dryRun     bool
	workspaces []string
}

func (*App) newRmCmd() *cobra.Command {
	var opts rmOptions
	cmd := &cobra.Command{
		Use:   "rm [OPTIONS] <WORKSPACE>...",
		Short: "Remove workspaces and their sessions now",
		Args:  atLeastOneArg("a workspace"),
		RunE: runE(func(_ *cobra.Command, args []string) error {
			opts.workspaces = args
			return errNotImplemented
		}),
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be removed, change nothing")
	return cmd
}
