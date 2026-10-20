package lifetime

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// trace records, in order, each claude command the Manager runs, keeping
// the fixture's behaviour.
func (f *fixture) trace() *[]string {
	var calls []string
	agents, stop, run, status, respawn := f.claude.AgentsFunc, f.claude.StopDaemonFunc, f.claude.RunDaemonFunc, f.claude.DaemonStatusFunc, f.claude.RespawnFunc
	f.claude.AgentsFunc = func(ctx context.Context, opts claude.ListOptions) ([]claude.Session, error) {
		calls = append(calls, "agents")
		return agents(ctx, opts)
	}
	f.claude.StopDaemonFunc = func(ctx context.Context) error {
		calls = append(calls, "daemon stop")
		return stop(ctx)
	}
	f.claude.RunDaemonFunc = func(ctx context.Context) (int, error) {
		calls = append(calls, "daemon run")
		return run(ctx)
	}
	f.claude.DaemonStatusFunc = func(ctx context.Context) (claude.DaemonStatus, error) {
		calls = append(calls, "daemon status")
		return status(ctx)
	}
	f.claude.RespawnFunc = func(ctx context.Context, id string) error {
		calls = append(calls, "respawn "+id)
		return respawn(ctx, id)
	}
	return &calls
}

func TestManager_Restart(t *testing.T) {
	t.Run("replaces the service, then respawns every running session, ws's or not", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.listed("s2", "/elsewhere")
		f.stopped("s3", "/cfg/workspaces/b")
		calls := f.trace()
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{
			{SessionID: "s1", Workspace: "a", Restarted: true},
			{SessionID: "s2", Restarted: true},
		}, got)
		assert.Equal(t, []string{"agents", "daemon status", "daemon stop", "daemon run", "daemon status", "respawn s1", "agents", "respawn s2", "agents"}, *calls)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("holds the respawn lock from the listing to the last respawn's process", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.listed("s1", "/x")
		var held []bool
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			held = append(held, f.respawnLocked(t))
			return f.sessions, nil
		}
		_, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []bool{true, true}, held)
		assert.False(t, f.respawnLocked(t))
	})
	t.Run("waits for another holder of the respawn lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, respawnLockName))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = f.m.Restart(ctx, RestartOptions{})
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, f.claude.AgentsCalls())
		assert.Empty(t, f.claude.StopDaemonCalls())
	})
	t.Run("a failed listing changes nothing", func(t *testing.T) {
		f := newFixture(t)
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.ErrorIs(t, err, errTest)
		assert.Nil(t, got)
		assert.Empty(t, f.claude.StopDaemonCalls())
	})
	t.Run("interrupted before the stop, changes nothing", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		ctx, cancel := context.WithCancel(t.Context())
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) {
			cancel()
			return f.sessions, nil
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.ErrorIs(t, err, context.Canceled)
		assert.Nil(t, got)
		assert.Empty(t, f.claude.StopDaemonCalls())
	})
	t.Run("interrupted during the stop, still starts the service, then leaves the sessions stale", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		ctx, cancel := context.WithCancel(t.Context())
		f.claude.StopDaemonFunc = func(context.Context) error {
			cancel()
			return nil
		}
		var runCtxErr, statusCtxErr error
		run := f.claude.RunDaemonFunc
		f.claude.RunDaemonFunc = func(ctx context.Context) (int, error) {
			runCtxErr = ctx.Err()
			return run(ctx)
		}
		status := f.claude.DaemonStatusFunc
		f.claude.DaemonStatusFunc = func(ctx context.Context) (claude.DaemonStatus, error) {
			statusCtxErr = ctx.Err()
			return status(ctx)
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.NoError(t, err)
		require.Len(t, f.claude.RunDaemonCalls(), 1)
		assert.NoError(t, runCtxErr)
		assert.NoError(t, statusCtxErr)
		assert.Empty(t, f.respawned())
		require.Len(t, got, 1)
		require.ErrorIs(t, got[0].Err, context.Canceled)
		require.EqualError(t, got[0].Err, "interrupted before session s1 was restarted, so it keeps the old environment: run ws restart again: context canceled")
		assert.False(t, got[0].Restarted)
	})
	t.Run("a respawn isn't cut short by an interruption", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		ctx, cancel := context.WithCancel(t.Context())
		var respawnCtxErr error
		f.claude.RespawnFunc = func(ctx context.Context, id string) error {
			cancel()
			respawnCtxErr = ctx.Err()
			f.giveProcess(id)
			return nil
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.NoError(t, err)
		assert.NoError(t, respawnCtxErr)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
	})
	t.Run("waits for the new service before respawning", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		calls := f.trace()
		statuses := 0
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) {
			*calls = append(*calls, "daemon status")
			statuses++
			if statuses < 4 {
				return claude.DaemonStatus{}, nil
			}
			return claude.DaemonStatus{Running: true, PID: newServicePID}, nil
		}
		_, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"agents", "daemon status", "daemon stop", "daemon run", "daemon status", "daemon status", "daemon status", "respawn s1", "agents"}, *calls)
		assert.Equal(t, []time.Duration{respawnPoll, respawnPoll}, f.sleeps)
	})
	t.Run("warns when another claude started the service first", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.RunDaemonFunc = func(context.Context) (int, error) {
			f.service = 777
			return newServicePID, nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Equal(t, []string{fmt.Sprintf("Claude Code's background service (pid 777) isn't the one ws started (pid %d): another claude started one first, so it may have that process's environment; run ws restart again", newServicePID)}, f.m.Warnings())
	})
	t.Run("ignores the old service while it waits for its own", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.StopDaemonFunc = func(context.Context) error { return nil }
		f.claude.RunDaemonFunc = func(context.Context) (int, error) {
			f.m.sleep = func(_ context.Context, d time.Duration) error {
				f.sleeps = append(f.sleeps, d)
				f.service = newServicePID
				return nil
			}
			return newServicePID, nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Len(t, f.claude.DaemonStatusCalls(), 3)
		assert.Equal(t, []time.Duration{respawnPoll}, f.sleeps)
		assert.Empty(t, f.m.Warnings())
	})
	t.Run("an old service that never exits fails like a failed claude daemon run", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.StopDaemonFunc = func(context.Context) error { return nil }
		f.claude.RunDaemonFunc = func(context.Context) (int, error) { return newServicePID, nil }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		require.Len(t, f.m.Warnings(), 1)
		assert.Contains(t, f.m.Warnings()[0], fmt.Sprintf("claude daemon status reports only the old service (pid %d)", servicePID))
	})
	t.Run("a failed claude daemon run still respawns the idle sessions and leaves working ones stale", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.working("s2", "/y")
		f.claude.RunDaemonFunc = func(context.Context) (int, error) { return 0, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, Restarted{SessionID: "s1", Restarted: true}, got[0])
		require.ErrorIs(t, got[1].Err, ErrServiceFailed)
		require.EqualError(t, got[1].Err, "couldn't replace Claude Code's background service, so session s2, which was working, was left stale: run ws restart again")
		assert.Equal(t, []string{"s1"}, f.respawned())
		assert.Empty(t, f.sleeps)
		assert.Len(t, f.claude.RunDaemonCalls(), 1, "a respawn started a service, so claude daemon run isn't tried again")
		assert.Equal(t, []string{"couldn't replace Claude Code's background service: `claude daemon run` failed: boom; ws respawned the sessions that weren't working, which starts a service if none is running"}, f.m.Warnings())
	})
	t.Run("a failed claude daemon run respawns even once interrupted, until one respawn succeeds", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.listed("s2", "/y")
		f.listed("s3", "/z")
		ctx, cancel := context.WithCancel(t.Context())
		f.claude.RunDaemonFunc = func(context.Context) (int, error) {
			cancel()
			return 0, errTest
		}
		f.claude.RespawnFunc = func(_ context.Context, id string) error {
			if id == "s1" {
				return errTest
			}
			f.giveProcess(id)
			return nil
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []string{"s1", "s2"}, f.respawned())
		require.Len(t, got, 3)
		assert.True(t, got[1].Restarted)
		assert.ErrorIs(t, got[2].Err, context.Canceled)
	})
	t.Run("a service that didn't start twice with no running session fails", func(t *testing.T) {
		f := newFixture(t)
		f.stopped("s1", "/x")
		f.claude.RunDaemonFunc = func(context.Context) (int, error) { return 0, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.ErrorIs(t, err, ErrServiceFailed)
		require.ErrorIs(t, err, errTest)
		assert.Nil(t, got)
		assert.Len(t, f.claude.RunDaemonCalls(), 2)
	})
	t.Run("with no respawn succeeding, tries claude daemon run again, then waits for the working sessions", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		run := f.claude.RunDaemonFunc
		f.claude.RunDaemonFunc = func(ctx context.Context) (int, error) {
			if len(f.claude.RunDaemonCalls()) == 1 {
				return 0, errTest
			}
			return run(ctx)
		}
		agents := f.claude.AgentsFunc
		f.claude.AgentsFunc = func(ctx context.Context, opts claude.ListOptions) ([]claude.Session, error) {
			if len(f.sleeps) > 0 {
				f.setStatus("s1", "idle")
			}
			return agents(ctx, opts)
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Len(t, f.claude.RunDaemonCalls(), 2)
		assert.Equal(t, []string{"couldn't replace Claude Code's background service: `claude daemon run` failed: boom; ws started one on the second try"}, f.m.Warnings())
	})
	t.Run("a second failed claude daemon run leaves the working sessions stale and warns that they stop", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.listed("s2", "/y")
		f.claude.RespawnFunc = func(context.Context, string) error { return errTest }
		f.claude.RunDaemonFunc = func(context.Context) (int, error) { return 0, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.EqualError(t, got[0].Err, "respawning Claude Code session s2: boom")
		require.ErrorIs(t, got[1].Err, ErrServiceFailed)
		assert.Len(t, f.claude.RunDaemonCalls(), 2)
		assert.Equal(t, []string{"couldn't replace Claude Code's background service: `claude daemon run` failed: boom; tried again: couldn't replace Claude Code's background service: `claude daemon run` failed: boom; no background service may be running, and background sessions stop within about a minute unless one is started: run ws restart again, or `claude respawn <id>` on any session"}, f.m.Warnings())
	})
	t.Run("a service that never shows up fails like a failed claude daemon run", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, nil }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Len(t, f.claude.DaemonStatusCalls(), 1+int(serviceWait/respawnPoll)+1)
		require.Len(t, f.m.Warnings(), 1)
		assert.Contains(t, f.m.Warnings()[0], "didn't start a service within 10s: claude daemon status reports no service running")
	})
	t.Run("a failed stop while the old service runs restarts nothing", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.StopDaemonFunc = func(context.Context) error { return errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.ErrorIs(t, err, ErrServiceFailed)
		require.ErrorIs(t, err, errTest)
		assert.Nil(t, got)
		assert.Empty(t, f.claude.RunDaemonCalls())
		assert.Empty(t, f.respawned())
	})
	t.Run("a failed stop with no service running goes on, with a warning", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.service = 0
		f.claude.StopDaemonFunc = func(context.Context) error { return errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Equal(t, []string{"`claude daemon stop` failed, but no background service was running, so ws started one: boom"}, f.m.Warnings())
	})
	t.Run("a failed stop and status start a service in case, and restart nothing", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.StopDaemonFunc = func(context.Context) error { return errTest }
		statuses := 0
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) {
			statuses++
			if statuses <= 2 {
				return claude.DaemonStatus{}, errTest
			}
			return claude.DaemonStatus{Running: true, PID: newServicePID}, nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.ErrorIs(t, err, ErrServiceFailed)
		assert.Nil(t, got)
		assert.Len(t, f.claude.RunDaemonCalls(), 1)
		assert.Empty(t, f.respawned())
		assert.Contains(t, err.Error(), "so no session was restarted, and ws started a service in case none was running")
	})
	t.Run("a failed stop, status, and run say that no service was started", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.StopDaemonFunc = func(context.Context) error { return errTest }
		f.claude.DaemonStatusFunc = func(context.Context) (claude.DaemonStatus, error) { return claude.DaemonStatus{}, errTest }
		f.claude.RunDaemonFunc = func(context.Context) (int, error) { return 0, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.ErrorIs(t, err, ErrServiceFailed)
		assert.Nil(t, got)
		assert.Empty(t, f.respawned())
		assert.Contains(t, err.Error(), "ws couldn't start a service in case none was running")
	})
	t.Run("respawns a working session once it stops working", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.working("s2", "/y")
		calls := f.trace()
		agents := f.claude.AgentsFunc
		f.claude.AgentsFunc = func(ctx context.Context, opts claude.ListOptions) ([]claude.Session, error) {
			if len(f.sleeps) == 3 {
				f.setStatus("s2", "idle")
			}
			return agents(ctx, opts)
		}
		var waiting []Restarted
		got, err := f.m.Restart(t.Context(), RestartOptions{Waiting: func(s []Restarted) { waiting = s }})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s2", Working: true}}, waiting)
		assert.Equal(t, []Restarted{
			{SessionID: "s1", Restarted: true},
			{SessionID: "s2", Restarted: true},
		}, got)
		assert.Equal(t, []string{"agents", "daemon status", "daemon stop", "daemon run", "daemon status", "respawn s1", "agents", "agents", "agents", "agents", "respawn s2", "agents"}, *calls)
		assert.Equal(t, []time.Duration{workingPoll, workingPoll, workingPoll}, f.sleeps)
	})
	t.Run("--force respawns working sessions at once", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.listed("s2", "/y")
		got, err := f.m.Restart(t.Context(), RestartOptions{Force: true, Waiting: func([]Restarted) { t.Error("waited") }})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{
			{SessionID: "s1", Working: true, Restarted: true},
			{SessionID: "s2", Restarted: true},
		}, got)
		assert.Equal(t, []string{"s1", "s2"}, f.respawned())
	})
	t.Run("--no-wait leaves working sessions stale", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.listed("s2", "/y")
		got, err := f.m.Restart(t.Context(), RestartOptions{NoWait: true, Waiting: func([]Restarted) { t.Error("waited") }})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{
			{SessionID: "s2", Restarted: true},
			{SessionID: "s1", Working: true},
		}, got)
		assert.Equal(t, []string{"s2"}, f.respawned())
		assert.NotContains(t, f.sleeps, workingPoll)
	})
	t.Run("a working session that goes away is gone", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.m.sleep = func(context.Context, time.Duration) error {
			f.unlist("s1")
			return nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Working: true, Gone: true}}, got)
		assert.Empty(t, f.respawned())
	})
	t.Run("a working session that lost its process is respawned", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.m.sleep = func(context.Context, time.Duration) error {
			f.sessions[0].PID, f.sessions[0].Status = 0, ""
			return nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
	})
	t.Run("a failed listing while waiting fails the sessions left", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		f.m.sleep = func(context.Context, time.Duration) error {
			f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
			return nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.ErrorIs(t, got[0].Err, errTest)
		require.EqualError(t, got[0].Err, "listing Claude Code sessions while waiting for session s1 to stop working: boom")
		assert.False(t, got[0].Restarted)
	})
	t.Run("interrupted while waiting, leaves the rest stale and releases the lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.working("s1", "/x")
		ctx, cancel := context.WithCancel(t.Context())
		f.m.sleep = func(context.Context, time.Duration) error {
			cancel()
			return context.Canceled
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.ErrorIs(t, got[0].Err, context.Canceled)
		assert.False(t, got[0].Restarted)
		assert.Empty(t, f.respawned())
		assert.False(t, f.respawnLocked(t))
	})
	t.Run("a listing cut short by an interruption while waiting leaves the rest stale", func(t *testing.T) {
		f := newFixture(t)
		f.working("s1", "/x")
		ctx, cancel := context.WithCancel(t.Context())
		f.m.sleep = func(context.Context, time.Duration) error {
			cancel()
			f.claude.AgentsFunc = func(ctx context.Context, _ claude.ListOptions) ([]claude.Session, error) { return nil, ctx.Err() }
			return nil
		}
		got, err := f.m.Restart(ctx, RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.ErrorIs(t, got[0].Err, context.Canceled)
		assert.Contains(t, got[0].Err.Error(), "interrupted before session s1 was restarted")
		assert.Empty(t, f.respawned())
	})
	t.Run("a failed respawn fails only its session", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.listed("s2", "/y")
		f.claude.RespawnFunc = func(_ context.Context, id string) error {
			if id == "s1" {
				return errTest
			}
			f.giveProcess(id)
			return nil
		}
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		require.Len(t, got, 2)
		require.EqualError(t, got[0].Err, "respawning Claude Code session s1: boom")
		assert.Equal(t, Restarted{SessionID: "s2", Restarted: true}, got[1])
	})
	t.Run("a session deleted before its respawn is gone", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.RespawnFunc = func(context.Context, string) error { return claude.ErrNoSession }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Gone: true}}, got)
	})
	t.Run("warns when a respawned session keeps its old process", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.claude.RespawnFunc = func(context.Context, string) error { return nil }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Len(t, f.m.Warnings(), 1)
	})
	t.Run("reports the sessions before the stop, and each outcome in order", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.working("s2", "/y")
		var events []string
		var recorded []Restarted
		f.claude.StopDaemonFunc = func(context.Context) error {
			events = append(events, "stop")
			return nil
		}
		_, err := f.m.Restart(t.Context(), RestartOptions{
			NoWait: true,
			Recorded: func(s []Restarted) {
				events = append(events, "recorded")
				recorded = s
			},
			Done: func(s Restarted) { events = append(events, "done "+s.SessionID) },
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"recorded", "stop", "done s1", "done s2"}, events)
		assert.Equal(t, []Restarted{{SessionID: "s1", Workspace: "a"}, {SessionID: "s2", Working: true}}, recorded)
	})
	t.Run("a session listed outside its workspace shows no workspace", func(t *testing.T) {
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/elsewhere")
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
	})
	t.Run("shell workspaces and unreadable records name no session", func(t *testing.T) {
		f := newFixture(t)
		f.shell("sh")
		f.add("unrecorded")
		f.listed("s1", "/x")
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
	})
	t.Run("unreadable workspaces: warns, and still restarts", func(t *testing.T) {
		f := newFixture(t)
		f.listed("s1", "/x")
		f.store.ListFunc = func() ([]workspace.Workspace, error) { return nil, errTest }
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Equal(t, []Restarted{{SessionID: "s1", Restarted: true}}, got)
		assert.Equal(t, []string{"couldn't read the workspaces, so no session is shown with its workspace: boom"}, f.m.Warnings())
	})
	t.Run("with no running session, still replaces the service", func(t *testing.T) {
		f := newFixture(t)
		f.stopped("s1", "/x")
		got, err := f.m.Restart(t.Context(), RestartOptions{})
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Len(t, f.claude.StopDaemonCalls(), 1)
		assert.Len(t, f.claude.RunDaemonCalls(), 1)
		assert.Empty(t, f.respawned())
	})
}
