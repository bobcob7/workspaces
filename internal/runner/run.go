// Package runner holds a runner session's files and the rules for reading
// them (ADR 0063). internal/daemon starts and stops the processes.
package runner

import (
	"time"

	"github.com/bobcob7/workspaces/internal/config"
)

// SessionPrefix starts every runner session id.
const SessionPrefix = "rn-"

// Run is run.toml (reference.md): what a spawn resolved, fixed for the run
// except for EndpointHistory, which the runner appends to.
type Run struct {
	Version          int             `toml:"version"`
	Session          string          `toml:"session"`
	ModelAlias       string          `toml:"model_alias"`
	Model            string          `toml:"model"`
	Endpoint         string          `toml:"endpoint"`
	URL              string          `toml:"url"`
	API              string          `toml:"api"`
	ContextTokens    int             `toml:"context_tokens"`
	MaxOutput        int             `toml:"max_output"`
	Temperature      float64         `toml:"temperature"`
	Reasoning        string          `toml:"reasoning"`
	RequestTimeout   time.Duration   `toml:"request_timeout"`
	IdleTimeout      time.Duration   `toml:"idle_timeout"`
	Role             string          `toml:"role"`
	Parent           string          `toml:"parent"`
	Pass             []string        `toml:"pass"`
	Fail             []string        `toml:"fail"`
	MaxSteps         int             `toml:"max_steps"`
	Timeout          time.Duration   `toml:"timeout"`
	MaxTokens        int             `toml:"max_tokens"`
	Branch           string          `toml:"branch"`
	MCPServers       []string        `toml:"mcp_servers"`
	MCPTools         []string        `toml:"mcp_tools"`
	MCPDeny          []string        `toml:"mcp_deny"`
	MCPUpfront       int             `toml:"mcp_upfront"`
	UserInstructions bool            `toml:"user_instructions"`
	Prompt           string          `toml:"prompt"`
	TraceID          string          `toml:"trace_id"`
	CreatedAt        time.Time       `toml:"created_at"`
	EndpointAlias    string          `toml:"endpoint_alias"`
	EndpointFlag     string          `toml:"endpoint_flag"`
	EndpointHistory  []EndpointEntry `toml:"endpoint_history"`
}

// EndpointEntry is one [[endpoint_history]] entry.
type EndpointEntry struct {
	Since         time.Time `toml:"since"`
	Step          int       `toml:"step"`
	Reason        string    `toml:"reason"` // spawn | retry | resume
	Endpoint      string    `toml:"endpoint"`
	URL           string    `toml:"url"`
	API           string    `toml:"api"`
	Model         string    `toml:"model"`
	ContextTokens int       `toml:"context_tokens"`
}

// Key is the run's config.RunKey: what its resolution started from.
func (r *Run) Key() config.RunKey {
	key := config.RunKey{ModelAlias: r.ModelAlias, EndpointAlias: r.EndpointAlias, EndpointFlag: r.EndpointFlag}
	if r.ModelAlias == "" {
		key.ModelID = r.Model
	}
	return key
}

// Current is the last endpoint history entry. Without any, it is the
// spawn's resolution, read from the run's own endpoint keys.
func (r *Run) Current() EndpointEntry {
	if len(r.EndpointHistory) == 0 {
		return EndpointEntry{
			Reason:        "spawn",
			Endpoint:      r.Endpoint,
			URL:           r.URL,
			API:           r.API,
			Model:         r.Model,
			ContextTokens: r.ContextTokens,
		}
	}
	return r.EndpointHistory[len(r.EndpointHistory)-1]
}
