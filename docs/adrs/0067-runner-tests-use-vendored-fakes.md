# 0067. Runner tests use fake-ollama and a fake MCP server kept in this repo; real models and real MCP servers are opt-in

- Status: Proposed
- Date: 2026-11-13; reworked 2026-11-14 after review (fake-ollama copied
  into this repo instead of built from a pinned llm-router clone; fake MCP
  server added)

## Context

### Problem

Every automated test of the runner must run against fakes, never a real
model or a real MCP server. fake-ollama was written in llm-router, an
internal tool whose `internal/` packages `ws` can't import. The first draft
of this ADR cloned llm-router at a pinned commit and built the fake from
it. The review said: don't clone llm-router; copy the code into this repo
and maintain it here. How do `ws`'s tests get their fakes, what scenarios
run, and what do the fakes still lack?

### What a solution needs

1. **Deterministic**: the same scripted replies every run; no GPU, no
   network beyond loopback.
2. **Owned here**: the fakes build with `go build` from this repo, follow
   this repo's lint and test rules, and change with the runner.
3. **Never shipped**: test binaries aren't in release artifacts or the
   image.
4. **Covers the failures** the runner handles: tool calls that edit files,
   verdicts, endpoint down, malformed calls, mid-stream drops, context
   overflow, a model without tools, MCP servers that fail, are slow, or
   expose dozens of tools, and secrets that must not reach a transcript.
5. **Fast in `make test`**; process-level tests are a separate target.

### Relevant facts

- llm-router @9789c32's fake-ollama is `internal/fakeollama` (about 1 200
  lines and 1 500 lines of tests, golden fixtures in `testdata/`),
  `internal/cli/fakeollama` (the cobra command), and `cmd/fake-ollama`. It
  imports llm-router's `openaiwire`, `sse`, `yamlconfig` and `httpserver`
  (about 1 400 lines with tests; `go.yaml.in/yaml/v3` is their only
  third-party import), and `telemetry`, which pulls in OpenTelemetry. One
  test (`config_test.go`) drives llm-router's `scenario` runner, which needs
  its agent and `openai-go`[^fake-imports].
- It is rule-driven (a regexp on the last user message gives a reply or a
  tool call), has fault injection (`down`, `status`, `hang`,
  `slow_first_token`, `drop_after_chunks`, `stall_after_chunks`, scoped to
  a model and counted), and a control API on a second port: `GET
  /requests` (method, path, model, stream, `traceparent`, body), `PUT
  /faults`, `POST /reset`[^fake].
- What it lacks for this runner: after tool results it always answers
  `The tool returned: …`, so it can't script a second tool call; it only
  calls offered tools with valid JSON arguments; a model without `tools`
  silently ignores them instead of answering 400; it serves no `/api/*`;
  it has no context limit; a `status` fault can't send `Retry-After`; and
  `/requests` doesn't say whether a bearer token was sent[^fake].
- `ws`'s release builds only `./cmd/ws` (`make dist`, the release workflow
  and `deploy/docker/Dockerfile`)[^release].
- `ws` tests already run fakes of `claude` and `gh` through
  `internal/exectest`[^exectest].
- Claude Code's MCP config for this user has a stdio server
  (`mcp-grafana`, configured by `GRAFANA_URL` in its `env` and a token in
  the process environment) and an HTTP server (`outline`, with an
  `Authorization` header)[^mcp-shape]. `mcp-grafana` exposes 80 tools and
  `outline` 20[^grafana-tools].

## Decision

**Copy fake-ollama into this repo, once, and maintain it here.** A task
([tasks.md](../design/ollama-agent/tasks.md), T1) copies from llm-router
@`9789c32`, reading a checkout of llm-router that is part of that task's
workspace, never cloned by a build or a test:

- `internal/fakeollama/` with its tests and golden `testdata/` (unchanged
  fixtures and their README);
- llm-router's `openaiwire`, `sse`, `yamlconfig` and `httpserver` as
  `internal/fakeollama/internal/{openaiwire,sse,yamlconfig,httpserver}`,
  so nothing else in `ws` can import them;
