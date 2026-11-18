package daemon

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/runner"
)

// Runner states the supervisor acts on, as runner.Files.State names them.
const (
	stateWorking = "working"
	stateIdle    = "idle"
	stateStopped = "stopped"
	stateStart   = "starting"
)

// Exit reasons the daemon records for a runner it ends (reference.md).
const (
	exitStopped = "stopped"
	exitCrashed = "crashed"
)

// Defaults for SupervisorConfig's zero values (ADR 0068).
const (
	defaultPoll      = 2 * time.Second
	defaultStartWait = 10 * time.Second
	defaultStopWait  = 10 * time.Second
	// stepInterval is how often a wait looks again.
	stepInterval = 50 * time.Millisecond
	// logTailBytes is how much of runner.log a runner_failed answer quotes.
	logTailBytes = 4 << 10
)

// defaultBackoffs are the waits before a dead runner's first, second and third
// resume.
func defaultBackoffs() []time.Duration {
	return []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
}

// SupervisorConfig is what a Supervisor needs from its daemon. Zero durations
// take the defaults; Backoff nil takes 5 s, 30 s and 2 min.
type SupervisorConfig struct {
	StateDir, ConfigDir, Exe string
	BaseEnv                  []string                      // os.Environ() at daemon start
	LoadConfig               func() (config.Config, error) // re-read at each start
	Poll                     time.Duration                 // 2 s
	Backoff                  []time.Duration               // 5 s, 30 s, 2 min
	ResumeOnStart            bool
	StartWait                time.Duration // 10 s: how long a start waits for status.json
	StopWait                 time.Duration // 10 s: how long a stop waits before SIGKILL
	Now                      func() time.Time
	Store                    RunnerRecords
}

// Supervisor starts, watches, resumes and stops the runner sessions under
// StateDir (ADR 0068).
type Supervisor struct {
	cfg     SupervisorConfig
	procs   processReader
	starter processStarter
	log     *slog.Logger

	mu                sync.Mutex
	runners           map[string]*watchedRunner
	lastConfigWarning string
}

// watchedRunner is one runner session the supervisor holds: its pid, its
// state at the last check, and when it was first seen dead.
type watchedRunner struct {
	workspace string
	session   string
	run       runner.Run
	pid       int
	state     string
	restarts  int
	adopted   bool
	deadSince time.Time
}

// NewSupervisor returns a supervisor of the runner sessions under cfg.StateDir.
// It starts nothing until Reconcile or Start.
//
//nolint:gocritic // hugeParam: the config is taken by value, as the daemon hands it over once.
func NewSupervisor(cfg SupervisorConfig, procs processReader, starter processStarter, log *slog.Logger) *Supervisor {
	withDefaults(&cfg)
	return &Supervisor{
		cfg:     cfg,
		procs:   procs,
		starter: starter,
		log:     log,
		runners: map[string]*watchedRunner{},
	}
}

func withDefaults(cfg *SupervisorConfig) {
	if cfg.Poll <= 0 {
		cfg.Poll = defaultPoll
	}
	if cfg.Backoff == nil {
		cfg.Backoff = defaultBackoffs()
	}
	if cfg.StartWait <= 0 {
		cfg.StartWait = defaultStartWait
	}
	if cfg.StopWait <= 0 {
		cfg.StopWait = defaultStopWait
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.LoadConfig == nil {
		cfg.LoadConfig = func() (config.Config, error) { return config.Config{}, nil }
	}
}

func (s *Supervisor) files(id string) runner.Files {
	return runner.FilesFor(s.cfg.StateDir, id)
}

// Reconcile runs before the daemon listens. It adopts the live runners under
// StateDir, resumes the stopped ones when ResumeOnStart is set, and leaves
// the finished ones alone.
func (s *Supervisor) Reconcile(context.Context) error {
	entries, err := os.ReadDir(s.cfg.StateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing runner sessions: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range entries {
		if entry.IsDir() {
			s.reconcileOne(entry.Name())
		}
	}
	return nil
}

func (s *Supervisor) reconcileOne(id string) {
	files := s.files(id)
	run, err := files.ReadRun()
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		s.log.Warn("reading a runner session", "workspace", id, "error", err)
		return
	}
	state, pid, _ := files.State(s.cfg.Now(), run.CreatedAt, s.procs)
	switch state {
	case stateWorking:
		s.track(id, &run, pid, stateWorking).adopted = true
	case stateStart:
		s.track(id, &run, 0, stateStart)
	case stateStopped:
		s.resumeAtStart(id, &run)
	}
}

// resumeAtStart resumes a stopped runner found at start, when the config says
// to. A resume that fails is retried by Watch after its backoff.
func (s *Supervisor) resumeAtStart(id string, run *runner.Run) {
	if !s.cfg.ResumeOnStart {
		return
	}
	w := s.track(id, run, 0, stateStopped)
	if err := s.resume(w); err != nil {
		s.log.Warn("resuming a runner at start", "session", w.session, "error", err)
		w.deadSince = s.cfg.Now()
	}
}

// Watch checks every runner each Poll until ctx ends.
func (s *Supervisor) Watch(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.poll()
		}
	}
}

