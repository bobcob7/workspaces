package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/mcp"
)

func TestStaticToolset(t *testing.T) {
	ts := StaticToolset(fakeTools())
	t.Run("offers its tools unchanged", func(t *testing.T) {
		names := make([]string, 0)
		for _, def := range ts.Definitions() {
			names = append(names, def.Name)
		}
		assert.Equal(t, []string{"write_file", "run", "record_result"}, names)
	})
	t.Run("knows its tools only", func(t *testing.T) {
		assert.True(t, ts.Known("run"))
		assert.False(t, ts.Known("nope"))
	})
	t.Run("calls a tool by name and refuses an unknown one", func(t *testing.T) {
		res, err := ts.Call(t.Context(), "run", `{"command":"true"}`)
		require.NoError(t, err)
		assert.Equal(t, "exit 0", res.Text)
		_, err = ts.Call(context.Background(), "nope", "{}")
		assert.ErrorContains(t, err, `unknown tool "nope"`)
	})
}

// mcpToolSet is n MCP tools on server "misc", except the one at index 20,
// which is query_loki_logs on server "logs".
func mcpToolSet(n int) []mcp.ToolInfo {
	out := make([]mcp.ToolInfo, 0, n)
	for i := range n {
		server, tool, description := "misc", fmt.Sprintf("tool_%02d", i), "Unrelated helper"
		if i == 20 {
			server, tool, description = "logs", "query_loki_logs", "Run a LogQL query over loki logs\nmore detail"
		}
		out = append(out, mcp.ToolInfo{Server: server, Tool: tool, Name: mcp.ToolName(server, tool), Description: description})
	}
	return out
}

func searchNamed(t *testing.T, ts *MCPToolset, query string) tools.Result {
	t.Helper()
	res, err := ts.Call(t.Context(), searchToolName, fmt.Sprintf(`{"query":%q}`, query))
	require.NoError(t, err)
	return res
}

func TestNewToolset(t *testing.T) {
	t.Run("offers no tool_search when every allowed tool fits up front", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(8), 8, &mcpCallerMock{}, nil)
		assert.False(t, ts.Known(searchToolName))
		assert.Len(t, ts.Definitions(), 3+8)
	})
	t.Run("offers tool_search and the first upfront tools", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		assert.True(t, ts.Known(searchToolName))
		assert.Len(t, ts.Definitions(), 3+8+1)
		assert.False(t, ts.Known("mcp__logs__query_loki_logs"))
	})
}

func TestMCPToolset_Definitions(t *testing.T) {
	t.Run("offers built-ins, loaded tools in allowed order, then tool_search", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		defs := ts.Definitions()
		names := make([]string, 0, len(defs))
		for _, def := range defs {
			names = append(names, def.Name)
		}
		assert.Equal(t, "tool_search", names[len(names)-1])
		assert.Equal(t, "write_file", names[0])
		assert.Equal(t, "mcp__misc__tool_00", names[3])
	})
	t.Run("includes a tool once a search loads it", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		searchNamed(t, ts, "loki logs")
		var names []string
		for _, def := range ts.Definitions() {
			names = append(names, def.Name)
		}
		assert.Contains(t, names, "mcp__logs__query_loki_logs")
	})
}

