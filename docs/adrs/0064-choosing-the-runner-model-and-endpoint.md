# 0064. Choosing the runner and model: `--runner`, `[models]`, `[endpoints]` and `[runner.roles]` in `config.toml`, and a fail-fast preflight

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (provider-neutral
  config, secrets in `secrets.env`, preflight in the daemon)

## Context

### Problem

An orchestrator decides per child whether it runs on Claude Code or on
`ws`'s runner. The user decides which models exist, where they're served,
over which API, and which model each role gets. How does a spawn pick its
runner and model? What is configured where? What happens when the endpoint
is down or the model can't call tools?

### What a solution needs

1. **Spawns stay short.** The handbook's spawn lines work unchanged, and
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

### Relevant facts

- Ollama reports `capabilities` (`completion`, `tools`, `thinking`, …) and
  `model_info.<family>.context_length` from `POST /api/show`[^ollama-show],
  and `GET /api/version`. A request with tools to a model without them gets
  400 `{"error":"<model> does not support tools"}`[^ollama-src-tools];
  llm-router passes a backend's 4xx through[^router-errors].
- Ollama truncates the oldest history silently past the context
  length[^ollama-src-truncate].
- Model ids contain `:`, `.` and `/` (`qwen3-coder:30b`,
  `claude-sonnet-5-5`, `org/model`), so they can't be bare TOML keys.
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
   child accepts, so `-- "" --model coder` works as it does for Claude);
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
model = "coder"            # default model alias for ws children
fallback = "none"          # none | claude: what spawn does when preflight fails

[endpoints.gpu]
url = "http://ollama-a.inference.svc:11434"
api = "openai"             # the wire protocol; the default. Reserved: ollama, openai-responses, anthropic
probe = "auto"             # auto | ollama | none: how preflight learns a model's capabilities
request_timeout = "10m"    # one completion, including a cold model load
idle_timeout = "90s"       # longest gap between streamed chunks

[endpoints.router]
url = "http://llm-router.inference.svc:8080"

[models.coder]
endpoint = "gpu"
id = "qwen3-coder:30b"     # the id the endpoint knows
context_tokens = 65536     # the server's context length for it; required

[models.small]
endpoint = "router"
id = "gpt-oss:20b"
context_tokens = 32768

[runner.roles.verifier]
runner = "ws"
model = "coder"
max_steps = 40
timeout = "45m"
```

Endpoint, model and role names are lowercase letters, digits and hyphens.
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
once a runner has started.

### Why this option

1. **Short spawns.** One `[runner.roles.implementer] runner = "ws"` moves
   every implementer. Aliases keep `--model coder` short and stable when
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

Where it's weak: `context_tokens` is the user's claim when there's no
probe, so a wrong value means silent truncation (the transcript records
`prompt_tokens`, so it shows up). `--model` means a Claude alias for one
runner and a `ws` alias for the other. Preflight adds about a second, plus
a daemon auto-start on first use.

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

### Encode the runner in the model (`--model ws:coder`)

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
- How we'll know: e2e scenarios against the fake for each preflight failure
  ([0067](0067-runner-tests-use-vendored-fakes.md)).

## References

[^ollama-openai-ctx]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The OpenAI API does not have a way of setting the context size for a model."
[^ollama-openai]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The client requires an API key value, but Ollama ignores it."
[^ollama-show]: Ollama, "Show model details." <https://docs.ollama.com/api-reference/show-model-details> (accessed 2026-11-13). `capabilities`: "List of supported features".
[^ollama-src-tools]: ollama/ollama `server/routes.go` @a5864fc, <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/routes.go> (accessed 2026-11-13). `errCapabilityTools`, formatted as `fmt.Errorf("%s %w", m.Name, err)`, mapped to `http.StatusBadRequest`.
[^router-errors]: llm-router README, "Errors" @9789c32 (read 2026-11-13). "Backend returned a `4xx` | Passed through unchanged".
[^ollama-src-truncate]: ollama/ollama `server/prompt.go` @a5864fc, <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/prompt.go> (accessed 2026-11-13). "chatPrompt truncates any messages that exceed the context window of the model".
[^config]: [docs/config.md](../config.md), "Changing it with `ws config`", and `internal/config/edit.go` @98697f3 (types `string`, `strings`, `duration`, `bool`, `hosts`).
