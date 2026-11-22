package daemon

import (
	"context"
	"time"
)

// EnvironmentsStatus is what GET /v1/status reports of remote environments
// (ADR 0074).
type EnvironmentsStatus struct {
	Listening    bool                  `json:"listening"`
	Listen       string                `json:"listen"`
	EnrollListen string                `json:"enroll_listen"`
	Environments []EnvironmentPresence `json:"environments"`
}

// EnvironmentPresence is one environment and what the daemon has heard from it.
type EnvironmentPresence struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`  // pending, enrolled
	Status   string            `json:"status"` // pending, online, offline
	LastSeen *time.Time        `json:"last_seen"`
	Hello    *EnvironmentHello `json:"hello"`
}

// EnvironmentHello is an online environment's hello, without the CA data.
type EnvironmentHello struct {
	WSVersion     string  `json:"ws_version"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	Claude        string  `json:"claude"`
	ClaudeVersion *string `json:"claude_version"`
	CertNotAfter  string  `json:"cert_not_after"`
}

// reloadResponse is POST /v1/environments/reload's answer.
type reloadResponse struct {
	Reloaded bool `json:"reloaded"`
}

// NopEnvironments serves no environments; Reason, when set, is its one warning.
type NopEnvironments struct{ Reason string }

// Start does nothing.
func (NopEnvironments) Start(context.Context) error { return nil }

// Reload does nothing.
func (NopEnvironments) Reload(context.Context) error { return nil }

// Status reports no listeners and no environments.
func (NopEnvironments) Status() EnvironmentsStatus {
	return EnvironmentsStatus{Environments: []EnvironmentPresence{}}
}

// Warnings is Reason, or none.
func (n NopEnvironments) Warnings() []string {
	if n.Reason == "" {
		return []string{}
	}
	return []string{n.Reason}
}

// Close does nothing.
func (NopEnvironments) Close() error { return nil }
