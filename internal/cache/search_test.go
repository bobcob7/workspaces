package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
)

func TestCache_Search(t *testing.T) {
	t.Parallel()
	ids := map[string]repo.ID{
		"exact":     {Host: "github.com", Owner: "z", Name: "bear"},
		"prefix":    {Host: "github.com", Owner: "z", Name: "bear-tools"},
		"substring": {Host: "github.com", Owner: "bobcob7", Name: "buff-bear"},
		"owner":     {Host: "github.com", Owner: "bears", Name: "honey"},
		"none":      {Host: "github.com", Owner: "o", Name: "cat"},
	}
	newCache := func(t *testing.T, g gitRunner) *Cache {
		t.Helper()
		c := newTestCache(t, g)
		for _, id := range ids {
			fakeEntry(t, c, id)
		}
		return c
	}
	t.Run("ranks matches", func(t *testing.T) {
		t.Parallel()
		matches, err := newCache(t, nil).Search(t.Context(), "Bear", 20, false)
		require.NoError(t, err)
		assert.Equal(t, []Match{{ID: ids["exact"]}, {ID: ids["prefix"]}, {ID: ids["substring"]}, {ID: ids["owner"]}}, matches)
	})
	t.Run("limits results", func(t *testing.T) {
		t.Parallel()
		matches, err := newCache(t, nil).Search(t.Context(), "bear", 2, false)
		require.NoError(t, err)
		assert.Equal(t, []Match{{ID: ids["exact"]}, {ID: ids["prefix"]}}, matches)
	})
	t.Run("refs follow repos", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, dir string, _ ...string) ([]byte, error) {
			if strings.HasSuffix(dir, filepath.FromSlash("o/cat.git")) {
				return []byte("main\nfeat/bear-hug\n"), nil
			}
			return []byte("main\n"), nil
		}}
		matches, err := newCache(t, g).Search(t.Context(), "bear", 20, true)
		require.NoError(t, err)
		require.Len(t, matches, 5)
		assert.Equal(t, Match{ID: ids["none"], Ref: "feat/bear-hug"}, matches[4])
	})
	t.Run("ref listing failure", func(t *testing.T) {
		t.Parallel()
		_, err := newCache(t, failingGit()).Search(t.Context(), "bear", 20, true)
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("cache failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		unreadable(t, filepath.Join(c.reposDir(), "github.com"))
		_, err := c.Search(t.Context(), "bear", 20, false)
		assert.Error(t, err)
	})
}

func TestRank(t *testing.T) {
	id := repo.ID{Host: "gitlab.example.com", Owner: "team", Name: "buff-bear"}
	t.Run("exact name", func(t *testing.T) {
		r, ok := rank(id, "buff-bear")
		require.True(t, ok)
		assert.Equal(t, 0, r)
	})
	t.Run("name prefix", func(t *testing.T) {
		r, ok := rank(id, "buff")
		require.True(t, ok)
		assert.Equal(t, 1, r)
	})
	t.Run("name substring", func(t *testing.T) {
		r, ok := rank(id, "bear")
		require.True(t, ok)
		assert.Equal(t, 2, r)
	})
	t.Run("owner", func(t *testing.T) {
		r, ok := rank(id, "tea")
		require.True(t, ok)
		assert.Equal(t, 3, r)
	})
	t.Run("host", func(t *testing.T) {
		r, ok := rank(id, "example")
		require.True(t, ok)
		assert.Equal(t, 3, r)
	})
	t.Run("no match", func(t *testing.T) {
		_, ok := rank(id, "cat")
		assert.False(t, ok)
	})
}

func TestCache_refNames(t *testing.T) {
	t.Parallel()
	t.Run("branches and tags", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		commit := up.Repo(t, "o", "n")
		up.Commit(t, "o", "n", "refs/heads/feat/x")
		up.Tag(t, "o", "n", "v1", commit)
		up.Git(t, up.Path("o", "n"), "update-ref", "refs/pull/1/head", commit)
		id := testID(t, "o", "n")
		_, err := c.Add(t.Context(), id, "https://example.test/o/n.git")
		require.NoError(t, err)
		names, err := c.refNames(t.Context(), id)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"main", "feat/x", "v1"}, names)
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		_, err := newTestCache(t, failingGit()).refNames(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}
