package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/repo"
)

// assertNothingCreated checks that a failed Create left no workspace and no
// state behind. The workspace root's CLAUDE.md isn't a workspace.
func assertNothingCreated(t *testing.T, s *Store) {
	t.Helper()
	for _, dir := range []string{s.root, s.stateDir} {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		var names []string
		for _, e := range entries {
			if dir != s.root || e.Name() != claudeMDName {
				names = append(names, e.Name())
			}
		}
		assert.Empty(t, names, dir)
	}
}

// createUnlocked runs Create and unlocks the lock it returns, for tests
// that don't hand it to a session.
func createUnlocked(t *testing.T, s *Store, opts CreateOptions) (Workspace, error) {
	t.Helper()
	ws, lock, err := s.Create(t.Context(), opts)
	if lock != nil {
		lock.Unlock()
	}
	return ws, err
}

func TestStore_Create(t *testing.T) {
	t.Parallel()
	t.Run("returns the workspace's lock still held", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		ws, lock, err := e.store.Create(t.Context(), CreateOptions{Name: "held", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		require.NotNil(t, lock)
		defer lock.Unlock()
		assert.Equal(t, e.store.lockPath(ws.ID), lock.File().Name())
		_, ok, err := lockfile.TryExclusive(e.store.lockPath(ws.ID))
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("a failure returns no lock", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, lock, err := e.store.Create(t.Context(), CreateOptions{Specs: mustSpecs(t, "n@nope")})
		require.Error(t, err)
		assert.Nil(t, lock)
	})
	t.Run("default branch, generated id", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		ws, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		assert.Equal(t, "ws-20261018-k3f9qa", ws.ID)
		assert.Equal(t, filepath.Join(e.store.root, ws.ID), ws.Path)
		assert.Equal(t, testNow(), ws.CreatedAt)
		assert.Equal(t, []Repo{{
			ID: "example.test/o/n", Spec: "n", Path: "n", Upstream: gittest.URL("o", "n"),
			Ref: "main", RefKind: "branch", Commit: e.main,
		}}, ws.Repos)
		got, err := readRecord(e.store.recordPath(ws.ID))
		require.NoError(t, err)
		assert.Equal(t, ws, got)
		assert.NoDirExists(t, filepath.Join(e.store.stateOf(ws.ID), "building"))
		assert.Equal(t, "origin/main", e.gitOut(t, filepath.Join(ws.Path, "n"), "rev-parse", "--abbrev-ref", "@{upstream}"))
	})
	t.Run("--name becomes the id with a random suffix", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		ws, err := createUnlocked(t, e.store, CreateOptions{Name: "This is a Test", Specs: mustSpecs(t, "o/n")})
		require.NoError(t, err)
		assert.Equal(t, "this-is-a-test-00000001", ws.ID)
		assert.Equal(t, "This is a Test", ws.Name)
		assert.Equal(t, filepath.Join(e.store.root, "this-is-a-test-00000001"), ws.Path)
	})
	t.Run("--name is kept without surrounding space", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		ws, err := createUnlocked(t, e.store, CreateOptions{Name: "  Fix Login "})
		require.NoError(t, err)
		assert.Equal(t, "Fix Login", ws.Name)
	})
	t.Run("no repos makes an empty workspace", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		ws, err := createUnlocked(t, e.store, CreateOptions{Name: "scratch"})
		require.NoError(t, err)
		assert.Empty(t, ws.Repos)
		entries, err := os.ReadDir(ws.Path)
		require.NoError(t, err)
		assert.Empty(t, entries)
		got, err := e.store.Find(ws.ID)
		require.NoError(t, err)
		assert.Equal(t, ws.Path, got.Path)
	})
	t.Run("several repos", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		e.up.Repo(t, "o", "api")
		ws, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "n@feat", "o/api@main")})
		require.NoError(t, err)
		require.Len(t, ws.Repos, 2)
		assert.DirExists(t, filepath.Join(ws.Path, "n", ".git"))
		assert.DirExists(t, filepath.Join(ws.Path, "api", ".git"))
	})
	t.Run("offline uses the cache as it is", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Name: "first", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		newer := e.up.Commit(t, "o", "n", "refs/heads/main")
		ws, err := createUnlocked(t, e.store, CreateOptions{Name: "second", Specs: mustSpecs(t, "n"), Offline: true})
		require.NoError(t, err)
		assert.Equal(t, e.main, ws.Repos[0].Commit)
		assert.NotEqual(t, newer, ws.Repos[0].Commit)
	})
	t.Run("online fetches a cached repo first", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Name: "first", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		newer := e.up.Commit(t, "o", "n", "refs/heads/main")
		ws, err := createUnlocked(t, e.store, CreateOptions{Name: "second", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		assert.Equal(t, newer, ws.Repos[0].Commit)
	})
	t.Run("offline with an uncached repo", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "n"), Offline: true})
		require.ErrorIs(t, err, cache.ErrRepoNotFound)
		assertNothingCreated(t, e.store)
	})
	t.Run("a name used twice makes two workspaces", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		first, err := createUnlocked(t, e.store, CreateOptions{Name: "x", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		second, err := createUnlocked(t, e.store, CreateOptions{Name: "X", Specs: mustSpecs(t, "n")})
		require.NoError(t, err)
		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, "X", second.Name)
	})
	t.Run("a name with nothing usable", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Name: "日本語", Specs: mustSpecs(t, "n")})
		require.ErrorIs(t, err, ErrNameInvalid)
		assertNothingCreated(t, e.store)
	})
	t.Run("a name with a control character", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Name: "a\tb", Specs: mustSpecs(t, "n")})
		require.ErrorIs(t, err, ErrNameInvalid)
		assertNothingCreated(t, e.store)
	})
	t.Run("a missing ref removes everything", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "n@nope")})
		require.ErrorIs(t, err, cache.ErrRefNotFound)
		assertNothingCreated(t, e.store)
	})
	t.Run("the same repo twice", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "o/n", "example.test/o/n@feat")})
		require.ErrorIs(t, err, ErrSpecConflict)
		assertNothingCreated(t, e.store)
	})
	t.Run("two repos with one name", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Specs: mustSpecs(t, "o/n", "p/n")})
		assert.ErrorIs(t, err, ErrSpecConflict)
	})
	t.Run("never runs git worktree", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		_, err := createUnlocked(t, e.store, CreateOptions{Name: "a", Specs: mustSpecs(t, "n@feat")})
		require.NoError(t, err)
		_, err = createUnlocked(t, e.store, CreateOptions{Name: "b", Specs: mustSpecs(t, "n@v1")})
		require.NoError(t, err)
		require.NotEmpty(t, e.git.calls)
		for _, args := range e.git.calls {
			assert.NotEqual(t, "worktree", args[0], "ADR 0017")
		}
	})
	t.Run("two workspaces on one branch can both commit", func(t *testing.T) {
		t.Parallel()
		e := newGitEnv(t)
		var paths []string
		for _, name := range []string{"a", "b"} {
			ws, err := createUnlocked(t, e.store, CreateOptions{Name: name, Specs: mustSpecs(t, "n@feat")})
			require.NoError(t, err)
			e.gitOut(t, filepath.Join(ws.Path, "n"), "commit", "-q", "--allow-empty", "-m", "work in "+name)
			paths = append(paths, ws.Path)
		}
		a := e.gitOut(t, filepath.Join(paths[0], "n"), "rev-parse", "HEAD")
		b := e.gitOut(t, filepath.Join(paths[1], "n"), "rev-parse", "HEAD")
		assert.NotEqual(t, a, b, "ADR 0017")
	})
}

