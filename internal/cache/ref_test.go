package cache

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/repo"
)

func TestCache_ResolveRef(t *testing.T) {
	t.Parallel()
	t.Run("default branch", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		ref, err := c.ResolveRef(t.Context(), id, "")
		require.NoError(t, err)
		assert.Equal(t, Ref{Name: "main", Kind: RefBranch, Commit: up.Git(t, up.Path("o", "n"), "rev-parse", "main")}, ref)
	})
	t.Run("branch before tag", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		first := up.Repo(t, "o", "n")
		second := up.Commit(t, "o", "n", "refs/heads/v1")
		up.Tag(t, "o", "n", "v1", first)
		id := testID(t, "o", "n")
		_, err := c.Add(t.Context(), id, "https://example.test/o/n.git")
		require.NoError(t, err)
		ref, err := c.ResolveRef(t.Context(), id, "v1")
		require.NoError(t, err)
		assert.Equal(t, Ref{Name: "v1", Kind: RefBranch, Commit: second}, ref)
	})
	t.Run("tag", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		commit := up.Repo(t, "o", "n")
		up.Tag(t, "o", "n", "v2.1.0", commit)
		id := testID(t, "o", "n")
		_, err := c.Add(t.Context(), id, "https://example.test/o/n.git")
		require.NoError(t, err)
		ref, err := c.ResolveRef(t.Context(), id, "v2.1.0")
		require.NoError(t, err)
		assert.Equal(t, Ref{Name: "v2.1.0", Kind: RefTag, Commit: commit}, ref)
	})
	t.Run("abbreviated commit in the cache", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		commit := up.Git(t, c.Path(id), "rev-parse", "main")
		ref, err := c.ResolveRef(t.Context(), id, commit[:7])
		require.NoError(t, err)
		assert.Equal(t, Ref{Name: commit[:7], Kind: RefCommit, Commit: commit}, ref)
	})
	t.Run("full hash fetched from upstream", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		pr := up.Commit(t, "o", "n", "refs/pull/1/head")
		ref, err := c.ResolveRef(t.Context(), id, pr)
		require.NoError(t, err)
		assert.Equal(t, Ref{Name: pr, Kind: RefCommit, Commit: pr}, ref)
		assert.Equal(t, pr, up.Git(t, c.Path(id), "rev-parse", "refs/ws/commits/"+pr))
	})
	t.Run("not cached", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		_, err := c.ResolveRef(t.Context(), testID(t, "o", "n"), "main")
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("missing ref", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		_, err := c.ResolveRef(t.Context(), id, "nope")
		assert.ErrorIs(t, err, ErrRefNotFound)
	})
	t.Run("default branch failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		_, err := c.ResolveRef(t.Context(), id, "")
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("git failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		_, err := c.ResolveRef(t.Context(), id, "main")
		assert.ErrorIs(t, err, errTest)
	})
}

