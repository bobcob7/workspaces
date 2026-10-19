package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

// cloneFromEnv caches o/n, resolves ref, and clones it with cloneRepo into
// a temp directory, returning the repo's directory and record.
func cloneFromEnv(t *testing.T, e *gitEnv, ref string) (string, Repo) {
	t.Helper()
	id, err := repo.NewID(gittest.Host, "o", "n")
	require.NoError(t, err)
	_, err = e.cache.Add(t.Context(), id, gittest.URL("o", "n"))
	require.NoError(t, err)
	resolved, err := e.cache.ResolveRef(t.Context(), id, ref)
	require.NoError(t, err)
	r := Repo{ID: id.String(), Path: "n", Upstream: gittest.URL("o", "n"), Ref: resolved.Name, RefKind: string(resolved.Kind), Commit: resolved.Commit}
	dir := filepath.Join(t.TempDir(), "n")
	require.NoError(t, e.store.cloneRepo(t.Context(), dir, id, &r))
	return dir, r
}

func TestStore_cloneRepo(t *testing.T) {
	t.Parallel()
	t.Run("branch tracks origin and pushes there", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		dir, _ := cloneFromEnv(t, e, "feat")
		assert.Equal(t, "feat", e.gitOut(t, dir, "branch", "--show-current"))
		assert.Equal(t, "origin/feat", e.gitOut(t, dir, "rev-parse", "--abbrev-ref", "@{upstream}"))
		assert.Equal(t, "origin", e.gitOut(t, dir, "config", "remote.pushDefault"))
		assert.Equal(t, gittest.URL("o", "n"), e.gitOut(t, dir, "config", "remote.origin.url"))
		id, err := repo.NewID(gittest.Host, "o", "n")
		require.NoError(t, err)
		assert.Equal(t, e.cache.Path(id), e.gitOut(t, dir, "config", "remote.cache.url"))
	})
	t.Run("origin has every branch and tag, without network", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		dir, _ := cloneFromEnv(t, e, "")
		assert.Equal(t, "origin/feat\norigin/main", e.gitOut(t, dir, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin/"))
		assert.Equal(t, "v1", e.gitOut(t, dir, "tag"))
	})
	t.Run("tag is checked out detached", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		dir, r := cloneFromEnv(t, e, "v1")
		assert.Empty(t, e.gitOut(t, dir, "branch", "--show-current"))
		assert.Equal(t, r.Commit, e.gitOut(t, dir, "rev-parse", "HEAD"))
	})
	t.Run("a commit only the cache's refs/ws/commits has", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		pr := e.up.Commit(t, "o", "n", "refs/pull/1/head")
		dir, _ := cloneFromEnv(t, e, pr)
		assert.Equal(t, pr, e.gitOut(t, dir, "rev-parse", "HEAD"))
	})
	t.Run("objects are hard links into the cache", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		dir, _ := cloneFromEnv(t, e, "")
		id, err := repo.NewID(gittest.Host, "o", "n")
		require.NoError(t, err)
		objects, err := filepath.Glob(filepath.Join(e.cache.Path(id), "objects", "??", "*"))
		require.NoError(t, err)
		require.NotEmpty(t, objects)
		for _, obj := range objects {
			rel, err := filepath.Rel(e.cache.Path(id), obj)
			require.NoError(t, err)
			cached, err := os.Stat(obj)
			require.NoError(t, err)
			cloned, err := os.Stat(filepath.Join(dir, ".git", rel))
			require.NoError(t, err)
			assert.True(t, os.SameFile(cached, cloned), "ADR 0009: %s", rel)
		}
	})
	t.Run("survives gc in the cache", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		dir, _ := cloneFromEnv(t, e, "feat")
		id, err := repo.NewID(gittest.Host, "o", "n")
		require.NoError(t, err)
		e.gitOut(t, e.cache.Path(id), "gc", "-q", "--prune=now")
		e.gitOut(t, dir, "fsck", "--full")
	})
	t.Run("config failure", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "config" {
				return nil, errTest
			}
			return nil, nil
		}}
		s := newTestStore(t, g, fakeCache())
		err := s.cloneRepo(t.Context(), "/w/n", repo.ID{Host: "h.com", Owner: "o", Name: "n"}, &Repo{RefKind: "branch", Ref: "main"})
		assert.ErrorIs(t, err, errTest)
	})
}