// fakeCache is a repoCache mock for unit tests where every repo resolves
// to example.test/<owner>/<name> and every call succeeds.
func fakeCache() *repoCacheMock {
	resolve := func(_ context.Context, sp *repo.Spec) (cache.Resolution, error) {
		owner := sp.Owner
		if owner == "" {
			owner = "o"
		}
		id, err := repo.NewID(gittest.Host, owner, sp.Name)
		return cache.Resolution{ID: id, Upstream: id.UpstreamURL(), Cached: true}, err
	}
	return &repoCacheMock{
		ResolveFunc: resolve,
		LookupFunc:  resolve,
		AddFunc:     func(context.Context, repo.ID, string) (bool, error) { return true, nil },
		FetchFunc:   func(context.Context, repo.ID) error { return nil },
		ResolveRefFunc: func(context.Context, repo.ID, string) (cache.Ref, error) {
			return cache.Ref{Name: "main", Kind: cache.RefBranch, Commit: "abc"}, nil
		},
		SharedLockFunc: func(context.Context, repo.ID) (func(), error) { return func() {}, nil },
		PathFunc:       func(id repo.ID) string { return "/cache/" + id.String() + ".git" },
	}
}

func okGit() *gitRunnerMock {
	return &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }}
}

func TestStore_resolveAll(t *testing.T) {
	t.Run("keeps spec order", func(t *testing.T) {
		s := newTestStore(t, nil, fakeCache())
		targets, err := s.resolveAll(t.Context(), mustSpecs(t, "a", "o/b"), false)
		require.NoError(t, err)
		require.Len(t, targets, 2)
		assert.Equal(t, "a", targets[0].res.ID.Name)
		assert.Equal(t, "b", targets[1].res.ID.Name)
	})
	t.Run("the same repo twice", func(t *testing.T) {
		s := newTestStore(t, nil, fakeCache())
		_, err := s.resolveAll(t.Context(), mustSpecs(t, "a", "o/a"), false)
		require.ErrorIs(t, err, ErrSpecConflict)
		assert.ErrorContains(t, err, "listed twice")
	})
	t.Run("two repos with one name", func(t *testing.T) {
		s := newTestStore(t, nil, fakeCache())
		_, err := s.resolveAll(t.Context(), mustSpecs(t, "o/a", "p/a"), false)
		require.ErrorIs(t, err, ErrSpecConflict)
		assert.ErrorContains(t, err, `checked out as "a"`)
	})
	t.Run("resolution failure", func(t *testing.T) {
		c := fakeCache()
		c.ResolveFunc = func(context.Context, *repo.Spec) (cache.Resolution, error) { return cache.Resolution{}, errTest }
		_, err := newTestStore(t, nil, c).resolveAll(t.Context(), mustSpecs(t, "a"), false)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestStore_resolve(t *testing.T) {
	t.Run("online resolves", func(t *testing.T) {
		c := fakeCache()
		sp := mustSpecs(t, "a")[0]
		_, err := newTestStore(t, nil, c).resolve(t.Context(), &sp, false)
		require.NoError(t, err)
		assert.Len(t, c.ResolveCalls(), 1)
		assert.Empty(t, c.LookupCalls())
	})
	t.Run("offline only looks up", func(t *testing.T) {
		c := fakeCache()
		sp := mustSpecs(t, "a")[0]
		_, err := newTestStore(t, nil, c).resolve(t.Context(), &sp, true)
		require.NoError(t, err)
		assert.Len(t, c.LookupCalls(), 1)
		assert.Empty(t, c.ResolveCalls())
	})
}

func TestStore_claim(t *testing.T) {
	t.Parallel()
	t.Run("sanitizes --name", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		id, lock, err := s.claim("Fix Login")
		require.NoError(t, err)
		defer lock.Unlock()
		assert.Equal(t, "fix-login-00000001", id)
	})
	t.Run("retries a taken generated id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		suffixes := []string{"aaaaaa", "bbbbbb"}
		s.suffix = func() (string, error) {
			next := suffixes[0]
			suffixes = suffixes[1:]
			return next, nil
		}
		require.NoError(t, os.Mkdir(s.stateOf("ws-20261018-aaaaaa"), 0o750))
		id, lock, err := s.claim("")
		require.NoError(t, err)
		defer lock.Unlock()
		assert.Equal(t, "ws-20261018-bbbbbb", id)
	})
	t.Run("gives up after repeated collisions", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		require.NoError(t, os.Mkdir(s.stateOf("ws-20261018-k3f9qa"), 0o750))
		_, _, err := s.claim("")
		assert.ErrorIs(t, err, ErrExists)
	})
	t.Run("retries a taken named id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		require.NoError(t, os.Mkdir(s.stateOf("fix-login-00000001"), 0o750))
		id, lock, err := s.claim("Fix Login")
		require.NoError(t, err)
		defer lock.Unlock()
		assert.Equal(t, "fix-login-00000002", id)
	})
	t.Run("name suffix failure", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		s.nameSuffix = func() (string, error) { return "", errTest }
		_, _, err := s.claim("x")
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("suffix failure", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		s.suffix = func() (string, error) { return "", errTest }
		_, _, err := s.claim("")
		assert.ErrorIs(t, err, errTest)
	})
}

