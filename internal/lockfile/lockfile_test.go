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
		unlock, ok, err := TryExclusive(path)
		require.NoError(t, err)
		require.True(t, ok, "free once the child exited")
		unlock()
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
		unlock, ok, err := TryExclusive(path)
		require.NoError(t, err)
		require.True(t, ok)
		unlock()
	})
}
