package cli

import (
	"testing"
)

func TestApp_newShellCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newShellCmd()
	t.Run("has the create flags", func(t *testing.T) {
		assertFlag(t, cmd, "name", "")
		assertFlag(t, cmd, "offline", "false")
	})
	t.Run("needs a spec", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		requireUsageError(t, cmd.PreRunE(cmd, nil))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, []string{"buff-bear"}))
	})
}
