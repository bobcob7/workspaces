package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommandName(t *testing.T) {
	t.Run("drops the leading ws", func(t *testing.T) {
		assert.Equal(t, "cache list", commandName(newTestTree()))
	})
	t.Run("root stays ws", func(t *testing.T) {
		assert.Equal(t, "ws", commandName(&cobra.Command{Use: "ws"}))
	})
}

func TestNoArgs(t *testing.T) {
	cmd := newTestTree()
	t.Run("accepts no arguments", func(t *testing.T) {
		assert.NoError(t, noArgs(cmd, nil))
	})
	t.Run("rejects an argument", func(t *testing.T) {
		err := noArgs(cmd, []string{"extra"})
		requireUsageError(t, err)
		assert.EqualError(t, err, `cache list takes no arguments, got "extra"`)
	})
}

func TestAtLeastOneArg(t *testing.T) {
	validate := atLeastOneArg("a repo spec")
	cmd := newTestTree()
	t.Run("accepts one", func(t *testing.T) {
		assert.NoError(t, validate(cmd, []string{"a"}))
	})
	t.Run("accepts several", func(t *testing.T) {
		assert.NoError(t, validate(cmd, []string{"a", "b"}))
	})
	t.Run("rejects none", func(t *testing.T) {
		err := validate(cmd, nil)
		requireUsageError(t, err)
		assert.EqualError(t, err, "cache list needs a repo spec")
	})
}

func TestExactArgs(t *testing.T) {
	validate := exactArgs(1, "query")
	cmd := newTestTree()
	t.Run("accepts exactly n", func(t *testing.T) {
		assert.NoError(t, validate(cmd, []string{"a"}))
	})
	t.Run("rejects fewer", func(t *testing.T) {
		err := validate(cmd, nil)
		requireUsageError(t, err)
		assert.EqualError(t, err, "cache list needs exactly 1 query")
	})
	t.Run("rejects more", func(t *testing.T) {
		requireUsageError(t, validate(cmd, []string{"a", "b"}))
	})
}

func TestAllArgs(t *testing.T) {
	cmd := newTestTree()
	pass := func(*cobra.Command, []string) error { return nil }
	fail := func(*cobra.Command, []string) error { return usageErrorf("first") }
	never := func(*cobra.Command, []string) error {
		t.Fatal("ran a validator after a failure")
		return nil
	}
	t.Run("passes when all pass", func(t *testing.T) {
		var calls int
		count := func(*cobra.Command, []string) error {
			calls++
			return nil
		}
		require.NoError(t, allArgs(pass, count)(cmd, nil))
		assert.Equal(t, 1, calls)
	})
	t.Run("stops at the first failure", func(t *testing.T) {
		err := allArgs(pass, fail, never)(cmd, nil)
		assert.EqualError(t, err, "first")
	})
	t.Run("passes with none", func(t *testing.T) {
		assert.NoError(t, allArgs()(cmd, nil))
	})
}

func TestValidSpecs(t *testing.T) {
	cmd := newTestTree()
	t.Run("accepts valid specs", func(t *testing.T) {
		assert.NoError(t, validSpecs(cmd, []string{"buff-bear@main", "git@github.com:o/n.git"}))
	})
	t.Run("accepts none", func(t *testing.T) {
		assert.NoError(t, validSpecs(cmd, nil))
	})
	t.Run("rejects an invalid spec", func(t *testing.T) {
		err := validSpecs(cmd, []string{"buff-bear", "a//b"})
		requireCode(t, codeSpecInvalid, err)
		assert.ErrorContains(t, err, `"a//b"`)
	})
}