func TestStore_claimID(t *testing.T) {
	t.Parallel()
	t.Run("creates and locks the state directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		_, lock, err := s.claimID("x")
		require.NoError(t, err)
		defer lock.Unlock()
		assert.DirExists(t, s.stateOf("x"))
		_, ok, err := lockfile.TryExclusive(s.lockPath("x"))
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("an unknown directory takes the id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.dir("x"), 0o750))
		_, _, err := s.claimID("x")
		assert.ErrorIs(t, err, ErrExists)
	})
	t.Run("existing state takes the id", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		_, _, err := s.claimID("x")
		assert.ErrorIs(t, err, ErrExists)
	})
	t.Run("missing state root", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		_, _, err := s.claimID("x")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrExists)
	})
	t.Run("unusable lock removes the state again", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, s.ensureRoots())
		s.stateDir = t.TempDir()
		require.NoError(t, os.Chmod(s.stateDir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(s.stateDir, 0o700) })
		_, _, err := s.claimID("x")
		assert.Error(t, err)
	})
}

func TestStore_build(t *testing.T) {
	t.Run("missing state directory", func(t *testing.T) {
		s := newTestStore(t, okGit(), fakeCache())
		_, err := s.build(t.Context(), "x", CreateOptions{}, nil)
		assert.Error(t, err)
	})
	t.Run("repo failure", func(t *testing.T) {
		c := fakeCache()
		c.FetchFunc = func(context.Context, repo.ID) error { return errTest }
		s := newTestStore(t, okGit(), c)
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		targets, err := s.resolveAll(t.Context(), mustSpecs(t, "a"), false)
		require.NoError(t, err)
		_, err = s.build(t.Context(), "x", CreateOptions{}, targets)
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("writes the record before moving into place", func(t *testing.T) {
		s := newTestStore(t, okGit(), fakeCache())
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		_, err := s.build(t.Context(), "x", CreateOptions{}, nil)
		require.ErrorContains(t, err, "moving x into place", "workspaces/ doesn't exist")
		assert.FileExists(t, s.recordPath("x"))
	})
	t.Run("a taken workspace path is left alone", func(t *testing.T) {
		s := newTestStore(t, okGit(), fakeCache())
		require.NoError(t, s.ensureRoots())
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		writeFile(t, filepath.Join(s.dir("x"), "keep"), "")
		_, err := s.build(t.Context(), "x", CreateOptions{}, nil)
		require.ErrorContains(t, err, "moving x into place")
		assert.FileExists(t, filepath.Join(s.dir("x"), "keep"))
	})
	t.Run("record write failure leaves no workspace directory", func(t *testing.T) {
		s := newTestStore(t, okGit(), fakeCache())
		require.NoError(t, s.ensureRoots())
		require.NoError(t, os.MkdirAll(filepath.Join(s.recordPath("x"), "child"), 0o750))
		_, err := s.build(t.Context(), "x", CreateOptions{}, nil)
		require.ErrorContains(t, err, "workspace record")
		assert.NoDirExists(t, s.dir("x"))
	})
}

func TestStore_addRepo(t *testing.T) {
	newTarget := func(t *testing.T, s *Store) target {
		t.Helper()
		targets, err := s.resolveAll(t.Context(), mustSpecs(t, "a@main"), false)
		require.NoError(t, err)
		return targets[0]
	}
	t.Run("records the resolved repo", func(t *testing.T) {
		s := newTestStore(t, okGit(), fakeCache())
		r, err := s.addRepo(t.Context(), t.TempDir(), new(newTarget(t, s)), false)
		require.NoError(t, err)
		assert.Equal(t, Repo{ID: "example.test/o/a", Spec: "a@main", Path: "a", Upstream: "https://example.test/o/a.git", Ref: "main", RefKind: "branch", Commit: "abc"}, r)
	})
	t.Run("offline doesn't touch upstream", func(t *testing.T) {
		c := fakeCache()
		s := newTestStore(t, okGit(), c)
		_, err := s.addRepo(t.Context(), t.TempDir(), new(newTarget(t, s)), true)
		require.NoError(t, err)
		assert.Empty(t, c.FetchCalls())
		assert.Empty(t, c.AddCalls())
	})
	t.Run("ref failure", func(t *testing.T) {
		c := fakeCache()
		c.ResolveRefFunc = func(context.Context, repo.ID, string) (cache.Ref, error) { return cache.Ref{}, cache.ErrRefNotFound }
		s := newTestStore(t, okGit(), c)
		_, err := s.addRepo(t.Context(), t.TempDir(), new(newTarget(t, s)), false)
		assert.ErrorIs(t, err, cache.ErrRefNotFound)
	})
	t.Run("clone failure", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, errTest }}
		s := newTestStore(t, g, fakeCache())
		_, err := s.addRepo(t.Context(), t.TempDir(), new(newTarget(t, s)), false)
		require.ErrorIs(t, err, errTest)
		assert.ErrorContains(t, err, "cloning example.test/o/a")
	})
}

