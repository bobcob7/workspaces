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

// limited makes session id's turn end at a five-hour limit that reset at
// resetsAt.
func (f *fixture) limited(id string, resetsAt time.Time) *claude.Limit {
	l := &claude.Limit{Type: "five_hour", ResetsAt: resetsAt}
	f.limits[id] = l
	return l
}

// sent is the session ids Send was called with.
func (f *fixture) sent() []string {
	var ids []string
	for _, c := range f.claude.SendCalls() {
		ids = append(ids, c.S.ID)
	}
	return ids
}

func TestManager_Continue(t *testing.T) {
	t.Run("continues idle agents whose limit has reset and leaves the rest", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		due := f.agent("due", "s1")
		early := f.agent("early", "s2")
		plain := f.agent("plain", "s3")
		busy := f.agent("busy", "s4")
		f.shell("sh")
		f.listed("s1", "/cfg/workspaces/due")
		f.listed("s2", "/cfg/workspaces/early")
		f.listed("s3", "/cfg/workspaces/plain")
		f.working("s4", "/cfg/workspaces/busy")
		dueLimit := f.limited("s1", testNow().Add(-2*time.Minute))
		earlyLimit := f.limited("s2", testNow().Add(time.Hour))
		f.limited("s4", testNow().Add(-time.Hour))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []Continued{
			{Workspace: due, SessionID: "s1", State: StateIdle, Limit: dueLimit, Action: ActionContinued},
			{Workspace: early, SessionID: "s2", State: StateIdle, Limit: earlyLimit, Action: ActionWaiting},
			{Workspace: plain, SessionID: "s3", State: StateIdle},
			{Workspace: busy, SessionID: "s4", State: StateWorking},
		}, got)
		assert.Equal(t, []string{"s1"}, f.sent())
		assert.Equal(t, ContinueMessage, f.claude.SendCalls()[0].Message)
	})
	t.Run("waits a minute past the reset", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.limited("s1", testNow().Add(-30*time.Second))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, ActionWaiting, got[0].Action)
		assert.Empty(t, f.sent())
	})
	t.Run("sends nothing while the machine isn't fully awake", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.asleep = true
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.listed("s1", "/cfg/workspaces/a")
		f.listed("s2", "/cfg/workspaces/b")
		f.limited("s1", testNow().Add(-time.Hour))
		f.limited("s2", testNow().Add(-time.Hour))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, ActionAsleep, got[0].Action)
		assert.Equal(t, ActionAsleep, got[1].Action)
		assert.Empty(t, f.sent())
		assert.Len(t, f.power.AwakeCalls(), 1)
	})
	t.Run("doesn't read the power state when nothing is due", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.limited("s1", testNow().Add(time.Hour))
		_, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Empty(t, f.power.AwakeCalls())
	})
	t.Run("sends nothing and warns when the power state can't be read", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.power.AwakeFunc = func(context.Context) (bool, error) { return false, errTest }
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.limited("s1", testNow().Add(-time.Hour))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, ActionAsleep, got[0].Action)
		assert.Empty(t, f.sent())
		assert.Len(t, f.m.Warnings(), 1)
	})
	t.Run("waits five minutes after a try", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.listed("s1", "/cfg/workspaces/a")
		f.listed("s2", "/cfg/workspaces/b")
		recent := f.limited("s1", testNow().Add(-time.Hour))
		recent.Tries, recent.LastTry = 1, testNow().Add(-4*time.Minute)
		older := f.limited("s2", testNow().Add(-time.Hour))
		older.Tries, older.LastTry = 2, testNow().Add(-5*time.Minute)
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, ActionWaiting, got[0].Action)
		assert.Equal(t, ActionContinued, got[1].Action)
		assert.Equal(t, []string{"s2"}, f.sent())
	})
	t.Run("gives up after three tries", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		l := f.limited("s1", testNow().Add(-time.Hour))
		l.Tries, l.LastTry = 3, testNow().Add(-time.Hour)
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, ActionGaveUp, got[0].Action)
		assert.Empty(t, f.sent())
	})
	t.Run("leaves stopped and unlisted sessions alone", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		stopped := f.agent("stopped", "s1")
		gone := f.agent("gone", "s2")
		f.stopped("s1", "/cfg/workspaces/stopped")
		f.limited("s1", testNow().Add(-time.Hour))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		assert.Equal(t, []Continued{
			{Workspace: stopped, SessionID: "s1", State: StateStopped},
			{Workspace: gone, SessionID: "s2", State: StateUnknown},
		}, got)
		assert.Empty(t, f.claude.UsageLimitCalls())
	})
	t.Run("refuses a session listed in another directory", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/elsewhere")
		f.limited("s1", testNow().Add(-time.Hour))
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		require.ErrorIs(t, got[0].Err, ErrSessionElsewhere)
		assert.Empty(t, f.sent())
	})
	t.Run("warns and moves on when a transcript can't be read", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.listed("s1", "/cfg/workspaces/a")
		f.listed("s2", "/cfg/workspaces/b")
		f.limited("s2", testNow().Add(-time.Hour))
		f.claude.UsageLimitFunc = func(s *claude.Session, _ string) (*claude.Limit, error) {
			if s.ID == "s1" {
				return nil, errTest
			}
			return f.limits[s.ID], nil
		}
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		require.NoError(t, got[0].Err)
		assert.Nil(t, got[0].Limit)
		assert.Equal(t, ActionContinued, got[1].Action)
		assert.Len(t, f.m.Warnings(), 1)
	})
	t.Run("fails only the workspace whose message isn't delivered", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.agent("b", "s2")
		f.listed("s1", "/cfg/workspaces/a")
		f.listed("s2", "/cfg/workspaces/b")
		f.limited("s1", testNow().Add(-time.Hour))
		f.limited("s2", testNow().Add(-time.Hour))
		f.claude.SendFunc = func(_ context.Context, s *claude.Session, _ string) error {
			if s.ID == "s1" {
				return claude.ErrNotDelivered
			}
			return nil
		}
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.NoError(t, err)
		require.ErrorIs(t, got[0].Err, claude.ErrNotDelivered)
		assert.Empty(t, got[0].Action)
		assert.Equal(t, ActionContinued, got[1].Action)
	})
	t.Run("a dry run sends nothing and takes no lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		f.limited("s1", testNow().Add(-time.Hour))
		unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, continueLockName))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		got, err := f.m.Continue(t.Context(), ContinueOptions{All: true, DryRun: true})
		require.NoError(t, err)
		assert.Equal(t, ActionWouldContinue, got[0].Action)
		assert.Empty(t, f.sent())
	})
	t.Run("waits for another pass's lock", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.listed("s1", "/cfg/workspaces/a")
		unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, continueLockName))
		require.NoError(t, err)
		require.True(t, ok)
		defer unlock()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err = f.m.Continue(ctx, ContinueOptions{All: true})
		require.ErrorIs(t, err, context.Canceled)
		assert.Empty(t, f.claude.AgentsCalls())
	})
	t.Run("fails when sessions can't be listed", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.agent("a", "s1")
		f.claude.AgentsFunc = func(context.Context, claude.ListOptions) ([]claude.Session, error) { return nil, errTest }
		_, err := f.m.Continue(t.Context(), ContinueOptions{All: true})
		require.ErrorIs(t, err, errTest)
	})
	t.Run("a named shell workspace is an error, without listing sessions", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		sh := f.shell("sh")
		got, err := f.m.Continue(t.Context(), ContinueOptions{Workspaces: []workspace.Workspace{sh}})
		require.NoError(t, err)
		require.ErrorIs(t, got[0].Err, ErrNotAgent)
		assert.Empty(t, f.claude.AgentsCalls())
	})
}

func TestDue(t *testing.T) {
	now := testNow()
	t.Run("not before a minute past the reset", func(t *testing.T) {
		assert.False(t, due(&claude.Limit{ResetsAt: now.Add(-59 * time.Second)}, now))
		assert.True(t, due(&claude.Limit{ResetsAt: now.Add(-time.Minute)}, now))
	})
	t.Run("not within five minutes of the last try", func(t *testing.T) {
		reset := now.Add(-time.Hour)
		assert.False(t, due(&claude.Limit{ResetsAt: reset, Tries: 1, LastTry: now.Add(-299 * time.Second)}, now))
		assert.True(t, due(&claude.Limit{ResetsAt: reset, Tries: 1, LastTry: now.Add(-5 * time.Minute)}, now))
	})
}
