package cache

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

var errTest = errors.New("boom")

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// newTestCache returns a cache in a temp dir that runs g, with defaults
// for gittest's forge and Time Machine exclusion turned off.
func newTestCache(t *testing.T, g gitRunner) *Cache {
	t.Helper()
	c := New(discardLogger(), g, filepath.Join(t.TempDir(), "cache"), config.Defaults{Host: gittest.Host, Owners: []string{"o"}})
	c.exclude = func(context.Context, string) error { return nil }
	return c
}

// newGitCache returns a cache that runs the real git against a gittest
// upstream.
func newGitCache(t *testing.T) (*Cache, *gittest.Upstream) {
	t.Helper()
	up := gittest.NewUpstream(t)
	return newTestCache(t, git.NewRunner(discardLogger(), "git", up.Environ)), up
}

// failingGit is a git runner where every command fails.
func failingGit() *gitRunnerMock {
	return &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, errTest }}
}

func testID(t *testing.T, owner, name string) repo.ID {
	t.Helper()
	id, err := repo.NewID(gittest.Host, owner, name)
	require.NoError(t, err)
	return id
}

// fakeEntry makes id look cached without git: a directory with a HEAD.
func fakeEntry(t *testing.T, c *Cache, id repo.ID) {
	t.Helper()
	require.NoError(t, os.MkdirAll(c.Path(id), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(c.Path(id), "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
}

// addRepo creates the upstream repo o/n and caches it.
func addRepo(t *testing.T, c *Cache, up *gittest.Upstream) repo.ID {
	t.Helper()
	up.Repo(t, "o", "n")
	id := testID(t, "o", "n")
	_, err := c.Add(t.Context(), id, gittest.URL("o", "n"))
	require.NoError(t, err)
	return id
}

func TestNew(t *testing.T) {
	g := failingGit()
	defaults := config.Defaults{Host: "github.com", Owners: []string{"me"}}
	c := New(discardLogger(), g, "/cfg/cache", defaults)
	assert.Same(t, g, c.git)
	assert.Equal(t, "/cfg/cache", c.root)
	assert.Equal(t, defaults, c.defaults)
	assert.NotNil(t, c.exclude)
}

func TestCache_Path(t *testing.T) {
	c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
	t.Run("host/owner/name.git", func(t *testing.T) {
		assert.Equal(t, "/cfg/cache/repos/github.com/o/n.git", c.Path(repo.ID{Host: "github.com", Owner: "o", Name: "n"}))
	})
	t.Run("nested groups and ports", func(t *testing.T) {
		assert.Equal(t, "/cfg/cache/repos/h.com:2222/a/b/n.git", c.Path(repo.ID{Host: "h.com:2222", Owner: "a/b", Name: "n"}))
	})
}

func TestCache_Warnings(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
		assert.Empty(t, c.Warnings())
	})
	t.Run("returns a copy", func(t *testing.T) {
		c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
		c.warnings = []string{"x"}
		got := c.Warnings()
		got[0] = "changed"
		assert.Equal(t, []string{"x"}, c.Warnings())
	})
}

func TestCache_warn(t *testing.T) {
	t.Run("formats and keeps order", func(t *testing.T) {
		c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
		c.warn("%s is %d", "x", 1)
		c.warn("second")
		assert.Equal(t, []string{"x is 1", "second"}, c.Warnings())
	})
	t.Run("safe from concurrent operations", func(t *testing.T) {
		c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
		forEach(50, func(i int) { c.warn("w%d", i) })
		assert.Len(t, c.Warnings(), 50)
	})
}

func TestCache_tmpDir(t *testing.T) {
	c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
	assert.Equal(t, "/cfg/cache/tmp", c.tmpDir())
}

func TestCache_lockPath(t *testing.T) {
	c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
	assert.Equal(t, "/cfg/cache/locks/github.com/o/n.lock", c.lockPath(repo.ID{Host: "github.com", Owner: "o", Name: "n"}))
}

func TestCache_reposDir(t *testing.T) {
	c := New(discardLogger(), nil, "/cfg/cache", config.Defaults{})
	assert.Equal(t, "/cfg/cache/repos", c.reposDir())
}

func TestCache_exists(t *testing.T) {
	t.Parallel()
	t.Run("cached", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		assert.True(t, c.exists(id))
	})
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		assert.False(t, c.exists(testID(t, "o", "n")))
	})
	t.Run("directory without HEAD", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(c.Path(id), 0o750))
		assert.False(t, c.exists(id))
	})
}

func TestCache_ensureRoot(t *testing.T) {
	t.Parallel()
	t.Run("creates it and excludes it from backups", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		var excluded []string
		c.exclude = func(_ context.Context, dir string) error {
			excluded = append(excluded, dir)
			return nil
		}
		require.NoError(t, c.ensureRoot(t.Context()))
		assert.DirExists(t, c.root)
		assert.Equal(t, []string{c.root}, excluded)
	})
	t.Run("leaves an existing directory alone", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		require.NoError(t, os.MkdirAll(c.root, 0o750))
		c.exclude = func(context.Context, string) error {
			t.Fatal("excluded an existing directory")
			return nil
		}
		assert.NoError(t, c.ensureRoot(t.Context()))
	})
	t.Run("failed exclusion is a warning", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		c.exclude = func(context.Context, string) error { return errTest }
		require.NoError(t, c.ensureRoot(t.Context()))
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "Time Machine")
	})
	t.Run("uncreatable directory", func(t *testing.T) {
		t.Parallel()
		blocker := filepath.Join(t.TempDir(), "file")
		require.NoError(t, os.WriteFile(blocker, nil, 0o600))
		c := newTestCache(t, nil)
		c.root = filepath.Join(blocker, "cache")
		assert.Error(t, c.ensureRoot(t.Context()))
	})
}
