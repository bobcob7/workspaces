package lifetime

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

func TestReader_judge(t *testing.T) {
	t.Run("no session record goes by abandonment", func(t *testing.T) {
		f := newFixture(t)
		f.abandoned["x"] = true
		sess, v := newReader(f.m, false).judge(t.Context(), "x", &record{err: workspace.ErrNoSession})
		assert.Nil(t, sess)
		assert.Equal(t, abandoned, v)
	})
	t.Run("an unreadable record is kept with a warning", func(t *testing.T) {
		f := newFixture(t)
		sess, v := newReader(f.m, false).judge(t.Context(), "x", &record{err: errTest})
		assert.Nil(t, sess)
		assert.Equal(t, keep, v)
		assert.Equal(t, []string{"kept workspace x: boom"}, f.m.Warnings())
	})
	t.Run("a shell", func(t *testing.T) {
		f := newFixture(t)
		f.locked["x"] = true
		sess, v := newReader(f.m, false).judge(t.Context(), "x", &record{sess: workspace.Session{Kind: workspace.SessionShell}})
		assert.Equal(t, &Session{Kind: KindShell, State: StateRunning}, sess)
		assert.Equal(t, keep, v)
	})
	t.Run("an agent", func(t *testing.T) {
		f := newFixture(t)
		_, v := newReader(f.m, false).judge(t.Context(), "x", &record{sess: workspace.Session{Kind: workspace.SessionClaude, ClaudeSessionID: "e8cdf5d8"}})
		assert.Equal(t, gone, v)
		require.Len(t, f.claude.JobExistsCalls(), 1)
		assert.Equal(t, "e8cdf5d8", f.claude.JobExistsCalls()[0].ID)
	})
}

func TestReader_unrecorded(t *testing.T) {
	t.Run("abandoned", func(t *testing.T) {
		f := newFixture(t)
		f.abandoned["x"] = true
		assert.Equal(t, abandoned, newReader(f.m, false).unrecorded("x"))
	})
	t.Run("still being handed over", func(t *testing.T) {
		f := newFixture(t)
		assert.Equal(t, keep, newReader(f.m, false).unrecorded("x"))
	})
	t.Run("can't tell", func(t *testing.T) {
		f := newFixture(t)
		f.store.AbandonedFunc = func(string) (bool, error) { return true, errTest }
		assert.Equal(t, keep, newReader(f.m, false).unrecorded("x"))
		assert.Len(t, f.m.Warnings(), 1)
	})
}

func TestReader_shell(t *testing.T) {
	t.Run("running while locked", func(t *testing.T) {
		f := newFixture(t)
		f.locked["x"] = true
		sess, v := newReader(f.m, false).shell("x")
		assert.Equal(t, &Session{Kind: KindShell, State: StateRunning}, sess)
		assert.Equal(t, keep, v)
	})
	t.Run("gone once unlocked", func(t *testing.T) {
		f := newFixture(t)
		_, v := newReader(f.m, false).shell("x")
		assert.Equal(t, gone, v)
	})
	t.Run("kept when the lock can't be checked", func(t *testing.T) {
		f := newFixture(t)
		f.store.LockedFunc = func(string) (bool, error) { return false, errTest }
		sess, v := newReader(f.m, false).shell("x")
		assert.Equal(t, &Session{Kind: KindShell, State: StateUnknown}, sess)
		assert.Equal(t, keep, v)
		assert.Len(t, f.m.Warnings(), 1)
	})
}

