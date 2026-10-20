package lockfile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "locks", "x.lock")
}

// requireFree checks that path's lock can be taken. A process another
// parallel test forks holds copies of this process's descriptors until it
// execs, so a lock just released can look held for a moment.
func requireFree(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		unlock, ok, err := TryExclusive(path)
		if err != nil || !ok {
			return false
		}
		unlock()
		return true
	}, 10*time.Second, 10*time.Millisecond, "the lock is still held")
}

func TestExclusive(t *testing.T) {
	t.Parallel()
	t.Run("creates the file and its directory", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Exclusive(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		assert.FileExists(t, path)
	})
	t.Run("waits for the holder to release", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Exclusive(t.Context(), path)
		require.NoError(t, err)
		released := make(chan struct{})
		go func() {
			time.Sleep(3 * pollInterval)
			close(released)
			unlock()
		}()
		second, err := Exclusive(t.Context(), path)
		require.NoError(t, err)
		defer second()
		select {
		case <-released:
		default:
			t.Fatal("got the lock while it was held")
		}
	})
	t.Run("waits for shared holders", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Shared(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 2*pollInterval)
		defer cancel()
		_, err = Exclusive(ctx, path)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("uncreatable directory", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		_, err := Exclusive(t.Context(), filepath.Join(blocker, "x.lock"))
		assert.Error(t, err)
	})
	t.Run("unopenable file", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		require.NoError(t, os.MkdirAll(path, 0o750))
		_, err := Exclusive(t.Context(), path)
		assert.Error(t, err)
	})
}

func TestShared(t *testing.T) {
	t.Parallel()
	t.Run("shares with other readers", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		first, err := Shared(t.Context(), path)
		require.NoError(t, err)
		defer first()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		second, err := Shared(ctx, path)
		require.NoError(t, err)
		second()
	})
	t.Run("waits for an exclusive holder", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Exclusive(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 2*pollInterval)
		defer cancel()
		_, err = Shared(ctx, path)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestTryExclusive(t *testing.T) {
	t.Parallel()
	t.Run("free lock", func(t *testing.T) {
		t.Parallel()
		unlock, ok, err := TryExclusive(lockPath(t))
		require.NoError(t, err)
		require.True(t, ok)
		unlock()
	})
	t.Run("held lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Shared(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		_, ok, err := TryExclusive(path)
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("unopenable file", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		require.NoError(t, os.MkdirAll(path, 0o750))
		_, _, err := TryExclusive(path)
		assert.Error(t, err)
	})
}

func TestTryExclusiveLock(t *testing.T) {
	t.Parallel()
	t.Run("free lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		l, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		defer l.Unlock()
		assert.Equal(t, path, l.File().Name())
		_, ok, err = TryExclusive(path)
		require.NoError(t, err)
		assert.False(t, ok, "held")
	})
	t.Run("held lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Shared(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		l, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, l)
	})
	t.Run("unopenable file", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		require.NoError(t, os.MkdirAll(path, 0o750))
		_, _, err := TryExclusiveLock(path)
		assert.Error(t, err)
	})
}

func TestHeldExclusively(t *testing.T) {
	t.Parallel()
	t.Run("free lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, ok, err := TryExclusive(path)
		require.NoError(t, err)
		require.True(t, ok)
		unlock()
		require.Eventually(t, func() bool {
			held, err := HeldExclusively(path)
			return err == nil && !held
		}, 10*time.Second, 10*time.Millisecond)
	})
	t.Run("exclusive holder", func(t *testing.T) {
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
	t.Run("a shared holder, such as another probe, doesn't count", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, err := Shared(t.Context(), path)
		require.NoError(t, err)
		defer unlock()
		held, err := HeldExclusively(path)
		require.NoError(t, err)
		assert.False(t, held)
	})
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
	t.Run("leaves the lock free after probing", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		unlock, ok, err := TryExclusive(path)
		require.NoError(t, err)
		require.True(t, ok)
		unlock()
		_, err = HeldExclusively(path)
		require.NoError(t, err)
		requireFree(t, path)
	})
	t.Run("missing file, without creating it or its directory", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		held, err := HeldExclusively(path)
		require.NoError(t, err)
		assert.False(t, held)
		assert.NoDirExists(t, filepath.Dir(path))
	})
	t.Run("unopenable file", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		require.NoError(t, os.MkdirAll(path, 0o750))
		_, err := HeldExclusively(path)
		assert.Error(t, err)
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

func TestLock_Unlock(t *testing.T) {
	t.Parallel()
	t.Run("frees the lock", func(t *testing.T) {
		t.Parallel()
		path := lockPath(t)
		l, ok, err := TryExclusiveLock(path)
		require.NoError(t, err)
		require.True(t, ok)
		l.Unlock()
		requireFree(t, path)
	})
}
