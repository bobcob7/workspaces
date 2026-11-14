# Ollama runner: implementation tasks

Implements ADRs [0062](../../adrs/0062-ollama-runner-built-into-ws.md)
to [0067](../../adrs/0067-ollama-runner-tests-use-fake-ollama.md) and
[reference.md](reference.md). Each task builds, passes its tests, and can
be merged on its own. Each task is one commit, conventional and
imperative, for example `feat: add the ollama client (ADR 0062)`.
**T1 targets the llm-router repo**; T2 to T8 target the workspaces repo.

**Order and parallelism**

| Task | Repo | Title | Depends on | Parallel with | Size |
| ---- | ---- | ----- | ---------- | ------------- | ---- |
| T1 | llm-router | fake-ollama: conversation rules, raw arguments, strict capabilities, `/api/*`, context length | nothing | T2, T3, T4 | |
| T2 | workspaces | `internal/config`: `[ollama]` schema and `ws config` keys | nothing | T1, T3, T4 | |
| T3 | workspaces | `internal/ollama`: client and preflight | nothing | T1, T2, T4 | |
| T4 | workspaces | `internal/agent/tools`: the seven tools and guards | nothing | T1, T2, T3 | |
| T5 | workspaces | `internal/agent`: the loop (forked), budgets, compaction, transcript | T3, T4 | T1, T2 | |
| T6 | workspaces | `internal/runner`, session kind `ollama`, lifetime, `ws runner run` | T2, T5 | T1 | |
| T7 | workspaces | `ws machine spawn --runner`, `logs`, wait/children/list fields, guides, docs, e2e | T1, T6 | none | |
| T8 | workspaces | Desktop: Ollama badge, transcript view, Settings › Models | T7 | none | |

T2, T3 and T4 touch disjoint packages. T1 is in another repo. Run at most
one heavy implementer at a time on an 8 GB host (handbook lesson). None of
these tasks is `small`; T2 and T4 are the smallest.

**Read before any task:** [reference.md](reference.md) (every flag, JSON
shape, file and error), the "Decision" section of the ADR the task names,
and only the files the task lists. Where this file and reference.md
disagree, reference.md wins. Don't read whole packages.

**Rules for every task (workspaces repo)**

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

**Rules for T1 (llm-router repo)**: the same Go rules. Also follow
llm-router's `docs/adrs/0013-unit-testing-conventions.md` and `0015`
(json/v2). Run `make fmt lint test` in llm-router.

---

## T1. fake-ollama: conversation rules, raw arguments, strict capabilities, `/api/*`, context length

**Repo:** llm-router (`git.bobcob7.com/forgejo_admin/llm-router`), branch
off `main`. **Depends on:** nothing. **Parallel with:** T2, T3, T4.

**Read:** `internal/fakeollama/config.go`, `reply.go`, `api.go`,
`response.go`, `server.go` (only `New` and `model`), `reply_test.go` (for
style), `docs/adrs/0018-rule-driven-fake-tool-calls.md`.

**Change**