func TestReader_agent(t *testing.T) {
	t.Run("listed, in any state", func(t *testing.T) {
		f := newFixture(t)
		f.sessions = []claude.Session{{ID: "e8cdf5d8", State: claude.StateStopped}}
		sess, v := newReader(f.m, false).agent(t.Context(), "e8cdf5d8")
		assert.Equal(t, &Session{Kind: KindAgent, ID: "e8cdf5d8", State: StateStopped}, sess)
		assert.Equal(t, keep, v)
		assert.Empty(t, f.claude.JobExistsCalls())
	})
	t.Run("not listed but its jobs directory exists", func(t *testing.T) {
		f := newFixture(t)
		f.jobs["e8cdf5d8"] = true
		sess, v := newReader(f.m, false).agent(t.Context(), "e8cdf5d8")
		assert.Equal(t, &Session{Kind: KindAgent, ID: "e8cdf5d8", State: StateUnknown}, sess)
		assert.Equal(t, keep, v)
	})
	t.Run("neither listed nor in the jobs directory is gone", func(t *testing.T) {
		f := newFixture(t)
		f.listed("5d8d652c", "/cfg/workspaces/y")
		_, v := newReader(f.m, false).agent(t.Context(), "e8cdf5d8")
		assert.Equal(t, gone, v)
	})
	t.Run("kept when sessions can't be listed", func(t *testing.T) {
		f := newFixture(t)
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		sess, v := newReader(f.m, false).agent(t.Context(), "e8cdf5d8")
		assert.Equal(t, StateUnknown, sess.State)
		assert.Equal(t, keep, v)
		assert.Empty(t, f.claude.JobExistsCalls())
	})
	t.Run("kept when the jobs directory can't be checked", func(t *testing.T) {
		f := newFixture(t)
		f.claude.JobExistsFunc = func(string) (bool, error) { return false, errTest }
		sess, v := newReader(f.m, false).agent(t.Context(), "e8cdf5d8")
		assert.Equal(t, StateUnknown, sess.State)
		assert.Equal(t, keep, v)
		assert.Equal(t, []string{"kept the workspace of Claude Code session e8cdf5d8: boom"}, f.m.Warnings())
	})
}

func TestReader_describe(t *testing.T) {
	describe := func(t *testing.T, s claude.Session) *Session {
		t.Helper()
		return newReader(newFixture(t).m, false).describe(t.Context(), &s)
	}
	t.Run("working: a process, busy", func(t *testing.T) {
		got := describe(t, claude.Session{ID: "x", PID: 7, Status: "busy", State: claude.StateBlocked})
		assert.Equal(t, &Session{Kind: KindAgent, ID: "x", State: StateWorking, PID: 7}, got)
	})
	t.Run("idle: a process, not busy, whatever its state", func(t *testing.T) {
		got := describe(t, claude.Session{ID: "x", PID: 7, Status: "idle", State: claude.StateWorking})
		assert.Equal(t, StateIdle, got.State)
	})
	t.Run("starting: no process, started less than 10 s ago", func(t *testing.T) {
		got := describe(t, claude.Session{ID: "x", StartedAt: testNow().Add(-5 * time.Second)})
		assert.Equal(t, StateStarting, got.State)
	})
	t.Run("stopped: no process for longer", func(t *testing.T) {
		got := describe(t, claude.Session{ID: "x", StartedAt: testNow().Add(-time.Minute)})
		assert.Equal(t, StateStopped, got.State)
		assert.Zero(t, got.PID)
	})
	t.Run("stale only when asked", func(t *testing.T) {
		f := newFixture(t)
		f.starts[7] = testNow().Add(-2 * time.Hour)
		s := claude.Session{ID: "x", PID: 7, Status: "idle", StartedAt: testNow()}
		assert.Nil(t, newReader(f.m, false).describe(t.Context(), &s).Stale)
		got := newReader(f.m, true).describe(t.Context(), &s)
		require.NotNil(t, got.Stale)
		assert.True(t, *got.Stale)
	})
}

func TestStateOf(t *testing.T) {
	t.Run("working", func(t *testing.T) {
		assert.Equal(t, StateWorking, stateOf(&claude.Session{PID: 1, Status: "busy"}, testNow()))
	})
	t.Run("idle", func(t *testing.T) {
		assert.Equal(t, StateIdle, stateOf(&claude.Session{PID: 1, Status: "idle"}, testNow()))
	})
	t.Run("starting", func(t *testing.T) {
		assert.Equal(t, StateStarting, stateOf(&claude.Session{StartedAt: testNow().Add(-time.Second)}, testNow()))
	})
	t.Run("stopped", func(t *testing.T) {
		assert.Equal(t, StateStopped, stateOf(&claude.Session{StartedAt: testNow().Add(-time.Minute)}, testNow()))
	})
	t.Run("state isn't consulted", func(t *testing.T) {
		assert.Equal(t, StateStopped, stateOf(&claude.Session{State: claude.StateWorking, StartedAt: testNow().Add(-time.Minute)}, testNow()))
	})
}

