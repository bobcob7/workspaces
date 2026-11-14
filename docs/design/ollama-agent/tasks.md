# Runner: implementation tasks

Implements ADRs [0062](../../adrs/0062-agent-runner-built-into-ws.md) to
[0070](../../adrs/0070-runner-agents-for-people-and-loops.md) and
[reference.md](reference.md). Every task targets **this repo**
(workspaces); nothing is done in llm-router, and nothing clones it. Each
task builds, passes its tests, and can be merged on its own. Each task is
one commit, conventional and imperative, for example `feat: add the
runner's provider seam (ADR 0062)`.

**Order and parallelism**

| Task | Title | Depends on | Parallel with | Size |
| ---- | ----- | ---------- | ------------- | ---- |
| T1 | Copy fake-ollama into `internal/fakeollama` and `cmd/fake-ollama` | nothing | T3–T6, T8 | |
| T2 | fake-ollama: conversation rules, raw arguments, strict capabilities, `/api/*`, context length, `Retry-After`, `auth` | T1 | T7, T9, T11, T16 | |
| T3 | `internal/fakemcp` and `cmd/fake-mcp` | nothing | T1, T4–T6, T8 | |
| T4 | `internal/config`: `[runner]`, `[endpoints]`, `[models]`, `[runner.roles]`, `[runner.env]`, `[daemon]`, `ws config` keys | nothing | T1, T3, T5, T6, T8 | |
| T5 | `internal/agent/tools`: the seven tools, guards, environment scrub, redactor | nothing | T1, T3, T4, T6, T8 | small |
| T6 | `internal/llm`: the provider seam, errors, preflight | nothing | T1, T3–T5, T8 | small |
| T7 | `internal/llm/openai`: the OpenAI-compatible provider, and `internal/llm/providers` | T1, T4, T6 | T2, T9, T11, T16 | |
| T8 | A `model` for every agent; `.Runner` and `.Model` in role templates | nothing | T1, T3–T6 | small |
| T9 | `internal/mcp`: server definitions, expansion, names, filter | nothing (T4 for the role keys' names only) | T2, T7, T11, T16 | small |
| T10 | `internal/mcp`: the client (stdio, streamable HTTP, SSE) | T3, T9 | T13, T17 | |
| T11 | `internal/agent`: the loop (copied), budgets, compaction, transcript, preamble, project instructions | T5, T6 | T2, T7, T9, T16 | |
| T12 | MCP tools in the loop: the toolset and `tool_search` | T10, T11 | T13, T23 | small |
| T13 | `internal/telemetry`: OpenTelemetry setup and the GenAI decorators | T6, T11 | T10, T12, T17 | |
| T14 | `internal/runner`, session kind `runner`, hidden `ws runner run` | T4, T7, T12, T13 | none | |
| T15 | `lifetime` reads runner sessions; `runner`/`model`/`ws_runner` in `ws list`, `children`, `wait`; `ws machine logs` | T8, T14 | T18 | |
| T16 | `secrets.env` and the runner's environment | T4 | T2, T7, T9, T11 | small |
| T17 | `internal/daemon` core and `ws daemon run|status|stop` (with `--origin app`) | T16 | T10, T13 | |
| T18 | The daemon supervises runners; preflight, `ws runner check` and `ws runner stop` | T14, T16, T17 | T15 | |
| T19 | `ws machine spawn --runner`, the shared runner start, `ws rm`/`ws restore` through the daemon, guides and docs | T15, T18 | none | |
| T20 | `ws claude --runner` and `--endpoint` | T19 | T21, T24 | small |
| T21 | Loops on the runner: `--runner`, `--endpoint`, record version 4, the tick's start path | T19 | T20, T24 | |
| T22 | `e2e/runner`: harness, scenarios, `make fakes`, `make e2e-runner`, CI | T2, T3, T21 | T25 | |
| T23 | The desktop app and `ws-online` keep a daemon running | T17 | T12–T15, T18–T21 | small |
| T24 | Desktop: a quiet model label on every agent, the runner transcript, Stop | T19 | T20, T21 | |
| T25 | Desktop: Settings › Models, with the Runner service line | T23, T24 | T22 | |
| T26 | Desktop: runner and model pickers in the loop form and the New agent dialog | T20, T21, T25 | T27 | |
| T27 | *Optional:* `internal/llm/ollama`, the Ollama-native provider | T2, T7 | T26 | |

Waves, if run in parallel: **1** T1, T3, T4, T5, T6, T8 · **2** T2, T7, T9,
T11, T16 · **3** T10, T13, T17 · **4** T12, T23 · **5** T14 · **6** T15,
T18 · **7** T19 · **8** T20, T21, T24 · **9** T22, T25 · **10** T26, T27.
Run at most one heavy implementer at a time on an 8 GB host (Go full test
runs, `cargo`, vitest): within a wave, start the heavy tasks one after
another and run the `small` ones beside them. The desktop tasks T24, T25
and T26 share `desktop/src/test/fixtures.ts`, so they run in that order.

**T27 is optional.** It's the next provider, planned but not required for
this plan to ship; the orchestrator runs it only when the owner asks. Its
spec is complete so it can be picked up later.

Task numbers changed twice on 2026-11-14. In the first rework, the first
draft's T1 (fake-ollama changes in llm-router) became T1 and T2, and its
T2–T8 became T4, T6/T7, T5, T11, T13/T14, T18/T19, T21. In the second,
T13 (telemetry), T20, T21 (ADR 0070), T25, T26 and T27 were added: the
first rework's T13–T19 are now T14–T19 and T22, its T20 is T23, and its
T21 is split into T24 and T25.

**Read before any task:** [reference.md](reference.md) (every flag, JSON
shape, file and error), the "Decision" section of the ADR the task names,
and only the files the task lists. Where this file and reference.md
disagree, reference.md wins. Don't read whole packages.

**Rules for every task**

- Go 1.27. Follow the code in the package you edit. Errors are wrapped
  with `fmt.Errorf("…: %w", err)`, lower case, with no "failed to".
  **No blank lines inside function bodies.** Return early. Keep functions
  under about 40 lines. `context.Context` is the first parameter. No
  `init()` and no package-level mutable state.
- Interfaces live in `interfaces.go` with `//go:generate go tool moq -out
  moq_test.go . <names>`, and mocks are generated with `go generate
  ./<pkg>/`. Never hand-write mocks.
- Tests: `github.com/stretchr/testify/assert` and `require`. One test
  function per function or method, `TestType_Method`, with `t.Run`
  subtests per behaviour. Use `t.Context()`, `t.TempDir()`, and
  `slog.New(slog.NewJSONHandler(io.Discard, nil))` for loggers. Call
  `t.Parallel()` only in tests that do I/O (files, sockets, processes).
- JSON: `encoding/json/v2` (`json.Marshal`, `json.Unmarshal`) in new
  packages, with `omitzero` and never `omitempty`. `ws` output fields are
  `snake_case`. An empty optional value is `null` (`optional(s)` in
  `internal/cli/cache.go`), lists are `[]`, and times use `formatTime`.
  Every output object keeps `warnings`. Never change an existing JSON field,
  error code or exit code. New fields go after the existing ones.
- Files `ws` writes go through a temp file in the same directory and an
  `os.Rename`, with mode 0o600 for files and 0o700 for directories.
- Run `make fmt` before committing. `make lint` must pass. Never call
  `exec.Command` outside the packages the Makefile's lint rules allow;
  shell commands go through `internal/gate`.
- **Code copied from llm-router** (T1, T11, T13) is this repo's code from the
  moment it lands: it follows every rule above, `make lint` included. Each
  copied package's doc comment says `Copied from llm-router internal/<pkg>
  @9789c32 (ADR 0067); maintained here.` (ADR 0062 for the loop, ADR 0065
  for telemetry). Read llm-router from the read-only checkout your
  workspace was given (the orchestrator spawns T1, T7, T11 and T13 with
  `llm-router@9789c32` as a second
  repo spec); never `git clone` it, never add it to `go.mod`.
- New dependencies go in `go.mod` only in the task that names them: T1
  (`go.yaml.in/yaml/v3` becomes direct), T3 (`github.com/modelcontextprotocol/go-sdk`,
  the newest v1), T7 (`github.com/openai/openai-go/v3 v3.71.1`), T13 (the
  OpenTelemetry Go SDK at llm-router's versions: `go.opentelemetry.io/otel`,
  `/sdk`, `/sdk/metric`, `/sdk/log`, `/trace`, `/metric` v1.47.0 (`/log`
  v1.47.0 comes in indirectly),
  `go.opentelemetry.io/contrib/exporters/autoexport` v0.72.0,
  `go.opentelemetry.io/contrib/bridges/otelslog` v0.21.0). Run `go mod
  tidy` and commit `go.sum`.
- In tasks that touch `internal/cli`, run
  `go test ./internal/cli/ -run TestGuideTopics`. It checks that each
  command has a guide in `internal/cli/guides/` and a topic in
  `guideTopics()` (`internal/cli/llm.go`), that each guide mentions every
  local flag and `--json`, has a ` ```json ` block under `## JSON output`,
  `## Exit codes` and `## Examples`, and that `guides/ws.md` links every
  guide and lists every error code. The finished guides are in
  [guides/](guides/): copy them with `cp`, unchanged.
- **Docs**: diagrams in markdown files are fenced ` ```mermaid ` blocks
  (`flowchart`, `sequenceDiagram`, `stateDiagram-v2`), never ASCII or
  box-drawing art. Keep them to syntax GitHub renders: quote labels that
  hold punctuation, no HTML, no click handlers or themes. File trees and
  command synopses stay plain code blocks.
- Run heavy commands one at a time (`go test -p 2`). Commit and push
  before the slowest suite. If a command was killed, say so in the report.
- **Model ids** (`qwen/qwen3.5-9b`, `qwen3.5:9b`) are opaque strings:
  never split them on `/` or `:`, never use one as a TOML key, a map key
  from config, or in a file name. Test values in new tests use these two
  ids where an id is needed.
- Commit trailer: as your role prompt says.

---

## T1. Copy fake-ollama into `internal/fakeollama` and `cmd/fake-ollama`

**Depends on:** nothing. **Parallel with:** T3–T6, T8.
**ADR:** [0067](../../adrs/0067-runner-tests-use-vendored-fakes.md)
("Copy fake-ollama into this repo").

**Spawn with** two repo specs: `workspaces@<base>` and
`llm-router@9789c32` (read-only source; don't commit to it).

**Read:** in llm-router, `internal/fakeollama/*.go` (file list and
imports only), `internal/cli/fakeollama/command.go`,
`cmd/fake-ollama/main.go`, `internal/fakeollama/testdata/README.md`. In
this repo: `Makefile` (`lint`, `build`), `.golangci.yml`, `cmd/ws/main.go`.

**Copy** (`cp`, then edit imports):

| From llm-router @9789c32 | To |
| ------------------------ | -- |
| `internal/fakeollama/*.go`, `testdata/` | `internal/fakeollama/` |
| `internal/openaiwire/` | `internal/fakeollama/internal/openaiwire/` |
| `internal/sse/` | `internal/fakeollama/internal/sse/` |
| `internal/yamlconfig/` | `internal/fakeollama/internal/yamlconfig/` |
| `internal/httpserver/` | `internal/fakeollama/internal/httpserver/` |
| `internal/cli/fakeollama/command.go` (+ test, `interfaces.go`) | `internal/fakeollama/cmd/` (package `cmd`) |

Then:

- Rewrite imports `github.com/bobcob7/llm-router/internal/<x>` to
  `github.com/bobcob7/workspaces/internal/fakeollama/internal/<x>` (and
  `…/internal/fakeollama`). Regenerate every `moq_test.go` with this repo's
  `go tool moq`.
- Replace `telemetry` and `telemetrytest`: tests use
  `slog.New(slog.NewJSONHandler(io.Discard, nil))`; the command takes a
  `*slog.Logger`.
- **Create** `cmd/fake-ollama/main.go`: a cobra root built from
  `cmd.NewCommand`, logging `slog` JSON to stderr, exit 1 on error. Keep
  the command's stdout lines `API listening on <url>` and `control API
  listening on <url>` exactly: tests wait for them.
- Drop the test case in `config_test.go` that calls `scenario.NewRunner`
  (it needs llm-router's agent); keep its config-loading cases.
- Add the doc-comment provenance line (rules above) to each copied
  package, and `internal/fakeollama/testdata/README.md` gains one line:
  "Copied from llm-router @9789c32; maintained here."
- Fix whatever `make lint` reports in the copy. Change behaviour only
  where a test shows the old behaviour is kept.
- `Makefile`: target `fakes` (`go build -o $(BIN)/fake-ollama
  ./cmd/fake-ollama`; T3 adds fake-mcp), in `.PHONY`. Lint rule: the
  fakes are imported only from themselves, `cmd/fake-*` and `e2e/`:
  `@! grep -rnE '"github.com/bobcob7/workspaces/internal/fake(ollama|mcp)' --include='*.go' . | grep -vE '^\./(internal/fake(ollama|mcp)|cmd/fake-|e2e)/'`.
- `go.mod`: `go.yaml.in/yaml/v3` moves to the direct block.
- Don't touch `make dist`, `.github/workflows/release.yml` or
  `deploy/docker/Dockerfile`: they build `./cmd/ws` by name, so the fake
  never ships. Check with `grep -n 'go build' Makefile deploy/docker/Dockerfile .github/workflows/release.yml`.

**Acceptance**

- `go test ./internal/fakeollama/...` passes with every copied test except
  the dropped scenario case. Golden fixtures are byte-identical to
  llm-router's (`diff -r`).
- `make fakes` builds `bin/fake-ollama`; `bin/fake-ollama --config
  docs/design/ollama-agent/fake-ollama/edit-file.yaml --listen 127.0.0.1:0
  --control-listen 127.0.0.1:0` prints both listening lines.
  (The design configs use T2's fields; until T2 lands, try a config from
  `internal/fakeollama/testdata` or a minimal one.)
- `grep -rn 'llm-router' --include='*.go' .` finds only provenance
  comments. `make lint` passes, including the new rule.

**Verify**

```console
$ go generate ./internal/fakeollama/...
$ go test -p 2 ./internal/fakeollama/...
$ make lint && make fakes
```

---

## T2. fake-ollama: conversation rules, raw arguments, strict capabilities, `/api/*`, context length, `Retry-After`, `auth`

**Depends on:** T1. **Parallel with:** T7, T9, T11, T16.
**ADR:** [0067](../../adrs/0067-runner-tests-use-vendored-fakes.md)
("Then fill the gaps").

**Read:** `internal/fakeollama/config.go`, `reply.go`, `api.go`,
`response.go`, `fault.go`, `control.go`, `server.go` (only `New` and
`model`), `reply_test.go` (style), and the design's
[fake-ollama/](fake-ollama/) configs, which must load after this task.

**Change**

- `config.go`
  - `Config` gains `StrictCapabilities bool `yaml:"strict_capabilities"``.
  - `Model` gains `ContextLength int `yaml:"context_length"`` (0: no limit)
    and `Family string `yaml:"family"`` (default `"fake"`).
  - `Rule` gains:
    ```go
    // Turn, if set, makes the rule apply only when the request has exactly
    // this many assistant messages (0 for the first reply).
    Turn *int `yaml:"turn"`
    // MatchToolResult, if set, is a regular expression matched against the
    // trailing tool results joined with "\n". A rule with it applies only
    // to requests that end with tool results.
    MatchToolResult string `yaml:"match_tool_result"`
    // RawArguments is sent verbatim as the tool call's arguments, even if
    // it isn't valid JSON. Exclusive with Arguments.
    RawArguments string `yaml:"raw_arguments"`
    // AnyTool calls Tool even when the request doesn't offer it.
    AnyTool bool `yaml:"any_tool"`
    ```
  - `validateModels`: `context_length` must not be negative.
- `reply.go`
  - `compileRule`: `RawArguments` with `Arguments` → `errRawAndArguments`
    ("set at most one of arguments and raw_arguments"). `RawArguments` or
    `AnyTool` without `Tool` → `errArgumentsWithoutTool`. `Match` may be
    empty when `Turn` or `MatchToolResult` is set. Compile
    `MatchToolResult`.
  - `answer`: first the rules with `Turn` or `MatchToolResult`, in config
    order; one applies when its `Turn` (if set) equals the number of
    assistant messages, its `MatchToolResult` (if set) matches the trailing
    tool results, and its `Match` matches the last user message. Then
    today's behaviour. Template data gains `ToolResults []string` and
    `Turn int`.
  - With `AnyTool`, skip `offeredTool` and use a synthetic tool with no
    parameters. With `RawArguments`, render it as a template and skip the
    JSON check.
- `api.go`
  - `StrictCapabilities`, a request with `tools`, and a model without the
    `tools` capability → `400 {"error":"<model> does not support tools"}`
    (Ollama's native shape; helper `writeOllamaError(w, status, msg)`).
  - New `truncate.go`: `func truncateToContext(req openaiwire.ChatRequest,
    limit int) openaiwire.ChatRequest` drops the oldest non-system messages
    until the word count (as `usageFor` counts) fits, keeping the last
    message; the reply and `usageFor` use the truncated request.
  - `GET /api/version` → `{"version":"0.0.0-fake"}`; `GET /api/tags` →
    `{"models":[{"name":"<m>","model":"<m>","size":0,"details":{"family":"<family>"}}]}`;
    `POST /api/show` `{"model":"<m>"}` → `{"capabilities":[…],
    "model_info":{"<family>.context_length":<n>},"details":{"family":"<family>"}}`,
    `capabilities` being `completion` plus `tools` and `thinking` (for the
    `reasoning` capability) when the model has them, `<n>` its
    `ContextLength` or 32768. Unknown model → `404 {"error":"model '<m>' not
    found"}`. These are recorded and faulted like the other endpoints.
- `fault.go`: `Fault` gains `RetryAfter int `yaml:"retry_after" json:"retry_after"``
  (seconds); a `status` fault with it sets `Retry-After`.
- `control.go`: each recorded request gains `Auth string `json:"auth"``:
  `""` without an `Authorization` header, else `"bearer:"` + the first 8
  hex digits of SHA-256 of the token. The token is never stored.
- `internal/fakeollama/testdata/README.md`: a "Rules and endpoints added in
  workspaces" section listing the above.

**Acceptance**

- Rules `turn: 0 → write_file`, `turn: 1 → run`, `turn: 2 → reply
  "done"`, over three growing requests, give `write_file`, `run`, `done`.
- `match_tool_result: "exit 1"` applies only when a trailing result
  contains it. `raw_arguments: '{"path": '` gives exactly that string,
  streamed and not. `any_tool` calls an unoffered tool.
- `strict_capabilities` → the 400 above; without it, unchanged.
- `context_length: 50`, a 200-word conversation → `prompt_tokens` ≤ 50, the
  system and last user message kept.
- `/api/show`, `/api/tags`, `/api/version` shapes; a `down` fault fails
  them; `/requests` records them.
- `status: 503, retry_after: 7` → `Retry-After: 7`. A request with
  `Authorization: Bearer secret` is recorded with `auth`
  `bearer:2bb80d53`, and `secret` appears nowhere in `/requests`.
- Every YAML file in `docs/design/ollama-agent/fake-ollama/` loads and
  compiles (`LoadConfig`, then `New`, which runs `compileRules`) without
  error (a table test over the directory).
- Model names with `/` and `:` work everywhere: `qwen/qwen3.5-9b` and
  `qwen3.5:9b` are listed by `/v1/models` and `/api/tags`, answered by
  `/api/show` and chat, and scoped by faults, exactly as written.
- Package coverage doesn't drop (`go test -cover`).

**Verify**

```console
$ go test -cover ./internal/fakeollama/...
$ make lint
```

---

## T3. `internal/fakemcp` and `cmd/fake-mcp`

**Depends on:** nothing. **Parallel with:** T1, T4–T6, T8.
**ADR:** [0067](../../adrs/0067-runner-tests-use-vendored-fakes.md)
("Add a fake MCP server").

**Read:** the MCP Go SDK's README and its `mcp.Server`, `mcp.AddTool` /
`Server.AddTool`, `mcp.StdioTransport`, `mcp.NewStreamableHTTPHandler` and
`mcp.NewInMemoryTransports` docs (`go doc`); [fake-mcp/](fake-mcp/) (the
three configs must load).

**Add** `github.com/modelcontextprotocol/go-sdk` (newest v1; write the
version into the report).

**Create** `internal/fakemcp/{config.go,server.go,record.go}` with tests,
and `cmd/fake-mcp/main.go`.

```go
// Package fakemcp is a scripted MCP server for tests (ADR 0067). It is
// never shipped.
package fakemcp

// Config is a fake-mcp YAML file (docs/design/ollama-agent/fake-mcp).
type Config struct {
	Server        ServerInfo        `yaml:"server"`         // initialize's serverInfo and instructions
	RequireEnv    map[string]string `yaml:"require_env"`    // stdio: initialize fails unless each is set to the value
	RequireHeader map[string]string `yaml:"require_header"` // http: each request must carry each header with the value
	Faults        Faults            `yaml:"faults"`
	Tools         []Tool            `yaml:"tools"`
	Generate      *Generate         `yaml:"generate"`
}
type ServerInfo struct {
	Name         string `yaml:"name"`    // default "fake"
	Version      string `yaml:"version"` // default "0.0.0-fake"
	Instructions string `yaml:"instructions"`
}
type Faults struct {
	SlowInitialize  time.Duration `yaml:"slow_initialize"`
	CrashAfterCalls int           `yaml:"crash_after_calls"` // exit(3) after this many calls; 0 = never
	SlowCall        time.Duration `yaml:"slow_call"`
}
type Tool struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	InputSchema map[string]any `yaml:"input_schema"` // default {"type":"object"}
	Annotations Annotations    `yaml:"annotations"`
	Replies     []Reply        `yaml:"replies"`
}
type Annotations struct {
	ReadOnlyHint    *bool `yaml:"readOnlyHint"`
	DestructiveHint *bool `yaml:"destructiveHint"`
}
// Reply: the first whose Match (a regexp on the canonical JSON arguments;
// empty matches all) matches is sent.
type Reply struct {
	Match         string `yaml:"match"`
	Text          string `yaml:"text"`           // a text result (Go template over .Args)
	Error         string `yaml:"error"`          // an isError result
	ProtocolError string `yaml:"protocol_error"` // a JSON-RPC error
}
type Generate struct {
	Count  int    `yaml:"count"`
	Prefix string `yaml:"prefix"` // tools <prefix>001…, read-only, one-line description, replying "ok"
}

func LoadConfig(path string) (Config, error)
// NewServer builds the SDK server for cfg; record (may be nil) gets one
// JSON line per call: {"time","tool","arguments"}.
func NewServer(cfg Config, record io.Writer, log *slog.Logger) (*mcp.Server, error)
// CheckEnv is RequireEnv against getenv; the error names the variable, never a value.
func CheckEnv(cfg Config, getenv func(string) string) error
// RequireHeaders wraps h, answering 401 when a required header is missing or wrong.
func RequireHeaders(cfg Config, h http.Handler) http.Handler
```

`cmd/fake-mcp/main.go`: flags `--config <file>` (required), `--http
<addr>` (streamable HTTP; prints `MCP listening on http://<addr>/mcp` on
stdout), `--record <file>`. Without `--http`, stdio; `CheckEnv` first
(exit 2 with the message on stderr). Logs to stderr. Then extend `make
fakes` with `go build -o $(BIN)/fake-mcp ./cmd/fake-mcp`.

**Acceptance** (in-process, `mcp.NewInMemoryTransports` and an SDK
client):

- The three configs in [fake-mcp/](fake-mcp/) load. `many-tools.yaml`
  lists 80 generated tools (`list_thing_001` …) plus the two scripted
  ones, with `readOnlyHint` on the generated ones and `destructiveHint`
  on `delete_dashboard`; `stdio.yaml`'s `server.instructions` reaches
  the client.
- A call matching a reply gets its text; `error` gives `isError`;
  `protocol_error` gives a JSON-RPC error; `--record` gets one line per
  call.
- `crash_after_calls: 1`: the second call fails (in tests, an injected
  exit function). `slow_call` delays.
- `CheckEnv` with a missing variable names it; `RequireHeaders` returns
  401 without the header and passes with it; neither message contains the
  expected value.
- `make fakes` builds both binaries; `make lint` passes.

**Verify**

```console
$ go test ./internal/fakemcp/
$ make lint && make fakes
```

---

## T4. `internal/config`: `[runner]`, `[endpoints]`, `[models]`, `[runner.roles]`, `[runner.env]`, `[daemon]`, `ws config` keys

**Depends on:** nothing. **Parallel with:** T1, T3, T5, T6, T8.
**ADRs:** [0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md),
[0068](../../adrs/0068-ws-daemon.md) (`[runner.env]`, `[daemon]`),
[0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md) (MCP role keys).

**Read:** `internal/config/config.go`, `internal/config/edit.go`
(`fixedRules`, `profileRule`, `parseKeyWith`, `normalizeOne`, `Settings`,
`tokenHint`), `config_test.go` and `edit_test.go` (style only),
`docs/config.md` "Schema", reference.md [config.toml](reference.md#configtoml).

**Create** `internal/config/runner.go` and `runner_test.go`.

```go
// Runner is config.toml's [runner] (ADRs 0064, 0068, 0069).
type Runner struct {
	Model            string                `toml:"model" json:"model"`
	Fallback         string                `toml:"fallback" json:"fallback"`
	KeepGitTokens    *bool                 `toml:"keep_git_tokens" json:"keep_git_tokens"`
	UserInstructions bool                  `toml:"user_instructions" json:"user_instructions"`
	Env              map[string]string     `toml:"env" json:"env"`
	Roles            map[string]RunnerRole `toml:"roles" json:"roles"`
}

// Endpoint is one [endpoints.<name>].
type Endpoint struct {
	URL            string        `toml:"url" json:"url"`
	API            string        `toml:"api" json:"api"`     // default APIOpenAI
	Probe          string        `toml:"probe" json:"probe"` // auto | ollama | none; default auto
	RequestTimeout time.Duration `toml:"request_timeout" json:"request_timeout"`
	IdleTimeout    time.Duration `toml:"idle_timeout" json:"idle_timeout"`
}

// Model is one [models.<alias>].
type Model struct {
	Endpoint      string `toml:"endpoint" json:"endpoint"`
	ID            string `toml:"id" json:"id"`
	ContextTokens int    `toml:"context_tokens" json:"context_tokens"`
	MaxOutput     int    `toml:"max_output" json:"max_output"`
	Temperature   string `toml:"temperature" json:"temperature"` // a float as a string; default "0.2"
	Reasoning     string `toml:"reasoning" json:"reasoning"`     // "" | none | low | medium | high
}

// RunnerRole is one [runner.roles.<role>].
type RunnerRole struct {
	Runner     string        `toml:"runner" json:"runner"`
	Model      string        `toml:"model" json:"model"`
	MaxSteps   int           `toml:"max_steps" json:"max_steps"`
	Timeout    time.Duration `toml:"timeout" json:"timeout"`
	MaxTokens  int           `toml:"max_tokens" json:"max_tokens"`
	MCPServers []string      `toml:"mcp_servers" json:"mcp_servers"`
	MCPTools   []string      `toml:"mcp_tools" json:"mcp_tools"`   // default ["*"]
	MCPDeny    []string      `toml:"mcp_deny" json:"mcp_deny"`
	MCPUpfront *int          `toml:"mcp_upfront" json:"mcp_upfront"` // default 8
}

// Daemon is [daemon].
type Daemon struct {
	ResumeOnStart *bool `toml:"resume_on_start" json:"resume_on_start"` // default true
}

const (
	RunnerClaude     = "claude"
	RunnerWS         = "ws"
	APIOpenAI        = "openai"
	MinContextTokens = 16384
)

// ReservedAPIs are wire protocols the design names but ws doesn't
// implement yet (ADR 0062): "ollama" is planned next (T27, which removes
// it from this list), the others are reserved.
func ReservedAPIs() []string { return []string{"ollama", "openai-responses", "anthropic"} }

// ReasoningLevels are the values of a model's reasoning besides "".
func ReasoningLevels() []string { return []string{"none", "low", "medium", "high"} }

var (
	ErrEndpointUnknown   = errors.New("endpoint not configured")
	ErrNoModel           = errors.New("no runner model given or configured")
	ErrAPINotImplemented = errors.New("api not implemented yet")
)

// Resolved is everything a spawn needs to start a ws child, defaults filled in.
type Resolved struct {
	ModelAlias    string // "" when a raw id was given
	ModelID       string
	EndpointName  string
	Endpoint      Endpoint
	ContextTokens int
	MaxOutput     int
	Temperature   float64
	Reasoning     string
	MaxSteps      int
	Timeout       time.Duration
	MaxTokens     int
	MCPServers    []string
	MCPTools      []string
	MCPDeny       []string
	MCPUpfront    int
	Warnings      []string
}

// RunnerFor is runner.roles.<role>.runner, or RunnerClaude.
func (c *Config) RunnerFor(role string) string
// Resolve applies ADR 0064's order: modelFlag (an alias, else a raw id on
// the endpoint of the alias the role or runner.model would choose, with a
// warning), the role's model, runner.model; endpointFlag overrides the
// alias's endpoint. Budgets from the role, else DefaultBudget(role).
func (c *Config) Resolve(role, endpointFlag, modelFlag string) (Resolved, error)
// KeepsGitTokens is runner.keep_git_tokens, default true.
func (r *Runner) KeepsGitTokens() bool
// ResumesOnStart is daemon.resume_on_start, default true.
func (d *Daemon) ResumesOnStart() bool
// DefaultBudget: implementer 80 steps, 60m, 4_000_000; reviewer 40, 30m,
// 2_000_000; verifier 40, 60m, 2_000_000; anything else 60, 60m, 4_000_000.
func DefaultBudget(role string) (maxSteps int, timeout time.Duration, maxTokens int)
// TokenEnv is WS_ENDPOINT_TOKEN_ + upper(name), '-' → '_'.
func TokenEnv(name string) string
func (c *Config) checkRunner() error // called from normalize
```

`Config` gains, after `Review`: `Runner Runner `toml:"runner"``,
`Endpoints map[string]Endpoint `toml:"endpoints"``, `Models
map[string]Model `toml:"models"``, `Daemon Daemon `toml:"daemon"``.

**`checkRunner` rules** (each error names the key):

- `fallback` ∈ {`""`, `none`, `claude`}; `runner` ∈ {`""`, `claude`, `ws`};
  `probe` ∈ {`""`, `auto`, `ollama`, `none`}; `api` is `""` or `openai`, a
  reserved name → `ErrAPINotImplemented` ("api = "anthropic" isn't
  implemented yet"), anything else invalid.
- Names (endpoint, alias, role) match `^[a-z][a-z0-9-]{0,31}$`.
- `url` required, `http`/`https`, a host, no user info, query or fragment.
- `endpoint` and `id` required on a model; `id` has no control
  characters and is kept exactly (no trimming, no splitting on `/` or
  `:`); `endpoint` defined; `context_tokens` ≥ `MinContextTokens`;
  `max_output` ≥ 0; `temperature` parses as a float in [0, 2];
  `reasoning` is `""` or one of `ReasoningLevels()`.
- `runner.model` and every role `model`, when set, name a defined alias.
- `[runner.env]` names match `^[A-Z_][A-Z0-9_]*$`; a name ending `_TOKEN`,
  `_KEY`, `_SECRET` or `_PASSWORD` → `put secrets in secrets.env`.
- `mcp_upfront` ≥ 0; `mcp_servers` names are non-empty.
- A `token` key anywhere in these tables → `tokens go in
  WS_ENDPOINT_TOKEN_<NAME> in the environment or secrets.env, never in
  config.toml` (extend `tokenHint`).

**`ws config` keys** (`edit.go`): new types `TypeInt` (`int`) and
`TypeFloat` (`float`, stored as a string). Fixed rules after the review
ones: `runner.model`, `runner.fallback`, `runner.keep_git_tokens`,
`runner.user_instructions`, `daemon.resume_on_start`. Pattern rules:
`runner.env.<NAME>`; `endpoints.<name>.{url,api,probe,request_timeout,idle_timeout}`;
`models.<alias>.{endpoint,id,context_tokens,max_output,temperature,reasoning}`;
`runner.roles.<role>.{runner,model,max_steps,timeout,max_tokens,mcp_servers,mcp_tools,mcp_deny,mcp_upfront}`
(string lists as for existing list keys). `Settings` lists fixed keys, then
each endpoint, model and role sorted by name. `Unset` of
`endpoints.<name>`, `models.<alias>`, `runner.roles.<role>` or
`runner.env.<NAME>` removes it.

**Docs:** `docs/config.md` gains the tables from reference.md and, at the
end of "The generated file", the commented block from the
[README](README.md#configuration); the same block goes into the generated
file's template in `internal/config/config.go` (with a test that the
generated file still parses to `Default()`). Also from the
[README](README.md#configuration); `docs/tools/ws-config.md` lists the keys
and the `int`/`float` types.

**Acceptance**

- `Resolve("implementer", "", "")` with `runner.model = "qwen9b"` and role
  implementer `model = "qwen9b-think"` → alias `qwen9b-think`, its
  endpoint, 80 steps, 60m, 4 000 000, `api` `openai`, `probe` `auto`, 10m,
  90s, `max_output` 4096, temperature 0.2, reasoning `""`, `mcp_tools`
  `["*"]`, `mcp_upfront` 8. With `reasoning = "none"` on the alias,
  `Resolved.Reasoning` is `none`.
- Flags win: `Resolve("implementer", "cpu", "qwen9b")` → endpoint `cpu`. An
  unknown endpoint → `ErrEndpointUnknown`. `Resolve("x", "", "qwen/qwen3.5-9b")`
  and `Resolve("x", "", "qwen3.5:9b")` (no such alias) → exactly that id
  on `runner.model`'s endpoint and a warning. `Resolve("", "", "")` (no
  role, as `ws claude` and loops call it) → `runner.model` with the "other"
  budget. No model anywhere → `ErrNoModel`.
- `RunnerFor`: configured role → its runner; anything else → `claude`.
  `TokenEnv("gpu-a")` = `WS_ENDPOINT_TOKEN_GPU_A`.
- Parsing fails, naming the key, for `context_tokens = 4096`, `url =
  "ftp://x"`, `url = "http://u:p@x"`, `fallback = "always"`, `api =
  "anthropic"` (the message says not implemented yet), a model whose
  endpoint isn't defined, `reasoning = "max"`, an `id` with a newline,
  `token = "x"`, `[runner.env] METRICS_TOKEN = "x"` and the name `GPU`.
  `id = "qwen/qwen3.5-9b"` parses and round-trips unchanged.
- `ws config set models.qwen9b.context_tokens 65536` and `ws config set
  models.qwen9b.id qwen/qwen3.5-9b` write them and keep comments; `set
  models.qwen9b.context_tokens 4096` → `ErrValueInvalid`; `unset
  models.qwen9b` removes the table. An empty `config.toml` still parses to `Default()`. Existing
  tests pass unchanged.

**Verify**

```console
$ go test ./internal/config/
$ go test ./internal/cli/ -run 'TestApp_.*Config|TestGuideTopics'
$ make lint
```

---

## T5. `internal/agent/tools`: the seven tools, guards, environment scrub, redactor

**Depends on:** nothing. **Parallel with:** T1, T3, T4, T6, T8.
**ADRs:** [0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md)
(Tools, What the agent must not do),
[0066](../../adrs/0066-runner-security-boundaries.md).

**Read:** `internal/gate/gate.go` (`Request`, `Result`, `Runner.Run`),
`internal/machine/store.go` (`WriteResult`) and `result.go`
(`NormalizeVerdict`).

**Create** `internal/agent/tools/{tools.go,files.go,run.go,result.go,guard.go,env.go,redact.go,interfaces.go}`,
a `_test.go` for each, and `moq_test.go`.

```go
// Package tools is the runner's built-in tool set (ADR 0065): file tools
// confined to one directory, a shell through internal/gate, the verdict,
// and the scrub and redaction rules of ADR 0066.
package tools

const MaxResult = 16 << 10

// Tool is one tool: its schema for the model and its implementation.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
	// Call runs the tool with the model's raw JSON arguments. A returned
	// error is shown to the model as "error: <err>"; Result.Done ends the run.
	Call func(ctx context.Context, arguments string) (Result, error)
}

type Result struct {
	Text      string
	Truncated bool
	Done      bool   // record_result succeeded
	Verdict   string // with Done
}

type Config struct {
	Dir      string   // the session directory; paths are relative to it
	Root     string   // the workspace directory; paths must stay inside it
	Branch   string   // the role's BRANCH, for the push guard; "" for none
	Verdicts []string // allowed verdicts, pass then fail
	Recorder recorder
	Shell    shellRunner // a gate.Runner built with Environ's output
}

// New returns read_file, list_files, search, write_file, edit_file, run,
// record_result, in that order.
func New(cfg Config) []Tool

// Environ is environ without secrets (ADR 0066): names matching
// WS_ENDPOINT_TOKEN_*, WS_FORGEJO_TOKEN_*, *_API_KEY, *_TOKEN, *_SECRET,
// *_PASSWORD, OTEL_EXPORTER_OTLP_HEADERS, OTEL_EXPORTER_OTLP_*_HEADERS, or
// in secretNames (the names in secrets.env), except
// GH_TOKEN and GITHUB_TOKEN when keepGit; WS_RUNNER=ws is set.
func Environ(environ []string, keepGit bool, secretNames []string) []string
// Secrets is the values Environ removed, for the redactor.
func Secrets(environ []string, keepGit bool, secretNames []string) []string

// CheckPath resolves p against dir, following symlinks of the existing
// part, and fails unless the result is inside root and not under any .git.
func CheckPath(root, dir, p string) (string, error)
// CheckCommand refuses: a word "ws" not followed by "machine result";
// "claude"; "sudo"; "git push" with -f/--force/--force-with-lease to a ref
// other than branch; "git push" to main or master. Words: strings.Fields
// after replacing ;&|() with spaces.
func CheckCommand(command, branch string) error

// Redactor replaces registered values with "[redacted]". Safe for
// concurrent use; values shorter than 4 bytes are ignored.
type Redactor struct{ /* mu, values (longest first) */ }
func NewRedactor(values ...string) *Redactor
func (r *Redactor) Add(values ...string)
func (r *Redactor) Redact(s string) string
```

`interfaces.go`: `recorder` (`Record(ctx, verdict string, report []byte)
error`) and `shellRunner` (`*gate.Runner`'s `Run`).

**Tool behaviour** (descriptions one sentence each):

- `read_file {path (required), offset, limit}`: `N\t<text>` lines, ≤ 400
  per call; `[file has M lines; use offset to read more]` when more;
  binary (a NUL in the first 8 KiB) → `binary file`.
- `list_files {path, glob}`: `git ls-files --cached --others
  --exclude-standard -- <path>` through `Shell` (so tracked plus untracked,
  not ignored, as ADR 0065 says), filtered by `glob` (`path.Match` on the
  path and on its base name); ≤ 500 sorted paths, then `[N more not
  shown]`. Outside a git repo: an error `not a git repository`.
- `search {pattern (required), path, glob}`: Go `regexp` over text files,
  `path:line: text` (lines cut at 200 chars), ≤ 100 matches.
- `write_file {path, content}`: parents 0o755, file 0o644, `wrote N bytes
  to path`.
- `edit_file {path, old, new (required), all}`: `old` exactly once (`old
  occurs K times; add context or set all`) or ≥ once with `all`;
  `replaced K occurrence(s) in path`.
- `run {command (required), timeout_seconds}`: `CheckCommand`, then
  `Shell.Run(gate.Request{Script, Dir: cfg.Dir, Timeout: 120s default, 900s
  max})`. Answer `exit <code>`, `--- stdout ---`, `--- stderr ---`, each
  the last 8 KiB with `[truncated]`; a timeout → `timed out after Ns`.
- `record_result {verdict, report (required)}`: `machine.NormalizeVerdict`,
  in `Verdicts` (else an error listing them), `Recorder.Record`,
  `Result{Done: true, Verdict}`.
- Arguments decode with `json.Unmarshal` into a struct per tool; a missing
  required field → `missing <field>`. (The loop checks JSON validity and
  required fields against `Parameters` before `Call`, so those count as
  invalid calls, T11; the tool's own check is a second line.) Export
  `func Validate(schema map[string]any, arguments string) error` here
  (required fields present, top-level types `string`/`integer`/`boolean`
  right) for the loop to use. Every result is cut to `MaxResult`
  with `[truncated: N more bytes]`.

**Acceptance** (table tests where three or more simple cases):

- `CheckPath`: inside → ok; `../x`, a symlink to outside, `.git/config`,
  `sub/.git/HEAD` → error; a new file in a new directory → ok.
- `CheckCommand`: `ws list`, `ws machine spawn x`, `claude logs`, `sudo
  ls`, `git push origin main`, `git push -f origin feat/y` (branch
  `feat/x`), `make test && ws rm a` → refused. `ws machine result PASS
  --file ../r.md`, `git push -f -u origin feat/x`, `go test ./...` →
  allowed.
- `Environ`: drops `WS_ENDPOINT_TOKEN_GPU`, `OPENAI_API_KEY`, `FOO_SECRET`,
  `NPM_TOKEN`, `DB_PASSWORD`, `METRICS_TOKEN`, `OTEL_EXPORTER_OTLP_HEADERS`
  and `OTEL_EXPORTER_OTLP_TRACES_HEADERS`, and `MY_VAR` when it's in
  `secretNames`; keeps `GH_TOKEN` with `keepGit`, drops it without; keeps
  `HOME`, `PATH`, `METRICS_URL`, `OTEL_EXPORTER_OTLP_ENDPOINT`; sets
  `WS_RUNNER=ws`. `Secrets` returns the dropped values.
- `Redactor`: redacts every registered value, longest first
  (`abcdef` and `abc` registered, `xabcdefx` → `x[redacted]x`); ignores
  values under 4 bytes; concurrent `Add`/`Redact` under `-race`.
- Each tool's success path and errors against `t.TempDir()`; `run` with
  `shellRunnerMock` (asserts `Dir`, `Timeout`, the 900 cap) and once with
  the real `gate.Runner` (`echo hi`); `record_result` rejects `MAYBE` and
  calls the mock once with `PASS`.

**Verify**

```console
$ go generate ./internal/agent/tools/
$ go test -race ./internal/agent/tools/
$ make lint
```

---

## T6. `internal/llm`: the provider seam, errors, preflight

**Depends on:** nothing. **Parallel with:** T1, T3–T5, T8.
**ADRs:** [0062](../../adrs/0062-agent-runner-built-into-ws.md) (the seam
and the normalization table),
[0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md) (preflight).

**Read:** ADR 0062's Decision; reference.md
[spawn preflight](reference.md#ws-machine-spawn-new-flags) and
[errors](reference.md#errors-and-exit-codes).

**Create** `internal/llm/{llm.go,errors.go,preflight.go,interfaces.go}`,
tests and `moq_test.go`. This package imports no SDK.

```go
// Package llm is the runner's provider seam (ADR 0062): the neutral
// conversation types the loop uses, and the Provider interface each wire
// protocol implements.
package llm

