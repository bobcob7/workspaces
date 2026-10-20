package lifetime

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

var errTest = errors.New("boom")

// testNow is the fixture's clock.
func testNow() time.Time {
	return time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
}

// fixture is a Manager whose store and claude client are mocks backed by
// the fixture's fields: workspaces under /cfg/workspaces, their session
// records, which are locked or abandoned, and Claude Code's sessions and
// jobs directories. Everything else succeeds.
type fixture struct {
	m          *Manager
	store      *workspaceStoreMock
	claude     *claudeClientMock
	workspaces []workspace.Workspace
	records    map[string]workspace.Session
	locked     map[string]bool
	abandoned  map[string]bool
	sessions   []claude.Session
	jobs       map[string]bool
	procs      *processReaderMock
	// starts is when each process started, by pid; servicePID is the
	// background service's, which started an hour before testNow.
	starts   map[int]time.Time
	stateDir string
	// service is the running background service's pid, servicePID until
	// the fixture's claude stops it, or 0 for none.
	service int
	// clock is the Manager's now, testNow until sleeps advance it.
	clock time.Time
	// sleeps is every wait the Manager made, which advance clock.
	sleeps []time.Duration
}

// servicePID is the fixture's background service process.
const servicePID = 49096

// newServicePID is the service process `claude daemon run` starts.
const newServicePID = 51515

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		records:   map[string]workspace.Session{},
		locked:    map[string]bool{},
		abandoned: map[string]bool{},
		jobs:      map[string]bool{},
		starts:    map[int]time.Time{servicePID: testNow().Add(-time.Hour)},
		stateDir:  t.TempDir(),
		service:   servicePID,
	}
	f.store = &workspaceStoreMock{
		CleanLeftoversFunc: func() []string { return nil },
		ListFunc:           func() ([]workspace.Workspace, error) { return f.workspaces, nil },
		UnknownFunc:        func() ([]string, error) { return nil, nil },
		SessionFunc: func(id string) (workspace.Session, error) {
			if sess, ok := f.records[id]; ok {
				return sess, nil
			}
			return workspace.Session{}, workspace.ErrNoSession
		},
		LockedFunc:     func(id string) (bool, error) { return f.locked[id], nil },
		AbandonedFunc:  func(id string) (bool, error) { return f.abandoned[id], nil },
		GuardedDirFunc: func(id string) (string, error) { return "/cfg/workspaces/" + id, nil },
		RemoveFunc:     func(string, workspace.RemoveOptions) error { return nil },
	}
	f.claude = &claudeClientMock{
		AgentsFunc: func(context.Context, claude.ListOptions) ([]claude.Session, error) { return f.sessions, nil },
		StopFunc:   func(context.Context, string) error { return nil },
		RemoveFunc: func(context.Context, string) error { return nil },
		RespawnFunc: func(_ context.Context, id string) error {
			f.giveProcess(id)
			return nil
		},
		JobExistsFunc: func(id string) (bool, error) {
			return f.jobs[id], nil
		},
		DaemonStatusFunc: func(context.Context) (claude.DaemonStatus, error) {
			if f.service == 0 {
				return claude.DaemonStatus{}, nil
			}
			return claude.DaemonStatus{Running: true, PID: f.service, Uptime: time.Hour}, nil
		},
		StopDaemonFunc: func(context.Context) error {
			f.service = 0
			return nil
		},
		RunDaemonFunc: func(context.Context) (int, error) {
			f.service = newServicePID
			return newServicePID, nil
		},
	}
	f.procs = &processReaderMock{StartTimeFunc: func(pid int) (time.Time, error) {
		if started, ok := f.starts[pid]; ok {
			return started, nil
		}
		return time.Time{}, errTest
	}}
	f.m = New(slog.New(slog.NewJSONHandler(io.Discard, nil)), f.store, f.claude, f.procs, f.stateDir)
	f.clock = testNow()
	f.m.now = func() time.Time { return f.clock }
	f.m.sleep = func(_ context.Context, d time.Duration) error {
		f.sleeps = append(f.sleeps, d)
		f.clock = f.clock.Add(d)
		return nil
	}
	return f
}

// giveProcess makes Claude Code list session id with a new process,
// respawnedPID, idle, as a respawn does.
func (f *fixture) giveProcess(id string) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			f.sessions[i].PID, f.sessions[i].Status = respawnedPID, "idle"
		}
	}
}

