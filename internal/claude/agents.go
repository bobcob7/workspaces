package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// State is a background session's state as `claude agents --json`
// reports it. Claude Code may add values; callers must accept ones they
// don't know. It isn't used to decide whether a session is running,
// working, or starting (ADR 0024).
type State string

// States seen from Claude Code 2.1.303 (ADR 0024).
const (
	StateWorking State = "working"
	StateBlocked State = "blocked"
	StateDone    State = "done"
	StateStopped State = "stopped"
)

// statusBusy is the status of a session that is in the middle of a turn.
const statusBusy = "busy"

// startingWindow is how long after it started a session with no process
// counts as starting rather than stopped: `claude --bg` returns before the
// session has a process (ADR 0024).
const startingWindow = 10 * time.Second

// kindBackground marks a background session in `claude agents --json`;
// interactive sessions have no short id and aren't ws's (ADR 0024).
const kindBackground = "background"

// Session is one Claude Code background session.
type Session struct {
	// ID is the short id `claude --bg` prints and attach, logs, stop, rm,
	// and respawn take.
	ID string
	// SessionID is the conversation's UUID, as `claude --resume` takes it.
	SessionID string
	Name      string
	// Cwd is the directory the session was started in, symlinks resolved.
	Cwd   string
	State State
	// Status is "idle" or "busy" while the session has a process, and
	// empty without one.
	Status string
	// PID is the session's process, or 0 when it has none: stopped, after
	// a reboot, or for a moment after `claude --bg` returns (ADR 0024).
	PID int
	// StartedAt is when the session's current process started, or when
	// the session was created while it has none (ADR 0024).
	StartedAt time.Time
}

// HasProcess reports whether the session is running in a process.
func (s *Session) HasProcess() bool {
	return s.PID != 0
}

// Working reports whether the session is in the middle of a turn: it has
// a process and its status is busy. State isn't consulted (ADR 0024).
func (s *Session) Working() bool {
	return s.HasProcess() && s.Status == statusBusy
}

// Starting reports whether, at now, the session has no process yet only
// because it started less than startingWindow ago. Such a session isn't
// respawned, and ws list shows it as starting (ADR 0024).
func (s *Session) Starting(now time.Time) bool {
	if s.HasProcess() || s.StartedAt.IsZero() {
		return false
	}
	return now.Sub(s.StartedAt) < startingWindow
}

// ListOptions selects the sessions Agents returns.
type ListOptions struct {
	// All includes sessions that have finished.
	All bool
	// Cwd, if set, keeps only sessions started in or below this directory.
	Cwd string
}

// Agents lists Claude Code's background sessions with `claude agents
// --json`. Interactive sessions are left out.
func (c *Client) Agents(ctx context.Context, opts ListOptions) ([]Session, error) {
	args := []string{"agents", "--json"}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	out, err := c.run(ctx, "", args...)
	if err != nil {
		return nil, err
	}
	return parseAgents(out)
}

// agentEntry is one element of `claude agents --json`'s array.
//
//nolint:tagliatelle // Claude Code's field names, not ws output (ADR 0016).
type agentEntry struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Cwd       string `json:"cwd"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Status    string `json:"status"`
	PID       int    `json:"pid"`
	StartedAt int64  `json:"startedAt"`
}

// parseAgents reads `claude agents --json` output and keeps the background
// sessions.
func parseAgents(out []byte) ([]Session, error) {
	var entries []agentEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("%w: claude agents --json: %w", ErrUnexpectedOutput, err)
	}
	sessions := make([]Session, 0, len(entries))
	for i := range entries {
		e := &entries[i]
		if e.Kind != kindBackground {
			continue
		}
		if e.ID == "" {
			return nil, fmt.Errorf("%w: claude agents --json: background session %q has no id", ErrUnexpectedOutput, e.SessionID)
		}
		sessions = append(sessions, e.session())
	}
	return sessions, nil
}

func (e *agentEntry) session() Session {
	s := Session{
		ID:        e.ID,
		SessionID: e.SessionID,
		Name:      e.Name,
		Cwd:       e.Cwd,
		State:     State(e.State),
		Status:    e.Status,
		PID:       e.PID,
	}
	if e.StartedAt != 0 {
		s.StartedAt = time.UnixMilli(e.StartedAt).UTC()
	}
	return s
}
