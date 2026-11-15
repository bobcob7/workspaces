package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolName(t *testing.T) {
	tests := []struct {
		name   string
		server string
		tool   string
		want   string
	}{
		{"plain names", "metrics-a", "query_logs", "mcp__metrics-a__query_logs"},
		{"other characters become underscores", "my server", "a.b", "mcp__my_server__a_b"},
		{"non-ASCII characters become one underscore each", "é", "x", "mcp_____x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ToolName(tt.server, tt.tool))
		})
	}
	t.Run("a name over 64 characters is shortened with a hash", func(t *testing.T) {
		server := strings.Repeat("s", 40)
		tool := strings.Repeat("t", 40)
		got := ToolName(server, tool)
		require.Len(t, got, maxToolNameLen)
		assert.Equal(t, ToolName(server, tool), got)
		assert.True(t, strings.HasPrefix(got, "mcp__"+server+"__"+strings.Repeat("t", 8)))
		assert.Regexp(t, `_[0-9a-f]{8}$`, got)
	})
}
