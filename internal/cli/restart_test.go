package cli

import (
	"testing"
)

func TestApp_newRestartCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRestartCmd()
	t.Run("has --no-wait and --force", func(t *testing.T) {
		assertFlag(t, cmd, "no-wait", "false")
		assertFlag(t, cmd, "force", "false")
	})
	t.Run("takes no arguments", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"extra"}))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}
