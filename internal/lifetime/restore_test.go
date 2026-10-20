package lifetime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestManager_Restore(t *testing.T) {
	t.Run("respawns every stopped agent and leaves the rest", func(t *testing.T) {
		f := newFixture(t)
		stopped := f.agent("stopped", "s1")
		running := f.agent("running", "s2")
		f.shell("sh")
		f.add("unrecorded")
		f.stopped("s1", "/cfg/workspaces/stopped")
		f.listed("s2", "/cfg/workspaces/running")
		got, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []Restored{
			{Workspace: stopped, SessionID: "s1", State: StateStopped, Respawned: true},
			{Workspace: running, SessionID: "s2", State: StateIdle},
		}, got)
		assert.Equal(t, []string{"s1"}, f.respawned())
	})
	t.Run("respawns in order, one at a time", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.stopped("s2", "/cfg/workspaces/b")
		f.stopped("s1", "/cfg/workspaces/a")
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"s1", "s2"}, f.respawned())
	})
	t.Run("waits for each respawned session's process before the next respawn", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.stopped("s1", "/cfg/workspaces/a")
		f.stopped("s2", "/cfg/workspaces/b")
		var order []string
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			order = append(order, "agents")
			return f.sessions, nil
		}
		f.claude.RespawnFunc = func(_ context.Context, id string) error {
			order = append(order, "respawn "+id)
			f.giveProcess(id)
			return nil
		}
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"agents", "respawn s1", "agents", "respawn s2", "agents"}, order)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("waits for another ws restore's lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, ".respawn.lock"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = f.m.Restore(ctx, RestoreOptions{All: true})
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, f.claude.AgentsCalls())
		assert.Empty(t, f.respawned())
	})
	t.Run("releases the lock when done", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		held, err := lockfile.HeldExclusively(filepath.Join(f.stateDir, ".respawn.lock"))
		require.NoError(t, err)
		assert.False(t, held)
	})
	t.Run("a dry run doesn't wait for the lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, ".respawn.lock"))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		got, err := f.m.Restore(ctx, RestoreOptions{All: true, DryRun: true})
		require.NoError(t, err)
		assert.Equal(t, []Restored{{Workspace: ws, SessionID: "s1", State: StateStopped}}, got)
	})
	t.Run("nothing to restore doesn't take the lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.shell("sh")
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.NoFileExists(t, filepath.Join(f.stateDir, ".respawn.lock"))
	})
	t.Run("named workspaces only", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		b := f.agent("b", "s2")
		f.stopped("s1", "/cfg/workspaces/a")
		f.stopped("s2", "/cfg/workspaces/b")
		got, err := f.m.Restore(t.Context(), RestoreOptions{Workspaces: []workspace.Workspace{b}})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "b", got[0].Workspace.ID)
		assert.Equal(t, []string{"s2"}, f.respawned())
		assert.Empty(t, f.store.ListCalls())
	})
	t.Run("a dry run respawns nothing", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		got, err := f.m.Restore(t.Context(), RestoreOptions{All: true, DryRun: true})
		require.NoError(t, err)
		assert.Equal(t, []Restored{{Workspace: ws, SessionID: "s1", State: StateStopped}}, got)
		assert.Empty(t, f.respawned())
	})
	t.Run("reads every record before listing sessions, once", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		var order []string
		f.store.SessionFunc = func(id string) (workspace.Session, error) {
			order = append(order, "record "+id)
			return f.records[id], nil
		}
		f.claude.AgentsFunc = func(_ context.Context, opts claude.ListOptions) ([]claude.Session, error) {
			order = append(order, "agents")
			assert.True(t, opts.All)
			return nil, nil
		}
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []string{"record a", "record b", "agents"}, order)
	})
	t.Run("doesn't list sessions without an agent", func(t *testing.T) {
		f := newFixture(t)
		f.shell("sh")
		got, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Empty(t, f.claude.AgentsCalls())
	})
	t.Run("a failed listing fails", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.ErrorIs(t, err, errTest)
		assert.Empty(t, f.respawned())
	})
	t.Run("the workspaces can't be listed", func(t *testing.T) {
		f := newFixture(t)
		f.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		_, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		assert.ErrorIs(t, err, errTest)
	})
	t.Run("a failed respawn fails only its workspace", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.stopped("s1", "/cfg/workspaces/a")
		f.stopped("s2", "/cfg/workspaces/b")
		f.claude.RespawnFunc = func(_ context.Context, id string) error {
			if id == "s1" {
				return errTest
			}
			return nil
		}
		got, err := f.m.Restore(t.Context(), RestoreOptions{All: true})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.ErrorIs(t, got[0].Err, errTest)
		assert.False(t, got[0].Respawned)
		assert.True(t, got[1].Respawned)
	})
	t.Run("a named shell is an error item, and the others are still restored", func(t *testing.T) {
		f := newFixture(t)
		sh := f.shell("sh")
		a := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		got, err := f.m.Restore(t.Context(), RestoreOptions{Workspaces: []workspace.Workspace{sh, a}})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.ErrorIs(t, got[0].Err, ErrNotAgent)
		assert.True(t, got[1].Respawned)
	})
}

