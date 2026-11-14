# 0064. Choosing the runner and model: `--runner`, `[models]`, `[endpoints]` and `[runner.roles]` in `config.toml`, and a fail-fast preflight

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (provider-neutral
  config, secrets in `secrets.env`, preflight in the daemon); revised
  2026-11-14 after the second review (`qwen/qwen3.5-9b` as the starting
  model, `reasoning`, ids with `/` and `:`); revised 2026-11-14 after the
  third review (a run re-resolves its model alias mid-run; no mid-run
  fallback to Claude)

## Context

### Problem

An orchestrator decides per child whether it runs on Claude Code or on
`ws`'s runner. The user decides which models exist, where they're served,
over which API, and which model each role gets. How does a spawn pick its
runner and model? What is configured where? What happens when the endpoint
is down or the model can't call tools? And, since endpoints can change
while a run is in progress (the third review), what does a running agent do
when its endpoint fails or the configuration points its model elsewhere?

### What a solution needs

1. **Spawns stay short.** Orchestrators' existing spawn lines work unchanged, and
   moving a role to another model is a config change.
2. **Fail fast, never silently.** A bad endpoint or model fails the spawn
   with a clear error code. No fallback to Claude unless configured, and
   then the output says so.
3. **No secrets in `config.toml`** ([0061](0061-hosts-in-config-toml.md)).
4. **Editable with `ws config`**, which edits named tables and plain values
   ([0054](0054-settings-all.md)).
5. **Provider-neutral**: the same keys describe an Ollama server, llm-router,
   LM Studio, vLLM, and later the OpenAI and Anthropic APIs
   ([0062](0062-agent-runner-built-into-ws.md)).
6. **Context size is honest**: over the OpenAI-compatible API it's the
   server's, not the request's[^ollama-openai-ctx].
