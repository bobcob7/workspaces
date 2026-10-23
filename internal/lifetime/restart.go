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

// serviceWait is how long, after starting `claude daemon run`, ws restart
// waits for `claude daemon status` to report a running service before it
// respawns anything.
const serviceWait = 10 * time.Second

// workingPoll is how often ws restart lists sessions while it waits for
// working ones to stop working (ADR 0018).
const workingPoll = 2 * time.Second

// ErrServiceFailed means ws restart couldn't stop Claude Code's background
// service or start its own.
var ErrServiceFailed = errors.New("couldn't replace Claude Code's background service")

var errNoService = errors.New("claude daemon status reports no service running")

var errOldService = errors.New("claude daemon status reports only the old service")

// RestartOptions are ws restart's options, and how it reports sessions as
// it goes.
type RestartOptions struct {
	// NoWait leaves working sessions stale instead of waiting for them.
	NoWait bool
	// Force respawns working sessions at once, interrupting their turn.
	Force bool
	// Recorded, if set, is called once with every session to restart,
	// before the service is stopped.
	Recorded func([]Restarted)
	// Waiting, if set, is called with the working sessions when ws starts
	// waiting for them.
	Waiting func([]Restarted)
	// Done, if set, is called with each session once its outcome is
	// known, in the order of the results.
	Done func(Restarted)
}

// Restarted is a background session that had a process when ws restart
// listed it.
type Restarted struct {
	SessionID string
	// Workspace is the id of the agent workspace the session belongs to,
	// or empty for a session ws didn't start.
	Workspace string
	// Working is whether the session was in the middle of a turn when ws
	// last read it.
	Working bool
	// Restarted reports whether `claude respawn` ran and succeeded.
	Restarted bool
	// Gone is set when Claude Code no longer had the session when ws went
	// to respawn it.
	Gone bool
	// Err is why the session couldn't be restarted, or nil.
	Err error
}

// restartSession is a session one ws restart works on, with the process
// it had when last listed.
type restartSession struct {
	out Restarted
	pid int
}

// restartRun is one ws restart.
type restartRun struct {
	m        *Manager
	opts     *RestartOptions
	sessions []*restartSession
	results  []Restarted
	// serviceUp is set once a service is known to run: ws's own started,
	// or a respawn succeeded, which starts one.
	serviceUp bool
	// oldPID is the service that ran before the stop, or 0 for none.
	oldPID int
}

// Restart replaces Claude Code's background service with one started from
// ws's environment, then respawns every background session that had a
// process, ws's or not (ADRs 0018, 0024, 0027):
//
//  1. under the respawn lock, it reads the session records, then lists
//     the sessions and records those with a process;
//  2. `claude daemon stop --any --keep-workers`, then a detached `claude
//     daemon run`, checked with `claude daemon status`;
//  3. it respawns the sessions that aren't working, or all with Force,
//     waiting after each until it shows a new process;
//  4. it waits for the working ones and respawns each once it stops
//     working; with NoWait they are left stale.
//
// The call fails only when it changed nothing that it reports: a failed
// listing or lock wait, an interruption before the stop, a failed stop
// while the old service still runs, or a service that didn't start when
// no session was running. The old service's pid is read before the stop,
// and ignored while ws waits for its own. Once the stop has run, the new
// service is started even if ctx is done, so the sessions aren't left
// without one. If it doesn't start, the sessions that aren't working are
// still respawned, even if ctx is done, until one is, since a respawn
// starts a service; if none is, `claude daemon run` is tried once more.
// A service that still didn't start is a warning, and the working
// sessions are left stale, each with an error wrapping ErrServiceFailed. An interruption leaves the
// sessions not yet respawned stale, each with ctx's error (ADR 0016).
func (m *Manager) Restart(ctx context.Context, opts RestartOptions) ([]Restarted, error) {
	unlock, err := lockfile.Exclusive(ctx, m.respawnLock)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r := &restartRun{m: m, opts: &opts}
	return r.run(ctx)
}