func TestManager_restoreTargets(t *testing.T) {
	t.Run("all skips workspaces without an agent", func(t *testing.T) {
		f := newFixture(t)
		f.shell("sh")
		f.add("unrecorded")
		f.locked["busy"] = true
		f.add("busy")
		a := f.agent("a", "s1")
		got, err := f.m.restoreTargets(RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []Restored{{Workspace: a, SessionID: "s1"}}, got)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("all warns about a record it can't read", func(t *testing.T) {
		f := newFixture(t)
		f.add("x")
		f.store.SessionFunc = func(string) (workspace.Session, error) { return workspace.Session{}, errTest }
		got, err := f.m.restoreTargets(RestoreOptions{All: true})
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Equal(t, []string{"skipped workspace x: boom"}, f.m.Warnings())
	})
	t.Run("named keeps every workspace, with its error", func(t *testing.T) {
		f := newFixture(t)
		sh := f.shell("sh")
		a := f.agent("a", "s1")
		got, err := f.m.restoreTargets(RestoreOptions{Workspaces: []workspace.Workspace{sh, a}})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.ErrorIs(t, got[0].Err, ErrNotAgent)
		assert.Equal(t, Restored{Workspace: a, SessionID: "s1"}, got[1])
	})
}

func TestManager_restoreTarget(t *testing.T) {
	t.Run("an agent", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		assert.Equal(t, Restored{Workspace: ws, SessionID: "s1"}, f.m.restoreTarget(&ws))
	})
	t.Run("a shell", func(t *testing.T) {
		f := newFixture(t)
		ws := f.shell("sh")
		r := f.m.restoreTarget(&ws)
		require.ErrorIs(t, r.Err, ErrNotAgent)
		assert.EqualError(t, r.Err, "not an agent workspace: sh is a shell workspace; only agent workspaces are restored")
	})
	t.Run("no session record", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		assert.ErrorIs(t, f.m.restoreTarget(&ws).Err, ErrNotAgent)
	})
	t.Run("an unreadable record", func(t *testing.T) {
		f := newFixture(t)
		ws := f.add("x")
		f.store.SessionFunc = func(string) (workspace.Session, error) { return workspace.Session{}, errTest }
		assert.ErrorIs(t, f.m.restoreTarget(&ws).Err, errTest)
	})
}

func TestManager_unrecordedTarget(t *testing.T) {
	t.Run("busy while locked", func(t *testing.T) {
		f := newFixture(t)
		f.locked["x"] = true
		assert.ErrorIs(t, f.m.unrecordedTarget("x"), ErrBusy)
	})
	t.Run("not an agent once unlocked", func(t *testing.T) {
		f := newFixture(t)
		assert.EqualError(t, f.m.unrecordedTarget("x"), "not an agent workspace: x has no session recorded")
	})
	t.Run("the lock can't be checked", func(t *testing.T) {
		f := newFixture(t)
		f.store.LockedFunc = func(string) (bool, error) { return false, errTest }
		assert.ErrorIs(t, f.m.unrecordedTarget("x"), errTest)
	})
}

