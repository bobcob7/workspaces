# 0062. A provider-neutral agent runner is built into `ws`; the first provider speaks the OpenAI-compatible chat API

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (provider seam,
  code copied into this repo)

## Context

### Problem

Some `ws machine` children, such as implementers, reviewers and verifiers,
should be able to run on models other than Claude Code's: first on-prem
models served by Ollama, directly or through llm-router, and later other
APIs, including the OpenAI API and the Anthropic API itself. Where does the
agent that talks to those models live, what is it, and where is the line
between the agent and the wire protocol, so that a second protocol is an
addition rather than a rewrite?

### What a solution needs

1. It works against Ollama directly **and** through llm-router now.
   llm-router serves only the OpenAI-compatible API[^router-scope].
2. **Room for other APIs**: Ollama's native API, the OpenAI Responses API
   and the Anthropic Messages API must fit behind the same agent later,
   without changing the loop, the transcript or the session.
3. Every automated test runs against fakes kept in this repo, never a real
   model ([0067](0067-runner-tests-use-vendored-fakes.md)).
4. It works with small models, whose context windows and tool-use skills
   are limited.
5. It installs and upgrades with `ws` and keeps `ws`'s dependency
   boundaries. llm-router is an internal tool in a separate repo, whose
   `internal/` packages can't be imported and which `ws` must not clone.

### Relevant facts

- llm-router's `internal/agent` is a 310-line tool-using chat loop on
  `openai-go/v3`, with streaming, tool-call accumulation, reasoning fields,
  and a step limit. It is tested against fake-ollama and a real
  Ollama[^router-agent]. Go forbids importing another module's `internal/`
  packages[^go-internal].
- llm-router's module path doesn't resolve on GitHub; the repo is on a
  private Forgejo host[^measured-remote].
- Ollama serves an OpenAI-compatible `/v1/chat/completions` with tool
  calling and streaming, but not `tool_choice`. It ignores the API
  key[^ollama-openai]. Its native `/api/chat` takes tool arguments as JSON
  objects, has no tool-call ids, and returns reasoning as
  `thinking`[^ollama-chat].
- The OpenAI Responses API returns tool calls as `function_call` output
  items with a `call_id` and string `arguments`, and reasoning as separate
  `reasoning` items that a stateless client passes back
  encrypted[^openai-responses].
- The Anthropic Messages API takes the system prompt as a top-level
  `system` field, returns tool calls as `tool_use` content blocks with an
  `id` and an object `input`, takes results as `tool_result` blocks in a
  user message, and returns `thinking` blocks with a `signature` that must
  be passed back unchanged with the tool results of that turn[^anthropic].
- The same chat-completions shape is served by LM Studio and
  vLLM[^lmstudio][^vllm].
- Ollama's docs say "agents, and coding tools should be set to at least
  64000 tokens" of context. The default is 4k below 24 GiB of
  VRAM[^ollama-ctx].
- ADR [0004](0004-single-binary-with-subcommands.md) makes `ws` one binary
  with subcommands. ADR [0037](0037-review-engine-forked-from-loam-refinery.md)
  set the precedent of copying another repo's code into `ws` and
  maintaining it here.

## Decision

We will build a **runner** into `ws`: a hidden command, `ws runner run
<workspace-id>`, that the `ws` daemon ([0068](0068-ws-daemon.md)) starts for
a child whose runner is `ws` (`--runner ws`). It is split at one seam: the
agent loop speaks only `ws`'s own message types, and a **provider**
translates them to one wire protocol.

**The seam.** A new package (`internal/llm`) holds the neutral types and
one interface:

```go
// Provider is one wire protocol to one endpoint.
type Provider interface {
	// Chat sends the conversation and returns the whole answer. Providers
	// stream internally when the API allows it.
	Chat(ctx context.Context, req Request) (Response, error)
	// Models lists the model ids the endpoint serves.
	Models(ctx context.Context) ([]string, error)
	// Probe reports what the endpoint can say about a model: its
	// capabilities and context length. ErrProbeUnsupported when the API
	// has no way to tell.
	Probe(ctx context.Context, model string) (ModelInfo, error)
}
```

