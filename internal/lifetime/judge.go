package lifetime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// reader judges sessions during one pass. It lists Claude Code's sessions
// and reads its background service at most once, and only when a
// workspace needs it.
type reader struct {
	m *Manager
	// stale says whether to read the background service, to tell which
	// agents are stale.
	stale        bool
	listed       bool
	sessions     []claude.Session
	listErr      error
	serviceRead  bool
	serviceStart time.Time
	serviceErr   error
}

func newReader(m *Manager, stale bool) *reader {
	return &reader{m: m, stale: stale}
}

// judge decides, from workspace id's session record, what the pass does
// with the workspace.
func (r *reader) judge(ctx context.Context, id string, rec *record) (*Session, verdict) {
	switch {
	case errors.Is(rec.err, workspace.ErrNoSession):
		return nil, r.unrecorded(id)
	case rec.err != nil:
		r.m.warn("kept workspace %s: %v", id, rec.err)
		return nil, keep
	case rec.sess.Kind == workspace.SessionShell:
		return r.shell(id)
	}
	return r.agent(ctx, rec.sess.ClaudeSessionID)
}

// unrecorded judges a workspace with no session record: abandoned once
// it is unlocked and a minute old (ADR 0023), otherwise still being
// handed to its session.
func (r *reader) unrecorded(id string) verdict {
	ok, err := r.m.store.Abandoned(id)
	if err != nil {
		r.m.warn("kept workspace %s: %v", id, err)
		return keep
	}
	if ok {
		return abandoned
	}
	return keep
}

// shell judges a shell session: running while its lock is held, gone once
// it's free (ADR 0015).
func (r *reader) shell(id string) (*Session, verdict) {
	locked, err := r.m.store.Locked(id)
	if err != nil {
		r.m.warn("kept workspace %s: %v", id, err)
		return &Session{Kind: KindShell, State: StateUnknown}, keep
	}
	if locked {
		return &Session{Kind: KindShell, State: StateRunning}, keep
	}
	return &Session{Kind: KindShell, State: StateUnknown}, gone
}

// agent judges a Claude Code session: gone only when `claude agents
// --json --all` doesn't list it and its jobs directory doesn't exist
// (ADRs 0015, 0024). When either can't be read, it is kept.
func (r *reader) agent(ctx context.Context, id string) (*Session, verdict) {
	unknown := &Session{Kind: KindAgent, ID: id, State: StateUnknown}
	sessions, err := r.agents(ctx)
	if err != nil {
		return unknown, keep
	}
	for i := range sessions {
		if sessions[i].ID == id {
			return r.describe(ctx, &sessions[i]), keep
		}
	}
	exists, err := r.m.claude.JobExists(id)
	if err != nil {
		r.m.warn("kept the workspace of Claude Code session %s: %v", id, err)
		return unknown, keep
	}
	if exists {
		return unknown, keep
	}
	return unknown, gone
}

// describe reads a listed session by its pid, status, and start time,
// never its state (ADR 0024).
func (r *reader) describe(ctx context.Context, s *claude.Session) *Session {
	out := &Session{Kind: KindAgent, ID: s.ID, State: stateOf(s, r.m.now()), PID: s.PID}
	if r.stale {
		out.Stale = r.staleness(ctx, s)
	}
	return out
}

// stateOf reads a listed session at now by its pid, status, and start
// time, never its state (ADR 0024).
func stateOf(s *claude.Session, now time.Time) State {
	switch {
	case s.Working():
		return StateWorking
	case s.HasProcess():
		return StateIdle
	case s.Starting(now):
		return StateStarting
	}
	return StateStopped
}

// staleness reports whether a session's process started before the
// process of Claude Code's current background service, so it has the old
// environment (ADR 0018). Both start times come from the operating
// system. A session without a process isn't stale. Without a running
// service, or when either start time can't be read, it isn't known.
func (r *reader) staleness(ctx context.Context, s *claude.Session) *bool {
	if !s.HasProcess() {
		return new(false)
	}
	service, ok := r.serviceStarted(ctx)
	if !ok {
		return nil
	}
	started, err := r.m.procs.StartTime(s.PID)
	if err != nil {
		r.m.log.DebugContext(ctx, "couldn't read when a session's process started", slog.Int("pid", s.PID), slog.Any("error", err))
		return nil
	}
	return new(started.Before(service))
}

// serviceStarted is when the process of Claude Code's background service
// started, read once per pass; ok is false when no service runs or that
// can't be read.
func (r *reader) serviceStarted(ctx context.Context) (time.Time, bool) {
	if !r.serviceRead {
		r.serviceRead = true
		r.serviceStart, r.serviceErr = r.readServiceStart(ctx)
		if r.serviceErr != nil {
			r.m.warn("couldn't read Claude Code's background service, so stale agents aren't marked: %v", r.serviceErr)
		}
	}
	return r.serviceStart, r.serviceErr == nil && !r.serviceStart.IsZero()
}

// readServiceStart finds the service's pid with `claude daemon status`
// and asks the operating system when that process started. No running
// service is the zero time.
func (r *reader) readServiceStart(ctx context.Context) (time.Time, error) {
	st, err := r.m.claude.DaemonStatus(ctx)
	if err != nil || !st.Running {
		return time.Time{}, err
	}
	started, err := r.m.procs.StartTime(st.PID)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading when its process %d started: %w", st.PID, err)
	}
	return started, nil
}

// agents lists Claude Code's background sessions, once per pass. A
// failure is reported once, and keeps every agent and abandoned
// workspace (ADR 0015).
func (r *reader) agents(ctx context.Context) ([]claude.Session, error) {
	if !r.listed {
		r.listed = true
		r.sessions, r.listErr = r.m.claude.Agents(ctx, claude.ListOptions{All: true})
		if r.listErr != nil {
			r.m.warn("couldn't list Claude Code sessions, so no agent or abandoned workspace was removed: %v", r.listErr)
		}
	}
	return r.sessions, r.listErr
}
