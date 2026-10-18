package cli

import (
	"testing"
)

func TestApp_newRmCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRmCmd()
	t.Run("has --dry-run", func(t *testing.T) {
		assertFlag(t, cmd, "dry-run", "false")
	})
	t.Run("needs a workspace", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"fix-login"}))
	})
}
