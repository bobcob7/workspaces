package lifetime

import (
	"context"
	"fmt"
	"time"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// ContinueMessage is what ws continue types into a session whose usage
// limit has reset. The transcript's copies of it count the tries (ADR
// 0047).
const ContinueMessage = "Your usage limit has reset. Continue where you left off."

// Waits and bounds of ws continue (ADR 0047).
const (
	// resetGrace is how long after a limit's reset a session is due, for
	// clock skew between this machine and the API.
	resetGrace = time.Minute
	// retryAfter is the least time between two tries on one session.
	retryAfter = 5 * time.Minute
	// maxTries is how many tries in a row ws continue makes before it
	// gives up on a session.
	maxTries = 3
)

// continueLockName is the lock a ws continue pass holds while it sends
// messages. It is hidden, so it's never taken for a workspace's state.
const continueLockName = ".continue.lock"

// Action is what ws continue did with a session.
type Action string

// Actions. A session that isn't limited, or isn't idle, has none.
const (
	ActionContinued     Action = "continued"
	ActionWouldContinue Action = "would_continue"
	ActionWaiting       Action = "waiting"
	ActionAsleep        Action = "asleep"
	ActionGaveUp        Action = "gave_up"
)

// ContinueOptions selects the workspaces Continue looks at.
type ContinueOptions struct {
	// Workspaces are the workspaces named on the command line, each of
	// which must be an agent's. Ignored with All.
	Workspaces []workspace.Workspace
	// All is every workspace; those that aren't agents' are skipped.
	All bool
	// DryRun sends nothing.
	DryRun bool
}

// Continued is an agent workspace Continue looked at.
type Continued struct {
	Workspace workspace.Workspace
	// SessionID is its agent's Claude Code session id.
	SessionID string
	// State is the session as ws read it; empty when it wasn't read.
	State State
	// Limit is the usage limit the session's turn ended at, or nil.
	Limit *claude.Limit
	// Action is what Continue did; empty for none.
	Action Action
	// Err is why the workspace couldn't be continued, or nil.
	Err error
}

// Continue sends ContinueMessage to each idle agent whose turn ended at a
// usage limit that has reset, while the machine is fully awake (ADR 0047).
// A session gets at most maxTries in a row, retryAfter apart. Unless it's
// a dry run, the listing and the messages happen under the continue lock,
// waiting for another ws continue to finish first. A failed listing fails
// the call; anything else fails only its own workspace, and a transcript
// that can't be read is a warning.
func (m *Manager) Continue(ctx context.Context, opts ContinueOptions) ([]Continued, error) {
	restored, err := m.restoreTargets(RestoreOptions{Workspaces: opts.Workspaces, All: opts.All})
	if err != nil {
		return nil, err
	}
	targets := make([]Continued, 0, len(restored))
	for i := range restored {
		r := &restored[i]
		targets = append(targets, Continued{Workspace: r.Workspace, SessionID: r.SessionID, Err: r.Err})
	}
	if !anyAgent(restored) {
		return targets, nil
	}
	unlock, err := m.lockContinue(ctx, opts.DryRun)
	if err != nil {
		return nil, err
	}
	defer unlock()
	sessions, err := m.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	awake := m.awakeOnce()
	for i := range targets {
		if targets[i].Err == nil {
			m.continueOne(ctx, &targets[i], sessions, awake, opts.DryRun)
		}
	}
	return targets, nil
}

// lockContinue waits for the continue lock, so that one pass sends at a
// time. A dry run sends nothing and takes no lock.
func (m *Manager) lockContinue(ctx context.Context, dryRun bool) (func(), error) {
	if dryRun {
		return func() {}, nil
	}
	return lockfile.Exclusive(ctx, m.continueLock)
}

// awakeOnce reads whether the machine is fully awake the first time it's
// asked, and answers every later call the same. A failure to read it is
// a warning and counts as asleep, so nothing is sent.
func (m *Manager) awakeOnce() func(ctx context.Context) bool {
	var asked, awake bool
	return func(ctx context.Context) bool {
		if asked {
			return awake
		}
		asked = true
		ok, err := m.power.Awake(ctx)
		if err != nil {
			m.warn("not continuing any agent: couldn't tell whether the machine is fully awake: %v", err)
			return false
		}
		awake = ok
		return awake
	}
}

// continueOne reads c's session and its limit, and sends it
// ContinueMessage if it's due and the machine is fully awake.
func (m *Manager) continueOne(ctx context.Context, c *Continued, sessions []claude.Session, awake func(context.Context) bool, dryRun bool) {
	s := findSession(sessions, c.SessionID)
	if s == nil {
		c.State = StateUnknown
		return
	}
	if c.Err = m.inWorkspace(&c.Workspace, s); c.Err != nil {
		return
	}
	now := m.now()
	if c.State = stateOf(s, now); c.State != StateIdle {
		return
	}
	limit, err := m.claude.UsageLimit(s, ContinueMessage)
	if err != nil {
		m.warn("not continuing workspace %s: %v", c.Workspace.ID, err)
		return
	}
	if c.Limit = limit; limit == nil {
		return
	}
	switch {
	case limit.Tries >= maxTries:
		c.Action = ActionGaveUp
	case !due(limit, now):
		c.Action = ActionWaiting
	case !awake(ctx):
		c.Action = ActionAsleep
	case dryRun:
		c.Action = ActionWouldContinue
	default:
		m.send(ctx, c, s)
	}
}

// due reports whether, at now, limit has reset, with resetGrace to spare,
// and the last try, if any, was at least retryAfter ago. Both times are
// wall-clock times, so a machine that slept meanwhile is judged by the
// time it is now.
func due(limit *claude.Limit, now time.Time) bool {
	if now.Before(limit.ResetsAt.Add(resetGrace)) {
		return false
	}
	return limit.Tries == 0 || !now.Before(limit.LastTry.Add(retryAfter))
}

func (m *Manager) send(ctx context.Context, c *Continued, s *claude.Session) {
	if err := m.claude.Send(ctx, s, ContinueMessage); err != nil {
		c.Err = fmt.Errorf("continuing Claude Code session %s: %w", s.ID, err)
		return
	}
	c.Action = ActionContinued
}
