package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

// unreadable creates dir without read permission, restoring it at cleanup
// so the temp dir can be removed.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o750))
	require.NoError(t, os.Chmod(dir, 0))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
}

func TestCache_IDs(t *testing.T) {
	t.Parallel()
	t.Run("no cache yet", func(t *testing.T) {
		t.Parallel()
		ids, err := newTestCache(t, nil).IDs()
		require.NoError(t, err)
		assert.Empty(t, ids)
	})
	t.Run("walks the tree, sorted", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		b := repo.ID{Host: "github.com", Owner: "b", Name: "x"}
		a := repo.ID{Host: "github.com", Owner: "a", Name: "y"}
		nested := repo.ID{Host: "gitlab.com", Owner: "g/sub", Name: "z"}
		for _, id := range []repo.ID{b, a, nested} {
			fakeEntry(t, c, id)
		}
		ids, err := c.IDs()
		require.NoError(t, err)
		assert.Equal(t, []repo.ID{a, b, nested}, ids)
	})
	t.Run("doesn't descend into repos", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := repo.ID{Host: "github.com", Owner: "o", Name: "n"}
		fakeEntry(t, c, id)
		require.NoError(t, os.MkdirAll(filepath.Join(c.Path(id), "inner", "x.git"), 0o750))
		ids, err := c.IDs()
		require.NoError(t, err)
		assert.Equal(t, []repo.ID{id}, ids)
	})
	t.Run("skips paths that aren't ids", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		require.NoError(t, os.MkdirAll(filepath.Join(c.reposDir(), "short.git"), 0o750))
		require.NoError(t, os.MkdirAll(filepath.Join(c.reposDir(), "github.com", "Upper", "n.git"), 0o750))
		ids, err := c.IDs()
		require.NoError(t, err)
		assert.Empty(t, ids)
	})
	t.Run("unreadable cache", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		unreadable(t, filepath.Join(c.reposDir(), "github.com"))
		_, err := c.IDs()
		assert.Error(t, err)
	})
}

func TestIDFromPath(t *testing.T) {
	t.Run("host/owner/name", func(t *testing.T) {
		id, ok := idFromPath("github.com/o/n.git")
		require.True(t, ok)
		assert.Equal(t, repo.ID{Host: "github.com", Owner: "o", Name: "n"}, id)
	})
	t.Run("nested groups", func(t *testing.T) {
		id, ok := idFromPath("gitlab.com/a/b/n.git")
		require.True(t, ok)
		assert.Equal(t, "a/b", id.Owner)
	})
	t.Run("port", func(t *testing.T) {
		id, ok := idFromPath("h.com:2222/o/n.git")
		require.True(t, ok)
		assert.Equal(t, "h.com:2222", id.Host)
	})
	t.Run("too short", func(t *testing.T) {
		_, ok := idFromPath("o/n.git")
		assert.False(t, ok)
	})
	t.Run("not lowercase", func(t *testing.T) {
		_, ok := idFromPath("github.com/O/n.git")
		assert.False(t, ok)
	})
	t.Run("invalid characters", func(t *testing.T) {
		_, ok := idFromPath("github.com/o o/n.git")
		assert.False(t, ok)
	})
}

func TestCache_List(t *testing.T) {
	t.Parallel()
	t.Run("describes each repo", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		entries, err := c.List(t.Context())
		require.NoError(t, err)
		require.Len(t, entries, 1)
		e := entries[0]
		assert.Equal(t, id, e.ID)
		assert.Equal(t, c.Path(id), e.Path)
		assert.Equal(t, gittest.URL("o", "n"), e.Upstream)
		assert.Equal(t, "main", e.DefaultBranch)
		assert.WithinDuration(t, time.Now(), e.LastFetchedAt, time.Minute)
		assert.Positive(t, e.SizeBytes)
	})
	t.Run("empty cache", func(t *testing.T) {
		t.Parallel()
		entries, err := newTestCache(t, nil).List(t.Context())
		require.NoError(t, err)
		assert.Empty(t, entries)
	})
	t.Run("listing failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		unreadable(t, filepath.Join(c.reposDir(), "github.com"))
		_, err := c.List(t.Context())
		assert.Error(t, err)
	})
	t.Run("entry failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.List(t.Context())
		assert.ErrorIs(t, err, errTest)
	})
}

