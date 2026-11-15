package fakemcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	t.Parallel()
	t.Run("loads every fake-mcp config", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{"http.yaml", "many-tools.yaml", "stdio.yaml"} {
			_, err := LoadConfig(filepath.Join("..", "..", "docs", "design", "ollama-agent", "fake-mcp", name))
			assert.NoError(t, err, name)
		}
	})
	t.Run("reads the generate block", func(t *testing.T) {
		t.Parallel()
		cfg := loadFixture(t, "many-tools.yaml")
		require.NotNil(t, cfg.Generate)
		assert.Equal(t, 80, cfg.Generate.Count)
		assert.Equal(t, "list_thing_", cfg.Generate.Prefix)
	})
	t.Run("defaults the server name and version", func(t *testing.T) {
		t.Parallel()
		path := writeConfig(t, "tools: []\n")
		cfg, err := LoadConfig(path)
		require.NoError(t, err)
		assert.Equal(t, "fake", cfg.Server.Name)
		assert.Equal(t, "0.0.0-fake", cfg.Server.Version)
	})
	t.Run("defaults a tool's input schema to an object", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadConfig(writeConfig(t, "tools:\n  - name: ping\n"))
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"type": "object"}, cfg.Tools[0].InputSchema)
	})
	t.Run("parses durations in faults", func(t *testing.T) {
		t.Parallel()
		cfg, err := LoadConfig(writeConfig(t, "faults:\n  slow_call: 250ms\n"))
		require.NoError(t, err)
		assert.Equal(t, 250*time.Millisecond, cfg.Faults.SlowCall)
	})
	t.Run("rejects unknown fields", func(t *testing.T) {
		t.Parallel()
		_, err := LoadConfig(writeConfig(t, "bogus: 1\n"))
		assert.Error(t, err)
	})
	t.Run("reports a missing file", func(t *testing.T) {
		t.Parallel()
		_, err := LoadConfig(filepath.Join(t.TempDir(), "missing.yaml"))
		assert.Error(t, err)
	})
}

func TestCheckEnv(t *testing.T) {
	const secret = "e2e-secret-4c1d"
	cfg := Config{RequireEnv: map[string]string{"FAKE_MCP_TOKEN": secret}}
	t.Run("passes when the variable has the value", func(t *testing.T) {
		err := CheckEnv(cfg, func(name string) string {
			return map[string]string{"FAKE_MCP_TOKEN": secret}[name]
		})
		assert.NoError(t, err)
	})
	t.Run("names a missing variable without its value", func(t *testing.T) {
		err := CheckEnv(cfg, func(string) string { return "" })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FAKE_MCP_TOKEN")
		assert.NotContains(t, err.Error(), secret)
	})
	t.Run("names a wrong variable without its value", func(t *testing.T) {
		err := CheckEnv(cfg, func(string) string { return "wrong" })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FAKE_MCP_TOKEN")
		assert.NotContains(t, err.Error(), secret)
	})
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
