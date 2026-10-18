package cli

import (
	"github.com/spf13/cobra"
)

type shellOptions struct {
	name    string
	offline bool
	specs   []string
}

func (a *App) newShellCmd() *cobra.Command {
	var opts shellOptions
	cmd := &cobra.Command{
		Use:     "shell [OPTIONS] <SPEC>...",
		Short:   "New workspace with an interactive shell",
		Args:    atLeastOneArg("a repo spec"),
		PreRunE: a.noJSON,
		RunE: runE(func(_ *cobra.Command, args []string) error {
			opts.specs = args
			return errNotImplemented
		}),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	return cmd
}
