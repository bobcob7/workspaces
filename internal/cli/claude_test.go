package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_newClaudeCmd(t *testing.T) {
	app, _, _ := newTestApp()
	newCmd := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		cmd := app.newClaudeCmd()
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("has the create flags", func(t *testing.T) {
		cmd := app.newClaudeCmd()
		assertFlag(t, cmd, "name", "")
		assertFlag(t, cmd, "offline", "false")
	})
	t.Run("needs a spec before --", func(t *testing.T) {
		cmd, args := newCmd(t, "--", "a prompt")
		requireUsageError(t, cmd.Args(cmd, args))
	})
	t.Run("runs", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "a prompt")
		requireNotImplemented(t, cmd.RunE(cmd, args))
	})
}

func TestSpecsBeforeDash(t *testing.T) {
	parse := func(t *testing.T, args ...string) (*cobra.Command, []string) {
		t.Helper()
		root := &cobra.Command{Use: "ws"}
		cmd := &cobra.Command{Use: "claude"}
		root.AddCommand(cmd)
		require.NoError(t, cmd.ParseFlags(args))
		return cmd, cmd.Flags().Args()
	}
	t.Run("accepts a spec", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("accepts a spec and claude args", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "prompt")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("rejects nothing", func(t *testing.T) {
		cmd, args := parse(t)
		requireUsageError(t, specsBeforeDash(cmd, args))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear@", "--", "prompt")
		requireCode(t, codeSpecInvalid, specsBeforeDash(cmd, args))
	})
	t.Run("checks only specs before --", func(t *testing.T) {
		cmd, args := parse(t, "buff-bear", "--", "not a spec: @{")
		assert.NoError(t, specsBeforeDash(cmd, args))
	})
	t.Run("rejects only claude args", func(t *testing.T) {
		cmd, args := parse(t, "--", "prompt")
		err := specsBeforeDash(cmd, args)
		requireUsageError(t, err)
		assert.EqualError(t, err, "claude needs at least one repo spec before --")
	})
}

func TestSplitAtDash(t *testing.T) {
	parse := func(t *testing.T, args ...string) *cobra.Command {
		t.Helper()
		cmd := &cobra.Command{}
		require.NoError(t, cmd.ParseFlags(args))
		return cmd
	}
	t.Run("no dash", func(t *testing.T) {
		cmd := parse(t, "a", "b")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a", "b"}, before)
		assert.Nil(t, after)
	})
	t.Run("args on both sides", func(t *testing.T) {
		cmd := parse(t, "a", "--", "--model", "opus", "prompt")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a"}, before)
		assert.Equal(t, []string{"--model", "opus", "prompt"}, after)
	})
	t.Run("nothing before the dash", func(t *testing.T) {
		cmd := parse(t, "--", "prompt")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Empty(t, before)
		assert.Equal(t, []string{"prompt"}, after)
	})
	t.Run("nothing after the dash", func(t *testing.T) {
		cmd := parse(t, "a", "--")
		before, after := splitAtDash(cmd, cmd.Flags().Args())
		assert.Equal(t, []string{"a"}, before)
		assert.Empty(t, after)
	})
}

func TestAddCreateFlags(t *testing.T) {
	var (
		name    string
		offline bool
	)
	cmd := &cobra.Command{}
	addCreateFlags(cmd, &name, &offline)
	require.NoError(t, cmd.ParseFlags([]string{"--name", "fix-login", "--offline"}))
	assert.Equal(t, "fix-login", name)
	assert.True(t, offline)
}
