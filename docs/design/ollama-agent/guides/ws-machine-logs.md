# ws machine logs

```
ws machine logs [--tail <n>] <workspace>
```

Prints the log of a child you started, as JSON. For a child on `ws`'s
own runner (`runner: "ws"`), it's the last events of its transcript: each
request, the model's replies and tool calls, the tool results (MCP tools
included), retries, context compaction, and why it stopped. For a Claude child it's the text of
`claude logs <session>`.

A workspace is named by its id, path, or session id, and must be one of
your descendants. A child already removed with `ws machine rm` can still be
named by its id while its state is kept; otherwise it's
`workspace_not_found`.

## When to use it

- After `ws machine wait` reports a child `fail` or `missing`, to see what
  it did and why it stopped. For a `ws` child, also read
  `result.report`, whose first line names the reason.
- Instead of `claude logs`, which doesn't know `ws` runner sessions
  (`rn-…`).
- Not to watch a child while it runs: wait for it with `ws machine wait`
  as a background command.

## Flags

| Flag         | Meaning                                       |
| ------------ | --------------------------------------------- |
| `--tail <n>` | The last n events; `0` for all; default `200` |

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{
  "id": "t1-impl-d8efb24f",
  "session": "rn-3f2a9c1e",
  "runner": "ws",
  "model": "qwen3-coder:30b",
  "events": [
    {"time": "2026-11-13T17:46:01Z", "step": 1, "kind": "request", "messages": 2, "tools": 7, "estimated_tokens": 3120},
    {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "response", "content": "",
     "tool_calls": [{"id": "call_ab12cd34", "name": "read_file", "arguments": "{\"path\":\"docs/design/x/tasks.md\"}"}],
     "stop_reason": "tool_calls", "usage": {"input_tokens": 3051, "output_tokens": 41}},
    {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "tool_result", "id": "call_ab12cd34", "name": "read_file",
     "ok": true, "bytes": 8123, "truncated": false, "text": "1\t# x: tasks\n…"},
    {"time": "2026-11-13T18:04:20Z", "step": 31, "kind": "exit", "reason": "done", "message": "", "verdict": "PASS"}
  ],
  "text": null,
  "truncated": true,
  "warnings": []
}
```

- `events`: a `ws` child's transcript events, oldest first. `kind` is
  `start`, `request`, `response`, `tool_result`, `invalid_call`, `nudge`,
  `compaction`, `truncated`, `retry`, `mcp_start`, `mcp_error`,
  `tool_search`, `restart` or `exit`. Secrets are already replaced by
  `[redacted]`. It's `[]` for a Claude child.
- `model`: as in `ws machine wait`.
- `text`: a Claude child's `claude logs` output, else `null`.
- `truncated`: `true` when `--tail` left older events out.

## Exit codes

| Situation                                         | Error code            | Exit |
| ------------------------------------------------- | --------------------- | ---- |
| Run outside a workspace                           | `outside_workspace`   | 2    |
| No workspace, more than one, or `--tail` negative | `usage`               | 2    |
| Not a workspace, nor a removed child with state   | `workspace_not_found` | 3    |
| Not one you started, or theirs                    | `not_descendant`      | 4    |
| `claude logs` failed; `details.stderr`            | `claude_failed`       | 1    |

## Examples

```console
$ ws machine logs t1-impl-d8efb24f
$ ws machine logs --tail 20 rn-3f2a9c1e
$ ws machine logs --tail 0 t1-impl-d8efb24f
```
