package daemon

import (
	"context"
	"errors"
)

//go:generate go tool moq -out moq_test.go . Supervisor

// Errors the daemon's callers test with errors.Is.
var (
	ErrRunning        = errors.New("a ws daemon already runs for this config directory")
	ErrNotRunning     = errors.New("no ws daemon is running")
	ErrUnavailable    = errors.New("the ws daemon is unavailable")
	ErrNotSupervising = errors.New("this daemon doesn't supervise runners")
)

// Supervisor is what the server serves; T18 implements it. Until then
// NopSupervisor answers status with no runners and runner requests with
// ErrNotSupervising.
type Supervisor interface {
	Runners() []RunnerStatus
	Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error)
	Start(ctx context.Context, id string, resume bool) (int, error)
	Stop(ctx context.Context, id string) error
	StopAll(ctx context.Context, keep bool) (stopped, kept []string)
	Idle() bool // no live runner
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
type PreflightRequest struct {
	Role     string `json:"role"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
}

// PreflightResponse is `ws runner check`'s object (reference.md).
type PreflightResponse struct {
	Endpoint      string   `json:"endpoint"`
	URL           string   `json:"url"`
	API           string   `json:"api"`
	Probe         string   `json:"probe"`
	Model         string   `json:"model"`
	Version       *string  `json:"version"`
	Capabilities  []string `json:"capabilities"`
	ContextTokens int      `json:"context_tokens"`
	Models        []string `json:"models"`
	TokenSource   *string  `json:"token_source"`
	Warnings      []string `json:"warnings"`
}

// NopSupervisor is the Supervisor until T18's supervisor replaces it: no
// runners, and every runner request is ErrNotSupervising.
type NopSupervisor struct{}

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
func (NopSupervisor) Stop(context.Context, string) error { return ErrNotSupervising }

// StopAll has no runner to stop or keep.
func (NopSupervisor) StopAll(context.Context, bool) (stopped, kept []string) { return nil, nil }

// Idle is always true: there is no runner.
func (NopSupervisor) Idle() bool { return true }
