package lifetime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestManager_Remove(t *testing.T) {
	t.Run("an agent", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		got, err := f.m.Remove(t.Context(), &ws, false)
		require.NoError(t, err)
		assert.Equal(t, Removed{Workspace: ws, SessionID: "e8cdf5d8"}, got)
		assert.Equal(t, []string{"e8cdf5d8"}, f.claudeRemoved())
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("a shell", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		got, err := f.m.Remove(t.Context(), &ws, false)
		require.NoError(t, err)
		assert.Equal(t, Removed{Workspace: ws}, got)
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("no session record", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		_, err := f.m.Remove(t.Context(), &ws, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"e8cdf5d8"}, f.claudeRemoved())
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("an unreadable record keeps the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.store.SessionFunc = func(string) (workspace.Session, error) { return workspace.Session{}, errTest }
		_, err := f.m.Remove(t.Context(), &ws, false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.removedIDs())
	})
}

func TestManager_removeShell(t *testing.T) {
	t.Run("refused while the shell runs", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.locked["x"] = true
		_, err := f.m.removeShell(&ws, false)
		require.ErrorIs(t, err, ErrShellRunning)
		assert.Empty(t, f.removedIDs())
	})
	t.Run("refused with a dry run too", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.locked["x"] = true
		_, err := f.m.removeShell(&ws, true)
		assert.ErrorIs(t, err, ErrShellRunning)
	})
	t.Run("removed once it has exited", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		_, err := f.m.removeShell(&ws, false)
		require.NoError(t, err)
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("the lock can't be checked", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.store.LockedFunc = func(string) (bool, error) { return false, errTest }
		_, err := f.m.removeShell(&ws, false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.removedIDs())
	})
}

func TestManager_removeAgent(t *testing.T) {
	t.Run("stops and removes the session, then the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		var order []string
		f.claude.StopFunc = func(_ context.Context, id string) error {
			order = append(order, "stop "+id)
			return nil
		}
		f.claude.RemoveFunc = func(_ context.Context, id string) error {
			order = append(order, "rm "+id)
			return nil
		}
		f.store.RemoveFunc = func(id string, _ workspace.RemoveOptions) error {
			order = append(order, "remove "+id)
			return nil
		}
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.NoError(t, err)
		assert.Equal(t, []string{"stop e8cdf5d8", "rm e8cdf5d8", "remove x"}, order)
	})
	t.Run("a session Claude Code no longer has", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.claude.StopFunc = func(context.Context, string) error { return claude.ErrNoSession }
		f.claude.RemoveFunc = func(context.Context, string) error { return claude.ErrNoSession }
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.NoError(t, err)
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("a failed stop keeps the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.claude.StopFunc = func(context.Context, string) error { return errTest }
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.claude.RemoveCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a failed claude rm keeps the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.claude.RemoveFunc = func(context.Context, string) error { return errTest }
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a session in another directory keeps both", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/Users/me/project")
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.ErrorIs(t, err, ErrSessionElsewhere)
		assert.Empty(t, f.claude.StopCalls())
		assert.Empty(t, f.claude.RemoveCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a dry run makes the same check", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/Users/me/project")
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", true)
		assert.ErrorIs(t, err, ErrSessionElsewhere)
	})
	t.Run("sessions can't be listed keeps both", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		_, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.claude.StopCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a dry run changes nothing", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		got, err := f.m.removeAgent(t.Context(), &ws, "e8cdf5d8", true)
		require.NoError(t, err)
		assert.Equal(t, Removed{Workspace: ws, SessionID: "e8cdf5d8"}, got)
		assert.Empty(t, f.claude.StopCalls())
		assert.Empty(t, f.claude.RemoveCalls())
		assert.Empty(t, f.removedIDs())
	})
}

func TestManager_checkSessionDir(t *testing.T) {
	t.Run("a session listed in the workspace's directory", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/cfg/workspaces/x/")
		require.NoError(t, f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8"))
		assert.Equal(t, claude.ListOptions{All: true}, f.claude.AgentsCalls()[0].Opts)
	})
	t.Run("a session Claude Code doesn't list is already gone", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("5d8d652c", "/elsewhere")
		assert.NoError(t, f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8"))
	})
	t.Run("a session listed in another directory is refused", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/Users/me/project")
		err := f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8")
		require.ErrorIs(t, err, ErrSessionElsewhere)
		assert.Contains(t, err.Error(), "session e8cdf5d8 is in /Users/me/project, not in workspace x's directory /cfg/workspaces/x")
	})
	t.Run("a session listed below the workspace is refused", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/cfg/workspaces/x/repo")
		assert.ErrorIs(t, f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8"), ErrSessionElsewhere)
	})
	t.Run("the guard refuses the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.store.GuardedDirFunc = func(string) (string, error) { return "", workspace.ErrUnsafe }
		require.ErrorIs(t, f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8"), workspace.ErrUnsafe)
		assert.Empty(t, f.claude.AgentsCalls())
	})
	t.Run("sessions can't be listed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("x", "e8cdf5d8")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		assert.ErrorIs(t, f.m.checkSessionDir(t.Context(), &ws, "e8cdf5d8"), errTest)
	})
}

