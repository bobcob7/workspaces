package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_lock(t *testing.T) {
	t.Parallel()
	t.Run("creates the lock file", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		assert.FileExists(t, c.lockPath(id))
	})
	t.Run("waits for the holder to release", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		released := make(chan struct{})
		go func() {
			time.Sleep(3 * lockPoll)
			close(released)
			unlock()
		}()
		second, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer second()
		select {
		case <-released:
		default:
			t.Fatal("got the lock while it was held")
		}
	})
	t.Run("gives up when the context ends", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 2*lockPoll)
		defer cancel()
		_, err = c.lock(ctx, id)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
	t.Run("other repos don't wait", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		unlock, err := c.lock(t.Context(), testID(t, "o", "a"))
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		other, err := c.lock(ctx, testID(t, "o", "b"))
		require.NoError(t, err)
		other()
	})
	t.Run("uncreatable lock directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(c.root, "locks"), nil, 0o600))
		_, err := c.lock(t.Context(), testID(t, "o", "n"))
		assert.Error(t, err)
	})
	t.Run("unopenable lock file", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(c.lockPath(id), 0o750))
		_, err := c.lock(t.Context(), id)
		assert.Error(t, err)
	})
}
