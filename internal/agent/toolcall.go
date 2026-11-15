package agent

import "context"

type toolCallIDKey struct{}

// WithToolCallID returns ctx carrying the id of the model's tool call, so a
// Toolset can tell which call it is running (telemetry names its span by it).
func WithToolCallID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, toolCallIDKey{}, id)
}

// ToolCallID is the id WithToolCallID stored in ctx, or "" when there is none.
func ToolCallID(ctx context.Context) string {
	id, _ := ctx.Value(toolCallIDKey{}).(string)
	return id
}
