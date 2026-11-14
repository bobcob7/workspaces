# 0067. Runner tests use fake-ollama built from a pinned llm-router commit; a real Ollama is opt-in

- Status: Proposed
- Date: 2026-11-13

## Context

### Problem

Every automated test of the runner must run against fake-ollama, never a
real model. fake-ollama lives in llm-router, whose `internal/` packages
`ws` can't import ([0062](0062-ollama-runner-built-into-ws.md)). How do
`ws`'s tests get it, how is its version pinned, what scenarios run, and
what is fake-ollama missing?

### What a solution needs

1. **Deterministic.** The same scripted replies every run, so there's no
   GPU and no flakiness.
2. **One source of truth** for the fake, so fixes made in llm-router reach
   `ws`.
3. **Pinned**: a `ws` commit always tests against the same fake.
4. **Covers the failures** the runner handles: tool calls that edit files,
   verdicts, endpoint down, malformed calls, mid-stream drops, context
   overflow, a model without tools.
5. **Fast in `make test`**; the process-level tests may be a separate
   target.

### Relevant facts

- fake-ollama is rule-driven (a regexp on the last user message gives a
  reply or a tool call), has fault injection (`down`, `status`, `hang`,
  `slow_first_token`, `drop_after_chunks`, `stall_after_chunks`, each
  optionally scoped to a model and counted), and has a control API on a
  second port with `GET /requests`, `PUT /faults` and `POST /reset`[^fake].
- When the conversation ends with tool results, fake-ollama always answers
  `The tool returned: …`, so it can't script a second tool call. Tool rules
  apply only to offered tools with valid JSON arguments, so it can't send a
  malformed or unknown call. A model without the `tools` capability
  silently ignores tools rather than answering 400. And it serves no
  `/api/*` endpoints and has no context limit[^fake].
- llm-router's e2e harness builds the binaries and runs them as processes,
  waits for their `listening on` log lines, and enables a real-Ollama
  target only when `E2E_OLLAMA_URL` is set[^harness].
- The llm-router repo can be cloned anonymously over HTTPS[^measured].
- `ws` tests already run fakes of `claude` and `gh` through
  `internal/exectest`[^exectest].

## Decision

**Three layers.**

1. **Unit tests** (`go test ./...`, run by `make test` and CI) never open a
   socket to a model. `internal/agent` uses a moq mock of `chatCompleter`,
   as llm-router's agent tests do. `internal/ollama` uses `httptest` servers
   that replay recorded fake-ollama responses, kept under
   `internal/ollama/testdata/`.
2. **Process e2e** (`make e2e-ollama`, build tag `e2e`, package
   `e2e/ollama`) runs the real `ws` binary and a real fake-ollama process,
   with `WS_CONFIG_DIR` in a temp dir, a local bare git repo as the
   upstream, and a fake `claude` from `internal/exectest` for the mixed
   tests.
3. **Real model, opt-in.** When `E2E_OLLAMA_URL` is set, with
   `E2E_OLLAMA_MODEL` (default `qwen3-coder:30b`), `make e2e-ollama` also
   runs the `edit-file` and `review-verdict` scenarios against it. They
   check only that a verdict was recorded and the run ended `done`, never
   the model's words. Nothing in CI sets it.