- `internal/fakeollama/config.go`
  - `Config` gains `StrictCapabilities bool `yaml:"strict_capabilities"``.
  - `Model` gains `ContextLength int `yaml:"context_length"`` (0 means no
    limit) and `Family string `yaml:"family"`` (default `"fake"`, used in
    `/api/show`'s `model_info` key).
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
- `internal/fakeollama/reply.go`
  - `compileRule`: `RawArguments` together with `Arguments` →
    `errRawAndArguments` ("set at most one of arguments and
    raw_arguments"). `RawArguments` or `AnyTool` without `Tool` →
    `errArgumentsWithoutTool`. `Match` may be empty when `Turn` or
    `MatchToolResult` is set (an empty pattern matches anything).
    Compile `MatchToolResult`.
  - `answer`: first try the rules that have `Turn` or `MatchToolResult`,
    in config order. A rule applies when its `Turn` (if set) equals the
    number of assistant messages, its `MatchToolResult` (if set) matches
    the trailing tool results, and its `Match` matches the last user
    message. Only then fall back to today's behaviour (`The tool returned:
    …` for trailing tool results, then the plain rules, then echo).
    Template data gains `ToolResults []string` and `Turn int`.
  - Tool rules: with `AnyTool`, skip `offeredTool` and use a synthetic
    tool with that name and no parameters. With `RawArguments`, render it
    as a template and skip the JSON validity check.
- `internal/fakeollama/api.go`
  - `handleChat`: when `s.cfg.StrictCapabilities` is true, the request has
    tools, and the model lacks `tools`, respond with `400` and the body
    `{"error":"<model> does not support tools"}`. That's Ollama's native
    error shape, not the OpenAI envelope; add a helper `writeOllamaError(w,
    status, msg)`.
  - Context length: when the model's `ContextLength > 0` and the request's
    word count (the same count `usageFor` uses) is over it, drop the
    oldest non-system messages until it fits, keeping the last message. Then
    respond to the truncated request, and have `usageFor` count the
    truncated messages. Put this in a new `truncate.go` with
    `func truncateToContext(req openaiwire.ChatRequest, limit int)
    openaiwire.ChatRequest`.
  - Register `GET /api/version` → `{"version":"0.0.0-fake"}`, `GET
    /api/tags` → `{"models":[{"name":"<m>","model":"<m>","size":0,"details":{"family":"<family>"}}]}`,
    and `POST /api/show` with body `{"model":"<m>"}` → `{"capabilities":
    [...], "model_info": {"<family>.context_length": <n>}, "details":
    {"family": "<family>"}}`. `capabilities` is `["completion"]` plus
    `"tools"` and `"thinking"` (for the `reasoning` capability) when the
    model has them, and `<n>` is `ContextLength` or 32768 when it's 0. An
    unknown model gets `404 {"error":"model '<m>' not found"}`. These go
    through `record` and the faults like the other endpoints.
- Docs: README "fake-ollama" section (the new rule fields, config keys and
  endpoints), and an "Update 2026-11" paragraph in
  `docs/adrs/0018-rule-driven-fake-tool-calls.md`.

**Acceptance**

- A config with rules `turn: 0 → tool write_file`, `turn: 1 → tool run`,
  `turn: 2 → reply "done"`, sent three requests that grow by an assistant
  tool call and a tool result each, returns `write_file`, then `run`, then
  `done`.
- `match_tool_result: "exit 1"` applies only when the trailing tool result
  contains `exit 1`.
- `raw_arguments: '{"path": '` produces exactly that `arguments` string,
  streamed and not. `any_tool: true` calls a tool the request didn't
  offer.
- `strict_capabilities: true`, a model without `tools`, and a request
  with `tools` → 400 `{"error":"fake-chat does not support tools"}`.
  Without the flag the behaviour is unchanged (existing tests pass
  unchanged).
- `context_length: 50` and a 200-word conversation → `prompt_tokens` ≤ 50,
  and the system message and last user message are kept.
- `/api/show`, `/api/tags` and `/api/version` return the shapes above. A
  `down` fault makes them fail too. `GET /requests` records them.
- Coverage of `internal/fakeollama` doesn't drop (`go test -cover`).

**Verify**

```console
$ go test ./internal/fakeollama/ ./e2e/... -run 'Fake|Rule|Truncat|API' -p 2
$ make lint
```

When merged, put the merge commit's full SHA in the T7 report: T7 writes
it to `e2e/ollama/FAKE_OLLAMA_REF`.

---

## T2. `internal/config`: `[ollama]` schema and `ws config` keys

**Depends on:** nothing. **Parallel with:** T1, T3, T4.
**ADR:** [0064](../../adrs/0064-choosing-the-runner-endpoint-and-model.md).

**Read:** `internal/config/config.go`, `internal/config/edit.go`
(`fixedRules`, `profileRule`, `parseKeyWith`, `normalizeOne`, `Settings`),
`internal/config/config_test.go` and `edit_test.go` (style only),
`docs/config.md` "Schema", reference.md [config.toml](reference.md#configtoml).

**Create** `internal/config/ollama.go` and `internal/config/ollama_test.go`.

```go
// Ollama is config.toml's [ollama]: where on-prem models are and which
// roles run on them (ADR 0064).
type Ollama struct {
	Endpoint      string                    `toml:"endpoint" json:"endpoint"`
	Fallback      string                    `toml:"fallback" json:"fallback"`
	KeepGitTokens *bool                     `toml:"keep_git_tokens" json:"keep_git_tokens"`
	Endpoints     map[string]OllamaEndpoint `toml:"endpoints" json:"endpoints"`
	Roles         map[string]OllamaRole     `toml:"roles" json:"roles"`
}

// OllamaEndpoint is one [ollama.endpoints.<name>].
type OllamaEndpoint struct {
	URL            string        `toml:"url" json:"url"`
	API            string        `toml:"api" json:"api"`
	Model          string        `toml:"model" json:"model"`
	ContextTokens  int           `toml:"context_tokens" json:"context_tokens"`
	RequestTimeout time.Duration `toml:"request_timeout" json:"request_timeout"`
	IdleTimeout    time.Duration `toml:"idle_timeout" json:"idle_timeout"`
}

// OllamaRole is one [ollama.roles.<role>].
type OllamaRole struct {
	Runner    string        `toml:"runner" json:"runner"`
	Endpoint  string        `toml:"endpoint" json:"endpoint"`
	Model     string        `toml:"model" json:"model"`
	MaxSteps  int           `toml:"max_steps" json:"max_steps"`
	Timeout   time.Duration `toml:"timeout" json:"timeout"`
	MaxTokens int           `toml:"max_tokens" json:"max_tokens"`
}

// Runner names.
const (
	RunnerClaude = "claude"
	RunnerOllama = "ollama"
)

// MinContextTokens is the smallest context_tokens accepted.
const MinContextTokens = 16384

var (
	// ErrEndpointUnknown is an endpoint name not in [ollama.endpoints].
	ErrEndpointUnknown = errors.New("ollama endpoint not configured")
	// ErrNoModel means no model was given or configured.
	ErrNoModel = errors.New("no ollama model given or configured")
)

// Run is everything a spawn needs to start an ollama child.
type Run struct {
	EndpointName string
	Endpoint     OllamaEndpoint // with defaults filled in
	Model        string
	MaxSteps     int
	Timeout      time.Duration
	MaxTokens    int
}

// RunnerFor is ollama.roles.<role>.runner, or RunnerClaude.
func (o *Ollama) RunnerFor(role string) string

// Resolve picks the endpoint (endpointFlag, the role's, then o.Endpoint)
// and the model (modelFlag, the role's, then the endpoint's), and the
// budgets (the role's, else DefaultBudget(role)). An unknown endpoint is
// ErrEndpointUnknown; none at all, or no model, is ErrNoModel or
// ErrEndpointUnknown wrapped with what was missing.
func (o *Ollama) Resolve(role, endpointFlag, modelFlag string) (Run, error)

// KeepsGitTokens is KeepGitTokens, default true.
func (o *Ollama) KeepsGitTokens() bool

// DefaultBudget is ADR 0065's defaults: implementer 80 steps, 60m,
// 4_000_000 tokens; reviewer 40, 30m, 2_000_000; verifier 40, 60m,
// 2_000_000; anything else 60, 60m, 4_000_000.
func DefaultBudget(role string) (maxSteps int, timeout time.Duration, maxTokens int)

// TokenEnv is the environment variable holding endpoint name's token:
// WS_OLLAMA_TOKEN_ + upper(name) with '-' replaced by '_'.
func TokenEnv(name string) string

// TokensFile is CONFIG_DIR/ollama-tokens.json's name.
const TokensFile = "ollama-tokens.json"

// LookupToken is getenv(TokenEnv(name)) when not empty, else the name's
// entry in <configDir>/ollama-tokens.json ({"version":1,"tokens":{…}}),
// else "". A missing file is "", nil; an unreadable or invalid one is an
// error naming the file.
func LookupToken(configDir, name string, getenv func(string) string) (string, error)

// check validates o; called from Config.normalize.
func (o *Ollama) check() error
```

`Config` gains `Ollama Ollama `toml:"ollama"`` after `Review`. `normalize`
calls `o.check()`, wrapping errors like the other sections. Endpoint
defaults are filled in by `Resolve`, not stored: `api` `"ollama"`,
`request_timeout` 10m, `idle_timeout` 90s.

**`check` rules** (each error names the key):

- `fallback` is `""`, `none` or `claude`. `runner` is `""`, `claude` or
  `ollama`. `api` is `""`, `ollama` or `openai`.
- Endpoint and role names match `^[a-z][a-z0-9-]{0,31}$`.
- `url` is required, parses with `net/url`, has scheme `http` or `https`,
  a host, and no user info, query or fragment.
- `context_tokens` is required and at least `MinContextTokens`.
- Timeouts, `max_steps` and `max_tokens` aren't negative.
- `ollama.endpoint` and every role's `endpoint`, when set, name a defined
  endpoint.
- Models have no spaces.
- A `token` key anywhere under `[ollama]` gives the error `ollama endpoint
  tokens go in WS_OLLAMA_TOKEN_<NAME> or Settings › Models, never in
  config.toml`. Extend `tokenHint`, which already does this for hosts.

**`ws config` keys** (`internal/config/edit.go`):

- New type `TypeInt Type = "int"`; values are base-10 integers.
- Fixed rules, after the review ones: `ollama.endpoint` (string, a name),
  `ollama.fallback` (string, `none` or `claude`), `ollama.keep_git_tokens`
  (bool).
- `ollamaEndpointRule(name, leaf)` for `ollama.endpoints.<name>.<leaf>`,
  with leaf `url` (string), `api` (string), `model` (string),
  `context_tokens` (int), `request_timeout` and `idle_timeout` (duration).
- `ollamaRoleRule(name, leaf)` for `ollama.roles.<name>.<leaf>`, with leaf
  `runner`, `endpoint` and `model` (string), `max_steps` and `max_tokens`
  (int), `timeout` (duration).
- `parseKeyWith` accepts both, using `^[a-z][a-z0-9-]{0,31}$` for names.
  `normalizeOne` dispatches on the leaf. Each value is checked with the
  `check` rules for that key, and the whole file is checked again after the
  edit by the existing `verifySet` path.
- `Settings` lists the fixed `ollama.*` keys, then for each endpoint
  (sorted) its six keys, then for each role (sorted) its six, after the
  review profile keys.
- `Unset` of `ollama.endpoints.<name>` or `ollama.roles.<name>` (no leaf)
  removes the whole table, as an empty profile table is removed today.

**Docs:** `docs/config.md` gains `### [ollama]`, `### [ollama.endpoints.<name>]`
and `### [ollama.roles.<role>]`, copying reference.md's tables, and the
commented block from the [README](README.md#configuration) at the end of
"The generated file". `docs/tools/ws-config.md` lists the new keys and
`int`.

**Acceptance**

- `Resolve("implementer", "", "")`, with `[ollama] endpoint="gpu"`,
  endpoint gpu `model="m1"` and role implementer `model="m2"` → endpoint
  `gpu`, model `m2`, 80 steps, 60m, 4 000 000; the endpoint has `api`
  `ollama`, 10m and 90s.
- The flags win: `Resolve("implementer", "cpu", "m3")` → `cpu`, `m3`. An
  unknown `cpu` → `ErrEndpointUnknown`. No endpoint anywhere →
  `ErrEndpointUnknown`. No model anywhere → `ErrNoModel`.
- `RunnerFor`: a configured role → its runner; anything else → `claude`.
- `TokenEnv("gpu-a")` = `WS_OLLAMA_TOKEN_GPU_A`. `LookupToken`: the env
  wins; else the file's entry; a missing file → `""`; invalid JSON → an
  error.
- Parsing fails, naming the key, for: `context_tokens = 4096`; `url =
  "ftp://x"`; `url = "http://u:p@x"`; `fallback = "always"`; a role
  `endpoint` that isn't defined; `token = "x"` (the message says
  `WS_OLLAMA_TOKEN_`); the name `GPU`.
- `ws config set ollama.endpoints.gpu.context_tokens 65536` writes `[ollama.endpoints.gpu]`
  `context_tokens = 65536` and keeps comments. `set … 4096` →
  `ErrValueInvalid`. `unset ollama.endpoints.gpu` removes the table.
  `Settings` lists the keys in the order above, with `type` `int` for
  `context_tokens`.
- An empty `config.toml` still parses to `Default()`. Existing tests
  pass unchanged.

**Verify**

```console
$ go test ./internal/config/
$ go test ./internal/cli/ -run 'TestApp_.*Config|TestGuideTopics'
$ make lint
```

---

## T3. `internal/ollama`: client and preflight

**Depends on:** nothing. **Parallel with:** T1, T2, T4.
**ADRs:** [0062](../../adrs/0062-ollama-runner-built-into-ws.md),
[0064](../../adrs/0064-choosing-the-runner-endpoint-and-model.md) (preflight).

**Read:** llm-router's `internal/agent/agent.go` and `interfaces.go` @9789c32
(in a clone: `git clone https://git.bobcob7.com/forgejo_admin/llm-router.git
/tmp/llm-router && git -C /tmp/llm-router checkout 9789c32`), especially
`completeStreaming`, `reasoningOf` and `WrapError`; and reference.md
[spawn preflight](reference.md#ws-machine-spawn-new-flags).

**Add** `github.com/openai/openai-go/v3 v3.71.1` to `go.mod` (`go get
github.com/openai/openai-go/v3@v3.71.1`).

**Create** `internal/ollama/{client.go,preflight.go,errors.go,interfaces.go,moq_test.go}`
plus a `_test.go` for each source file, and `internal/ollama/testdata/`.

```go
// Package ollama talks to an Ollama server, or an OpenAI-compatible router
// in front of Ollama, for the runner (ADRs 0062, 0064). It is the only
// package that sends requests to /v1/chat/completions.
package ollama

// Endpoint is where and how to reach a server.
type Endpoint struct {
	Name           string
	URL            string // base URL without /v1
	API            string // "ollama" or "openai"
	Token          string // bearer token, or ""
	RequestTimeout time.Duration
	IdleTimeout    time.Duration
}

// Message, ToolCall and Tool are the runner's own types, so callers don't
// import openai-go.
type Message struct {
	Role       string     // "system", "user", "assistant", "tool"
	Content    string
	Reasoning  string     // assistant only, never sent back
	ToolCalls  []ToolCall // assistant only
	ToolCallID string     // tool only
}
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON text as the model sent it
}
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
}

// Request is one chat completion.
type Request struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature float64
	TraceParent string // W3C traceparent header, or ""
}

// Usage is a response's token counts.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Response is one completed (streamed) chat completion.
type Response struct {
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	HasUsage     bool
	Duration     time.Duration
}

// Client talks to one endpoint.
type Client struct { /* endpoint, http client, chatCompleter, logger */ }

// New returns a Client for ep, sending requests through httpClient
// (nil: a client with ep.RequestTimeout as its timeout).
func New(ep Endpoint, httpClient *http.Client, log *slog.Logger) *Client

// Chat streams one completion with include_usage, and returns it once the
// stream ends. A gap of more than ep.IdleTimeout between chunks is
// ErrStreamStalled. Errors are classified (errors.go).
func (c *Client) Chat(ctx context.Context, req Request) (Response, error)

// Models lists /v1/models' ids.
func (c *Client) Models(ctx context.Context) ([]string, error)

// Version is /api/version's version (API "ollama" only).
func (c *Client) Version(ctx context.Context) (string, error)

// Show is /api/show for model: its capabilities and its context length
// (0 when model_info has no *.context_length).
func (c *Client) Show(ctx context.Context, model string) (ModelInfo, error)

type ModelInfo struct {
	Capabilities  []string
	ContextLength int
}
```

`interfaces.go`: `chatCompleter` exactly as llm-router's (both methods), so
`Chat` is testable with a moq mock. `Client` builds the real one from
`openai.NewClient(option.WithBaseURL(ep.URL+"/v1"),
option.WithAPIKey(token or "ollama"), option.WithHTTPClient(…),
option.WithMaxRetries(0))`. Retries belong to the loop (T5).
`Models`, `Version` and `Show` use plain `net/http` with the bearer token.

**`preflight.go`**

```go
// PreflightResult is what a successful preflight learned.
type PreflightResult struct {
	Version       string // "" for API "openai"
	Capabilities  []string
	ContextTokens int // min(configured, model's context length when known)
	Warnings      []string
}

// Preflight checks, with a 10 s limit per request: the endpoint answers
// /v1/models (and /api/version for API "ollama"), model is listed, and,
// for API "ollama", /api/show's capabilities include "tools". A model
// context length below contextTokens lowers it, with a warning.
func Preflight(ctx context.Context, c *Client, model string, contextTokens int) (PreflightResult, error)
```

**`errors.go`**

```go
var (
	ErrUnreachable  = errors.New("ollama endpoint unreachable")     // dial errors, timeouts, 5xx on preflight
	ErrModelMissing = errors.New("model not on the endpoint")      // not in /v1/models, or 404 on chat
	ErrNoTools      = errors.New("model does not support tools")   // /api/show, or 400 "does not support tools"
	ErrRetryable    = errors.New("retryable endpoint error")       // dial, reset, 429, 502, 503, 504 on chat
	ErrStreamStalled = errors.New("stream stalled")                // also retryable
	ErrStreamDropped = errors.New("stream ended before [DONE]")    // also retryable
)

// ModelMissingError carries the models the endpoint does have.
type ModelMissingError struct{ Model string; Models []string }
// UnreachableError carries the URL and the cause's text.
type UnreachableError struct{ URL, Cause string }
// NoToolsError carries the model's capabilities.
type NoToolsError struct{ Model string; Capabilities []string }

// Retryable reports whether err is worth retrying (ErrRetryable,
// ErrStreamStalled, ErrStreamDropped), and Retry-After when the server
// sent one (0 otherwise).
func Retryable(err error) (bool, time.Duration)
```

Each `*Error` type has `Error()` and `Is(target)`, returning true for its
sentinel, so `errors.Is` and `errors.As` both work.

**Behaviour**

- `Chat` sends `stream: true`, `stream_options.include_usage: true`,
  `tools` (function tools), `max_tokens` and `temperature`. It never sends
  `tool_choice` (Ollama ignores it). The assistant messages it sends back
  carry tool calls built as llm-router's `assistantMessage` does, with no
  reasoning. `traceparent` is set as a header when given.
- Reasoning is read from `reasoning` or `reasoning_content`, as llm-router
  does.
- A 400 whose message contains `does not support tools` → `NoToolsError`.
  404 → `ModelMissingError`. 429/502/503/504 or a connection error →
  `ErrRetryable`, wrapped. A stream that ends without `[DONE]` →
  `ErrStreamDropped`. Other errors are wrapped with the server's message
  (`WrapError`).
- Record fake-ollama responses as fixtures in `testdata/`: a streamed tool
  call, a streamed text answer with usage, `/api/show` with and without
  tools, and `/v1/models`. Tests serve them from `httptest.NewServer`. You
  may hand-write them from the shapes in llm-router's
  `internal/fakeollama/response.go` (T1 isn't needed).

**Acceptance**

- `Chat` against a streamed tool-call fixture → one `ToolCall` with the id,
  name and arguments; `FinishReason` `tool_calls`; usage set.
- The request body has `stream_options.include_usage = true` and no
  `tool_choice`; `Authorization: Bearer t` when the token is `t`;
  `traceparent` when set.
- A server that stops mid-stream → `ErrStreamDropped`. One that sends a
  chunk and then nothing for longer than `IdleTimeout` (use 100ms) →
  `ErrStreamStalled`. 503 with `Retry-After: 7` → `Retryable` gives true
  and 7s. 400 `{"error":"m does not support tools"}` →
  `errors.Is(err, ErrNoTools)`.
- `Preflight`: a closed port → `UnreachableError` with the URL. A missing
  model → `ModelMissingError` with the list. `/api/show` without `tools`
  → `NoToolsError`. A context length of 32768 against 65536 configured →
  `ContextTokens` 32768 and one warning. API `openai` skips `/api/*`.
- `make lint` gains, and passes, a rule that `/v1/chat/completions`
  appears only in `internal/ollama/` (and `_test.go` files):
  `@! grep -rn '"/v1/chat/completions"\|chat/completions' --include='*.go' . | grep -v '^\./internal/ollama/' | grep -v '_test\.go:'`.

**Verify**

```console
$ go generate ./internal/ollama/
$ go test ./internal/ollama/
$ make lint
```

---

## T4. `internal/agent/tools`: the seven tools and guards

**Depends on:** nothing. **Parallel with:** T1, T2, T3.
**ADRs:** [0065](../../adrs/0065-ollama-agent-loop-tools-and-budgets.md)
(Tools, What the agent must not do),
[0066](../../adrs/0066-ollama-runner-security-boundaries.md).

**Read:** `internal/gate/gate.go` (`Request`, `Result`, `Runner.Run`),
`internal/machine/store.go` (`WriteResult`) and `result.go`
(`NormalizeVerdict`, `Result`).

**Create** `internal/agent/tools/{tools.go,files.go,run.go,result.go,guard.go,env.go,interfaces.go}`,
a `_test.go` for each, and `moq_test.go`.

```go
// Package tools is the Ollama runner's tool set (ADR 0065): file tools
// confined to one directory, a shell through internal/gate, and the
// verdict.
package tools

// MaxResult is the largest text a tool returns to the model.
const MaxResult = 16 << 10

// Tool is one tool: its schema for the model and its implementation.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
	// Call runs the tool with the model's raw JSON arguments. A returned
	// error is shown to the model as "error: <err>"; Result.Done ends the run.
	Call func(ctx context.Context, arguments string) (Result, error)
}

// Result is a tool's answer.
type Result struct {
	Text      string
	Truncated bool
	Done      bool   // record_result succeeded
	Verdict   string // with Done
}

// Config is what the tools act on.
type Config struct {
	Dir      string   // the session directory; every path is relative to it
	Root     string   // the workspace directory; paths must stay inside it
	Branch   string   // the role's BRANCH, for the push guard; "" for none
	Verdicts []string // allowed verdicts, pass then fail
	Environ  []string // already scrubbed with Environ
	Recorder recorder // writes the verdict
	Shell    shellRunner
}

// New returns the seven tools in this order: read_file, list_files,
// search, write_file, edit_file, run, record_result.
func New(cfg Config) []Tool

// Environ is environ without tokens and secrets (ADR 0066): names starting
// WS_OLLAMA_TOKEN_ or WS_FORGEJO_TOKEN_, or ending _API_KEY, _TOKEN or
// _SECRET, except GH_TOKEN and GITHUB_TOKEN when keepGit is true; plus
// WS_RUNNER=ollama.
func Environ(environ []string, keepGit bool) []string

// Secrets is the values Environ removed, for the transcript redactor.
func Secrets(environ []string, keepGit bool) []string

// CheckPath resolves p against dir, following symlinks of the existing
// part, and fails unless the result is inside root and not inside
// root/**/.git.
func CheckPath(root, dir, p string) (string, error)

// CheckCommand returns an error naming the rule when command is refused:
// a word "ws" not followed by "machine result"; "claude"; "sudo";
// "git push" with -f/--force/--force-with-lease to a ref other than
// branch; "git push" to main or master. Words are split with
// strings.Fields after replacing ;&|() with spaces.
func CheckCommand(command, branch string) error
```

`interfaces.go`:

```go
//go:generate go tool moq -out moq_test.go . recorder shellRunner

// recorder records the run's verdict, as ws machine result does.
type recorder interface {
	Record(ctx context.Context, verdict string, report []byte) error
}

// shellRunner is *gate.Runner.
type shellRunner interface {
	Run(ctx context.Context, req gate.Request) (gate.Result, error)
}
```

**Tool schemas and behaviour** (descriptions are one sentence each, as
in ADR 0065's table):

- `read_file {path: string (required), offset: integer, limit: integer}`:
  lines `N\t<text>`, at most 400 per call (`limit` is capped). The last
  line says `[file has M lines; use offset to read more]` when there are
  more. Binary files (a NUL in the first 8 KiB) → error `binary file`.
- `list_files {path: string, glob: string}`: walks under the path,
  skipping `.git` and anything `git check-ignore` would ignore (read
  `.gitignore` files with a simple matcher: skip directories named
  `node_modules`, `vendor`, `target`, `dist`, `bin`, plus exact
  `.gitignore` entries). At most 500 paths, sorted, then
  `[N more not shown]`. Don't run git: this package may only run commands
  through `Shell`.
- `search {pattern: string (required), path: string, glob: string}`: Go
  `regexp` over text files under the path, as `path:line: text` (lines cut
  at 200 characters), at most 100 matches.
- `write_file {path, content}` (both required): creates parent
  directories (0o755), writes 0o644, and answers `wrote N bytes to path`.
- `edit_file {path, old, new (required), all: boolean}`: `old` must occur
  exactly once (error `old occurs K times; add context or set all`), or at
  least once with `all`. Answers `replaced K occurrence(s) in path`.
- `run {command: string (required), timeout_seconds: integer}`:
  `CheckCommand` first. Then `Shell.Run` with `gate.Request{Script:
  command, Dir: cfg.Dir, Timeout: default 120s, max 900s}`. The runner's
  `gate.Runner` is built with the scrubbed environ. The answer is `exit
  <code>` then `--- stdout ---` and `--- stderr ---` sections, each cut to
  its last 8 KiB, with `[truncated]` markers. A timeout answers `timed out
  after Ns`.
- `record_result {verdict: string (required), report: string (required)}`:
  `machine.NormalizeVerdict`, which must be in `Verdicts` (error lists them
  otherwise). Then `Recorder.Record`, and `Result{Done: true, Verdict: v}`.
- Arguments are decoded with `json.Unmarshal(…,
  json.RejectUnknownMembers(false))` into a struct per tool. A missing
  required field is an error `missing <field>`.
- Every result is cut to `MaxResult` with `[truncated: N more bytes]`.

**Acceptance** (table tests where 3 or more simple cases):

- `CheckPath`: `a/b.txt` inside → ok. `../x` → error. A symlink inside
  pointing outside → error. `.git/config` and `sub/.git/HEAD` → error. A
  new file in a new directory inside → ok.
- `CheckCommand`: `ws list`, `ws machine spawn x`, `claude logs`, `sudo
  ls`, `git push origin main`, `git push -f origin feat/y` (branch
  `feat/x`), and `make test && ws rm a` → refused. `ws machine result PASS
  --file ../r.md`, `git push -f -u origin feat/x` (branch `feat/x`), and
  `go test ./...` → allowed.
- `Environ`: drops `WS_OLLAMA_TOKEN_GPU`, `OPENAI_API_KEY`, `FOO_SECRET`
  and `NPM_TOKEN`; keeps `GH_TOKEN` with `keepGit`, drops it without;
  keeps `HOME` and `PATH`; adds `WS_RUNNER=ollama`. `Secrets` returns the
  dropped values.
- Each tool: the success path and each error above, against
  `t.TempDir()`. `run` uses the `shellRunnerMock` and asserts `Dir`,
  `Timeout` and the cap at 900. One test runs the real `gate.Runner` with
  `echo hi`. `record_result` rejects `MAYBE` and calls the mock once with
  `PASS`.

**Verify**

```console
$ go generate ./internal/agent/tools/
$ go test ./internal/agent/tools/
$ make lint
```

---

## T5. `internal/agent`: the loop (forked), budgets, compaction, transcript

**Depends on:** T3, T4. **Parallel with:** T1, T2.
**ADR:** [0065](../../adrs/0065-ollama-agent-loop-tools-and-budgets.md)
(all of it).

**Read:** llm-router `internal/agent/agent.go` @9789c32 (`Run` and
`execute`); this repo's `internal/ollama/client.go` (types only) and
`internal/agent/tools/tools.go` (types only); the README's
[agent loop](README.md#the-agent-loop) diagram and preamble; reference.md
[transcript](reference.md#stateidrunnertranscriptjsonl).

**Create** `internal/agent/{agent.go,budget.go,compact.go,transcript.go,prompt.go,preamble.md,interfaces.go,moq_test.go}`
and a `_test.go` for each `.go` file. `agent.go`'s header comment says:
`Forked from github.com/bobcob7/llm-router internal/agent @9789c32 (ADR 0062).`

```go
//go:generate go tool moq -out moq_test.go . chatClient clock

// chatClient is *ollama.Client.
type chatClient interface {
	Chat(ctx context.Context, req ollama.Request) (ollama.Response, error)
}

// clock is time.Now and sleeping, for tests.
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Exit reasons (reference.md › Runner exit reasons).
const (
	ExitDone = "done"; ExitMaxSteps = "max_steps"; ExitTimeout = "timeout"
	ExitMaxTokens = "max_tokens"; ExitEndpointDown = "endpoint_down"
	ExitModelNoTools = "model_no_tools"; ExitModelMissing = "model_missing"
	ExitMalformed = "malformed_tool_calls"; ExitNoResult = "no_result"
	ExitLooping = "looping"; ExitContextOverflow = "context_overflow"
	ExitStopped = "stopped"; ExitInternal = "internal"
)

// Config is one run.
type Config struct {
	Model         string
	ContextTokens int
	MaxOutput     int // max_tokens per request; 4096
	Temperature   float64 // 0.2
	Budget        Budget
	Tools         []tools.Tool
	System        string // rendered preamble
	Task          string // rendered role prompt (first user message)
	TraceID       string // 32 hex; a new span id per request
	Stop          func() bool // true once `stop` exists
	OnStatus      func(Status) // called on every state change
}

// Budget is a run's limits.
type Budget struct {
	MaxSteps  int
	Timeout   time.Duration
	MaxTokens int
}

// Status is what status.json shows.
type Status struct {
	State string // "working", "waiting_model", "exited"
	Step  int
	Usage Usage
	Exit  string // "" while running
	ExitMessage string
}

// Usage is summed over the run.
type Usage struct {
	Requests         int `json:"requests"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Outcome is how a run ended.
type Outcome struct {
	Exit    string
	Message string
	Verdict string // set when the model recorded one (Exit == ExitDone)
	Usage   Usage
	Steps   int
}

// Agent runs the loop.
type Agent struct { /* client, clock, transcript, cfg, log */ }

func New(client chatClient, clk clock, tr *Transcript, cfg Config, log *slog.Logger) *Agent

// Run runs from the start, or from history when it isn't empty (resume),
// until an exit reason. It never returns an error: every failure is an
// Outcome.
func (a *Agent) Run(ctx context.Context, history []ollama.Message) Outcome
```

**Loop** (each rule from ADR 0065, in this order per step):

1. If `Stop()` → `ExitStopped`. If the time, steps or tokens are used up
   → `ExitTimeout`, `ExitMaxSteps` or `ExitMaxTokens`.
2. `Compact` (below). If it can't fit → `ExitContextOverflow`.
3. `Chat`. On `ollama.Retryable` → wait 2s, 8s, 30s, or the server's
   Retry-After when it's longer, up to 60s, logging a `retry` event; after
   3 failed retries → `ExitEndpointDown`. `ErrNoTools` →
   `ExitModelNoTools`. `ErrModelMissing` → `ExitModelMissing`. Anything
   else → `ExitEndpointDown`, with the message.
4. Add the usage. If `prompt_tokens` is under 80% of the estimate → a
   `truncated` event. The second time, lower the working limit to the
   observed `prompt_tokens`.
5. No tool calls → nudge `Call a tool. When the task is done, call
   record_result.` (a user message); the 3rd time in a row →
   `ExitNoResult`.
6. For each call: an unknown name, invalid JSON, or a tool error from
   argument decoding → a tool message `error: <what>; the arguments must
   match: <compact JSON schema>`, and an `invalid_call` event. 3 invalid in
   a row → `ExitMalformed`. A valid call runs; its result is sent as
   `<tool_result tool="NAME">…</tool_result>`. The same name and arguments
   as the previous 2 calls → also send the nudge `You already did this;
   its result is above. Do something else.`; the 5th in a row →
   `ExitLooping`.
7. A tool `Result.Done` → `ExitDone`, with the verdict.

**Budget defaults** come from the caller (T6). `budget.go` has `type
budgetState struct` with `Spend(Usage)`, `Step()`, `Exceeded(now)
(string, bool)`.

**Compaction** (`compact.go`):

```go
// EstimateTokens is lastPrompt + ceil(bytes added since / 3).
func EstimateTokens(lastPrompt, bytesSince int) int

// Compact returns messages fitted under limit = contextTokens - maxOutput:
// above 70% of limit, it elides tool results (not in the last 6 messages)
// oldest first until under 50%; if still above 70%, it drops whole
// assistant+tool turns after messages[1] and inserts one user message
// "Progress so far (written by ws, not by you):" listing write_file and
// edit_file paths and run commands with their exit codes, from done. ok is
// false when messages[0], messages[1] and the last turn alone exceed limit.
func Compact(messages []ollama.Message, estimate, limit int, done []ToolRecord) (out []ollama.Message, ev CompactionEvent, ok bool)

type ToolRecord struct{ Name, Summary string; ExitCode *int }
type CompactionEvent struct{ BeforeTokens, AfterTokens, Elided, DroppedTurns int }
```

Elided text: `[elided by ws: <tool> <first 80 chars of the arguments>,
<N> lines; run it again if you need it]`. Use a byte-based estimate for
the messages being compacted (`len/3`).

**Transcript** (`transcript.go`):

```go
// Transcript appends events to transcript.jsonl, redacting secrets.
type Transcript struct { /* file, secrets, mu */ }

func OpenTranscript(path string, secrets []string) (*Transcript, error) // O_APPEND|O_CREATE, 0o600
func (t *Transcript) Write(ev Event) error   // one json/v2 line, secrets → "[redacted]"
func (t *Transcript) Close() error

// Event is one line; Kind is start, request, response, tool_result,
// invalid_call, nudge, compaction, truncated, retry or exit, and only the
// fields of that kind are set (omitzero).
type Event struct {
	Time            time.Time       `json:"time"`
	Step            int             `json:"step"`
	Kind            string          `json:"kind"`
	// start
	Model           string          `json:"model,omitzero"`
	Messages        []EventMessage  `json:"messages,omitzero"`
	Resume          bool            `json:"resume,omitzero"`
	// … one field per column of reference.md's table …
}

// ReadHistory rebuilds the conversation from a transcript for --resume:
// start's messages, then each response as an assistant message and each
// tool_result, invalid_call and nudge as the message that was sent. It
// returns the last step number too.
func ReadHistory(path string) (messages []ollama.Message, step int, err error)

// Tail returns the last n events (all when n <= 0) and whether some were left out.
func Tail(path string, n int) ([]Event, bool, error)
```

**Prompt** (`prompt.go` + `preamble.md`, embedded with `//go:embed`):

```go
// PreambleData fills preamble.md.
type PreambleData struct{ Dir string; StepsLeft, MinutesLeft int; Verdicts string }
func Preamble(d PreambleData) (string, error)
```

`preamble.md` is the README's preamble text, verbatim, with these fields.

**Acceptance** (all with `chatClientMock` and `clockMock`; no network):

- Happy path: write_file → run → record_result PASS → `Outcome{Exit:
  "done", Verdict: "PASS", Steps: 3}`. The transcript has `start`, 3
  `request`/`response` pairs, 3 `tool_result`s and `exit`, in order.
- Each exit reason from a scripted mock: `max_steps` (always calls
  `read_file`), `timeout` (the clock jumps), `max_tokens`,
  `endpoint_down` (4 retryable errors; the sleeps were 2s, 8s, 30s),
  recovery (2 retryable errors, then success), `model_no_tools`,
  `malformed_tool_calls` (3 calls with arguments `{"path":`), recovery
  after one malformed call, `no_result` (3 text replies), `looping` (the
  same call 5 times; the nudge was sent after the 3rd), `stopped`.
- `Compact`: elides oldest first and keeps the last 6; drops turns and
  inserts the progress note; returns `ok=false` for an oversized task.
  `EstimateTokens(1000, 300)` = 1100.
- The truncation check lowers the limit after two `truncated` events.
- `ReadHistory` of a transcript written by a run gives the same messages
  the next request would have sent. `Write` redacts a secret that appears
  in a tool result.
- `Preamble` renders all four fields.

**Verify**

```console
$ go generate ./internal/agent/
$ go test ./internal/agent/...
$ make lint
```

---

## T6. `internal/runner`, session kind `ollama`, lifetime, `ws runner run`

**Depends on:** T2, T5. **Parallel with:** T1.
**ADR:** [0063](../../adrs/0063-ollama-children-are-ws-supervised-sessions.md).

**Read:** `internal/workspace/session.go`, `internal/lifetime/{interfaces.go,judge.go,remove.go,restore.go}`
(the functions that switch on `SessionShell` or `ClaudeSessionID`),
`internal/claude/daemon.go` (how the service is started apart:
`Setsid`, `/dev/null`), `internal/cli/list.go` (`listSessionJSON`),
`internal/cli/app.go` (`AddCommand`), `internal/machine/role/role.go`
(`Render`), reference.md [Files](reference.md#files).

**Create**

- `internal/runner/{runner.go,files.go,process.go,interfaces.go}` with
  tests and `moq_test.go`.
- `internal/cli/runner.go` and `runner_test.go`: the hidden command.

```go
// Package runner keeps an Ollama runner session's files and process
// (ADR 0063). It is the only package that starts `ws runner run`.
package runner

const SessionPrefix = "ol-"

// NewSessionID is "ol-" + 8 random lowercase hex digits.
func NewSessionID() string

// Run is run.toml (reference.md).
type Run struct {
	Version        int           `toml:"version"`
	Session        string        `toml:"session"`
	Endpoint       string        `toml:"endpoint"`
	URL            string        `toml:"url"`
	API            string        `toml:"api"`
	Model          string        `toml:"model"`
	ContextTokens  int           `toml:"context_tokens"`
	RequestTimeout time.Duration `toml:"request_timeout"`
	IdleTimeout    time.Duration `toml:"idle_timeout"`
	Role           string        `toml:"role"`
	Pass           []string      `toml:"pass"`
	Fail           []string      `toml:"fail"`
	MaxSteps       int           `toml:"max_steps"`
	Timeout        time.Duration `toml:"timeout"`
	MaxTokens      int           `toml:"max_tokens"`
	Branch         string        `toml:"branch"`
	Prompt         string        `toml:"prompt"`
	CreatedAt      time.Time     `toml:"created_at"`
}

// Status is status.json (reference.md); Exit and ExitMessage are null
// while running.
type Status struct {
	Version          int         `json:"version"`
	Session          string      `json:"session"`
	PID              int         `json:"pid"`
	ProcessStartedAt time.Time   `json:"process_started_at"`
	State            string      `json:"state"`
	Step             int         `json:"step"`
	MaxSteps         int         `json:"max_steps"`
	Usage            agent.Usage `json:"usage"`
	Exit             *string     `json:"exit"`
	ExitMessage      *string     `json:"exit_message"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// UsageRecord is usage.json under the root's machine/<child>/ (reference.md).
type UsageRecord struct { /* fields as reference.md, json/v2 */ }

// Files are one session's paths: <state>/<id>/runner/...
type Files struct{ Dir string }
func FilesFor(stateDir, id string) Files
func (f Files) WriteRun(r *Run) error
func (f Files) ReadRun() (Run, error)
func (f Files) WriteStatus(s *Status) error // temp+rename
func (f Files) ReadStatus() (Status, error) // fs.ErrNotExist when missing
func (f Files) Transcript() string
func (f Files) Log() string
func (f Files) RequestStop() error
func (f Files) StopRequested() bool

// State is lifetime's reading of a session (ADR 0063 table):
// "working", "starting", "idle", "stopped", "unknown".
func (f Files) State(now, recordedAt time.Time, procs processReader) (state string, pid int, st *Status)

// Starter starts and stops runner processes.
type Starter struct { /* exe path (os.Executable), environ, log */ }
func NewStarter(exe string, environ []string, log *slog.Logger) *Starter
// Start runs `<exe> runner run [--resume] <id>` with Setsid, stdin and
// stdout on /dev/null, stderr appended to Files.Log(), the dir given, and
// waits up to 10s for status.json; ErrStartFailed otherwise.
func (s *Starter) Start(ctx context.Context, f Files, id, dir string, resume bool) error
// Stop writes stop, SIGTERMs the process group of status.json's pid if it
// is alive and its start time matches, waits up to 10s, then SIGKILLs.
func (s *Starter) Stop(ctx context.Context, f Files, procs processReader) error

var ErrStartFailed = errors.New("runner did not start")

//go:generate go tool moq -out moq_test.go . processReader
type processReader interface{ StartTime(pid int) (time.Time, error) }
```

**`internal/workspace/session.go`**: `SessionOllama SessionKind =
"ollama"`. `Session` gains `RunnerSessionID string
`toml:"runner_session_id,omitempty"``. Add `RecordRunnerSession(id,
runnerSessionID string) error`, which writes `version = 2`. `check`
accepts `ollama` with a non-empty `runner_session_id`, starting `ol-`.
`Session` reads versions up to 2. Keep `formatVersion`'s meaning for the
other kinds, and add `ollamaSessionVersion = 2`.

**`internal/lifetime`**: a new `runnerReader` interface in `interfaces.go`
(and regenerate the mocks):

```go
// runnerReader reads and stops Ollama runner sessions (ADR 0063).
type runnerReader interface {
	State(id string, recordedAt time.Time) (state string, pid int, st *runner.Status)
	Exists(id string) bool                     // state/<id>/runner exists
	Stop(ctx context.Context, id string) error
	Resume(ctx context.Context, ws *workspace.Workspace) error
}
```

`Manager` gets it through a new setter `UseRunners(r runnerReader)`
(nil-safe: without it, `ollama` sessions read as `unknown` and are kept).

- `judge`: `case rec.sess.Kind == workspace.SessionOllama:` → `Session{Kind:
  KindAgent, ID: runner id, State: …, PID: …, Runner: "ollama", Ollama:
  st}`. It's gone only when `!Exists(id)`.
- `lifetime.Session` gains `Runner string` (`"claude"` for Claude agents,
  `""` for shells) and `Ollama *runner.Status`.
- `remove.go`: for `SessionOllama`, call `Stop` and then remove as today.
- `restore.go`: for `SessionOllama` with state `stopped`, call `Resume`.
  The `Restored` item has `SessionID` set to the runner id. `restart.go`
  is unchanged.

**`internal/cli/list.go`**: `listSessionJSON` gains `Runner *string
`json:"runner"`` and `Ollama *listOllamaJSON `json:"ollama"`` after
`stale`, filled from `lifetime.Session` (shape in reference.md). Also wire
`UseRunners` where the Manager is built.

**`ws runner run [--resume] <id>`** (`internal/cli/runner.go`), with
`Hidden: true`, and not added to `guideTopics` (`TestGuideTopics` skips
hidden commands; if it doesn't, add a skip for `Hidden` commands in that
test). It:

1. reads `run.toml`, and writes `status.json` (`starting`, pid,
   `process_started_at` from `internal/proc`);
2. builds the `ollama.Client` (the token from `config.LookupToken`), the
   tools (`tools.Environ(os.Environ(), cfg.Ollama.KeepsGitTokens())`, the
   recorder wrapping `machine.NewStore(paths.State).WriteResult` with the
   root and ancestors computed as `ws machine result` does), the
   transcript (secrets: the token plus `tools.Secrets`), and the preamble;
3. runs `agent.Run` (with `ReadHistory` for `--resume`), mapping
   `OnStatus` to `WriteStatus`;
4. on exit: when `Outcome.Verdict == ""` and the exit isn't `stopped`,
   records `Fail[0]` (or `FAIL`) with the report `ws ollama runner:
   <exit>\n\n<message>\n\n## Last events\n` plus the last 20 transcript
   events as a JSON-lines code block; writes `usage.json` under the root;
   writes the final `status.json` with `exit`; exits 0.

**Role template**: `role.Render` passes `"Runner"` in the template data
(`"claude"` unless the new `RenderFor(vars, extra, runner string)` is
called; `Render` calls `RenderFor(..., "claude")`). In
`internal/machine/role/roles/implementer.md`, step 3's trailer becomes
`{{if eq .Runner "ollama"}}'Generated-By: ws ollama runner'{{else}}'Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>'{{end}}`.
The existing role tests must still render the same text for Claude.

**Makefile lint rule** (ADR 0063): `runner run` is started only from
`internal/runner/`:
`@! grep -rn '"runner", "run"' --include='*.go' . | grep -v '^\./internal/runner/' | grep -v '_test\.go:'`.

**docs/state.md**: add `runner/` (run.toml, status.json,
transcript.jsonl, runner.log, stop) and `machine/<child>/usage.json`.

**Acceptance**

- `Files.State`: no status and recorded 3s ago → `starting`; no status
  and 20s → `stopped`; status `working` with a live pid whose start time
  matches → `working`; the same pid with a different start time →
  `stopped`; `exit` set → `idle` with pid 0; invalid JSON → `unknown`.
- `Starter.Start` with `exe` a test script (`internal/exectest`) that
  writes `status.json` → ok; one that sleeps → `ErrStartFailed` within
  10s (use a 200ms timeout in tests via an unexported field).
  `Stop` kills a `sleep 60` started in its own group.
- Session records: `RecordRunnerSession` then `Session` round-trips. A
  version 3 record → "written by a newer ws". `kind = "ollama"` without
  `runner_session_id` → an error.
- Lifetime with `runnerReaderMock`: `List` shows `kind agent`, `runner
  ollama`, and the mock's state; removal removes the workspace only when
  `Exists` is false; `RemoveTree` calls `Stop`; `Restore` calls `Resume`
  for `stopped` only. Without `UseRunners`: `unknown` and kept.
- `ws list --json` of an `ollama` workspace (store and lifetime mocks)
  matches reference.md's `session` shape, with `runner` `"claude"` for a
  Claude agent and `null` for a shell. Existing list tests pass.
- `ws runner run` against an `httptest` server that replays T3's
  fixtures: a 3-step happy path writes `result.toml` PASS, `usage.json`,
  and the final status `exit "done"`; an endpoint returning 503 forever
  records `FAIL` with a report starting `ws ollama runner: endpoint_down`
  (fast backoff through an unexported clock).
- The implementer role renders with `Runner = "ollama"` to the
  `Generated-By` trailer, and as before otherwise.

**Verify**

```console
$ go generate ./internal/runner/ ./internal/lifetime/
$ go test -p 2 ./internal/runner/ ./internal/workspace/ ./internal/lifetime/ ./internal/machine/... ./internal/agent/...
$ go test ./internal/cli/ -run 'TestApp_.*List|TestApp_.*Runner|TestGuideTopics'
$ make lint
```

---

## T7. `ws machine spawn --runner`, `logs`, wait/children/list fields, guides, docs, e2e

**Depends on:** T1 (merged in llm-router), T6. **Parallel with:** none.
**ADRs:** [0064](../../adrs/0064-choosing-the-runner-endpoint-and-model.md),
[0063](../../adrs/0063-ollama-children-are-ws-supervised-sessions.md),
[0067](../../adrs/0067-ollama-runner-tests-use-fake-ollama.md).

**Read:** `internal/cli/machine.go` (`newMachineSpawnCmd`,
`runMachineSpawn`, `spawnRole`), `internal/cli/claude.go` (`runClaude`,
`startAgent`), `internal/cli/machine_wait.go` (`machineWaitItemJSON`),
`internal/cli/errors.go`, `internal/cli/llm.go` (`guideTopics`),
reference.md (all of it), [guides/](guides/), [fake-ollama/](fake-ollama/).

**Change**

- `internal/cli/errors.go`: codes `endpoint_unknown` (3),
  `ollama_unreachable` (1), `ollama_model_missing` (3), `ollama_no_tools`
  (1), `runner_failed` (1). Map `ollama.UnreachableError`,
  `ModelMissingError` and `NoToolsError` with their `details` (reference.md),
  `config.ErrEndpointUnknown` with `details.endpoints`,
  `config.ErrNoModel` to `config_invalid`, and `runner.ErrStartFailed`
  with `details.log`.
- `internal/cli/machine.go`: the flags `--runner` and `--endpoint`.
  `runMachineSpawn` picks the runner (flag, then
  `cfg.Ollama.RunnerFor(role)`). For `ollama` it calls the new
  `runOllamaChild(ctx, opts)` in **new** `internal/cli/machine_ollama.go`:
  1. the `usage` checks (reference.md);
  2. `cfg.Ollama.Resolve(role, endpoint, model)`;
  3. `ollama.Preflight` with the token from `config.LookupToken`. On a
     preflight error with `fallback = "claude"` → add the warning and run
     the Claude path. Otherwise return the error;
  4. `a.workspaces.Create` (as `runClaude` does, holding the lock);
  5. `runner.FilesFor(...).WriteRun` with the rendered prompt
     (`ro.RenderFor(vars, extra, "ollama")`, or the prompt after `--`
     without a role), budgets, and `branch` = `vars["BRANCH"]`;
  6. `a.runners.Start(ctx, files, id, ws.SessionDir(), false)`;
  7. `RecordRunnerSession`; then `writeSpawn` for a role;
  8. print `machineSpawnJSON` plus `runner` and `ollama` (add those fields
     to `machineSpawnJSON`; for Claude children `runner` is `"claude"` and
     `ollama` is `null`).
  Any failure after step 4 removes the workspace (and stops the runner),
  as `startAgent` does.
- `internal/cli/machine_wait.go` and `runMachineChildren`: each item gains
  `runner` and `ollama` (from the live session, else from `usage.json`
  for a gone child). `ollama.usage_file` is `usage.json`'s path, or
  `null`.
- **New hidden** `ws runner check [--model <m>] <endpoint>` in
  `internal/cli/runner.go`, for the desktop app: `Resolve("", endpoint,
  model)`, then `ollama.Preflight`. It prints
  `{"endpoint","url","api","model","version","capabilities","context_tokens","models","warnings"}`,
  or the preflight error codes. It needs no workspace.
- **New** `internal/cli/machine_logs.go`: `ws machine logs [--tail N]
  <workspace>`, with the caller and descendant checks as in `wait`. For
  `ollama`, `agent.Tail`; for `claude`, `a.claude.Logs(ctx, session)`. Add
  `Logs(ctx, id) (string, error)` to `internal/claude` (runs `claude logs
  <id>`; a short test with `exectest`) and to the cli `claudeClient`
  interface; regenerate the mocks.
- Guides: `cp docs/design/ollama-agent/guides/ws-machine-spawn.md
  docs/design/ollama-agent/guides/ws-machine-wait.md
  docs/design/ollama-agent/guides/ws-machine-logs.md internal/cli/guides/`.
  Add the topic `"machine logs"` after `"machine children"` in
  `guideTopicsBeforeReview`. In `internal/cli/guides/ws.md`: add the
  bullet `` - `ws llm machine logs`: print a child's log: an Ollama child's
  transcript, or a Claude child's `claude logs` `` after the `machine
  children` bullet; add the five codes to the exit-code table (row 1:
  `ollama_unreachable`, `ollama_no_tools`, `runner_failed`; row 3:
  `endpoint_unknown`, `ollama_model_missing`); and in "Rules for agents"
  change "follow it with `claude logs <session>`." to "follow it with `ws
  machine logs <id>`.". In `internal/cli/guides/ws-list.md`, after the
  `session.stale` bullet, add: ``- `session.runner`: `claude` or `ollama`
  for an agent, `null` for a shell. `session.ollama`: for an Ollama child,
  its endpoint, model, step, usage and `exit` (see `ws llm machine
  wait`); otherwise `null`.``
- `internal/cli/guides/ws-machine.md`: add `ws machine logs` to the
  synopsis and the "When to use it" list, and the five codes to its table.
- `docs/tools/ws-machine.md`: apply the
  [proposed diff](README.md#proposed-changes-to-docstoolsws-machinemd).
  `docs/tools/ws-list.md`: the two `session` fields.
- **e2e** (new, build tag `e2e`):
  - `e2e/ollama/FAKE_OLLAMA_REF`: T1's merge SHA (from the orchestrator).
  - `Makefile`: the targets
    ```make
    FAKE_OLLAMA_REF := $(shell cat e2e/ollama/FAKE_OLLAMA_REF)
    LLM_ROUTER_URL := https://git.bobcob7.com/forgejo_admin/llm-router.git
    fake-ollama:
    	@if [ -n "$$WS_FAKE_OLLAMA" ]; then exit 0; fi; \
    	if [ ! -d $(BIN)/llm-router-src/.git ]; then git clone -q $(LLM_ROUTER_URL) $(BIN)/llm-router-src; fi; \
    	git -C $(BIN)/llm-router-src fetch -q origin && git -C $(BIN)/llm-router-src checkout -q $(FAKE_OLLAMA_REF) && \
    	cd $(BIN)/llm-router-src && go build -o ../fake-ollama ./cmd/fake-ollama
    e2e-ollama: build fake-ollama
    	go test -tags e2e -p 1 -timeout 15m ./e2e/ollama/
    ```
    and `.PHONY` entries.
  - `e2e/ollama/harness_test.go`: `startFake(t, config string) *fake`
    starts `$WS_FAKE_OLLAMA` or `bin/fake-ollama` with `--config`,
    `--listen 127.0.0.1:0` and `--control-listen 127.0.0.1:0`. It parses
    `API listening on <addr>` and `control API listening on <addr>` from the
    JSON log's `msg` (regex as in llm-router's harness), and stops it with
    SIGINT. When the binary is missing: `t.Fatal("fake-ollama not found;
    run make fake-ollama")`. Helpers: `setFaults`, `requests`, and
    `newEnv(t)`, which makes a `WS_CONFIG_DIR`, a bare upstream repo with
    one commit, a fake `claude` (`internal/exectest`) answering
    `--version`, `--bg` and `agents --json`, a parent workspace record, and
    `config.toml` pointing endpoint `fake` at the fake (`api =
    "ollama"`, `context_tokens = 32768`, `model = "fake-coder"`). Also
    `runWS(t, env, args...) (stdout, stderr string, code int)` running
    `bin/ws` from the parent workspace's directory.
  - `e2e/ollama/scenarios_test.go`: one `t.Run` per scenario in
    [0067](../../adrs/0067-ollama-runner-tests-use-fake-ollama.md)'s
    table, using the configs copied from
    [fake-ollama/](fake-ollama/) (`cp docs/design/ollama-agent/fake-ollama/*.yaml
    e2e/ollama/testdata/`), waiting with `ws machine wait --timeout 2m`.
  - `e2e/ollama/realmodel_test.go`: when `E2E_OLLAMA_URL` is set, run
    `edit-file` and `review-verdict` against it with `E2E_OLLAMA_MODEL`
    (default `qwen3-coder:30b`), asserting only `ollama.exit == "done"` and
    a recorded verdict; otherwise `t.Skip("E2E_OLLAMA_URL not set")`.
  - `.github/workflows/ci.yml`: a job `e2e-ollama` (ubuntu-latest, Go
    1.27.x) running `make e2e-ollama`.

**Acceptance**

- Unit (`internal/cli`, with mocks): `--runner ollama` without a role
  and without a model anywhere → `config_invalid`. `--endpoint` with
  `--runner claude` → `usage`. `-- "x" --permission-mode auto` for ollama
  → `usage`. Each preflight error → its code and `details`, and no
  workspace is created. With `fallback = "claude"` → a Claude child with
  `runner: "claude"` and the warning. Success → the reference.md JSON, the
  session recorded as `ollama`, and `spawn.toml` for a role. A Claude
  spawn's JSON gains `"runner": "claude", "ollama": null` and is otherwise
  byte-identical.
- `ws machine logs` → reference.md's shape for both runners;
  `not_descendant` for a sibling.
- `TestGuideTopics` passes. `go test ./internal/cli/` passes.
- `make e2e-ollama` passes all 12 scenarios from a clean checkout. With
  the binary deleted and `WS_FAKE_OLLAMA` pointing nowhere, it fails with
  the "run make fake-ollama" message.

**Verify**

```console
$ go test -p 2 ./internal/cli/ ./internal/claude/
$ go test ./internal/cli/ -run TestGuideTopics
$ make lint
$ make build && make e2e-ollama
```

---

## T8. Desktop: Ollama badge, transcript view, Settings › Models

**Depends on:** T7. **Parallel with:** none.
**Design:** the [Screens](README.md#screens) table and
[mockups.html](mockups.html) (`#agents-tree`, `#transcript`,
`#settings-models`, `#settings-models-errors`). Match the PNGs in
[img/](img/).

**Read:** `desktop/src-tauri/src/model.rs` (`WsSession`,
`workspace_entry`), `desktop/src-tauri/src/transcript.rs` (the public
fns), `desktop/src-tauri/src/ws_config.rs`,
`desktop/src/features/agents/{EntryRow.tsx,EntryPane.tsx,AgentsActions.tsx}`,
`desktop/src/features/conversation/{rows.ts,ConversationView.tsx}`,
`desktop/src/features/settings/{SettingsPane.tsx,SettingsList.tsx,ForgesGroup.tsx,Row.tsx}`,
`desktop/src/api.ts`, the Tauri mock used for screenshots (`desktop/src/test/fixtures.ts`).

**Change**

- `model.rs`: `WsSession` gains `runner: Option<String>` and `ollama:
  Option<OllamaSession>` (`endpoint`, `model`, `step`, `max_steps`,
  `usage {requests, prompt_tokens, completion_tokens}`, `exit:
  Option<String>`), all `#[serde(default)]`. `Entry` gains `runner:
  String` (`"claude"`, `"ollama"`, or `""` for a shell) and `ollama:
  Option<OllamaSession>` (camelCase out). For `runner == "ollama"`, don't
  look up a Claude job (`job_for`): the status comes from `state` alone.
- `transcript.rs`: `pub fn runner_transcript(state_dir, id) ->
  Vec<ConversationRow>`, which reads `state/<id>/runner/transcript.jsonl`
  and maps `response` → assistant text (and reasoning), `tool_calls` →
  tool rows (name plus the first 120 characters of the arguments),
  `tool_result` → result rows (ok/error, bytes, the first 20 lines),
  `retry`, `compaction`, `truncated`, `nudge` and `invalid_call` → a muted
  system row, and `exit` → a final banner row (`done` green, others amber
  with the reason). The existing conversation command dispatches on the
  entry's runner.
- `EntryRow.tsx`: after the name, a chip `OLLAMA` (class
  `runnerChip`: 10px, uppercase, `--status-running` at 18% background) when
  `entry.runner === "ollama"`. The summary line is `{model} @ {endpoint} ·
  step {step}/{maxSteps}`.
- `EntryPane.tsx` and `AgentsActions.tsx`: for Ollama entries, hide
  Attach, Send/reply box and Continue, and show a meta row: endpoint chip,
  model chip, `step n/max`, `tokens 512k in · 18k out`, and the exit
  reason when finished.
- **New** `desktop/src/features/settings/ModelsGroup.tsx` (plus
  `.module.css` and `.test.tsx`), listed as "Models" in `SettingsList`
  after "Forges". Sections:
  1. *Endpoints*: a table (name, URL, API, default model, context, status
     dot) with Add, Edit and Remove. Add/Edit opens a form with name, URL,
     API (select: Ollama or OpenAI-compatible / llm-router), default model,
     context tokens, request and idle timeouts, and a token field (saved
     with `ollama_token_set`; see 2). Saving runs `ws config set` for each
     changed key.
  2. *Test connection* per endpoint: a new Tauri command `ollama_check`
     runs `ws runner check --json <endpoint> [--model <m>]` (added in T7)
     through `runner.rs` and shows its result:
     `✓ 4 models · qwen3-coder:30b supports tools · context 65536`, or the
     error banners in `#settings-models-errors` for `ollama_unreachable`,
     `ollama_model_missing` and `ollama_no_tools`. A token typed in the form
     is saved first, by a new Tauri command `ollama_token_set(name, token)`
     that writes `CONFIG_DIR/ollama-tokens.json` (0600, temp file and
     rename), modelled on `reviews/tokens.rs`. The frontend never receives
     a token back, only `token_source` (`env`, `file` or `null`).
  3. *Roles*: one row per role (`implementer`, `reviewer`, `verifier`,
     plus the user's roles from `ws machine roles`) with a runner select
     (Claude/Ollama), and, for Ollama, endpoint and model selects. Changes
     run `ws config set ollama.roles.<role>.runner …` etc.
  4. *Fallback*: a switch, "When an endpoint is down, start a Claude child
     instead", setting `ollama.fallback` to `claude` or `none`.
- The Tauri mock gains Ollama fixtures for `list_entries`, `settings_get`
  (an `ollama` block), `ollama_check`, and an Ollama conversation, so the
  screenshot child can render the four screens.

**Acceptance**

- Rust: `workspace_entry` maps an Ollama session (runner and ollama
  fields set; no job lookup). `runner_transcript` maps a fixture JSONL
  with every event kind. `cargo test` and `cargo clippy --all-targets -- -D
  warnings` pass (default and `--no-default-features --features online`).
- TS: an Ollama entry shows the chip and summary, and hides Attach and
  Send. `ModelsGroup` renders the endpoints, calls `ws config set` with the
  right keys on save, shows a check result and both error states, and
  saves role routing. `npm run typecheck` and `npm test --
  --maxWorkers=2 --testTimeout=60000` pass.
- Layout: at 1280×800 and 390×844, no clipped inputs, no horizontal
  scroll, and tap targets of at least 40px on phone widths. A screenshot
  child checks this; put it in the report.

**Verify**

```console
$ cd desktop && npm ci && npm run typecheck && npm test -- --maxWorkers=2 --testTimeout=60000
$ cd desktop/src-tauri && cargo test && cargo clippy --all-targets -- -D warnings
$ cd desktop/src-tauri && cargo test --no-default-features --features online
```