func (s *Supervisor) poll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, w := range s.runners {
		if s.check(id, w) {
			delete(s.runners, id)
		}
	}
}

// check updates w from its files and reports whether it is no longer watched.
func (s *Supervisor) check(id string, w *watchedRunner) bool {
	now := s.cfg.Now()
	state, pid, st := s.files(id).State(now, w.run.CreatedAt, s.procs)
	w.state = state
	if st != nil {
		w.restarts = st.Restarts
	}
	switch state {
	case stateWorking:
		w.pid = pid
		w.deadSince = time.Time{}
		return false
	case stateIdle:
		return true
	case stateStopped:
		return s.handleDeath(id, w, now)
	case stateStart:
		// A runner this supervisor resumed reads starting for the restart
		// window after its last write, even when it has died again; the
		// daemon is the one that resumes it, so it must see the death.
		if s.diedWithoutExit(st) {
			return s.handleDeath(id, w, now)
		}
	}
	return false
}

// diedWithoutExit reports whether status st names a process that is gone and
// recorded no exit.
func (s *Supervisor) diedWithoutExit(st *runner.Status) bool {
	if st == nil || st.Exit != nil || st.PID == 0 {
		return false
	}
	_, err := s.procs.StartTime(st.PID)
	return err != nil
}

// handleDeath resumes a dead runner once its backoff has passed, and finalizes
// it as crashed once the backoff is spent.
func (s *Supervisor) handleDeath(id string, w *watchedRunner, now time.Time) bool {
	if w.deadSince.IsZero() {
		w.deadSince = now
		s.log.Warn("runner stopped without an exit", "session", w.session, "restarts", w.restarts)
	}
	if w.restarts >= len(s.cfg.Backoff) {
		s.crash(id, w)
		return true
	}
	if now.Sub(w.deadSince) < s.cfg.Backoff[w.restarts] {
		return false
	}
	if err := s.resume(w); err != nil {
		s.log.Warn("resuming a runner", "session", w.session, "error", err)
		w.deadSince = now
	}
	return false
}

func (s *Supervisor) crash(id string, w *watchedRunner) {
	message := fmt.Sprintf("the runner died %d times; the daemon gave up", w.restarts+1)
	s.log.Error("runner crashed", "session", w.session)
	if err := s.finish(s.files(id), &w.run, exitCrashed, message); err != nil {
		s.log.Error("finalizing a crashed runner", "session", w.session, "error", err)
	}
}

// resume starts w's runner again with --resume, counting the restart in
// status.json before the start.
func (s *Supervisor) resume(w *watchedRunner) error {
	files := s.files(w.workspace)
	st, err := readStatusOrNew(files, &w.run)
	if err != nil {
		return err
	}
	st.Restarts++
	if err = files.WriteStatus(&st); err != nil {
		return fmt.Errorf("counting the restart: %w", err)
	}
	pid, err := s.spawn(w.workspace, true)
	if err != nil {
		return err
	}
	w.pid, w.restarts, w.deadSince, w.state = pid, st.Restarts, time.Time{}, stateWorking
	return nil
}

func readStatusOrNew(files runner.Files, run *runner.Run) (runner.Status, error) {
	st, err := files.ReadStatus()
	if errors.Is(err, fs.ErrNotExist) {
		return runner.Status{Session: run.Session, MaxSteps: run.MaxSteps}, nil
	}
	return st, err
}

// Start starts workspace id's runner, or returns the pid of one that is
// already working.
func (s *Supervisor) Start(_ context.Context, id string, resume bool) (int, error) {
	run, err := s.readSession(id)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w, ok := s.runners[id]; ok && w.state == stateWorking {
		return w.pid, nil
	}
	pid, err := s.spawn(id, resume)
	if err != nil {
		return 0, err
	}
	s.track(id, &run, pid, stateWorking)
	return pid, nil
}

// track holds workspace id's runner, which is at pid in state.
func (s *Supervisor) track(id string, run *runner.Run, pid int, state string) *watchedRunner {
	w := &watchedRunner{workspace: id, session: run.Session, run: *run, pid: pid, state: state}
	s.runners[id] = w
	return w
}

func (s *Supervisor) untrack(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.runners, id)
}

