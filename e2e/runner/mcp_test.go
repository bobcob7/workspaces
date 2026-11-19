//go:build e2e

package runner_test

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The MCP scenarios of ADR 0067's table: a ws child calls tools on fake-mcp
// servers over stdio or HTTP, filtered, deferred or failing. The servers'
// scripts are in testdata/; fake-ollama's script is mcp.yaml.

const mcpToken = "e2e-secret-4c1d" // FAKE_MCP_TOKEN, as stdio.yaml and http.yaml require

// mcpRoles are the runner roles the MCP scenarios spawn with: tools allows every
// tool of the server "fake"; search and destructive allow its query_* and
// list_* tools only.
const mcpRoles = `
[runner.roles.tools]
mcp_servers = ["fake"]

[runner.roles.search]
mcp_servers = ["fake"]
mcp_tools = ["mcp__fake__query_*", "mcp__fake__list_*"]
mcp_upfront = 8

[runner.roles.destructive]
mcp_servers = ["fake"]
mcp_tools = ["mcp__fake__query_*", "mcp__fake__list_*"]
`

// startMCPEnv builds a scenario whose fake-ollama runs mcp.yaml and whose
// secrets hold the token the fake servers require.
func startMCPEnv(t *testing.T) (*testEnv, fakeOllama) {
	t.Helper()
	env := newEnv(t)
	fake := startFakeOllama(t, "mcp.yaml")
	env.writeConfig(t, runnerConfig(fake.URL)+mcpRoles)
	env.writeMachineRole(t, "tools", "MCP_CALL: look up api and worker with lookup_service, then record PASS.")
	env.writeMachineRole(t, "search", "MCP_SEARCH: find the logs tool, then query api logs.")
	env.writeMachineRole(t, "destructive", "MCP_DESTRUCTIVE: delete dashboard abc.")
	env.writeSecrets(t, map[string]string{"FAKE_MCP_TOKEN": mcpToken})
	return env, fake
}

// writeClaudeJSON writes the user's ~/.claude.json, which names the server
// "fake" as server defines it.
func (e *testEnv) writeClaudeJSON(t *testing.T, server map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"mcpServers": map[string]any{"fake": server}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(e.home, ".claude.json"), data, 0o600))
}

// stdioServer defines fake-mcp over stdio. The token reaches the server
// through ${FAKE_MCP_TOKEN}, the way a user's config passes a secret.
func stdioServer(t *testing.T, config, record string) map[string]any {
	t.Helper()
	bin, args := fakeMCPStdio(t, config, record)
	return map[string]any{
		"type":    "stdio",
		"command": bin,
		"args":    args,
		"env":     map[string]string{"FAKE_MCP_TOKEN": "${FAKE_MCP_TOKEN}"},
	}
}

