package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_newCacheCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheCmd()
	t.Run("has every operation", func(t *testing.T) {
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"add", "update", "list", "search", "resolve", "path", "remove", "prune"}, names)
	})
	t.Run("takes no arguments of its own", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"frobnicate"}))
	})
}

func TestApp_newCacheAddCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheAddCmd()
	t.Run("needs a spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"buff-bear"}))
	})
}

func TestApp_newCacheUpdateCmd(t *testing.T) {
	app, _, _ := newTestApp()
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newCacheUpdateCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts specs", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("accepts --all", func(t *testing.T) {
		cmd, args := parse(t, "--all")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects neither", func(t *testing.T) {
		cmd, args := parse(t)
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects both", func(t *testing.T) {
		cmd, args := parse(t, "--all", "buff-bear")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		cmd, args := parse(t, "a//b")
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, args))
	})
	t.Run("runs", func(t *testing.T) {
		cmd, args := parse(t, "--all")
		requireNotImplemented(t, cmd.RunE(cmd, args))
	})
}

func TestApp_newCacheListCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheListCmd()
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}

func TestApp_newCacheSearchCmd(t *testing.T) {
	app, _, _ := newTestApp()
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newCacheSearchCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has --limit and --refs", func(t *testing.T) {
		cmd := app.newCacheSearchCmd()
		assertFlag(t, cmd, "limit", "20")
		assertFlag(t, cmd, "refs", "false")
	})
	t.Run("needs exactly one query", func(t *testing.T) {
		cmd := app.newCacheSearchCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects a limit below 1", func(t *testing.T) {
		cmd, args := parse(t, "--limit", "0", "bear")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("accepts one query", func(t *testing.T) {
		cmd, args := parse(t, "bear")
		assert.NoError(t, cmd.Args(cmd, args))
	})
	t.Run("runs", func(t *testing.T) {
		cmd, args := parse(t, "bear")
		requireNotImplemented(t, cmd.RunE(cmd, args))
	})
}

func TestApp_newCacheResolveCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheResolveCmd()
	t.Run("needs a spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"buff-bear"}))
	})
}

func TestApp_newCachePathCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCachePathCmd()
	t.Run("needs exactly one spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"buff-bear"}))
	})
}

func TestApp_newCacheRemoveCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCacheRemoveCmd()
	t.Run("needs a spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		requireCode(t, codeSpecInvalid, cmd.Args(cmd, []string{"a//b"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"buff-bear"}))
	})
}

func TestApp_newCachePruneCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newCachePruneCmd()
	t.Run("has -n, --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
		assert.NotNil(t, cmd.Flags().ShorthandLookup("n"))
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}

func TestNotImplemented(t *testing.T) {
	requireNotImplemented(t, notImplemented(nil, nil))
}