// spawn starts workspace id's runner process and waits for it to write
// status.json under its pid. A runner that doesn't is runner_failed.
func (s *Supervisor) spawn(id string, resume bool) (int, error) {
	files := s.files(id)
	env, err := s.runnerEnv(id)
	if err != nil {
		return 0, runnerFailed(files, err)
	}
	if err = files.ClearStop(); err != nil {
		return 0, runnerFailed(files, err)
	}
	pid, err := s.starter.Start(s.cfg.Exe, runnerArgs(id, resume), files.Dir, files.Log(), env)
	if err != nil {
		return 0, runnerFailed(files, err)
	}
	if err := s.awaitStatus(files, pid); err != nil {
		_ = s.starter.Signal(pid, syscall.SIGTERM)
		return 0, runnerFailed(files, err)
	}
	return pid, nil
}

func runnerArgs(id string, resume bool) []string {
	args := []string{"runner", "run"}
	if resume {
		args = append(args, "--resume")
	}
	return append(args, id)
}

// runnerEnv is workspace id's runner environment, built as RunnerEnv documents
// from the daemon's own, the config's [runner.env] and secrets.env.
func (s *Supervisor) runnerEnv(id string) ([]string, error) {
	secrets, _, err := config.ReadSecrets(s.cfg.ConfigDir)
	if err != nil {
		return nil, fmt.Errorf("reading secrets.env: %w", err)
	}
	return RunnerEnv(s.cfg.BaseEnv, s.runnerConfig(), secrets, id, s.cfg.ConfigDir), nil
}

// runnerConfig re-reads config.toml for a start. One it can't use is kept as
// a warning, and the runner starts without its [runner.env].
func (s *Supervisor) runnerConfig() config.Runner {
	cfg, err := s.cfg.LoadConfig()
	if err == nil {
		err = cfg.RunnerError()
	}
	s.lastConfigWarning = ""
	if err != nil {
		s.lastConfigWarning = fmt.Sprintf("config.toml: %v", err)
		s.log.Warn("config.toml can't be used for a runner", "error", err)
		return config.Runner{}
	}
	return cfg.Runner
}

// awaitStatus waits up to StartWait for pid to write status.json.
func (s *Supervisor) awaitStatus(files runner.Files, pid int) error {
	deadline := time.NewTimer(s.cfg.StartWait)
	defer deadline.Stop()
	ticker := time.NewTicker(stepInterval)
	defer ticker.Stop()
	for {
		if st, err := files.ReadStatus(); err == nil && st.PID == pid {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("no status.json from pid %d within %s", pid, s.cfg.StartWait)
		case <-ticker.C:
		}
	}
}

// runnerFailed is the answer for a runner that didn't start.
func runnerFailed(files runner.Files, cause error) error {
	return &RemoteError{
		Code:    "runner_failed",
		Message: fmt.Sprintf("runner %s didn't start: %v", files.ID, cause),
		Details: map[string]any{"log": files.Log(), "stderr": logTail(files.Log())},
	}
}

// logTail is the end of the log at path, for a runner_failed answer.
func logTail(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // G703: path is under the state directory, and the workspace id is one path element.
	if err != nil {
		return ""
	}
	if len(data) > logTailBytes {
		data = data[len(data)-logTailBytes:]
	}
	return string(data)
}

// readSession reads workspace id's run.toml. An id that isn't one path element,
// or has no runner session, is workspace_not_found.
func (s *Supervisor) readSession(id string) (runner.Run, error) {
	if !ValidWorkspaceID(id) {
		return runner.Run{}, sessionError(id, fs.ErrNotExist)
	}
	run, err := s.files(id).ReadRun()
	if err != nil {
		return runner.Run{}, sessionError(id, err)
	}
	return run, nil
}

