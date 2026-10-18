package cli

import (
	"testing"
)

func TestApp_newLLMCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newLLMCmd()
	t.Run("has --all", func(t *testing.T) {
		assertFlag(t, cmd, "all", "false")
	})
	t.Run("refuses --json", func(t *testing.T) {
		app.global.json = true
		t.Cleanup(func() { app.global.json = false })
		requireUsageError(t, cmd.PreRunE(cmd, nil))
	})
	t.Run("runs", func(t *testing.T) {
		requireNotImplemented(t, cmd.RunE(cmd, nil))
	})
}
