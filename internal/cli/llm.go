package cli

import (
	"github.com/spf13/cobra"
)

type llmOptions struct {
	all     bool
	command []string
}

func (a *App) newLLMCmd() *cobra.Command {
	var opts llmOptions
	cmd := &cobra.Command{
		Use:     "llm [<COMMAND>...]",
		Short:   "Print guidance for AI agents",
		PreRunE: a.noJSON,
		RunE: runE(func(_ *cobra.Command, args []string) error {
			opts.command = args
			return errNotImplemented
		}),
	}
	cmd.Flags().BoolVar(&opts.all, "all", false, "print the overview and every command's guide")
	return cmd
}
