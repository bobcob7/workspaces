package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilter(t *testing.T) {
	tools := []ToolInfo{
		{Name: "mcp__g__query_x", ReadOnly: true},
		{Name: "mcp__g__query_y"},
		{Name: "mcp__g__drop_all", ReadOnly: true, Destructive: true},
		{Name: "mcp__g__query_z", ReadOnly: true},
	}
	tests := []struct {
		name  string
		allow []string
		deny  []string
		want  []string
	}{
		{"glob allows read-only tools only", []string{"mcp__g__query_*"}, nil, []string{"mcp__g__query_x", "mcp__g__query_z"}},
		{"an exact name allows a tool that is not read-only", []string{"mcp__g__query_y"}, nil, []string{"mcp__g__query_y"}},
		{"star allows read-only, non-destructive tools", []string{"*"}, nil, []string{"mcp__g__query_x", "mcp__g__query_z"}},
		{"an exact name allows a destructive tool", []string{"mcp__g__drop_all"}, nil, []string{"mcp__g__drop_all"}},
		{"deny removes after allow", []string{"*"}, []string{"mcp__g__query_z"}, []string{"mcp__g__query_x"}},
		{"allow order comes first", []string{"mcp__g__query_z", "mcp__g__query_*"}, nil, []string{"mcp__g__query_z", "mcp__g__query_x"}},
		{"nothing allowed", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, tool := range Filter(tools, tt.allow, tt.deny) {
				got = append(got, tool.Name)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