func TestMCPToolset_Call(t *testing.T) {
	t.Run("runs a built-in tool", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(8), 8, &mcpCallerMock{}, nil)
		res, err := ts.Call(t.Context(), "run", `{"command":"true"}`)
		require.NoError(t, err)
		assert.Equal(t, "exit 0", res.Text)
	})
	t.Run("loads the matches of tool_search and reports them", func(t *testing.T) {
		var gotQuery string
		var gotLoaded, gotDropped []string
		onSearch := func(query string, loaded, dropped []string) {
			gotQuery, gotLoaded, gotDropped = query, loaded, dropped
		}
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, onSearch)
		res := searchNamed(t, ts, "loki logs")
		assert.Equal(t, "mcp__logs__query_loki_logs: Run a LogQL query over loki logs", strings.Split(res.Text, "\n")[0])
		assert.Equal(t, "loki logs", gotQuery)
		assert.Equal(t, "mcp__logs__query_loki_logs", gotLoaded[0])
		assert.Empty(t, gotDropped)
	})
	t.Run("answers a search with no match", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		assert.Equal(t, `no tools match "kubernetes"`, searchNamed(t, ts, "kubernetes").Text)
	})
	t.Run("calls a loaded MCP tool and frames its server", func(t *testing.T) {
		mc := &mcpCallerMock{CallFunc: func(_ context.Context, name, arguments string) (mcp.CallResult, error) {
			assert.Equal(t, "mcp__misc__tool_00", name)
			assert.Equal(t, `{"x":1}`, arguments)
			return mcp.CallResult{Text: "done"}, nil
		}}
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, mc, nil)
		res, err := ts.Call(t.Context(), "mcp__misc__tool_00", `{"x":1}`)
		require.NoError(t, err)
		assert.Equal(t, tools.Result{Text: "done", Server: "misc"}, res)
	})
	t.Run("caps a long MCP result", func(t *testing.T) {
		long := strings.Repeat("a", tools.MaxResult+10)
		mc := &mcpCallerMock{CallFunc: func(context.Context, string, string) (mcp.CallResult, error) {
			return mcp.CallResult{Text: long}, nil
		}}
		ts := NewToolset(fakeTools(), mcpToolSet(8), 8, mc, nil)
		res, err := ts.Call(t.Context(), "mcp__misc__tool_00", "{}")
		require.NoError(t, err)
		assert.True(t, res.Truncated)
		assert.Contains(t, res.Text, "[truncated: 10 more bytes]")
	})
	t.Run("reports a tool error as an error with its server", func(t *testing.T) {
		mc := &mcpCallerMock{CallFunc: func(context.Context, string, string) (mcp.CallResult, error) {
			return mcp.CallResult{Text: "bad input", IsError: true}, nil
		}}
		ts := NewToolset(fakeTools(), mcpToolSet(8), 8, mc, nil)
		res, err := ts.Call(t.Context(), "mcp__misc__tool_00", "{}")
		require.EqualError(t, err, "bad input")
		assert.Equal(t, "misc", res.Server)
	})
	t.Run("returns a call failure", func(t *testing.T) {
		mc := &mcpCallerMock{CallFunc: func(context.Context, string, string) (mcp.CallResult, error) {
			return mcp.CallResult{}, errors.New("boom")
		}}
		ts := NewToolset(fakeTools(), mcpToolSet(8), 8, mc, nil)
		_, err := ts.Call(t.Context(), "mcp__misc__tool_00", "{}")
		assert.ErrorContains(t, err, "boom")
	})
	t.Run("refuses a deferred tool not yet loaded", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		_, err := ts.Call(t.Context(), "mcp__logs__query_loki_logs", "{}")
		assert.ErrorContains(t, err, `unknown tool "mcp__logs__query_loki_logs"`)
	})
	t.Run("drops the least recently called tool past 24 loaded", func(t *testing.T) {
		mc := &mcpCallerMock{CallFunc: func(context.Context, string, string) (mcp.CallResult, error) {
			return mcp.CallResult{Text: "ok"}, nil
		}}
		var dropped []string
		onSearch := func(_ string, _, d []string) { dropped = append(dropped, d...) }
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, mc, onSearch)
		_, err := ts.Call(t.Context(), "mcp__misc__tool_00", "{}")
		require.NoError(t, err)
		for i := 8; i < 40; i++ {
			searchNamed(t, ts, fmt.Sprintf("tool_%02d", i))
		}
		require.NotEmpty(t, dropped)
		assert.Equal(t, "mcp__misc__tool_01", dropped[0])
		assert.Len(t, ts.Definitions(), 3+24+1)
	})
}

func TestMCPToolset_Known(t *testing.T) {
	ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
	t.Run("knows built-ins and tool_search", func(t *testing.T) {
		assert.True(t, ts.Known("run"))
		assert.True(t, ts.Known(searchToolName))
	})
	t.Run("knows an up-front MCP tool but not a deferred one", func(t *testing.T) {
		assert.True(t, ts.Known("mcp__misc__tool_00"))
		assert.False(t, ts.Known("mcp__logs__query_loki_logs"))
	})
	t.Run("knows a deferred tool once loaded", func(t *testing.T) {
		searchNamed(t, ts, "loki logs")
		assert.True(t, ts.Known("mcp__logs__query_loki_logs"))
	})
}

func TestMCPToolset_Deferred(t *testing.T) {
	t.Run("lists the tools not loaded by server", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(40), 8, &mcpCallerMock{}, nil)
		lines := strings.Split(ts.Deferred(), "\n")
		assert.Equal(t, "misc: tool_08, tool_09, tool_10, tool_11, tool_12, tool_13, tool_14, tool_15, tool_16, tool_17, tool_18, tool_19, tool_21, tool_22, tool_23, tool_24, tool_25, tool_26, tool_27, tool_28, tool_29, tool_30, tool_31, tool_32, tool_33, tool_34, tool_35, tool_36, tool_37, tool_38, tool_39", lines[0])
		assert.Equal(t, "logs: query_loki_logs", lines[1])
	})
	t.Run("cuts the list at 2 KiB on a line boundary", func(t *testing.T) {
		ts := NewToolset(fakeTools(), mcpToolSet(400), 8, &mcpCallerMock{}, nil)
		deferred := ts.Deferred()
		assert.LessOrEqual(t, len(deferred), 2<<10)
		assert.NotEmpty(t, deferred)
		assert.False(t, strings.HasSuffix(deferred, ","))
	})
}
