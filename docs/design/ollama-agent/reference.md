# Runner: command reference (draft)

Draft of the reference for `ws`'s own agent runner and the `ws` daemon
([0062](../../adrs/0062-agent-runner-built-into-ws.md)–[0070](../../adrs/0070-runner-agents-for-people-and-loops.md)).
When this ships, its parts move into `docs/tools/ws-machine.md`,
`docs/tools/ws-daemon.md`, `docs/config.md`, `docs/state.md` and the `ws
llm` guides; the [README](README.md#proposed-changes-to-docstoolsws-machinemd)
gives the diff. Everything here is **added**: no existing field, error code
or exit code changes ([0016](../../adrs/0016-output-contract.md)).

Names: the runner value is `ws` (`--runner ws`), the session kind is
`runner`, runner session ids start `rn-`. A **model alias** is a
`[models.<alias>]` name; a **model id** is what the endpoint calls the
model (`qwen/qwen3.5-9b` in LM Studio, `qwen3.5:9b` in Ollama). An id is
kept exactly as written: `/` and `:` are part of it, never separators,
and there's no `provider:model` syntax. Ids appear only as string values,
never as keys or in file names.

- [Commands](#commands)
- [ws claude and ws loop](#ws-claude-and-ws-loop)
- [JSON output](#json-output)
- [Files](#files)
- [config.toml](#configtoml)
- [secrets.env and the runner's environment](#secretsenv-and-the-runners-environment)
- [Re-resolving the endpoint mid-run](#re-resolving-the-endpoint-mid-run)
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
ws claude [--runner claude|ws] [--endpoint <name>] [OPTIONS] [<spec>...] [-- "<prompt>" [<claude args>... | --model <m>]]
ws loop add <NAME> ... [--runner claude|ws] [--endpoint <name>] [--model <m>] ...
ws loop edit <NAME> ... [--runner claude|ws] [--endpoint <name>] [--model <m>] ...
ws daemon run [--origin transient|managed|app]
ws daemon status [--json]
ws daemon stop [--keep-runners] [--json]
ws runner run [--resume] <workspace-id>          (hidden; started by the daemon)
ws runner check [--json] [--model <m>] [<endpoint>]   (hidden; the desktop app's Test connection)
ws runner stop [--json] <workspace-id>           (hidden; the desktop app's Stop)
```

### `ws machine spawn` (new flags)

| Flag | Meaning |
| ---- | ------- |
| `--runner claude\|ws` | Which agent runs the child. Default: `runner.roles.<role>.runner` for `--role`, else `claude` |
| `--endpoint <name>` | Overrides the chosen model's endpoint; only with the `ws` runner |

For a `ws` child, the arguments after the prompt may only be `--model <m>`,
where `<m>` is a model alias, or a model id on the endpoint of the alias
the role's `model` or `runner.model` would choose (with that alias's
context size and a warning); anything else is `usage`. Use `-- "" --model <m>` to override only
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
runner.fallback = "claude"`. The fallback applies only here, before
anything is created; a running agent is never handed to Claude. Mid-run,
the runner [re-resolves its endpoint](#re-resolving-the-endpoint-mid-run)
instead.

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
for `ws-online`, launchd and systemd. `--origin app` is what the desktop
app starts: it acts as `managed` while its parent process lives and as
`transient` once the parent pid changes (the app quit)
([0068](../../adrs/0068-ws-daemon.md)). It takes `CONFIG_DIR/daemon/lock`;
when another daemon holds it, it fails with `daemon_running` (exit 4).
`slog` JSON logs go to stderr and `CONFIG_DIR/daemon/daemon.log`. `--json` is
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
{"endpoint": "ollama", "url": "http://ollama-a.inference.svc:11434", "api": "openai", "probe": "ollama",
 "model": "qwen3.5:9b", "version": "0.40.16", "capabilities": ["completion", "vision", "tools", "thinking"],
 "context_tokens": 65536, "models": ["qwen3.5:9b", "gemma3:12b"], "token_source": null,
 "warnings": []}
```

`version` and `capabilities` are `null` without a probe (as for an LM
Studio endpoint, which serves no `/api/version`). `token_source` is
`env`, `secrets.env` or `null`; the token itself is never printed. Errors
are the preflight codes.

### `ws runner stop` (hidden)

Stops one runner without removing it, for the desktop app's **Stop**:
`POST /v1/runners/<id>/stop` through the daemon (started if needed). The
runner exits `stopped` after its current step, records no verdict, and
keeps its workspace and files; it isn't resumed later. Prints
`{"id": "…", "session": "rn-…", "stopped": true, "warnings": []}`;
`stopped` is `false` when the runner had already exited. Errors:
`workspace_not_found` (3) when the workspace has no runner session,
`daemon_unavailable` (1). There's no `ws machine stop`
([0063](../../adrs/0063-runner-children-are-supervised-sessions.md)).

## ws claude and ws loop

([0070](../../adrs/0070-runner-agents-for-people-and-loops.md))

**`ws claude`** gains:

| Flag | Meaning |
| ---- | ------- |
| `--runner claude\|ws` | Which agent runs it. Default `claude` |
| `--endpoint <name>` | Overrides the model's endpoint; only with `ws` |

With `ws`, a prompt after `--` is required (`usage`: `a ws runner agent
needs a prompt; it can't be attached`), only `--model <m>` may follow it
(an alias or a raw id; default `runner.model`), and the start is the same
as `ws machine spawn`'s, with no role and the parent `ws claude` already
chooses (`--parent`, else the workspace it runs in; `run.toml`'s `parent`
is `""` for none, and then the results stay in the session's own
`runner/` directory). The output gains, after the existing fields:

```json
{"id": "fix-docs-2b4d6f80", "session": "rn-7a1c3e5b", "path": "/…/workspaces/fix-docs-2b4d6f80",
 "repos": [{"id": "github.com/example/notes", "path": "notes", "ref": "main", "ref_kind": "branch", "commit": "9c1e…"}],
 "warnings": [], "runner": "ws", "model": "qwen/qwen3.5-9b",
 "ws_runner": {"model_alias": "qwen9b", "endpoint": "gpu", "url": "http://gpu-a.inference.svc:1234",
               "api": "openai", "context_tokens": 65536, "mcp_servers": []}}
```

A Claude agent's output gains `"runner": "claude"`, `"model"` (its
`--model` or `null`) and `"ws_runner": null`.

**`ws loop add` and `ws loop edit`** gain:

| Option | Meaning |
| ------ | ------- |
| `--runner claude\|ws` | What runs each run; default `claude`. `edit --runner claude` clears `endpoint` |
| `--endpoint <name>` | Only with `ws`: overrides the model's endpoint; `--endpoint ""` clears it |

`--model` is a Claude model for a `claude` loop and a model alias or raw
id for a `ws` loop (unset: `runner.model` at each run). `loop_invalid`
when: `permission_mode` is set on a `ws` loop; `endpoint` on a `claude`
loop; the alias or endpoint isn't in `config.toml`. The record gains
`runner = "ws"` and `endpoint`, written only when set; a record with
either is version 4. The loop JSON gains, after `hidden`: `"runner":
"claude"` or `"ws"`, and `"endpoint": null` or a name.

A `ws` loop's run is started like `ws claude --runner ws`. A failed
preflight fails the run with its code in `error_code`
(`endpoint_unreachable`, `model_not_served`, `model_without_tools`,
`endpoint_unknown`, `daemon_unavailable`, `config_invalid`).

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
  "model": "qwen/qwen3.5-9b",
  "ws_runner": {"model_alias": "qwen9b", "endpoint": "gpu", "url": "http://gpu-a.inference.svc:1234",
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
            "runner": "ws", "model": "qwen/qwen3.5-9b",
            "ws_runner": {"model_alias": "qwen9b", "endpoint": "gpu", "step": 12, "max_steps": 80, "restarts": 0,
                          "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
                          "mcp": [{"server": "metrics", "state": "connected", "tools": 6, "loaded": 3}],
                          "exit": null, "endpoint_changes": 0}}
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
  runs, else an [exit reason](#runner-exit-reasons). `endpoint` and the
  session's `model` are the ones the run uses now (from `status.json`);
  `endpoint_changes` counts its switches
  ([re-resolution](#re-resolving-the-endpoint-mid-run)), added after the
  existing fields. `mcp[].state` is
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
 "model": "qwen/qwen3.5-9b",
 "ws_runner": {"model_alias": "qwen9b", "endpoint": "gpu", "exit": "done", "steps": 31, "restarts": 0,
               "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
               "wall_seconds": 1104,
               "usage_file": "/…/state/orch-1a2b3c4d/machine/t1-impl-d8efb24f/usage.json",
               "endpoint_changes": 0}}
```

`runner`, `model` and `ws_runner` are as in `session` above. For a gone
child, they come from `usage.json` (a `ws` child) or are `null`. `endpoint`
and `model` are the last ones the run used; `endpoint_changes` is the
length of `usage.json`'s `endpoints` minus one.

### `ws machine logs`

```json
{"id": "t1-impl-d8efb24f", "session": "rn-3f2a9c1e", "runner": "ws", "model": "qwen/qwen3.5-9b",
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
model_alias = "qwen9b"         # "" when --model gave a raw id
model = "qwen/qwen3.5-9b"      # this key and the next six: as resolved at spawn
endpoint = "gpu"
url = "http://gpu-a.inference.svc:1234"
api = "openai"
context_tokens = 65536
max_output = 4096
temperature = 0.7
reasoning = "none"             # "" for the server's default
request_timeout = "10m"
idle_timeout = "90s"
role = "implementer"          # "" without a role
parent = "orch-1a2b3c4d"      # "" without one (a loop run, ws claude --parent none): results stay in runner/
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
trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"   # sent in traceparent; kept across --resume
created_at = 2026-11-13T17:45:58Z
endpoint_alias = ""            # for a raw id: the alias whose endpoint it runs on; else ""
endpoint_flag = ""             # --endpoint, or ""

[[endpoint_history]]           # written by the spawn
since = 2026-11-13T17:45:58Z
step = 0
reason = "spawn"               # spawn | retry | resume
endpoint = "gpu"
url = "http://gpu-a.inference.svc:1234"
api = "openai"
model = "qwen/qwen3.5-9b"
context_tokens = 65536

[[endpoint_history]]           # appended by the runner when it switched
since = 2026-11-13T17:52:31Z
step = 12
reason = "retry"
endpoint = "gpu-b"
url = "http://gpu-b.inference.svc:1234"
api = "openai"
model = "qwen/qwen3.5-9b"
context_tokens = 65536
```

`model_alias`, `endpoint_alias` and `endpoint_flag` are what
[re-resolution](#re-resolving-the-endpoint-mid-run) starts from. The
last `endpoint_history` entry is the endpoint the run uses; a resume
starts from it. After the spawn, only the runner writes this file, and
only to append an entry (temp file and rename). No token, URL credential,
header or environment value is ever written here.

### `state/<id>/runner/status.json`

```json
{"version": 1, "session": "rn-3f2a9c1e", "pid": 51234, "process_started_at": "2026-11-13T17:45:58Z",
 "state": "working", "step": 12, "max_steps": 80, "restarts": 0,
 "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120},
 "mcp": [{"server": "metrics", "state": "connected", "tools": 6, "loaded": 3, "error": null}],
 "exit": null, "exit_message": null, "verdict": null, "updated_at": "2026-11-13T17:52:40Z",
 "endpoint": "gpu", "model": "qwen/qwen3.5-9b", "endpoint_changes": 0,
 "endpoints": [{"endpoint": "gpu", "model": "qwen/qwen3.5-9b",
                "usage": {"requests": 12, "prompt_tokens": 210344, "completion_tokens": 9120}}]}
```

`verdict` is the recorded verdict once there is one, from the model or
the finalizer, for every run (with or without a parent). `state` is `starting`, `working` (running a tool), `waiting_model` (a
request is in flight) or `exited`. Written to a temp file and renamed.
`restarts` is set by the daemon when it resumes after a crash. `endpoint`
and `model` are the current target (the last `endpoint_history` entry);
`endpoint_changes` counts switches since the spawn; `endpoints` is the
usage per endpoint used, one entry per `endpoint_history` entry, kept
across resumes and copied into `usage.json`.

### `state/<id>/runner/transcript.jsonl`

One JSON object per line:

| `kind` | Fields besides `time`, `step`, `kind` |
| ------ | ------------------------------------- |
| `start` | `model`, `model_alias`, `endpoint`, `api`, `context_tokens`, `reasoning`, `resume` (bool), `trace_id`, `messages` (system prompt and task, redacted), `tools` (names offered) |
| `request` | `messages`, `tools` (count), `estimated_tokens` |
| `response` | `content`, `reasoning`, `tool_calls` [{`id`,`name`,`arguments`}], `stop_reason`, `usage` {`input_tokens`,`output_tokens`} or `null`, `duration_ms`, `span_id` (the request's `chat` span, or `null` without telemetry) |
| `tool_result` | `id`, `name`, `ok`, `bytes`, `truncated`, `text` (as sent to the model); for MCP tools also `server`, `tool` |
| `invalid_call` | `id`, `name`, `error` |
| `nudge` | `reason` (`no_tool_call`, `text_tool_call`, `repeat`) |
| `compaction` | `before_tokens`, `after_tokens`, `elided`, `dropped_turns` |
| `truncated` | `estimated_tokens`, `prompt_tokens` |
| `retry` | `attempt`, `error`, `wait_ms`, `endpoint` (the name the failed attempt went to) |
| `endpoint_changed` | `reason` (`retry`, `resume`), `attempt` (`null` for `resume`), `from_endpoint`, `to_endpoint`, `from_model`, `to_model`, `from_context_tokens`, `to_context_tokens`, `api`, `changed` (the keys that differ: `endpoint`, `url`, `api`, `id`, `context_tokens`, `max_output`, `temperature`, `reasoning`, `request_timeout`, `idle_timeout`), `version` and `capabilities` from the probe (`null` without one) |
| `endpoint_rejected` | `reason`, `attempt`, `endpoint` (the rejected one's name), `model`, `code` (`model_not_served`, `model_without_tools`, `context_too_small`; at a resume also `endpoint_unreachable`), `message`, `kept_endpoint` |
| `config_warning` | `code` (`config_invalid`, `model_gone`, `endpoint_unknown`, `api_not_implemented`), `message` (names the key, never a value), `kept_endpoint` |
| `mcp_start` | `server`, `transport` (`stdio`, `http`, `sse`), `ok`, `tools`, `allowed`, `error` |
| `mcp_error` | `server`, `tool` or `null`, `error` |
| `tool_search` | `query`, `loaded` (names), `dropped` (names) |
| `restart` | `restarts`, `reason`, `trace_id` (written by the runner on `--resume`) |
| `exit` | `reason`, `message`, `verdict` or `null` |

Every event is redacted before it's written ([0066](../../adrs/0066-runner-security-boundaries.md)).
Endpoint events carry names and model ids, never a URL, token or header.

### `state/<root>/machine/<child>/usage.json`

Written once, at exit, next to `result.toml`. It outlives `ws machine rm`.
A run without a parent writes it, and `report.md`, to
`state/<id>/runner/` instead.

```json
{"version": 1, "runner": "ws", "model": "qwen/qwen3.5-9b", "model_alias": "qwen9b",
 "endpoint": "gpu", "url": "http://gpu-a.inference.svc:1234", "api": "openai",
 "role": "implementer", "exit": "done", "steps": 31, "restarts": 0,
 "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230},
 "mcp_calls": {"metrics": 4},
 "endpoints": [{"endpoint": "gpu", "url": "http://gpu-a.inference.svc:1234", "api": "openai", "model": "qwen/qwen3.5-9b",
                "usage": {"requests": 33, "prompt_tokens": 512004, "completion_tokens": 18230}}],
 "started_at": "2026-11-13T17:45:58Z", "ended_at": "2026-11-13T18:04:22Z", "wall_seconds": 1104,
 "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"}
```

`endpoint`, `url`, `api` and `model` are the last ones the run used;
`endpoints` has one entry per `endpoint_history` entry, in order, with the
requests and tokens sent there (a failed attempt counts as a request).
`trace_id` is the run's OpenTelemetry trace id: every span the runner
exports belongs to it, and each model request's `traceparent` carries it,
so llm-router's spans join the same trace
([0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md),
Observability).

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
| `model` | string | none | The default model alias for `ws` agents (children, `ws claude --runner ws`, `ws` loops) |
| `fallback` | string | `"none"` | `none` or `claude`: what a start (spawn, `ws claude`, a loop run) does when preflight fails. Never applies mid-run |
| `keep_git_tokens` | bool | `true` | Keep `GH_TOKEN` and `GITHUB_TOKEN` in the tools' environment |
| `user_instructions` | bool | `false` | Also load `~/.claude/CLAUDE.md` into the system prompt; off by the owner's decision, an opt-in per machine ([0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)) |

### `[runner.env]`

`NAME = "value"` pairs added to every runner's environment (layer 2 of
[0068](../../adrs/0068-ws-daemon.md)), for example `METRICS_URL` or
`OTEL_EXPORTER_OTLP_ENDPOINT`. Names are
`^[A-Z_][A-Z0-9_]*$`. A name ending `_TOKEN`, `_KEY`, `_SECRET` or
`_PASSWORD` is refused: `put secrets in secrets.env`.

### `[endpoints.<name>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `url` | string | required | Base URL without `/v1`: `http(s)://host[:port][/path]` |
| `api` | string | `"openai"` | The wire protocol: `openai` (OpenAI-compatible chat completions: Ollama, llm-router, LM Studio, vLLM). Refused as not implemented yet: `ollama` (planned next, the optional last task), `openai-responses`, `anthropic` (reserved) |
| `probe` | string | `"auto"` | `auto`, `ollama` or `none`: how preflight learns capabilities and context length |
| `request_timeout` | duration | `"10m"` | One completion, including a cold model load |
| `idle_timeout` | duration | `"90s"` | The longest gap between streamed chunks |

### `[models.<alias>]`

| Key | Type | Default | Used for |
| --- | ---- | ------- | -------- |
| `endpoint` | string | required | An `[endpoints.<name>]` |
| `id` | string | required | The model id on that endpoint, exactly as it lists it (`qwen/qwen3.5-9b`, `qwen3.5:9b`); any non-empty string without control characters |
| `context_tokens` | int | required | The server's context length for it (`OLLAMA_CONTEXT_LENGTH` or the model's `num_ctx`); at least 16384 |
| `max_output` | int | `4096` | Output tokens per request |
| `temperature` | float, as a string | `"0.2"` | Sampling temperature, `0`–`2` |
| `reasoning` | string | `""` | `""` (send nothing: the server's default), `none`, `low`, `medium` or `high`; sent as `reasoning_effort` ([0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md)) |

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
model = "qwen9b"

[runner.env]
METRICS_URL = "https://metrics.example.internal"
OTEL_EXPORTER_OTLP_ENDPOINT = "http://otel-collector.example.internal:4318"

[endpoints.gpu]
url = "http://gpu-a.inference.svc:1234"

[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 65536
reasoning = "none"
temperature = "0.7"

[models.qwen9b-think]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 131072
max_output = 16384
temperature = "0.6"

[runner.roles.verifier]
runner = "ws"
model = "qwen9b"
max_steps = 30
timeout = "30m"
max_tokens = 1500000

# An example of a role with MCP tools; no role has any by default.
[runner.roles.triage]
runner = "ws"
model = "qwen9b-think"
mcp_servers = ["metrics"]
mcp_tools = ["mcp__metrics__query_*", "mcp__metrics__list_*", "mcp__metrics__get_*"]
mcp_deny = ["mcp__metrics__get_panel_image"]
```

## secrets.env and the runner's environment

`CONFIG_DIR/secrets.env`, mode 0600 (looser: ignored, with a warning in
`daemon.log` and `ws daemon status`):

```
# NAME=value, one per line; no expansion; optional double quotes; # comments
WS_ENDPOINT_TOKEN_ROUTER=…
METRICS_TOKEN=…
OTEL_EXPORTER_OTLP_HEADERS=authorization=Bearer …
```

Settings › Models writes `WS_ENDPOINT_TOKEN_<NAME>` lines (temp file and
rename) and never reads a value back to the frontend.

A runner's environment, lowest precedence first ([0068](../../adrs/0068-ws-daemon.md)):

1. the daemon's environment when it started;
2. `[runner.env]`;
3. `secrets.env`;
4. `WS_RUNNER=ws`, `WS_WORKSPACE=<id>`, `WS_CONFIG_DIR`, `WS_RUNNER_SECRET_NAMES`.

From it:

- **the endpoint token** is `WS_ENDPOINT_TOKEN_<NAME>` (name uppercased,
  `-` → `_`);
- **`run` commands** get it scrubbed ([0066](../../adrs/0066-runner-security-boundaries.md)):
  without `WS_ENDPOINT_TOKEN_*`, `WS_FORGEJO_TOKEN_*`, `*_API_KEY`,
  `*_TOKEN`, `*_SECRET`, `*_PASSWORD`, `OTEL_EXPORTER_OTLP_HEADERS`,
  `OTEL_EXPORTER_OTLP_*_HEADERS` and every name in `secrets.env`,
  except `GH_TOKEN` and `GITHUB_TOKEN` while `runner.keep_git_tokens` is
  true;
- **MCP stdio servers** get it unscrubbed, plus their own expanded `env`;
- **the runner's telemetry** reads the standard `OTEL_*` variables from it
  ([0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md)).

`secrets.env` is the only secrets store.

## Re-resolving the endpoint mid-run

([0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md),
third review.) A run is bound to its model alias, not to the URL it
resolved at spawn.

| | |
| - | - |
| Fixed for the run | `model_alias` (or a raw `model` with `endpoint_alias`), `endpoint_flag`, role, budgets, MCP keys |
| Read again | the alias's `id`, `endpoint`, `context_tokens`, `max_output`, `temperature`, `reasoning`; that endpoint's (or `endpoint_flag`'s) `url`, `api`, `probe`, `request_timeout`, `idle_timeout`; its token |
| When | after each retry's backoff, just before the retry's request; and at `ws runner run --resume`, before the first request. Never while requests succeed |
| Retryable | connection refused, reset or timed out; `request_timeout`; 429; any 5xx; a dropped or stalled stream |
| Who reads | the runner: `$WS_CONFIG_DIR/config.toml` (parsed and validated as every `ws` command does; never created), and `$WS_CONFIG_DIR/secrets.env` for the new endpoint's token only, else its environment. The daemon pushes nothing |
| Check before a switch | when the endpoint name, `url`, `api` or model id differs: the spawn's preflight against the new endpoint with its token (`Models`, the id listed, `Probe`), 10 s per request; then the loop's window check |
| Budget | 3 retries per request, across endpoints; a switch doesn't reset them; an unreachable new endpoint spends one |

```mermaid
sequenceDiagram
    participant L as Loop
    participant R as Runner (re-resolver)
    participant C as config.toml and secrets.env
    participant A as Current endpoint
    participant B as New endpoint
    L->>A: request (attempt n)
    A-->>L: connection error, timeout or 5xx
    L->>L: retry event, then backoff (2 s, 8 s, 30 s or Retry-After)
    L->>R: re-resolve (current target, attempt)
    R->>C: read config.toml; resolve the run's alias
    alt unreadable or unusable
        R-->>L: keep the current target (config_warning)
    else same target
        R-->>L: keep the current target
    else another endpoint, URL, api or model id
        R->>C: read the new endpoint's token
        R->>B: preflight: models, then probe
        alt served, with tools
            R-->>L: new target
            L->>L: recompute the window; does the conversation fit?
        else not served, no tools, or does not fit
            R-->>L: keep the current target (endpoint_rejected)
        end
    end
    L->>B: request (attempt n+1) to the target chosen
```

Outcomes for one retry:

| Live configuration | What the runner does | Records |
| ------------------ | -------------------- | ------- |
| Unchanged | retries the current endpoint | `retry` |
| Points elsewhere; the check passes | switches; the token, provider and window follow the new endpoint | `endpoint_changed`, an `endpoint_history` entry, `status.json` `endpoint` and `endpoint_changes` |
| Points elsewhere; unreachable | stays; this attempt counts as failed | `retry` with the new endpoint's name |
| Points elsewhere; id not served, no tools, or the conversation doesn't fit the smaller window | stays | `endpoint_rejected` |
| Doesn't parse or validate; the alias or `endpoint_flag` is gone; `api` not implemented | stays | `config_warning`, and a `runner.log` warning |
| Only `max_output`, `temperature`, `reasoning` or a timeout changed | applies them, no probe (a smaller window is still checked) | `endpoint_changed` with `changed` naming them |

At a resume there's no retry to spend: an unreachable new endpoint keeps
the last one with an `endpoint_rejected` event, code
`endpoint_unreachable`, and the first request then retries as usual.

When the third retry fails: `endpoint_down` if the last attempt went to
the endpoint the live configuration resolves to; `endpoint_rejected` if
the live configuration points at an endpoint the check refused. A switch
to an endpoint with `probe = "none"` can't check tools; a 400 "does not
support tools" from it ends the run `model_no_tools`, with the endpoint's
name in `exit_message`. A switch to another `api` builds a new provider,
and reasoning another provider must receive verbatim is dropped with a
transcript note ([0062](../../adrs/0062-agent-runner-built-into-ws.md)).
Edits reach a run through `ws config set` or Settings › Models (both
replace `config.toml` atomically) or any editor that does the same; a
healthy run sees them only at its next retry or resume.

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
| `POST /v1/runners/<id>/stop` | | `{"stopped": true}`, or `false` when it had already exited (`ws rm`, `ws runner stop`) |
| `POST /v1/shutdown` | `{"keep_runners": false}` | `ws daemon stop`'s object |

## Environment variables

| Variable | Read by | Meaning |
| -------- | ------- | ------- |
| `WS_ENDPOINT_TOKEN_<NAME>` | the runner, the daemon's preflight | Bearer token for endpoint `<name>`; usually in `secrets.env`. When a run switches endpoint, the runner reads the new endpoint's from `secrets.env` as it is then, else from its environment |
| `WS_CONFIG_DIR` | the runner | Set by the daemon; where the runner re-reads `config.toml` and `secrets.env` when it re-resolves |
| `WS_DAEMON_AUTOSTART` | commands that need the daemon | `0`: don't auto-start; fail with `daemon_unavailable` |
| `WS_RUNNER`, `WS_WORKSPACE` | tools, MCP servers | Set by the daemon in a runner's environment |
| `WS_RUNNER_SECRET_NAMES` | the runner | Set by the daemon: the names defined in `secrets.env`, comma-separated, so `run` commands get them scrubbed |
| `WS_FAKE_OLLAMA`, `WS_FAKE_MCP` | e2e tests | Paths to prebuilt fakes; otherwise the harness builds them |
| `E2E_RUNNER_URL`, `E2E_RUNNER_MODEL`, `E2E_RUNNER_API` | e2e tests | Opt in to the real-model scenarios |
| `E2E_MCP_SMOKE` | e2e tests | `<server>:<tool>[,…]` (for example `docs:search`): opt in to calling real MCP servers from the user's Claude Code config |
| `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_*_ENDPOINT`, `OTEL_*_EXPORTER`, `OTEL_EXPORTER_OTLP_HEADERS`, `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` | the runner | Standard OpenTelemetry settings. Without an endpoint or exporter variable nothing is exported; `service.name` defaults to `ws-runner` |

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
`--endpoint` with the `claude` runner; for a `ws` child or `ws claude
--runner ws`, an argument after the prompt other than `--model <m>`; `ws
claude --runner ws` without a prompt. `ws loop add|edit`: a bad
`--runner`, `--endpoint` or `--permission-mode` with the other runner, or
an unknown alias or endpoint, is `loop_invalid` (2). No model anywhere, a `token` key, a
secret-looking `[runner.env]` name, an `api` not implemented yet, a bad
`reasoning`, or `context_tokens`
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
| `endpoint_down` | first fail | 3 retries failed (connection, timeout, 429/5xx, dropped or stalled stream), the last one against the endpoint the live configuration resolves to |
| `endpoint_rejected` | first fail | 3 retries failed while the live configuration points the alias at an endpoint the switch check refused; `exit_message` names it and the code (`model_not_served`, `model_without_tools`, `context_too_small`) |
| `model_no_tools` | first fail | The provider reported the model can't take tools |
| `model_missing` | first fail | The endpoint no longer serves the model |
| `malformed_tool_calls` | first fail | 3 invalid tool calls in a row |
| `no_result` | first fail | 3 replies in a row with no tool call |
| `looping` | first fail | The same call 5 times in a row |
| `context_overflow` | first fail | The system prompt, task and last turn don't fit |
| `crashed` | first fail, by the daemon | The process died 4 times; the daemon gave up |
| `stopped` | none | `ws rm` or the desktop's Stop (`ws runner stop`) asked it to stop; it isn't resumed. (`ws daemon stop` interrupts without an exit reason, so the runner reads `stopped` and resumes later) |
| `internal` | first fail | A runner bug; `exit_message` says what |