func TestReader_staleness(t *testing.T) {
	staleness := func(t *testing.T, f *fixture, s claude.Session) *bool {
		t.Helper()
		return newReader(f.m, true).staleness(t.Context(), &s)
	}
	// running is a session whose process 7 started at started, and which
	// Claude Code says started just now, as a pre-spawned process does.
	running := func(f *fixture, started time.Time) claude.Session {
		f.starts[7] = started
		return claude.Session{ID: "x", PID: 7, Status: "idle", StartedAt: testNow()}
	}
	t.Run("a process started before the service's", func(t *testing.T) {
		f := newFixture(t)
		got := staleness(t, f, running(f, testNow().Add(-time.Hour-time.Millisecond)))
		require.NotNil(t, got)
		assert.True(t, *got)
	})
	t.Run("a process started after the service's", func(t *testing.T) {
		f := newFixture(t)
		got := staleness(t, f, running(f, testNow().Add(-time.Minute)))
		require.NotNil(t, got)
		assert.False(t, *got)
	})
	t.Run("a process started with the service's isn't stale", func(t *testing.T) {
		f := newFixture(t)
		got := staleness(t, f, running(f, testNow().Add(-time.Hour)))
		require.NotNil(t, got)
		assert.False(t, *got)
	})
	t.Run("no process isn't stale", func(t *testing.T) {
		f := newFixture(t)
		got := staleness(t, f, claude.Session{ID: "x"})
		require.NotNil(t, got)
		assert.False(t, *got)
		assert.Empty(t, f.claude.DaemonStatusCalls())
		assert.Empty(t, f.procs.StartTimeCalls())
	})
	t.Run("the process's start can't be read", func(t *testing.T) {
		f := newFixture(t)
		assert.Nil(t, staleness(t, f, claude.Session{ID: "x", PID: 7, Status: "idle"}))
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("no service running", func(t *testing.T) {
		f := newFixture(t)
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, nil }
		assert.Nil(t, staleness(t, f, running(f, testNow().Add(-2*time.Hour))))
	})
	t.Run("the service can't be read", func(t *testing.T) {
		f := newFixture(t)
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, errTest }
		assert.Nil(t, staleness(t, f, running(f, testNow().Add(-2*time.Hour))))
		assert.Len(t, f.m.Warnings(), 1)
	})
}

func TestReader_serviceStarted(t *testing.T) {
	t.Run("its process's start, read once", func(t *testing.T) {
		f := newFixture(t)
		r := newReader(f.m, true)
		started, ok := r.serviceStarted(t.Context())
		require.True(t, ok)
		assert.Equal(t, testNow().Add(-time.Hour), started)
		_, _ = r.serviceStarted(t.Context())
		assert.Len(t, f.claude.DaemonStatusCalls(), 1)
		require.Len(t, f.procs.StartTimeCalls(), 1)
		assert.Equal(t, servicePID, f.procs.StartTimeCalls()[0].Pid)
	})
	t.Run("no service", func(t *testing.T) {
		f := newFixture(t)
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, nil }
		_, ok := newReader(f.m, true).serviceStarted(t.Context())
		assert.False(t, ok)
		assert.Empty(t, f.m.Warnings())
		assert.Empty(t, f.procs.StartTimeCalls())
	})
	t.Run("its process's start can't be read", func(t *testing.T) {
		f := newFixture(t)
		delete(f.starts, servicePID)
		_, ok := newReader(f.m, true).serviceStarted(t.Context())
		assert.False(t, ok)
		require.Len(t, f.m.Warnings(), 1)
		assert.Contains(t, f.m.Warnings()[0], "49096")
	})
	t.Run("a failure is reported once", func(t *testing.T) {
		f := newFixture(t)
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, errTest }
		r := newReader(f.m, true)
		_, ok := r.serviceStarted(t.Context())
		assert.False(t, ok)
		_, _ = r.serviceStarted(t.Context())
		assert.Len(t, f.m.Warnings(), 1)
	})
}

func TestReader_agents(t *testing.T) {
	t.Run("lists every background session once", func(t *testing.T) {
		f := newFixture(t)
		f.listed("e8cdf5d8", "/cfg/workspaces/x")
		r := newReader(f.m, false)
		got, err := r.agents(t.Context())
		require.NoError(t, err)
		assert.Equal(t, f.sessions, got)
		_, _ = r.agents(t.Context())
		require.Len(t, f.claude.AgentsCalls(), 1)
		assert.Equal(t, claude.ListOptions{All: true}, f.claude.AgentsCalls()[0].Opts)
	})
	t.Run("a failure is reported once", func(t *testing.T) {
		f := newFixture(t)
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		r := newReader(f.m, false)
		_, err := r.agents(t.Context())
		require.ErrorIs(t, err, errTest)
		_, err = r.agents(t.Context())
		require.ErrorIs(t, err, errTest)
		assert.Equal(t, []string{"couldn't list Claude Code sessions, so no agent or abandoned workspace was removed: boom"}, f.m.Warnings())
	})
}
