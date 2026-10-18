package cli

import (
	"github.com/spf13/cobra"
)

type restartOptions struct {
	noWait bool
	force  bool
}

func (a *App) newRestartCmd() *cobra.Command {
	var opts restartOptions
	cmd := &cobra.Command{
		Use:   "restart [OPTIONS]",
		Short: "Restart Claude Code's background service and every running agent",
		Args:  noArgs,
		RunE:  runE(a.withConfig(notImplemented)),
	}
	cmd.Flags().BoolVar(&opts.noWait, "no-wait", false, "respawn only sessions that aren't working, then exit")
	cmd.Flags().BoolVar(&opts.force, "force", false, "respawn working sessions at once, interrupting their turn")
	return cmd
}
