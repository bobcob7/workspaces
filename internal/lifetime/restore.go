package lifetime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// respawnWait is how long, after a respawn, ws restore waits for Claude
// Code to list the session with a process before it moves on (ADR 0027).
const respawnWait = 10 * time.Second

// respawnPoll is how often it lists sessions while it waits.
const respawnPoll = 250 * time.Millisecond

// ErrNotAgent means ws restore was named a workspace that has no agent to
// restore: a shell workspace, or one whose session was never recorded.
var ErrNotAgent = errors.New("not an agent workspace")

// RestoreOptions selects the workspaces Restore looks at.
type RestoreOptions struct {
	// Workspaces are the workspaces named on the command line, each of
	// which must be an agent's. Ignored with All.
	Workspaces []workspace.Workspace
	// All is every workspace; those that aren't agents' are skipped.
	All bool
	// DryRun respawns nothing.
	DryRun bool
}

// Restored is an agent workspace Restore looked at.
type Restored struct {
	Workspace workspace.Workspace
	// SessionID is its agent's Claude Code session id; empty when it has
	// no agent.
	SessionID string
	// State is the session as ws read it before restoring (ADR 0024):
	// StateStopped for one that was respawned, or would be with a dry
	// run; StateUnknown for one Claude Code doesn't list. Empty when the
	// session wasn't read.
	State State
	// Respawned reports whether `claude respawn` ran and succeeded.
	Respawned bool
	// Err is why the workspace couldn't be restored, or nil.
	Err error
}

// Restore runs `claude respawn` on the session of each agent workspace
// that has no process and isn't starting, one at a time, in order (ADRs
// 0015, 0024, 0027). A session that Claude Code lists in another
// directory is refused and left alone; one it doesn't list is left alone.
// Every session record is read before Claude Code's sessions are listed,
// once. Unless it's a dry run, the listing and the respawns happen under
// the respawn lock, waiting for another ws restore or ws restart to finish first, and
// each respawn is followed by a wait until the session is listed with a
// process. A failed listing fails the call; anything else fails only its
// own workspace.
func (m *Manager) Restore(ctx context.Context, opts RestoreOptions) ([]Restored, error) {
	targets, err := m.restoreTargets(opts)
	if err != nil {
		return nil, err
	}
	if !anyAgent(targets) {
		return targets, nil
	}
	unlock, err := m.lockRestore(ctx, opts.DryRun)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sessions, err := m.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for i := range targets {
		if targets[i].Err == nil {
			m.restoreOne(ctx, &targets[i], sessions, opts.DryRun)
		}
	}
	return targets, nil
}

// lockRestore waits for the respawn lock, so that one ws restore or ws
// restart respawns at a time (ADR 0027). A dry run changes nothing and takes no lock.
func (m *Manager) lockRestore(ctx context.Context, dryRun bool) (func(), error) {
	if dryRun {
		return func() {}, nil
	}
	return lockfile.Exclusive(ctx, m.respawnLock)
}

// restoreTargets reads the session record of each workspace to restore.
// A named workspace without an agent is an error; with All it is skipped.
func (m *Manager) restoreTargets(opts RestoreOptions) ([]Restored, error) {
	list := opts.Workspaces
	if opts.All {
		var err error
		if list, err = m.store.List(); err != nil {
			return nil, err
		}
	}
	targets := make([]Restored, 0, len(list))
	for i := range list {
		r := m.restoreTarget(&list[i])
		if opts.All && r.Err != nil {
			m.skipped(&r)
			continue
		}
		targets = append(targets, r)
	}
	return targets, nil
}

// restoreTarget is ws's agent, from its session record, or why it has
// none.
func (m *Manager) restoreTarget(ws *workspace.Workspace) Restored {
	r := Restored{Workspace: *ws}
	sess, err := m.store.Session(ws.ID)
	switch {
	case errors.Is(err, workspace.ErrNoSession):
		r.Err = m.unrecordedTarget(ws.ID)
	case err != nil:
		r.Err = err
	case sess.Kind == workspace.SessionShell:
		r.Err = fmt.Errorf("%w: %s is a shell workspace; only agent workspaces are restored", ErrNotAgent, ws.ID)
	default:
		r.SessionID = sess.ClaudeSessionID
	}
	return r
}

