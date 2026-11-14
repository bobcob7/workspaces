# Runner: command reference (draft)

Draft of the reference for `ws`'s own agent runner and the `ws` daemon
([0062](../../adrs/0062-agent-runner-built-into-ws.md)–[0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)).
When this ships, its parts move into `docs/tools/ws-machine.md`,
`docs/tools/ws-daemon.md`, `docs/config.md`, `docs/state.md` and the `ws
llm` guides; the [README](README.md#proposed-changes-to-docstoolsws-machinemd)
gives the diff. Everything here is **added**: no existing field, error code
or exit code changes ([0016](../../adrs/0016-output-contract.md)).

Names: the runner value is `ws` (`--runner ws`), the session kind is
`runner`, runner session ids start `rn-`. A **model alias** is a
`[models.<alias>]` name; a **model id** is what the endpoint calls the
model (`qwen3-coder:30b`).

- [Commands](#commands)
- [JSON output](#json-output)
- [Files](#files)
- [config.toml](#configtoml)
- [secrets.env and the runner's environment](#secretsenv-and-the-runners-environment)
- [MCP servers](#mcp-servers)
- [The daemon socket](#the-daemon-socket)
- [Environment variables](#environment-variables)
- [Errors and exit codes](#errors-and-exit-codes)
- [Runner exit reasons](#runner-exit-reasons)

## Commands

```
ws machine spawn [--runner claude|ws] [--endpoint <name>] [--name <n>] [--offline] [--hidden]
                 [--role <name> [--var KEY=VALUE]...] [<spec>...] -- "<prompt>" [<claude args>... | --model <m>]
ws machine logs [--tail <n>] <workspace>
ws daemon run [--origin transient|managed]
ws daemon status [--json]
ws daemon stop [--keep-runners] [--json]
ws runner run [--resume] <workspace-id>          (hidden; started by the daemon)
ws runner check [--json] [--model <m>] [<endpoint>]   (hidden; the desktop app's Test connection)
```

### `ws machine spawn` (new flags)

| Flag | Meaning |
| ---- | ------- |
| `--runner claude\|ws` | Which agent runs the child. Default: `runner.roles.<role>.runner` for `--role`, else `claude` |
| `--endpoint <name>` | Overrides the chosen model's endpoint; only with the `ws` runner |

For a `ws` child, the arguments after the prompt may only be `--model <m>`,
where `<m>` is a model alias, or a model id on the default model's
endpoint; anything else is `usage`. Use `-- "" --model <m>` to override only
the model of a role.

Order of work for `--runner ws`: check flags and role (before anything is
created) → resolve the model ([0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md))
→ connect to the daemon, starting it if needed → **preflight** through the
daemon → create the workspace → write `state/<id>/runner/run.toml` → record
the session → ask the daemon to start the runner → wait for `status.json`
(10 s) → write `spawn.toml` when there is a role → print. Any failure after
the workspace was created removes it.

Preflight requests, each with a 10 s limit:

1. `Models` (`GET <url>/v1/models` for `api = "openai"`).
2. The model id is listed.
3. `Probe`, chosen by the endpoint's `probe`: `ollama` sends `GET
   <url>/api/version` and `POST <url>/api/show` `{"model": "<id>"}`;
   `capabilities` must contain `tools`, and when `model_info` has a
   `*.context_length` smaller than `context_tokens`, the run uses the
   smaller value and a warning says so. `auto` tries `/api/version` once
   and behaves as `ollama` if it answers, else as `none`. `none` skips this
   step.

With `runner.fallback = "claude"`, a failure of steps 1–3
(`endpoint_unreachable`, `model_not_served`, `model_without_tools`) starts a
Claude child instead. Its output has `"runner": "claude"` and a warning
`runner preflight failed (<code>: <message>); started a claude child because
runner.fallback = "claude"`.

### `ws machine logs`

Prints a descendant's log as one JSON object. For a `ws` child, the last
`--tail` events (default 200, `0` for all) of `transcript.jsonl`. For a
`claude` child, the text of `claude logs <session>`. Outside a workspace:
`outside_workspace`. A removed child is accepted by id while its state
still exists; otherwise `workspace_not_found`.

| Flag | Meaning |
| ---- | ------- |
| `--tail <n>` | Last n events; 0 for all; default 200 |

### `ws daemon run`

Runs the daemon in the foreground until `SIGTERM` or `SIGINT`, or, with
`--origin transient` (what auto-start uses), until it has had no live
runner and no request for 15 minutes. `--origin managed` (the default) is
for `ws-online`, launchd and systemd. It takes `CONFIG_DIR/daemon/lock`;
when another daemon holds it, it fails with `daemon_running` (exit 4).
Text logs go to stderr and `CONFIG_DIR/daemon/daemon.log`. `--json` is
refused.

### `ws daemon status`

```json
{"running": true, "pid": 40211, "origin": "managed", "started_at": "2026-11-14T08:00:03Z",
 "ws_version": "v0.31.0", "socket": "/run/user/1000/ws/3be1f0a94c21.sock",
 "config_dir": "/data/ws",
 "runners": [{"workspace": "x-t1-impl-41aa0b7c", "session": "rn-5d2e8b10", "pid": 51234,
              "state": "working", "restarts": 0, "adopted": false}],
 "warnings": []}
```

Not running: `{"running": false, "pid": null, "origin": null, "started_at":
null, "ws_version": null, "socket": "<where it would be>", "config_dir": "…",
"runners": [], "warnings": []}`, exit 0. It never starts a daemon.

### `ws daemon stop`

```json
{"stopped": true, "pid": 40211, "runners_stopped": ["rn-5d2e8b10"], "runners_kept": [], "warnings": []}
```

Without `--keep-runners`, every runner is interrupted (`SIGTERM` to its
process group, without the `stop` file `ws rm` writes, so no exit reason is
recorded) and its workspace kept; they read `stopped` and are resumed when
a daemon next starts. With it, runners keep running and are listed in
`runners_kept`. No daemon running: `{"stopped": false, "pid": null, …}`,
exit 0. A daemon that hasn't exited within 30 s is `daemon_unavailable`.

### `ws runner run` (hidden)

Started by the daemon only. Reads `state/<id>/runner/run.toml`, runs the
loop, and exits 0 once it has written `status.json` with `exit` set,
whatever the reason. Exits 1 only when it can't read `run.toml` or write
`status.json`. `--resume` rebuilds the conversation from
`transcript.jsonl`.

### `ws runner check` (hidden)

Resolves `<endpoint>` (or the default model's endpoint) and `--model`
(default: that endpoint's first configured alias), and runs the spawn
preflight through the daemon, outside any workspace, for Settings ›
Models:

```json
{"endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434", "api": "openai", "probe": "ollama",
 "model": "qwen3-coder:30b", "version": "0.40.16", "capabilities": ["completion", "tools"],
 "context_tokens": 65536, "models": ["qwen3-coder:30b", "gpt-oss:20b"], "token_source": "secrets.env",
 "warnings": []}
```

`version` and `capabilities` are `null` without a probe. `token_source` is
`env`, `secrets.env` or `null`; the token itself is never printed. Errors
are the preflight codes.

## JSON output

### `ws machine spawn`

```json
{
  "id": "t1-impl-d8efb24f",
  "session": "rn-3f2a9c1e",
  "path": "/…/workspaces/t1-impl-d8efb24f",
  "parent": "orch-1a2b3c4d",
  "repos": [{"id": "github.com/bobcob7/buff-bear", "path": "buff-bear", "ref": "feat/x", "ref_kind": "branch", "commit": "3f2a9c1e4b…"}],
  "warnings": [],
  "role": "implementer",
  "runner": "ws",
  "model": "qwen3-coder:30b",
  "ws_runner": {"model_alias": "coder", "endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434",
                "api": "openai", "context_tokens": 65536, "mcp_servers": []}
}
```

- `runner`: `claude` or `ws`.
- `model`: for `ws`, the model id. For `claude`, the `--model` value it
  was started with (the role's or yours, such as `haiku`), or `null` for
  Claude Code's default.
- `ws_runner`: `null` for a Claude child.

### `ws list --json` and `ws machine children`: `session`

```json
"session": {"kind": "agent", "id": "rn-3f2a9c1e", "state": "working", "pid": 51234, "stale": null,
            "runner": "ws", "model": "qwen3-coder:30b",
            "ws_runner": {"model_alias": "coder", "endpoint": "gpu", "step": 12, "max_steps": 80, "restarts": 0,
                          "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
                          "mcp": [{"server": "mcp-grafana", "state": "connected", "tools": 6, "loaded": 3}],
                          "exit": null}}
```

A Claude agent:

```json
"session": {"kind": "agent", "id": "e8cdf5d8", "state": "idle", "pid": 4120, "stale": false,
            "runner": "claude", "model": "claude-sonnet-5-5", "ws_runner": null}
```

- `runner`: `claude` or `ws` for an agent, `null` for a shell. It says what
  runs the session; use it to choose `ws machine logs` behaviour or
  Attach.
- `model`: every agent's model, the same way: the model id the runner
  uses, or for Claude the `model` of the last assistant entry in its
  transcript; `null` for a shell, or a Claude agent that hasn't answered
  yet.
- `ws_runner`: `null` unless `runner` is `ws`. `exit` is `null` while it
  runs, else an [exit reason](#runner-exit-reasons). `mcp[].state` is
  `starting`, `connected` or `failed`; `loaded` counts tools added by
  `tool_search`.
- `state` for `runner` sessions
  ([0063](../../adrs/0063-runner-children-are-supervised-sessions.md)):
  `working` while the runner process is alive; `starting` for the first
  10 s with no `status.json`, or while the daemon is about to resume it
  after a crash; `idle` once it has exited normally; `stopped` when the
  process is gone without an exit and no resume is pending; `unknown` when
  `status.json` can't be read.
- `pid` is the runner's pid while alive, else `null`. `stale` is always
  `null` for runners.

### `ws machine wait`: each child

```json
{"id": "t1-impl-d8efb24f", "session": "rn-3f2a9c1e", "role": "implementer",
 "status": "idle", "finished": true, "outcome": "pass",
 "result": {"verdict": "PASS", "outcome": "pass", "report": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/report.md", "recorded_at": "2026-11-13T18:04:05Z"},
 "error": null,
 "runner": "ws",
 "model": "qwen3-coder:30b",
 "ws_runner": {"model_alias": "coder", "endpoint": "gpu", "exit": "done", "steps": 31, "restarts": 0,
               "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
               "wall_seconds": 1104,
               "usage_file": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/usage.json"}}
```

`runner`, `model` and `ws_runner` are as in `session` above. For a gone
child, they come from `usage.json` (a `ws` child) or are `null`.

### `ws machine logs`

```json
{"id": "t1-impl-d8efb24f", "session": "rn-3f2a9c1e", "runner": "ws", "model": "qwen3-coder:30b",
 "events": [
   {"time": "2026-11-13T17:46:01Z", "step": 1, "kind": "request", "messages": 2, "tools": 7, "estimated_tokens": 3120},
   {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "response", "content": "", "reasoning": "I'll read the task first.", "tool_calls": [{"id": "call_ab12cd34", "name": "read_file", "arguments": "{\"path\":\"docs/design/x/tasks.md\"}"}], "stop_reason": "tool_calls", "usage": {"input_tokens": 3051, "output_tokens": 41}},
   {"time": "2026-11-13T17:46:09Z", "step": 1, "kind": "tool_result", "id": "call_ab12cd34", "name": "read_file", "ok": true, "bytes": 8123, "truncated": false, "text": "1\t# x: tasks\n…"}
 ],
 "text": null,
 "truncated": true,
 "warnings": []}
```

For a Claude child, `events` is `[]` and `text` is `claude logs`' output.
`truncated` is `true` when `--tail` left events out.

## Files

Under `CONFIG_DIR` (mode 0700 directories, 0600 files).

### `state/<id>/session` (version 2)

```toml
version = 2
kind = "runner"
runner_session_id = "rn-3f2a9c1e"
started_at = 2026-11-13T17:45:58Z
```

`claude` and `shell` records keep `version = 1`.

### `state/<id>/runner/run.toml`

```toml
version = 1
session = "rn-3f2a9c1e"
model_alias = "coder"         # "" when --model gave a raw id
model = "qwen3-coder:30b"
endpoint = "gpu"
url = "http://ollama-a.inference.svc:11434"
api = "openai"
context_tokens = 65536
max_output = 4096
temperature = 0.2
request_timeout = "10m"
idle_timeout = "90s"
role = "implementer"          # "" without a role
pass = ["PASS"]
fail = ["FAIL"]
max_steps = 80
timeout = "60m"
max_tokens = 4000000
branch = "feat/x-t1"          # the role's BRANCH var, for the push guard; "" when none
mcp_servers = []
mcp_tools = ["*"]
mcp_deny = []
mcp_upfront = 8
user_instructions = false
prompt = """…the rendered role prompt…"""
created_at = 2026-11-13T17:45:58Z
```

No token, URL credential, header or environment value is ever written
here.

### `state/<id>/runner/status.json`

```json
{"version": 1, "session": "rn-3f2a9c1e", "pid": 51234, "process_started_at": "2026-11-13T17:45:58Z",
 "state": "working", "step": 12, "max_steps": 80, "restarts": 0,
 "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
 "mcp": [{"server": "mcp-grafana", "state": "connected", "tools": 6, "loaded": 3, "error": null}],
 "exit": null, "exit_message": null, "updated_at": "2026-11-13T17:52:40Z"}
```

`state` is `starting`, `working` (running a tool), `waiting_model` (a
request is in flight) or `exited`. Written to a temp file and renamed.
`restarts` is set by the daemon when it resumes after a crash.

### `state/<id>/runner/transcript.jsonl`

One JSON object per line:

| `kind` | Fields besides `time`, `step`, `kind` |
| ------ | ------------------------------------- |
| `start` | `model`, `model_alias`, `endpoint`, `api`, `context_tokens`, `resume` (bool), `messages` (system prompt and task, redacted), `tools` (names offered) |
| `request` | `messages`, `tools` (count), `estimated_tokens` |
| `response` | `content`, `reasoning`, `tool_calls` [{`id`,`name`,`arguments`}], `stop_reason`, `usage` {`input_tokens`,`output_tokens`} or `null`, `duration_ms` |
| `tool_result` | `id`, `name`, `ok`, `bytes`, `truncated`, `text` (as sent to the model); for MCP tools also `server`, `tool` |
| `invalid_call` | `id`, `name`, `error` |
| `nudge` | `reason` (`no_tool_call`, `repeat`) |
| `compaction` | `before_tokens`, `after_tokens`, `elided`, `dropped_turns` |
| `truncated` | `estimated_tokens`, `prompt_tokens` |
| `retry` | `attempt`, `error`, `wait_ms` |
| `mcp_start` | `server`, `transport` (`stdio`, `http`, `sse`), `ok`, `tools`, `allowed`, `error` |
| `mcp_error` | `server`, `tool` or `null`, `error` |
| `tool_search` | `query`, `loaded` (names), `dropped` (names) |
| `restart` | `restarts`, `reason` (written by the runner on `--resume`) |
| `exit` | `reason`, `message`, `verdict` or `null` |

Every event is redacted before it's written ([0066](../../adrs/0066-runner-security-boundaries.md)).

### `state/<root>/machine/<child>/usage.json`

Written once, at exit, next to `result.toml`. It outlives `ws machine rm`.

```json
{"version": 1, "runner": "ws", "model": "qwen3-coder:30b", "model_alias": "coder",
 "endpoint": "gpu", "url": "http://ollama-a.inference.svc:11434", "api": "openai",
 "role": "implementer", "exit": "done", "steps": 31, "restarts": 0,
 "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
 "mcp_calls": {"mcp-grafana": 4},
 "started_at": "2026-11-13T17:45:58Z", "ended_at": "2026-11-13T18:04:22Z", "wall_seconds": 1104,
 "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"}
```

`trace_id` is the W3C trace id sent in `traceparent` on every model
request. llm-router records it, so a run's requests can be found in Tempo.

### `CONFIG_DIR/daemon/`

| File | Holds |
| ---- | ----- |
| `lock` | The single-instance `flock` |
| `daemon.json` | `{"version":1,"pid","started_at","origin","socket","ws_version"}`, rewritten at start |
| `daemon.log` | The daemon's `slog` JSON log, and auto-started daemons' stderr |

### `CONFIG_DIR/secrets.env`

See [below](#secretsenv-and-the-runners-environment).

## config.toml

New keys, all optional. Set them with `ws config set` or Settings ›
Models. Names (`<alias>`, `<name>`, `<role>`) are 1–32 lowercase letters,
digits and `-`, starting with a letter. A `token` key anywhere in these
tables is an error: `tokens go in WS_ENDPOINT_TOKEN_<NAME> in the
environment or secrets.env, never in config.toml`.

### `[runner]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `model` | string | none | The default model alias for `ws` children |
| `fallback` | string | `"none"` | `none` or `claude`: what `spawn` does when preflight fails |
| `keep_git_tokens` | bool | `true` | Keep `GH_TOKEN` and `GITHUB_TOKEN` in the tools' environment |
| `user_instructions` | bool | `false` | Also load `~/.claude/CLAUDE.md` into the system prompt ([0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)) |

### `[runner.env]`

`NAME = "value"` pairs added to every runner's environment (layer 2 of
[0068](../../adrs/0068-ws-daemon.md)), for example `GRAFANA_URL`. Names are
`^[A-Z_][A-Z0-9_]*$`. A name ending `_TOKEN`, `_KEY`, `_SECRET` or
`_PASSWORD` is refused: `put secrets in secrets.env`.

### `[endpoints.<name>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `url` | string | required | Base URL without `/v1`: `http(s)://host[:port][/path]` |
| `api` | string | `"openai"` | The wire protocol: `openai` (OpenAI-compatible chat completions: Ollama, llm-router, LM Studio, vLLM). Reserved, refused as not implemented yet: `ollama`, `openai-responses`, `anthropic` |
| `probe` | string | `"auto"` | `auto`, `ollama` or `none`: how preflight learns capabilities and context length |
| `request_timeout` | duration | `"10m"` | One completion, including a cold model load |
| `idle_timeout` | duration | `"90s"` | The longest gap between streamed chunks |

### `[models.<alias>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `endpoint` | string | required | An `[endpoints.<name>]` |
| `id` | string | required | The model id on that endpoint (`qwen3-coder:30b`) |
| `context_tokens` | int | required | The server's context length for it (`OLLAMA_CONTEXT_LENGTH` or the model's `num_ctx`); at least 16384 |
| `max_output` | int | `4096` | Output tokens per request |
| `temperature` | float, as a string | `"0.2"` | Sampling temperature, `0`–`2` |

### `[runner.roles.<role>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `runner` | string | `"claude"` | `claude` or `ws`, for `spawn --role <role>` without `--runner` |
| `model` | string | `runner.model` | A model alias |
| `max_steps` | int | per role (implementer 80, reviewer 40, verifier 40, other 60) | Requests per run |
| `timeout` | duration | `"60m"` (reviewer `"30m"`) | Wall-clock limit per run |
| `max_tokens` | int | 4 000 000 (reviewer and verifier 2 000 000) | Prompt plus completion tokens per run |
| `mcp_servers` | strings | `[]` | MCP servers to start ([MCP servers](#mcp-servers)) |
| `mcp_tools` | strings | `["*"]` | Allowed tools: full `mcp__<server>__<tool>` names or globs |
| `mcp_deny` | strings | `[]` | Globs removed from the allowed tools |
| `mcp_upfront` | int | `8` | MCP tool definitions sent up front; more are reached with `tool_search` |

`<role>` is any role name; it doesn't have to exist yet. An endpoint or
model alias that isn't defined is `config_invalid` when the file is read.

### `[daemon]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `resume_on_start` | bool | `true` | Resume `stopped` runners when a daemon starts |

`ws config` keys: `runner.model`, `runner.fallback`,
`runner.keep_git_tokens`, `runner.user_instructions`, `runner.env.<NAME>`,
`endpoints.<name>.<key>`, `models.<alias>.<key>`,
`runner.roles.<role>.<key>`, `daemon.resume_on_start`. `ws config unset
endpoints.<name>` (or `models.<alias>`, `runner.roles.<role>`) removes the
whole table. New `ws config` types: `int` and `float`.

Example, with the commented block for the generated file in the
[README](README.md#configuration):

```toml
[runner]
model = "coder"

[runner.env]
GRAFANA_URL = "https://grafana.example.internal"

[endpoints.gpu]
url = "http://ollama-a.inference.svc:11434"

[models.coder]
endpoint = "gpu"
id = "qwen3-coder:30b"
context_tokens = 65536

[runner.roles.verifier]
runner = "ws"

[runner.roles.oncall]
runner = "ws"
mcp_servers = ["mcp-grafana"]
mcp_tools = ["mcp__mcp-grafana__query_*", "mcp__mcp-grafana__list_*", "mcp__mcp-grafana__get_*"]
mcp_deny = ["mcp__mcp-grafana__get_panel_image"]
```

## secrets.env and the runner's environment

`CONFIG_DIR/secrets.env`, mode 0600 (looser: ignored, with a warning in
`daemon.log` and `ws daemon status`):

```
# NAME=value, one per line; no expansion; optional double quotes; # comments
WS_ENDPOINT_TOKEN_ROUTER=…
GRAFANA_SERVICE_ACCOUNT_TOKEN=…
```

Settings › Models writes `WS_ENDPOINT_TOKEN_<NAME>` lines (temp file and
rename) and never reads a value back to the frontend.

A runner's environment, lowest precedence first ([0068](../../adrs/0068-ws-daemon.md)):

1. the daemon's environment when it started;
2. `[runner.env]`;
3. `secrets.env`;
4. `WS_RUNNER=ws`, `WS_WORKSPACE=<id>`, `WS_CONFIG_DIR`.

From it:

- **the endpoint token** is `WS_ENDPOINT_TOKEN_<NAME>` (name uppercased,
  `-` → `_`);
- **`run` commands** get it scrubbed ([0066](../../adrs/0066-runner-security-boundaries.md)):
  without `WS_ENDPOINT_TOKEN_*`, `WS_FORGEJO_TOKEN_*`, `*_API_KEY`,
  `*_TOKEN`, `*_SECRET`, `*_PASSWORD` and every name in `secrets.env`,
  except `GH_TOKEN` and `GITHUB_TOKEN` while `runner.keep_git_tokens` is
  true;
- **MCP stdio servers** get it unscrubbed, plus their own expanded `env`.

## MCP servers

([0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md))

| Source | Read from | Used when |
| ------ | --------- | --------- |
| project | `.mcp.json` at the session repo's root, `mcpServers` | named in the role's `mcp_servers` (wins over user) |
| user | `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`), `mcpServers` | named in the role's `mcp_servers` |

A definition is Claude Code's: `type` (`stdio`, the default when
`command` is set; `http`; `sse`), `command`, `args`, `env`, `url`,
`headers`. `${VAR}` and `${VAR:-default}` are expanded in those fields from
the runner's environment. `type: "ws"`, OAuth-only servers, plugin and
connector servers are skipped with a warning.

Tools are offered as `mcp__<server>__<tool>` (characters outside
`A-Za-z0-9_-` → `_`; over 64 characters: the first 55, `_`, and 8 hex
digits of the name's SHA-256). Allowed = matches `mcp_tools`, minus
`mcp_deny`; a tool without `readOnlyHint: true`, or with `destructiveHint:
true`, only when named exactly in `mcp_tools`. With more allowed tools than
`mcp_upfront`, the built-in `tool_search {query}` is offered.

| Limit | Value |
| ----- | ----- |
| Initialize and list tools | 30 s per server |
| One call | 120 s |
| Result text | 16 KiB, then `[truncated: N more bytes]` |
| Tools loaded by `tool_search` | 5 per search; at most 24 MCP tools per request |
| Deferred names in the system prompt | 2 KiB |

## The daemon socket

`$XDG_RUNTIME_DIR/ws/<h>.sock`, else `${TMPDIR:-/tmp}/ws-<uid>/<h>.sock`;
`<h>` = the first 12 hex digits of SHA-256(absolute `CONFIG_DIR`).
Directory 0700 and owned by the user, socket 0600, peer uid checked. HTTP/1.1,
JSON bodies, errors as `{"error": {"code", "message", "details"}}`.

| Request | Body | Answer |
| ------- | ---- | ------ |
| `GET /v1/status` | | `ws daemon status`'s object |
| `POST /v1/preflight` | `{"role", "endpoint", "model"}` (each `""` for the default) | `ws runner check`'s object, or a preflight error |
| `POST /v1/runners/<id>/start` | `{"resume": false}` | `{"pid"}`; `workspace_not_found`, `runner_failed` |
| `POST /v1/runners/<id>/stop` | | `{"stopped": true}` |
| `POST /v1/shutdown` | `{"keep_runners": false}` | `ws daemon stop`'s object |

## Environment variables

| Variable | Read by | Meaning |
| -------- | ------- | ------- |
| `WS_ENDPOINT_TOKEN_<NAME>` | the runner, the daemon's preflight | Bearer token for endpoint `<name>`; usually in `secrets.env` |
| `WS_DAEMON_AUTOSTART` | commands that need the daemon | `0`: don't auto-start; fail with `daemon_unavailable` |
| `WS_RUNNER`, `WS_WORKSPACE` | tools, MCP servers | Set by the daemon in a runner's environment |
| `WS_FAKE_OLLAMA`, `WS_FAKE_MCP` | e2e tests | Paths to prebuilt fakes; otherwise the harness builds them |
| `E2E_RUNNER_URL`, `E2E_RUNNER_MODEL`, `E2E_RUNNER_API` | e2e tests | Opt in to the real-model scenarios |
| `E2E_MCP_SMOKE` | e2e tests | `<server>:<tool>[,…]`: opt in to calling real MCP servers from the user's Claude Code config |

## Errors and exit codes

New codes, under existing exit codes:

| Code | Exit | Situation | `details` |
| ---- | ---- | --------- | --------- |
| `endpoint_unknown` | 3 | `--endpoint`, or the chosen model's endpoint, isn't in `config.toml` | `{"endpoints": ["gpu", …]}` |
| `endpoint_unreachable` | 1 | Preflight couldn't reach the endpoint, or got a 5xx | `{"url": "…", "cause": "connection refused"}` |
| `model_not_served` | 3 | The model id isn't in the endpoint's model list | `{"model": "…", "models": ["…"]}` |
| `model_without_tools` | 1 | The probe says the model has no `tools` capability | `{"model": "…", "capabilities": ["completion"]}` |
| `runner_failed` | 1 | The runner didn't start, or wrote no `status.json` within 10 s | `{"log": "<runner.log path>", "stderr": "…"}` |
| `daemon_unavailable` | 1 | No daemon answered and auto-start failed or is off, or the daemon is too old | `{"socket": "…", "log": "<daemon.log path>"}` |
| `daemon_running` | 4 | `ws daemon run` while another daemon holds the lock | `{"pid": 40211}` |

New `usage` (exit 2) situations: `--runner` isn't `claude` or `ws`;
`--endpoint` with the `claude` runner; for a `ws` child, an argument after
the prompt other than `--model <m>`. No model anywhere, a `token` key, a
secret-looking `[runner.env]` name, a reserved `api`, or `context_tokens`
under 16384 is `config_invalid` (exit 1).

`ws machine logs`: `outside_workspace` (2), `usage` (2),
`workspace_not_found` (3), `not_descendant` (4), and `claude_failed` (1)
when `claude logs` fails.

## Runner exit reasons

In `status.json` `exit`, `usage.json` `exit`, `session.ws_runner.exit`,
`wait`'s `ws_runner.exit`, and the first line of a report `ws` wrote
(`ws runner: <reason>`).

| Reason | Verdict recorded | Meaning |
| ------ | ---------------- | -------- |
| `done` | the model's | The model called `record_result` (or ran `ws machine result`) |
| `max_steps` | first fail | Step budget used up |
| `timeout` | first fail | Wall-clock budget used up |
| `max_tokens` | first fail | Token budget used up |
| `endpoint_down` | first fail | 3 retries failed: connection, 429/5xx, dropped or stalled stream |
| `model_no_tools` | first fail | The provider reported the model can't take tools |
| `model_missing` | first fail | The endpoint no longer serves the model |
| `malformed_tool_calls` | first fail | 3 invalid tool calls in a row |
| `no_result` | first fail | 3 replies in a row with no tool call |
| `looping` | first fail | The same call 5 times in a row |
| `context_overflow` | first fail | The system prompt, task and last turn don't fit |
| `crashed` | first fail, by the daemon | The process died 4 times; the daemon gave up |
| `stopped` | none | `ws rm` asked it to stop (`ws daemon stop` interrupts without an exit reason, so the runner reads `stopped` and resumes later) |
| `internal` | first fail | A runner bug; `exit_message` says what |
