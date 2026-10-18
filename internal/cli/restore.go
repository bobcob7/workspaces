package cli

import (
	"github.com/spf13/cobra"
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
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			opts.workspaces = args
			return errNotImplemented
		})),
	}
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print what would be restarted, change nothing")
	return cmd
}
