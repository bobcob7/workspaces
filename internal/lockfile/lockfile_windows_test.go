package lockfile

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openLock opens path the way the lock functions do. The handle is closed
// before the test's temporary directory is removed.
func openLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestTryLock(t *testing.T) {
	t.Parallel()
	t.Run("a second exclusive lock is held", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		first, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		defer first.Unlock()
		_, ok, err = TryExclusiveLock(path)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("two shared locks are both granted", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		first := openLock(t, path)
		second := openLock(t, path)
		held, err := tryLock(first, shared)
		require.NoError(t, err)
		assert.False(t, held)
		held, err = tryLock(second, shared)
		require.NoError(t, err)
		assert.False(t, held)
	})
	t.Run("an exclusive lock is held while a shared one is", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		reader := openLock(t, path)
		writer := openLock(t, path)
		held, err := tryLock(reader, shared)
		require.NoError(t, err)
		require.False(t, held)
		held, err = tryLock(writer, exclusive)
		require.NoError(t, err)
		assert.True(t, held)
	})
	t.Run("closing the holder frees the lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		first, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		first.Unlock()
		second, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		second.Unlock()
	})
}

func TestHeldExclusivelyLockModes(t *testing.T) {
	t.Parallel()
	t.Run("a missing file is a free lock and is not created", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		held, err := HeldExclusively(path)
		require.NoError(t, err)
		assert.False(t, held)
		assert.NoFileExists(t, path)
	})
	t.Run("reports an exclusive holder", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, ok, err := TryExclusive(path)
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		held, err := HeldExclusively(path)
		require.NoError(t, err)
		assert.True(t, held)
	})
	t.Run("a shared holder is not an exclusive one", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		reader := openLock(t, path)
		held, err := tryLock(reader, shared)
		require.NoError(t, err)
		require.False(t, held)
		held, err = HeldExclusively(path)
		require.NoError(t, err)
		assert.False(t, held)
	})
}