func TestSessionElsewhere(t *testing.T) {
	ws := workspace.Workspace{ID: "x"}
	t.Run("in the directory", func(t *testing.T) {
		assert.NoError(t, sessionElsewhere(&ws, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/x/"}, "/cfg/workspaces/x"))
	})
	t.Run("in another directory", func(t *testing.T) {
		err := sessionElsewhere(&ws, &claude.Session{ID: "s1", Cwd: "/Users/me/project"}, "/cfg/workspaces/x")
		require.ErrorIs(t, err, ErrSessionElsewhere)
		assert.EqualError(t, err, "the workspace's Claude Code session is in another directory: session s1 is in /Users/me/project, not in workspace x's directory /cfg/workspaces/x; neither was touched")
	})
	oneRepo := workspace.Workspace{ID: "x", Repos: []workspace.Repo{{ID: "example.test/o/api", Path: "api"}}}
	t.Run("in a one-repo workspace's repo directory", func(t *testing.T) {
		assert.NoError(t, sessionElsewhere(&oneRepo, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/x/api"}, "/cfg/workspaces/x"))
	})
	t.Run("in a one-repo workspace's directory", func(t *testing.T) {
		assert.NoError(t, sessionElsewhere(&oneRepo, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/x"}, "/cfg/workspaces/x"))
	})
	t.Run("elsewhere from a one-repo workspace names both directories", func(t *testing.T) {
		err := sessionElsewhere(&oneRepo, &claude.Session{ID: "s1", Cwd: "/Users/me/project"}, "/cfg/workspaces/x")
		require.ErrorIs(t, err, ErrSessionElsewhere)
		assert.EqualError(t, err, "the workspace's Claude Code session is in another directory: session s1 is in /Users/me/project, not in workspace x's directory /cfg/workspaces/x or /cfg/workspaces/x/api; neither was touched")
	})
}

func TestManager_removeUnrecorded(t *testing.T) {
	t.Run("refused while a creation or hand-off holds the lock", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.locked["x"] = true
		_, err := f.m.removeUnrecorded(t.Context(), &ws, false)
		require.ErrorIs(t, err, ErrBusy)
		assert.Empty(t, f.claude.AgentsCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("removes the sessions started in it, then the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		f.listed("5d8d652c", "/elsewhere")
		got, err := f.m.removeUnrecorded(t.Context(), &ws, false)
		require.NoError(t, err)
		assert.Equal(t, Removed{Workspace: ws}, got)
		assert.Equal(t, []string{"e8cdf5d8"}, f.claudeRemoved())
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("a dry run lists nothing and changes nothing", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		_, err := f.m.removeUnrecorded(t.Context(), &ws, true)
		require.NoError(t, err)
		assert.Empty(t, f.claude.AgentsCalls())
		assert.Empty(t, f.removedIDs())
	})
	t.Run("kept when sessions can't be listed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		_, err := f.m.removeUnrecorded(t.Context(), &ws, false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.removedIDs())
	})
	t.Run("kept when a session can't be removed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		f.claude.RemoveFunc = func(context.Context, string) error { return errTest }
		_, err := f.m.removeUnrecorded(t.Context(), &ws, false)
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.removedIDs())
	})
	t.Run("the lock can't be checked", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.store.LockedFunc = func(string) (bool, error) { return false, errTest }
		_, err := f.m.removeUnrecorded(t.Context(), &ws, false)
		assert.ErrorIs(t, err, errTest)
	})
}

func TestManager_removeWorkspace(t *testing.T) {
	t.Run("removes through the deletion guard", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		got, err := f.m.removeWorkspace(&ws, "e8cdf5d8", false)
		require.NoError(t, err)
		assert.Equal(t, Removed{Workspace: ws, SessionID: "e8cdf5d8"}, got)
		require.Len(t, f.store.RemoveCalls(), 1)
		assert.False(t, f.store.RemoveCalls()[0].Opts.AllowOutsideRoot)
	})
	t.Run("a dry run", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		_, err := f.m.removeWorkspace(&ws, "", true)
		require.NoError(t, err)
		assert.Empty(t, f.removedIDs())
	})
	t.Run("a failure", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return workspace.ErrUnsafe }
		_, err := f.m.removeWorkspace(&ws, "", false)
		assert.ErrorIs(t, err, workspace.ErrUnsafe)
	})
}
