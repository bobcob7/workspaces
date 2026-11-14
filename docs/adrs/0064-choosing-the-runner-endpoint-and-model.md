# 0064. Choosing the runner, endpoint and model: `--runner`, `[ollama]` in `config.toml`, and fail-fast preflight

- Status: Proposed
- Date: 2026-11-13

## Context

### Problem

An orchestrator decides per child whether it runs on Claude or on an
on-prem model. The user decides where the models are and which one each
role gets. How does a spawn pick its runner, endpoint and model? What is
configured where? And what happens when the endpoint is down or the model
can't call tools?

### What a solution needs

1. **Spawns stay short.** The handbook's spawn lines work unchanged, and
   switching a role to on-prem is a config change, not a change to every
   orchestrator.
2. **Fail fast, never silently.** A bad endpoint or model fails the spawn
   with a clear error code. There's no fallback to Claude unless the user
   configured one, and then the output says so.
3. **No secrets in `config.toml`**, as with forge tokens
   ([0061](0061-hosts-in-config-toml.md)).
4. **Editable with `ws config`**, which edits only some forms of key
   ([0054](0054-settings-all.md)).
5. **Context size is honest.** Over the OpenAI API, the context size is
   the server's, not the request's[^ollama-openai-ctx].

### Relevant facts

- Ollama reports a model's `capabilities` (for example `completion`,
  `tools`, `thinking`) from `POST /api/show`[^ollama-show]. When tools are
  sent to a model without them, Ollama answers 400 with `{"error":"<model>
  does not support tools"}`[^ollama-src-tools]. llm-router passes a
  backend's 4xx through unchanged[^router-errors].
- When a prompt is longer than the context, Ollama truncates the oldest
  history silently: "making sure to always include 1) the latest message
  and 2) system messages"[^ollama-src-truncate]. The default context is
  4k, 32k or 256k depending on VRAM, set by
  `OLLAMA_CONTEXT_LENGTH`[^ollama-ctx].
- `ws config` already edits named tables like `[review.profiles.<name>]`
  and an array of tables, `[[hosts]]`. Dotted and inline forms are
  refused with `config_uneditable`[^config].
- Model names contain `:` and `.` (`qwen3-coder:30b`), so they can't be
  bare TOML keys.
- `ws machine spawn` passes everything after the prompt to `claude`.
  Orchestrators already override the model with `-- "" --model haiku`.

## Decision

**Choosing the runner**, first match wins:

1. `ws machine spawn --runner claude|ollama`;
2. `ollama.roles.<role>.runner` in `config.toml`, for `--role <role>`;
3. `claude`.

**Choosing the endpoint and model** for an `ollama` child, first match
wins for each:

- endpoint: `--endpoint <name>`, then `ollama.roles.<role>.endpoint`, then
  `ollama.endpoint`, and with none of those it's a `config_invalid` error;
- model: `--model <m>` after the prompt (the only `claude`-style argument
  an `ollama` child accepts, so `-- "" --model qwen3-coder:30b` works as it
  does for Claude), then `ollama.roles.<role>.model`, then
  `ollama.endpoints.<name>.model`, and otherwise `config_invalid`. A role
  file's `model` (`haiku`, `sonnet`) is a Claude alias and is ignored for
  `ollama` children.

Any other argument after the prompt is `usage` for an `ollama` child.

