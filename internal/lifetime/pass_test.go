package lifetime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// stampAt sets the removal pass's timestamp to when.
func stampAt(t *testing.T, f *fixture, when time.Time) {
	t.Helper()
	require.NoError(t, os.WriteFile(f.m.stamp, nil, 0o600))
	require.NoError(t, os.Chtimes(f.m.stamp, when, when))
}

// stampTime is the removal pass's timestamp.
func stampTime(t *testing.T, f *fixture) time.Time {
	t.Helper()
	info, err := os.Stat(f.m.stamp)
	require.NoError(t, err)
	return info.ModTime()
}

func TestManager_Sweep(t *testing.T) {
	t.Parallel()
	t.Run("removes leftovers and gone workspaces when due", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ws := f.shell("x")
		removed := f.m.Sweep(t.Context())
		assert.Len(t, f.store.CleanLeftoversCalls(), 1)
		assert.Equal(t, []Removed{{Workspace: ws}}, removed)
		assert.Equal(t, []string{"x"}, f.removedIDs())
		assert.True(t, testNow().Equal(stampTime(t, f)), "records the pass")
	})
	t.Run("only leftovers within a minute of the last pass", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.shell("x")
		stampAt(t, f, testNow().Add(-59*time.Second))
		assert.Empty(t, f.m.Sweep(t.Context()))
		assert.Len(t, f.store.CleanLeftoversCalls(), 1)
		assert.Empty(t, f.store.ListCalls())
	})
	t.Run("a failed listing is a warning", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		assert.Empty(t, f.m.Sweep(t.Context()))
		assert.Equal(t, []string{"couldn't check workspaces' sessions: boom"}, f.m.Warnings())
	})
}

func TestManager_List(t *testing.T) {
	t.Parallel()
	t.Run("removes gone workspaces and returns the rest, even right after a pass", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.shell("gone")
		running := f.shell("running")
		f.locked["running"] = true
		f.store.UnknownFunc = func() ([]string, error) { return []string{"/cfg/workspaces/stray"}, nil }
		stampAt(t, f, testNow().Add(-time.Second))
		got, err := f.m.List(t.Context())
		require.NoError(t, err)
		assert.Equal(t, Listing{
			Workspaces: []Entry{{Workspace: running, Session: &Session{Kind: KindShell, State: StateRunning}}},
			Unknown:    []string{"/cfg/workspaces/stray"},
		}, got)
		assert.Equal(t, []string{"gone"}, f.removedIDs())
		assert.Len(t, f.store.CleanLeftoversCalls(), 1)
		assert.True(t, testNow().Equal(stampTime(t, f)))
	})
	t.Run("reads the background service for stale agents", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("x", "e8cdf5d8")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		got, err := f.m.List(t.Context())
		require.NoError(t, err)
		require.Len(t, got.Workspaces, 1)
		require.NotNil(t, got.Workspaces[0].Session.Stale)
		assert.False(t, *got.Workspaces[0].Session.Stale)
	})
	t.Run("a failed listing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		_, err := f.m.List(t.Context())
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("unknown directories can't be read", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.store.UnknownFunc = func() ([]string, error) { return nil, errTest }
		_, err := f.m.List(t.Context())
		assert.ErrorIs(t, err, errTest)
	})
}

func TestManager_pass(t *testing.T) {
	t.Run("keeps live sessions, removes gone and abandoned workspaces", func(t *testing.T) {
		f := newFixture(t)
		live := f.agent("live", "aaaa1111")
		f.listed("aaaa1111", "/cfg/workspaces/live")
		gone := f.agent("gone", "bbbb2222")
		abandoned := f.add("abandoned")
		f.abandoned["abandoned"] = true
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, live, entries[0].Workspace)
		assert.Equal(t, []Removed{{Workspace: gone, SessionID: "bbbb2222"}, {Workspace: abandoned}}, removed)
	})
	t.Run("a workspace still being handed over is kept without a session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Equal(t, []Entry{{Workspace: ws}}, entries)
		assert.Empty(t, removed)
	})
	t.Run("an abandoned workspace that can't be removed is still listed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.abandoned["x"] = true
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Equal(t, []Entry{{Workspace: ws}}, entries)
		assert.Empty(t, removed)
	})
	t.Run("a gone workspace that can't be removed is still listed, with a warning", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return errTest }
		entries, removed, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Equal(t, []Entry{{Workspace: ws, Session: &Session{Kind: KindShell, State: StateUnknown}}}, entries)
		assert.Empty(t, removed)
		assert.Equal(t, []string{"couldn't remove workspace x: boom"}, f.m.Warnings())
	})
	t.Run("lists Claude Code's sessions only when a workspace needs it", func(t *testing.T) {
		f := newFixture(t)
		f.shell("x")
		f.locked["x"] = true
		_, _, err := f.m.pass(t.Context(), newReader(f.m, true))
		require.NoError(t, err)
		assert.Empty(t, f.claude.AgentsCalls())
		assert.Empty(t, f.claude.DaemonStatusCalls())
	})
	t.Run("lists Claude Code's sessions once for many agents", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "aaaa1111")
		f.agent("b", "bbbb2222")
		f.listed("aaaa1111", "/cfg/workspaces/a")
		f.listed("bbbb2222", "/cfg/workspaces/b")
		_, _, err := f.m.pass(t.Context(), newReader(f.m, true))
		require.NoError(t, err)
		assert.Len(t, f.claude.AgentsCalls(), 1)
		assert.True(t, f.claude.AgentsCalls()[0].Opts.All)
		assert.Len(t, f.claude.DaemonStatusCalls(), 1)
	})
	t.Run("reads every session record before listing Claude Code's sessions", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "aaaa1111")
		f.agent("b", "bbbb2222")
		f.listed("aaaa1111", "/cfg/workspaces/a")
		f.listed("bbbb2222", "/cfg/workspaces/b")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			assert.Len(t, f.store.SessionCalls(), 2, "both records read first")
			return f.sessions, nil
		}
		entries, _, err := f.m.pass(t.Context(), newReader(f.m, false))
		require.NoError(t, err)
		assert.Len(t, entries, 2)
	})
	t.Run("a failed listing", func(t *testing.T) {
		f := newFixture(t)
		f.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		_, _, err := f.m.pass(t.Context(), newReader(f.m, false))
		assert.ErrorIs(t, err, errTest)
	})
}

