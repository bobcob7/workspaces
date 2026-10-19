package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
)

func TestCache_Add(t *testing.T) {
	t.Parallel()
	t.Run("clones a missing repo", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		commit := up.Repo(t, "o", "n")
		added, err := c.Add(t.Context(), testID(t, "o", "n"), gittest.URL("o", "n"))
		require.NoError(t, err)
		assert.True(t, added)
		assert.Equal(t, commit, up.Git(t, c.Path(testID(t, "o", "n")), "rev-parse", "refs/heads/main"))
	})
	t.Run("leaves a cached repo alone", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		added, err := c.Add(t.Context(), id, gittest.URL("o", "n"))
		require.NoError(t, err)
		assert.False(t, added)
	})
	t.Run("failed clone leaves nothing", func(t *testing.T) {
		t.Parallel()
		c, _ := newGitCache(t)
		id := testID(t, "o", "missing")
		_, err := c.Add(t.Context(), id, gittest.URL("o", "missing"))
		require.ErrorIs(t, err, ErrCloneFailed)
		assert.NoDirExists(t, c.Path(id))
		entries, err := os.ReadDir(filepath.Join(c.root, "tmp"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("missing git", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, git.NewRunner(discardLogger(), filepath.Join(t.TempDir(), "no-git"), nil))
		_, err := c.Add(t.Context(), testID(t, "o", "n"), gittest.URL("o", "n"))
		assert.ErrorIs(t, err, git.ErrNotInstalled)
	})
	t.Run("uncreatable cache directory", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		c := newTestCache(t, failingGit())
		c.root = filepath.Join(blocker, "cache")
		_, err := c.Add(t.Context(), testID(t, "o", "n"), gittest.URL("o", "n"))
		assert.Error(t, err)
	})
	t.Run("lock failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(c.lockPath(id), 0o750))
		_, err := c.Add(t.Context(), id, gittest.URL("o", "n"))
		assert.ErrorContains(t, err, "lock")
	})
}

func TestCache_clone(t *testing.T) {
	t.Parallel()
	t.Run("bare, with the cache's refspecs", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		up.Repo(t, "o", "n")
		id := testID(t, "o", "n")
		require.NoError(t, c.clone(t.Context(), id, gittest.URL("o", "n")))
		path := c.Path(id)
		assert.Equal(t, "true", up.Git(t, path, "rev-parse", "--is-bare-repository"))
		assert.Equal(t, "+refs/heads/*:refs/heads/*\n+refs/tags/*:refs/tags/*", up.Git(t, path, "config", "--get-all", "remote.origin.fetch"))
		assert.Equal(t, gittest.URL("o", "n"), up.Git(t, path, "config", "remote.origin.url"))
	})
	t.Run("git failure removes the temp clone", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		err := c.clone(t.Context(), testID(t, "o", "n"), gittest.URL("o", "n"))
		require.ErrorIs(t, err, errTest)
		entries, err := os.ReadDir(filepath.Join(c.root, "tmp"))
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("refspec failure", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{}
		g.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "config" {
				return nil, errTest
			}
			return nil, nil
		}
		c := newTestCache(t, g)
		assert.ErrorIs(t, c.clone(t.Context(), testID(t, "o", "n"), "x"), errTest)
	})
	t.Run("second refspec failure", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{}
		g.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "config" && args[1] == "--add" {
				return nil, errTest
			}
			return nil, nil
		}
		c := newTestCache(t, g)
		assert.ErrorIs(t, c.clone(t.Context(), testID(t, "o", "n"), "x"), errTest)
	})
	t.Run("uncreatable temp directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(c.root, "tmp"), nil, 0o600))
		assert.Error(t, c.clone(t.Context(), testID(t, "o", "n"), "x"))
	})
	t.Run("unmovable clone", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }}
		c := newTestCache(t, g)
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Dir(c.Path(id))), 0o750))
		require.NoError(t, os.WriteFile(filepath.Dir(c.Path(id)), nil, 0o600))
		assert.Error(t, c.clone(t.Context(), id, "x"))
	})
}
