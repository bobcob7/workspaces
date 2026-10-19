package cache

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

func spec(t *testing.T, s string) *repo.Spec {
	t.Helper()
	sp, err := repo.ParseSpec(s)
	require.NoError(t, err)
	return &sp
}

// upstreamGit answers `git config --get remote.origin.url` with url.
func upstreamGit(url string) *gitRunnerMock {
	return &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return []byte(url + "\n"), nil }}
}

func TestCache_Resolve(t *testing.T) {
	t.Parallel()
	t.Run("probes an uncached name", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		up.Repo(t, "o", "n")
		r, err := c.Resolve(t.Context(), spec(t, "n"))
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: testID(t, "o", "n"), Upstream: gittest.URL("o", "n"), Source: SourceProbe}, r)
	})
	t.Run("cached name, with its stored upstream", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		r, err := c.Resolve(t.Context(), spec(t, "n"))
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: id, Upstream: gittest.URL("o", "n"), Cached: true, Source: SourceCache}, r)
	})
}

func TestCache_Lookup(t *testing.T) {
	t.Parallel()
	t.Run("cached", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, upstreamGit("https://example.test/o/n.git"))
		fakeEntry(t, c, testID(t, "o", "n"))
		r, err := c.Lookup(t.Context(), spec(t, "o/n"))
		require.NoError(t, err)
		assert.True(t, r.Cached)
	})
	t.Run("uncached qualified spec", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		_, err := c.Lookup(t.Context(), spec(t, "example.test/o/n"))
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("never probes", func(t *testing.T) {
		t.Parallel()
		g := failingGit()
		c := newTestCache(t, g)
		_, err := c.Lookup(t.Context(), spec(t, "n"))
		require.ErrorIs(t, err, ErrRepoNotFound)
		assert.Empty(t, g.RunCalls())
	})
	t.Run("resolution failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		fakeEntry(t, c, repo.ID{Host: "a.com", Owner: "x", Name: "n"})
		fakeEntry(t, c, repo.ID{Host: "a.com", Owner: "y", Name: "n"})
		_, err := c.Lookup(t.Context(), spec(t, "n"))
		var ambiguous *AmbiguousError
		assert.ErrorAs(t, err, &ambiguous)
	})
}

func TestCache_resolve(t *testing.T) {
	t.Parallel()
	t.Run("qualified spec", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		r, err := c.resolve(t.Context(), spec(t, "github.com/O/N"), false)
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: repo.ID{Host: "github.com", Owner: "o", Name: "n"}, Upstream: "https://github.com/o/n.git", Source: SourceSpec}, r)
	})
	t.Run("URL spec keeps its URL", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		r, err := c.resolve(t.Context(), spec(t, "git@github.com:O/N.git"), false)
		require.NoError(t, err)
		assert.Equal(t, "git@github.com:O/N.git", r.Upstream)
	})
	t.Run("owner/name", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		r, err := c.resolve(t.Context(), spec(t, "o/n"), false)
		require.NoError(t, err)
		assert.Equal(t, SourceDefault, r.Source)
	})
	t.Run("name", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, upstreamGit("u"))
		fakeEntry(t, c, testID(t, "o", "n"))
		r, err := c.resolve(t.Context(), spec(t, "n"), false)
		require.NoError(t, err)
		assert.Equal(t, SourceCache, r.Source)
	})
	t.Run("cache failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, nil)
		unreadable(t, filepath.Join(c.reposDir(), "github.com"))
		_, err := c.resolve(t.Context(), spec(t, "n"), false)
		assert.Error(t, err)
	})
	t.Run("upstream failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.resolve(t.Context(), spec(t, "n"), false)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestCache_resolveOwnerName(t *testing.T) {
	c := newTestCache(t, nil)
	c.defaults = config.Defaults{Host: "github.com"}
	onGitHub := repo.ID{Host: "github.com", Owner: "o", Name: "n"}
	onGitLab := repo.ID{Host: "gitlab.com", Owner: "o", Name: "n"}
	onCodeberg := repo.ID{Host: "codeberg.org", Owner: "o", Name: "n"}
	t.Run("one match on any host", func(t *testing.T) {
		r, err := c.resolveOwnerName([]repo.ID{onGitLab}, spec(t, "o/n"))
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: onGitLab, Source: SourceCache}, r)
	})
	t.Run("prefers defaults.host", func(t *testing.T) {
		r, err := c.resolveOwnerName([]repo.ID{onGitLab, onGitHub}, spec(t, "o/n"))
		require.NoError(t, err)
		assert.Equal(t, onGitHub, r.ID)
	})
	t.Run("ambiguous without defaults.host", func(t *testing.T) {
		_, err := c.resolveOwnerName([]repo.ID{onGitLab, onCodeberg}, spec(t, "o/n"))
		var ambiguous *AmbiguousError
		require.ErrorAs(t, err, &ambiguous)
		assert.Equal(t, []repo.ID{onGitLab, onCodeberg}, ambiguous.Candidates)
		assert.Equal(t, "o/n", ambiguous.Spec)
	})
	t.Run("uncached is on defaults.host", func(t *testing.T) {
		r, err := c.resolveOwnerName(nil, spec(t, "o/n"))
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: onGitHub, Upstream: "https://github.com/o/n.git", Source: SourceDefault}, r)
	})
	t.Run("ignores other names", func(t *testing.T) {
		r, err := c.resolveOwnerName([]repo.ID{{Host: "github.com", Owner: "o", Name: "other"}}, spec(t, "o/n"))
		require.NoError(t, err)
		assert.Equal(t, SourceDefault, r.Source)
	})
	t.Run("bad defaults.host", func(t *testing.T) {
		bad := newTestCache(t, nil)
		bad.defaults = config.Defaults{Host: "bad host"}
		_, err := bad.resolveOwnerName(nil, spec(t, "o/n"))
		assert.ErrorIs(t, err, repo.ErrInvalidSpec)
	})
}