**Configuration** (full schema in
[reference.md](../design/ollama-agent/reference.md#configtoml)):

```toml
[ollama]
endpoint = "gpu"          # default endpoint name
fallback = "none"         # none | claude: what spawn does when preflight fails

[ollama.endpoints.gpu]
url = "http://ollama-a.inference.svc:11434"
api = "ollama"            # ollama: preflight with /api/show; openai: llm-router or other OpenAI-compatible, /v1/models only
model = "qwen3-coder:30b" # default model on this endpoint
context_tokens = 65536    # the server's OLLAMA_CONTEXT_LENGTH or the model's num_ctx; required
request_timeout = "10m"   # one completion, including a cold model load
idle_timeout = "90s"      # longest gap between streamed chunks

[ollama.roles.verifier]
runner = "ollama"
endpoint = "gpu"
model = "qwen3-coder:30b"
max_steps = 40
timeout = "45m"
```

Endpoint and role names are lowercase letters, digits and hyphens. A
`token` key is an unknown key: an endpoint's bearer token comes from
`WS_OLLAMA_TOKEN_<NAME>` (the name uppercased, `-` to `_`), sent as
`Authorization: Bearer`. It is never written to disk and is removed from
the runner's tool environment
([0066](0066-ollama-runner-security-boundaries.md)). The desktop app's
Settings › Models edits these keys through `ws config`, and keeps tokens in
the OS keychain.

**Preflight**, run by `ws machine spawn` before the workspace is created,
with a 10 s limit per request:

| Check | Fails with | Exit |
| ----- | ---------- | ---- |
| The endpoint name is in `config.toml` | `endpoint_unknown` | 3 |
| `GET /v1/models` (with `api = "ollama"`, also `GET /api/version`) answers | `ollama_unreachable`, `details.url`, `details.cause` | 1 |
| The model is in `/v1/models` | `ollama_model_missing`, `details.models` | 3 |
| `api = "ollama"`: `/api/show`'s `capabilities` include `tools` | `ollama_no_tools`, `details.capabilities` | 1 |
| `api = "ollama"`: `/api/show`'s context length is at least `context_tokens`; otherwise `context_tokens` is lowered to it, with a warning | none (warning) | |
| `context_tokens` is at least 16384 | `config_invalid` | 1 |

`api = "openai"` endpoints can't report capabilities, so a model without
tools is caught by the first request's 400 `does not support tools`: the
runner stops at once with exit reason `model_no_tools`, recording the
fail verdict.

**Fallback.** With `ollama.fallback = "claude"`, and only for
`ollama_unreachable`, `ollama_model_missing` and `ollama_no_tools`, the
spawn starts a Claude child instead. Its output says `"runner": "claude"`
and carries a warning naming the preflight error. There's never a fallback
once a runner has started: a run that loses its endpoint fails with a
reason ([0065](0065-ollama-agent-loop-tools-and-budgets.md)).

### Why this option

1. **Short spawns.** One `[ollama.roles.implementer] runner = "ollama"`
   line moves every implementer on-prem. `--runner` is there for one-offs.
2. **Fail fast.** Preflight costs two or three small requests, and turns
   the commonest failures into distinct codes before anything is created.
   Fallback is opt-in and visible.
3. **Secrets** stay in the environment or the keychain, as for forges.
4. **Editable.** Named tables are a form `ws config` already edits. Model
   names are values, never keys.
5. **Honest context.** `context_tokens` lives on the endpoint because
   that's where the context size is set. The runner budgets against it
   rather than trusting Ollama's silent truncation.

Where it's weak: `context_tokens` is the user's claim about a server
setting that `ws` can't read over the OpenAI API, so a wrong value means
silent truncation; the transcript records `prompt_tokens` so it shows up.
Preflight adds about a second to each spawn. `--model` means different
things for the two runners.

## Alternatives

### Encode the runner in the model: `--model ollama:qwen3-coder:30b`

**Pros**

- No new flag.

**Cons**

- It's ambiguous with Ollama's own `name:tag` syntax.
- It can't name an endpoint.
- The handbook's habit of overriding only the model would silently switch
  runners.

### A `runner` field in role files

**Pros**

- Self-contained roles.

**Cons**

- Built-in roles would need overrides to switch.
- The choice is per machine (which GPUs exist), not per prompt, so it
  belongs in `config.toml`.

### Detect tools support by sending a test request

**Pros**

- Works for `api = "openai"` too.

**Cons**

- It spends a model load (seconds to minutes on a cold GPU) per spawn.
- `/api/show` answers from metadata.

### Fall back to Claude by default

**Pros**

- An orchestration never stalls.

**Cons**

- It silently spends Claude usage the user chose to avoid. The task asks
  for no silent fallback.

## Consequences

- `internal/config` gains `Ollama`, `OllamaEndpoint` and `OllamaRole`.
  `ws config` gains the `ollama.*` keys; the reference lists them.
- New error codes: `endpoint_unknown` (3), `ollama_unreachable` (1),
  `ollama_model_missing` (3), `ollama_no_tools` (1). The `usage` cases gain
  "`--runner` value" and "an argument other than `--model` after the
  prompt for an `ollama` child".
- How we'll know: e2e scenarios against fake-ollama for each preflight
  failure ([0067](0067-ollama-runner-tests-use-fake-ollama.md)).

## References

[^ollama-openai-ctx]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The OpenAI API does not have a way of setting the context size for a model."
[^ollama-show]: Ollama, "Show model details." <https://docs.ollama.com/api-reference/show-model-details> (accessed 2026-11-13). `capabilities`: "List of supported features", for example "completion", "thinking", "vision".
[^ollama-src-tools]: ollama/ollama `server/images.go` and `server/routes.go` @a5864fc (accessed 2026-11-13), <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/routes.go>. `errCapabilities = errors.New("does not support")`, `errCapabilityTools = errors.New("tools")`, the error formatted as `fmt.Errorf("%s %w", m.Name, err)`, and mapped to `http.StatusBadRequest`.
[^router-errors]: llm-router README, "Errors" @9789c32 (read 2026-11-13). "Backend returned a `4xx` | Passed through unchanged".
[^ollama-src-truncate]: ollama/ollama `server/prompt.go` and `server/routes.go` @a5864fc (accessed 2026-11-13), <https://github.com/ollama/ollama/blob/a5864fcb33969705de3c0ebfedf4b2a16c970bcc/server/prompt.go>. "chatPrompt truncates any messages that exceed the context window of the model, making sure to always include 1) the latest message and 2) system messages"; `truncate := req.Truncate == nil || *req.Truncate`. Issue #14259 reports that truncation "happens silently with no user-visible indication", <https://github.com/ollama/ollama/issues/14259>.
[^ollama-ctx]: Ollama, "Context length." <https://docs.ollama.com/context-length> (accessed 2026-11-13). "Ollama defaults to the following context lengths based on VRAM: < 24 GiB VRAM: 4k context; 24-48 GiB: 32k; >= 48 GiB: 256k". Example: `OLLAMA_CONTEXT_LENGTH=64000 ollama serve`.
[^config]: [docs/config.md](../config.md), "Changing it with `ws config`". "A key written in a form `ws config` doesn't edit, such as a dotted key or an inline table, fails with `config_uneditable`."
