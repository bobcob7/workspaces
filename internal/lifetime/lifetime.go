// Package lifetime keeps each workspace exactly as long as its session:
// it removes workspaces whose session is gone, reads each workspace's
// session for ws list, removes workspaces with their sessions for ws rm,
// and respawns agents that aren't running for ws restore (ADRs 0015,
// 0023, 0024, 0025, 0027).
package lifetime

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// Manager decides whether workspaces' sessions are gone, and removes
// them.
type Manager struct {
	log    *slog.Logger
	store  workspaceStore
	claude claudeClient
	procs  processReader
	// stamp is the file whose modification time is when the removal pass
	// last ran.
	stamp string
	// respawnLock is the file ws restore and ws restart lock while they
	// respawn sessions.
	respawnLock string
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) error
	mu          sync.Mutex
	warnings    []string
}

// New returns a Manager for the workspaces in store, whose state is under
// stateDir, reading their agents' sessions with c and their processes'
// start times with procs.
func New(log *slog.Logger, store workspaceStore, c claudeClient, procs processReader, stateDir string) *Manager {
	return &Manager{
		log:         log,
		store:       store,
		claude:      c,
		procs:       procs,
		stamp:       filepath.Join(stateDir, stampName),
		respawnLock: filepath.Join(stateDir, respawnLockName),
		now:         time.Now,
		sleep:       sleep,
	}
}

// sleep waits for d, or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// stampName is the removal pass's timestamp file in the state directory.
// It is hidden, so it's never taken for a workspace's state.
const stampName = ".removal-pass"

// respawnLockName is the respawn lock's file in the state directory, taken
// by ws restore and ws restart (ADR 0027). It is hidden, so it's never
// taken for a workspace's state.
const respawnLockName = ".respawn.lock"

// Kind is what kind of session a workspace has, as ws list shows it.
type Kind string

// Session kinds.
const (
	KindAgent Kind = "agent"
	KindShell Kind = "shell"
)

// State is ws's reading of a session (ADR 0024), not Claude Code's own
// state field.
type State string

// Session states.
const (
	// StateWorking is an agent with a process, in the middle of a turn.
	StateWorking State = "working"
	// StateIdle is an agent with a process, waiting.
	StateIdle State = "idle"
	// StateStarting is an agent with no process yet that started less than
	// about 10 s ago.
	StateStarting State = "starting"
	// StateStopped is an agent with no process.
	StateStopped State = "stopped"
	// StateUnknown is a session ws couldn't read.
	StateUnknown State = "unknown"
	// StateRunning is a shell that is still running.
	StateRunning State = "running"
)

// Session is a workspace's session as ws reads it.
type Session struct {
	Kind Kind
	// ID is the agent's Claude Code session id; empty for a shell.
	ID    string
	State State
	// PID is the agent's process, or 0 without one or for a shell.
	PID int
	// Stale is whether the agent's process started before the process of
	// Claude Code's current background service (ADR 0018); nil when that
	// isn't known, or for a shell.
	Stale *bool
}

// Entry is a workspace and its session. Session is nil while the session
// is still being recorded.
type Entry struct {
	Workspace workspace.Workspace
	Session   *Session
}

// Listing is every workspace, and the directories under workspaces/ that
// aren't workspaces (ADR 0012).
type Listing struct {
	Workspaces []Entry
	Unknown    []string
}

// Removed is a workspace that was removed, or would be with a dry run.
type Removed struct {
	Workspace workspace.Workspace
	// SessionID is its agent's Claude Code session id; empty otherwise.
	SessionID string
}

// Warnings are problems that didn't stop an operation, for the command's
// output (ADR 0016).
func (m *Manager) Warnings() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.warnings)
}

func (m *Manager) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.log.Warn("lifetime warning", slog.String("warning", msg))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.warnings = append(m.warnings, msg)
}
