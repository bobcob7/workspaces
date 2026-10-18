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
	t.Run("rejects no spec", func(t *testing.T) {
		cmd, args := newCmd(t)
		requireUsageError(t, cmd.RunE(cmd, args))
	})
	t.Run("rejects only claude args", func(t *testing.T) {
		cmd, args := newCmd(t, "--", "a prompt")
		requireUsageError(t, cmd.RunE(cmd, args))
	})
	t.Run("accepts specs with claude args", func(t *testing.T) {
		cmd, args := newCmd(t, "buff-bear", "--", "a prompt")
		requireNotImplemented(t, cmd.RunE(cmd, args))
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