func TestStore_copyFromCache(t *testing.T) {
	id := repo.ID{Host: "h.com", Owner: "o", Name: "n"}
	failOn := func(sub string) *gitRunnerMock {
		return &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == sub {
				return nil, errTest
			}
			return nil, nil
		}}
	}
	t.Run("runs the clone sequence under the shared lock", func(t *testing.T) {
		c := fakeCache()
		locked, unlocked := 0, 0
		c.SharedLockFunc = func(context.Context, repo.ID) (func(), error) {
			locked++
			return func() { unlocked++ }, nil
		}
		g := okGit()
		require.NoError(t, newTestStore(t, g, c).copyFromCache(t.Context(), "/w/n", id, &Repo{Upstream: "u", RefKind: "branch"}))
		assert.Equal(t, 1, locked)
		assert.Equal(t, 1, unlocked)
		calls := g.RunCalls()
		require.Len(t, calls, 3)
		assert.Equal(t, []string{"clone", "-q", "-o", "cache", "--no-checkout", "--", "/cache/h.com/o/n.git", "/w/n"}, calls[0].Args)
		assert.Equal(t, "/w", calls[0].Dir)
		assert.Equal(t, []string{"remote", "add", "origin", "u"}, calls[1].Args)
		assert.Equal(t, []string{"fetch", "-q", "--no-tags", "cache", "+refs/heads/*:refs/remotes/origin/*", "+refs/tags/*:refs/tags/*"}, calls[2].Args)
	})
	t.Run("lock failure", func(t *testing.T) {
		c := fakeCache()
		c.SharedLockFunc = func(context.Context, repo.ID) (func(), error) { return nil, errTest }
		assert.ErrorIs(t, newTestStore(t, okGit(), c).copyFromCache(t.Context(), "/w/n", id, &Repo{}), errTest)
	})
	t.Run("clone failure", func(t *testing.T) {
		assert.ErrorIs(t, newTestStore(t, failOn("clone"), fakeCache()).copyFromCache(t.Context(), "/w/n", id, &Repo{}), errTest)
	})
	t.Run("remote failure", func(t *testing.T) {
		assert.ErrorIs(t, newTestStore(t, failOn("remote"), fakeCache()).copyFromCache(t.Context(), "/w/n", id, &Repo{}), errTest)
	})
	t.Run("fetch failure", func(t *testing.T) {
		assert.ErrorIs(t, newTestStore(t, failOn("fetch"), fakeCache()).copyFromCache(t.Context(), "/w/n", id, &Repo{}), errTest)
	})
	t.Run("a commit is fetched if missing", func(t *testing.T) {
		g := failOn("cat-file")
		g.RunFunc = func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "cat-file" {
				return nil, &git.CommandError{Args: args, ExitCode: 1, Err: errTest}
			}
			return nil, nil
		}
		require.NoError(t, newTestStore(t, g, fakeCache()).copyFromCache(t.Context(), "/w/n", id, &Repo{RefKind: string(cache.RefCommit), Commit: "abc"}))
		last := g.RunCalls()[len(g.RunCalls())-1]
		assert.Equal(t, []string{"fetch", "-q", "--no-tags", "cache", "refs/ws/commits/abc"}, last.Args)
	})
}

func TestStore_fetchCommit(t *testing.T) {
	missing := func(args []string) error { return &git.CommandError{Args: args, ExitCode: 1, Err: errTest} }
	t.Run("a reachable commit needs nothing", func(t *testing.T) {
		g := okGit()
		require.NoError(t, newTestStore(t, g, nil).fetchCommit(t.Context(), "/w/n", "abc"))
		assert.Len(t, g.RunCalls(), 1)
	})
	t.Run("a missing commit is fetched", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "cat-file" {
				return nil, missing(args)
			}
			return nil, nil
		}}
		require.NoError(t, newTestStore(t, g, nil).fetchCommit(t.Context(), "/w/n", "abc"))
		assert.Len(t, g.RunCalls(), 2)
	})
	t.Run("a failed fetch", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) { return nil, missing(args) }}
		err := newTestStore(t, g, nil).fetchCommit(t.Context(), "/w/n", "abc")
		assert.ErrorContains(t, err, "fetching commit abc from the cache")
	})
	t.Run("git that couldn't run", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, git.ErrNotInstalled }}
		err := newTestStore(t, g, nil).fetchCommit(t.Context(), "/w/n", "abc")
		assert.ErrorIs(t, err, git.ErrNotInstalled)
	})
}
