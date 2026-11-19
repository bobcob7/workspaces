package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
)

func noServer(string) string {
	return ""
}

func TestTracedToolset_Call(t *testing.T) {
	t.Run("runs the tool in an execute_tool span with the call id", func(t *testing.T) {
		rec := newRecorder()
		inner := &ToolsetMock{CallFunc: func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Text: "ok"}, nil
		}}
		ts := rec.tel.Toolset(inner, noServer)
		res, err := ts.Call(agent.WithToolCallID(t.Context(), "call_7"), "read_file", `{"path":"a"}`)
		require.NoError(t, err)
		assert.Equal(t, "ok", res.Text)
		span := endedSpan(t, rec.spans, "execute_tool read_file")
		assert.Equal(t, trace.SpanKindInternal, span.SpanKind())
		assert.Equal(t, "read_file", attrOf(span.Attributes(), "gen_ai.tool.name").AsString())
		assert.Equal(t, "call_7", attrOf(span.Attributes(), "gen_ai.tool.call.id").AsString())
	})
	t.Run("gives an MCP tool its server", func(t *testing.T) {
		rec := newRecorder()
		inner := &ToolsetMock{CallFunc: func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{}, nil
		}}
		ts := rec.tel.Toolset(inner, func(string) string { return "docs" })
		_, err := ts.Call(t.Context(), "mcp__docs__search", "{}")
		require.NoError(t, err)
		span := endedSpan(t, rec.spans, "execute_tool mcp__docs__search")
		assert.Equal(t, "docs", attrOf(span.Attributes(), AttrMCPServer).AsString())
	})
	t.Run("marks a failed call without its message", func(t *testing.T) {
		rec := newRecorder()
		inner := &ToolsetMock{CallFunc: func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{}, errBoom
		}}
		ts := rec.tel.Toolset(inner, noServer)
		_, err := ts.Call(t.Context(), "run", "{}")
		require.ErrorIs(t, err, errBoom)
		span := endedSpan(t, rec.spans, "execute_tool run")
		assert.Equal(t, errorToolCall, attrOf(span.Attributes(), "error.type").AsString())
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.Equal(t, errorToolCall, span.Status().Description)
	})
}

func TestTracedToolset_attributes(t *testing.T) {
	t.Run("leaves the server out for a built-in tool", func(t *testing.T) {
		ts := &tracedToolset{serverOf: noServer}
		for _, kv := range ts.attributes(t.Context(), "run") {
			assert.NotEqual(t, AttrMCPServer, kv.Key)
		}
	})
}
