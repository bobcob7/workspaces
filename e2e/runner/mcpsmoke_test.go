//go:build e2e

package runner_test

import (
	"encoding/json/v2"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// smokeTool is one tool of E2E_MCP_SMOKE, "<server>:<tool>".
type smokeTool struct {
	server, tool string
}

// fullName is the tool's name as the runner offers it, mcp__<server>__<tool>.
func (s smokeTool) fullName() string {
	return "mcp__" + s.server + "__" + s.tool
}

// TestMCPSmoke calls the named tools on the user's real MCP servers, as the
// user's ~/.claude.json defines them. Fake-ollama asks for each tool once,
// with empty arguments, and the run must succeed. It asserts that none of
// the servers' env or header values reaches the transcript or runner.log. It
// never runs in CI, and skips without E2E_MCP_SMOKE=<server>:<tool>[,...].
func TestMCPSmoke(t *testing.T) {
	spec := os.Getenv("E2E_MCP_SMOKE")
	if spec == "" {
		t.Skip("E2E_MCP_SMOKE is not set; the MCP smoke test calls real servers")
	}
	tools := parseSmoke(t, spec)
	home, err := os.UserHomeDir()
	require.NoError(t, err)

	env := newEnv(t)
	env.home = home // the user's own Claude config, ~/.claude.json, is read as is
	fake := startFakeOllamaAt(t, smokeScript(t, tools))
	env.writeConfig(t, runnerConfig(fake.URL)+smokeRole(tools))
	env.writeMachineRole(t, "smoke", "SMOKE: call each allowed tool once with {}, then record PASS.")

	c := spawnRole(t, env, "smoke")
	w := waitRunner(t, env, c.ID)
	assert.Equal(t, "done", w.WSRunner.Exit)

	transcript := env.transcriptOf(c.ID)
	for _, tool := range tools {
		assert.True(t, toolSucceeded(transcript, tool.fullName()), "the call to %s did not succeed", tool.fullName())
	}
	assertNoSecret(t, env, c.ID, serverSecrets(t, home, tools)...)
}

// parseSmoke reads "<server>:<tool>[,<server>:<tool>...]".
func parseSmoke(t *testing.T, spec string) []smokeTool {
	t.Helper()
	var tools []smokeTool
	for entry := range strings.SplitSeq(spec, ",") {
		server, tool, ok := strings.Cut(strings.TrimSpace(entry), ":")
		require.True(t, ok && server != "" && tool != "", "E2E_MCP_SMOKE entry %q is not <server>:<tool>", entry)
		tools = append(tools, smokeTool{server: server, tool: tool})
	}
	return tools
}

// smokeScript is a fake-ollama script that calls each tool in turn, then
// records PASS.
func smokeScript(t *testing.T, tools []smokeTool) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("strict_capabilities: true\nmodels:\n")
	b.WriteString("  - {name: qwen/qwen3.5-9b, capabilities: [tools], context_length: 32768}\nrules:\n")
	for turn, tool := range tools {
		fmt.Fprintf(&b, "  - match: SMOKE\n    turn: %d\n    tool: %s\n    arguments: '{}'\n", turn, tool.fullName())
	}
	fmt.Fprintf(&b, "  - match: SMOKE\n    turn: %d\n    tool: record_result\n", len(tools))
	b.WriteString("    arguments: '{\"verdict\":\"PASS\",\"report\":\"smoke\"}'\n")
	path := filepath.Join(t.TempDir(), "smoke.yaml")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
	return path
}

// smokeRole is the runner role that allows exactly the named tools.
func smokeRole(tools []smokeTool) string {
	var servers, names []string
	for _, tool := range tools {
		servers = appendOnce(servers, fmt.Sprintf("%q", tool.server))
		names = append(names, fmt.Sprintf("%q", tool.fullName()))
	}
	return fmt.Sprintf("\n[runner.roles.smoke]\nmcp_servers = [%s]\nmcp_tools = [%s]\n",
		strings.Join(servers, ", "), strings.Join(names, ", "))
}

// appendOnce appends v to list unless it is already there.
func appendOnce(list []string, v string) []string {
	if slices.Contains(list, v) {
		return list
	}
	return append(list, v)
}

// serverSecrets are the env and header values of the named servers in the
// Claude config under home: what the runner must keep out of a transcript.
func serverSecrets(t *testing.T, home string, tools []smokeTool) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	require.NoError(t, err)
	var cfg struct {
		MCPServers map[string]struct {
			Env     map[string]string `json:"env"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"` //nolint:tagliatelle // Claude Code's file format
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	var values []string
	for _, tool := range tools {
		server := cfg.MCPServers[tool.server]
		secrets := slices.Concat(slices.Collect(maps.Values(server.Env)), slices.Collect(maps.Values(server.Headers)))
		for _, v := range secrets {
			if v != "" {
				values = append(values, v)
			}
		}
	}
	return values
}

// toolSucceeded reports whether the transcript holds a successful result
// for the named tool.
func toolSucceeded(transcript, name string) bool {
	for _, ev := range events(transcript) {
		if ev["kind"] == "tool_result" && ev["name"] == name && ev["ok"] == true {
			return true
		}
	}
	return false
}
