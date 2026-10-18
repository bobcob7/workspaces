package cli

import (
	"github.com/spf13/cobra"
)

type listOptions struct {
	idsOnly bool
	repo    string
}

func (*App) newListCmd() *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list [OPTIONS]",
		Short: "List workspaces and their sessions",
		Args:  noArgs,
		RunE: runE(func(*cobra.Command, []string) error {
			return errNotImplemented
		}),
	}
	cmd.Flags().BoolVarP(&opts.idsOnly, "ids", "q", false, "print only workspace IDs")
	cmd.Flags().StringVar(&opts.repo, "repo", "", "only workspaces containing this repo")
	return cmd
}
