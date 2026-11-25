//go:build unix

package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareSocketDir(t *testing.T) {
	t.Parallel()
	t.Run("creates a 0700 directory", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "ws")
		require.NoError(t, prepareSocketDir(dir))
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	})
	t.Run("refuses a directory that other users can enter", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o755))
		require.Error(t, prepareSocketDir(dir))
	})
}
