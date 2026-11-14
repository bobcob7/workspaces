# Fake MCP scripts for the runner's e2e scenarios

Scripts for `fake-mcp` (task T3, [ADR 0067](../../../adrs/0067-runner-tests-use-vendored-fakes.md)),
copied by task T19 to `e2e/runner/testdata/`. The e2e harness writes a
`.mcp.json` (project scope) or a test `~/.claude.json` (user scope, through
`CLAUDE_CONFIG_DIR`) naming the server `fake`, and the matching fake-ollama
config is [../fake-ollama/mcp.yaml](../fake-ollama/mcp.yaml).

| Script | Transport | Scenario | What it checks |
| ------ | --------- | -------- | -------------- |
| `stdio.yaml` | stdio | `mcp-stdio`, `mcp-server-fails` (with `crash_after_calls: 1` set by the test's copy) | `require_env` proves `FAKE_MCP_TOKEN` reached the server from `secrets.env` through `${FAKE_MCP_TOKEN}` in the definition's `env` |
| `http.yaml` | streamable HTTP | `mcp-http` | `require_header` proves the expanded `Authorization: Bearer ${FAKE_MCP_TOKEN}` header was sent |
| `many-tools.yaml` | stdio | `mcp-filter`, `mcp-tool-search` | 80 generated tools like `mcp-grafana`'s, a read-only `query_loki_logs`, and a destructive `delete_dashboard` |

Every scenario also greps the transcript, `runner.log`, `status.json` and
`usage.json` for the token value and fails if it appears.