func (r *restartRun) run(ctx context.Context) ([]Restarted, error) {
	if err := r.record(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	swapCtx := context.WithoutCancel(ctx)
	r.oldPID = r.m.servicePID(swapCtx)
	abort, serviceErr := r.m.replaceService(swapCtx, r.oldPID)
	if abort {
		return nil, serviceErr
	}
	r.serviceUp = serviceErr == nil
	left := r.respawnReady(ctx)
	if serviceErr != nil && !r.serviceUp {
		serviceErr = r.retryService(swapCtx, serviceErr)
	}
	if serviceErr != nil && len(r.sessions) == 0 {
		return nil, serviceErr
	}
	if serviceErr != nil {
		r.warnServiceFailed(serviceErr)
	}
	if serviceErr == nil && !r.opts.NoWait && ctx.Err() == nil {
		left = r.awaitWorking(ctx, left)
	}
	for _, s := range left {
		s.out.Err = staleErr(ctx, s, serviceErr != nil)
		r.done(s)
	}
	return r.results, nil
}

// retryService runs `claude daemon run` a second time, when the first
// didn't start a service and no respawn started one, so the sessions
// aren't left without one. It returns nil once a service runs, or both
// failures.
func (r *restartRun) retryService(ctx context.Context, firstErr error) error {
	err := r.m.startService(ctx, r.oldPID)
	if err != nil {
		return fmt.Errorf("%w; tried again: %w", firstErr, err)
	}
	r.serviceUp = true
	r.m.warn("%v; ws started one on the second try", firstErr)
	return nil
}

// warnServiceFailed warns that ws's service didn't start, and, unless a
// respawn started one, that the sessions stop without one.
func (r *restartRun) warnServiceFailed(serviceErr error) {
	if r.serviceUp {
		r.m.warn("%v; ws respawned the sessions that weren't working, which starts a service if none is running", serviceErr)
		return
	}
	r.m.warn("%v; no background service may be running, and background sessions stop within about a minute unless one is started: run ws restart again, or `claude respawn <id>` on any session", serviceErr)
}

// staleErr is why s is left stale, the old service's environment kept
// until a later ws restart: a working one's when ws's service didn't
// start, any one's when ctx is done, or nil, as with NoWait.
func staleErr(ctx context.Context, s *restartSession, serviceFailed bool) error {
	switch {
	case serviceFailed && s.out.Working:
		return fmt.Errorf("%w, so session %s, which was working, was left stale: run ws restart again", ErrServiceFailed, s.out.SessionID)
	case ctx.Err() != nil:
		return fmt.Errorf("interrupted before session %s was restarted, so it keeps the old environment: run ws restart again: %w", s.out.SessionID, ctx.Err())
	}
	return nil
}

// record reads the session records, then lists Claude Code's sessions
// once, and keeps each that has a process.
func (r *restartRun) record(ctx context.Context) error {
	owners := r.m.sessionOwners()
	sessions, err := r.m.claude.Agents(ctx, claude.ListOptions{All: true})
	if err != nil {
		return err
	}
	for i := range sessions {
		s := &sessions[i]
		if !s.HasProcess() {
			continue
		}
		rec := Restarted{SessionID: s.ID, Workspace: r.m.owner(owners, s), Working: s.Working()}
		r.sessions = append(r.sessions, &restartSession{out: rec, pid: s.PID})
	}
	if r.opts.Recorded != nil {
		r.opts.Recorded(snapshot(r.sessions))
	}
	return nil
}

// sessionOwners maps each recorded Claude Code session id to its
// workspace. Workspaces whose session record can't be read are left out.
func (m *Manager) sessionOwners() map[string]workspace.Workspace {
	list, err := m.store.List()
	if err != nil {
		m.warn("couldn't read the workspaces, so no session is shown with its workspace: %v", err)
		return nil
	}
	owners := make(map[string]workspace.Workspace, len(list))
	for i := range list {
		sess, err := m.store.Session(list[i].ID)
		if err == nil && sess.Kind == workspace.SessionClaude && sess.ClaudeSessionID != "" {
			owners[sess.ClaudeSessionID] = list[i]
		}
	}
	return owners
}

// owner is the id of the workspace whose recorded session s is, if s runs
// in one of that workspace's session directories (ADRs 0026, 0028); empty
// otherwise.
func (m *Manager) owner(owners map[string]workspace.Workspace, s *claude.Session) string {
	ws, ok := owners[s.ID]
	if !ok || m.inWorkspace(&ws, s) != nil {
		return ""
	}
	return ws.ID
}

// replaceService stops Claude Code's background service, leaving its
// sessions running, and starts ws's own (ADR 0018). ctx mustn't be
// cancelled partway, so the sessions are never left without a service.
// abort means nothing should be respawned: the old service still runs, or
// it couldn't be told whether it does. Otherwise err is set when ws's
// service didn't start.
// oldPID is the service that ran before the stop, which ws ignores while
// it waits for its own.
func (m *Manager) replaceService(ctx context.Context, oldPID int) (bool, error) {
	if stopErr := m.claude.StopDaemon(ctx); stopErr != nil {
		abort, err := m.stopFailed(ctx, stopErr, oldPID)
		if abort {
			return true, err
		}
	}
	return false, m.startService(ctx, oldPID)
}

// servicePID is the running service's pid before ws stops it, or 0 when
// none runs or `claude daemon status` fails.
func (m *Manager) servicePID(ctx context.Context) int {
	st, err := m.claude.DaemonStatus(ctx)
	if err != nil || !st.Running {
		return 0
	}
	return st.PID
}

// stopFailed decides what follows a failed `claude daemon stop`: nothing,
// if the service still runs; starting ws's own if none runs, as when none
// was running to begin with. If `claude daemon status` fails too, ws
// starts a service in case none runs, and respawns nothing.
func (m *Manager) stopFailed(ctx context.Context, stopErr error, oldPID int) (bool, error) {
	st, err := m.claude.DaemonStatus(ctx)
	switch {
	case err != nil:
		started := "ws started a service in case none was running"
		if runErr := m.startService(ctx, oldPID); runErr != nil {
			started = fmt.Sprintf("ws couldn't start a service in case none was running (%v)", runErr)
		}
		return true, fmt.Errorf("%w: `claude daemon stop` failed and `claude daemon status` failed too (%w), so no session was restarted, and %s: %w", ErrServiceFailed, err, started, stopErr)
	case st.Running:
		return true, fmt.Errorf("%w: `claude daemon stop` failed and the service (pid %d) is still running, so no session was restarted: %w", ErrServiceFailed, st.PID, stopErr)
	}
	m.warn("`claude daemon stop` failed, but no background service was running, so ws started one: %v", stopErr)
	return false, nil
}

// startService starts `claude daemon run` detached, and waits up to
// serviceWait for `claude daemon status` to report a service other than
// oldPID, which may still be exiting. A service with another pid was
// started first by another claude, and may have that process's
// environment: ws warns (ADR 0018).
func (m *Manager) startService(ctx context.Context, oldPID int) error {
	pid, err := m.claude.RunDaemon(ctx)
	if err != nil {
		return fmt.Errorf("%w: `claude daemon run` failed: %w", ErrServiceFailed, err)
	}
	st, err := m.awaitService(ctx, oldPID)
	if err != nil {
		return fmt.Errorf("%w: `claude daemon run` (pid %d) didn't start a service within %s: %w", ErrServiceFailed, pid, serviceWait, err)
	}
	if st.PID != pid {
		m.warn("Claude Code's background service (pid %d) isn't the one ws started (pid %d): another claude started one first, so it may have that process's environment; run ws restart again", st.PID, pid)
	}
	return nil
}

// awaitService polls `claude daemon status` until it reports a running
// service other than oldPID, for up to serviceWait.
func (m *Manager) awaitService(ctx context.Context, oldPID int) (claude.DaemonStatus, error) {
	deadline := m.now().Add(serviceWait)
	for {
		st, err := m.claude.DaemonStatus(ctx)
		switch {
		case err != nil:
		case !st.Running:
			err = errNoService
		case oldPID != 0 && st.PID == oldPID:
			err = fmt.Errorf("%w (pid %d)", errOldService, oldPID)
		default:
			return st, nil
		}
		if !m.now().Before(deadline) {
			return claude.DaemonStatus{}, err
		}
		if sleepErr := m.sleep(ctx, respawnPoll); sleepErr != nil {
			return claude.DaemonStatus{}, sleepErr
		}
	}
}

// respawnReady respawns, in order, each recorded session that isn't
// working, or every one with Force, and returns the rest. Once ctx is
// done it respawns no more, unless no service is known to run yet.
func (r *restartRun) respawnReady(ctx context.Context) []*restartSession {
	var left []*restartSession
	for _, s := range r.sessions {
		if (ctx.Err() != nil && r.serviceUp) || (s.out.Working && !r.opts.Force) {
			left = append(left, s)
			continue
		}
		r.respawn(ctx, s)
	}
	return left
}

// respawn runs `claude respawn` on s, which ws's interruption doesn't cut
// short, then waits for s's new process (ADR 0027). A session Claude Code
// no longer has is gone.
func (r *restartRun) respawn(ctx context.Context, s *restartSession) {
	err := r.m.claude.Respawn(context.WithoutCancel(ctx), s.out.SessionID)
	switch {
	case errors.Is(err, claude.ErrNoSession):
		s.out.Gone = true
	case err != nil:
		s.out.Err = fmt.Errorf("respawning Claude Code session %s: %w", s.out.SessionID, err)
	default:
		s.out.Restarted, r.serviceUp = true, true
		r.m.awaitProcess(ctx, s.out.SessionID, s.pid)
	}
	r.done(s)
}

// awaitWorking lists the sessions every workingPoll and respawns each
// working one once it stops working, until none is left (ADR 0018). It
// returns the sessions left when ctx is done. A failed listing fails
// every session left.
func (r *restartRun) awaitWorking(ctx context.Context, working []*restartSession) []*restartSession {
	if len(working) == 0 {
		return nil
	}
	if r.opts.Waiting != nil {
		r.opts.Waiting(snapshot(working))
	}
	for len(working) > 0 {
		if r.m.sleep(ctx, workingPoll) != nil {
			return working
		}
		sessions, err := r.m.claude.Agents(ctx, claude.ListOptions{All: true})
		if err != nil && ctx.Err() != nil {
			return working
		}
		if err != nil {
			r.failAll(working, err)
			return nil
		}
		working = r.respawnFinished(ctx, working, sessions)
	}
	return nil
}

// respawnFinished respawns each session in working that, in sessions, is
// no longer working, and returns those that still are. One Claude Code no
// longer lists is gone.
func (r *restartRun) respawnFinished(ctx context.Context, working []*restartSession, sessions []claude.Session) []*restartSession {
	var left []*restartSession
	for _, s := range working {
		listed := findSession(sessions, s.out.SessionID)
		switch {
		case listed == nil:
			s.out.Gone = true
			r.done(s)
		case listed.Working() || ctx.Err() != nil:
			left = append(left, s)
		default:
			s.pid, s.out.Working = listed.PID, false
			r.respawn(ctx, s)
		}
	}
	return left
}

func (r *restartRun) failAll(left []*restartSession, err error) {
	for _, s := range left {
		s.out.Err = fmt.Errorf("listing Claude Code sessions while waiting for session %s to stop working: %w", s.out.SessionID, err)
		r.done(s)
	}
}

func (r *restartRun) done(s *restartSession) {
	r.results = append(r.results, s.out)
	if r.opts.Done != nil {
		r.opts.Done(s.out)
	}
}

func snapshot(sessions []*restartSession) []Restarted {
	out := make([]Restarted, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.out)
	}
	return out
}
