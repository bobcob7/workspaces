package lifetime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestManager_removeOrphans(t *testing.T) {
	t.Run("removes only sessions in exactly the workspace's directory", func(t *testing.T) {
		f := newFixture(t)
		f.listed("in000001", "/cfg/workspaces/x")
		f.listed("in000002", "/cfg/workspaces/x/")
		f.listed("below001", "/cfg/workspaces/x/repo")
		f.listed("sibling1", "/cfg/workspaces/x-2")
		f.listed("parent01", "/cfg/workspaces")
		f.listed("relative", "workspaces/x")
		f.listed("nocwd001", "")
		require.NoError(t, f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions))
		assert.Equal(t, []string{"in000001", "in000002"}, f.claudeRemoved())
	})
	t.Run("removes sessions in a one-repo workspace's repo directory too", func(t *testing.T) {
		f := newFixture(t)
		f.listed("in000001", "/cfg/workspaces/x")
		f.listed("repo0001", "/cfg/workspaces/x/api")
		f.listed("below001", "/cfg/workspaces/x/api/sub")
		f.listed("other001", "/cfg/workspaces/x/web")
		ws := workspace.Workspace{ID: "x", Repos: []workspace.Repo{{ID: "example.test/o/api", Path: "api"}}}
		require.NoError(t, f.m.removeOrphans(t.Context(), &ws, f.sessions))
		assert.Equal(t, []string{"in000001", "repo0001"}, f.claudeRemoved())
	})
	t.Run("leaves repo directories of a workspace with several repos", func(t *testing.T) {
		f := newFixture(t)
		f.listed("repo0001", "/cfg/workspaces/x/api")
		ws := workspace.Workspace{ID: "x", Repos: []workspace.Repo{{ID: "example.test/o/api", Path: "api"}, {ID: "example.test/o/web", Path: "web"}}}
		require.NoError(t, f.m.removeOrphans(t.Context(), &ws, f.sessions))
		assert.Empty(t, f.claudeRemoved())
	})
	t.Run("matches the guarded directory, not the recorded path", func(t *testing.T) {
		f := newFixture(t)
		f.store.GuardedDirFunc = func(string) (string, error) { return "/private/cfg/workspaces/x", nil }
		f.listed("in000001", "/private/cfg/workspaces/x")
		f.listed("recorded", "/cfg/workspaces/x")
		require.NoError(t, f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions))
		assert.Equal(t, []string{"in000001"}, f.claudeRemoved())
	})
	t.Run("resolves symlinks in a session's directory", func(t *testing.T) {
		root, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		dir := filepath.Join(root, "workspaces", "x")
		require.NoError(t, os.MkdirAll(dir, 0o750))
		link := filepath.Join(root, "link")
		require.NoError(t, os.Symlink(filepath.Join(root, "workspaces"), link))
		f := newFixture(t)
		f.store.GuardedDirFunc = func(string) (string, error) { return dir, nil }
		f.listed("in000001", filepath.Join(link, "x"))
		f.listed("below001", filepath.Join(link, "x", "repo"))
		require.NoError(t, f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions))
		assert.Equal(t, []string{"in000001"}, f.claudeRemoved())
	})
	t.Run("nothing is removed when the guard refuses the workspace", func(t *testing.T) {
		f := newFixture(t)
		f.store.GuardedDirFunc = func(string) (string, error) { return "", workspace.ErrUnsafe }
		f.listed("in000001", "/cfg/workspaces/x")
		require.ErrorIs(t, f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions), workspace.ErrUnsafe)
		assert.Empty(t, f.claudeRemoved())
	})
	t.Run("a session already gone counts as removed", func(t *testing.T) {
		f := newFixture(t)
		f.listed("in000001", "/cfg/workspaces/x")
		f.claude.RemoveFunc = func(context.Context, string) error { return claude.ErrNoSession }
		assert.NoError(t, f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions))
	})
	t.Run("stops at a failed removal", func(t *testing.T) {
		f := newFixture(t)
		f.listed("in000001", "/cfg/workspaces/x")
		f.listed("in000002", "/cfg/workspaces/x")
		f.claude.RemoveFunc = func(context.Context, string) error { return errTest }
		err := f.m.removeOrphans(t.Context(), &workspace.Workspace{ID: "x"}, f.sessions)
		require.ErrorIs(t, err, errTest)
		assert.Len(t, f.claude.RemoveCalls(), 1)
	})
}

func TestResolved(t *testing.T) {
	t.Parallel()
	t.Run("resolves symlinks", func(t *testing.T) {
		t.Parallel()
		root, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		require.NoError(t, os.Mkdir(filepath.Join(root, "real"), 0o750))
		require.NoError(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")))
		assert.Equal(t, filepath.Join(root, "real"), resolved(filepath.Join(root, "link")))
	})
	t.Run("a missing directory is cleaned", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "/no/such/dir", resolved("/no/such/dir/"))
	})
}