func TestCache_resolveName(t *testing.T) {
	a := repo.ID{Host: "github.com", Owner: "a", Name: "n"}
	b := repo.ID{Host: "github.com", Owner: "b", Name: "n"}
	x := repo.ID{Host: "github.com", Owner: "x", Name: "n"}
	newCache := func(t *testing.T, g gitRunner, owners ...string) *Cache {
		t.Helper()
		c := newTestCache(t, g)
		c.defaults = config.Defaults{Host: "github.com", Owners: owners}
		return c
	}
	t.Run("one match", func(t *testing.T) {
		r, err := newCache(t, nil).resolveName(t.Context(), []repo.ID{a}, spec(t, "n"), false)
		require.NoError(t, err)
		assert.Equal(t, Resolution{ID: a, Source: SourceCache}, r)
	})
	t.Run("prefers defaults.owners in order", func(t *testing.T) {
		r, err := newCache(t, nil, "b", "a").resolveName(t.Context(), []repo.ID{a, b, x}, spec(t, "n"), false)
		require.NoError(t, err)
		assert.Equal(t, b, r.ID)
	})
	t.Run("skips owners with no match", func(t *testing.T) {
		r, err := newCache(t, nil, "zzz", "a").resolveName(t.Context(), []repo.ID{a, x}, spec(t, "n"), false)
		require.NoError(t, err)
		assert.Equal(t, a, r.ID)
	})
	t.Run("ambiguous", func(t *testing.T) {
		_, err := newCache(t, nil, "zzz").resolveName(t.Context(), []repo.ID{a, x}, spec(t, "n"), false)
		var ambiguous *AmbiguousError
		require.ErrorAs(t, err, &ambiguous)
		assert.Equal(t, []repo.ID{a, x}, ambiguous.Candidates)
	})
	t.Run("uncached without probing", func(t *testing.T) {
		_, err := newCache(t, nil, "a").resolveName(t.Context(), nil, spec(t, "n"), false)
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("uncached probes", func(t *testing.T) {
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }}
		r, err := newCache(t, g, "a").resolveName(t.Context(), nil, spec(t, "n"), true)
		require.NoError(t, err)
		assert.Equal(t, SourceProbe, r.Source)
	})
}

