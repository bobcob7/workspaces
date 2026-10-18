package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
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
