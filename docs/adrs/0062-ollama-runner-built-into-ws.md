# 0062. The Ollama agent is a runner built into `ws`, forked from llm-router's agent

- Status: Proposed
- Date: 2026-11-13

## Context

### Problem

Some `ws machine` children, such as implementers, reviewers and verifiers,
should be able to run on on-prem models served by Ollama, either directly
or through llm-router, instead of on Claude. Where does the agent that talks
to Ollama live, what is it, and what code does it reuse?

### What a solution needs

1. It works against Ollama directly **and** through llm-router. llm-router
   serves only the OpenAI-compatible API[^router-scope].
2. Every automated test runs against fake-ollama, never a real model
   ([0067](0067-ollama-runner-tests-use-fake-ollama.md)).
3. It works with small models, whose context windows and tool-use skills
   are limited.
4. It installs and upgrades with `ws` and keeps `ws`'s dependency
   boundaries. llm-router is a separate repo whose `internal/` packages
   can't be imported.
5. It adds as little as possible: the session plumbing of
   [0063](0063-ollama-children-are-ws-supervised-sessions.md) is new
   either way.

### Relevant facts

- llm-router's `internal/agent` is a 310-line tool-using chat loop on
  `openai-go/v3`, with streaming, tool-call accumulation, reasoning fields,
  and a step limit. It is tested against fake-ollama and a real
  Ollama[^router-agent]. Go forbids importing another module's `internal/`
  packages[^go-internal].
- llm-router's `go.mod` declares the module `github.com/bobcob7/llm-router`,
  but the repo is hosted at `git.bobcob7.com/forgejo_admin/llm-router`, and
  GitHub doesn't resolve `bobcob7/llm-router`[^measured-remote]. So
  `go get` of its module path fails without a `replace` directive.
- Ollama serves an OpenAI-compatible `/v1/chat/completions` with tool
  calling and streaming, but not `tool_choice`. It ignores the API
  key[^ollama-openai].
- Ollama also serves a subset of the Anthropic Messages API at
  `/v1/messages`, and documents Claude Code as a client through
  `ANTHROPIC_BASE_URL`[^ollama-anthropic]. llm-router doesn't serve that
  API[^router-scope], and fake-ollama doesn't either[^fake-endpoints].
- Ollama's docs say "agents, and coding tools should be set to at least
  64000 tokens" of context. The default is 4k below 24 GiB of
  VRAM[^ollama-ctx].
- ADR [0004](0004-single-binary-with-subcommands.md) makes `ws` one binary
  with subcommands. ADR [0037](0037-review-engine-forked-from-loam-refinery.md)
  set the precedent of forking another repo's code into `ws` and
  specializing it, rather than depending on that repo.

## Decision

We will build the Ollama agent into `ws` as a **runner**: a hidden command,
`ws runner run <workspace-id>`, that `ws` starts as a detached process for
an Ollama-backed child. It is built from three new packages:

- **`internal/ollama`**: the HTTP client. Chat completions go through the
  OpenAI-compatible `/v1/chat/completions`, streamed, so the same code works
  against Ollama and llm-router. Preflight checks use `/v1/models`, plus
  Ollama's native `/api/version` and `/api/show` when an endpoint is
  declared as Ollama (`api = "ollama"`).
- **`internal/agent`**: the loop, **forked** from llm-router's
  `internal/agent` at commit `9789c32` and specialized for coding tasks
  ([0065](0065-ollama-agent-loop-tools-and-budgets.md)). It keeps llm-router's
  `chatCompleter` seam, stream accumulator and reasoning-field handling.
  It adds a persistent transcript, budgets, context compaction, and
  recovery from malformed tool calls.
- **`internal/agent/tools`**: the coding tools.

`ws` gains one dependency, `github.com/openai/openai-go/v3`, pinned to the
version llm-router uses (`v3.71.1`). The fork's header comment names the
source commit. Changes worth sharing go back to llm-router by hand. Neither
repo imports the other.

### Why this option

1. **Both endpoints.** The OpenAI-compatible API is the one thing direct
   Ollama and llm-router both serve.
2. **Testable.** fake-ollama speaks exactly that API, and llm-router's
   agent already passes its scenarios.
3. **Small models.** `ws` writes the system prompt and the tool set, so a
   run spends a few thousand tokens on them, not a general-purpose agent's
   instructions ([0065](0065-ollama-agent-loop-tools-and-budgets.md)).
4. **Install.** The runner is the `ws` binary, so there's nothing extra to
   install, and the runner can never be a different version from `ws`.
5. **Least new code.** The loop is a known-good 310 lines, not a new
   design.

Where it's weak: the fork drifts from llm-router's agent, and fixes have to
be copied across by hand. `openai-go` is a large dependency for one
command. The runner's tools are far fewer than Claude Code's, so jobs that
need web search or vision stay on Claude.

## Alternatives

### Claude Code pointed at Ollama's Anthropic API

Start the child with `claude --bg` as today, with `ANTHROPIC_BASE_URL`
set to the Ollama server and a local model name.

**Pros**

- No new session machinery: `claude agents`, `logs`, `stop`, `respawn` and
  `ws restore` all work unchanged.
- Claude Code's full tool set and permission modes.

**Cons**

- It doesn't work through llm-router, which serves only the OpenAI
  API[^router-scope], and it can't be tested against fake-ollama, which
  has no `/v1/messages`[^fake-endpoints]. That fails criteria 1 and 2.
