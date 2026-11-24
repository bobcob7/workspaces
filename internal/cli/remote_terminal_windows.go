//go:build windows

package cli

import (
	"os"
	"syscall"
)

// terminalSignals are the signals a remote terminal watches on Windows, where
// there is no resize signal: Ctrl-C and Ctrl-Break end the terminal.
func terminalSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

func isResize(os.Signal) bool { return false }