- the control API, unchanged;
- the cobra command as `internal/fakeollama/cmd` and the binary as
  `cmd/fake-ollama/main.go`, with llm-router's `telemetry` replaced by a
  `slog` JSON logger on stderr that keeps the `API listening on <addr>` and
  `control API listening on <addr>` lines tests wait for;
- not `config_test.go`'s scenario-runner test, which needs llm-router's
  agent; its config-loading cases are kept.

Each copied package's doc comment says `Copied from llm-router
internal/<pkg> @9789c32 (ADR 0067); maintained here.` From then on it's
`ws` code: it follows `ws`'s lint rules (`make lint`, the golangci config,
`moq`, json/v2), its test rules, and its review. There's **no sync
obligation**: fixes in either repo may be copied by hand when useful, and
nothing records or checks llm-router's commit after the copy. `go.mod`
gains `go.yaml.in/yaml/v3` as a direct requirement (it is already an
indirect one). The copy may need lint fixes; T1 makes them, and changes
behaviour only where a test proves the old behaviour is kept.

**Then fill the gaps in our copy** (T2): rules on conversation state
(`turn`, `match_tool_result`), `raw_arguments` and `any_tool`,
`strict_capabilities`, `GET /api/version`, `GET /api/tags` and `POST
/api/show`, per-model `context_length` with Ollama-style truncation,
`retry_after` on `status` faults, and an `auth` field on recorded requests
(`"bearer:" + the first 8 hex digits of the token's SHA-256`, never the
token).

**Add a fake MCP server** (T3), `internal/fakemcp` and `cmd/fake-mcp`, on
the official MCP Go SDK's server side, driven by a YAML script:

- transports: stdio (default) and streamable HTTP (`--http 127.0.0.1:0`,
  printing `MCP listening on <addr>`);
- `tools`: name, description, input schema, annotations, and `replies`
  chosen by a regexp on the call's canonical JSON arguments (text, an
  error result, or a protocol error);
- `generate: {count, prefix}`: filler tools, to imitate a server the size
  of `mcp-grafana`;
- faults: `slow_initialize`, `crash_after_calls`, `slow_call`;
- `require_env` (stdio) and `require_header` (HTTP): initialization fails
  unless the named variable or header has the given value, which proves the
  runner passes secrets to the server;
- `--record <file>`: one JSON line per call (tool, arguments, time), for
  assertions.

**Never shipped.** `cmd/fake-ollama` and `cmd/fake-mcp` are built only by
`make fakes` (into `bin/`) and by the e2e harness. Release builds name
`./cmd/ws` explicitly, so they don't change; `make lint` gains a rule that
`internal/fakeollama` and `internal/fakemcp` are imported only from
themselves, `cmd/fake-*` and `e2e/`.

**Three layers of tests.**

1. **Unit tests** (`go test ./...`, run by `make test` and CI) never open
   a socket to a model or an MCP process. `internal/agent` uses moq mocks;
   `internal/llm/openai` uses `httptest` servers replaying the copied
   golden fixtures; `internal/mcp` uses the SDK's in-memory transport with a
   `fakemcp` server in the same process. The copied fakes' own tests run
   here too.
2. **Process e2e** (`make e2e-runner`, build tag `e2e`, package
   `e2e/runner`) builds and runs the real `ws`, `fake-ollama` and
   `fake-mcp` binaries, with `WS_CONFIG_DIR` in a temp dir, a daemon on a
   temp socket, a local bare git repo as upstream, and a fake `claude` for
   the mixed tests. No network beyond loopback; no llm-router.
3. **Opt-in real services**, never in CI:
   - `E2E_RUNNER_URL` (with `E2E_RUNNER_MODEL`, default `qwen3-coder:30b`,
     and `E2E_RUNNER_API`, default `openai`): runs `edit-file` and
     `review-verdict` against a real model, asserting only that the run
     ended `done` with a verdict;
   - `E2E_MCP_SMOKE=<server>:<tool>[,…]`: with fake-ollama scripted to
     call those read-only tools, runs against the user's real MCP servers
     as defined in their Claude Code config (for example
     `outline:list_collections`), asserting the call succeeded and that no
     header or env value from the definition appears in the transcript.