func TestStore_refreshCache(t *testing.T) {
	id, err := repo.NewID(gittest.Host, "o", "a")
	require.NoError(t, err)
	t.Run("clones an uncached repo", func(t *testing.T) {
		c := fakeCache()
		require.NoError(t, newTestStore(t, nil, c).refreshCache(t.Context(), &cache.Resolution{ID: id, Upstream: "u"}))
		require.Len(t, c.AddCalls(), 1)
		assert.Equal(t, "u", c.AddCalls()[0].Upstream)
		assert.Empty(t, c.FetchCalls())
	})
	t.Run("fetches a cached repo", func(t *testing.T) {
		c := fakeCache()
		require.NoError(t, newTestStore(t, nil, c).refreshCache(t.Context(), &cache.Resolution{ID: id, Cached: true}))
		assert.Len(t, c.FetchCalls(), 1)
		assert.Empty(t, c.AddCalls())
	})
	t.Run("clone failure", func(t *testing.T) {
		c := fakeCache()
		c.AddFunc = func(context.Context, repo.ID, string) (bool, error) { return false, errTest }
		assert.ErrorIs(t, newTestStore(t, nil, c).refreshCache(t.Context(), &cache.Resolution{ID: id}), errTest)
	})
}

func TestStore_discard(t *testing.T) {
	t.Run("removes the state and its partial clones", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(filepath.Join(s.stateOf("x"), "building", "n"), 0o750))
		s.discard("x")
		assert.NoDirExists(t, s.stateOf("x"))
		assert.Empty(t, s.Warnings())
	})
	t.Run("leaves whatever is at the workspace path", func(t *testing.T) {
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(filepath.Join(s.dir("x"), "someone-elses"), 0o750))
		require.NoError(t, os.MkdirAll(s.stateOf("x"), 0o750))
		s.discard("x")
		assert.DirExists(t, filepath.Join(s.dir("x"), "someone-elses"))
	})
}
