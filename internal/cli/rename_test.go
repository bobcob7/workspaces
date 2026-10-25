package cli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// renamedID is the workspace newRenameApp's store knows.
const renamedID = "fix-login-d8efb24f"

// newRenameApp is a test App whose workspace store finds renamedID, by its
// id or session id e8cdf5d8, and renames it.
func newRenameApp() *mockedApp {
	id := renamedID
	e := newMockedApp()
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		if arg == id || arg == "e8cdf5d8" {
			return workspace.Workspace{ID: id, Path: "/cfg/workspaces/" + id}, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.store.RenameFunc = func(id, name string) (workspace.Workspace, error) {
		return workspace.Workspace{ID: id, Name: name, Path: "/cfg/workspaces/" + id}, nil
	}
	return e
}

func TestApp_newRenameCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newRenameCmd()
	t.Run("needs a workspace and a name", func(t *testing.T) {
		requireUsageError(t, cmd.Args(cmd, []string{"fix-login-d8efb24f"}))
		requireUsageError(t, cmd.Args(cmd, []string{"a", "b", "c"}))
		require.NoError(t, cmd.Args(cmd, []string{"a", "b"}))
	})
	t.Run("runs the removal pass, then renames", func(t *testing.T) {
		e := newRenameApp()
		cmd := e.app.newRenameCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.RunE(cmd, []string{"fix-login-d8efb24f", "Fix login"}))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		assert.Len(t, e.store.RenameCalls(), 1)
	})
}

func TestApp_runRename(t *testing.T) {
	t.Run("renames the workspace an argument names, by its id", func(t *testing.T) {
		e := newRenameApp()
		require.NoError(t, e.app.runRename("e8cdf5d8", "This is a Test"))
		require.Len(t, e.store.RenameCalls(), 1)
		assert.Equal(t, "fix-login-d8efb24f", e.store.RenameCalls()[0].ID)
		assert.Equal(t, "This is a Test", e.store.RenameCalls()[0].Name)
	})
	t.Run("prints the id and the new name", func(t *testing.T) {
		e := newRenameApp()
		require.NoError(t, e.app.runRename("fix-login-d8efb24f", "This is a Test"))
		assert.Equal(t, "fix-login-d8efb24f  This is a Test\n", e.stdout.String())
	})
	t.Run("--json", func(t *testing.T) {
		e := newRenameApp()
		e.app.global.json = true
		require.NoError(t, e.app.runRename("fix-login-d8efb24f", "This is a Test"))
		assert.JSONEq(t, `{"id":"fix-login-d8efb24f","name":"This is a Test","path":"/cfg/workspaces/fix-login-d8efb24f","warnings":[]}`, e.stdout.String())
	})
	t.Run("an unknown workspace", func(t *testing.T) {
		e := newRenameApp()
		requireCode(t, codeWorkspaceNotFound, e.app.runRename("nope", "x"))
		assert.Empty(t, e.store.RenameCalls())
	})
	t.Run("an invalid name", func(t *testing.T) {
		e := newRenameApp()
		e.store.RenameFunc = func(string, string) (workspace.Workspace, error) {
			return workspace.Workspace{}, workspace.ErrNameInvalid
		}
		requireCode(t, codeNameInvalid, e.app.runRename("fix-login-d8efb24f", " "))
	})
}

func TestApp_rename_endToEnd(t *testing.T) {
	t.Parallel()
	e := newLifetimeE2E(t)
	path := e.start(t, "This is a Test", "e8cdf5d8")
	id := filepath.Base(path)
	assert.Regexp(t, `^this-is-a-test-[0-9a-f]{8}$`, id)
	e.setAgents(t, "["+agentJSON("e8cdf5d8", path)+"]")
	for _, name := range []string{"First", "Second name"} {
		require.Equal(t, exitOK, e.run(t, "rename", id, name), "stderr: %s", e.stderr.String())
	}
	require.Equal(t, exitOK, e.run(t, "--json", "list"), "stderr: %s", e.stderr.String())
	var got listJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
	require.Len(t, got.Workspaces, 1)
	assert.Equal(t, id, *got.Workspaces[0].ID)
	assert.Equal(t, "Second name", *got.Workspaces[0].Name)
	assert.Equal(t, path, got.Workspaces[0].Path)
	assert.DirExists(t, path)
}
