package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCache_lock(t *testing.T) {
	t.Parallel()
	t.Run("locks id's lock file", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		assert.FileExists(t, c.lockPath(id))
	})
	t.Run("excludes a shared holder", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.SharedLock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
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
}

func TestCache_SharedLock(t *testing.T) {
	t.Parallel()
	t.Run("shares with other readers", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		first, err := c.SharedLock(t.Context(), id)
		require.NoError(t, err)
		defer first()
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		second, err := c.SharedLock(ctx, id)
		require.NoError(t, err)
		second()
	})
	t.Run("waits for a writer", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err = c.SharedLock(ctx, id)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}