`Request` carries the model id, `System` (the system prompt, kept apart
from the messages because the Anthropic API needs it apart), `Messages`,
`Tools` (name, description, JSON schema), the output limit and the
temperature. A `Message` has a role (`user`, `assistant`, `tool`), text,
`ToolCalls` (`ID`, `Name`, `Arguments` as JSON text), `ToolCallID` for a
tool result, `Reasoning` (text, shown in transcripts, never sent back by
providers that don't need it) and `ReasoningOpaque` (bytes a provider
asked to have returned verbatim, tagged with the provider's name).
`Response` has the assistant `Message`, a `StopReason` (`end`,
`tool_calls`, `max_tokens`) and `Usage` (input, output, and cache read and
write tokens when reported). Errors are classified once, as sentinels:
`ErrUnreachable`, `ErrRetryable` (with Retry-After), `ErrStreamDropped`,
`ErrStreamStalled`, `ErrModelMissing`, `ErrNoTools`, `ErrContextExceeded`.
Nothing above the seam imports a provider's SDK.

**What each provider normalizes**, so the loop never sees the difference:

| Shape | OpenAI chat completions (now) | Ollama native `/api/chat` (later) | OpenAI Responses (later) | Anthropic Messages (later) |
| ----- | ----------------------------- | --------------------------------- | ------------------------ | -------------------------- |
| System prompt | first `system` message | first `system` message | `instructions` | top-level `system` |
| Tool call | `tool_calls[].{id, function.name, function.arguments}` (string) | `tool_calls[].function.{name, arguments}` (object, no id): the provider marshals the object and makes ids `call_<step>_<n>` | `function_call` item: `call_id`, `name`, `arguments` | `tool_use` block: `id`, `name`, `input` (object, marshalled) |
| Tool result | `role: "tool"`, `tool_call_id` | `role: "tool"`, `tool_name` | `function_call_output` item, `call_id` | `tool_result` block in a `user` message |
| Reasoning | `reasoning` or `reasoning_content` → `Reasoning`; not sent back | `thinking` → `Reasoning`; not sent back | `reasoning` items → `Reasoning` (summary) and `ReasoningOpaque` (encrypted content), sent back | `thinking` blocks → `Reasoning`, with `signature` in `ReasoningOpaque`, sent back unchanged in the same turn |
| Stop reason | `finish_reason` `stop`/`tool_calls`/`length` | `done_reason` | `status`, output kinds | `stop_reason` `end_turn`/`tool_use`/`max_tokens` |
| Usage | `prompt_tokens`, `completion_tokens` | `prompt_eval_count`, `eval_count` | `input_tokens`, `output_tokens` | `input_tokens`, `output_tokens`, `cache_*_input_tokens` |
| Context size | the server's; not settable per request | `options.num_ctx` per request | the model's | the model's |

`ReasoningOpaque` from one provider is dropped, with a transcript note,
if a resumed run is ever sent to a different provider; a run keeps one
provider for its whole life, so this happens only if config changes
between a crash and a resume.

**Providers in this design.** `api` on an endpoint
([0064](0064-choosing-the-runner-model-and-endpoint.md)) names the
provider:

- **`openai`** (the default, the only one implemented now): OpenAI-compatible
  chat completions, streamed with `include_usage`, on
  `github.com/openai/openai-go/v3`. It covers Ollama's `/v1`, llm-router,
  LM Studio and vLLM. `Models` is `GET /v1/models`. `Probe` uses Ollama's
  native `/api/show` when the endpoint's `probe` setting finds an Ollama
  server, and is `ErrProbeUnsupported` otherwise.
- **`ollama`**, **`openai-responses`**, **`anthropic`**: reserved names.
  Config that names one is `config_invalid`, "api = "anthropic" isn't
  implemented yet". Each is a new package implementing `Provider`, one
  table row above, its own fixture tests, and a line in the provider
  registry. The loop, the tools, the transcript, the session, the daemon
  and the config schema don't change.

We read the review's "openapi" as the OpenAI API (chat completions now,
Responses later). If it meant tools described by OpenAPI documents, that's
a different feature, listed in the design's open questions.

**The loop** (`internal/agent`) is copied from llm-router's
`internal/agent` at commit `9789c32` and from then on maintained here
([0065](0065-runner-agent-loop-tools-and-budgets.md)). It keeps llm-router's
stream accumulator and reasoning handling, moved into the `openai`
provider, and adds a transcript, budgets, compaction, recovery from
malformed tool calls, and MCP tools ([0069](0069-runner-mcp-tools-and-claude-code-parity.md)).
Its header comment records the origin. There's no sync obligation with
llm-router; nothing in `ws` clones, imports or builds llm-router.

### Why this option

1. **Both endpoints now.** The OpenAI-compatible API is the one thing
   direct Ollama and llm-router both serve.
2. **Room for other APIs.** The table shows every shape difference ends at
   the provider: tool-call ids, argument encoding, reasoning that must be
   returned, the system prompt's place, usage names. A new API is one
   package.
3. **Testable** with the fake-ollama copy kept in this repo
   ([0067](0067-runner-tests-use-vendored-fakes.md)).
4. **Small models.** `ws` writes the system prompt and the tool set, so a
   run spends a few thousand tokens on them.
5. **Install.** The runner is the `ws` binary.

Where it's weak: the neutral types are designed from documentation for
three of the four protocols, and only one is built; a reserved provider may
still need a field we didn't foresee (the risk is mostly in Responses'
stateful mode, which this design doesn't use). `openai-go` is a large
dependency for one provider. The runner's tools are far fewer than Claude
Code's ([0069](0069-runner-mcp-tools-and-claude-code-parity.md) says which
gaps close and which don't).

## Alternatives

### Claude Code pointed at another API (`ANTHROPIC_BASE_URL`)

