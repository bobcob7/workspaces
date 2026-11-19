package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// Status is one server's line in status.json's mcp.
type Status struct {
	Server string  `json:"server"`
	State  string  `json:"state"` // starting, connected, failed
	Tools  int     `json:"tools"`
	Loaded int     `json:"loaded"`
	Error  *string `json:"error"`
}

// Manager starts a run's servers in parallel and routes calls by tool name.
type Manager struct {
	entries []managerEntry
	routes  map[string]route
}

// managerEntry is one configured server: started, or failed with err.
type managerEntry struct {
	name   string
	server *Server
	err    string
}

type route struct {
	server *Server
	tool   string
}

type startResult struct {
	name    string
	server  *Server
	secrets []string
	err     error
}

// StartAll starts every def in parallel. A server that fails is logged and
// left out; the returned secrets are every expanded value, for the redactor.
func StartAll(ctx context.Context, defs []ServerDef, opts StartOptions, log *slog.Logger) (*Manager, []string) {
	results := make([]startResult, len(defs))
	var wg sync.WaitGroup
	for i := range defs {
		wg.Go(func() {
			results[i] = startOne(ctx, &defs[i], opts, log)
		})
	}
	wg.Wait()
	return newManager(results)
}

func startOne(ctx context.Context, def *ServerDef, opts StartOptions, log *slog.Logger) startResult {
	server, secrets, err := Start(ctx, *def, opts)
	if err != nil {
		log.WarnContext(ctx, "MCP server failed to start", "server", def.Name, "error", err)
		return startResult{name: def.Name, secrets: secrets, err: err}
	}
	return startResult{name: def.Name, server: server, secrets: secrets}
}

func newManager(results []startResult) (*Manager, []string) {
	m := &Manager{routes: make(map[string]route)}
	var secrets []string
	for _, r := range results {
		secrets = append(secrets, r.secrets...)
		if r.err != nil {
			m.entries = append(m.entries, managerEntry{name: r.name, err: r.err.Error()})
			continue
		}
		m.entries = append(m.entries, managerEntry{name: r.name, server: r.server})
		for _, tool := range r.server.Tools() {
			m.routes[tool.Name] = route{server: r.server, tool: tool.Tool}
		}
	}
	return m, secrets
}

// Tools returns every connected server's tools, in server order.
func (m *Manager) Tools() []ToolInfo {
	var tools []ToolInfo
	for _, e := range m.entries {
		if e.server != nil {
			tools = append(tools, e.server.Tools()...)
		}
	}
	return tools
}

// Call runs the tool with the given offered name.
func (m *Manager) Call(ctx context.Context, name, arguments string) (CallResult, error) {
	r, ok := m.routes[name]
	if !ok {
		return CallResult{}, fmt.Errorf("unknown tool %q", name)
	}
	return r.server.Call(ctx, r.tool, arguments)
}

// Status is one line per configured server, in config order.
func (m *Manager) Status() []Status {
	out := make([]Status, 0, len(m.entries))
	for _, e := range m.entries {
		if e.server == nil {
			out = append(out, Status{Server: e.name, State: stateFailed, Error: errorField(e.err)})
			continue
		}
		out = append(out, e.server.status())
	}
	return out
}

// Close stops every started server.
func (m *Manager) Close() error {
	var errs []error
	for _, e := range m.entries {
		if e.server != nil {
			errs = append(errs, e.server.Close())
		}
	}
	return errors.Join(errs...)
}
