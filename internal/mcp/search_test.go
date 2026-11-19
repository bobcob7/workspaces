package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSearch(t *testing.T) {
	tools := []ToolInfo{
		{Name: "mcp__metrics__query_prometheus", Description: "Run a PromQL query"},
		{Name: "mcp__logs__query_loki_logs", Description: "Run a LogQL query over logs"},
		{Name: "mcp__logs__list_labels", Description: "List label names for loki"},
		{Name: "mcp__docs__search", Description: "Search the docs"},
	}
	t.Run("ranks name matches before description matches", func(t *testing.T) {
		got := Search(tools, "loki logs", 5)
		names := make([]string, 0, len(got))
		for _, tool := range got {
			names = append(names, tool.Name)
		}
		assert.Equal(t, []string{"mcp__logs__query_loki_logs", "mcp__logs__list_labels"}, names)
	})
	t.Run("keeps input order on ties", func(t *testing.T) {
		got := Search(tools, "query", 5)
		assert.Equal(t, "mcp__metrics__query_prometheus", got[0].Name)
		assert.Equal(t, "mcp__logs__query_loki_logs", got[1].Name)
	})
	t.Run("is case-insensitive and splits names on _ and -", func(t *testing.T) {
		got := Search(tools, "LABELS", 5)
		assert.Len(t, got, 1)
		assert.Equal(t, "mcp__logs__list_labels", got[0].Name)
	})
	t.Run("returns at most n", func(t *testing.T) {
		assert.Len(t, Search(tools, "query logs search", 2), 2)
	})
	t.Run("returns nothing when no word matches", func(t *testing.T) {
		assert.Empty(t, Search(tools, "kubernetes", 5))
	})
}
