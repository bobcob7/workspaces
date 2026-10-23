package lifetime

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/bobcob7/workspaces/internal/workspace"
)

// passInterval is how often the removal pass runs at most, except for ws
// list (ADR 0015).
const passInterval = time.Minute

// verdict is what the removal pass does with a workspace.
type verdict int

const (
	// keep: the session exists, or can't be judged.
	keep verdict = iota
	// gone: the session is gone, so the workspace goes (ADR 0015).
	gone
	// abandoned: no session was ever recorded, so the workspace goes with
	// any session started in it (ADRs 0023, 0025).
	abandoned
)

// Sweep removes leftover state (ADRs 0012, 0022) and, at most once a
// minute, the workspaces whose session is gone (ADR 0015). Every command
// that reads CONFIG_DIR runs it first. It returns the workspaces it
// removed. Failures are warnings.
func (m *Manager) Sweep(ctx context.Context) []Removed {
	m.store.CleanLeftovers()
	if !m.due() {
		return nil
	}
	_, removed, err := m.pass(ctx, newReader(m, false))
	if err != nil {
		m.warn("couldn't check workspaces' sessions: %v", err)
	}
	return removed
}

// List removes leftover state and the workspaces whose session is gone,
// however recently that last ran, so they don't appear, and returns the
// rest with their sessions.
func (m *Manager) List(ctx context.Context) (Listing, error) {
	m.store.CleanLeftovers()
	m.touch()
	entries, _, err := m.pass(ctx, newReader(m, true))
	if err != nil {
		return Listing{}, err
	}
	unknown, err := m.store.Unknown()
	if err != nil {
		return Listing{}, err
	}
	return Listing{Workspaces: entries, Unknown: unknown}, nil
}

// pass judges every workspace's session, removes those that are gone or
// abandoned, and returns the rest and the removed. A workspace whose
// removal failed is among the rest, with a warning, since it is still on
// disk. Every session record is read before Claude Code's sessions are
// listed: `ws claude` records a session only once Claude Code has accepted
// it, so a listing taken after the record includes the session.
func (m *Manager) pass(ctx context.Context, r *reader) ([]Entry, []Removed, error) {
	list, err := m.store.List()
	if err != nil {
		return nil, nil, err
	}
	records := make([]record, len(list))
	for i := range list {
		records[i].sess, records[i].err = m.store.Session(list[i].ID)
	}
	var entries []Entry
	var removed []Removed
	for i := range list {
		ws := &list[i]
		sess, v := r.judge(ctx, ws.ID, &records[i])
		switch {
		case v == keep:
			entries = append(entries, Entry{Workspace: *ws, Session: sess})
		case m.reap(ctx, r, ws, v):
			removed = append(removed, Removed{Workspace: *ws, SessionID: sessionID(sess)})
		default:
			entries = append(entries, Entry{Workspace: *ws, Session: sess})
		}
	}
	return entries, removed, nil
}

// record is a workspace's session record as read, or why it couldn't be.
type record struct {
	sess workspace.Session
	err  error
}

func sessionID(sess *Session) string {
	if sess == nil {
		return ""
	}
	return sess.ID
}

// reap removes a workspace the pass found gone or abandoned. It reports
// whether the workspace is gone afterwards; a failure is a warning, and
// the workspace is left for a later pass. A workspace whose directory
// another ws removed meanwhile, with its sessions, counts as removed.
func (m *Manager) reap(ctx context.Context, r *reader, ws *workspace.Workspace, v verdict) bool {
	if v == abandoned {
		sessions, err := r.agents(ctx)
		if err != nil {
			return false
		}
		err = m.removeOrphans(ctx, ws, sessions)
		if err != nil && !errors.Is(err, workspace.ErrNotFound) {
			m.warn("kept abandoned workspace %s: %v", ws.ID, err)
			return false
		}
	}
	err := m.store.Remove(ws.ID, workspace.RemoveOptions{})
	if err != nil && !errors.Is(err, workspace.ErrNotFound) {
		m.warn("couldn't remove workspace %s: %v", ws.ID, err)
		return false
	}
	m.log.InfoContext(ctx, "removed workspace", slog.String("id", ws.ID))
	return true
}

// due reports whether the removal pass should run now: it hasn't for a
// minute. If so, it records that it runs now. Without a state directory
// there are no workspaces, and nothing is due.
func (m *Manager) due() bool {
	if _, err := os.Stat(filepath.Dir(m.stamp)); err != nil {
		return false
	}
	info, err := os.Stat(m.stamp)
	if err == nil {
		since := m.now().Sub(info.ModTime())
		if since >= 0 && since < passInterval {
			return false
		}
	}
	m.touch()
	return true
}

// touch records that the removal pass runs now. A failure is a warning:
// the next command then runs the pass again.
func (m *Manager) touch() {
	if err := m.writeStamp(); err != nil {
		m.warn("couldn't record the removal pass: %v", err)
	}
}

func (m *Manager) writeStamp() error {
	now := m.now()
	err := os.Chtimes(m.stamp, now, now)
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(filepath.Dir(m.stamp)); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := os.WriteFile(m.stamp, nil, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", m.stamp, err)
	}
	return os.Chtimes(m.stamp, now, now)
}