// add adds workspace id under /cfg/workspaces.
func (f *fixture) add(id string) workspace.Workspace {
	ws := workspace.Workspace{Version: 1, ID: id, Path: "/cfg/workspaces/" + id, CreatedAt: testNow().Add(-time.Hour)}
	f.workspaces = append(f.workspaces, ws)
	return ws
}

// agent adds workspace id with an agent session.
func (f *fixture) agent(id, session string) workspace.Workspace {
	f.records[id] = workspace.Session{Kind: workspace.SessionClaude, ClaudeSessionID: session}
	return f.add(id)
}

// shell adds workspace id with a shell session.
func (f *fixture) shell(id string) workspace.Workspace {
	f.records[id] = workspace.Session{Kind: workspace.SessionShell}
	return f.add(id)
}

// listedPID is the process of every session listed.
const listedPID = 4242

// respawnedPID is the process a respawn gives a session.
const respawnedPID = 5151

// listed makes Claude Code list a session with a process in dir, started
// after the background service.
func (f *fixture) listed(id, dir string) {
	f.sessions = append(f.sessions, claude.Session{ID: id, Cwd: dir, PID: listedPID, Status: "idle", StartedAt: testNow().Add(-time.Minute)})
	f.starts[listedPID] = testNow().Add(-time.Minute)
}

// working makes Claude Code list a session with a process in dir, in the
// middle of a turn.
func (f *fixture) working(id, dir string) {
	f.listed(id, dir)
	f.sessions[len(f.sessions)-1].Status = "busy"
}

// setStatus sets the status of listed session id.
func (f *fixture) setStatus(id, status string) {
	for i := range f.sessions {
		if f.sessions[i].ID == id {
			f.sessions[i].Status = status
		}
	}
}

// unlist makes Claude Code no longer list session id.
func (f *fixture) unlist(id string) {
	f.sessions = slices.DeleteFunc(f.sessions, func(s claude.Session) bool { return s.ID == id })
}

// respawnLocked reports whether the respawn lock is held.
func (f *fixture) respawnLocked(t *testing.T) bool {
	t.Helper()
	unlock, ok, err := lockfile.TryExclusive(filepath.Join(f.stateDir, respawnLockName))
	require.NoError(t, err)
	if ok {
		unlock()
	}
	return !ok
}

// stopped makes Claude Code list a session in dir with no process,
// created long enough ago that it isn't starting.
func (f *fixture) stopped(id, dir string) {
	f.sessions = append(f.sessions, claude.Session{ID: id, Cwd: dir, StartedAt: testNow().Add(-time.Hour)})
}

// respawned is the session ids passed to claude respawn.
func (f *fixture) respawned() []string {
	var ids []string
	for _, c := range f.claude.RespawnCalls() {
		ids = append(ids, c.ID)
	}
	return ids
}

// removedIDs is the ids of the workspaces removed through the store.
func (f *fixture) removedIDs() []string {
	var ids []string
	for _, c := range f.store.RemoveCalls() {
		ids = append(ids, c.ID)
	}
	return ids
}

// claudeRemoved is the session ids passed to claude rm.
func (f *fixture) claudeRemoved() []string {
	var ids []string
	for _, c := range f.claude.RemoveCalls() {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestNew(t *testing.T) {
	store, c, procs := &workspaceStoreMock{}, &claudeClientMock{}, &processReaderMock{}
	m := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), store, c, procs, "/cfg/state")
	assert.Same(t, store, m.store)
	assert.Same(t, c, m.claude)
	assert.Same(t, procs, m.procs)
	assert.Equal(t, "/cfg/state/.removal-pass", m.stamp)
	assert.NotNil(t, m.now)
}

func TestManager_Warnings(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		assert.Empty(t, newFixture(t).m.Warnings())
	})
	t.Run("in order, as a copy", func(t *testing.T) {
		f := newFixture(t)
		f.m.warn("one %d", 1)
		f.m.warn("two")
		got := f.m.Warnings()
		got[0] = "changed"
		assert.Equal(t, []string{"one 1", "two"}, f.m.Warnings())
	})
}

func TestSleep(t *testing.T) {
	t.Run("returns after the duration", func(t *testing.T) {
		assert.NoError(t, sleep(t.Context(), 0))
	})
	t.Run("returns when the context is done", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.ErrorIs(t, sleep(ctx, time.Hour), context.Canceled)
	})
}
