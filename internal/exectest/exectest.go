// Package exectest writes fake executables for tests.
package exectest

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// WriteScript writes an executable script to path. It holds
// syscall.ForkLock while the file is open for writing, so a process that
// a parallel test forks meanwhile can't inherit the descriptor and make
// running the script fail with ETXTBSY on Linux (golang/go#22315).
func WriteScript(t *testing.T, path, script string) {
	t.Helper()
	syscall.ForkLock.RLock()
	err := os.WriteFile(path, []byte(script), 0o700) //nolint:gosec // G306: a script must be executable.
	syscall.ForkLock.RUnlock()
	require.NoError(t, err)
}