type Provider interface {
	Chat(ctx context.Context, req Request) (Response, error)
	Models(ctx context.Context) ([]string, error)
	Probe(ctx context.Context, model string) (ModelInfo, error) // ErrProbeUnsupported when the API can't tell
}

// Endpoint is where and how to reach a server; Token is never logged.
type Endpoint struct {
	Name, URL, API, Probe, Token string
	RequestTimeout, IdleTimeout  time.Duration
}

type Request struct {
	Model       string
	System      string
	Messages    []Message
	Tools       []Tool
	MaxOutput   int
	Temperature float64
	Reasoning   string // "" (server default) | none | low | medium | high (ADR 0064)
	TraceParent string // W3C traceparent, or ""; T13's decorator sets it from its span when telemetry runs
}

type Role string
const (RoleUser Role = "user"; RoleAssistant Role = "assistant"; RoleTool Role = "tool")

type Message struct {
	Role            Role
	Content         string
	ToolCalls       []ToolCall // assistant
	ToolCallID      string     // tool
	ToolName        string     // tool; for protocols keyed by name
	Reasoning       string     // shown in transcripts; not sent back by providers that don't need it
	ReasoningOpaque *Opaque    // returned verbatim by the provider that produced it
}
type Opaque struct{ Provider string; Data []byte }
type ToolCall struct{ ID, Name, Arguments string } // Arguments: JSON text as sent
type Tool struct{ Name, Description string; Parameters map[string]any }

