package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
)

// addCommitRef caches o/n and gives it a refs/ws/commits ref for a commit
// only upstream's other branch has.
func addCommitRef(t *testing.T) (*Cache, repo.ID, string) {
	t.Helper()
	c, up := newGitCache(t)
	id := addRepo(t, c, up)
	commit := up.Commit(t, "o", "n", "refs/pull/1/head")
	_, err := c.ResolveRef(t.Context(), id, commit)
	require.NoError(t, err)
	return c, id, commit
}

func keepNothing(repo.ID, string) (bool, error) { return false, nil }

func TestCache_Prune(t *testing.T) {
	t.Parallel()
	t.Run("deletes unused commit refs", func(t *testing.T) {
		t.Parallel()
		c, id, commit := addCommitRef(t)
		pruned, err := c.Prune(t.Context(), keepNothing, false)
		require.NoError(t, err)
		assert.Equal(t, []PrunedRef{{ID: id, Commit: commit}}, pruned)
		refs, err := c.commitRefs(t.Context(), id)
		require.NoError(t, err)
		assert.Empty(t, refs)
	})
	t.Run("keeps used commit refs", func(t *testing.T) {
		t.Parallel()
		c, id, commit := addCommitRef(t)
		keep := func(got repo.ID, hash string) (bool, error) { return got == id && hash == commit, nil }
		pruned, err := c.Prune(t.Context(), keep, false)
		require.NoError(t, err)
		assert.Empty(t, pruned)
	})
	t.Run("dry run deletes nothing", func(t *testing.T) {
		t.Parallel()
		c, id, commit := addCommitRef(t)
		pruned, err := c.Prune(t.Context(), keepNothing, true)
		require.NoError(t, err)
		assert.Len(t, pruned, 1)
		refs, err := c.commitRefs(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, []string{commit}, refs)
	})
	t.Run("empty cache", func(t *testing.T) {
		t.Parallel()
		pruned, err := newTestCache(t, failingGit()).Prune(t.Context(), keepNothing, false)
		require.NoError(t, err)
		assert.Empty(t, pruned)
	})
	t.Run("stops at a failing repo", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.Prune(t.Context(), keepNothing, false)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestCache_pruneRepo(t *testing.T) {
	t.Parallel()
	t.Run("reports a failed delete", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "for-each-ref" {
				return []byte("refs/ws/commits/abc\n"), nil
			}
			return nil, errTest
		}}
		c := newTestCache(t, g)
		_, err := c.pruneRepo(t.Context(), testID(t, "o", "n"), keepNothing, false)
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("stops when keep fails", func(t *testing.T) {
		t.Parallel()
		var deleted bool
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "for-each-ref" {
				return []byte("refs/ws/commits/abc\n"), nil
			}
			deleted = true
			return nil, nil
		}}
		c := newTestCache(t, g)
		keep := func(repo.ID, string) (bool, error) { return false, errTest }
		_, err := c.pruneRepo(t.Context(), testID(t, "o", "n"), keep, false)
		require.ErrorIs(t, err, errTest)
		assert.False(t, deleted)
	})
	t.Run("asks keep while holding the repo's lock", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/ws/commits/abc\n"), nil
		}}
		c := newTestCache(t, g)
		id := testID(t, "o", "n")
		var lockedDuringKeep bool
		keep := func(repo.ID, string) (bool, error) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			_, err := c.SharedLock(ctx, id)
			lockedDuringKeep = err != nil
			return true, nil
		}
		_, err := c.pruneRepo(t.Context(), id, keep, false)
		require.NoError(t, err)
		assert.True(t, lockedDuringKeep)
	})
	t.Run("lock failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		unlock, err := c.lock(t.Context(), id)
		require.NoError(t, err)
		defer unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = c.pruneRepo(ctx, id, keepNothing, false)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

func TestCache_commitRefs(t *testing.T) {
	t.Run("reads hashes", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/ws/commits/abc\nrefs/ws/commits/def\n"), nil
		}}
		refs, err := newTestCache(t, g).commitRefs(t.Context(), testID(t, "o", "n"))
		require.NoError(t, err)
		assert.Equal(t, []string{"abc", "def"}, refs)
	})
	t.Run("git failure", func(t *testing.T) {
		_, err := newTestCache(t, failingGit()).commitRefs(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}
