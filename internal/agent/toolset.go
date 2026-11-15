package agent

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/mcp"
)

// StaticToolset offers ts unchanged.
func StaticToolset(ts []tools.Tool) Toolset {
	return staticToolset{items: slices.Clone(ts)}
}

type staticToolset struct {
	items []tools.Tool
}

func (s staticToolset) Definitions() []llm.Tool {
	defs := make([]llm.Tool, 0, len(s.items))
	for _, t := range s.items {
		defs = append(defs, llm.Tool{Name: t.Name, Description: t.Description, Parameters: t.Parameters})
	}
	return defs
}

func (s staticToolset) Call(ctx context.Context, name, arguments string) (tools.Result, error) {
	t, ok := s.find(name)
	if !ok {
		return tools.Result{}, fmt.Errorf("unknown tool %q", name)
	}
	return t.Call(ctx, arguments)
}

func (s staticToolset) Known(name string) bool {
	_, ok := s.find(name)
	return ok
}

func (s staticToolset) find(name string) (tools.Tool, bool) {
	for _, t := range s.items {
		if t.Name == name {
			return t, true
		}
	}
	return tools.Tool{}, false
}

const (
	searchToolName   = "tool_search"
	maxLoadedMCP     = 24
	maxSearchResults = 5
	maxDeferredBytes = 2 << 10
)

// MCPToolset offers the built-in tools, the MCP tools loaded so far, and
// tool_search when some allowed MCP tools are deferred.
type MCPToolset struct {
	builtin    []tools.Tool
	allowed    []mcp.ToolInfo
	searchable bool
	mc         mcpCaller
	onSearch   func(query string, loaded, dropped []string)
	lastUsed   map[string]int // loaded MCP tools by offered name, with their last use
	clock      int
}

// NewToolset offers builtin, the first upfront of allowed, and, when
// len(allowed) > upfront, tool_search. Loaded tools stay; past 24 MCP
// tools the least recently called loaded one is dropped. onSearch gets
// each search for the transcript.
func NewToolset(builtin []tools.Tool, allowed []mcp.ToolInfo, upfront int, mc mcpCaller, onSearch func(query string, loaded, dropped []string)) *MCPToolset {
	t := &MCPToolset{
		builtin:    slices.Clone(builtin),
		allowed:    slices.Clone(allowed),
		searchable: len(allowed) > upfront,
		mc:         mc,
		onSearch:   onSearch,
		lastUsed:   make(map[string]int),
	}
	for _, tool := range t.allowed[:min(upfront, len(t.allowed))] {
		t.load(tool.Name)
	}
	return t
}

// Definitions returns the built-ins, the loaded MCP tools in allowed order,
// and tool_search.
func (t *MCPToolset) Definitions() []llm.Tool {
	defs := make([]llm.Tool, 0, len(t.builtin)+len(t.lastUsed)+1)
	for _, b := range t.builtin {
		defs = append(defs, llm.Tool{Name: b.Name, Description: b.Description, Parameters: b.Parameters})
	}
	for _, tool := range t.allowed {
		if _, ok := t.lastUsed[tool.Name]; ok {
			defs = append(defs, llm.Tool{Name: tool.Name, Description: tool.Description, Parameters: schemaOf(&tool)})
		}
	}
	if t.searchable {
		defs = append(defs, searchDefinition())
	}
	return defs
}

// Call runs a built-in, a loaded MCP tool, or tool_search.
func (t *MCPToolset) Call(ctx context.Context, name, arguments string) (tools.Result, error) {
	if b, ok := t.builtinNamed(name); ok {
		return b.Call(ctx, arguments)
	}
	if name == searchToolName && t.searchable {
		return t.search(arguments)
	}
	if !t.loaded(name) {
		return tools.Result{}, fmt.Errorf("unknown tool %q", name)
	}
	return t.callMCP(ctx, name, arguments)
}

// Known reports whether Call accepts name. A deferred tool not yet loaded is
// unknown.
func (t *MCPToolset) Known(name string) bool {
	if _, ok := t.builtinNamed(name); ok {
		return true
	}
	if name == searchToolName {
		return t.searchable
	}
	return t.loaded(name)
}

