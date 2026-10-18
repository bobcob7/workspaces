package cli

import (
	"github.com/spf13/cobra"
)

type claudeOptions struct {
	name       string
	offline    bool
	specs      []string
	claudeArgs []string
}

func (a *App) newClaudeCmd() *cobra.Command {
	var opts claudeOptions
	cmd := &cobra.Command{
		Use:   "claude [OPTIONS] <SPEC>... [-- <CLAUDE ARGS>...]",
		Short: "New workspace with a Claude Code background agent",
		Args:  specsBeforeDash,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs, opts.claudeArgs = splitAtDash(cmd, args)
			return errNotImplemented
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	return cmd
}

// specsBeforeDash requires at least one repo spec before "--".
func specsBeforeDash(cmd *cobra.Command, args []string) error {
	if specs, _ := splitAtDash(cmd, args); len(specs) == 0 {
		return usageErrorf("%s needs at least one repo spec before --", commandName(cmd))
	}
	return nil
}

// splitAtDash separates the arguments before "--" from those after it.
func splitAtDash(cmd *cobra.Command, args []string) (before, after []string) {
	n := cmd.ArgsLenAtDash()
	if n < 0 {
		return args, nil
	}
	return args[:n], args[n:]
}

// addCreateFlags adds the flags shared by commands that create a workspace.
func addCreateFlags(cmd *cobra.Command, name *string, offline *bool) {
	cmd.Flags().StringVar(name, "name", "", "derive the workspace id from this name")
	cmd.Flags().BoolVar(offline, "offline", false, "don't fetch; use the cache as-is")
}