type StopReason string
const (StopEnd StopReason = "end"; StopToolCalls StopReason = "tool_calls"; StopMaxTokens StopReason = "max_tokens")

type Usage struct{ InputTokens, OutputTokens, CacheReadTokens, CacheWriteTokens int; Reported bool }
type Response struct {
	Message    Message
	StopReason StopReason
	Usage      Usage
	Duration   time.Duration
	SpanID     string // the request's chat span, set by T13's decorator; "" without telemetry
}

type ModelInfo struct {
	Version       string   // the server's, when probed
	Capabilities  []string // e.g. completion, tools, thinking, vision
	ContextLength int      // 0 when unknown
}

// PreflightResult is what a successful preflight learned.
type PreflightResult struct {
	Models        []string
	Version       string   // "" without a probe
	Capabilities  []string // nil without a probe
	ContextTokens int      // min(configured, the model's when known)
	Warnings      []string
}

// Preflight: Models answers (UnreachableError otherwise), model is listed
// (ModelMissingError), and Probe, unless ErrProbeUnsupported, has "tools"
// (NoToolsError); a smaller context length lowers contextTokens with a
// warning. Each call gets a 10 s limit.
func Preflight(ctx context.Context, p Provider, ep Endpoint, model string, contextTokens int) (PreflightResult, error)

// TraceParent is "00-<traceID>-<16 random hex>-01".
func TraceParent(traceID string) string
// NewTraceID is 32 random lowercase hex digits.
func NewTraceID() string
```

`errors.go`: sentinels `ErrUnreachable`, `ErrRetryable`,
`ErrStreamDropped`, `ErrStreamStalled`, `ErrModelMissing`, `ErrNoTools`,
`ErrContextExceeded`, `ErrProbeUnsupported`; types `UnreachableError{URL,
Cause}`, `ModelMissingError{Model; Models []string}`,
`NoToolsError{Model; Capabilities []string}`, `RetryableError{Cause error;
RetryAfter time.Duration}`, each with `Error`, `Unwrap` or `Is` so
`errors.Is` (sentinel) and `errors.As` both work; `func Retryable(err
error) (bool, time.Duration)` true for `ErrRetryable`, `ErrStreamDropped`,
`ErrStreamStalled`. `interfaces.go` generates `ProviderMock` for this
package's tests (`//go:generate go tool moq -out moq_test.go . Provider`);
other packages declare the small interface they use and mock that.

**Acceptance** (with `ProviderMock`):

- `Preflight`: `Models` error → `UnreachableError` wrapping it; missing
  model → `ModelMissingError` with the list; probe without `tools` →
  `NoToolsError`; `ErrProbeUnsupported` → ok, nil capabilities; context
  32768 against 65536 → 32768 and one warning; each call's context has a
  deadline ≤ 10 s.
- `errors.Is` and `errors.As` work for each type; `Retryable` gives the
  `RetryAfter` of a wrapped `RetryableError`.
- `TraceParent(NewTraceID())` matches `^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`.

**Verify**

```console
$ go generate ./internal/llm/
$ go test ./internal/llm/
$ make lint
```

---

## T7. `internal/llm/openai`: the OpenAI-compatible provider, and `internal/llm/providers`

**Depends on:** T1 (golden fixtures), T4 (`config.ErrAPINotImplemented`), T6. **Parallel with:** T2, T9, T11, T16.
**ADRs:** [0062](../../adrs/0062-agent-runner-built-into-ws.md) (the
`openai` column of the table),
[0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md) (`probe`).

**Spawn with** `llm-router@9789c32` as a second, read-only repo spec.

**Read:** llm-router `internal/agent/agent.go` and `interfaces.go`
(`completeStreaming`, `reasoningOf`, `WrapError`); this repo's
`internal/llm/llm.go`, `errors.go`; `internal/fakeollama/testdata/README.md`.

**Add** `github.com/openai/openai-go/v3 v3.71.1`.

**Create** `internal/llm/openai/{provider.go,stream.go,probe.go,errors.go,interfaces.go}`
with tests and `moq_test.go`; `internal/llm/providers/providers.go` with a
test.