**Scenarios**, each a fake config under `e2e/runner/testdata/`:

| Scenario | Fakes | Asserts |
| -------- | ----- | ------- |
| `edit-file` | turns: `write_file` → `run` (`git commit`) → `record_result PASS` | file content, commit, `result.toml` PASS, `usage.json`, wait JSON `ws_runner.exit = "done"`, `model` |
| `review-verdict` | `read_file` → `run` (`git diff`) → `record_result CHANGES_REQUESTED` | outcome `fail`, `child_failed`, report text |
| `endpoint-down` | fault `down` before spawn | spawn fails `endpoint_unreachable`, exit 1, no workspace left |
| `endpoint-down-mid-run` | `status: 503`, `retry_after: 1`, `count: 4` from turn 2 | 3 retries in the transcript, exit `endpoint_down`, FAIL with reason |
| `malformed-tool-call` | `raw_arguments: '{"path": '` once, then valid; three in a row | recovers; `malformed_tool_calls` |
| `unknown-tool` | `any_tool` call to `delete_repo` | error answer, then recovery |
| `mid-stream-drop` | `drop_after_chunks: 2`, `count: 1` | one retry, then `done` |
| `context-overflow` | `context_length: 4096`, a 40 KiB file read; a 30 KiB task | a `compaction` event; `context_overflow` |
| `no-tools-model` | model without `tools`, `probe = "ollama"`; again with `probe = "none"` | spawn fails `model_without_tools`; the run exits `model_no_tools` |
| `model-not-served` | a model id the fake doesn't list | spawn fails `model_not_served`, `details.models` |
| `token-redacted` | `secrets.env` has `WS_ENDPOINT_TOKEN_FAKE`; a `run` of `env` | `/requests` shows `auth`; the transcript and `runner.log` don't contain the token; `env` output lacks it |
| `mcp-stdio` | fake-mcp stdio with `require_env`; a rule calls `mcp__fake__lookup` | the call reached fake-mcp (`--record`), the result is in the transcript, the env value isn't |
| `mcp-http` | fake-mcp HTTP with `require_header` from a `${VAR}` header | as above, for the header |
| `mcp-filter` | fake-mcp `generate: {count: 80}`; role allows `lookup_*` | request bodies offer only allowed tools; a destructive tool not named exactly is refused |
| `mcp-tool-search` | 40 allowed tools, `mcp_upfront = 8`; rules call `tool_search`, then a found tool | the second request offers the found tool; the call succeeds |
| `mcp-server-fails` | fake-mcp `crash_after_calls: 1` | an `mcp_error` event, the run continues, the next call answers an error |
| `mixed-wait` | fake `claude` child plus `edit-file` | one `ws machine wait` returns both; `runner` and `model` are right |
| `daemon-resume` | `hang` fault; kill the runner; clear the fault | the daemon resumes it at the same step, then `done`; `restarts: 1` |
| `daemon-crash-limit` | kill the runner four times | exit `crashed`, FAIL with reason |
| `daemon-restart` | `ws daemon stop --keep-runners` mid-run, then a spawn | the new daemon adopts the runner; it finishes `done` |
| `daemon-start-resumes` | stop the daemon with runners stopped, start it | `stopped` runners resume |

### Why this option

1. **Deterministic**: scripted turns and tools; loopback only.
2. **Owned here**, as the review asked: a build never reaches llm-router,
   and fixes go straight into the code the tests use.
3. **Never shipped**: release builds name `./cmd/ws`, and a lint rule
   keeps the fakes out of production imports.
4. **Covers** the runner's failures, the daemon's supervision, and MCP,
   once the gaps are filled.
5. **Fast**: `make test` stays free of processes; e2e is its own target and
   CI job.