func TestSessionID(t *testing.T) {
	t.Run("no session", func(t *testing.T) {
		assert.Empty(t, sessionID(nil))
	})
	t.Run("an agent", func(t *testing.T) {
		assert.Equal(t, "e8cdf5d8", sessionID(&Session{Kind: KindAgent, ID: "e8cdf5d8"}))
	})
}

func TestManager_reap(t *testing.T) {
	t.Run("a gone workspace is removed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		assert.True(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, gone))
		assert.Equal(t, []string{"x"}, f.removedIDs())
		assert.False(t, f.store.RemoveCalls()[0].Opts.AllowOutsideRoot)
		assert.Empty(t, f.claude.AgentsCalls())
	})
	t.Run("an abandoned workspace goes with the sessions started in it", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		f.listed("5d8d652c", "/cfg/workspaces/y")
		assert.True(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, abandoned))
		assert.Equal(t, []string{"e8cdf5d8"}, f.claudeRemoved())
		assert.Equal(t, []string{"x"}, f.removedIDs())
	})
	t.Run("an abandoned workspace is kept when sessions can't be listed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		assert.False(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, abandoned))
		assert.Empty(t, f.removedIDs())
	})
	t.Run("an abandoned workspace is kept when its session can't be removed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		f.claude.RemoveFunc = func(context.Context, string) error { return errTest }
		assert.False(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, abandoned))
		assert.Empty(t, f.removedIDs())
		require.Len(t, f.m.Warnings(), 1)
		assert.Contains(t, f.m.Warnings()[0], "kept abandoned workspace x")
	})
	t.Run("a workspace another ws already removed counts as removed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return workspace.ErrNotFound }
		assert.True(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, gone))
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("an abandoned workspace another ws is removing counts as removed", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.store.GuardedDirFunc = func(string) (string, error) { return "", workspace.ErrNotFound }
		f.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return workspace.ErrNotFound }
		assert.True(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, abandoned))
		assert.Empty(t, f.claudeRemoved())
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("a failed removal is a warning", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("x")
		f.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return workspace.ErrUnsafe }
		assert.False(t, f.m.reap(t.Context(), newReader(f.m, false), &ws, gone))
		assert.Equal(t, []string{"couldn't remove workspace x: refusing to delete"}, f.m.Warnings())
	})
}

func TestManager_due(t *testing.T) {
	t.Parallel()
	t.Run("never run", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		assert.True(t, f.m.due())
		assert.True(t, testNow().Equal(stampTime(t, f)))
	})
	t.Run("run a minute ago", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		stampAt(t, f, testNow().Add(-time.Minute))
		assert.True(t, f.m.due())
	})
	t.Run("run less than a minute ago", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		when := testNow().Add(-30 * time.Second)
		stampAt(t, f, when)
		assert.False(t, f.m.due())
		assert.True(t, when.Equal(stampTime(t, f)), "unchanged")
	})
	t.Run("a stamp in the future, after the clock went back", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		stampAt(t, f, testNow().Add(time.Hour))
		assert.True(t, f.m.due())
	})
	t.Run("no state directory, so no workspaces", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		require.NoError(t, os.Remove(f.stateDir))
		assert.False(t, f.m.due())
		assert.NoDirExists(t, f.stateDir)
	})
}

func TestManager_touch(t *testing.T) {
	t.Parallel()
	t.Run("records now", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.m.touch()
		assert.True(t, testNow().Equal(stampTime(t, f)))
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("a failure is a warning", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.m.stamp = f.stateDir + "/missing/stamp"
		require.NoError(t, os.WriteFile(f.stateDir+"/missing", nil, 0o600))
		f.m.touch()
		assert.Len(t, f.m.Warnings(), 1)
	})
}

func TestManager_writeStamp(t *testing.T) {
	t.Parallel()
	t.Run("creates the stamp", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		require.NoError(t, f.m.writeStamp())
		assert.True(t, testNow().Equal(stampTime(t, f)))
	})
	t.Run("updates the stamp", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		stampAt(t, f, testNow().Add(-time.Hour))
		require.NoError(t, f.m.writeStamp())
		assert.True(t, testNow().Equal(stampTime(t, f)))
	})
	t.Run("no state directory, nothing written", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		require.NoError(t, os.Remove(f.stateDir))
		require.NoError(t, f.m.writeStamp())
		assert.NoDirExists(t, f.stateDir)
	})
	t.Run("the stamp can't be written", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		require.NoError(t, os.Chmod(f.stateDir, 0o500))
		t.Cleanup(func() { _ = os.Chmod(f.stateDir, 0o700) })
		assert.Error(t, f.m.writeStamp())
	})
}