```go
// Package openai is the runner's provider for OpenAI-compatible chat
// completions (ADR 0062): Ollama's /v1, llm-router, LM Studio, vLLM. It is
// the only package that requests chat/completions. The stream accumulator
// and reasoning handling are copied from llm-router internal/agent @9789c32.
package openai

func New(ep llm.Endpoint, httpClient *http.Client, log *slog.Logger) *Provider
func (p *Provider) Chat(ctx context.Context, req llm.Request) (llm.Response, error)
func (p *Provider) Models(ctx context.Context) ([]string, error)   // GET /v1/models
func (p *Provider) Probe(ctx context.Context, model string) (llm.ModelInfo, error)
```

```go
// Package providers builds the Provider an endpoint's api names.
package providers

// New returns the provider for ep.API ("" or "openai"); a reserved name is
// config.ErrAPINotImplemented, anything else an error.
func New(ep llm.Endpoint, httpClient *http.Client, log *slog.Logger) (llm.Provider, error)
```

**Behaviour**

- `Chat`: `openai.NewClient(option.WithBaseURL(ep.URL+"/v1"),
  option.WithAPIKey(token, or "ws" when empty), option.WithHTTPClient(…),
  option.WithMaxRetries(0))` (retries belong to the loop). Streams with
  `stream_options.include_usage`; sends `System` as the first `system`
  message, tools as function tools, `max_tokens`, `temperature`; never
  `tool_choice`. Assistant messages sent back carry their tool calls and no
  reasoning; tool messages carry `tool_call_id`. `traceparent` header when
  set. `reasoning_effort` = `req.Reasoning` only when it isn't `""`. A gap
  over `IdleTimeout` → `ErrStreamStalled`; end without `[DONE]`
  → `ErrStreamDropped`. Reasoning from `reasoning` or `reasoning_content`.
  `finish_reason` `stop`/`tool_calls`/`length` → `end`/`tool_calls`/`max_tokens`.
  Usage from `prompt_tokens`/`completion_tokens`, `Reported` when sent.
- Errors: 400 containing `does not support tools` → `NoToolsError`; 404 →
  `ModelMissingError`; 400 mentioning context length → `ErrContextExceeded`;
  429/502/503/504, reset or refused → `RetryableError` with `Retry-After`;
  others wrapped with the server's message.
- `Probe` by `ep.Probe`: `ollama` → `GET /api/version`, `POST /api/show`
  (capabilities, `*.context_length` from `model_info`); `none` →
  `ErrProbeUnsupported`; `auto` → `/api/version` once per `Provider`, then
  as `ollama` if it answered, else `none`. Plain `net/http`, bearer token.
- Warning (once, in the log) when a token goes over plain `http://` to a
  non-loopback host (ADR 0066).

**Acceptance** (`httptest` servers replaying
`internal/fakeollama/testdata/*.response.*`; no process, no network):

- `stream-tools` → one `ToolCall` (id, name, arguments), `tool_calls`,
  usage; `stream-reasoning` → `Reasoning` set; `stream-no-usage` →
  `Usage.Reported` false; `models.response.json` → the ids.
- The request body has `include_usage: true`, no `tool_choice`, the system
  prompt first, `"model": "qwen/qwen3.5-9b"` byte-for-byte;
  `reasoning_effort: "none"` when `Reasoning` is `none` and no
  `reasoning_effort` key when it's `""`; `Authorization: Bearer t` for
  token `t`; `traceparent` when set. A streamed reply with
  `reasoning_content` deltas and one with `reasoning` deltas both fill
  `Reasoning`.
- `Models` returns ids containing `/` and `:` unchanged.
- Mid-stream close → `ErrStreamDropped`; a chunk then silence over a 100 ms
  `IdleTimeout` → `ErrStreamStalled`; 503 `Retry-After: 7` → `Retryable`
  true, 7 s; 400 `{"error":"m does not support tools"}` → `ErrNoTools`;
  404 → `ErrModelMissing`.
- `Probe`: `ollama` parses capabilities and context length; `auto` against
  a server without `/api/version` → `ErrProbeUnsupported`, and asks only
  once.
- `providers.New` for `openai` and `""` → `*openai.Provider`; `anthropic`
  → `errors.Is(err, config.ErrAPINotImplemented)`.
- `make lint` gains, and passes: `@! grep -rn 'chat/completions' --include='*.go' . | grep -vE '^\./internal/(llm/openai|fakeollama)/' | grep -v '_test\.go:'`.

**Verify**

```console
$ go generate ./internal/llm/openai/
$ go test ./internal/llm/...
$ make lint
```

---

## T8. A `model` for every agent; `.Runner` and `.Model` in role templates

**Depends on:** nothing. **Parallel with:** T1, T3–T6.
**ADRs:** [0063](../../adrs/0063-runner-children-are-supervised-sessions.md)
("Every agent reports its model"),
[0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md) (Prompts).

This is the data half of the review's "mark every agent with its model":
Claude agents get `runner` and `model` now; T15 adds runner sessions and
T24 shows the label.

**Read:** `internal/claude/transcript.go` (`readTail`, `transcriptEntry`,
`transcriptPath`), `internal/lifetime/lifetime.go` (`Session`),
`internal/lifetime/judge.go` (where a Claude session is built),
`internal/lifetime/interfaces.go`, `internal/cli/list.go`
(`listSessionJSON`), `internal/cli/machine.go` (`machineSpawnJSON`),
`internal/cli/machine_wait.go` (`machineWaitItemJSON`),
`internal/machine/role/role.go` (`Render`),
`internal/machine/role/roles/implementer.md`.

**Change**

- `internal/claude/transcript.go`: `func (c *Client) Model(s *Session)
  (string, error)`: the `message.model` of the last assistant entry in the
  transcript tail, `""` when there is none (a missing transcript is `"",
  nil`). `transcriptEntry` gains `Message.Model`.
- `internal/lifetime`: `claudeClient` gains `Model`; regenerate mocks.
  `Session` gains `Runner string` (`"claude"` for agents, `""` for
  shells) and `Model string`. `judge` fills both for Claude agents; a
  `Model` error is ignored (logged at debug), leaving `""`.