func TestManager_skipped(t *testing.T) {
	t.Run("not an agent is silent", func(t *testing.T) {
		f := newFixture(t)
		f.m.skipped(&Restored{Err: ErrNotAgent})
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("busy is silent", func(t *testing.T) {
		f := newFixture(t)
		f.m.skipped(&Restored{Err: ErrBusy})
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("anything else warns", func(t *testing.T) {
		f := newFixture(t)
		f.m.skipped(&Restored{Workspace: workspace.Workspace{ID: "x"}, Err: errTest})
		assert.Equal(t, []string{"skipped workspace x: boom"}, f.m.Warnings())
	})
}

func TestAnyAgent(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.False(t, anyAgent(nil))
	})
	t.Run("only errors", func(t *testing.T) {
		assert.False(t, anyAgent([]Restored{{Err: ErrNotAgent}}))
	})
	t.Run("an agent", func(t *testing.T) {
		assert.True(t, anyAgent([]Restored{{Err: ErrNotAgent}, {SessionID: "s1"}}))
	})
}

func TestManager_restoreOne(t *testing.T) {
	t.Run("respawns a stopped session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.Equal(t, Restored{Workspace: ws, SessionID: "s1", State: StateStopped, Respawned: true}, r)
		assert.Equal(t, []string{"s1"}, f.respawned())
		assert.Len(t, f.claude.AgentsCalls(), 1)
	})
	t.Run("a respawn that never shows a process still counts as restored", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		f.claude.RespawnFunc = func(context.Context, string) error { return nil }
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		require.NoError(t, r.Err)
		assert.True(t, r.Respawned)
		assert.Len(t, f.m.Warnings(), 1)
	})
	t.Run("leaves an idle session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.Equal(t, StateIdle, r.State)
		assert.Empty(t, f.respawned())
	})
	t.Run("leaves a working session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.sessions[0].Status = "busy"
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.Equal(t, StateWorking, r.State)
		assert.Empty(t, f.respawned())
	})
	t.Run("leaves a starting session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.sessions = []claude.Session{{ID: "s1", Cwd: "/cfg/workspaces/a", StartedAt: testNow().Add(-3 * time.Second)}}
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.Equal(t, StateStarting, r.State)
		assert.Empty(t, f.respawned())
	})
	t.Run("leaves a session Claude Code doesn't list", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, nil, false)
		assert.Equal(t, Restored{Workspace: ws, SessionID: "s1", State: StateUnknown}, r)
		assert.Empty(t, f.respawned())
	})
	t.Run("refuses a session in another directory", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/Users/me/project")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		require.ErrorIs(t, r.Err, ErrSessionElsewhere)
		assert.Empty(t, r.State)
		assert.Empty(t, f.respawned())
	})
	t.Run("refuses a running session in another directory too", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.listed("s1", "/Users/me/project")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.ErrorIs(t, r.Err, ErrSessionElsewhere)
	})
	t.Run("a dry run makes the same checks", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/Users/me/project")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, true)
		assert.ErrorIs(t, r.Err, ErrSessionElsewhere)
	})
	t.Run("a failed respawn", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		f.claude.RespawnFunc = func(context.Context, string) error { return errTest }
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		require.ErrorIs(t, r.Err, errTest)
		require.EqualError(t, r.Err, "respawning Claude Code session s1: boom")
		assert.Equal(t, StateStopped, r.State)
		assert.False(t, r.Respawned)
	})
	t.Run("a session removed since the listing is unknown", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		f.claude.RespawnFunc = func(context.Context, string) error { return claude.ErrNoSession }
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		assert.Equal(t, Restored{Workspace: ws, SessionID: "s1", State: StateUnknown}, r)
	})
	t.Run("a dry run leaves a stopped session", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, true)
		assert.Equal(t, Restored{Workspace: ws, SessionID: "s1", State: StateStopped}, r)
		assert.Empty(t, f.respawned())
	})
	t.Run("a workspace removed since its record was read", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.stopped("s1", "/cfg/workspaces/a")
		f.store.GuardedDirFunc = func(string) (string, error) { return "", workspace.ErrNotFound }
		r := Restored{Workspace: ws, SessionID: "s1"}
		f.m.restoreOne(t.Context(), &r, f.sessions, false)
		require.ErrorIs(t, r.Err, workspace.ErrNotFound)
		assert.Empty(t, f.respawned())
	})
}

