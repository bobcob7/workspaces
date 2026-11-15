package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeConfigPath(t *testing.T) {
	tests := []struct {
		name    string
		environ []string
		want    string
	}{
		{"CLAUDE_CONFIG_DIR wins", []string{"HOME=/home/me", "CLAUDE_CONFIG_DIR=/cfg"}, "/cfg/.claude.json"},
		{"HOME when unset", []string{"HOME=/home/me"}, "/home/me/.claude.json"},
		{"HOME when CLAUDE_CONFIG_DIR is empty", []string{"HOME=/home/me", "CLAUDE_CONFIG_DIR="}, "/home/me/.claude.json"},
		{"empty without HOME", []string{"PATH=/bin"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ClaudeConfigPath(tt.environ))
		})
	}
}

func TestLoadDefs(t *testing.T) {
	t.Parallel()
	t.Run("project wins, types default, unsupported are skipped with warnings", func(t *testing.T) {
		t.Parallel()
		defs, warnings, err := LoadDefs(filepath.Join("testdata", "repo"), filepath.Join("testdata", "claude.json"))
		require.NoError(t, err)
		assert.Equal(t, map[string]ServerDef{
			"metrics": {Name: "metrics", Source: sourceProject, Type: typeStdio, Command: "project-metrics", Args: []string{"--project"}},
			"docs": {
				Name: "docs", Source: sourceProject, Type: typeHTTP, URL: "https://docs.example.test/mcp",
				Headers: map[string]string{"Authorization": "Bearer fake-project-token"},
			},
			"search": {
				Name: "search", Source: sourceUser, Type: typeSSE, URL: "https://search.example.test/sse",
				Env: map[string]string{"SEARCH_KEY": "${SEARCH_KEY:-fake-key}"},
			},
		}, defs)
		require.Len(t, warnings, 3)
		assert.Contains(t, warnings[0], "chat")
		assert.Contains(t, warnings[1], "remote")
		assert.Contains(t, warnings[2], "ws-only")
	})
	t.Run("no files is empty and nil", func(t *testing.T) {
		t.Parallel()
		defs, warnings, err := LoadDefs(t.TempDir(), filepath.Join(t.TempDir(), "missing.json"))
		require.NoError(t, err)
		assert.Empty(t, defs)
		assert.Nil(t, warnings)
	})
	t.Run("unparsable file is an error naming the file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		bad := filepath.Join(dir, ".mcp.json")
		require.NoError(t, os.WriteFile(bad, []byte("{not json"), 0o600))
		_, _, err := LoadDefs(dir, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), bad)
	})
}

func TestSelect(t *testing.T) {
	t.Parallel()
	defs := map[string]ServerDef{
		"metrics": {Name: "metrics"},
		"docs":    {Name: "docs"},
	}
	t.Run("returns defs in the order named, each once", func(t *testing.T) {
		t.Parallel()
		selected, warnings := Select(defs, []string{"docs", "metrics", "docs"})
		assert.Equal(t, []ServerDef{{Name: "docs"}, {Name: "metrics"}}, selected)
		assert.Empty(t, warnings)
	})
	t.Run("an unknown name is a warning", func(t *testing.T) {
		t.Parallel()
		selected, warnings := Select(defs, []string{"nope"})
		assert.Empty(t, selected)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "nope")
	})
}
