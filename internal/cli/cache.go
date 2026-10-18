package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache <OP>",
		Short: "Manage, search, and resolve cached repos",
		Args:  noArgs,
	}
	cmd.AddCommand(
		a.newCacheAddCmd(),
		a.newCacheUpdateCmd(),
		a.newCacheListCmd(),
		a.newCacheSearchCmd(),
		a.newCacheResolveCmd(),
		a.newCachePathCmd(),
		a.newCacheRemoveCmd(),
		a.newCachePruneCmd(),
	)
	return cmd
}

func (a *App) newCacheAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <SPEC>...",
		Short: "Clone repos into the cache if missing",
		Args:  atLeastOneArg("a repo spec"),
		RunE:  runE(a.withConfig(notImplemented)),
	}
}

func (a *App) newCacheUpdateCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "update [<SPEC>... | --all]",
		Short: "Fetch cached repos from upstream",
		Args: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return usageErrorf("%s needs repo specs or --all, not both", commandName(cmd))
			}
			return nil
		},
		RunE: runE(a.withConfig(notImplemented)),
	}
	cmd.Flags().BoolVar(&all, "all", false, "update every cached repo")
	return cmd
}

func (a *App) newCacheListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cached repos",
		Args:  noArgs,
		RunE:  runE(a.withConfig(notImplemented)),
	}
}

func (a *App) newCacheSearchCmd() *cobra.Command {
	var (
		limit int
		refs  bool
	)
	cmd := &cobra.Command{
		Use:   "search <QUERY>",
		Short: "Search cached repo ids",
		Args: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return usageErrorf("--limit must be at least 1")
			}
			return exactArgs(1, "query")(cmd, args)
		},
		RunE: runE(a.withConfig(notImplemented)),
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results")
	cmd.Flags().BoolVar(&refs, "refs", false, "also search branch and tag names")
	return cmd
}

func (a *App) newCacheResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <SPEC>...",
		Short: "Resolve specs to canonical ids without cloning",
		Args:  atLeastOneArg("a repo spec"),
		RunE:  runE(a.withConfig(notImplemented)),
	}
}

func (a *App) newCachePathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <SPEC>",
		Short: "Print the cache directory for a spec",
		Args:  exactArgs(1, "repo spec"),
		RunE:  runE(a.withConfig(notImplemented)),
	}
}

func (a *App) newCacheRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <SPEC>...",
		Short: "Delete cache entries",
		Args:  atLeastOneArg("a repo spec"),
		RunE:  runE(a.withConfig(notImplemented)),
	}
}

func (a *App) newCachePruneCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete commit refs no workspace uses",
		Args:  noArgs,
		RunE:  runE(a.withConfig(notImplemented)),
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "list what would be deleted")
	return cmd
}

func notImplemented(*cobra.Command, []string) error {
	return errNotImplemented
}