Start the child with `claude --bg` as today, with `ANTHROPIC_BASE_URL` set
to Ollama's Anthropic-compatible endpoint and a local model name.

**Pros**

- No new session machinery, and Claude Code's full tool set.

**Cons**

- It doesn't work through llm-router, which serves only the OpenAI
  API[^router-scope], and the fake has no `/v1/messages`. That fails
  criteria 1 and 3.
- Background sessions share Claude Code's service and its environment; a
  per-child base URL would need `ws` to inject settings, which it refuses
  ([0024](0024-claude-code-client.md)).
- Claude Code's system prompt and tool schemas are written for frontier
  models; their size against a 4k–32k default window is unmeasured but
  not small.

Still worth a spike for users with large GPUs and no router.

### Hard-code the OpenAI chat API in the loop (the first draft)

**Pros**

- Less code now.

**Cons**

- Tool-call ids, reasoning and the system prompt are protocol details; the
  Anthropic API's signed thinking blocks would leak into the loop and the
  transcript. Fails criterion 2.

### A separate binary in llm-router, or llm-router's agent as a public package

**Pros**

- One copy of the code.

**Cons**

- llm-router is internal; `ws` would have to clone or `replace` it, and
  the module path doesn't resolve[^measured-remote]. It pulls llm-router's
  OpenTelemetry dependencies, and turns its test helpers into a public API.

### One generic SDK for every provider (an "any LLM" library)

**Pros**

- Other protocols for free.

**Cons**

- Another large dependency whose own types leak upward; the four shapes
  above are small enough to own.

## Consequences

- `ws` gets a hidden `runner` command, and the packages `internal/llm`,
  `internal/llm/openai`, `internal/agent` and `internal/agent/tools`. `make
  lint` gains a rule that `chat/completions` is requested only from
  `internal/llm/openai`.
- `go.mod` gains `github.com/openai/openai-go/v3` (`v3.71.1`, the version
  llm-router uses), required only by `internal/llm/openai`.
- Context size can't be set per request through the OpenAI API. It's a
  property of the model on its endpoint, configured in `config.toml`
  ([0064](0064-choosing-the-runner-model-and-endpoint.md)).
- Adding a provider later is: a package, fixtures, a registry line, and
  removing its name from the reserved list. The ADR for it records only
  what the table above got wrong.
- Unverified: how `openai-go`'s accumulator handles Ollama sending a whole
  tool call in one chunk against a real Ollama. The fake reproduces it.

## References

[^router-scope]: llm-router README, "MVP scope" (@9789c32, read 2026-11-13). "One API: OpenAI-compatible `POST /v1/chat/completions` … and `GET /v1/models`."
[^router-agent]: llm-router `internal/agent/agent.go` and `docs/adrs/0009-llm-probe-test-agent.md` @9789c32 (read 2026-11-13). "Package agent implements a minimal tool-using chat agent on top of the OpenAI chat completions API."
[^go-internal]: The Go Authors, "Go 1.4 Release Notes: Internal packages." <https://go.dev/doc/go1.4#internalpackages> (accessed 2026-11-13).
[^measured-remote]: Measured 2026-11-13 in the ws-online pod: `git ls-remote` of the Forgejo repo answered; `gh repo view bobcob7/llm-router` returned "Could not resolve to a Repository". Each was run once.
[^ollama-openai]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). It lists "Tool calling" and `stream_options` `include_usage`, and lists `tool_choice` among unsupported fields. "The client requires an API key value, but Ollama ignores it."
[^ollama-chat]: Ollama, "Chat." <https://docs.ollama.com/api/chat> (accessed 2026-11-13). Request fields include `tools` and `think`; responses carry `message.thinking` and `message.tool_calls[].function.arguments` as an object.
[^openai-responses]: OpenAI, "Function calling" and "Reasoning models" guides for the Responses API, <https://platform.openai.com/docs/guides/function-calling>, <https://platform.openai.com/docs/guides/reasoning> (not re-read for this revision; shapes as documented in 2025). Unverified against the current API; the provider task for it must re-check.
[^anthropic]: Anthropic, "Tool use" and "Extended thinking," <https://docs.anthropic.com/en/docs/build-with-claude/tool-use>, <https://docs.anthropic.com/en/docs/build-with-claude/extended-thinking> (not re-read for this revision; shapes as documented in 2025). Unverified against the current API; the provider task for it must re-check.
[^lmstudio]: LM Studio, "OpenAI Compatibility API." <https://lmstudio.ai/docs/app/api/endpoints/openai> (not re-read for this revision).
[^vllm]: vLLM, "OpenAI-Compatible Server." <https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html> (not re-read for this revision).
[^ollama-ctx]: Ollama, "Context length." <https://docs.ollama.com/context-length> (accessed 2026-11-13). "< 24 GiB VRAM: 4k context; 24-48 GiB: 32k; >= 48 GiB: 256k", and "agents, and coding tools should be set to at least 64000 tokens".