- Background sessions run under Claude Code's background service, which
  has its own environment. A per-child base URL would need `ws` to inject
  settings, and `ws` refuses `--settings`
  ([0024](0024-claude-code-client.md)). Unmeasured.
- Claude Code's system prompt and tool schemas are written for frontier
  models. Their size against a 4k–32k default window[^ollama-ctx] is
  unmeasured, but it is not small.
- Prompt caching and token counting aren't supported by Ollama's Anthropic
  API[^ollama-anthropic].

Worth a spike later for users with large GPUs and no router; see the open
questions in the design.

### A separate binary in llm-router (`cmd/ws-agent`)

`ws` runs an external binary, as it runs `claude`.

**Pros**

- It reuses `internal/agent` directly, so nothing is forked.

**Cons**

- It's a second install that has to stay version-compatible with `ws`'s
  session records, through a new `*_missing` error path.
- It puts coding tools and `ws` concepts (roles, results) into a router
  repo.

### Export llm-router's agent as a public package and import it

Move `internal/agent` to `pkg/agent` and `require` llm-router.

**Pros**

- One copy of the code.

**Cons**

- The module path doesn't resolve[^measured-remote], so `ws` would need a
  `replace`, which breaks `go install` for its users.
- It pulls llm-router's OpenTelemetry dependencies into `ws`.
- It turns llm-router's test helper into a public API with a compatibility
  promise.

### Write the client from scratch on Ollama's native `/api/chat`

**Pros**

- Per-request `num_ctx`, `think`, and arguments as objects[^ollama-chat].
- No `openai-go`.

**Cons**

- It doesn't work through llm-router (criterion 1).
- fake-ollama doesn't serve `/api/chat` (criterion 2).
- New code where proven code exists.

## Consequences

- `ws` gets a hidden `runner` command and three packages. `make lint` gains
  a rule that `/v1/chat/completions` is requested only from
  `internal/ollama`, like the existing rules for `git`, `claude` and `gh`.
- The fork's file header records `forked from llm-router internal/agent
  @9789c32`. A reviewer can diff the two.
- Context size can't be set per request through the OpenAI
  API[^ollama-openai-ctx]. It's a property of the endpoint, configured in
  `config.toml` ([0064](0064-choosing-the-runner-endpoint-and-model.md)).
- Unverified: how well `openai-go`'s accumulator handles Ollama sending a
  whole tool call in one chunk. llm-router's e2e tests pass against real
  Ollama[^router-agent], and the runner's tests repeat that against
  fake-ollama.

## References

[^router-scope]: llm-router README, "MVP scope" (repo `git.bobcob7.com/forgejo_admin/llm-router` @9789c32, read 2026-11-13). "One API: OpenAI-compatible `POST /v1/chat/completions` … and `GET /v1/models`." Out of scope: "Provider types other than Ollama, and Ollama's native API".
[^router-agent]: llm-router `internal/agent/agent.go` and `docs/adrs/0009-llm-probe-test-agent.md` @9789c32 (read 2026-11-13). "Package agent implements a minimal tool-using chat agent on top of the OpenAI chat completions API."
[^go-internal]: The Go Authors, "Go 1.4 Release Notes: Internal packages." <https://go.dev/doc/go1.4#internalpackages> (accessed 2026-11-13). "An import of a path containing the element “internal” is disallowed if the importing code is outside the tree rooted at the parent of the “internal” directory."
[^measured-remote]: Measured 2026-11-13 in the ws-online pod (linux/amd64, git 2.x, gh): `git ls-remote https://git.bobcob7.com/forgejo_admin/llm-router.git HEAD` with no credentials returned `9789c328…`, and the Forgejo API returned 200. `gh repo view bobcob7/llm-router` returned "Could not resolve to a Repository". Each was run once.
[^ollama-openai]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). It lists "Tool calling" and "`stream_options` (supports `include_usage`)", and lists `tool_choice` among the fields not supported. "The client requires an API key value, but Ollama ignores it."
[^ollama-openai-ctx]: Ollama, "OpenAI compatibility." <https://docs.ollama.com/api/openai-compatibility> (accessed 2026-11-13). "The OpenAI API does not have a way of setting the context size for a model. If you need to change the context size, create a `Modelfile` …"
[^ollama-anthropic]: Ollama, "Anthropic compatibility." <https://docs.ollama.com/api/anthropic-compatibility> (accessed 2026-11-13). The page says it "supports a subset of the Anthropic Messages API", shows `export ANTHROPIC_BASE_URL=http://localhost:11434`, and lists token counting, `tool_choice` and prompt caching among the features "not currently supported".
[^fake-endpoints]: llm-router `internal/fakeollama/api.go` @9789c32 (read 2026-11-13). Its handler registers only `GET /v1/models` and `POST /v1/chat/completions`.
[^ollama-ctx]: Ollama, "Context length." <https://docs.ollama.com/context-length> (accessed 2026-11-13). "Ollama defaults to the following context lengths based on VRAM: < 24 GiB VRAM: 4k context; 24-48 GiB: 32k; >= 48 GiB: 256k", and "agents, and coding tools should be set to at least 64000 tokens".
[^ollama-chat]: Ollama, "Chat." <https://docs.ollama.com/api/chat> (accessed 2026-11-13). Its request fields include `tools` ("Optional list of function tools the model may call during the chat") and `think`.
