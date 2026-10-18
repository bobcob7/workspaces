package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestApp_newRestoreCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRestoreCmd()
	t.Run("has --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
	})
	t.Run("workspaces are optional", func(t *testing.T) {
		assert.Nil(t, cmd.Args)
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}