// offeredTools is the names of the tools a chat request offered the model.
func offeredTools(req *fakeRequest) []string {
	body, _ := req.Body.(map[string]any)
	list, _ := body["tools"].([]any)
	var names []string
	for _, item := range list {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		if name, ok := fn["name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// toolResultFailed reports whether the transcript holds a failed tool result
// for the named tool.
func toolResultFailed(transcript, name string) bool {
	for _, ev := range events(transcript) {
		if ev["kind"] == "tool_result" && ev["name"] == name && ev["ok"] == false {
			return true
		}
	}
	return false
}

// toolEvent is the first event of kind that names tool in the transcript, or nil.
func toolEvent(transcript, kind, tool string) map[string]any {
	for _, ev := range events(transcript) {
		if ev["kind"] == kind && ev["name"] == tool {
			return ev
		}
	}
	return nil
}

// recordedTools is the tool each recorded call named.
func recordedTools(calls []map[string]any) []string {
	var names []string
	for _, call := range calls {
		if name, ok := call["tool"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

func testMCPStdio(t *testing.T) {
	env, _ := startMCPEnv(t)
	record := filepath.Join(env.root, "stdio.jsonl")
	env.writeClaudeJSON(t, stdioServer(t, testdata(t, "stdio.yaml"), record))
	c := spawnRole(t, env, "tools")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)
	assert.Equal(t, []string{"lookup_service", "lookup_service"}, recordedTools(recorded(t, record)))
	assert.Contains(t, readFileString(t, env.transcriptOf(c.ID)), "api: owner team-a, port 8080")
	assertNoSecret(t, env, c.ID, mcpToken)
}

func testMCPHTTP(t *testing.T) {
	env, _ := startMCPEnv(t)
	record := filepath.Join(env.root, "http.jsonl")
	url := startFakeMCPHTTP(t, "http.yaml", "--record", record)
	env.writeClaudeJSON(t, map[string]any{
		"type":    "http",
		"url":     url,
		"headers": map[string]string{"Authorization": "Bearer ${FAKE_MCP_TOKEN}"},
	})
	c := spawnRole(t, env, "tools")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	require.NotNil(t, w.Result)
	assert.Equal(t, "PASS", w.Result.Verdict)
	assert.Equal(t, []string{"lookup_service", "lookup_service"}, recordedTools(recorded(t, record)))
	assert.Contains(t, readFileString(t, env.transcriptOf(c.ID)), "api: owner team-a, port 8080")
	assertNoSecret(t, env, c.ID, mcpToken)
}

func testMCPServerFails(t *testing.T) {
	env, _ := startMCPEnv(t)
	record := filepath.Join(env.root, "crash.jsonl")
	crashing := filepath.Join(t.TempDir(), "stdio.yaml")
	script, err := os.ReadFile(testdata(t, "stdio.yaml"))
	require.NoError(t, err)
	body := strings.Replace(string(script), "faults: {}", "faults: {crash_after_calls: 1}", 1)
	require.NotEqual(t, string(script), body, "stdio.yaml has no faults line to replace")
	require.NoError(t, os.WriteFile(crashing, []byte(body), 0o600))
	env.writeClaudeJSON(t, stdioServer(t, crashing, record))
	c := spawnRole(t, env, "tools")
	w := waitRunner(t, env, c.ID)

	transcript := env.transcriptOf(c.ID)
	assert.Equal(t, "done", w.WSRunner.Exit)
	assert.NotZero(t, countEvents(transcript, "mcp_error"), "the crashed server must report an mcp_error")
	assert.True(t, toolResultFailed(transcript, "mcp__fake__lookup_service"), "the call after the crash must answer an error")
}

func testMCPFilter(t *testing.T) {
	env, fake := startMCPEnv(t)
	record := filepath.Join(env.root, "filter.jsonl")
	env.writeClaudeJSON(t, stdioServer(t, testdata(t, "many-tools.yaml"), record))
	c := spawnRole(t, env, "destructive")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	chats := chatRequests(requests(t, fake.Control))
	require.NotEmpty(t, chats)
	for _, req := range chats {
		offered := offeredTools(&req)
		assert.Contains(t, offered, "mcp__fake__query_loki_logs")
		assert.NotContains(t, offered, "mcp__fake__delete_dashboard")
	}
	assert.NotContains(t, recordedTools(recorded(t, record)), "delete_dashboard")
	assert.NotNil(t, toolEvent(env.transcriptOf(c.ID), "invalid_call", "mcp__fake__delete_dashboard"), "the refused call must be an invalid_call")
}

func testMCPToolSearch(t *testing.T) {
	env, fake := startMCPEnv(t)
	record := filepath.Join(env.root, "search.jsonl")
	env.writeClaudeJSON(t, stdioServer(t, testdata(t, "many-tools.yaml"), record))
	c := spawnRole(t, env, "search")
	w := waitRunner(t, env, c.ID)

	assert.Equal(t, "done", w.WSRunner.Exit)
	chats := chatRequests(requests(t, fake.Control))
	require.GreaterOrEqual(t, len(chats), 2)
	assert.True(t, slices.Contains(offeredTools(&chats[1]), "mcp__fake__query_loki_logs"), "the request after tool_search must offer the found tool")
	assert.Contains(t, recordedTools(recorded(t, record)), "query_loki_logs")
	assert.Contains(t, readFileString(t, env.transcriptOf(c.ID)), "api GET /healthz 200")
}

// readFileString is the file's contents, failing the test when it can't read it.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// spawnRole starts a ws child of a role, whose machine role file supplies the
// prompt.
func spawnRole(t *testing.T, env *testEnv, role string) child {
	t.Helper()
	res := runWS(t, env, "machine", "spawn", "--runner", "ws", "--role", role, "--json", "--name", "child", "o/n")
	require.Zero(t, res.Code, "spawn: %s %s", res.Stdout, res.Stderr)
	var got child
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &got))
	require.NotEmpty(t, got.ID)
	return got
}

// writeMachineRole writes the ws machine role name, whose body is its prompt.
func (e *testEnv) writeMachineRole(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(e.configDir, "machine", "roles")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	text := "---\ndescription: " + name + " role for the MCP scenarios\n---\n\n" + body + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o600))
}