**Getting the fake.** `make fake-ollama` clones
`https://git.bobcob7.com/forgejo_admin/llm-router.git` at the commit in
`e2e/ollama/FAKE_OLLAMA_REF` (one line: a full SHA) into
`bin/llm-router-src` (reused when it's already at that commit), and builds
`bin/fake-ollama`. `WS_FAKE_OLLAMA=/path/to/fake-ollama` skips the build.
The e2e tests fail, rather than skip, when the binary is missing, and say
to run `make fake-ollama`. Bumping the fake is a one-line change to
`FAKE_OLLAMA_REF` in its own commit. CI gets a separate `e2e-ollama` job.

**Scenarios**, each a fake-ollama config under `e2e/ollama/testdata/`:

| Scenario | Fake config | Asserts |
| -------- | ----------- | ------- |
| `edit-file` | rules by turn: `write_file` → `run` (`git commit`) → `record_result PASS` | file content, commit, `result.toml` PASS, `usage.json`, wait JSON `ollama.exit = "done"` |
| `review-verdict` | `read_file` → `run` (`git diff`) → `record_result CHANGES_REQUESTED` | outcome `fail`, `child_failed`, report text |
| `endpoint-down` | fault `down` before spawn | spawn fails `ollama_unreachable`, exit 1, no workspace left |
| `endpoint-down-mid-run` | `status: 503`, `count: 4` from turn 2 | 3 retries in the transcript, exit `endpoint_down`, FAIL with reason |
| `malformed-tool-call` | `raw_arguments: '{"path": '` once, then valid | recovers; transcript has the error answer. Three times in a row: `malformed_tool_calls` |
| `unknown-tool` | a call to `delete_repo` | error answer, then recovery |
| `mid-stream-drop` | `drop_after_chunks: 2`, `count: 1` | one retry, then `done` |
| `context-overflow` | model `context_length: 4096`, a 40 KiB file read | a `compaction` event; with a 30 KiB task prompt, `context_overflow` |
| `no-tools-model` | model without `tools`, `api = "ollama"` | spawn fails `ollama_no_tools`; with `api = "openai"`, the run exits `model_no_tools` |
| `model-missing` | model not configured | spawn fails `ollama_model_missing`, `details.models` |
| `mixed-wait` | fake `claude` child plus `edit-file` | one `ws machine wait` returns both; the `runner` fields are right |
| `restore` | `hang` fault, kill the runner, clear the fault, `ws restore` | resumes at the same step, then `done` |

**fake-ollama gaps**, fixed by one small llm-router change (task T1 in the
[tasks](../design/ollama-agent/tasks.md)):

1. **Rules on conversation state**: `turn` (the number of assistant
   messages so far) and `match_tool_result` (a regexp on the trailing tool
   results). Such a rule is tried before the `The tool returned:` default.
2. **`raw_arguments`**: sends the text verbatim, even when it isn't valid
   JSON, and **`any_tool: true`**: calls `tool` even when it isn't offered.
3. **`strict_capabilities: true`** (config level): a request with tools to
   a model without `tools` gets 400 `{"error":"<model> does not support
   tools"}`, as Ollama answers.
4. **Native endpoints**: `GET /api/version`, `GET /api/tags`, and `POST
   /api/show` with `capabilities` (`completion`, plus `tools` and
   `thinking` from the model's capabilities) and `model_info` with
   `context_length`.
5. **`context_length`** per model: prompts longer than it (counted as
   words, as usage already is) are truncated from the oldest non-system
   message, and `prompt_tokens` reports the truncated size, like Ollama.

### Why this option

1. **Deterministic**: scripted turns, no GPU.
2. **One source**: the fake is built from llm-router, never copied.
3. **Pinned** by a SHA in the repo.
4. **Covers** every failure the runner handles, once the five gaps are
   filled.
5. **Fast**: `make test` stays free of processes and network; e2e is its
   own target and its own CI job.

Where it's weak: e2e needs network access to the Forgejo host on first
build, and CI on GitHub runners reaching `git.bobcob7.com` hasn't been
tried. The word-count tokenizer means overflow thresholds in tests don't
match a real model's.

## Alternatives

### Vendor fake-ollama's source into `ws`

**Pros**

- No network.
- Builds with `go test`.

**Cons**

- It's a second copy of seven packages (`fakeollama`, `openaiwire`, `sse`,
  `yamlconfig`, `httpserver`, `telemetry` …) that drifts, and gap fixes
  would have to be made twice.

### `go tool` directive on llm-router's `cmd/fake-ollama`

**Pros**

- Pinned by `go.sum`, as the handbook prefers for dev tools.

**Cons**

- The module path `github.com/bobcob7/llm-router` doesn't resolve on
  GitHub[^measured]. A `replace` to the Forgejo URL works locally, but
  pulls llm-router's whole dependency graph, OpenTelemetry included, into
  `ws`'s `go.mod`.

### Only `httptest` stubs, no fake-ollama

**Pros**

- The simplest.

**Cons**

- It tests the runner against our own idea of Ollama. fake-ollama encodes
  llm-router's tested understanding (streaming, usage chunks, faults).

## Consequences

- New: `e2e/ollama/` (harness, scenarios, testdata), `make fake-ollama`,
  `make e2e-ollama`, and a CI job.
- llm-router gets one feature change to fake-ollama (T1), with its own
  tests and an update to its ADR 0018.
- How we'll know: `make e2e-ollama` passes from a clean checkout, and fails
  with "run make fake-ollama" when the binary is absent.

## References

[^fake]: llm-router `internal/fakeollama/{config,reply,api,fault,control}.go` @9789c32 (read 2026-11-13). `answer`: "if results := trailingToolResults(req); len(results) > 0 { return reply{content: "The tool returned: " + …}"; tool rules: "if !ok || !m.has(CapabilityTools) { continue }"; arguments: "if !jsontext.Value(args).IsValid() { return … errInvalidArguments }"; `apiHandler` registers only `/v1/models` and `/v1/chat/completions`.
[^harness]: llm-router `e2e/harness/{binaries,fakeollama,target}.go` @9789c32 (read 2026-11-13). `StartFakeOllama` "runs fake-ollama with the config at path on free loopback ports and waits until it is listening"; `OllamaTarget` is "the real Ollama configured through the environment, or false if $E2E_OLLAMA_URL is unset".
[^measured]: Measured 2026-11-13 in the ws-online pod: anonymous `git ls-remote https://git.bobcob7.com/forgejo_admin/llm-router.git HEAD` returned `9789c328c6edd5f68cfd08c53bebeb15f607073d`. `gh repo view bobcob7/llm-router` failed with "Could not resolve to a Repository". Each was run once.
[^exectest]: workspaces `internal/exectest` @98697f3 (read 2026-11-13), used by the `claude` and `gh` client tests.
