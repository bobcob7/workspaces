package runner

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/gate"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/llm/providers"
	"github.com/bobcob7/workspaces/internal/mcp"
)

// mcpSet is a run's started MCP servers and the tools they offer.
type mcpSet struct {
	manager  *mcp.Manager
	toolset  *agent.MCPToolset
	allowed  []mcp.ToolInfo
	serverOf map[string]string // every connected tool, by offered name
	calls    map[string]int    // calls per server
	failed   []string          // servers that didn't start
	status   []mcp.Status
	// transports is each selected server's transport, for the transcript.
	transports map[string]string
}

// statuses is each server's line for status.json, with the tools loaded.
func (m *mcpSet) statuses() []mcp.Status {
	loaded := m.toolset.LoadedByServer()
	out := make([]mcp.Status, len(m.status))
	copy(out, m.status)
	for i := range out {
		out[i].Loaded = loaded[out[i].Server]
	}
	return out
}

// toolset builds the run's tools: the built-ins, the MCP tools the run
// allows, and tool_search, traced and with MCP errors logged.
func (s *session) toolset(ctx context.Context) (agent.Toolset, error) {
	toolEnv := tools.Environ(s.in.Environ, s.keepGit, secretNames(s.in.Environ))
	builtin := tools.New(tools.Config{
		Dir:      s.in.workDir(),
		Root:     s.in.Workspace,
		Branch:   s.run.Branch,
		Verdicts: append(append([]string{}, s.run.Pass...), s.run.Fail...),
		Recorder: &verdictRecorder{files: s.in.Files, run: s.run, store: s.in.Store},
		Shell:    gate.New(toolEnv),
	})
	mc, err := s.startMCP(ctx, builtin)
	if err != nil {
		return nil, err
	}
	s.mcp = mc
	logged := &loggedToolset{Toolset: mc.toolset, s: s}
	return s.tel.Toolset(logged, func(name string) string { return mc.serverOf[name] }), nil
}

// startMCP starts the run's servers and writes one mcp_start event per
// server, and an mcp_error for each that failed to start.
func (s *session) startMCP(ctx context.Context, builtin []tools.Tool) (*mcpSet, error) {
	defs, err := s.mcpDefs()
	if err != nil {
		return nil, err
	}
	manager, secrets := mcp.StartAll(ctx, defs, mcp.StartOptions{Dir: s.in.Files.Dir, Environ: s.in.Environ, Stderr: s.logOut}, s.log)
	s.redactor.Add(secrets...)
	all := manager.Tools()
	allowed := mcp.Filter(all, s.run.MCPTools, s.run.MCPDeny)
	mc := &mcpSet{
		manager:    manager,
		allowed:    allowed,
		serverOf:   serverOfTools(all),
		calls:      make(map[string]int),
		status:     manager.Status(),
		transports: transportsOf(defs),
	}
	mc.toolset = agent.NewToolset(builtin, allowed, min(max(s.run.MCPUpfront, 0), maxUpfront), manager, s.searched)
	s.recordStarts(mc, allowed)
	return mc, nil
}

func transportsOf(defs []mcp.ServerDef) map[string]string {
	out := make(map[string]string, len(defs))
	for _, d := range defs {
		out[d.Name] = d.Type
	}
	return out
}

// mcpDefs are the definitions of the run's servers, found in the workspace's
// .mcp.json and the user's Claude Code config. A run without servers reads
// none.
func (s *session) mcpDefs() ([]mcp.ServerDef, error) {
	if len(s.run.MCPServers) == 0 {
		return nil, nil
	}
	defs, warnings, err := mcp.LoadDefs(s.in.workDir(), mcp.ClaudeConfigPath(s.in.Environ))
	if err != nil {
		return nil, fmt.Errorf("loading MCP servers: %w", err)
	}
	for _, w := range warnings {
		s.log.Warn("MCP definition", "warning", w)
	}
	selected, missing := mcp.Select(defs, s.run.MCPServers)
	for _, w := range missing {
		s.log.Warn("MCP selection", "warning", w)
	}
	return selected, nil
}

func (s *session) recordStarts(mc *mcpSet, allowed []mcp.ToolInfo) {
	counts := make(map[string]int)
	for _, t := range allowed {
		counts[t.Server]++
	}
	for _, st := range mc.status {
		ok := st.State == stateConnected
		s.writeEvent(0, "mcp_start", mcpStartEvent{Server: st.Server, Transport: mc.transports[st.Server], OK: ok, Tools: st.Tools, Allowed: counts[st.Server], Error: st.Error})
		if !ok {
			mc.failed = append(mc.failed, st.Server)
			s.writeEvent(0, "mcp_error", mcpErrorEvent{Server: st.Server, Error: errorText(st.Error)})
		}
	}
}

// searched is the transcript's record of a tool_search.
func (s *session) searched(query string, loaded, dropped []string) {
	s.writeEvent(s.step, "tool_search", toolSearchEvent{Query: query, Loaded: loaded, Dropped: dropped})
}

func serverOfTools(found []mcp.ToolInfo) map[string]string {
	out := make(map[string]string, len(found))
	for _, t := range found {
		out[t.Name] = t.Server
	}
	return out
}

// stateConnected is a server's state once it started (mcp.Status.State).
const stateConnected = "connected"

func errorText(msg *string) string {
	if msg == nil {
		return ""
	}
	return *msg
}

// loggedToolset counts each MCP call and logs a failed one to the
// transcript as mcp_error. Built-in calls pass through unlogged.
type loggedToolset struct {
	agent.Toolset
	s *session
}

// Call runs the tool and, for an MCP tool, counts it and logs a Go error.
func (t *loggedToolset) Call(ctx context.Context, name, arguments string) (tools.Result, error) {
	res, err := t.Toolset.Call(ctx, name, arguments)
	server := t.s.mcp.serverOf[name]
	if server == "" {
		return res, err
	}
	t.s.mcp.calls[server]++
	if err != nil {
		tool := name
		t.s.writeEvent(t.s.step, "mcp_error", mcpErrorEvent{Server: server, Tool: &tool, Error: err.Error()})
	}
	return res, err
}

// verdictRecorder is the record_result tool's recorder.
type verdictRecorder struct {
	files Files
	run   Run
	store resultStore
}

// Record writes the verdict and report through Files.Record.
func (r *verdictRecorder) Record(_ context.Context, verdict string, report []byte) error {
	return r.files.Record(&r.run, r.store, verdict, report)
}

// newProvider is the provider for the endpoint's api.
func newProvider(ep *llm.Endpoint, log *slog.Logger) (llm.Provider, error) {
	p, err := providers.New(*ep, http.DefaultClient, log)
	if err != nil {
		return nil, fmt.Errorf("endpoint %s: %w", ep.Name, err)
	}
	return p, nil
}

type mcpStartEvent struct {
	Server    string  `json:"server"`
	Transport string  `json:"transport"`
	OK        bool    `json:"ok"`
	Tools     int     `json:"tools"`
	Allowed   int     `json:"allowed"`
	Error     *string `json:"error"`
}

type mcpErrorEvent struct {
	Server string  `json:"server"`
	Tool   *string `json:"tool"`
	Error  string  `json:"error"`
}

type toolSearchEvent struct {
	Query   string   `json:"query"`
	Loaded  []string `json:"loaded"`
	Dropped []string `json:"dropped"`
}
