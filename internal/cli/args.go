package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/repo"
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

// knownOperation refuses an argument to a command group, such as ws cache:
// it names no operation the group has, since cobra already ran any that it
// does. Without it, cobra prints the group's help and exits 0.
func knownOperation(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageErrorf("unknown %s operation %q; run `ws %s --help`", commandName(cmd), args[0], commandName(cmd))
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

// allArgs runs each validator in order and returns the first error.
func allArgs(validators ...cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		for _, validate := range validators {
			if err := validate(cmd, args); err != nil {
				return err
			}
		}
		return nil
	}
}

// validSpecs requires every argument to parse as a repo spec (ADR 0007).
// It reports spec_invalid, also exit 2.
func validSpecs(_ *cobra.Command, args []string) error {
	for _, arg := range args {
		if _, err := repo.ParseSpec(arg); err != nil {
			return newError(codeSpecInvalid, err)
		}
	}
	return nil
}

func exactArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErrorf("%s needs exactly %d %s", commandName(cmd), n, what)
		}
		return nil
	}
}
