package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApp_newListCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newListCmd()
	t.Run("has -q and --repo", func(t *testing.T) {
		assert.NotNil(t, cmd.Flags().ShorthandLookup("q"))
		assertFlag(t, cmd, "repo", "")
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}
