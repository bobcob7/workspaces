//go:build unix

package cli

import (
	"os"
	"syscall"
)

// terminalSignals are the signals a remote terminal watches: a resize, and
// the ones that end the terminal.
func terminalSignals() []os.Signal {
	return []os.Signal{syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT}
}

func isResize(sig os.Signal) bool { return sig == syscall.SIGWINCH }
