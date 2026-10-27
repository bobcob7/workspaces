package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gittest"
	"github.com/bobcob7/workspaces/internal/repo"
)

// commitFiles commits files, written over the tree of ref's commit, to ref
// in the cached repo, and returns the commit. A missing ref starts a root
// commit.
func commitFiles(t *testing.T, c *Cache, up *gittest.Upstream, id repo.ID, ref string, files map[string]string) string {
	t.Helper()
	gitDir := c.Path(id)
	work := t.TempDir()
	var parents []string
	if parent, ok, err := c.revParse(t.Context(), id, ref); err == nil && ok {
		up.Git(t, gitDir, "--work-tree", work, "read-tree", parent)
		parents = []string{"-p", parent}
	} else {
		up.Git(t, gitDir, "--work-tree", work, "read-tree", "--empty")
	}
	for name, content := range files {
		path := filepath.Join(work, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
		up.Git(t, gitDir, "--work-tree", work, "add", name)
	}
	tree := up.Git(t, gitDir, "--work-tree", work, "write-tree")
	commit := up.Git(t, gitDir, append([]string{"commit-tree", tree, "-m", "commit"}, parents...)...)
	up.Git(t, gitDir, "update-ref", ref, commit)
	return commit
}

func TestCache_ChangedFiles(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*Cache, *gittest.Upstream, repo.ID) {
		t.Helper()
		c, up := newGitCache(t)
		id := addRepo(t, c, up)
		return c, up, id
	}
	t.Run("lists head's changes since it branched from base", func(t *testing.T) {
		t.Parallel()
		c, up, id := setup(t)
		commitFiles(t, c, up, id, "refs/heads/main", map[string]string{"README.md": "a"})
		up.Git(t, c.Path(id), "update-ref", "refs/heads/feat", "refs/heads/main")
		commitFiles(t, c, up, id, "refs/heads/feat", map[string]string{"cmd/main.go": "package main", "go.mod": "module x"})
		head := commitFiles(t, c, up, id, "refs/heads/feat", map[string]string{"docs/a b.md": "x"})
		commitFiles(t, c, up, id, "refs/heads/main", map[string]string{"web/app.ts": "x"})
		files, err := c.ChangedFiles(t.Context(), id, "main", head)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"cmd/main.go", "go.mod", "docs/a b.md"}, files)
	})
	t.Run("lists a commit's changes from its first parent", func(t *testing.T) {
		t.Parallel()
		c, up, id := setup(t)
		commitFiles(t, c, up, id, "refs/heads/main", map[string]string{"a.go": "a"})
		head := commitFiles(t, c, up, id, "refs/heads/main", map[string]string{"b.ts": "b"})
		files, err := c.ChangedFiles(t.Context(), id, "", head)
		require.NoError(t, err)
		assert.Equal(t, []string{"b.ts"}, files)
	})
	t.Run("lists every file of a root commit", func(t *testing.T) {
		t.Parallel()
		c, up, id := setup(t)
		root := commitFiles(t, c, up, id, "refs/heads/orphan", map[string]string{"a.go": "a", "b/c.ts": "c"})
		files, err := c.ChangedFiles(t.Context(), id, "", root)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"a.go", "b/c.ts"}, files)
	})
	t.Run("an empty diff is an empty list", func(t *testing.T) {
		t.Parallel()
		c, up, id := setup(t)
		head := up.Git(t, c.Path(id), "rev-parse", "main")
		files, err := c.ChangedFiles(t.Context(), id, "main", head)
		require.NoError(t, err)
		assert.Empty(t, files)
		assert.NotNil(t, files)
	})
	t.Run("an unknown base fails", func(t *testing.T) {
		t.Parallel()
		c, up, id := setup(t)
		head := up.Git(t, c.Path(id), "rev-parse", "main")
		_, err := c.ChangedFiles(t.Context(), id, "nope", head)
		assert.ErrorContains(t, err, "listing the files")
	})
	t.Run("an uncached repo is ErrRepoNotFound", func(t *testing.T) {
		t.Parallel()
		c, _ := newGitCache(t)
		_, err := c.ChangedFiles(t.Context(), testID(t, "o", "n"), "", "abc")
		assert.ErrorIs(t, err, ErrRepoNotFound)
	})
	t.Run("git failing is an error", func(t *testing.T) {
		c := newTestCache(t, failingGit())
		fakeEntry(t, c, testID(t, "o", "n"))
		_, err := c.ChangedFiles(t.Context(), testID(t, "o", "n"), "", "abc")
		assert.ErrorIs(t, err, errTest)
	})
}