// unrecordedTarget is the error for a named workspace with no session
// record: ErrBusy while a creation or hand-off holds its lock, as for ws
// rm (ADR 0026), and ErrNotAgent once nothing does.
func (m *Manager) unrecordedTarget(id string) error {
	locked, err := m.store.Locked(id)
	switch {
	case err != nil:
		return err
	case locked:
		return fmt.Errorf("%w: %s", ErrBusy, id)
	}
	return fmt.Errorf("%w: %s has no session recorded", ErrNotAgent, id)
}

// skipped notes a workspace that restoring every agent passes over: a
// shell or unrecorded one silently, one whose record can't be read with a
// warning.
func (m *Manager) skipped(r *Restored) {
	if errors.Is(r.Err, ErrNotAgent) || errors.Is(r.Err, ErrBusy) {
		return
	}
	m.warn("skipped workspace %s: %v", r.Workspace.ID, r.Err)
}

func anyAgent(targets []Restored) bool {
	for i := range targets {
		if targets[i].Err == nil {
			return true
		}
	}
	return false
}

// restoreOne reads r's session from Claude Code's listing and respawns it
// if it is stopped and runs in r's workspace. A session Claude Code no
// longer has by the time it is respawned, as after a `claude rm` or `ws
// rm` since the listing, is unknown, as if it hadn't been listed.
func (m *Manager) restoreOne(ctx context.Context, r *Restored, sessions []claude.Session, dryRun bool) {
	s := findSession(sessions, r.SessionID)
	if s == nil {
		r.State = StateUnknown
		return
	}
	if r.Err = m.inWorkspace(&r.Workspace, s); r.Err != nil {
		return
	}
	r.State = stateOf(s, m.now())
	if r.State != StateStopped || dryRun {
		return
	}
	err := m.claude.Respawn(ctx, s.ID)
	switch {
	case errors.Is(err, claude.ErrNoSession):
		r.State = StateUnknown
	case err != nil:
		r.Err = fmt.Errorf("respawning Claude Code session %s: %w", s.ID, err)
	default:
		r.Respawned = true
		m.awaitProcess(ctx, s.ID, 0)
	}
}

// awaitProcess waits, for up to respawnWait, until Claude Code lists the
// respawned session id with a process other than oldPID, the one it had
// before the respawn (0 for none), so that a ws restore or ws restart run
// right after this one sees it running (ADR 0027). Running out of time is
// a warning, not a failure.
func (m *Manager) awaitProcess(ctx context.Context, id string, oldPID int) {
	deadline := m.now().Add(respawnWait)
	var lastErr error
	for {
		sessions, err := m.claude.Agents(ctx, claude.ListOptions{All: true})
		if s := findSession(sessions, id); err == nil && s != nil && s.HasProcess() && s.PID != oldPID {
			return
		}
		if err != nil {
			lastErr = err
		}
		if !m.now().Before(deadline) {
			m.warnNoProcess(id, lastErr)
			return
		}
		if m.sleep(ctx, respawnPoll) != nil {
			return
		}
	}
}

func (m *Manager) warnNoProcess(id string, lastErr error) {
	msg := fmt.Sprintf("Claude Code didn't list session %s with a process within %s of respawning it; another ws restore may respawn it again", id, respawnWait)
	if lastErr != nil {
		msg += fmt.Sprintf(" (last listing failed: %v)", lastErr)
	}
	m.warn("%s", msg)
}

// inWorkspace checks that the listed session s runs in workspace ws's
// directory, as the deletion guard resolves it (ADRs 0012, 0026).
func (m *Manager) inWorkspace(ws *workspace.Workspace, s *claude.Session) error {
	dir, err := m.store.GuardedDir(ws.ID)
	if err != nil {
		return err
	}
	return sessionElsewhere(ws, s, dir)
}

func findSession(sessions []claude.Session, id string) *claude.Session {
	for i := range sessions {
		if sessions[i].ID == id {
			return &sessions[i]
		}
	}
	return nil
}
