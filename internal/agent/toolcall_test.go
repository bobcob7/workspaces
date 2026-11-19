package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWithToolCallID(t *testing.T) {
	t.Run("carries the id to ToolCallID", func(t *testing.T) {
		ctx := WithToolCallID(t.Context(), "call_1")
		assert.Equal(t, "call_1", ToolCallID(ctx))
	})
	t.Run("keeps the parent context's values", func(t *testing.T) {
		parent := WithToolCallID(t.Context(), "call_1")
		child := WithToolCallID(parent, "call_2")
		assert.Equal(t, "call_1", ToolCallID(parent))
		assert.Equal(t, "call_2", ToolCallID(child))
	})
}

func TestToolCallID(t *testing.T) {
	t.Run("is empty without an id", func(t *testing.T) {
		assert.Empty(t, ToolCallID(t.Context()))
	})
}
