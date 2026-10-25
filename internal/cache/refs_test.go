package cache

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gittest"
)

func TestCache_Refs(t *testing.T) {
	t.Parallel()
	t.Run("lists branches and tags with their commits", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		up.Repo(t, "o", "n")
		feature := up.Commit(t, "o", "n", "refs/heads/feat/x")
		up.Tag(t, "o", "n", "v1", feature)
		up.Git(t, up.Path("o", "n"), "tag", "-a", "-m", "release two", "v2", feature)
		id := testID(t, "o", "n")
		_, err := c.Add(t.Context(), id, gittest.URL("o", "n"))
		require.NoError(t, err)
		listing, err := c.Refs(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, "main", listing.DefaultBranch)
		names := make([]string, 0, len(listing.Refs))
		for _, r := range listing.Refs {
			names = append(names, string(r.Kind)+":"+r.Name)
		}
		assert.Equal(t, []string{"branch:main", "branch:feat/x", "tag:v1", "tag:v2"}, names)
		annotated := listing.Refs[3]
		assert.Equal(t, feature, annotated.Commit)
		assert.Equal(t, "commit on refs/heads/feat/x", annotated.Subject)
		assert.Equal(t, "ws test", annotated.Author)
		assert.WithinDuration(t, time.Now(), annotated.Date, time.Minute)
	})
	t.Run("uncached repo", func(t *testing.T) {
		t.Parallel()
		_, err := newTestCache(t, nil).Refs(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("git failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.Refs(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("for-each-ref failure", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "for-each-ref" {
				return nil, errTest
			}
			return []byte("main\n"), nil
		}}
		c := newTestCache(t, g)
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.Refs(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}

func TestParseRefs(t *testing.T) {
	t.Parallel()
	t.Run("reads a branch", func(t *testing.T) {
		t.Parallel()
		refs := parseRefs("refs/heads/main\x00abc\x00Fix it\x00ana\x002026-10-24T09:12:00+02:00\x00\x00\x00\x00\n")
		want := RefInfo{Name: "main", Kind: RefBranch, Commit: "abc", Subject: "Fix it", Author: "ana", Date: time.Date(2026, 10, 24, 7, 12, 0, 0, time.UTC)}
		require.Len(t, refs, 1)
		assert.Equal(t, want.Date.Unix(), refs[0].Date.Unix())
		refs[0].Date = want.Date
		assert.Equal(t, want, refs[0])
	})
	t.Run("peels an annotated tag", func(t *testing.T) {
		t.Parallel()
		refs := parseRefs("refs/tags/v1\x00tagobj\x00Release\x00\x00\x00abc\x00Fix it\x00ana\x002026-10-24T09:12:00Z\n")
		require.Len(t, refs, 1)
		assert.Equal(t, "abc", refs[0].Commit)
		assert.Equal(t, "Fix it", refs[0].Subject)
	})
	t.Run("leaves a missing date zero", func(t *testing.T) {
		t.Parallel()
		refs := parseRefs("refs/tags/v1\x00abc\x00\x00\x00\x00\x00\x00\x00\n")
		require.Len(t, refs, 1)
		assert.True(t, refs[0].Date.IsZero())
	})
	t.Run("skips other refs and malformed lines", func(t *testing.T) {
		t.Parallel()
		refs := parseRefs("refs/remotes/x\x00a\x00b\x00c\x00d\x00\x00\x00\x00\ngarbage\n")
		assert.Empty(t, refs)
	})
	t.Run("never nil", func(t *testing.T) {
		t.Parallel()
		assert.NotNil(t, parseRefs(""))
	})
}

func TestSortRefs(t *testing.T) {
	t.Parallel()
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	refs := []RefInfo{
		{Name: "old-tag", Kind: RefTag, Date: day(1)},
		{Name: "b", Kind: RefBranch, Date: day(3)},
		{Name: "main", Kind: RefBranch, Date: day(1)},
		{Name: "new-tag", Kind: RefTag, Date: day(5)},
		{Name: "a", Kind: RefBranch, Date: day(3)},
		{Name: "newest", Kind: RefBranch, Date: day(4)},
	}
	sortRefs(refs, "main")
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		names = append(names, r.Name)
	}
	assert.Equal(t, []string{"main", "newest", "a", "b", "new-tag", "old-tag"}, names)
}

func TestCache_refCounts(t *testing.T) {
	t.Parallel()
	t.Run("counts branches and tags", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("refs/heads/main\nrefs/heads/feat/x\nrefs/tags/v1\n"), nil
		}}
		branches, tags, err := newTestCache(t, g).refCounts(t.Context(), testID(t, "o", "n"))
		require.NoError(t, err)
		assert.Equal(t, [2]int{2, 1}, [2]int{branches, tags})
	})
	t.Run("git failure", func(t *testing.T) {
		t.Parallel()
		_, _, err := newTestCache(t, failingGit()).refCounts(t.Context(), testID(t, "o", "n"))
		assert.ErrorIs(t, err, errTest)
	})
}
