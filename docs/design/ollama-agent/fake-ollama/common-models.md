# fake-ollama configs for the runner's e2e scenarios

Copied by task T7 to `e2e/ollama/testdata/`. They need fake-ollama with
task T1's features (`turn`, `match_tool_result`, `raw_arguments`,
`any_tool`, `strict_capabilities`, `context_length`, `/api/show`).
Faults (`down`, `status`, `drop_after_chunks`) are set by the tests
through the control API, not here, so one config serves several
scenarios. Every config has `strict_capabilities: true` and these models:

| Model | Capabilities | Context |
| ----- | ------------ | ------- |
| `fake-coder` | tools | 32768 |
| `fake-small` | tools | 4096 (context-overflow) |
| `fake-notools` | none | 32768 (no-tools-model) |

`turn` counts the assistant messages already in the request. A retried
request (after a fault) has the same messages, so it's the same turn and
gets the same reply. Rules with `turn` or `match_tool_result` are tried
before fake-ollama's default answer to tool results.