func TestManager_awaitProcess(t *testing.T) {
	t.Run("returns at once when the session has a process", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/cfg/workspaces/a")
		f.m.awaitProcess(t.Context(), "s1")
		assert.Len(t, f.claude.AgentsCalls(), 1)
		assert.Empty(t, f.sleeps)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("lists again until the process shows", func(t *testing.T) {
		f := newFixture(t)
		f.stopped("s1", "/cfg/workspaces/a")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			if len(f.sleeps) == 2 {
				f.giveProcess("s1")
			}
			return f.sessions, nil
		}
		f.m.awaitProcess(t.Context(), "s1")
		assert.Len(t, f.claude.AgentsCalls(), 3)
		assert.Equal(t, []time.Duration{respawnPoll, respawnPoll}, f.sleeps)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("warns and moves on when the process never shows", func(t *testing.T) {
		f := newFixture(t)
		f.stopped("s1", "/cfg/workspaces/a")
		f.m.awaitProcess(t.Context(), "s1")
		assert.Equal(t, testNow().Add(respawnWait), f.clock)
		assert.Len(t, f.sleeps, int(respawnWait/respawnPoll))
		assert.Equal(t, []string{"Claude Code didn't list session s1 with a process within 10s of respawning it; another ws restore may respawn it again"}, f.m.Warnings())
	})
	t.Run("an unlisted session is waited for too", func(t *testing.T) {
		f := newFixture(t)
		f.m.awaitProcess(t.Context(), "s1")
		assert.Len(t, f.m.Warnings(), 1)
	})
	t.Run("a failing listing keeps waiting, and the warning says why", func(t *testing.T) {
		f := newFixture(t)
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		f.m.awaitProcess(t.Context(), "s1")
		assert.Len(t, f.sleeps, int(respawnWait/respawnPoll))
		assert.Equal(t, []string{"Claude Code didn't list session s1 with a process within 10s of respawning it; another ws restore may respawn it again (last listing failed: boom)"}, f.m.Warnings())
	})
	t.Run("stops when the context is done", func(t *testing.T) {
		f := newFixture(t)
		f.m.sleep = func(context.Context, time.Duration) error { return context.Canceled }
		f.m.awaitProcess(t.Context(), "s1")
		assert.Len(t, f.claude.AgentsCalls(), 1)
		assert.Empty(t, f.m.Warnings())
	})
}

func TestManager_inWorkspace(t *testing.T) {
	t.Run("in the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		assert.NoError(t, f.m.inWorkspace(&ws, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/a"}))
	})
	t.Run("below the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		assert.ErrorIs(t, f.m.inWorkspace(&ws, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/a/repo"}), ErrSessionElsewhere)
	})
	t.Run("the guard refuses the workspace", func(t *testing.T) {
		f := newFixture(t)
		ws := f.agent("a", "s1")
		f.store.GuardedDirFunc = func(string) (string, error) { return "", errTest }
		assert.ErrorIs(t, f.m.inWorkspace(&ws, &claude.Session{ID: "s1", Cwd: "/cfg/workspaces/a"}), errTest)
	})
}

func TestFindSession(t *testing.T) {
	sessions := []claude.Session{{ID: "s1"}, {ID: "s2"}}
	t.Run("found", func(t *testing.T) {
		assert.Same(t, &sessions[1], findSession(sessions, "s2"))
	})
	t.Run("missing", func(t *testing.T) {
		assert.Nil(t, findSession(sessions, "s3"))
	})
	t.Run("no id", func(t *testing.T) {
		assert.Nil(t, findSession(sessions, ""))
	})
}
