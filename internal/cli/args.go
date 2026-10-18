package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// Positional-argument validators that report usage errors (exit 2).

// commandName is the command path without the leading "ws", for messages
// that are already prefixed with "ws: ".
func commandName(cmd *cobra.Command) string {
	return strings.TrimPrefix(cmd.CommandPath(), "ws ")
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf("%s takes no arguments, got %q", commandName(cmd), args[0])
	}
	return nil
}

func atLeastOneArg(what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return usageErrorf("%s needs %s", commandName(cmd), what)
		}
		return nil
	}
}

func exactArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErrorf("%s needs exactly %d %s", commandName(cmd), n, what)
		}
		return nil
	}
}