- `internal/cli/list.go`: `listSessionJSON` gains, after `stale`, `Runner
  *string `json:"runner"``, `Model *string `json:"model"`` and `WSRunner
  *listWSRunnerJSON `json:"ws_runner"`` (always `null` in this task; T15
  defines the struct's fields, here an empty struct type). `null` for
  empty values.
- `internal/cli/machine.go`: `machineSpawnJSON` gains `Runner string
  `json:"runner"`` (`"claude"`), `Model *string `json:"model"`` (the
  `--model` value the child was started with, from the role or the claude
  args, else `null`) and `WSRunner *spawnWSRunnerJSON `json:"ws_runner"``
  (`null`).
- `internal/cli/machine_wait.go` and `children`: each item gains `runner`,
  `model`, `ws_runner` (`null`) from the live session; a gone child gets
  `null` for all three.
- `internal/machine/role/role.go`: template data gains `Runner` and
  `Model`. New `RenderFor(vars map[string]string, extra, runner, model
  string)`; `Render` calls it with `"claude"` and `""`. `implementer.md`'s
  trailer becomes `{{if eq .Runner "ws"}}Generated-By: ws runner
  ({{.Model}}){{else}}Co-Authored-By: Claude Haiku 5.5
  <noreply@anthropic.com>{{end}}`.
- `internal/cli/guides/ws-list.md`: after the `session.stale` bullet:
  ``- `session.runner`: `claude` or `ws` for an agent, `null` for a shell.
  `session.model`: the agent's model (`claude-sonnet-5-5`, `qwen/qwen3.5-9b`),
  `null` for a shell or an agent that hasn't answered yet.
  `session.ws_runner`: details for a `ws` child (see `ws llm machine
  wait`), otherwise `null`.``, and the JSON example gains the three fields.

**Acceptance**

- `Model` on a fixture transcript whose last assistant entry has `model:
  "claude-sonnet-5-5"` → that; no assistant entry → `""`; no file → `""`,
  nil.
- `ws list --json` (mocks) shows `"runner": "claude", "model":
  "claude-sonnet-5-5", "ws_runner": null` for an agent and three `null`s
  for a shell; all other fields byte-identical to before.
- A spawn with `--role reviewer` whose role sets `model: sonnet` prints
  `"runner": "claude", "model": "sonnet", "ws_runner": null`; without any
  `--model`, `"model": null`.
- Rendering `implementer` with `Render` is byte-identical to before (golden
  test); `RenderFor(…, "ws", "qwen/qwen3.5-9b")` ends with
  `Generated-By: ws runner (qwen/qwen3.5-9b)`.
- `TestGuideTopics` passes.

**Verify**

```console
$ go generate ./internal/lifetime/ ./internal/cli/
$ go test -p 2 ./internal/claude/ ./internal/lifetime/ ./internal/machine/...
$ go test ./internal/cli/ -run 'TestApp_.*List|TestApp_.*Machine|TestGuideTopics'
$ make lint
```

---

## T9. `internal/mcp`: server definitions, expansion, names, filter

**Depends on:** nothing. **Parallel with:** T2, T7, T11, T16.
**ADR:** [0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)
(Where servers come from, Naming, the annotation rule),
[0066](../../adrs/0066-runner-security-boundaries.md) items 6–7.

**Read:** reference.md [MCP servers](reference.md#mcp-servers);
`internal/claude/session.go` for how `CLAUDE_CONFIG_DIR` is resolved
today (ADR 0024 item 7). Look at the shape of your own `~/.claude.json`
`mcpServers` with `jq 'del(..|.env?, .headers?) | .mcpServers | map_values(keys)'`
only; never copy a value into a test.

**Create** `internal/mcp/{defs.go,expand.go,names.go,filter.go}` with
tests and `testdata/` (hand-written `.mcp.json` and `claude.json` files with
fake values). Pure Go; no SDK, no processes.

```go
// Package mcp is the runner's MCP client (ADR 0069).
package mcp

// ServerDef is one server definition, Claude Code's shape.
type ServerDef struct {
	Name    string
	Source  string // "project" or "user"
	Type    string // "stdio" (default when Command is set), "http", "sse"; others skipped
	Command string
	Args    []string
	Env     map[string]string
	URL     string
	Headers map[string]string
}

// ClaudeConfigPath is $CLAUDE_CONFIG_DIR/.claude.json when set, else
// $HOME/.claude.json.
func ClaudeConfigPath(environ []string) string
// LoadDefs reads <repoDir>/.mcp.json (project) and claudeJSON's top-level
// mcpServers (user); project wins on a name. Unsupported types (ws, and
// definitions with an "oauth" block) are left out with a warning. Missing
// files are fine; an unparsable one is an error naming the file.
func LoadDefs(repoDir, claudeJSON string) (map[string]ServerDef, []string, error)
// Select returns the defs names lists, in that order; an unknown name is a
// warning, not an error.
func Select(defs map[string]ServerDef, names []string) ([]ServerDef, []string)

// Expand applies ${VAR} and ${VAR:-default} to Command, Args, Env, URL and
// Headers from getenv. It returns the expanded def and every value it
// substituted plus every literal Headers value (for the redactor). An
// unset ${VAR} without a default is *UnsetVarError naming VAR.
func Expand(d ServerDef, getenv func(string) (string, bool)) (ServerDef, []string, error)
type UnsetVarError struct{ Server, Var string }

// ToolName is mcp__<server>__<tool>, characters outside [A-Za-z0-9_-]
// replaced by "_"; over 64 characters: the first 55, "_", and 8 hex digits
// of SHA-256 of the full name.
func ToolName(server, tool string) string

// ToolInfo is one tool a server offers.
type ToolInfo struct {
	Server, Tool, Name, Description string // Name = ToolName(Server, Tool)
	Schema                          map[string]any
	ReadOnly, Destructive           bool // annotations
}
// Filter keeps tools matching allow (exact names or path.Match globs on
// Name), minus deny; a tool that isn't ReadOnly, or is Destructive, only
// when allow names it exactly. Order: allow's order, then the server's.
func Filter(tools []ToolInfo, allow, deny []string) []ToolInfo
```

**Acceptance**

- `LoadDefs` with both files: project `metrics` beats user `metrics`;
  `type` defaults to `stdio` with `command`; a `type: "ws"` server and one
  with `oauth` are left out with warnings; no files → empty, nil.
- `ClaudeConfigPath` with and without `CLAUDE_CONFIG_DIR`.
- `Expand`: `${A}`, `${B:-x}` (unset → `x`), `$A` (unchanged; Claude only
  expands braces), in every field; the returned secrets include each
  substituted value and the literal `Authorization` header; unset `${C}` →
  `UnsetVarError{Var: "C"}` whose message has no value in it.
- `ToolName("metrics-a", "query_logs")` = `mcp__metrics-a__query_logs`;
  `("my server", "a.b")` = `mcp__my_server__a_b`; a 70-character result is
  64 long and stable.
- `Filter`: `mcp__g__query_*` allows read-only `query_x`, not
  non-read-only `query_y`; listing `mcp__g__query_y` exactly allows it;
  `deny` removes after allow; `["*"]` allows only read-only tools.

**Verify**

```console
$ go test ./internal/mcp/
$ make lint
```

---

## T10. `internal/mcp`: the client (stdio, streamable HTTP, SSE)

**Depends on:** T3, T9. **Parallel with:** T13, T17.
**ADR:** [0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)
(Starting and calling).

**Read:** `internal/mcp/defs.go`, `names.go`, `filter.go` (types only);
`internal/fakemcp/server.go` (`NewServer`); the SDK's `mcp.Client`,
`ClientSession.ListTools`/`CallTool`, `mcp.CommandTransport`,
`mcp.StreamableClientTransport`, `mcp.SSEClientTransport` (if present),
`mcp.NewInMemoryTransports` (`go doc`).

**Create** `internal/mcp/{client.go,result.go,manager.go,interfaces.go}`
with tests and `moq_test.go`.

```go
// StartOptions is how servers are started for one run.
type StartOptions struct {
	Dir     string        // stdio servers' working directory (the session directory)
	Environ []string      // the runner's unscrubbed environment (ADR 0068)
	Stderr  io.Writer     // runner.log
	Timeout time.Duration // initialize + list tools; 30 s
	HTTP    *http.Client
}

// Server is one started server.
type Server struct{ /* def name, session, tools, state */ }
// Start expands def, connects (stdio: own process group; http; sse), and
// lists tools. The error never contains an expanded value.
func Start(ctx context.Context, def ServerDef, opts StartOptions) (*Server, []string, error) // []string: secrets for the redactor
func (s *Server) Tools() []ToolInfo
// Call runs one tool with JSON arguments, 120 s limit (callTimeout field).
func (s *Server) Call(ctx context.Context, tool, arguments string) (CallResult, error)
func (s *Server) Close() error // kills a stdio server's group

type CallResult struct{ Text string; IsError bool }

// Status is one server's line in status.json's mcp.
type Status struct {
	Server string  `json:"server"`
	State  string  `json:"state"` // starting, connected, failed
	Tools  int     `json:"tools"`
	Loaded int     `json:"loaded"`
	Error  *string `json:"error"`
}

// Manager starts a run's servers in parallel and routes calls by tool name.
type Manager struct{ /* servers by name, tools by offered name */ }
func StartAll(ctx context.Context, defs []ServerDef, opts StartOptions, log *slog.Logger) (*Manager, []string)
func (m *Manager) Tools() []ToolInfo            // every connected server's tools
func (m *Manager) Call(ctx context.Context, name, arguments string) (CallResult, error) // by offered name
func (m *Manager) Status() []Status
func (m *Manager) Close() error
```

- `result.go`: text contents joined with `\n`; image and audio →
  `[image omitted: this runner has no vision]`; resource links → their
  URI; embedded text resources → their text. `isError` → `IsError`.
- A server that fails to start is `failed` with its error, and its tools
  are absent; `Call` on a dead server returns an error and marks it
  `failed`; it isn't restarted.
- `sse`: if the SDK has no SSE client transport, `Start` returns an error
  `sse transport not supported by this build` (state `failed`).
- The stdio `exec.Cmd` is built here only; add a lint rule:
  `@! grep -rn 'mcp.CommandTransport' --include='*.go' . | grep -vE '^\./internal/mcp/'`.

**Acceptance** (in-process `fakemcp` servers over in-memory transports,
plus one real stdio test that runs `go run ./cmd/fake-mcp` built once into
`t.TempDir()`, and one HTTP test on `httptest` with `RequireHeaders`):

- Tools listed with annotations mapped; `Call` returns text; an `error`
  reply → `IsError`; an image → the placeholder.
- stdio with `require_env`: started with the variable in `Environ` →
  connected; without → failed, and the error names the variable only.
- HTTP with `Authorization: Bearer ${T}` and `T` in `Environ` → connected;
  the returned secrets contain the token.
- `StartAll` with one good and one `slow_initialize: 5s` server, 200 ms
  timeout → one `connected`, one `failed`; total time under 1 s.
- `crash_after_calls: 1` → the second `Call` errors and `Status` says
  `failed`. `Close` leaves no child process (check with the pid).

**Verify**

```console
$ go generate ./internal/mcp/
$ go test -p 2 ./internal/mcp/
$ make lint
```

---

## T11. `internal/agent`: the loop (copied), budgets, compaction, transcript, preamble, project instructions

**Depends on:** T5, T6. **Parallel with:** T2, T7, T9, T16.
**ADRs:** [0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md)
(all), [0062](../../adrs/0062-agent-runner-built-into-ws.md) (the copy),
[0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)
(project instructions).

**Spawn with** `llm-router@9789c32` as a second, read-only repo spec.

**Read:** llm-router `internal/agent/agent.go` (`Run`, `execute`) and
`tools.go`; this repo's `internal/llm/llm.go` and
`internal/agent/tools/tools.go` (types only); the README's
[agent loop](README.md#the-agent-loop) and preamble; reference.md
[transcript](reference.md#stateidrunnertranscriptjsonl) and
[exit reasons](reference.md#runner-exit-reasons).

**Create** `internal/agent/{agent.go,budget.go,compact.go,transcript.go,prompt.go,preamble.md,instructions.go,toolset.go,interfaces.go,moq_test.go}`
and a `_test.go` for each `.go` file. `agent.go`'s doc comment: `Copied
from llm-router internal/agent @9789c32 (ADR 0062); maintained here.` The
copy keeps the loop's shape; the OpenAI specifics went to T7.

```go
//go:generate go tool moq -out moq_test.go . chatter clock Toolset

// chatter is llm.Provider's Chat.
type chatter interface {
	Chat(ctx context.Context, req llm.Request) (llm.Response, error)
}
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Toolset is what the loop offers and calls. T11 has StaticToolset; T12
// adds MCP tools and tool_search.
type Toolset interface {
	Definitions() []llm.Tool // for this request
	Call(ctx context.Context, name, arguments string) (tools.Result, error)
	Known(name string) bool
}
// StaticToolset offers ts unchanged.
func StaticToolset(ts []tools.Tool) Toolset

const (
	ExitDone = "done"; ExitMaxSteps = "max_steps"; ExitTimeout = "timeout"
	ExitMaxTokens = "max_tokens"; ExitEndpointDown = "endpoint_down"
	ExitModelNoTools = "model_no_tools"; ExitModelMissing = "model_missing"
	ExitMalformed = "malformed_tool_calls"; ExitNoResult = "no_result"
	ExitLooping = "looping"; ExitContextOverflow = "context_overflow"
	ExitStopped = "stopped"; ExitCrashed = "crashed"; ExitInternal = "internal"
)

type Config struct {
	Model         string
	ContextTokens int
	MaxOutput     int
	Temperature   float64
	Reasoning     string // passed to llm.Request.Reasoning
	Budget        Budget
	Tools         Toolset
	System        string // rendered preamble + project instructions
	Task          string // rendered role prompt (first user message)
	TraceID       string
	Stop          func() bool  // true once `stop` exists
	ResultWritten func() bool  // true once result.toml exists (ws machine result via run)
	OnStatus      func(Status) // on every state change
}
type Budget struct{ MaxSteps int; Timeout time.Duration; MaxTokens int }
type Status struct{ State string; Step int; Usage Usage } // working | waiting_model | exited
type Usage struct {
	Requests         int `json:"requests"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
type Outcome struct{ Exit, Message, Verdict string; Usage Usage; Steps int }

func New(c chatter, clk clock, tr *Transcript, cfg Config, log *slog.Logger) *Agent
// Run starts, or resumes from history (non-empty), until an exit reason.
// It never returns an error: every failure is an Outcome.
func (a *Agent) Run(ctx context.Context, history []llm.Message, step int) Outcome
```

**Loop**, in this order per step (ADR 0065):

1. `Stop()` → `ExitStopped`; `ResultWritten()` → `ExitDone` (verdict
   read by the caller); a used-up budget → `ExitTimeout`, `ExitMaxSteps`
   or `ExitMaxTokens`.
2. `Compact`; if it can't fit → `ExitContextOverflow`.
3. `Chat` with `TraceParent(TraceID)`. `llm.Retryable` → wait 2 s, 8 s,
   30 s (or `Retry-After` if longer, ≤ 60 s), a `retry` event each; after
   3 → `ExitEndpointDown`. `ErrNoTools` → `ExitModelNoTools`;
   `ErrModelMissing` → `ExitModelMissing`; `ErrContextExceeded` → force a
   compaction once, then `ExitContextOverflow`; other errors →
   `ExitEndpointDown` with the message.
4. Add usage. `prompt_tokens` under 80% of the estimate → `truncated`
   event; the second time, lower the working limit to it.
5. No tool call → user message `Call a tool. When the task is done, call
   record_result.` (a `nudge` event, `reason` `no_tool_call`); when the
   reply's content contains `<tool_call>`, the message is instead `Your
   tool call came back as text. Call the tool through the tools API.`
   (`reason` `text_tool_call`). A reply with only reasoning and
   `StopMaxTokens` is a reply with no tool call. Either way, the third in
   a row → `ExitNoResult`.
6. Per call: unknown name (`!Known`), invalid JSON, or
   `tools.Validate` failing against the tool's schema →
   tool message `error: <what>; the arguments must match: <compact
   schema>` and `invalid_call`; three in a row → `ExitMalformed`. Valid →
   `Call`, result sent as `<tool_result tool="NAME">…</tool_result>`. The
   same name and arguments as the previous two → also the nudge `You
   already did this; its result is above. Do something else.`; the fifth
   → `ExitLooping`.
7. `Result.Done` → `ExitDone` with the verdict.

**Compaction** (`compact.go`), byte estimate `len/3`:

```go
func EstimateTokens(lastPrompt, bytesSince int) int // lastPrompt + ceil(bytesSince/3)
// Compact fits messages under limit (contextTokens - maxOutput): above 70%,
// elide tool results outside the last 6 messages, oldest first, to under
// 50%; still above 70%, drop whole assistant+tool turns after the task and
// insert one user message "Progress so far (written by ws, not by you):"
// listing written/edited paths and run commands with exit codes. ok=false
// when the task and last turn alone don't fit.
func Compact(msgs []llm.Message, system string, estimate, limit int, done []ToolRecord) ([]llm.Message, CompactionEvent, bool)
type ToolRecord struct{ Name, Summary string; ExitCode *int }
type CompactionEvent struct{ BeforeTokens, AfterTokens, Elided, DroppedTurns int }
```

Elided text: `[elided by ws: <tool> <first 80 chars of the arguments>, <N>
lines; run it again if you need it]`.

**Transcript** (`transcript.go`): `OpenTranscript(path string, r
*tools.Redactor) (*Transcript, error)` (append, 0o600); `Write(ev Event)
error` redacts every string field through `r` before marshalling;
`Close`. `Event` is `Time`, `Step`, `Kind` plus one payload struct per
kind (`StartEvent`, `RequestEvent`, … `ExitEvent`), each marshalled with
exactly reference.md's columns for that kind: no `omitzero` on payload
fields, so `"ok": false`, `"truncated": false`, `"resume": false` and the
`null`s (`usage`, `verdict`, `tool`, `span_id`) are written; nullable
fields are pointers. `start` carries `Config.TraceID` as `trace_id` and
`Config.Reasoning` as `reasoning`; `response` carries `Response.SpanID`
as `span_id` (`null` when empty). `messages` and `tools` are lists in `start` and counts in
`request`, so they live in different payload types. Writing flattens the
payload into the event object; `Tail` decodes by `kind`. `ReadHistory(path)
([]llm.Message, int, error)` rebuilds the conversation for `--resume`
(start's task, responses as assistant messages with `ReasoningOpaque`
dropped when its provider differs from the current one, tool results,
invalid calls and nudges as sent; and the last step). `Tail(path, n)
([]Event, bool, error)`.

**Prompt** (`prompt.go` + `preamble.md`, `//go:embed`): the README's
preamble text verbatim.

```go
type PreambleData struct {
	Dir                    string
	StepsLeft, MinutesLeft int
	Verdicts               string
	MCP                    bool   // MCP tools are offered
	Deferred               string // "server: a, b, c" lines, ≤ 2 KiB (T12 fills it)
	Unavailable            []string // MCP servers that failed to start
	Instructions           string
}
func Preamble(d PreambleData) (string, error)
```

**Project instructions** (`instructions.go`):

```go
// LoadInstructions reads CLAUDE.md then AGENTS.md at repoDir and, when
// different, workspaceDir; then, when userFile != "", that file. Joined
// with "\n\n", cut at 8 KiB with "[instructions cut at 8 KiB by ws]".
// @path imports are left as text.
func LoadInstructions(repoDir, workspaceDir, userFile string) (string, error)
```

**Acceptance** (`chatterMock`, `clockMock`; no network):

- Happy path: `write_file` → `run` → `record_result PASS` → `Outcome{Exit:
  "done", Verdict: "PASS", Steps: 3}`; transcript `start`, 3
  request/response pairs, 3 `tool_result`s, `exit`, in order.
- One scripted test per exit reason: `max_steps`, `timeout`, `max_tokens`,
  `endpoint_down` (sleeps 2 s, 8 s, 30 s), recovery after 2 retryable
  errors, `model_no_tools`, `model_missing`, `context_overflow`,
  `malformed_tool_calls`, recovery after one malformed call, `no_result`
  (once with plain text, once with `<tool_call>{…}</tool_call>` as text,
  which gets the `text_tool_call` nudge, and once with only reasoning and
  `max_tokens`),
  `looping` (nudge after the third), `stopped`, and `done` via
  `ResultWritten`.
- `Compact` elides oldest first keeping the last 6, then drops turns and
  inserts the note; `ok=false` for an oversized task;
  `EstimateTokens(1000, 300)` = 1100. The limit drops after two
  `truncated` events.
- `ReadHistory` of a run's transcript equals the next request's messages;
  `Write` redacts a registered secret in a tool result and in reasoning.
- `Preamble` renders every field; with `MCP` false the MCP lines are
  absent. `LoadInstructions`: order, the 8 KiB cut, missing files skipped,
  the user file only when given.

**Verify**

```console
$ go generate ./internal/agent/
$ go test ./internal/agent/...
$ make lint
```

---

## T12. MCP tools in the loop: the toolset and `tool_search`

**Depends on:** T10, T11. **Parallel with:** T13, T23.
**ADR:** [0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md)
(Deferral).

**Read:** `internal/agent/toolset.go`, `prompt.go`; `internal/mcp/client.go`,
`manager.go`, `filter.go` (types only); reference.md [MCP limits](reference.md#mcp-servers).

**Create** `internal/mcp/search.go` (+ test) and extend
`internal/agent/toolset.go` (+ test).

```go
// Search ranks tools by case-insensitive word overlap of query with Name
// (split on _ and -), then Description; ties keep input order; at most n.
func Search(tools []ToolInfo, query string, n int) []ToolInfo

// in internal/agent:
// mcpCaller is *mcp.Manager's Call.
type mcpCaller interface {
	Call(ctx context.Context, name, arguments string) (mcp.CallResult, error)
}
// NewToolset offers builtin, the first upfront of allowed, and, when
// len(allowed) > upfront, tool_search. Loaded tools stay; past 24 MCP
// tools the least recently called loaded one is dropped. onSearch gets
// each search for the transcript.
func NewToolset(builtin []tools.Tool, allowed []mcp.ToolInfo, upfront int, mc mcpCaller, onSearch func(query string, loaded, dropped []string)) *MCPToolset
// Deferred is the preamble's "server: tool, tool" lines for the tools not
// offered up front, cut at 2 KiB.
func (t *MCPToolset) Deferred() string
```

- `tool_search {query: string (required)}`, description `Find more tools
  by a few words; matching tools become callable.` Answer: up to 5 lines
  `name: first line of description`, or `no tools match "<query>"`.
- An MCP call: `IsError` → `error: <text>`; a Go error → `error: <err>`
  (the loop logs `mcp_error`); results get the 16 KiB cap and the
  `<tool_result tool="NAME" server="S">` framing.
- `Known` is true for built-ins, `tool_search`, and offered MCP tools; a
  deferred tool not yet loaded is unknown (the error says to call
  `tool_search`).

**Acceptance**

- 40 allowed, `upfront` 8 → `Definitions` has 7 built-ins + 8 + `tool_search`;
  `tool_search {"query":"loki logs"}` loads `query_loki_logs` first; the
  next `Definitions` includes it; `Deferred` lists the rest by server
  under 2 KiB.
- 8 or fewer allowed → no `tool_search`. 30 loaded → 24 MCP definitions,
  the least recently called dropped and reported to `onSearch`.
- A call to a deferred, unloaded tool → not `Known`. MCP results framed
  and capped.

**Verify**

```console
$ go test ./internal/mcp/ ./internal/agent/...
$ make lint
```

---

## T13. `internal/telemetry`: OpenTelemetry setup and the GenAI decorators

**Depends on:** T6, T11. **Parallel with:** T10, T12, T17.
**ADR:** [0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md)
(Observability), [0066](../../adrs/0066-runner-security-boundaries.md)
item 8.

**Spawn with** `llm-router@9789c32` as a second, read-only repo spec.

**Read:** llm-router `internal/telemetry/telemetry.go`, `conventions.go`,
`docs/adrs/0019-telemetry-setup.md` (Decision), and how it records a chat
attempt in `internal/router/attempt.go` and `internal/router/metrics.go`;
this repo's `internal/llm/llm.go`, `internal/llm/errors.go`,
`internal/agent/toolset.go` and `agent.go` (where `Toolset.Call` is
called; types only otherwise).

**Add** the OpenTelemetry modules named in the rules above.

**Create** `internal/telemetry/{telemetry.go,conventions.go,run.go,provider.go,toolset.go}`
with a `_test.go` each. `telemetry.go`'s doc comment: `Copied from
llm-router internal/telemetry @9789c32 (ADR 0065); maintained here.` Copy
`New`'s provider and exporter setup (`exporterEnv`, `exportConfigured`,
`startOpenTelemetry`, `withExporters`, the `otelslog` bridge and its
level handler); leave out llm-router's `LOG_FORMAT`/`LOG_LEVEL` handling:
the logger is always `slog` JSON to the given writer.

```go
// Package telemetry is the runner's OpenTelemetry setup and the
// decorators that instrument the provider seam and the toolset (ADR 0065).
// It is the only package that imports go.opentelemetry.io.
package telemetry

const ServiceName = "ws-runner" // OTEL_SERVICE_NAME overrides it

// ProviderName is gen_ai.provider.name for an endpoint's api:
// "openai" → "openai_compatible" (as llm-router); "ollama" → "ollama".
func ProviderName(api string) string

type Telemetry struct {
	Logger *slog.Logger
	// unexported: tracer, meter, propagator, instruments, shutdown funcs
}
// New: SDK tracer and meter providers always; exporters (autoexport) and
// the otelslog log bridge only when an OTEL_EXPORTER_OTLP_*ENDPOINT or
// OTEL_*_EXPORTER variable is set (read through getenv). Logs go to w as
// slog JSON either way.
func New(ctx context.Context, w io.Writer, getenv func(string) string) (*Telemetry, error)
func (t *Telemetry) Shutdown(ctx context.Context) error // flushes; safe without exporters

// RunInfo names one process run of an agent.
type RunInfo struct{ Role, ModelID, WorkspaceID, Session string }
// StartRun starts the run's "invoke_agent <role or ws>" INTERNAL span as a
// child of a remote span context whose trace id is traceID (32 hex), so
// every process run of one agent shares a trace. end records
// ws.runner.exit on the span, adds 1 to ws_runner.runs, and ends it.
func (t *Telemetry) StartRun(ctx context.Context, traceID string, info RunInfo) (context.Context, func(exit string))

// Provider wraps p: each Chat is a "chat <model>" CLIENT span whose
// context is injected as req.TraceParent (replacing any), with the GenAI
// attributes of ADR 0065; it records gen_ai.client.operation.duration and
// gen_ai.client.token.usage, and sets Response.SpanID.
func (t *Telemetry) Provider(p llm.Provider, ep llm.Endpoint) llm.Provider
// Toolset wraps ts: each Call is an "execute_tool <name>" INTERNAL span
// with gen_ai.tool.name, gen_ai.tool.call.id (agent.ToolCallID(ctx)), and
// ws.mcp.server = serverOf(name) when not "".
func (t *Telemetry) Toolset(ts agent.Toolset, serverOf func(name string) string) agent.Toolset
// newForTest builds a Telemetry on given providers (in-memory exporters).
func newForTest(tp trace.TracerProvider, mp metric.MeterProvider, w io.Writer) *Telemetry
```

`conventions.go`: `AttrWorkspaceID = "ws.workspace.id"`, `AttrSession =
"ws.runner.session"`, `AttrExit = "ws.runner.exit"`, `AttrMCPServer =
"ws.mcp.server"`, the counter name `ws_runner.runs` with attribute
`ws_runner.exit`; instruments from `semconv/v1.39.0` and `genaiconv`
(`NewClientOperationDuration`, `NewClientTokenUsage`). `error.type` on a
failed `chat` span is one of `unreachable`, `retryable`, `stream_dropped`,
`stream_stalled`, `model_missing`, `no_tools`, `context_exceeded`, `other`
(by `errors.Is` on the `llm` sentinels).

**Never content**: no attribute, event, metric attribute or exported log
record may hold `Request.System`, a message's content or reasoning, tool
definitions, arguments, results or `Response` text. Only names, ids,
counts, durations, finish reasons and error classes.

**In `internal/agent`** (one small change): `func WithToolCallID(ctx
context.Context, id string) context.Context` and `func ToolCallID(ctx
context.Context) string`, and the loop calls `Toolset.Call` with
`WithToolCallID(ctx, call.ID)`.

Lint rule: `@! grep -rn '"go.opentelemetry.io/' --include='*.go' . | grep -vE '^\./internal/telemetry/'`.

**Acceptance** (in-memory span exporter and manual metric reader through
`newForTest`; no network):

- `exportConfigured` (copied) is true for each of the seven variables and
  false for none; `New` with none set returns a working `Telemetry` whose
  spans have valid trace ids, and `Shutdown` returns nil.
- `StartRun(ctx, "4bf92f3577b34da6a3ce929d0e0e4736", …)` → the span's trace
  id is that, its name `invoke_agent implementer` (`invoke_agent ws`
  without a role); two calls share the trace; `end("done")` sets
  `ws.runner.exit` and counts one `ws_runner.runs`.
- `Provider` around a `ProviderMock`: span `chat qwen/qwen3.5-9b`, kind
  client, `gen_ai.operation.name` `chat`, `gen_ai.provider.name`
  `openai_compatible`, `gen_ai.request.model`, `gen_ai.usage.input_tokens`
  and `output_tokens`, `gen_ai.response.finish_reasons`, `server.address`
  and `server.port` from the endpoint URL; the inner request's
  `TraceParent` is `00-<trace id>-<this span's id>-01`; `Response.SpanID`
  is the span id; an `llm.ErrStreamDropped` → `error.type`
  `stream_dropped` and status error; both GenAI metrics recorded, token
  usage with `gen_ai.token.type` `input` and `output`.
- `Toolset` around a mock: span `execute_tool read_file` with
  `gen_ai.tool.call.id` from the context; an MCP name gets `ws.mcp.server`.
- **No content**: a run of `Provider` and `Toolset` with the marker
  `SECRET-CONTENT-7f3a` in the system prompt, a message, reasoning, tool
  arguments and the tool result exports no span, event, metric or log
  record containing it.
- `make lint` passes with the new rule. Report the size of `go build
  -o /tmp/ws ./cmd/ws` before and after this task (the binary links
  `internal/telemetry` once T14 imports it; measure with a blank import in
  a scratch build if needed, and say which).

**Verify**

```console
$ go mod tidy && go test ./internal/telemetry/ ./internal/agent/...
$ make lint
```

---

## T14. `internal/runner`, session kind `runner`, hidden `ws runner run`

**Depends on:** T4, T7, T12, T13. **Parallel with:** none.
**ADRs:** [0063](../../adrs/0063-runner-children-are-supervised-sessions.md)
(including "Without a parent"),
[0065](../../adrs/0065-runner-agent-loop-tools-and-budgets.md) (Exit
reasons, Observability), [0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md).

**Read:** `internal/telemetry/telemetry.go`, `run.go` (signatures only),
`internal/workspace/session.go`, `internal/machine/store.go`
(`WriteResult`, root and ancestors as `ws machine result` computes them in
`internal/cli/machine_result.go`), `internal/cli/app.go` (`AddCommand`),
`internal/proc/proc.go`, reference.md [Files](reference.md#files).

**Create** `internal/runner/{files.go,state.go,finalize.go,ids.go,interfaces.go}`
with tests and `moq_test.go`; `internal/cli/runner.go` and
`runner_test.go`.

```go
// Package runner holds a runner session's files and the rules for reading
// them (ADR 0063). internal/daemon starts and stops the processes.
package runner

const SessionPrefix = "rn-"
func NewSessionID() string // "rn-" + 8 random lowercase hex

// Run is run.toml (reference.md), including MCPServers, MCPTools, MCPDeny,
// MCPUpfront, UserInstructions, ModelAlias, API, MaxOutput, Temperature,
// Reasoning, Parent ("" for none) and TraceID.
type Run struct{ /* every key in reference.md's run.toml, toml tags */ }
// Status is status.json; Exit and ExitMessage are null while running.
type Status struct {
	Version          int          `json:"version"`
	Session          string       `json:"session"`
	PID              int          `json:"pid"`
	ProcessStartedAt time.Time    `json:"process_started_at"`
	State            string       `json:"state"`
	Step             int          `json:"step"`
	MaxSteps         int          `json:"max_steps"`
	Restarts         int          `json:"restarts"`
	Usage            agent.Usage  `json:"usage"`
	MCP              []mcp.Status `json:"mcp"`
	Exit             *string      `json:"exit"`
	ExitMessage      *string      `json:"exit_message"`
	Verdict          *string      `json:"verdict"`
	UpdatedAt        time.Time    `json:"updated_at"`
}
// Usage is usage.json under state/<root>/machine/<child>/ (reference.md).
type Usage struct{ /* fields as reference.md, json/v2 */ }

type Files struct{ Dir string } // state/<id>/runner
func FilesFor(stateDir, id string) Files
func (f Files) WriteRun(r *Run) error
func (f Files) ReadRun() (Run, error)
func (f Files) WriteStatus(s *Status) error // temp + rename
func (f Files) ReadStatus() (Status, error) // fs.ErrNotExist when missing
func (f Files) Transcript() string
func (f Files) Log() string
func (f Files) RequestStop() error
func (f Files) StopRequested() bool
// UsagePath is state/<root>/machine/<id>/usage.json for a run with a
// parent, else state/<id>/runner/usage.json.
func (f Files) UsagePath(run Run, store resultStore) string
// Record writes a verdict and report: through store (as `ws machine
// result`) when run.Parent != "", else to runner/report.md; either way it
// sets status.json's verdict. The runner's record_result and Finalize
// both use it.

// State is ADR 0063's table: working, starting, idle, stopped, unknown.
func (f Files) State(now, recordedAt time.Time, procs processReader) (state string, pid int, st *Status)

// Finalize ends a run that has no verdict from the model: unless exit is
// "stopped", it records the role's first fail verdict (FAIL without a
// role) with the report "ws runner: <exit>\n\n<message>\n\n## Last events\n"
// plus the last 20 transcript events as a JSON-lines block; it writes
// usage.json and the final status.json. Used by `ws runner run` and, for
// "crashed", by the daemon.
func Finalize(ctx context.Context, f Files, run Run, exit, message string, out FinalizeInput, store resultStore) error

//go:generate go tool moq -out moq_test.go . processReader resultStore
type processReader interface{ StartTime(pid int) (time.Time, error) }
type resultStore interface{ /* machine.Store's WriteResult and paths */ }
```

**`internal/workspace/session.go`**: `SessionRunner SessionKind =
"runner"`; `Session` gains `RunnerSessionID string
`toml:"runner_session_id,omitempty"``; `RecordRunnerSession(id,
runnerSessionID string) error` writes `version = 2`; `check` accepts
`runner` with a `runner_session_id` starting `rn-`; versions up to 2 are
read; a higher one is "written by a newer ws".

**`ws runner run [--resume] <id>`** (`internal/cli/runner.go`, `Hidden:
true`; `TestGuideTopics` skips hidden commands, add the skip if it
doesn't):

1. read `run.toml`; write `status.json` (`starting`, pid, start time from
   `internal/proc`), keeping `restarts` from the previous status;
2. build: the provider (`providers.New` with the token from
   `WS_ENDPOINT_TOKEN_<NAME>` in its own environment); the redactor
   (token, `tools.Secrets`); the tools (`tools.Environ(os.Environ(),
   keepGit, secretNames)` where `secretNames` is
   `WS_RUNNER_SECRET_NAMES`, a comma list the daemon sets (T16)); MCP
   (`mcp.LoadDefs` + `Select(run.MCPServers)` + `StartAll` with the
   unscrubbed environment, secrets added to the redactor,
   `Filter(manager.Tools(), MCPTools, MCPDeny)`, `NewToolset`); the
   transcript; the system prompt (`Preamble` + `LoadInstructions`, with
   `~/.claude/CLAUDE.md` only when `run.UserInstructions`);
   `StartAll`'s result is written as one `mcp_start` event per server
   (`transport`, `ok`, `tools`, `allowed`, `error`) and, for a failed one,
   an `mcp_error` event with `tool: null`; failed servers go into
   `PreambleData.Unavailable`; telemetry: `telemetry.New(ctx,
   runnerLog, os.Getenv)` (its `Logger` is the runner's logger),
   `StartRun(ctx, run.TraceID, RunInfo{run.Role, run.Model, id,
   run.Session})`, the provider wrapped with `tel.Provider(p, ep)` and the
   toolset with `tel.Toolset(ts, serverOf)` (`serverOf` from the MCP
   manager); `end(exit)` and `Shutdown` (5 s limit) before the process
   exits;
3. `agent.Run` (`ReadHistory` and a `restart` event for `--resume`),
   `OnStatus` → `WriteStatus` with `manager.Status()`, each server's
   `loaded` taken from the toolset (`MCPToolset.LoadedByServer()
   map[string]int`, added here);
   the trace id is created by `ws machine spawn` into `run.toml`
   (`trace_id`, T19) so it survives `--resume`, and goes to
   `agent.Config.TraceID`, `StartRun`, the `restart` event and
   `usage.json`; `run.Reasoning` goes to `agent.Config.Reasoning`;
4. on exit: `Finalize` unless the model's verdict was recorded; exit 0.
   Exit 1 only when `run.toml` can't be read or `status.json` can't be
   written.

**docs/state.md**: `runner/` (run.toml, status.json, transcript.jsonl,
runner.log, stop, and for a run without a parent report.md and
usage.json), `machine/<child>/usage.json`.

**Acceptance**

- `Files.State`: no status, recorded 3 s ago → `starting`; 20 s →
  `stopped`; `working` with a live pid whose start time matches →
  `working`; same pid, other start time → `stopped`; `exit` set → `idle`,
  pid 0; `restarts` grew within 3 min, no `exit`, dead pid → `starting`;
  invalid JSON → `unknown`.
- `Finalize` writes the fail verdict, report header, `usage.json` and
  `exit`, and sets `status.json`'s `verdict`; for `stopped` it writes no
  verdict. With `parent = ""` the report is `runner/report.md` and
  `usage.json` is in `runner/`; nothing is written under `machine/`.
- Session records round-trip; version 3 → "written by a newer ws"; `kind
  = "runner"` without `runner_session_id` → error.
- `ws runner run` against an `httptest` server replaying T7's fixtures and
  an in-process fake MCP: a 3-step happy path writes `result.toml` PASS,
  `usage.json`, and `exit "done"`; a 503-forever endpoint records `FAIL`
  with a report starting `ws runner: endpoint_down` (fast backoff via an
  unexported clock); a `--resume` continues at the next step. No token or
  MCP header value appears in `transcript.jsonl` or `runner.log`.
- With `OTEL_TRACES_EXPORTER=console` in the test's environment, the
  spans the run prints all have `run.toml`'s `trace_id`, include one
  `invoke_agent`, three `chat` and three `execute_tool` spans, and none
  contains the task prompt's text; `--resume` adds a second
  `invoke_agent` span in the same trace. Without `OTEL_*` variables
  nothing is printed.

**Verify**

```console
$ go generate ./internal/runner/
$ go test -p 2 ./internal/runner/ ./internal/workspace/
$ go test ./internal/cli/ -run 'TestApp_.*Runner|TestGuideTopics'
$ make lint
```

---

## T15. `lifetime` reads runner sessions; `runner`/`model`/`ws_runner` in `ws list`, `children`, `wait`; `ws machine logs`

**Depends on:** T8, T14. **Parallel with:** T18.
**ADR:** [0063](../../adrs/0063-runner-children-are-supervised-sessions.md).

**Read:** `internal/lifetime/{interfaces.go,judge.go,remove.go,restore.go}`
(the switches on `SessionShell` or `ClaudeSessionID`),
`internal/cli/list.go`, `machine_wait.go`, `machine.go`
(`runMachineChildren`), `internal/runner/state.go`, reference.md
[JSON output](reference.md#json-output), [guides/ws-machine-logs.md](guides/ws-machine-logs.md).

**Change**

- `internal/lifetime/interfaces.go`: new `runnerReader` (regenerate mocks):
  ```go
  // runnerReader reads runner sessions (ADR 0063); stopping and resuming
  // go through the daemon (T19).
  type runnerReader interface {
  	State(id string, recordedAt time.Time) (state string, pid int, st *runner.Status, run *runner.Run)
  	Exists(id string) bool // state/<id>/runner exists
  }
  ```
  `Manager.UseRunners(r runnerReader)`; without it, `runner` sessions
  read as `unknown` and are kept.
- `judge`: `SessionRunner` → `Session{Kind: KindAgent, ID: runner id,
  State, PID, Runner: "ws", Model: run.Model, WSRunner: st+run}`; gone only
  when `!Exists`. `lifetime.Session` gains `WSRunner *RunnerInfo` (alias,
  endpoint, step, max steps, restarts, usage, MCP, exit).
- `internal/cli/list.go`: `listWSRunnerJSON` gets reference.md's fields
  (`model_alias`, `endpoint`, `step`, `max_steps`, `restarts`, `usage`,
  `mcp`, `exit`); wire `UseRunners` where the Manager is built.
- `wait` items: top-level `runner: "ws"`, `model`, and `ws_runner`
  (`model_alias`, `endpoint`, `exit`, `steps`, `restarts`, `usage`,
  `wall_seconds`, `usage_file`); for a gone child, from `usage.json` when
  it exists. `children` gets nothing new at the top level: its items
  embed `ws list`'s object, so `session.runner`, `session.model` and
  `session.ws_runner` come from the change above.
- **New** `internal/cli/machine_logs.go`: `ws machine logs [--tail N]
  <workspace>` with `wait`'s caller and descendant checks. The argument
  is a workspace id or name, or a session id (`rn-…` or a Claude
  session id); a removed child is accepted by id while
  `state/<id>/runner/` (or its Claude session) still exists, else
  `workspace_not_found`. `ws` child →
  `agent.Tail`; Claude child → `a.claude.Logs(ctx, session)` (add `Logs`
  to `internal/claude` running `claude logs <id>`, tested with
  `exectest`, and to the cli `claudeClient`; regenerate mocks).
- Guides: `cp docs/design/ollama-agent/guides/ws-machine-logs.md
  docs/design/ollama-agent/guides/ws-machine-wait.md internal/cli/guides/`;
  topic `"machine logs"` after `"machine children"` in
  `guideTopicsBeforeReview`; in `guides/ws.md` the bullet
  `` - `ws llm machine logs`: print a child's log: a `ws` child's
  transcript, or a Claude child's `claude logs` `` after `machine
  children`, and in "Rules for agents" "follow it with `claude logs
  <session>`." becomes "follow it with `ws machine logs <id>`.";
  `guides/ws-machine.md` gains `ws machine logs` in its synopsis and list.
  `docs/tools/ws-list.md`: the three `session` fields.

**Acceptance**

- Lifetime with `runnerReaderMock`: `List` shows `kind agent`, `runner
  ws`, the model and the mock's state; a workspace is gone only when
  `Exists` is false. Without `UseRunners`: `unknown`, kept.
- `ws list --json` for a runner workspace matches reference.md's
  `session`; Claude agents unchanged from T8.
- `wait` on a finished runner child → `outcome`, `runner`, `model`,
  `ws_runner.exit`, `usage_file`; on a removed one, the same from
  `usage.json`.
- `ws machine logs` → reference.md's shape for both runners; `--tail 2`
  sets `truncated`; a session id finds the same child; a removed child
  whose state remains is readable; `not_descendant` for a sibling.
- `TestGuideTopics` and `go test ./internal/cli/` pass.

**Verify**

```console
$ go generate ./internal/lifetime/ ./internal/cli/ ./internal/claude/
$ go test -p 2 ./internal/lifetime/ ./internal/claude/
$ go test ./internal/cli/
$ make lint
```

---

## T16. `secrets.env` and the runner's environment

**Depends on:** T4. **Parallel with:** T2, T7, T9, T11.
**ADRs:** [0068](../../adrs/0068-ws-daemon.md) (Environment and runtime
configuration), [0066](../../adrs/0066-runner-security-boundaries.md) item 1.

**Read:** `internal/config/runner.go` (`Runner.Env`, `TokenEnv`),
reference.md [secrets.env](reference.md#secretsenv-and-the-runners-environment).

**Create** `internal/config/secrets.go` (+ test) and
`internal/daemon/env.go` (+ test). (`internal/daemon` is created here with
only this file, without a package doc comment; T17 adds the rest and the
doc comment.)

```go
// in internal/config:
const SecretsFile = "secrets.env"

// ReadSecrets parses CONFIG_DIR/secrets.env: NAME=value lines, # comments,
// blank lines, optional double quotes, no expansion. A missing file is
// empty. A mode looser than 0600 ignores the file and returns a warning.
// A bad line is an error naming the line number, never its value.
func ReadSecrets(configDir string) (map[string]string, []string, error)
// SetSecret writes or replaces NAME's line (temp file, rename, 0600),
// keeping other lines and comments. An empty value removes the line.
func SetSecret(configDir, name, value string) error

// in internal/daemon:
// RunnerEnv builds a runner's environment, lowest precedence first: base
// (the daemon's own, captured at start), cfg.Env, secrets, then
// WS_RUNNER=ws, WS_WORKSPACE=<id>, WS_CONFIG_DIR=<configDir> and
// WS_RUNNER_SECRET_NAMES (the secrets' names, comma-separated, for the
// runner's scrub). Later layers replace earlier values of the same name.
func RunnerEnv(base []string, cfg config.Runner, secrets map[string]string, workspaceID, configDir string) []string
```


**Acceptance**

- `ReadSecrets`: quotes stripped, comments and blanks skipped,
  `A=b=c` → `b=c`; mode 0644 → empty plus a warning; a line without `=` →
  an error with the line number and without the line's text.
- `SetSecret` adds, replaces (keeping comments and order) and removes; the
  file is 0600.
- `RunnerEnv`: `[runner.env]` beats the base, secrets beat
  `[runner.env]`, `ws`'s own variables beat all; `WS_RUNNER_SECRET_NAMES`
  is sorted.

**Verify**

```console
$ go test ./internal/config/ ./internal/daemon/
$ make lint
```

---

## T17. `internal/daemon` core and `ws daemon run|status|stop`

**Depends on:** T16 (it adds to `internal/daemon`, which T16 creates). **Parallel with:** T10, T13.
**ADR:** [0068](../../adrs/0068-ws-daemon.md) (Lifecycle, The socket, How
`ws` reaches it).

**Read:** `internal/lockfile/lockfile.go`, `internal/claude/daemon.go`
(how Claude Code's service is started detached: `Setsid`, `/dev/null`),
`internal/cli/app.go`, `internal/cli/errors.go`, `internal/cli/llm.go`
(`guideTopics`), reference.md [ws daemon](reference.md#ws-daemon-run) and
[The daemon socket](reference.md#the-daemon-socket), [guides/](guides/)
`ws-daemon*.md`.

**Create** `internal/daemon/{paths.go,lock.go,server.go,client.go,autostart.go,peercred_linux.go,peercred_darwin.go,interfaces.go}`
with tests and `moq_test.go`; `internal/cli/daemon.go` and
`daemon_test.go`.

```go
// Package daemon is the ws daemon (ADR 0068): one process per CONFIG_DIR
// on a user-only Unix socket that supervises runners.
package daemon

// Paths are a CONFIG_DIR's daemon files.
type Paths struct{ Dir, Lock, Info, Log, Socket string }
// PathsFor: CONFIG_DIR/daemon/{lock,daemon.json,daemon.log}; the socket is
// $XDG_RUNTIME_DIR/ws/<h>.sock, else ${TMPDIR:-/tmp}/ws-<uid>/<h>.sock,
// <h> = first 12 hex of SHA-256(abs CONFIG_DIR).
func PathsFor(configDir string, getenv func(string) string, uid int) Paths

type Info struct { // daemon.json
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Origin    string    `json:"origin"` // transient | managed | app
	Socket    string    `json:"socket"`
	WSVersion string    `json:"ws_version"`
}

// Supervisor is what the server serves; T18 implements it. Until then
// NopSupervisor answers status with no runners and runner requests with
// ErrNotSupervising.
type Supervisor interface {
	Runners() []RunnerStatus
	Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error)
	Start(ctx context.Context, id string, resume bool) (int, error)
	Stop(ctx context.Context, id string) error
	StopAll(ctx context.Context, keep bool) (stopped, kept []string)
	Idle() bool // no live runner
}

// Serve takes the lock (ErrRunning with the holder's pid if held), writes
// daemon.json, prepares the socket directory (0700, owned by uid) and
// socket (0600), checks every connection's peer uid, and serves /v1/*
// until ctx ends, a shutdown request, or, for transient, 15 min of Idle()
// and no request. For app, it polls os.Getppid() every 2 s; once the
// parent pid differs from the one at start (the app quit), it logs
// "parent gone; acting as transient" and applies the transient rule.
func Serve(ctx context.Context, p Paths, origin string, sup Supervisor, log *slog.Logger) error

// Client talks to a running daemon.
func Dial(p Paths) *Client
func (c *Client) Status(ctx context.Context) (Status, error) // ErrNotRunning when nothing answers
func (c *Client) Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error)
func (c *Client) StartRunner(ctx context.Context, id string, resume bool) (int, error)
func (c *Client) StopRunner(ctx context.Context, id string) error
func (c *Client) Shutdown(ctx context.Context, keepRunners bool) (ShutdownResult, error)

// Ensure returns a client to a running daemon, starting `<exe> daemon run
// --origin transient` detached (own session, stdio /dev/null, stderr
// appended to daemon.log, home as working directory) when none answers
// and autostart is true, and waits up to 5 s. ErrUnavailable otherwise.
// A daemon whose ws_version is older than minVersion is ErrUnavailable
// with "run ws daemon stop --keep-runners".
func Ensure(ctx context.Context, p Paths, exe string, autostart bool, minVersion string) (*Client, error)

var (ErrRunning, ErrNotRunning, ErrUnavailable, ErrNotSupervising error)
```

Wire format: HTTP/1.1 over the socket, JSON (json/v2), errors as
`{"error":{"code","message","details"}}`; requests and bodies as
reference.md's table. The server never takes a command, path or
environment in a request.

**`ws daemon`** (`internal/cli/daemon.go`): `run [--origin
transient|managed|app]` (default `managed`; `--json` refused; `ErrRunning` →
`daemon_running`, exit 4, `details.pid`), `status [--json]` (never
starts one; not running → `running: false`, exit 0), `stop
[--keep-runners] [--json]` (not running → `stopped: false`, exit 0; not
gone within 30 s → `daemon_unavailable`). New codes in `errors.go`:
`daemon_unavailable` (1, `details.socket`, `details.log`), `daemon_running`
(4). `WS_DAEMON_AUTOSTART=0` makes `Ensure` callers pass `autostart =
false`.

Guides: `cp docs/design/ollama-agent/guides/ws-daemon*.md
internal/cli/guides/`; topics `"daemon"`, `"daemon run"`, `"daemon
status"`, `"daemon stop"` after `"config"` in `guideTopics`;
`guides/ws.md` gains the four `ws llm daemon…` bullets (under the commands
for people) and the two codes in the exit-code table (row 1:
`daemon_unavailable`; row 4: `daemon_running`). `docs/tools/ws-daemon.md`:
a reference page made from the guides, linked from `docs/tools/README.md`.

**Acceptance** (sockets under `t.TempDir()`; `XDG_RUNTIME_DIR` set to a
temp dir):

- `PathsFor` with and without `XDG_RUNTIME_DIR`; two config dirs → two
  sockets.
- `Serve` then a second `Serve` → `ErrRunning` with the first pid;
  `daemon.json` written; socket 0600, directory 0700; a directory owned by
  someone else (simulate with a mode check) → error.
- Client `Status` against `Serve` with `NopSupervisor`; `Shutdown` ends
  `Serve`; a transient daemon with a 100 ms idle limit (unexported field)
  exits by itself; a managed one doesn't; an `app` one with an unexported
  parent-pid function that changes value exits after the idle limit, and
  doesn't while it stays the same.
- `Ensure` with `autostart=false` and nothing running → `ErrUnavailable`;
  with a test `exe` script that runs a fake server → a client within 5 s.
- A client whose daemon answers an older `ws_version` than it needs →
  `ErrUnavailable` with "run ws daemon stop --keep-runners"; `ws daemon
  status` shows that as a warning.
- `ws daemon status --json` with nothing running → reference.md's
  not-running object, exit 0. `ws daemon run --json` → `usage`.
  `TestGuideTopics` passes.

**Verify**

```console
$ go generate ./internal/daemon/
$ go test -p 2 ./internal/daemon/
$ go test ./internal/cli/ -run 'TestApp_.*Daemon|TestGuideTopics'
$ make lint
```

---

## T18. The daemon supervises runners; preflight and `ws runner check`

**Depends on:** T14, T16, T17. **Parallel with:** T15.
**ADRs:** [0068](../../adrs/0068-ws-daemon.md) (Supervision),
[0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md) (Preflight).

**Read:** `internal/daemon/server.go` (`Supervisor`), `env.go`;
`internal/runner/files.go`, `state.go`, `finalize.go`;
`internal/llm/preflight.go`; `internal/llm/providers/providers.go`;
`internal/proc/proc.go`; `internal/claude/daemon.go` (detached start).

**Create** `internal/daemon/{supervisor.go,process.go,preflight.go}` with
tests; add `ws runner check` to `internal/cli/runner.go`; `serve` in
`internal/cli/daemon.go` uses the real supervisor.

```go
// NewSupervisor watches every runner session under stateDir.
func NewSupervisor(cfg SupervisorConfig, procs processReader, starter processStarter, log *slog.Logger) *Supervisor
type SupervisorConfig struct {
	StateDir, ConfigDir, Exe string
	BaseEnv                  []string        // os.Environ() at daemon start
	LoadConfig               func() (config.Config, error) // re-read at each start
	Poll                     time.Duration   // 2 s
	Backoff                  []time.Duration // 5 s, 30 s, 2 min
	ResumeOnStart            bool
}
// Reconcile runs once before Serve listens: adopt live runners, resume
// stopped ones (when ResumeOnStart), leave finished ones.
func (s *Supervisor) Reconcile(ctx context.Context) error
// Watch polls until ctx ends: a dead runner with no exit is resumed after
// Backoff[restarts]; past len(Backoff) it is finalized as "crashed".
func (s *Supervisor) Watch(ctx context.Context)

//go:generate go tool moq -out moq_test.go . processReader processStarter
type processStarter interface {
	// Start runs exe with args in its own session, stdin/stdout /dev/null,
	// stderr appended to logPath, working directory dir, environment env.
	Start(exe string, args []string, dir, logPath string, env []string) (pid int, err error)
	Signal(pgid int, sig syscall.Signal) error
}
```

- **Start** (`POST /v1/runners/<id>/start`): `state/<id>/runner/run.toml`
  must exist (`workspace_not_found`); environment from `RunnerEnv` with
  `config.ReadSecrets` read now; `ws runner run [--resume] <id>` in the
  session directory; wait ≤ 10 s for `status.json` → else `runner_failed`
  with `details.log`.
- **Stop**: write `stop`, `SIGTERM` the process group if `status.json`'s
  pid is alive with a matching start time, wait 10 s, `SIGKILL`.
- **StopAll(keep=false)**: `SIGTERM` without writing `stop`; `keep=true`:
  nothing, list them.
- **Restarts**: before a resume, increment `restarts` in `status.json`; a
  fourth death → `runner.Finalize(…, "crashed", …)`.
- **Preflight** (`POST /v1/preflight`): `LoadConfig().Resolve(role,
  endpoint, model)`, `providers.New` with the token from the built
  environment, `llm.Preflight`; returns reference.md's `ws runner check`
  object (`token_source`: `env`, `secrets.env` or `null`).
- **`ws runner check [--json] [--model <m>] [<endpoint>]`** (hidden): asks
  the daemon (`Ensure`) to preflight and prints the object. Defaults
  (reference.md): no `<endpoint>` → the endpoint of `runner.model`'s
  alias; no `--model` → the first alias (sorted by name) whose endpoint is
  that endpoint, else `runner.model`. `PreflightRequest` gains `Check
  bool` to select these rules instead of `Resolve`'s role order.
- **`ws runner stop [--json] <workspace-id>`** (hidden, for the desktop's
  Stop): `Ensure`, then `StopRunner`; prints `{"id", "session",
  "stopped", "warnings"}` (reference.md). The `stop` request answers
  `{"stopped": false}` when `status.json` already has `exit`. No runner
  session in that workspace → `workspace_not_found` (3).
- **Warnings** in `GET /v1/status` and `ws daemon status`: `secrets.env`
  ignored because its mode is looser than 0600 (from `ReadSecrets`), and
  any `config.toml` error met at the last runner start.
- **Error codes** (this task declares them, because `ws runner check` and
  the daemon's answers use them): `endpoint_unknown` (3),
  `endpoint_unreachable` (1), `model_not_served` (3), `model_without_tools`
  (1), `runner_failed` (1) in `internal/cli/errors.go`, mapping
  `config.ErrEndpointUnknown` (`details.endpoints`),
  `config.ErrNoModel`/`ErrAPINotImplemented` (→ `config_invalid`),
  `llm.UnreachableError`, `ModelMissingError`, `NoToolsError` and the
  daemon's error objects with reference.md's `details`. `guides/ws.md`'s
  exit-code table gains them (row 1: `endpoint_unreachable`,
  `model_without_tools`, `runner_failed`; row 3: `endpoint_unknown`,
  `model_not_served`).
- Lint rule: `runner run` is started only from `internal/daemon/`:
  `@! grep -rn '"runner", "run"' --include='*.go' . | grep -vE '^\./internal/(daemon|cli)/' | grep -v '_test\.go:'`
  (`internal/cli` defines the command).

**Acceptance** (`processStarterMock`, `processReaderMock`, temp state
dirs; one test with real processes using a test `exe` script):

- `Reconcile`: live → adopted; `stopped` → resumed with `--resume`
  (`ResumeOnStart`) or left; finished → left.
- `Watch`: a dead runner resumes after 5 s (fake clock), then 30 s, 2 min;
  the fourth death finalizes `crashed` with a FAIL verdict and
  `usage.json`.
- `Start` builds the environment in the documented order and passes
  `WS_RUNNER_SECRET_NAMES`; a starter whose process writes no
  `status.json` → `runner_failed` within 10 s (100 ms in tests).
- `Stop` writes `stop` and signals; on a finished runner it returns
  `stopped: false` and signals nothing; `StopAll(false)` signals without
  `stop`. `ws runner stop` on a mock daemon prints the object; a
  workspace without a runner session → `workspace_not_found`.
- Preflight through a real socket against an `httptest` OpenAI server →
  the check object; a closed port → `endpoint_unreachable` with `url`.

**Verify**

```console
$ go generate ./internal/daemon/
$ go test -p 2 ./internal/daemon/
$ go test ./internal/cli/ -run 'TestApp_.*Runner|TestApp_.*Daemon|TestGuideTopics'
$ make lint
```

---

## T19. `ws machine spawn --runner`, `ws rm`/`ws restore` through the daemon, guides and docs

**Depends on:** T15, T18. **Parallel with:** none.
**ADRs:** [0064](../../adrs/0064-choosing-the-runner-model-and-endpoint.md),
[0063](../../adrs/0063-runner-children-are-supervised-sessions.md),
[0068](../../adrs/0068-ws-daemon.md) (How `ws` reaches it, Migration).

**Read:** `internal/cli/machine.go` (`newMachineSpawnCmd`,
`runMachineSpawn`, `spawnRole`), `internal/cli/claude.go` (`runClaude`,
`startAgent`), `internal/cli/errors.go`, `internal/lifetime/remove.go`,
`restore.go`, `internal/daemon/client.go`, reference.md (Commands, spawn
JSON, errors), [guides/ws-machine-spawn.md](guides/ws-machine-spawn.md),
the README's [proposed diff](README.md#proposed-changes-to-docstoolsws-machinemd).

**Change**

- Error codes: use the ones T18 declared; add nothing to `errors.go`
  except the new `usage` messages.
- `machine.go`: flags `--runner` and `--endpoint`. The runner is the flag,
  else `cfg.RunnerFor(role)`. For `ws`, the new `runWSChild(ctx, opts)` in
  **new** `internal/cli/machine_runner.go`, in reference.md's order:
  1. `usage` checks (`--runner` value; `--endpoint` with `claude`; any
     argument after the prompt other than `--model <m>`);
  2. `cfg.Resolve(role, endpoint, model)`;
  3. `daemon.Ensure` (`WS_DAEMON_AUTOSTART`), then `Preflight`. A preflight
     error with `fallback = "claude"` → the Claude path with the warning;
     otherwise the error;
  4. create the workspace (as `runClaude`, holding the lock);
  5. `runner.FilesFor(…).WriteRun` (with `trace_id =
     llm.NewTraceID()`) with the prompt from
     `role.RenderFor(vars, extra, "ws", resolved.ModelID)` (or the prompt
     after `--`), budgets, MCP keys, `branch = vars["BRANCH"]`,
     `user_instructions`; no token or header;
  6. `RecordRunnerSession`; `StartRunner`; `writeSpawn` for a role;
  7. print `machineSpawnJSON` with `runner: "ws"`, `model`, `ws_runner`
     (`model_alias`, `endpoint`, `url`, `api`, `context_tokens`,
     `mcp_servers`).
  Any failure after step 4 stops the runner and removes the workspace.
- `ws rm` / `ws machine rm` (`lifetime/remove.go`): for a runner session,
  `StopRunner` through `daemon.Ensure`; when that fails, `runner.Files`
  `RequestStop` and `SIGTERM`/`SIGKILL` the group directly. Then remove as
  today.
- `ws restore` (`lifetime/restore.go`): for a `stopped` runner session,
  `StartRunner(id, resume=true)`; the `Restored` item has the runner id.
  `ws restart` is unchanged.
- Guides: `cp docs/design/ollama-agent/guides/ws-machine-spawn.md
  internal/cli/guides/`; `guides/ws-machine.md` gains the codes and the `--runner` line in its
  synopsis.
- Docs: apply the README's diff to `docs/tools/ws-machine.md`;
  `docs/state.md` gains `daemon/` and `secrets.env`; `docs/config.md`
  links `ws daemon`.

**Acceptance** (`internal/cli` with mocks; the daemon client behind an
interface):

- `--runner ws` with no model anywhere → `config_invalid`; `--endpoint`
  with `--runner claude` → `usage`; `-- "x" --permission-mode auto` for a
  `ws` child → `usage`; each preflight error → its code and `details`, and
  no workspace created; with `fallback = "claude"` → a Claude child with
  `runner: "claude"` and the warning.
- Success → reference.md's spawn JSON, the session recorded as `runner`,
  `spawn.toml` for a role, `run.toml` without secrets. A Claude spawn's
  JSON is T8's, unchanged.
- `ws machine rm` of a runner child with the daemon mock failing → stops
  it directly and removes it. `ws restore` resumes only `stopped` ones.
- `WS_DAEMON_AUTOSTART=0` and no daemon → `daemon_unavailable`.
- `TestGuideTopics` and `go test ./internal/cli/` pass.

**Verify**

```console
$ go generate ./internal/cli/ ./internal/lifetime/
$ go test -p 2 ./internal/lifetime/
$ go test ./internal/cli/
$ make lint
```

---

## T22. `e2e/runner`: harness, scenarios, `make fakes`, `make e2e-runner`, CI

**Depends on:** T2, T3, T21 (and so T18, T19). **Parallel with:** T25.
**ADR:** [0067](../../adrs/0067-runner-tests-use-vendored-fakes.md)
(Three layers, Scenarios).

**Read:** `internal/exectest/exectest.go`, the `Makefile`,
`.github/workflows/ci.yml`, reference.md, [fake-ollama/](fake-ollama/) and
[fake-mcp/](fake-mcp/) (their READMEs say how the harness fills
placeholders).

**Create** `e2e/runner/{harness_test.go,scenarios_test.go,daemon_test.go,mcp_test.go,realmodel_test.go,mcpsmoke_test.go}`
(build tag `e2e`) and `e2e/runner/testdata/` (`cp
docs/design/ollama-agent/fake-ollama/*.yaml
docs/design/ollama-agent/fake-mcp/*.yaml e2e/runner/testdata/`).

- `Makefile`: `e2e-runner: build fakes` → `go test -tags e2e -p 1 -timeout
  20m ./e2e/runner/`; `.PHONY`. No network, no llm-router.
- Harness: `startFakeOllama(t, config)` runs `$WS_FAKE_OLLAMA` or
  `bin/fake-ollama --config … --listen 127.0.0.1:0 --control-listen
  127.0.0.1:0` and reads both listening lines from stdout;
  `startFakeMCPHTTP(t, config)` likewise with `--http 127.0.0.1:0`; a
  missing binary → `t.Fatal("… not found; run make fakes")`. Helpers
  `setFaults`, `requests`, `recorded(t, file)`. `newEnv(t)`: a temp
  `WS_CONFIG_DIR`, `XDG_RUNTIME_DIR` and `HOME` (with a `.claude.json`
  naming the fake MCP servers), a bare upstream repo, a fake `claude`
  (`exectest`), a parent workspace record, `config.toml` with endpoint
  `fake` (`probe = "ollama"`), model alias `coder` → `fake-coder`
  (`context_tokens = 32768`), and `runWS(t, env, args...)` running
  `bin/ws`. Each test stops its daemon (`ws daemon stop`) in `t.Cleanup`.
- One `t.Run` per scenario in 0067's table (21), waiting with `ws machine
  wait --timeout 2m`; daemon scenarios kill the runner by the pid in
  `status.json`.
- `realmodel_test.go`: `E2E_RUNNER_URL` (+ `E2E_RUNNER_MODEL`, default
  `qwen3-coder:30b`; `E2E_RUNNER_API`, default `openai`) runs `edit-file`
  and `review-verdict`, asserting only `exit == "done"` and a verdict;
  otherwise `t.Skip`.
- `mcpsmoke_test.go`: `E2E_MCP_SMOKE=<server>:<tool>[,…]` uses the real
  `HOME`'s Claude config, fake-ollama scripted to call those tools with
  `{}`, and a role allowing exactly them; asserts each call succeeded and
  that no `env` or `headers` value of those definitions appears in the
  transcript or `runner.log`. Otherwise `t.Skip`. Never in CI.
- `.github/workflows/ci.yml`: job `e2e-runner` (ubuntu-latest, Go
  1.27.x) running `make e2e-runner`.

**Acceptance**

- `make e2e-runner` passes all 21 scenarios from a clean checkout with no
  network beyond loopback (CI proves it).
- With `bin/fake-ollama` deleted and `WS_FAKE_OLLAMA` unset, it fails
  with the "run make fakes" message.
- Without the opt-in variables, the real-model and MCP smoke tests skip.

**Verify**

```console
$ make build && make fakes
$ make e2e-runner
```

---

## T23. `ws-online` keeps a managed daemon running

**Depends on:** T17. **Parallel with:** T12–T15, T18–T21.
**ADR:** [0068](../../adrs/0068-ws-daemon.md) (Where it runs).

**Read:** `desktop/src-tauri/src/online/server.rs` (`run`, the
`tokio::spawn` calls), `desktop/src-tauri/src/loops.rs` (`tick_forever`,
how it runs `ws`), `desktop/src-tauri/src/runner.rs` (how the app finds
and runs `ws`), `docs/online.md`.

**Create** `desktop/src-tauri/src/online/daemon.rs` (+ tests in the same
file).

```rust
/// Keeps `ws daemon run --origin managed` running for the life of the
/// server (ADR 0068): restarts it when it exits, with backoff 1 s
/// doubling to 60 s, reset after 10 min of uptime. Its stderr goes to the
/// server's log. A `daemon_running` exit (4) means another daemon holds
/// the lock: wait 60 s and try again, without logging an error each time.
pub async fn keep_running(ws: WsCommand) -> !
fn next_backoff(prev: Duration, uptime: Duration) -> Duration
```

`server.rs` `run`: `tokio::spawn(online::daemon::keep_running(…))` beside
the loop tick task, started before the HTTP listener. The daemon inherits
the container's environment. `docs/online.md`: a "Background work"
paragraph: the server runs `ws loop tick`, the review and notification
tasks, and a managed `ws daemon`, which resumes runner children after a
pod restart; `/tmp` holds its socket and needn't persist.

**Acceptance**

- `next_backoff`: 1 → 2 → … → 60 s cap; reset after 10 min of uptime.
- A test with a fake `ws` script that exits at once: restarted with the
  backoff (tokio paused time); exit 4 → the 60 s wait.
- `cargo test --no-default-features --features online` and `cargo clippy
  --all-targets --no-default-features --features online -- -D warnings`
  pass.

**Verify**

```console
$ cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online online::daemon
$ cd desktop/src-tauri && cargo clippy --all-targets --no-default-features --features online -- -D warnings
```

---

## T24. Desktop: a quiet model label on every agent, the runner transcript, Settings › Models

**Depends on:** T19. **Parallel with:** T20, T21.
**Design:** the [Screens](README.md#screens) table and rules, and
[mockups.html](mockups.html) (`#agents-tree`, `#transcript`,
`#settings-models`, `#settings-models-errors`). Match the PNGs in
[img/](img/).

**Read:** `desktop/src-tauri/src/model.rs` (`WsSession`,
`workspace_entry`), `desktop/src-tauri/src/transcript.rs` (public fns),
`desktop/src-tauri/src/ws_config.rs`,
`desktop/src/features/agents/{EntryRow.tsx,EntryPane.tsx,AgentsActions.tsx}`,
`desktop/src/features/conversation/{rows.ts,ConversationView.tsx}`,
`desktop/src/features/settings/{SettingsPane.tsx,SettingsList.tsx,ForgesGroup.tsx,Row.tsx}`,
`desktop/src/api.ts`, `desktop/src/test/fixtures.ts`.

**Change**

- `model.rs`: `WsSession` gains `runner: Option<String>`, `model:
  Option<String>` and `ws_runner: Option<WsRunner>` (reference.md's
  fields), `#[serde(default)]`. `Entry` gains `runner` (`"claude"`,
  `"ws"` or `""`), `model: Option<String>` (display form: a `claude-`
  prefix removed, so `claude-sonnet-5-5` → `sonnet-5-5`), and `ws_runner`.
  For `runner == "ws"`, don't look up a Claude job: status from `state`.
- `transcript.rs`: `pub fn runner_transcript(state_dir, id) ->
  Vec<ConversationRow>` mapping `transcript.jsonl` events to the existing
  row kinds: `response` → assistant text (and reasoning), `tool_calls` →
  tool rows (name + first 120 chars of arguments), `tool_result` →
  result rows (ok/error, bytes, first 20 lines), `retry`, `compaction`,
  `truncated`, `nudge`, `invalid_call`, `mcp_start`, `mcp_error`,
  `tool_search`, `restart` → a muted system row, `exit` → a final row
  (`done` in the normal colour, others with the reason in the warning
  colour, as any failed agent's last row). The conversation command
  dispatches on the entry's runner.
- **Every agent's model, quietly.** `EntryRow.tsx`: at the right of the
  summary line, the model as secondary text (`.modelLabel` in the mockup:
  11px `--font-mono`, `--text-muted`, normal case, no background, no
  border), for every agent with a model, Claude or not; nothing for shells or a Claude agent with no model yet. The row's
  `title` tooltip: `ws` child → `qwen3-coder:30b · coder on gpu
  (http://…, openai)`; Claude agent → `sonnet-5-5 · Claude Code`. **No
  runner chip, badge, icon or colour anywhere.** The summary line is
  unchanged for Claude; for a `ws` child it is `step {step}/{maxSteps}`.
- `EntryPane.tsx` and `AgentsActions.tsx`: the model id beside the title
  in muted monospace for every agent (`.titleModel`). For `ws` children,
  the existing neutral meta chips (`step n / max`, `tokens 210k in · 9.1k
  out`, the role) and muted text with the endpoint, session id and state
  (`#transcript`), and a "Run" card with rows Runner, Model (id, alias,
  context), Endpoint (name, URL, API), Progress, MCP servers, Last event,
  Session, and the exit reason when finished (`#agents-tree`). Attach, Send and
  Continue are hidden for `ws` children; Remove works as for any child.
- **New** `desktop/src/features/settings/ModelsGroup.tsx` (+
  `.module.css`, `.test.tsx`), "Models" in `SettingsList` after "Forges",
  as in `#settings-models`:
  1. *Endpoints*: name, URL, API (`OpenAI-compatible` only, others shown
     disabled as "later"), probe, timeouts, token (write-only); Add, Edit,
     Remove; saving runs `ws config set endpoints.<name>.<key>` per
     changed key, and the token through a new Tauri command
     `endpoint_token_set(name, token)` in **new**
     `desktop/src-tauri/src/secrets.rs`, which writes the
     `WS_ENDPOINT_TOKEN_<NAME>` line of `CONFIG_DIR/secrets.env` with the
     rules of T16's `SetSecret` (temp file, rename, 0600, other lines
     kept), modelled on `reviews/tokens.rs`; an empty token removes the
     line. The frontend never receives a token back, only `token_source`
     from `runner_check`.
  2. *Models*: alias, endpoint, id, context, max output, temperature.
  3. *Test connection* per endpoint: a Tauri command `runner_check` runs
     `ws runner check --json <endpoint> [--model <m>]` and shows `✓ 4 models ·
     qwen3-coder:30b supports tools · context 65536`, or the error banners
     of `#settings-models-errors` (`endpoint_unreachable`,
     `model_not_served`, `model_without_tools`).
  4. *Roles*: per role (`implementer`, `reviewer`, `verifier`, plus `ws
     machine roles`): runner (Claude Code / ws runner), model alias, MCP
     servers (from the user's Claude Code config names), `ws config set
     runner.roles.<role>.…`.
  5. *Fallback*: "When preflight fails, start a Claude child instead"
     (`runner.fallback`).
  Daemon status is not shown here (deferred, ADR 0068).
- The Tauri mock (`fixtures.ts`) gains a `ws` child with a model, Claude
  agents with models, `settings_get` with the new tables, `runner_check`,
  and a runner conversation, so the four screens render from fixtures.

**Acceptance**

- Rust: `workspace_entry` maps a runner session (no job lookup) and a
  Claude session's model (`claude-` stripped); `runner_transcript` maps a
  fixture JSONL with every event kind. `cargo test -j 2` and `cargo clippy
  --all-targets -- -D warnings` pass, default and `--no-default-features
  --features online`.
- TS: every agent row with a model shows it as muted text and nothing
  else; no element in the agents tree has a runner-specific class or
  colour (a test asserts the row markup is the same for a Claude and a
  `ws` entry apart from the text); the tooltip text is as above; Attach
  and Send are hidden for `ws` entries. `ModelsGroup` renders, calls `ws
  config set` with the right keys, never renders a token, shows a check
  result and the three error states, and saves role routing. `npm run
  typecheck` and `npm test -- --maxWorkers=2 --testTimeout=60000` pass.
- Layout: at 1280×800 and 390×844, no clipped inputs, no horizontal
  scroll, tap targets ≥ 40px on phone widths. A screenshot child checks
  this against [img/](img/); put it in the report.

**Verify**

```console
$ cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000
$ cd desktop/src-tauri && cargo test -j 2 && cargo clippy --all-targets -- -D warnings
$ cd desktop/src-tauri && cargo test -j 2 --no-default-features --features online
```
