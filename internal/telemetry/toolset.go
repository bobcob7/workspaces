package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
)

// errorToolCall is error.type for a failed tool call. The tool's message is
// left out: it can hold tool output.
const errorToolCall = "tool_error"

type tracedToolset struct {
	agent.Toolset
	t        *Telemetry
	serverOf func(name string) string
}

// Toolset wraps ts so that each Call is an "execute_tool <name>" span. serverOf
// names the MCP server of a tool, or "" for a built-in.
func (t *Telemetry) Toolset(ts agent.Toolset, serverOf func(name string) string) agent.Toolset {
	return &tracedToolset{Toolset: ts, t: t, serverOf: serverOf}
}

// Call runs the tool inside its span, and marks the span when the call fails.
func (s *tracedToolset) Call(ctx context.Context, name, arguments string) (tools.Result, error) {
	ctx, span := s.t.tracer.Start(ctx, "execute_tool "+name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(s.attributes(ctx, name)...),
	)
	res, err := s.Toolset.Call(ctx, name, arguments)
	if err != nil {
		span.SetAttributes(semconv.ErrorTypeKey.String(errorToolCall))
		span.SetStatus(codes.Error, errorToolCall)
	}
	span.End()
	return res, err
}

func (s *tracedToolset) attributes(ctx context.Context, name string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.GenAIOperationNameKey.String("execute_tool"),
		semconv.GenAIToolName(name),
		semconv.GenAIToolCallID(agent.ToolCallID(ctx)),
	}
	if server := s.serverOf(name); server != "" {
		attrs = append(attrs, AttrMCPServer.String(server))
	}
	return attrs
}