func TestCache_probe(t *testing.T) {
	t.Parallel()
	t.Run("first owner that has it wins", func(t *testing.T) {
		t.Parallel()
		c, up := newGitCache(t)
		up.Repo(t, "second", "n")
		up.Repo(t, "third", "n")
		c.defaults.Owners = []string{"first", "second", "third"}
		r, err := c.probe(t.Context(), "n")
		require.NoError(t, err)
		assert.Equal(t, testID(t, "second", "n"), r.ID)
	})
	t.Run("nobody has it", func(t *testing.T) {
		t.Parallel()
		c, _ := newGitCache(t)
		_, err := c.probe(t.Context(), "n")
		require.ErrorIs(t, err, ErrRepoNotFound)
		assert.ErrorContains(t, err, gittest.Host)
	})
	t.Run("no owners configured", func(t *testing.T) {
		t.Parallel()
		c, _ := newGitCache(t)
		c.defaults.Owners = nil
		_, err := c.probe(t.Context(), "n")
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("a missing repo moves on to the next owner", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if strings.Contains(args[3], "/first/") {
				return nil, gitFailure("remote: Repository not found.")
			}
			return nil, nil
		}}
		c := newTestCache(t, g)
		c.defaults.Owners = []string{"first", "second"}
		r, err := c.probe(t.Context(), "n")
		require.NoError(t, err)
		assert.Equal(t, testID(t, "second", "n"), r.ID)
	})
	t.Run("an unreachable upstream stops the probe as fetch failed", func(t *testing.T) {
		t.Parallel()
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			return nil, gitFailure("fatal: unable to access 'https://example.test/first/n.git/': Could not resolve host: example.test")
		}}
		c := newTestCache(t, g)
		c.defaults.Owners = []string{"first", "second"}
		_, err := c.probe(t.Context(), "n")
		require.ErrorIs(t, err, ErrFetchFailed)
		require.NotErrorIs(t, err, ErrRepoNotFound)
		assert.Len(t, g.RunCalls(), 1)
	})
	t.Run("a cancelled probe stops", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		g := &gitRunnerMock{RunFunc: func(context.Context, string, ...string) ([]byte, error) {
			cancel()
			return nil, context.Canceled
		}}
		_, err := newTestCache(t, g).probe(ctx, "n")
		require.ErrorIs(t, err, context.Canceled)
		assert.NotErrorIs(t, err, ErrFetchFailed)
	})
	t.Run("missing git stops the probe", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, git.NewRunner(discardLogger(), filepath.Join(t.TempDir(), "no-git"), nil))
		_, err := c.probe(t.Context(), "n")
		assert.ErrorIs(t, err, git.ErrNotInstalled)
	})
	t.Run("bad owner", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		c.defaults.Owners = []string{"bad owner"}
		_, err := c.probe(t.Context(), "n")
		assert.ErrorIs(t, err, repo.ErrInvalidSpec)
	})
	t.Run("uncreatable cache directory", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		c.root += "\x00"
		_, err := c.probe(t.Context(), "n")
		assert.Error(t, err)
	})
}

func TestCache_withUpstream(t *testing.T) {
	t.Parallel()
	t.Run("uncached keeps the spec's upstream", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		r := Resolution{ID: testID(t, "o", "n"), Upstream: "https://x/o/n.git"}
		require.NoError(t, c.withUpstream(t.Context(), &r, true))
		assert.False(t, r.Cached)
		assert.Equal(t, "https://x/o/n.git", r.Upstream)
	})
	t.Run("cached uses the stored upstream", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, upstreamGit("git@example.test:o/n.git"))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		r := Resolution{ID: id}
		require.NoError(t, c.withUpstream(t.Context(), &r, false))
		assert.True(t, r.Cached)
		assert.Equal(t, "git@example.test:o/n.git", r.Upstream)
		assert.Empty(t, c.Warnings())
	})
	t.Run("URL spec that disagrees is a warning", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, upstreamGit("git@example.test:o/n.git"))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		r := Resolution{ID: id, Upstream: "https://example.test/o/n.git"}
		require.NoError(t, c.withUpstream(t.Context(), &r, true))
		assert.Equal(t, "git@example.test:o/n.git", r.Upstream)
		require.Len(t, c.Warnings(), 1)
		assert.Contains(t, c.Warnings()[0], "https://example.test/o/n.git")
	})
	t.Run("URL spec that agrees", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, upstreamGit("https://example.test/o/n.git"))
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		r := Resolution{ID: id, Upstream: "https://example.test/o/n.git"}
		require.NoError(t, c.withUpstream(t.Context(), &r, true))
		assert.Empty(t, c.Warnings())
	})
	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		c := newTestCache(t, failingGit())
		id := testID(t, "o", "n")
		fakeEntry(t, c, id)
		r := Resolution{ID: id}
		assert.ErrorIs(t, c.withUpstream(t.Context(), &r, false), errTest)
	})
}

func TestFilterIDs(t *testing.T) {
	a := repo.ID{Host: "h.com", Owner: "a", Name: "n"}
	b := repo.ID{Host: "h.com", Owner: "b", Name: "n"}
	t.Run("keeps matches in order", func(t *testing.T) {
		assert.Equal(t, []repo.ID{b}, filterIDs([]repo.ID{a, b}, func(id repo.ID) bool { return id.Owner == "b" }))
	})
	t.Run("nothing matches", func(t *testing.T) {
		assert.Empty(t, filterIDs([]repo.ID{a, b}, func(repo.ID) bool { return false }))
	})
}

func TestPreferred(t *testing.T) {
	a := repo.ID{Host: "h.com", Owner: "a", Name: "n"}
	b := repo.ID{Host: "h.com", Owner: "b", Name: "n"}
	t.Run("the preferred ones", func(t *testing.T) {
		assert.Equal(t, []repo.ID{a}, preferred([]repo.ID{a, b}, func(id repo.ID) bool { return id.Owner == "a" }))
	})
	t.Run("all when none are preferred", func(t *testing.T) {
		assert.Equal(t, []repo.ID{a, b}, preferred([]repo.ID{a, b}, func(repo.ID) bool { return false }))
	})
}
