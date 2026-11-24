package cli

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTerminalSignals(t *testing.T) {
	t.Run("watches Ctrl-C and Ctrl-Break", func(t *testing.T) {
		assert.NotEmpty(t, terminalSignals())
		assert.Contains(t, terminalSignals(), os.Interrupt)
	})
	t.Run("there is no resize signal", func(t *testing.T) {
		assert.False(t, isResize(os.Interrupt))
		assert.False(t, isResize(syscall.SIGTERM))
	})
}