func TestCache_entry(t *testing.T) {
	t.Parallel()
	answer := func(failOn string) *gitRunnerMock {
		return &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == failOn {
				return nil, errTest
			}
			if args[0] == "config" {
				return []byte("https://example.test/o/n.git\n"), nil
			}
			return []byte("main\n"), nil
		}}
	}
	t.Run("combines git and the filesystem", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, answer(""))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		e, err := c.entry(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, "https://example.test/o/n.git", e.Upstream)
		assert.Equal(t, "main", e.DefaultBranch)
		assert.Equal(t, int64(len("ref: refs/heads/main\n")), e.SizeBytes)
	})
	t.Run("upstream failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, answer("config"))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		_, err := c.entry(t.Context(), id)
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("default branch failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, answer("symbolic-ref"))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		_, err := c.entry(t.Context(), id)
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("fetch time failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, answer(""))
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(c.Path(id), 0o750))
		_, err := c.entry(t.Context(), id)
		assert.ErrorContains(t, err, "no HEAD")
	})
}

func TestCache_upstream(t *testing.T) {
	t.Run("trims git's output", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return []byte("https://x/o/n.git\n"), nil }}
		got, err := newTestCache(t, g).upstream(t.Context(), testID(t, "o", "n"))
		require.NoError(t, err)
		assert.Equal(t, "https://x/o/n.git", got)
		assert.Equal(t, []string{"config", "--get", "remote.origin.url"}, g.RunCalls()[0].Args)
	})
	t.Run("failure", func(t *testing.T) {
		_, err := newTestCache(t, failingGit()).upstream(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}

func TestCache_defaultBranch(t *testing.T) {
	t.Run("trims git's output", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return []byte("main\n"), nil }}
		got, err := newTestCache(t, g).defaultBranch(t.Context(), testID(t, "o", "n"))
		require.NoError(t, err)
		assert.Equal(t, "main", got)
	})
	t.Run("failure", func(t *testing.T) {
		_, err := newTestCache(t, failingGit()).defaultBranch(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}

func TestLastFetched(t *testing.T) {
	t.Parallel()
	stamp := func(t *testing.T, path string, at time.Time) {
		t.Helper()
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		require.NoError(t, os.Chtimes(path, at, at))
	}
	t.Run("FETCH_HEAD", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		fetched := time.Date(2026, 10, 17, 15, 48, 0, 0, time.UTC)
		stamp(t, filepath.Join(dir, "HEAD"), fetched.Add(-time.Hour))
		stamp(t, filepath.Join(dir, "FETCH_HEAD"), fetched)
		got, err := lastFetched(dir)
		require.NoError(t, err)
		assert.True(t, fetched.Equal(got))
	})
	t.Run("HEAD before any fetch", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		cloned := time.Date(2026, 10, 17, 14, 0, 0, 0, time.UTC)
		stamp(t, filepath.Join(dir, "HEAD"), cloned)
		got, err := lastFetched(dir)
		require.NoError(t, err)
		assert.True(t, cloned.Equal(got))
	})
	t.Run("neither", func(t *testing.T) {
		t.Parallel()
		_, err := lastFetched(t.TempDir())
		assert.ErrorContains(t, err, "no HEAD")
	})
	t.Run("unreadable", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		_, err := lastFetched(blocker)
		assert.ErrorContains(t, err, "fetch time")
	})
}

func TestDirSize(t *testing.T) {
	t.Parallel()
	t.Run("adds up files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a"), make([]byte, 10), 0o600))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b"), make([]byte, 5), 0o600))
		size, err := dirSize(dir)
		require.NoError(t, err)
		assert.Equal(t, int64(15), size)
	})
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		_, err := dirSize(filepath.Join(t.TempDir(), "missing"))
		assert.Error(t, err)
	})
}
