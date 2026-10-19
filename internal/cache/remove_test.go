package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
)

func TestCache_Remove(t *testing.T) {
	t.Parallel()
	t.Run("deletes the repo and empty parents", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := repo.ID{Host: "gitlab.com", Owner: "a/b", Name: "n"}
		keep := repo.ID{Host: "gitlab.com", Owner: "a", Name: "other"}
		fakeEntry(t, c, id)
		fakeEntry(t, c, keep)
		require.NoError(t, c.Remove(t.Context(), id))
		assert.NoDirExists(t, filepath.Join(c.reposDir(), "gitlab.com", "a", "b"))
		assert.DirExists(t, c.Path(keep))
	})
	t.Run("leaves nothing in the temp directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		require.NoError(t, c.Remove(t.Context(), id))
		entries, err := os.ReadDir(filepath.Join(c.root, "tmp"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("not cached", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		assert.ErrorIs(t, c.Remove(t.Context(), testID(t, "o", "n")), ErrRepoNotFound)
	})
	t.Run("lock failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		require.NoError(t, os.MkdirAll(c.lockPath(id), 0o750))
		assert.ErrorContains(t, c.Remove(t.Context(), id), "lock")
	})
	t.Run("undeletable repo", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		unreadable(t, filepath.Join(c.Path(id), "objects"))
		parent := filepath.Dir(c.Path(id))
		require.NoError(t, os.Chmod(parent, 0o500))
		t.Cleanup(func() { _ = os.Chmod(parent, 0o750) })
		assert.Error(t, c.Remove(t.Context(), id))
	})
}

func TestCache_moveToTmp(t *testing.T) {
	t.Parallel()
	t.Run("moves the directory out of place", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		trash, err := c.moveToTmp(c.Path(id))
		require.NoError(t, err)
		assert.False(t, c.exists(id))
		assert.DirExists(t, filepath.Join(trash, "n.git"))
	})
	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		_, err := c.moveToTmp(c.Path(testID(t, "o", "n")))
		require.Error(t, err)
		entries, err := os.ReadDir(filepath.Join(c.root, "tmp"))
		require.NoError(t, err)
		assert.Empty(t, entries, "the temp directory it made is removed")
	})
	t.Run("uncreatable temp directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(c.root, "tmp"), nil, 0o600))
		_, err := c.moveToTmp(c.Path(testID(t, "o", "n")))
		assert.ErrorContains(t, err, "temp directory")
	})
}

func TestRemoveEmptyParents(t *testing.T) {
	t.Parallel()
	t.Run("stops at a non-empty directory", func(t *testing.T) {
		t.Parallel()
		stop := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(stop, "a", "b", "c"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(stop, "a", "keep"), nil, 0o600))
		removeEmptyParents(filepath.Join(stop, "a", "b", "c"), stop)
		assert.NoDirExists(t, filepath.Join(stop, "a", "b"))
		assert.DirExists(t, filepath.Join(stop, "a"))
	})
	t.Run("never removes stop", func(t *testing.T) {
		t.Parallel()
		stop := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(stop, "a"), 0o750))
		removeEmptyParents(filepath.Join(stop, "a"), stop)
		assert.DirExists(t, stop)
	})
}
