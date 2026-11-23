package daemon

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/bobcob7/workspaces/internal/machine"
)

//go:generate go tool moq -out moq_test.go . RunnerSupervisor processReader processStarter RunnerRecords EnvironmentHost

// Errors the daemon's callers test with errors.Is.
var (
	ErrRunning        = errors.New("a ws daemon already runs for this config directory")
	ErrNotRunning     = errors.New("no ws daemon is running")
	ErrUnavailable    = errors.New("the ws daemon is unavailable")
	ErrNotSupervising = errors.New("this daemon doesn't supervise runners")
)

// RunnerSupervisor is what the server serves: Supervisor, or NopSupervisor
// for a daemon that starts no runners.
type RunnerSupervisor interface {
	// Reconcile adopts and resumes the runners found before the daemon
	// listens.
	Reconcile(ctx context.Context) error
	// Watch resumes or finalizes dead runners until ctx ends.
	Watch(ctx context.Context)
	Runners() []RunnerStatus
	Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error)
	Start(ctx context.Context, id string, resume bool) (int, error)
	// Stop reports false when the runner had already exited.
	Stop(ctx context.Context, id string) (bool, error)
	StopAll(ctx context.Context, keep bool) (stopped, kept []string)
	Idle() bool // no live runner
	// Warnings are the configuration problems the daemon reports in its status.
	Warnings() []string
}

// EnvironmentHost is what serves remote environments in the daemon (ADR
// 0074): the controller, or NopEnvironments.
type EnvironmentHost interface {
	// Start listens for environments until Close. A failure is kept as a
	// warning; the daemon keeps running.
	Start(ctx context.Context) error
	// Reload rereads the environments' state.
	Reload(ctx context.Context) error
	Status() EnvironmentsStatus
	Warnings() []string
	Close() error
}

// processReader reads a process's start time, to tell a live runner from a
// reused pid.
type processReader interface {
	StartTime(pid int) (time.Time, error)
}

// processStarter starts and signals runner processes.
type processStarter interface {
	// Start runs exe with args in its own session, stdin and stdout on
	// /dev/null, stderr appended to logPath, working directory dir and
	// environment env. It returns the process id, which is also its group id.
	Start(exe string, args []string, dir, logPath string, env []string) (pid int, err error)
	Signal(pgid int, sig syscall.Signal) error
}

// RunnerRecords is what a runner's finalizer records through: machine.Store
// plus the lineage ws machine result computes.
type RunnerRecords interface {
	Dir(root, child string) string
	Read(root, child string) (machine.Record, error)
	WriteResult(root, child string, r *machine.Result, report []byte) error
	Lineage(child string) (root string, ancestors []string, err error)
}

// RunnerStatus is one runner the daemon watches.
type RunnerStatus struct {
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	PID       int    `json:"pid"`
	State     string `json:"state"`
	Restarts  int    `json:"restarts"`
	Adopted   bool   `json:"adopted"`
}

// PreflightRequest names a model to check; each empty field is the default.
// Check selects `ws runner check`'s defaults over the role's.
type PreflightRequest struct {
	Role     string `json:"role"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Check    bool   `json:"check"`
}

// PreflightResponse is `ws runner check`'s object (reference.md).
type PreflightResponse struct {
	Endpoint      string   `json:"endpoint"`
	URL           string   `json:"url"`
	API           string   `json:"api"`
	Probe         string   `json:"probe"`
	Model         *string  `json:"model"` // null for an endpoint-only check
	Version       *string  `json:"version"`
	Capabilities  []string `json:"capabilities"`
	ContextTokens *int     `json:"context_tokens"` // null for an endpoint-only check
	Models        []string `json:"models"`
	TokenSource   *string  `json:"token_source"`
	Warnings      []string `json:"warnings"`
}

// NopSupervisor is the RunnerSupervisor of a daemon with no runners: every
// runner request is ErrNotSupervising.
type NopSupervisor struct{}

// Reconcile has no runners to adopt.
func (NopSupervisor) Reconcile(context.Context) error { return nil }

// Watch returns at once: there is nothing to watch.
func (NopSupervisor) Watch(context.Context) {}

// Runners returns no runners.
func (NopSupervisor) Runners() []RunnerStatus { return []RunnerStatus{} }

// Preflight is ErrNotSupervising.
func (NopSupervisor) Preflight(context.Context, PreflightRequest) (PreflightResponse, error) {
	return PreflightResponse{}, ErrNotSupervising
}

// Start is ErrNotSupervising.
func (NopSupervisor) Start(context.Context, string, bool) (int, error) {
	return 0, ErrNotSupervising
}

// Stop is ErrNotSupervising.
func (NopSupervisor) Stop(context.Context, string) (bool, error) { return false, ErrNotSupervising }

// StopAll has no runner to stop or keep.
func (NopSupervisor) StopAll(context.Context, bool) (stopped, kept []string) { return nil, nil }

// Idle is always true: there is no runner.
func (NopSupervisor) Idle() bool { return true }

// Warnings has none.
func (NopSupervisor) Warnings() []string { return []string{} }
