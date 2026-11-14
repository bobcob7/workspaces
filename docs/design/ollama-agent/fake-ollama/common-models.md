# Fake configs for the runner's e2e scenarios

Copied by task T22 to `e2e/runner/testdata/`: the fake-ollama configs here
and the fake MCP scripts in [../fake-mcp/](../fake-mcp/). The fake-ollama
configs need the copy in this repo with task T2's features (`turn`,
`match_tool_result`, `raw_arguments`, `any_tool`, `strict_capabilities`,
`context_length`, `/api/show`, `retry_after`, `auth`). Faults (`down`,
`status`, `retry_after`, `drop_after_chunks`, `hang`) are set by the tests
through the control API, not here, so one config serves several scenarios.
Every config has `strict_capabilities: true` and some of these models:

| Model id | Capabilities | Context | Alias in the e2e `config.toml` |
| -------- | ------------ | ------- | ------------------------------ |
| `qwen/qwen3.5-9b` | tools | 32768 | `qwen9b` (`context_tokens = 32768`), the e2e `runner.model` |
| `fake-small` | tools | 4096 (context-overflow) | `small` (`context_tokens = 16384`) |
| `fake-notools` | none | 32768 (no-tools-model) | `notools` |

The main model is named like the design's starting model, so an id with
`/` goes through every layer (config, preflight, request bodies, JSON,
the transcript, the desktop fixtures). The e2e endpoint is `[endpoints.fake]` with `api = "openai"` and `probe =
"ollama"` (the fake serves `/api/show`), except where a scenario sets
`probe = "none"`. Its token, when a scenario uses one, is
`WS_ENDPOINT_TOKEN_FAKE` in the test's `secrets.env`.

`turn` counts the assistant messages already in the request. A retried
request (after a fault) has the same messages, so it's the same turn and
gets the same reply. Rules with `turn` or `match_tool_result` are tried
before fake-ollama's default answer to tool results.
