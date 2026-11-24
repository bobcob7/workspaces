//go:build unix

package lockfile

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These run a child that inherits the lock's file descriptor (ExtraFiles),
// which only unix does: on Windows the lock isn't shared with the child.
func TestHeldExclusivelyByChild(t *testing.T) {
	t.Parallel()
	t.Run("held by a child process after this one let go", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		l, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		child := exec.CommandContext(t.Context(), "/bin/cat")
		child.ExtraFiles = []*os.File{l.File()}
		stdin, err := child.StdinPipe()
		require.NoError(t, err)
		require.NoError(t, child.Start())
		l.Unlock()
		held, err := HeldExclusively(path)
		require.NoError(t, err)
		assert.True(t, held, "held by the child")
		require.NoError(t, stdin.Close())
		require.NoError(t, child.Wait())
		require.Eventually(t, func() bool {
			held, err := HeldExclusively(path)
			return err == nil && !held
		}, 10*time.Second, 10*time.Millisecond, "free once the child exited")
	})
}

func TestLock_File(t *testing.T) {
	t.Parallel()
	t.Run("a child given it holds the lock after the parent lets go", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		l, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		child := exec.CommandContext(t.Context(), "/bin/cat")
		child.ExtraFiles = []*os.File{l.File()}
		stdin, err := child.StdinPipe()
		require.NoError(t, err)
		require.NoError(t, child.Start())
		l.Unlock()
		_, ok, err = TryExclusive(path)
		require.NoError(t, err)
		assert.False(t, ok, "held by the child")
		require.NoError(t, stdin.Close())
		require.NoError(t, child.Wait())
		requireFree(t, path)
	})
}