Where it's weak: two copies of fake-ollama now drift apart, by design. The
word-count tokenizer means overflow thresholds in tests don't match a real
model's. The fake MCP server shares the SDK with the client, so a protocol
misunderstanding in the SDK wouldn't show up; only the opt-in real smoke
catches that.

## Alternatives

### Clone llm-router at a pinned SHA and build the fake (the first draft)

**Pros**

- One source for the fake.

**Cons**

- The review rejected it: llm-router is an internal tool. It also needed
  network access to a private host on every clean build and in CI.

### `go tool` directive on llm-router's `cmd/fake-ollama`

**Pros**

- Pinned by `go.sum`.

**Cons**

- The module path doesn't resolve; a `replace` pulls llm-router's whole
  graph, OpenTelemetry included, into `ws`'s `go.mod`.

### Only `httptest` stubs and in-process MCP fakes, no processes

**Pros**

- The simplest.

**Cons**

- The daemon, process supervision and stdio MCP servers can only be tested
  with real processes.

### Test MCP against real servers in CI

**Pros**

- Real protocol behaviour.

**Cons**

- Needs credentials and network in CI, and the results change with the
  servers' data. Kept as an opt-in smoke.

## Consequences

- New: `internal/fakeollama/…` and `cmd/fake-ollama` (copied, then
  extended), `internal/fakemcp` and `cmd/fake-mcp`, `e2e/runner/`
  (harness, scenarios, testdata), `make fakes`, `make e2e-runner`, a CI
  job, and lint rules.
- `go.mod`: `go.yaml.in/yaml/v3` becomes direct; the MCP Go SDK is added by
  T3 (and used by the MCP client).
- llm-router changes nothing.
- How we'll know: `make e2e-runner` passes from a clean checkout with no
  network beyond loopback.

## References

[^fake-imports]: llm-router @9789c32, `grep` of imports in `internal/fakeollama/*.go`, `internal/cli/fakeollama/*.go` and `cmd/fake-ollama/main.go`, and `wc -l` of the packages (measured 2026-11-14): `openaiwire` 377 lines, `sse` 430, `yamlconfig` 133, `httpserver` 441, with tests; `telemetry` imports `go.opentelemetry.io/…`; `config_test.go` calls `scenario.NewRunner`.
[^fake]: llm-router `internal/fakeollama/{config,reply,api,fault,control,server}.go` @9789c32 (read 2026-11-13 and 2026-11-14). `answer`: "if results := trailingToolResults(req); len(results) > 0 { return reply{content: "The tool returned: " + …}"; tool rules: "if !ok || !m.has(CapabilityTools) { continue }"; `Fault` has `Down`, `Status`, `Message`, `Hang`, `SlowFirstToken`, `DropAfterChunks`, `StallAfterChunks` and no header field; `Request` records `Method`, `Path`, `Model`, `Stream`, `Traceparent`, `Body`.
[^release]: workspaces `Makefile` (`dist`: `go build … ./cmd/ws`), `.github/workflows/release.yml`, `deploy/docker/Dockerfile` (`go build … -o /out/ws ./cmd/ws`) @98697f3 (read 2026-11-14).
[^exectest]: workspaces `internal/exectest` @98697f3, used by the `claude` and `gh` client tests.
[^mcp-shape]: The shape of this user's `~/.claude.json` `mcpServers`, read 2026-11-14 with `jq` printing key names only: `mcp-grafana` `{type: "stdio", command, args, env: [GRAFANA_URL]}`, `outline` `{type: "http", url, headers: [Authorization]}`; no project-scope servers. `GRAFANA_SERVICE_ACCOUNT_TOKEN` is set in the pod's environment. No values were copied.
[^grafana-tools]: Counted 2026-11-14 from the tool list Claude Code 2.1.325 loaded in this session: 80 for `mcp-grafana`, from `add_activity_to_incident` to `validate_provisioning_file`, and 20 for `outline`, from `create_attachment` to `update_document`.
