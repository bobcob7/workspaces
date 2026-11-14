# Ollama runner: command reference (draft)

Draft of the reference for the Ollama runner
([0062](../../adrs/0062-ollama-runner-built-into-ws.md)–[0067](../../adrs/0067-ollama-runner-tests-use-fake-ollama.md)).
When this ships, its parts move into `docs/tools/ws-machine.md`,
`docs/config.md`, `docs/state.md` and the `ws llm` guides; the
[README](README.md#proposed-changes-to-docstoolsws-machinemd) gives the
diff. Everything here is **added**: no existing field, error code or exit
code changes ([0016](../../adrs/0016-output-contract.md)).

- [Commands](#commands)
- [JSON output](#json-output)
- [Files](#files)
- [config.toml](#configtoml)
- [Environment](#environment)
- [Errors and exit codes](#errors-and-exit-codes)
- [Runner exit reasons](#runner-exit-reasons)

## Commands

```
ws machine spawn [--runner claude|ollama] [--endpoint <name>] [--name <n>] [--offline] [--hidden]
                 [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>... | --model <m>]
ws machine logs [--tail <n>] <workspace>
ws runner run [--resume] <workspace-id>          (hidden; started by ws itself)
ws runner check [--model <m>] <endpoint>         (hidden; the desktop app's Test connection)
```

### `ws machine spawn` (new flags)

| Flag | Meaning |
| ---- | ------- |
| `--runner claude\|ollama` | Which agent runs the child. Default: `ollama.roles.<role>.runner` for `--role`, else `claude` |
| `--endpoint <name>` | An `[ollama.endpoints.<name>]` from `config.toml`; only with the `ollama` runner. Default: the role's, then `ollama.endpoint` |

For an `ollama` child, the arguments after the prompt may only be `--model
<m>` (an Ollama model name such as `qwen3-coder:30b`); anything else is
`usage`. Use `-- "" --model <m>` to override only the model of a role.

Order of work for `--runner ollama`: check flags and role (before anything
is created) → resolve the endpoint and model → **preflight** → create the
workspace → write `state/<id>/runner/run.toml` → start `ws runner run <id>`
→ record the session → write `spawn.toml` when there is a role → print.
Any failure after the workspace was created removes it.

Preflight requests, each with a 10 s limit:

1. `GET <url>/v1/models`; with `api = "ollama"` also `GET <url>/api/version`.
2. The model is listed in `/v1/models`' `data[].id`.
3. With `api = "ollama"`: `POST <url>/api/show` `{"model": "<m>"}`.
   `capabilities` must contain `tools`. When `model_info` has a
   `*.context_length` smaller than `context_tokens`, the run uses the
   smaller value and a warning says so.

With `ollama.fallback = "claude"`, a failure of steps 1–3
(`ollama_unreachable`, `ollama_model_missing`, `ollama_no_tools`) starts a
Claude child instead. Its output has `"runner": "claude"` and a warning
`ollama preflight failed (<code>: <message>); started a claude child
because ollama.fallback = "claude"`.

### `ws machine logs`

Prints a descendant's log as one JSON object. For an `ollama` child, these
are the last `--tail` events (default 200, `0` for all) of
`transcript.jsonl`. For a `claude` child it's the text of `claude logs
<session>`. Run it outside a workspace and it fails with
`outside_workspace`. It accepts a removed child by id while its workspace's
state still exists; otherwise `workspace_not_found`.

| Flag | Meaning |
| ---- | ------- |
| `--tail <n>` | Last n events; 0 for all; default 200 |

### `ws runner run` (hidden)

Run by `ws` only, never by people or agents, and hidden from `ws --help`
and `ws llm`. It reads `state/<id>/runner/run.toml`, runs the loop
([0065](../../adrs/0065-ollama-agent-loop-tools-and-budgets.md)), and
exits 0 once it has written `status.json` with `exit` set, whatever the
reason. It exits 1 only when it can't read `run.toml` or write
`status.json`. `--resume` rebuilds the conversation from
`transcript.jsonl`.

### `ws runner check` (hidden)

Runs the spawn preflight for one configured endpoint, outside any
workspace, and prints the result for Settings › Models:

```json
{"endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434", "api": "ollama",
 "model": "qwen3-coder:30b", "version": "0.40.16", "capabilities": ["completion", "tools"],
 "context_tokens": 65536, "models": ["qwen3-coder:30b", "gpt-oss:20b"], "warnings": []}
```

Errors are the preflight codes, as for `spawn`.

## JSON output

### `ws machine spawn`

```json
{
  "id": "t1-impl-d8efb24f",
  "session": "ol-3f2a9c1e",
  "path": "/…/workspaces/t1-impl-d8efb24f",
  "parent": "orch-1a2b3c4d",
  "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "feat/x", "ref_kind": "branch", "commit": "3f2a9c1e4b…"}],
  "warnings": [],
  "role": "implementer",
  "runner": "ollama",
  "ollama": {"endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434", "model": "qwen3-coder:30b", "context_tokens": 65536}
}
```

`runner` is `claude` or `ollama`. `ollama` is `null` for a Claude child.
`session` is the runner session id, `ol-` followed by 8 hex digits.

### `ws list --json` and `ws machine children`: `session`

```json
"session": {"kind": "agent", "id": "ol-3f2a9c1e", "state": "working", "pid": 51234, "stale": null,
            "runner": "ollama",
            "ollama": {"endpoint": "gpu", "model": "qwen3-coder:30b", "step": 12, "max_steps": 80,
                       "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
                       "exit": null}}
```

- `runner`: `claude` for a Claude Code agent, `ollama` for a runner,
  `null` for a shell.
- `ollama`: `null` unless `runner` is `ollama`. `exit` is `null` while it
  runs, else an [exit reason](#runner-exit-reasons).
- `state` for `ollama` ([0063](../../adrs/0063-ollama-children-are-ws-supervised-sessions.md)):
  `working` while the runner process is alive; `starting` for the first
  10 s with no `status.json`; `idle` once it has exited normally (`exit`
  set); `stopped` when the process is gone without an exit (crash, kill,
  reboot; `ws restore` resumes it); `unknown` when `status.json` can't be
  read.
- `pid` is the runner's pid while alive, else `null`. `stale` is always
  `null`.

### `ws machine wait`: each child

```json
{"id": "t1-impl-d8efb24f", "session": "ol-3f2a9c1e", "role": "implementer",
 "status": "idle", "finished": true, "outcome": "pass",
 "result": {"verdict": "PASS", "outcome": "pass", "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md", "recorded_at": "2026-11-13T18:04:05Z"},
 "error": null,
 "runner": "ollama",
 "ollama": {"endpoint": "gpu", "model": "qwen3-coder:30b", "exit": "done", "steps": 31,
            "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
            "wall_seconds": 1104,
            "usage_file": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/usage.json"}}
```

`runner` and `ollama` are as in `session` above. For a gone child, both
come from `usage.json`, or are `null` when it doesn't exist.

### `ws machine logs`

```json
{"id": "t1-impl-d8efb24f", "session": "ol-3f2a9c1e", "runner": "ollama",
 "events": [
   {"time": "2026-11-13T17:46:01Z", "step": 1, "kind": "request", "messages": 2, "estimated_tokens": 3120},
   {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "response", "content": "", "reasoning": "I'll read the task first.", "tool_calls": [{"id": "call_ab12cd34", "name": "read_file", "arguments": "{\"path\":\"docs/design/x/tasks.md\"}"}], "finish_reason": "tool_calls", "usage": {"prompt_tokens": 3051, "completion_tokens": 41}},
   {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "tool_result", "id": "call_ab12cd34", "name": "read_file", "ok": true, "bytes": 8123, "truncated": false, "text": "1\t# x: tasks\n…"}
 ],
 "text": null,
 "truncated": true,
 "warnings": []}
```

For a Claude child, `events` is `[]` and `text` is `claude logs`' output.
`truncated` is `true` when `--tail` left events out.

## Files

All under `CONFIG_DIR/state/` (mode 0700, files 0600).

### `state/<id>/session` (version 2)

```toml
version = 2
kind = "ollama"
runner_session_id = "ol-3f2a9c1e"
started_at = 2026-11-13T17:45:58Z
```

`claude` and `shell` records keep `version = 1`.

### `state/<id>/runner/run.toml`

```toml
version = 1
session = "ol-3f2a9c1e"
endpoint = "gpu"
url = "http://ollama-a.inference.svc:11434"
api = "ollama"
model = "qwen3-coder:30b"
context_tokens = 65536
request_timeout = "10m"
idle_timeout = "90s"
role = "implementer"          # "" without a role
pass = ["PASS"]
fail = ["FAIL"]
max_steps = 80
timeout = "60m"
max_tokens = 4000000
branch = "feat/x-t1"          # the role's BRANCH var, for the push guard; "" when none
prompt = """…the rendered role prompt…"""
created_at = 2026-11-13T17:45:58Z
```

### `state/<id>/runner/status.json`

```json
{"version": 1, "session": "ol-3f2a9c1e", "pid": 51234, "process_started_at": "2026-11-13T17:45:58Z",
 "state": "working", "step": 12, "max_steps": 80,
 "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
 "exit": null, "exit_message": null, "updated_at": "2026-11-13T17:52:40Z"}
```

`state` is `starting`, `working` (running a tool), `waiting_model` (a
request is in flight) or `exited`. The file is written to a temp file and
renamed.

### `state/<id>/runner/transcript.jsonl`

One JSON object per line, with these kinds:

| `kind` | Fields besides `time`, `step`, `kind` |
| ------ | ------------------------------------- |
| `start` | `model`, `endpoint`, `context_tokens`, `resume` (bool) |
| `request` | `messages`, `estimated_tokens` |
| `response` | `content`, `reasoning`, `tool_calls` [{`id`,`name`,`arguments`}], `finish_reason`, `usage` {`prompt_tokens`,`completion_tokens`} or `null`, `duration_ms` |
| `tool_result` | `id`, `name`, `ok`, `bytes`, `truncated`, `text` (as sent to the model) |
| `invalid_call` | `id`, `name`, `error` |
| `nudge` | `reason` (`no_tool_call`, `repeat`) |
| `compaction` | `before_tokens`, `after_tokens`, `elided`, `dropped_turns` |
| `truncated` | `estimated_tokens`, `prompt_tokens` |
| `retry` | `attempt`, `error`, `wait_ms` |
| `exit` | `reason`, `message`, `verdict` or `null` |

The system prompt and the first user message are in the `start` event's
`messages` (redacted), so `--resume` can rebuild the conversation.

### `state/<root>/machine/<child>/usage.json`

Written once, at exit, next to `result.toml`. It outlives `ws machine rm`.

```json
{"version": 1, "runner": "ollama", "endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434",
 "model": "qwen3-coder:30b", "role": "implementer", "exit": "done", "steps": 31,
 "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
 "started_at": "2026-11-13T17:45:58Z", "ended_at": "2026-11-13T18:04:22Z", "wall_seconds": 1104,
 "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"}
```

`trace_id` is the W3C trace id the runner sent in `traceparent` on every
request. llm-router records it, so the run's requests can be found in
Tempo.

## config.toml

New keys, all optional. Set them with `ws config set` or Settings › Models.

### `[ollama]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `endpoint` | string | none | The default endpoint name |
| `fallback` | string | `"none"` | `none` or `claude`: what `spawn` does when preflight fails |
| `keep_git_tokens` | bool | `true` | Keep `GH_TOKEN` and `GITHUB_TOKEN` in the tools' environment |

### `[ollama.endpoints.<name>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `url` | string | required | Base URL without `/v1`: `http(s)://host[:port][/path]` |
| `api` | string | `"ollama"` | `ollama` (a direct Ollama server: `/api/show` preflight) or `openai` (llm-router or another OpenAI-compatible server) |
| `model` | string | none | The default model on this endpoint |
| `context_tokens` | integer | required | The server's context length (`OLLAMA_CONTEXT_LENGTH` or the model's `num_ctx`); at least 16384 |
| `request_timeout` | duration | `"10m"` | One completion, including a cold model load |
| `idle_timeout` | duration | `"90s"` | The longest gap between streamed chunks |

`<name>` is 1–32 lowercase letters, digits and `-`, starting with a
letter. A `token` key is an error: `endpoint tokens go in
WS_OLLAMA_TOKEN_<NAME> or Settings › Models, never in config.toml`.

### `[ollama.roles.<role>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `runner` | string | `"claude"` | `claude` or `ollama`, for `spawn --role <role>` without `--runner` |
| `endpoint` | string | `ollama.endpoint` | The endpoint name |
| `model` | string | the endpoint's `model` | The Ollama model |
| `max_steps` | integer | per role (implementer 80, reviewer 40, verifier 40, other 60) | Requests per run |
| `timeout` | duration | `"60m"` (reviewer `"30m"`) | Wall-clock limit per run |
| `max_tokens` | integer | 4 000 000 (reviewer and verifier 2 000 000) | Prompt plus completion tokens per run |

`<role>` is any role name; it doesn't have to exist yet. An endpoint name
that isn't defined is `config_invalid` when the file is read.

`ws config` keys: `ollama.endpoint`, `ollama.fallback`,
`ollama.keep_git_tokens`, `ollama.endpoints.<name>.<key>`,
`ollama.roles.<role>.<key>`. `ws config unset ollama.endpoints.<name>`
removes the whole table.

## Environment

| Variable | Read by | Meaning |
| -------- | ------- | ------- |
| `WS_OLLAMA_TOKEN_<NAME>` | spawn preflight, runner | Bearer token for endpoint `<name>`, uppercased, with `-` → `_`. Without it, `CONFIG_DIR/ollama-tokens.json` (`{"version":1,"tokens":{"<name>":"…"}}`, mode 0600, written by the desktop app) is read |
| `WS_RUNNER` | tools | Set to `ollama` in the environment of `run` commands |
| `WS_FAKE_OLLAMA` | e2e tests | Path to a prebuilt fake-ollama; skips `make fake-ollama` |
| `E2E_OLLAMA_URL`, `E2E_OLLAMA_MODEL` | e2e tests | Opt in to the real-model scenarios |

## Errors and exit codes

New codes, under existing exit codes:

| Code | Exit | Situation | `details` |
| ---- | ---- | --------- | --------- |
| `endpoint_unknown` | 3 | `--endpoint`, or the resolved endpoint, isn't in `config.toml` | `{"endpoints": ["gpu", …]}` |
| `ollama_unreachable` | 1 | Preflight couldn't reach the endpoint, or got a 5xx | `{"url": "…", "cause": "connection refused"}` |
| `ollama_model_missing` | 3 | The model isn't in `/v1/models` | `{"model": "…", "models": ["…"]}` |
| `ollama_no_tools` | 1 | `/api/show` says the model has no `tools` capability | `{"model": "…", "capabilities": ["completion"]}` |
| `runner_failed` | 1 | `ws runner run` didn't start, or wrote no `status.json` within 10 s | `{"log": "<runner.log path>", "stderr": "…"}` |

New `usage` (exit 2) situations: `--runner` isn't `claude` or `ollama`;
`--endpoint` with the `claude` runner; for an `ollama` child, an argument
after the prompt other than `--model <m>`. A missing endpoint or model with
no default anywhere, a `token` key, or `context_tokens` under 16384 is
`config_invalid` (exit 1).

`ws machine logs`: `outside_workspace` (2), `usage` (2),
`workspace_not_found` (3), `not_descendant` (4), and `claude_failed` (1)
when `claude logs` fails.

## Runner exit reasons

In `status.json` `exit`, `usage.json` `exit`, `session.ollama.exit`,
`wait`'s `ollama.exit`, and the first line of a runner-written report
(`ws ollama runner: <reason>`).

| Reason | Verdict recorded | Meaning |
| ------ | ---------------- | -------- |
| `done` | the model's | The model called `record_result` (or ran `ws machine result`) |
| `max_steps` | first fail | Step budget used up |
| `timeout` | first fail | Wall-clock budget used up |
| `max_tokens` | first fail | Token budget used up |
| `endpoint_down` | first fail | 3 retries failed: connection, 429/5xx, dropped or stalled stream |
| `model_no_tools` | first fail | 400 "does not support tools" |
| `model_missing` | first fail | 404 for the model |
| `malformed_tool_calls` | first fail | 3 invalid tool calls in a row |
| `no_result` | first fail | 3 replies in a row with no tool call |
| `looping` | first fail | The same call 5 times in a row |
| `context_overflow` | first fail | The system prompt, task and last turn don't fit |
| `stopped` | none | `ws rm` asked it to stop |
| `internal` | first fail | A runner bug; `exit_message` says what |
