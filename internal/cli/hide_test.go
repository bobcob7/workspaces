package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// newHideApp is a test App whose workspace store finds renamedID, by its
// id or session id e8cdf5d8, and hides or shows it.
func newHideApp() *mockedApp {
	e := newRenameApp()
	e.store.SetHiddenFunc = func(id string, hidden bool) (workspace.Workspace, error) {
		return workspace.Workspace{ID: id, Name: "Fix login", Path: "/cfg/workspaces/" + id, Hidden: hidden}, nil
	}
	return e
}

func TestApp_newHideCmd(t *testing.T) {
	app, _, _ := newTestApp()
	t.Run("needs exactly one workspace", func(t *testing.T) {
		for _, cmd := range []*cobra.Command{app.newHideCmd(), app.newUnhideCmd()} {
			requireUsageError(t, cmd.Args(cmd, nil))
			requireUsageError(t, cmd.Args(cmd, []string{"a", "b"}))
			require.NoError(t, cmd.Args(cmd, []string{"a"}))
		}
	})
	t.Run("runs the removal pass, then hides", func(t *testing.T) {
		e := newHideApp()
		cmd := e.app.newHideCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.RunE(cmd, []string{renamedID}))
		assert.Len(t, e.lifetime.SweepCalls(), 1)
		require.Len(t, e.store.SetHiddenCalls(), 1)
		assert.True(t, e.store.SetHiddenCalls()[0].Hidden)
	})
	t.Run("unhide shows", func(t *testing.T) {
		e := newHideApp()
		cmd := e.app.newUnhideCmd()
		cmd.SetContext(t.Context())
		require.NoError(t, cmd.RunE(cmd, []string{renamedID}))
		require.Len(t, e.store.SetHiddenCalls(), 1)
		assert.False(t, e.store.SetHiddenCalls()[0].Hidden)
	})
}

func TestApp_runSetHidden(t *testing.T) {
	t.Run("hides the workspace an argument names, by its id", func(t *testing.T) {
		e := newHideApp()
		require.NoError(t, e.app.runSetHidden("e8cdf5d8", true))
		require.Len(t, e.store.SetHiddenCalls(), 1)
		assert.Equal(t, renamedID, e.store.SetHiddenCalls()[0].ID)
	})
	t.Run("prints the id and whether it's hidden", func(t *testing.T) {
		e := newHideApp()
		require.NoError(t, e.app.runSetHidden(renamedID, true))
		require.NoError(t, e.app.runSetHidden(renamedID, false))
		assert.Equal(t, renamedID+"  hidden\n"+renamedID+"  visible\n", e.stdout.String())
	})
	t.Run("--json", func(t *testing.T) {
		e := newHideApp()
		e.app.global.json = true
		require.NoError(t, e.app.runSetHidden(renamedID, true))
		assert.JSONEq(t, `{"id":"fix-login-d8efb24f","name":"Fix login","path":"/cfg/workspaces/fix-login-d8efb24f","hidden":true,"warnings":[]}`, e.stdout.String())
	})
	t.Run("an unknown workspace", func(t *testing.T) {
		e := newHideApp()
		requireCode(t, codeWorkspaceNotFound, e.app.runSetHidden("nope", true))
		assert.Empty(t, e.store.SetHiddenCalls())
	})
	t.Run("a failed write", func(t *testing.T) {
		e := newHideApp()
		e.store.SetHiddenFunc = func(string, bool) (workspace.Workspace, error) { return workspace.Workspace{}, errTest }
		assert.ErrorIs(t, e.app.runSetHidden(renamedID, true), errTest)
	})
}
