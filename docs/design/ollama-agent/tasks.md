# Runner: implementation tasks

Implements ADRs [0062](../../adrs/0062-agent-runner-built-into-ws.md) to
[0069](../../adrs/0069-runner-mcp-tools-and-claude-code-parity.md) and
[reference.md](reference.md). Every task targets **this repo**
(workspaces); nothing is done in llm-router, and nothing clones it. Each
task builds, passes its tests, and can be merged on its own. Each task is
one commit, conventional and imperative, for example `feat: add the
runner's provider seam (ADR 0062)`.

**Order and parallelism**

| Task | Title | Depends on | Parallel with | Size |
| ---- | ----- | ---------- | ------------- | ---- |
| T1 | Copy fake-ollama into `internal/fakeollama` and `cmd/fake-ollama` | nothing | T3–T6, T8 | |
| T2 | fake-ollama: conversation rules, raw arguments, strict capabilities, `/api/*`, context length, `Retry-After`, `auth` | T1 | T7, T9, T11, T15, T16 | |
| T3 | `internal/fakemcp` and `cmd/fake-mcp` | nothing | T1, T4–T6, T8 | |
| T4 | `internal/config`: `[runner]`, `[endpoints]`, `[models]`, `[runner.roles]`, `[runner.env]`, `[daemon]`, `ws config` keys | nothing | T1, T3, T5, T6, T8 | |
| T5 | `internal/agent/tools`: the seven tools, guards, environment scrub, redactor | nothing | T1, T3, T4, T6, T8 | small |
| T6 | `internal/llm`: the provider seam, errors, preflight | nothing | T1, T3–T5, T8 | small |
| T7 | `internal/llm/openai`: the OpenAI-compatible provider, and `internal/llm/providers` | T1, T6 | T2, T9, T11, T15, T16 | |
| T8 | A `model` for every agent; `.Runner` and `.Model` in role templates | nothing | T1, T3–T6 | small |
| T9 | `internal/mcp`: server definitions, expansion, names, filter | nothing (T4 for the role keys' names only) | T2, T7, T11, T15, T16 | small |
| T10 | `internal/mcp`: the client (stdio, streamable HTTP, SSE) | T3, T9 | T20 | |
| T11 | `internal/agent`: the loop (copied), budgets, compaction, transcript, preamble, project instructions | T5, T6 | T2, T7, T9, T15, T16 | |
| T12 | MCP tools in the loop: the toolset and `tool_search` | T10, T11 | none | small |
| T13 | `internal/runner`, session kind `runner`, hidden `ws runner run` | T4, T7, T12 | none | |
| T14 | `lifetime` reads runner sessions; `runner`/`model`/`ws_runner` in `ws list`, `children`, `wait`; `ws machine logs` | T8, T13 | T17 | |
| T15 | `secrets.env` and the runner's environment | T4 | T2, T7, T9, T11, T16 | small |
| T16 | `internal/daemon` core and `ws daemon run|status|stop` | T4 | T2, T7, T9, T11, T15 | |
| T17 | The daemon supervises runners; preflight and `ws runner check` | T13, T15, T16 | T14 | |
| T18 | `ws machine spawn --runner`, `ws rm`/`ws restore` through the daemon, guides and docs | T14, T17 | none | |
| T19 | `e2e/runner`: harness, scenarios, `make fakes`, `make e2e-runner`, CI | T2, T3, T18 | T21 | |
| T20 | `ws-online` keeps a managed daemon running | T16 | T10–T19 | small |
| T21 | Desktop: a quiet model label on every agent, the runner transcript, Settings › Models | T18 | T19 | |

Waves, if run in parallel: **1** T1, T3, T4, T5, T6, T8 · **2** T2, T7, T9,
T11, T15, T16 · **3** T10, T20 · **4** T12 · **5** T13 · **6** T14, T17 ·
**7** T18 · **8** T19, T21. Run at most one heavy implementer at a time on
an 8 GB host (handbook lesson: Go full test runs, `cargo`, vitest); the
tasks marked `small` touch one package and are light.

Task numbers changed in the 2026-11-14 rework. The first draft's T1
(fake-ollama changes in llm-router) is now T1 and T2 here; its T2–T8 map to
T4, T6/T7, T5, T11, T13/T14, T18/T19, T21. The first draft's T8 acceptance
("an Ollama badge") is replaced by T21's quiet model label, and the data
behind it is T8 and T14.

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
- **Code copied from llm-router** (T1, T11) is this repo's code from the
  moment it lands: it follows every rule above, `make lint` included. Each
  copied package's doc comment says `Copied from llm-router internal/<pkg>
  @9789c32 (ADR 0067); maintained here.` (ADR 0062 for the loop). Read
  llm-router from the read-only checkout your workspace was given (the
  orchestrator spawns T1 and T11 with `llm-router@9789c32` as a second
  repo spec); never `git clone` it, never add it to `go.mod`.
- New dependencies go in `go.mod` only in the task that names them: T1
  (`go.yaml.in/yaml/v3` becomes direct), T3 (`github.com/modelcontextprotocol/go-sdk`,
  the newest v1), T7 (`github.com/openai/openai-go/v3 v3.71.1`). Run `go
  mod tidy` and commit `go.sum`.
- In tasks that touch `internal/cli`, run
  `go test ./internal/cli/ -run TestGuideTopics`. It checks that each
  command has a guide in `internal/cli/guides/` and a topic in
  `guideTopics()` (`internal/cli/llm.go`), that each guide mentions every
  local flag and `--json`, has a ` ```json ` block under `## JSON output`,
  `## Exit codes` and `## Examples`, and that `guides/ws.md` links every
  guide and lists every error code. The finished guides are in
  [guides/](guides/): copy them with `cp`, unchanged.
- Run heavy commands one at a time (`go test -p 2`). Commit and push
  before the slowest suite. If a command was killed, say so in the report.
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

**Depends on:** T1. **Parallel with:** T7, T9, T11, T15, T16.
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
- Every YAML file in `docs/design/ollama-agent/fake-ollama/` loads
  (`LoadConfig`) without error (a table test over the directory).
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
	Name          string            `yaml:"name"`           // server name in initialize; default "fake"
	RequireEnv    map[string]string `yaml:"require_env"`    // stdio: initialize fails unless each is set to the value
	RequireHeader map[string]string `yaml:"require_header"` // http: each request must carry each header with the value
	Faults        Faults            `yaml:"faults"`
	Tools         []Tool            `yaml:"tools"`
	Generate      *Generate         `yaml:"generate"`
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
	ReadOnly    *bool          `yaml:"read_only"`    // readOnlyHint
	Destructive *bool          `yaml:"destructive"`  // destructiveHint
	Replies     []Reply        `yaml:"replies"`
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
	Prefix string `yaml:"prefix"` // tools <prefix>_001…, read-only, replying "ok"
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

- The three configs in [fake-mcp/](fake-mcp/) load. `generate: {count:
  80, prefix: lookup}` lists 80 tools plus the scripted ones, with
  `readOnlyHint` set.
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
// implement yet (ADR 0062).
func ReservedAPIs() []string { return []string{"ollama", "openai-responses", "anthropic"} }

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
- `endpoint` and `id` required on a model; `endpoint` defined;
  `context_tokens` ≥ `MinContextTokens`; `max_output` ≥ 0; `temperature`
  parses as a float in [0, 2].
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
`models.<alias>.{endpoint,id,context_tokens,max_output,temperature}`;
`runner.roles.<role>.{runner,model,max_steps,timeout,max_tokens,mcp_servers,mcp_tools,mcp_deny,mcp_upfront}`
(string lists as for existing list keys). `Settings` lists fixed keys, then
each endpoint, model and role sorted by name. `Unset` of
`endpoints.<name>`, `models.<alias>`, `runner.roles.<role>` or
`runner.env.<NAME>` removes it.

**Docs:** `docs/config.md` gains the tables from reference.md and, at the
end of "The generated file", the commented block from the
[README](README.md#configuration); `docs/tools/ws-config.md` lists the keys
and the `int`/`float` types.

**Acceptance**

- `Resolve("implementer", "", "")` with `runner.model = "coder"` and role
  implementer `model = "small"` → alias `small`, its endpoint, 80 steps,
  60m, 4 000 000, `api` `openai`, `probe` `auto`, 10m, 90s, `max_output`
  4096, temperature 0.2, `mcp_tools` `["*"]`, `mcp_upfront` 8.
- Flags win: `Resolve("implementer", "cpu", "coder")` → endpoint `cpu`. An
  unknown endpoint → `ErrEndpointUnknown`. `Resolve("x", "", "qwen3:8b")`
  (no such alias) → that id on `runner.model`'s endpoint and a warning. No
  model anywhere → `ErrNoModel`.
- `RunnerFor`: configured role → its runner; anything else → `claude`.
  `TokenEnv("gpu-a")` = `WS_ENDPOINT_TOKEN_GPU_A`.
- Parsing fails, naming the key, for `context_tokens = 4096`, `url =
  "ftp://x"`, `url = "http://u:p@x"`, `fallback = "always"`, `api =
  "anthropic"` (the message says not implemented yet), a model whose
  endpoint isn't defined, `token = "x"`, `[runner.env] GRAFANA_TOKEN = "x"`
  and the name `GPU`.
- `ws config set models.coder.context_tokens 65536` writes it and keeps
  comments; `set … 4096` → `ErrValueInvalid`; `unset models.coder` removes
  the table. An empty `config.toml` still parses to `Default()`. Existing
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
// *_PASSWORD, or in secretNames (the names in secrets.env), except
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
- `list_files {path, glob}`: walks the path, skipping `.git`, directories
  named `node_modules`, `vendor`, `target`, `dist`, `bin`, and exact
  `.gitignore` entries; ≤ 500 sorted paths, then `[N more not shown]`.
  Runs no command.
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
  required field → `missing <field>`. Every result is cut to `MaxResult`
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
  `NPM_TOKEN`, `DB_PASSWORD` and `GRAFANA_SERVICE_ACCOUNT_TOKEN`, and
  `MY_VAR` when it's in `secretNames`; keeps `GH_TOKEN` with `keepGit`,
  drops it without; keeps `HOME`, `PATH`, `GRAFANA_URL`; sets
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
	TraceParent string // W3C traceparent, or ""
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
type Response struct{ Message Message; StopReason StopReason; Usage Usage; Duration time.Duration }

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

**Depends on:** T1 (golden fixtures), T6. **Parallel with:** T2, T9, T11, T15, T16.
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
  set. A gap over `IdleTimeout` → `ErrStreamStalled`; end without `[DONE]`
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
  prompt first; `Authorization: Bearer t` for token `t`; `traceparent`
  when set.
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