7. **Endpoints can change mid-run** (the owner's answer to the second
   draft's open question). A run follows its model alias to wherever the
   configuration points it now, without a restart, and never moves to a
   model it hasn't checked.

### Relevant facts

- Ollama reports `capabilities` (`completion`, `tools`, `thinking`, …) and
  `model_info.<family>.context_length` from `POST /api/show`[^ollama-show],
  and `GET /api/version`. A request with tools to a model without them gets
  400 `{"error":"<model> does not support tools"}`[^ollama-src-tools];
  llm-router passes a backend's 4xx through[^router-errors].
- Ollama truncates the oldest history silently past the context
  length[^ollama-src-truncate].
- Model ids contain `:`, `.` and `/`: the same Qwen3.5 9B model is
  `qwen/qwen3.5-9b` in LM Studio and `qwen3.5:9b` in Ollama's
  library[^qwen-ids], and Claude's are `claude-sonnet-5-5`. So they can't
  be bare TOML keys, and nothing may split them on `:` or `/`.
- Qwen3.5 models think by default; Ollama's and LM Studio's
  OpenAI-compatible APIs take `reasoning_effort` to change
  that[^ollama-openai-ctx][^lmstudio-effort]. With thinking on, a low output
  limit can leave `content` empty, and LM Studio has an open report that
  `enableThinking: false` is ignored on `/v1`[^lmstudio-1990].
- `ws config` edits `[review.profiles.<name>]` tables, string, string-list,
  bool and duration values; dotted and inline forms are refused[^config].
- `ws machine spawn` passes everything after the prompt to `claude`.
  Orchestrators override the model with `-- "" --model haiku`.

## Decision

**Choosing the runner**, first match wins:

1. `ws machine spawn --runner claude|ws`;
2. `runner.roles.<role>.runner` in `config.toml`, for `--role <role>`;
3. `claude`.

**Choosing the model** for a `ws` child, first match wins:

1. `--model <m>` after the prompt (the only `claude`-style argument a `ws`
   child accepts, so `-- "" --model qwen9b-think` works as it does for Claude);
2. `runner.roles.<role>.model`;
3. `runner.model`;
4. otherwise `config_invalid`.

`<m>` is a **model alias**, the name of a `[models.<m>]` table. When no
alias has that name, `<m>` is taken as a raw model id on the endpoint of
the alias that steps 2–3 would have chosen, with that alias's context
size, and a warning says so; with no such alias it's `config_invalid`. A
role file's `model` (`haiku`, `sonnet`) is a Claude alias and is ignored for
`ws` children.

`--endpoint <name>` overrides the chosen model's endpoint for one spawn.
Any argument after the prompt other than `--model` is `usage` for a `ws`
child.

**Configuration** (full schema in
[reference.md](../design/ollama-agent/reference.md#configtoml)):

```toml
[runner]
model = "qwen9b"           # default model alias for ws children
fallback = "none"          # none | claude: what spawn does when preflight fails

[endpoints.gpu]
url = "http://gpu-a.inference.svc:1234"
api = "openai"             # the wire protocol; the default. Next: ollama. Reserved: openai-responses, anthropic
probe = "auto"             # auto | ollama | none: how preflight learns a model's capabilities
request_timeout = "10m"    # one completion, including a cold model load
idle_timeout = "90s"       # longest gap between streamed chunks

[endpoints.ollama]
url = "http://ollama-a.inference.svc:11434"

[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"     # the id the endpoint knows; "/" and ":" are part of it
context_tokens = 65536     # the server's context length for it; required
reasoning = "none"         # "" (the server's default) | none | low | medium | high
temperature = "0.7"

[models.qwen9b-think]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 131072
max_output = 16384         # thinking spends output tokens before the answer
temperature = "0.6"

[models.qwen9b-ollama]
endpoint = "ollama"
id = "qwen3.5:9b"          # the same model in Ollama's library
context_tokens = 65536     # must match the server's num_ctx for it
reasoning = "none"

[runner.roles.verifier]
runner = "ws"
model = "qwen9b"
max_steps = 30
timeout = "30m"
max_tokens = 1500000
```

Endpoint, model and role names are lowercase letters, digits and hyphens.
A model **id** is any non-empty string without control characters, kept
exactly as written: there's no `provider:model` or `runner:model` syntax
anywhere, so `:` and `/` are never separators. `--model qwen/qwen3.5-9b`
and `--model qwen3.5:9b` are raw ids (they can't be aliases, whose names
have neither character). Ids appear only as TOML and JSON string values,
never in a key or a file name.

**Reasoning.** `reasoning` on a model is `""` (send nothing: the server's
default), `none`, `low`, `medium` or `high`. The `openai` provider sends it
as `reasoning_effort`; reasoning text that comes back in `reasoning` or
`reasoning_content` is kept for the transcript and never sent back
([0062](0062-agent-runner-built-into-ws.md)). Whether a server honours it
is the server's business: the transcript shows reasoning that came back
anyway.
Per-role MCP keys are in [0069](0069-runner-mcp-tools-and-claude-code-parity.md);
`[runner.env]` and `[daemon]` are in [0068](0068-ws-daemon.md).

**Tokens.** A `token` key anywhere is an error. An endpoint's bearer token
(sent as `Authorization: Bearer`; for `api = "anthropic"` later, as that
API's key header) is the variable `WS_ENDPOINT_TOKEN_<NAME>` (the name
uppercased, `-` → `_`) in the runner's environment, which the daemon builds
from its own environment and `CONFIG_DIR/secrets.env`
([0068](0068-ws-daemon.md)). Settings › Models writes that line to
`secrets.env`. Without a token, the `openai` provider sends a placeholder
key, which Ollama ignores[^ollama-openai].

**Preflight**, run by the daemon for `ws machine spawn` before the
workspace is created, with a 10 s limit per request:

| Check | Fails with | Exit |
| ----- | ---------- | ---- |
| The endpoint name is in `config.toml` | `endpoint_unknown` | 3 |
| `api` is implemented | `config_invalid` | 1 |
| `Models` answers (`GET /v1/models`) | `endpoint_unreachable`, `details.url`, `details.cause` | 1 |
| The model id is listed | `model_not_served`, `details.models` | 3 |
| `Probe` (below) says the model has tools | `model_without_tools`, `details.capabilities` | 1 |
| `Probe`'s context length is at least `context_tokens`; otherwise `context_tokens` is lowered to it, with a warning | none (warning) | |
| `context_tokens` is at least 16384 | `config_invalid` | 1 |

`probe` decides how `Probe` works: `ollama` uses `/api/version` and
`/api/show`; `none` skips it; `auto` (the default) tries `GET /api/version`
once and uses `ollama` if it answers, `none` otherwise. With no probe, a
model without tools is caught by the first request's 400: the run stops at
once with exit reason `model_no_tools` and a fail verdict.

**Fallback.** With `runner.fallback = "claude"`, and only for
`endpoint_unreachable`, `model_not_served` and `model_without_tools`, the
spawn starts a Claude child instead. Its output says `"runner": "claude"`
and carries a warning naming the preflight error. There's never a fallback
once a runner has started: mid-run, the runner re-resolves (below) and,
when that fails too, records an exit reason for the orchestrator. Handing
a running agent to Claude was considered and rejected (Alternatives).

**Re-resolution mid-run** (third review). A run is bound to its model
alias, not to the endpoint URL it resolved at spawn.

- **What is fixed.** Spawn writes to `run.toml` what it resolved from:
  `model_alias` (for a raw id: the id, and `endpoint_alias`, the alias
  whose endpoint it runs on) and `endpoint_flag` (`--endpoint`, or `""`).
  Those stay fixed for the run: a run never moves to another alias, even
  when the role's `model` or `runner.model` changes. Budgets and MCP keys
  are fixed too. What the alias points at is read again: its `id`,
  `endpoint`, `context_tokens`, `max_output`, `temperature`, `reasoning`,
  and that endpoint's `url`, `api`, `probe` and timeouts.
- **When.** Only when the run is already retrying, and at every resume.
  A request that fails with a retryable error (connection refused, reset
  or timed out, `request_timeout`, 429, any 5xx, a dropped or stalled
  stream) records a `retry`, waits its backoff (2 s, 8 s, 30 s, or
  `Retry-After` up to 60 s), then **re-resolves, just before the retry's
  request is sent**, so a fix made during the wait is used at once. `ws
  runner run --resume` (after a crash, a daemon restart or a reboot)
  re-resolves before its first request. A run whose requests succeed
  never re-reads the configuration: an edit reaches it at its next retry
  or resume.
- **Who reads the configuration.** The runner itself, with the same parser
  and validation as every `ws` command, from `WS_CONFIG_DIR/config.toml`.
  The daemon doesn't watch the file or push anything to runners: a runner
  re-resolves while the daemon is down or restarting, and there's no
  daemon-to-runner channel to keep in step. `ws config set` and
  Settings › Models replace `config.toml` with a temp file and a rename,
  so a runner reads the old file or the new one, never half of one.
- **Tokens follow the endpoint.** Each request carries the token of the
  endpoint it goes to: `WS_ENDPOINT_TOKEN_<NAME>` for the new endpoint's
  name, looked up in `secrets.env` as it is now (read as the daemon reads
  it: a mode looser than 0600 ignores the file, with a warning), then in
  the runner's own environment. A token added to `secrets.env` for a new
  endpoint is used without a restart. It is added to the redactor and
  never to tools' environment ([0066](0066-runner-security-boundaries.md)).
  The rest of the environment (layers 1 and 2 of
  [0068](0068-ws-daemon.md)) is rebuilt only at resume.
- **The check before switching.** When the new resolution differs in
  endpoint name, `url`, `api` or model id, the runner runs this ADR's
  preflight against it (`Models`, the id listed, `Probe` per the
  endpoint's `probe`, 10 s per request) with the new endpoint's token:
  - unreachable: this attempt failed (one retry spent); the run stays on
    its current endpoint and re-resolves again before the next retry;
  - the id isn't served (`model_not_served`), or the probe says the model
    has no tools (`model_without_tools`): the switch is **rejected**. A
    run never moves to a model without tools. With `probe = "none"` there
    is no way to tell; the switch happens, and a 400 "does not support
    tools" then ends the run `model_no_tools`, naming the endpoint;
  - a different model id that is served with tools is accepted: the alias
    is the user's word for what to run, and one model has different ids on
    different servers (`qwen/qwen3.5-9b`, `qwen3.5:9b`). Both ids are
    recorded;
  - the new window is `context_tokens`, lowered to the probe's as at
    spawn. The loop recomputes its compaction limits from it; when the
    system prompt, task and last turn don't fit it even after compaction,
    the switch is rejected (`context_too_small`).
  A change to `context_tokens`, `max_output`, `temperature`, `reasoning`
  or a timeout alone needs no probe and is applied (the fit check still
  applies to a smaller window). A rejected switch keeps the current
  endpoint and records `endpoint_rejected`; the retries go on there.
- **A configuration that can't be used** (it doesn't parse or validate,
  the alias or the `--endpoint` name is gone, the endpoint's `api` isn't
  implemented) keeps the current endpoint, records a `config_warning`
  event and a log warning, and the retry goes on.
- **One retry budget across endpoints.** The 3 retries belong to the
  request, whichever endpoints they go to; a switch doesn't reset them.
  When the third retry fails, the exit reason is `endpoint_down` if that
  last attempt went to the endpoint the live configuration resolves to
  (so the current resolution failed too), and `endpoint_rejected` if the
  live configuration points at an endpoint the check refused (the
  rejection's code is in the exit message): a config fix is needed, not a
  respawn.
- **Records.** An `endpoint_changed` transcript event (old and new
  endpoint names and model ids, the window, the reason; no URL, token or
  header), `[[endpoint_history]]` in `run.toml`, the current endpoint in
  `status.json` and, additively, `endpoint_changes` in `ws list`,
  `children` and `wait`, usage per endpoint in `usage.json`, and
  telemetry attributes without content
  ([0065](0065-runner-agent-loop-tools-and-budgets.md),
  [reference.md](../design/ollama-agent/reference.md#stateidrunnertranscriptjsonl)).

### Why this option

1. **Short spawns.** One `[runner.roles.implementer] runner = "ws"` moves
   every implementer. Aliases keep `--model qwen9b` short and stable when
   the id behind it changes.
2. **Fail fast.** Two to four small requests turn the commonest failures
   into distinct codes before anything is created. Preflight runs in the
   daemon, so it sees the same environment and token as the runner will.
3. **Secrets** live in the environment or `secrets.env`, never in
   `config.toml`.
4. **Editable.** Every key is a named table's plain value; model ids are
   values, never keys.
5. **Provider-neutral.** `api` picks the protocol, `probe` the optional
   metadata source; nothing is named after Ollama except the probe that
   only Ollama serves.
6. **Honest context**: `context_tokens` lives on the model, checked against
   the probe when there is one.
7. **Endpoints can change mid-run**: a retrying run picks up a repointed
   or failed-over endpoint (another URL, another endpoint for the alias,
   llm-router in front) from the live configuration, at the moment it
   needs one, with the spawn's checks, and without the daemon.

Where it's weak: `context_tokens` is the user's claim when there's no
probe, so a wrong value means silent truncation (the transcript records
`prompt_tokens`, so it shows up). `--model` means a Claude alias for one
runner and a `ws` alias for the other. Preflight adds about a second, plus
a daemon auto-start on first use. A healthy run ignores config edits until
it retries or resumes. A token removed from `secrets.env` mid-run is still
found in the runner's environment until it resumes. With `probe = "none"`,
a switch to a model without tools is caught only by its first request.

## Alternatives

### Keep everything under `[ollama]` (the first draft)

**Pros**

- One table to find.

**Cons**

- It names a provider in every key and can't describe the Anthropic API or
  a router without reading as Ollama. The review asked for room for other
  APIs.

### Endpoint-only config with the model id in `--model`

**Pros**

- No aliases.

**Cons**

- Context size is per model, not per endpoint; ids with `:` would have to
  be keys for per-model settings.

### Encode the runner in the model (`--model ws:qwen9b`)

**Pros**

- No new flag.

**Cons**

- Ambiguous with model ids, and an orchestrator overriding only the model
  would silently switch runners.

### Detect tool support by sending a test request

**Pros**

- Works without a probe.

**Cons**

- A model load (seconds to minutes) per spawn.

### Pin a run to the endpoint resolved at spawn (the second draft)

**Pros**

- One endpoint per run: simple records, nothing to re-check.

**Cons**

- A repointed alias or a failed-over endpoint reaches no running agent;
  each one burns its retries and fails `endpoint_down`, and the
  orchestrator respawns it from scratch. The owner rejected it:
  "endpoints can change mid-run".

### Fall back to Claude mid-run

When retries are used up, hand the run to a Claude child.

**Pros**

- An orchestration never stalls on an endpoint.

**Cons**

- Spends Claude usage the user didn't choose, mid-task, with a
  conversation Claude didn't start; the orchestrator can already respawn
  on Claude when it sees `endpoint_down`, deliberately. Rejected by the
  owner.

### The daemon watches `config.toml` and pushes changes to runners

**Pros**

- One reader of the configuration; runners get changes at once.

**Cons**

- A daemon-to-runner channel that doesn't exist today, and a runner that
  can't follow a change while the daemon is down or restarting, which is
  when endpoints tend to change. Reading one small file on a retry costs
  nothing.

### Re-resolve before every request

**Pros**

- An edit reaches a healthy run at its next step.

**Cons**

- Moves runs that work: a typo in `config.toml` or a new default changes
  every running agent at once, with a probe per change. Re-resolving only
  when retrying limits changes to runs that need a new endpoint.

### Fall back to Claude by default

**Pros**

- An orchestration never stalls.

**Cons**

- Silently spends Claude usage the user chose to avoid.

## Consequences

- `internal/config` gains `Runner`, `Endpoint`, `Model`, `RunnerRole` and
  `Daemon`; `ws config` gains their keys (reference.md lists them).
- New error codes: `endpoint_unknown` (3), `endpoint_unreachable` (1),
  `model_not_served` (3), `model_without_tools` (1). The `usage` cases gain
  "`--runner` value" and "an argument other than `--model` after the prompt
  for a `ws` child".
- `internal/config` gains `Reresolve` and `ReadFile`; `run.toml` gains
  `endpoint_alias`, `endpoint_flag` and `[[endpoint_history]]`; the
  transcript gains `endpoint_changed`, `endpoint_rejected` and
  `config_warning`; the exit reasons gain `endpoint_rejected`.
- How we'll know: e2e scenarios against the fake for each preflight failure
  and for re-resolution: an endpoint goes down and the alias is repointed
  to a second fake, and the run finishes there; a repoint to a model
  without tools is rejected and the run ends `endpoint_rejected`; an
  invalid `config.toml` mid-run keeps the endpoint
  ([0067](0067-runner-tests-use-vendored-fakes.md)).
- Unverified: how llm-router's own failover and a runner's
  re-resolution interact when both are in play (each retries; the
  runner's budget bounds the total).

## References

[^ollama-openai-ctx]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The OpenAI API does not have a way of setting the context size for a model."
[^ollama-openai]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The client requires an API key value, but Ollama ignores it."
[^ollama-show]: Ollama, "Show model details." <https://docs.ollama.com/api-reference/show-model-details> (accessed 2026-11-13). `capabilities`: "List of supported features".
[^ollama-src-tools]: ollama/ollama `server/routes.go` @a5864fc, <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/routes.go> (accessed 2026-11-13). `errCapabilityTools`, formatted as `fmt.Errorf("%s %w", m.Name, err)`, mapped to `http.StatusBadRequest`.
[^router-errors]: llm-router README, "Errors" @9789c32 (read 2026-11-13). "Backend returned a `4xx` | Passed through unchanged".
[^ollama-src-truncate]: ollama/ollama `server/prompt.go` @a5864fc, <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/prompt.go> (accessed 2026-11-13). "chatPrompt truncates any messages that exceed the context window of the model".
[^qwen-ids]: Qwen, "Qwen3.5-9B" model card, <https://huggingface.co/Qwen/Qwen3.5-9B>; LM Studio, <https://lmstudio.ai/models/qwen/qwen3.5-9b>; Ollama library, <https://ollama.com/library/qwen3.5/tags> (all accessed 2026-11-14). The card: "Qwen3.5 models operate in thinking mode by default".
[^lmstudio-effort]: LM Studio changelog v0.4.8, <https://lmstudio.ai/changelog/lmstudio-v0.4.8> (search-result summary, accessed 2026-11-14; page not opened): `reasoning_effort` on the OpenAI-compatible API.
[^lmstudio-1990]: lmstudio-ai/lmstudio-bug-tracker #1990, <https://github.com/lmstudio-ai/lmstudio-bug-tracker/issues/1990> (accessed 2026-11-14, via a research subagent; open): with qwen3.5-9b, `enableThinking:false` ignored on `/v1`, reasoning returned as `reasoning_content`, and a low `max_tokens` leaving `content` empty.
[^config]: [docs/config.md](../config.md), "Changing it with `ws config`", and `internal/config/edit.go` @98697f3 (types `string`, `strings`, `duration`, `bool`, `hosts`).
