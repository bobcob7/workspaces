package cache

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

func TestCache_Fetch(t *testing.T) {
	t.Parallel()
	t.Run("gets new commits", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		commit := up.Commit(t, "o", "n", "refs/heads/main")
		require.NoError(t, c.Fetch(t.Context(), id))
		assert.Equal(t, commit, up.Git(t, c.Path(id), "rev-parse", "refs/heads/main"))
	})
	t.Run("prunes deleted branches but keeps fetched commits", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		up.Repo(t, "o", "n")
		up.Commit(t, "o", "n", "refs/heads/feat")
		id := testID(t, "o", "n")
		_, err := c.Add(t.Context(), id, "https://example.test/o/n.git")
		require.NoError(t, err)
		head := up.Git(t, c.Path(id), "rev-parse", "refs/heads/main")
		up.Git(t, c.Path(id), "update-ref", "refs/ws/commits/"+head, head)
		up.Git(t, up.Path("o", "n"), "update-ref", "-d", "refs/heads/feat")
		require.NoError(t, c.Fetch(t.Context(), id))
		refs := up.Git(t, c.Path(id), "for-each-ref", "--format=%(refname)")
		assert.NotContains(t, refs, "refs/heads/feat")
		assert.Contains(t, refs, "refs/ws/commits/"+head)
	})
	t.Run("follows a new default branch", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		up.Commit(t, "o", "n", "refs/heads/trunk")
		up.Git(t, up.Path("o", "n"), "symbolic-ref", "HEAD", "refs/heads/trunk")
		require.NoError(t, c.Fetch(t.Context(), id))
		assert.Equal(t, "trunk", up.Git(t, c.Path(id), "symbolic-ref", "--short", "HEAD"))
		assert.Empty(t, c.Warnings())
	})
	t.Run("parallel fetches after a force-push all succeed", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		for i := range 5 {
			up.Commit(t, "o", "n", fmt.Sprintf("refs/heads/b%d", i))
		}
		up.Git(t, up.Path("o", "n"), "update-ref", "refs/heads/main", up.Commit(t, "o", "n", "refs/heads/rewrite"))
		errs := make(chan error, 8)
		for range 8 {
			go func() { errs <- c.Fetch(t.Context(), id) }()
		}
		for range 8 {
			assert.NoError(t, <-errs)
		}
	})
	t.Run("not cached", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		assert.ErrorIs(t, c.Fetch(t.Context(), testID(t, "o", "n")), ErrRepoNotFound)
	})
	t.Run("upstream gone", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		require.NoError(t, os.RemoveAll(up.Path("o", "n")))
		assert.ErrorIs(t, c.Fetch(t.Context(), id), ErrFetchFailed)
	})
	t.Run("lock failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		require.NoError(t, os.MkdirAll(c.lockPath(id), 0o750))
		assert.ErrorContains(t, c.Fetch(t.Context(), id), "lock")
	})
}

func TestCache_FetchEach(t *testing.T) {
	t.Parallel()
	t.Run("returns each repo's result in order", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, dir string, args ...string) ([]byte, error) {
			if args[0] == "fetch" && strings.Contains(dir, "/bad.git") {
				return nil, gitFailure("fatal: unable to access")
			}
			return nil, nil
		}}
		c := newTestCache(t, g)
		ids := []repo.ID{testID(t, "o", "a"), testID(t, "o", "bad"), testID(t, "o", "c")}
		for _, id := range ids {
			fakeEntry(t, c, id)
		}
		errs := c.FetchEach(t.Context(), ids)
		require.Len(t, errs, 3)
		require.NoError(t, errs[0])
		require.ErrorIs(t, errs[1], ErrFetchFailed)
		assert.NoError(t, errs[2])
	})
	t.Run("real fetches of several repos", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		var ids []repo.ID
		for _, name := range []string{"a", "b"} {
			up.Repo(t, "o", name)
			id := testID(t, "o", name)
			_, err := c.Add(t.Context(), id, gittest.URL("o", name))
			require.NoError(t, err)
			ids = append(ids, id)
		}
		for _, err := range c.FetchEach(t.Context(), ids) {
			assert.NoError(t, err)
		}
	})
	t.Run("no repos", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, newTestCache(t, failingGit()).FetchEach(t.Context(), nil))
	})
}

func TestCache_updateHead(t *testing.T) {
	answer := func(lsRemote string, lsErr, symErr error) *gitRunnerMock {
		return &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "ls-remote" {
				return []byte(lsRemote), lsErr
			}
			return nil, symErr
		}}
	}
	t.Run("points HEAD at upstream's default branch", func(t *testing.T) {
		g := answer("ref: refs/heads/trunk\tHEAD\nabc\tHEAD\n", nil, nil)
		c := newTestCache(t, g)
		c.updateHead(t.Context(), testID(t, "o", "n"))
		require.Len(t, g.RunCalls(), 2)
		assert.Equal(t, []string{"symbolic-ref", "HEAD", "refs/heads/trunk"}, g.RunCalls()[1].Args)
		assert.Empty(t, c.Warnings())
	})
	t.Run("ls-remote failure is a warning", func(t *testing.T) {
		c := newTestCache(t, answer("", errTest, nil))
		c.updateHead(t.Context(), testID(t, "o", "n"))
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "default branch")
	})
	t.Run("no HEAD branch is a warning", func(t *testing.T) {
		c := newTestCache(t, answer("abc\tHEAD\n", nil, nil))
		c.updateHead(t.Context(), testID(t, "o", "n"))
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "no HEAD branch")
	})
	t.Run("symbolic-ref failure is a warning", func(t *testing.T) {
		c := newTestCache(t, answer("ref: refs/heads/trunk\tHEAD\n", nil, errTest))
		c.updateHead(t.Context(), testID(t, "o", "n"))
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "trunk")
	})
}

func TestParseSymref(t *testing.T) {
	t.Run("branch", func(t *testing.T) {
		branch, ok := parseSymref("ref: refs/heads/main\tHEAD\nabc123\tHEAD\n")
		require.True(t, ok)
		assert.Equal(t, "main", branch)
	})
	t.Run("branch with slashes", func(t *testing.T) {
		branch, ok := parseSymref("ref: refs/heads/release/1.x\tHEAD\n")
		require.True(t, ok)
		assert.Equal(t, "release/1.x", branch)
	})
	t.Run("detached HEAD", func(t *testing.T) {
		_, ok := parseSymref("abc123\tHEAD\n")
		assert.False(t, ok)
	})
	t.Run("not a branch", func(t *testing.T) {
		_, ok := parseSymref("ref: refs/tags/v1\tHEAD\n")
		assert.False(t, ok)
	})
	t.Run("empty", func(t *testing.T) {
		_, ok := parseSymref("")
		assert.False(t, ok)
	})
	t.Run("skips other names", func(t *testing.T) {
		branch, ok := parseSymref("ref: refs/heads/x\tother\nref: refs/heads/main\tHEAD\n")
		require.True(t, ok)
		assert.Equal(t, "main", branch)
	})
}