func TestCache_fetchedCommit(t *testing.T) {
	t.Parallel()
	full := strings.Repeat("a", fullHashLen)
	t.Run("not a hash", func(t *testing.T) {
		t.Parallel()
		_, err := newTestCache(t, failingGit()).fetchedCommit(t.Context(), testID(t, "o", "n"), "feat/x")
		require.ErrorIs(t, err, ErrRefNotFound)
		assert.ErrorContains(t, err, "no branch, tag, or commit")
	})
	t.Run("abbreviated hash asks for the full one", func(t *testing.T) {
		t.Parallel()
		_, err := newTestCache(t, failingGit()).fetchedCommit(t.Context(), testID(t, "o", "n"), "abc1234")
		require.ErrorIs(t, err, ErrRefNotFound)
		assert.ErrorContains(t, err, "full 40-character hash")
	})
	t.Run("upstream doesn't have it", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		_, err := c.fetchedCommit(t.Context(), id, full)
		assert.ErrorIs(t, err, ErrRefNotFound)
	})
	t.Run("unreachable upstream is a fetch failure", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			return nil, gitFailure("fatal: unable to access 'https://example.test/o/n.git/': Could not resolve host: example.test")
		}}
		_, err := newTestCache(t, g).fetchedCommit(t.Context(), testID(t, "o", "n"), full)
		require.ErrorIs(t, err, ErrFetchFailed)
		assert.NotErrorIs(t, err, ErrRefNotFound)
	})
	t.Run("fetched but not resolvable", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "rev-parse" {
				return nil, &git.CommandError{Args: args, ExitCode: 1, Err: errTest}
			}
			return nil, nil
		}}
		c := newTestCache(t, g)
		_, err := c.fetchedCommit(t.Context(), testID(t, "o", "n"), full)
		require.ErrorIs(t, err, ErrRefNotFound)
		assert.ErrorContains(t, err, "wasn't fetched")
	})
	t.Run("rev-parse failure", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "rev-parse" {
				return nil, errTest
			}
			return nil, nil
		}}
		_, err := newTestCache(t, g).fetchedCommit(t.Context(), testID(t, "o", "n"), full)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestFetchCommitError(t *testing.T) {
	id := repo.ID{Host: "example.test", Owner: "o", Name: "n"}
	t.Run("upstream doesn't have the commit", func(t *testing.T) {
		err := fetchCommitError(id, "abc", gitFailure("fatal: remote error: upload-pack: not our ref abc"))
		require.ErrorIs(t, err, ErrRefNotFound)
		assert.ErrorContains(t, err, "has no commit abc upstream")
	})
	t.Run("other git failures are fetch failures", func(t *testing.T) {
		err := fetchCommitError(id, "abc", gitFailure("fatal: unable to access"))
		require.ErrorIs(t, err, ErrFetchFailed)
		assert.NotErrorIs(t, err, ErrRefNotFound)
	})
	t.Run("non-git errors are unchanged", func(t *testing.T) {
		assert.Equal(t, errTest, fetchCommitError(id, "abc", errTest))
	})
}

func TestCache_fetchCommit(t *testing.T) {
	t.Parallel()
	t.Run("fetches into refs/ws/commits", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }}
		c := newTestCache(t, g)
		require.NoError(t, c.fetchCommit(t.Context(), testID(t, "o", "n"), "abc"))
		require.Len(t, g.RunCalls(), 1)
		assert.Equal(t, []string{"fetch", "-q", "origin", "abc:refs/ws/commits/abc"}, g.RunCalls()[0].Args)
	})
	t.Run("lock failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		require.NoError(t, os.MkdirAll(c.lockPath(id), 0o750))
		assert.ErrorContains(t, c.fetchCommit(t.Context(), id, "abc"), "lock")
	})
}

func TestCache_revParse(t *testing.T) {
	answer := func(out string, err error) *gitRunnerMock {
		return &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return []byte(out), err }}
	}
	t.Run("found", func(t *testing.T) {
		g := answer("abc\n", nil)
		commit, ok, err := newTestCache(t, g).revParse(t.Context(), testID(t, "o", "n"), "main")
		require.NoError(t, err)
		assert.True(t, ok)
		assert.Equal(t, "abc", commit)
		assert.Equal(t, []string{"rev-parse", "--verify", "-q", "main^{commit}"}, g.RunCalls()[0].Args)
	})
	t.Run("missing", func(t *testing.T) {
		_, ok, err := newTestCache(t, answer("", &git.CommandError{ExitCode: 1, Err: errTest})).revParse(t.Context(), testID(t, "o", "n"), "x")
		require.NoError(t, err)
		assert.False(t, ok)
	})
	t.Run("git failure", func(t *testing.T) {
		_, _, err := newTestCache(t, answer("", &git.CommandError{ExitCode: 128, Err: errTest})).revParse(t.Context(), testID(t, "o", "n"), "x")
		assert.ErrorIs(t, err, errTest)
	})
}

func TestIsHex(t *testing.T) {
	t.Run("hex", func(t *testing.T) {
		assert.True(t, isHex("0123456789abcdef"))
	})
	t.Run("empty", func(t *testing.T) {
		assert.False(t, isHex(""))
	})
	t.Run("uppercase", func(t *testing.T) {
		assert.False(t, isHex("ABC"))
	})
	t.Run("other letters", func(t *testing.T) {
		assert.False(t, isHex("feat"))
	})
}
