package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmpEntry creates name in c's tmp/ with a modification time age before
// now.
func tmpEntry(t *testing.T, c *Cache, now time.Time, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(c.tmpDir(), name)
	require.NoError(t, os.MkdirAll(filepath.Join(path, "objects"), 0o750))
	require.NoError(t, os.Chtimes(path, now.Add(-age), now.Add(-age)))
	return path
}

func TestCache_cleanTmp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	newCache := func(t *testing.T) *Cache {
		t.Helper()
		c := newTestCache(t, failingGit())
		c.now = func() time.Time { return now }
		return c
	}
	t.Run("removes old leftovers", func(t *testing.T) {
		t.Parallel()
		c := newCache(t)
		old := tmpEntry(t, c, now, "clone-old", tmpMaxAge+time.Minute)
		c.cleanTmp()
		assert.NoDirExists(t, old)
		assert.Empty(t, c.Warnings())
	})
	t.Run("keeps recent entries, such as a clone in progress", func(t *testing.T) {
		t.Parallel()
		c := newCache(t)
		recent := tmpEntry(t, c, now, "clone-new", tmpMaxAge-time.Minute)
		c.cleanTmp()
		assert.DirExists(t, recent)
	})
	t.Run("no tmp directory", func(t *testing.T) {
		t.Parallel()
		c := newCache(t)
		c.cleanTmp()
		assert.Empty(t, c.Warnings())
	})
	t.Run("unreadable tmp is a warning", func(t *testing.T) {
		t.Parallel()
		c := newCache(t)
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		require.NoError(t, os.WriteFile(c.tmpDir(), nil, 0o600))
		c.cleanTmp()
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "couldn't clean")
	})
}

func TestCache_startOp(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c := newTestCache(t, failingGit())
	c.now = func() time.Time { return now }
	first := tmpEntry(t, c, now, "first", 2*tmpMaxAge)
	c.startOp()
	second := tmpEntry(t, c, now, "second", 2*tmpMaxAge)
	c.startOp()
	assert.NoDirExists(t, first)
	assert.DirExists(t, second, "cleaning runs once per Cache")
}