// ValidWorkspaceID reports whether id names one state directory.
func ValidWorkspaceID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\`)
}

// sessionError is workspace_not_found when id has no runner session.
func sessionError(id string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return &RemoteError{Code: "workspace_not_found", Message: fmt.Sprintf("workspace %s has no runner session", id)}
	}
	return fmt.Errorf("reading runner session %s: %w", id, err)
}

// Stop ends workspace id's runner. It writes the stop file, interrupts the
// process group, and kills it when it is still alive after StopWait. A runner
// that had already exited is left alone and reports false.
func (s *Supervisor) Stop(_ context.Context, id string) (bool, error) {
	files := s.files(id)
	run, err := s.readSession(id)
	if err != nil {
		return false, err
	}
	s.untrack(id)
	state, pid, _ := files.State(s.cfg.Now(), run.CreatedAt, s.procs)
	switch state {
	case stateIdle:
		return false, nil
	case stateStopped:
		if err := s.finish(files, &run, exitStopped, "stopped after the process had died"); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := files.RequestStop(); err != nil {
		return false, err
	}
	if state == stateWorking {
		s.terminate(files, &run, pid)
	}
	return true, nil
}

// terminate interrupts the runner at pid and waits StopWait for it to exit. One
// still alive then is killed and finalized as stopped.
func (s *Supervisor) terminate(files runner.Files, run *runner.Run, pid int) {
	if err := s.starter.Signal(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		s.log.Warn("interrupting a runner", "session", run.Session, "error", err)
	}
	if s.awaitExit(files, run.CreatedAt) {
		return
	}
	_ = s.starter.Signal(pid, syscall.SIGKILL)
	if err := s.finish(files, run, exitStopped, "killed after the stop wait"); err != nil {
		s.log.Error("finalizing a killed runner", "session", run.Session, "error", err)
	}
}

// awaitExit reports whether the runner leaves the working state within StopWait.
func (s *Supervisor) awaitExit(files runner.Files, recordedAt time.Time) bool {
	deadline := time.NewTimer(s.cfg.StopWait)
	defer deadline.Stop()
	ticker := time.NewTicker(stepInterval)
	defer ticker.Stop()
	for {
		if state, _, _ := files.State(s.cfg.Now(), recordedAt, s.procs); state != stateWorking {
			return true
		}
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

// StopAll interrupts every working runner, or with keep leaves them running.
// It lists the sessions it stopped or kept.
func (s *Supervisor) StopAll(ctx context.Context, keep bool) (stopped, kept []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stopped, kept = []string{}, []string{}
	for id, w := range s.runners {
		state, pid, _ := s.files(id).State(s.cfg.Now(), w.run.CreatedAt, s.procs)
		if state != stateWorking {
			continue
		}
		if keep {
			kept = append(kept, w.session)
			continue
		}
		if err := s.starter.Signal(pid, syscall.SIGTERM); err != nil {
			s.log.WarnContext(ctx, "interrupting a runner at shutdown", "session", w.session, "error", err)
		}
		stopped = append(stopped, w.session)
		delete(s.runners, id)
	}
	return stopped, kept
}

// Idle reports whether no runner is watched.
func (s *Supervisor) Idle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runners) == 0
}

// Runners lists the watched runners, by workspace.
func (s *Supervisor) Runners() []RunnerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RunnerStatus, 0, len(s.runners))
	for _, id := range slices.Sorted(maps.Keys(s.runners)) {
		w := s.runners[id]
		out = append(out, RunnerStatus{
			Workspace: id,
			Session:   w.session,
			PID:       w.pid,
			State:     w.state,
			Restarts:  w.restarts,
			Adopted:   w.adopted,
		})
	}
	return out
}

// Warnings are secrets.env's own warning and the config problem the last
// runner start met.
func (s *Supervisor) Warnings() []string {
	warnings := []string{}
	if _, secretWarnings, err := config.ReadSecrets(s.cfg.ConfigDir); err == nil {
		warnings = append(warnings, secretWarnings...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastConfigWarning != "" {
		warnings = append(warnings, s.lastConfigWarning)
	}
	return warnings
}

// finish ends the run in files with exit and message: it records the verdict
// it has, if any, and writes usage.json and the final status.json.
func (s *Supervisor) finish(files runner.Files, run *runner.Run, exit, message string) error {
	st, err := files.ReadStatus()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	verdict, err := s.recordedVerdict(files, run)
	if err != nil {
		return err
	}
	in := runner.FinalizeInput{
		Verdict:   verdict,
		Steps:     st.Step,
		Restarts:  st.Restarts,
		Usage:     st.Usage,
		Endpoints: st.Endpoints,
		StartedAt: st.ProcessStartedAt,
		EndedAt:   s.cfg.Now(),
	}
	return runner.Finalize(files, run, exit, message, &in, s.cfg.Store)
}

// recordedVerdict is the verdict the run already has: result.toml's with a
// parent, else status.json's. "" when there is none.
func (s *Supervisor) recordedVerdict(files runner.Files, run *runner.Run) (string, error) {
	if run.Parent == "" {
		st, err := files.ReadStatus()
		if errors.Is(err, fs.ErrNotExist) || (err == nil && st.Verdict == nil) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		return *st.Verdict, nil
	}
	root, _, err := s.cfg.Store.Lineage(files.ID)
	if err != nil {
		return "", fmt.Errorf("finding %s's root: %w", files.ID, err)
	}
	rec, err := s.cfg.Store.Read(root, files.ID)
	if err != nil {
		return "", fmt.Errorf("reading %s's result: %w", files.ID, err)
	}
	if rec.Result == nil {
		return "", nil
	}
	return rec.Result.Verdict, nil
}
