package workspace

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

var errTest = errors.New("boom")

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// testNow is the clock newTestStore's stores use.
func testNow() time.Time {
	return time.Date(2026, 10, 18, 8, 12, 0, 0, time.UTC)
}

// newTestStore returns a store under a temp CONFIG_DIR with g and c, a
// fixed clock and suffix, and no Time Machine exclusion.
func newTestStore(t *testing.T, g gitRunner, c repoCache) *Store {
	t.Helper()
	paths := config.NewPaths(t.TempDir())
	s := New(discardLogger(), g, c, paths.Workspaces, paths.State)
	s.exclude = func(string) {}
	s.now = testNow
	s.suffix = func() (string, error) { return "k3f9qa", nil }
	return s
}

// recordingGit runs the real git and records every command's arguments.
type recordingGit struct {
	runner *git.Runner
	mu     sync.Mutex
	calls  [][]string
}

func (r *recordingGit) Run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, slices.Clone(args))
	r.mu.Unlock()
	return r.runner.Run(ctx, dir, args...)
}

// gitEnv is a store wired to real git, a real cache, and a gittest
// upstream with repos o/n (branches main and feat, tag v1) and p/n.
type gitEnv struct {
	store *Store
	cache *cache.Cache
	up    *gittest.Upstream
	git   *recordingGit
	// main is o/n's main commit.
	main string
}

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	up := gittest.NewUpstream(t)
	main := up.Repo(t, "o", "n")
	up.Commit(t, "o", "n", "refs/heads/feat")
	up.Tag(t, "o", "n", "v1", main)
	up.Repo(t, "p", "n")
	runner := git.NewRunner(discardLogger(), "git", up.Environ)
	paths := config.NewPaths(t.TempDir())
	require.NoError(t, os.MkdirAll(paths.Cache, 0o750))
	c := cache.New(discardLogger(), runner, paths.Cache, config.Defaults{Host: gittest.Host, Owners: []string{"o"}})
	rec := &recordingGit{runner: runner}
	s := newTestStore(t, rec, c)
	s.root, s.stateDir = paths.Workspaces, paths.State
	return &gitEnv{store: s, cache: c, up: up, git: rec, main: main}
}

// git runs git in dir with the upstream's environment.
func (e *gitEnv) gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return e.up.Git(t, dir, args...)
}

func mustSpecs(t *testing.T, args ...string) []repo.Spec {
	t.Helper()
	specs := make([]repo.Spec, 0, len(args))
	for _, arg := range args {
		sp, err := repo.ParseSpec(arg)
		require.NoError(t, err)
		specs = append(specs, sp)
	}
	return specs
}

func TestNew(t *testing.T) {
	g := &gitRunnerMock{}
	c := &repoCacheMock{}
	s := New(discardLogger(), g, c, "/cfg/workspaces", "/cfg/state")
	assert.Same(t, g, s.git)
	assert.Same(t, c, s.cache)
	assert.Equal(t, "/cfg/workspaces", s.root)
	assert.Equal(t, "/cfg/state", s.stateDir)
	assert.NotNil(t, s.exclude)
	assert.NotNil(t, s.now)
	assert.NotNil(t, s.suffix)
}

func TestStore_Warnings(t *testing.T) {
	s := newTestStore(t, nil, nil)
	assert.Empty(t, s.Warnings())
	s.warn("a %d", 1)
	got := s.Warnings()
	got[0] = "changed"
	assert.Equal(t, []string{"a 1"}, s.Warnings(), "returns a copy")
}

func TestStore_warn(t *testing.T) {
	s := newTestStore(t, nil, nil)
	s.warn("x %s", "y")
	assert.Equal(t, []string{"x y"}, s.Warnings())
}

func TestStore_dir(t *testing.T) {
	s := New(discardLogger(), nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/workspaces/fix-login", s.dir("fix-login"))
}

func TestStore_stateOf(t *testing.T) {
	s := New(discardLogger(), nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/state/fix-login", s.stateOf("fix-login"))
}

func TestStore_recordPath(t *testing.T) {
	s := New(discardLogger(), nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/state/fix-login/workspace.toml", s.recordPath("fix-login"))
}

func TestStore_lockPath(t *testing.T) {
	s := New(discardLogger(), nil, nil, "/cfg/workspaces", "/cfg/state")
	assert.Equal(t, "/cfg/state/fix-login/lock", s.lockPath("fix-login"))
}

func TestStore_ensureRoots(t *testing.T) {
	t.Parallel()
	t.Run("creates both and excludes workspaces/ from backups", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		var excluded []string
		s.exclude = func(dir string) {
			excluded = append(excluded, dir)
		}
		require.NoError(t, s.ensureRoots())
		assert.DirExists(t, s.root)
		assert.DirExists(t, s.stateDir)
		assert.Equal(t, []string{s.root}, excluded)
	})
	t.Run("leaves an existing workspaces/ alone", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		require.NoError(t, os.MkdirAll(s.root, 0o750))
		s.exclude = func(string) {
			t.Fatal("excluded an existing directory")
		}
		require.NoError(t, s.ensureRoots())
	})
	t.Run("uncreatable state directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		writeFile(t, s.stateDir, "")
		assert.Error(t, s.ensureRoots())
	})
	t.Run("uncreatable workspace directory", func(t *testing.T) {
		t.Parallel()
		s := newTestStore(t, nil, nil)
		blocker := filepath.Join(t.TempDir(), "file")
		writeFile(t, blocker, "")
		s.root = filepath.Join(blocker, "workspaces")
		assert.Error(t, s.ensureRoots())
	})
}
