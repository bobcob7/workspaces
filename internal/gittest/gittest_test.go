package gittest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewUpstream(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	assert.DirExists(t, up.Dir)
	t.Run("git reaches it through the fake host", func(t *testing.T) {
		t.Parallel()
		up.Repo(t, "o", "n")
		out := up.Git(t, up.Dir, "ls-remote", URL("o", "n"), "refs/heads/main")
		assert.Contains(t, out, "refs/heads/main")
	})
	t.Run("ignores the user's git config", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, up.Environ, "GIT_CONFIG_NOSYSTEM=1")
	})
}

func TestURL(t *testing.T) {
	assert.Equal(t, "https://example.test/o/n.git", URL("o", "n"))
}

func TestUpstream_Path(t *testing.T) {
	up := &Upstream{Dir: "/up"}
	assert.Equal(t, "/up/o/n.git", up.Path("o", "n"))
}

func TestUpstream_Repo(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	commit := up.Repo(t, "o", "n")
	assert.Equal(t, "true", up.Git(t, up.Path("o", "n"), "rev-parse", "--is-bare-repository"))
	assert.Equal(t, commit, up.Git(t, up.Path("o", "n"), "rev-parse", "HEAD"))
	up.Git(t, up.Path("o", "n"), "fsck", "--full")
}

func TestUpstream_Commit(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	first := up.Repo(t, "o", "n")
	t.Run("on an existing branch", func(t *testing.T) {
		second := up.Commit(t, "o", "n", "refs/heads/main")
		assert.Equal(t, first, up.Git(t, up.Path("o", "n"), "rev-parse", second+"^"))
	})
	t.Run("on a new ref", func(t *testing.T) {
		commit := up.Commit(t, "o", "n", "refs/pull/1/head")
		assert.Equal(t, commit, up.Git(t, up.Path("o", "n"), "rev-parse", "refs/pull/1/head"))
	})
}

func TestUpstream_Tag(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	commit := up.Repo(t, "o", "n")
	up.Tag(t, "o", "n", "v1", commit)
	assert.Equal(t, commit, up.Git(t, up.Path("o", "n"), "rev-parse", "v1"))
}

func TestUpstream_Git(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	assert.Equal(t, "ws test", up.Git(t, up.Dir, "config", "user.name"))
}

func TestUpstream_rev(t *testing.T) {
	t.Parallel()
	up := NewUpstream(t)
	commit := up.Repo(t, "o", "n")
	t.Run("existing ref", func(t *testing.T) {
		got, ok := up.rev(t, up.Path("o", "n"), "refs/heads/main")
		require.True(t, ok)
		assert.Equal(t, commit, got)
	})
	t.Run("missing ref", func(t *testing.T) {
		_, ok := up.rev(t, up.Path("o", "n"), "refs/heads/nope")
		assert.False(t, ok)
	})
	t.Run("missing repo", func(t *testing.T) {
		_, ok := up.rev(t, filepath.Join(up.Dir, "missing"), "HEAD")
		assert.False(t, ok)
		_, err := os.Stat(filepath.Join(up.Dir, "missing"))
		assert.True(t, os.IsNotExist(err))
	})
}