// Deferred lists the allowed MCP tools not loaded, one "server: tool, tool"
// line per server, cut at 2 KiB on a tool boundary.
func (t *MCPToolset) Deferred() string {
	var servers []string
	byServer := make(map[string][]string)
	for _, tool := range t.allowed {
		if t.loaded(tool.Name) {
			continue
		}
		if _, ok := byServer[tool.Server]; !ok {
			servers = append(servers, tool.Server)
		}
		byServer[tool.Server] = append(byServer[tool.Server], tool.Tool)
	}
	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		lines = append(lines, server+": "+strings.Join(byServer[server], ", "))
	}
	return cutList(strings.Join(lines, "\n"), maxDeferredBytes)
}

// cutList cuts s to limit bytes, back to the last tool or line boundary.
func cutList(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	s = s[:limit]
	if i := strings.LastIndexAny(s, ",\n"); i >= 0 {
		return s[:i]
	}
	return ""
}

// search loads the matches and answers with their names and first lines.
func (t *MCPToolset) search(arguments string) (tools.Result, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return tools.Result{}, fmt.Errorf("decoding tool_search arguments: %w", err)
	}
	matches := mcp.Search(t.allowed, args.Query, maxSearchResults)
	var loaded, dropped []string
	for _, tool := range matches {
		if t.loaded(tool.Name) {
			continue
		}
		loaded = append(loaded, tool.Name)
		dropped = append(dropped, t.load(tool.Name)...)
	}
	if t.onSearch != nil {
		t.onSearch(args.Query, loaded, dropped)
	}
	return tools.Result{Text: searchAnswer(args.Query, matches)}, nil
}

func searchAnswer(query string, matches []mcp.ToolInfo) string {
	if len(matches) == 0 {
		return fmt.Sprintf("no tools match %q", query)
	}
	lines := make([]string, 0, len(matches))
	for _, tool := range matches {
		first, _, _ := strings.Cut(tool.Description, "\n")
		lines = append(lines, tool.Name+": "+first)
	}
	return strings.Join(lines, "\n")
}

// load marks name as loaded and returns the tools dropped to keep the cap.
func (t *MCPToolset) load(name string) []string {
	t.touch(name)
	var dropped []string
	for len(t.lastUsed) > maxLoadedMCP {
		oldest := t.leastRecentlyUsed()
		delete(t.lastUsed, oldest)
		dropped = append(dropped, oldest)
	}
	return dropped
}

func (t *MCPToolset) touch(name string) {
	t.clock++
	t.lastUsed[name] = t.clock
}

// leastRecentlyUsed is the loaded tool with the oldest use; uses are unique.
func (t *MCPToolset) leastRecentlyUsed() string {
	oldest := ""
	for name, used := range t.lastUsed {
		if oldest == "" || used < t.lastUsed[oldest] {
			oldest = name
		}
	}
	return oldest
}

func (t *MCPToolset) callMCP(ctx context.Context, name, arguments string) (tools.Result, error) {
	t.touch(name)
	server := t.serverOf(name)
	res, err := t.mc.Call(ctx, name, arguments)
	if err != nil {
		return tools.Result{Server: server}, err
	}
	text, cut := tools.CapText(res.Text)
	out := tools.Result{Text: text, Truncated: cut, Server: server}
	if res.IsError {
		return out, errors.New(text)
	}
	return out, nil
}

func (t *MCPToolset) builtinNamed(name string) (tools.Tool, bool) {
	for _, b := range t.builtin {
		if b.Name == name {
			return b, true
		}
	}
	return tools.Tool{}, false
}

func (t *MCPToolset) loaded(name string) bool {
	_, ok := t.lastUsed[name]
	return ok
}

func (t *MCPToolset) serverOf(name string) string {
	for _, tool := range t.allowed {
		if tool.Name == name {
			return tool.Server
		}
	}
	return ""
}

func schemaOf(tool *mcp.ToolInfo) map[string]any {
	if tool.Schema == nil {
		return map[string]any{"type": "object"}
	}
	return tool.Schema
}

func searchDefinition() llm.Tool {
	return llm.Tool{
		Name:        searchToolName,
		Description: "Find more tools by a few words; matching tools become callable.",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
			"required":   []string{"query"},
		},
	}
}

// LoadedByServer counts the loaded MCP tools of each server, for status.json.
func (t *MCPToolset) LoadedByServer() map[string]int {
	counts := make(map[string]int)
	for name := range t.lastUsed {
		counts[t.serverOf(name)]++
	}
	return counts
}
