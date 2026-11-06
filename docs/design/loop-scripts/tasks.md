# Loop scripts and variables: implementation tasks

Implements [0049](../../adrs/0049-loop-scripts-and-variables.md) and the
script, variable, and template parts of [`ws loop`](../../tools/ws-loop.md).
Each task builds, passes its tests, and can be merged on its own. Each
task is one PR (conventional commit, imperative mood, e.g. `feat: add
loop record version 2 with scripts and vars (ADR 0049)`).

**Order and parallelism**

| Task | Title | Depends on | Can run in parallel with |
| ---- | ----- | ---------- | ------------------------ |
| T1 | `internal/loop`: record version 2, vars, script timeout, prompt templates | nothing | T2 |
| T2 | `internal/gate`: run a gate script | nothing | T1 |
| T3 | `ws loop add` flags and the loop JSON | T1 | (T2, if T2 is still open) |
| T4 | `ws loop tick` and `ws loop run`: run the gate | T1, T2, T3 | T5 |
| T5 | Desktop backend: new add arguments and loop fields | T3 | T4 |
| T6 | Desktop UI: script, variables, and last check | T5 | (T4, if T4 is still open) |

**Read before any task:** `docs/tools/ws-loop.md` (the behavior and every
JSON shape) and the "Decision" section of
`docs/adrs/0049-loop-scripts-and-variables.md`. When this file and those
two disagree, they win.

**Rules for every task**

- Go: follow the existing code in the package you edit. Errors are
  wrapped with `fmt.Errorf("…: %w", err)`, lower-case, no "failed to".
  Tests use `github.com/stretchr/testify/assert` and `require`, one test
  function per function or method with `t.Run` subtests per behavior,
  `t.Context()` instead of `context.Background()`, and `t.TempDir()` for
  files. Call `t.Parallel()` only in tests that run processes or touch
  files.
- Run `make fmt` before committing. `make lint` must pass: it forbids
  running `git`, `claude`, or `gh` outside their packages. Never call
  `exec.Command` in new code, except in `internal/gate` (T2).
- JSON fields are `snake_case`; times are `formatTime(t)` (RFC 3339, UTC,
  whole seconds); an empty optional string is `null` (use `optional(s)`
  from `internal/cli/cache.go`); lists are `[]` and maps `{}`, never
  `null`.
- Don't change any existing JSON field, error code, or exit code. New
  JSON fields go after the existing ones.

---

## T1. `internal/loop`: record version 2, vars, script timeout, prompt templates

**Depends on:** nothing. **Parallel with:** T2.

**Create**

- `internal/loop/prompt.go`
- `internal/loop/prompt_test.go`

**Modify**

- `internal/loop/loop.go`
- `internal/loop/loop_test.go`
- `internal/loop/store.go`
- `internal/loop/store_test.go`: the two tests that write
  `"version = 2\nname = \"future\"\n"` as a newer record now write
  `version = 3` (in both the file and the asserted content).

**`loop.go`**: change `FormatVersion` to 2 and add:

```go
// FormatVersion is the newest loop record version this ws reads and writes.
const FormatVersion = 2

// Version1 is the record version of loops added before ADR 0049: no
// script, no vars, and a literal prompt.
const Version1 = 1

// Bounds of a loop's script timeout and vars (ADR 0049).
const (
	DefaultScriptTimeout = time.Minute
	MinScriptTimeout     = time.Second
	MaxScriptTimeout     = 10 * time.Minute
	MaxVars              = 32
	MaxVarValueLen       = 4096
	maxVarKeyLen         = 64
)

// Outcomes of a loop's gate script, as last_gate.outcome records them.
const (
	GatePassed   = "passed"
	GateSkipped  = "skipped"
	GateTimedOut = "timed_out"
	GateFailed   = "failed"
)

// Add to the existing var block:
var (
	// ErrSkipped is a run not started because the loop's script exited non-zero.
	ErrSkipped = errors.New("loop script exited non-zero")
	// ErrScriptFailed is a loop script that timed out or couldn't start.
	ErrScriptFailed = errors.New("loop script failed")
)

// Gate is the outcome of the last time a loop's script ran.
type Gate struct {
	At       time.Time `toml:"at"`
	Outcome  string    `toml:"outcome"`
	ExitCode int       `toml:"exit_code"` // -1 when it timed out, was killed by a signal, or couldn't start
	Message  string    `toml:"message"`
}
```

Add these fields to `Loop`, after `NextRunAt` and before `Runs`:

```go
	Script        string            `toml:"script,omitempty"`
	ScriptTimeout string            `toml:"script_timeout,omitempty"`
	Vars          map[string]string `toml:"vars,omitempty"`
	LastGate      *Gate             `toml:"last_gate,omitempty"`
```

New functions:

```go
// ValidateVarKey checks a var key: 1–64 bytes of [A-Za-z0-9_], not
// starting with a digit.
func ValidateVarKey(key string) error

// ParseVars parses --var arguments, KEY=VALUE each, split at the first
// '='. VALUE may be empty or contain '='. A missing '=', a bad key, a key
// given twice, a value over MaxVarValueLen bytes, or more than MaxVars
// is ErrInvalid. No arguments is a nil map.
func ParseVars(args []string) (map[string]string, error)

// ParseScriptTimeout parses a Go duration of whole seconds in
// [MinScriptTimeout, MaxScriptTimeout].
func ParseScriptTimeout(s string) (time.Duration, error)

// FormatScriptTimeout prints d without zero units: 1m, 1m30s, 45s, 10m.
func FormatScriptTimeout(d time.Duration) string

// Timeout is the script's timeout: DefaultScriptTimeout when
// ScriptTimeout is empty, else ParseScriptTimeout(l.ScriptTimeout).
func (l *Loop) Timeout() (time.Duration, error)

// Templated reports whether the prompt is a template: every version but
// Version1 (a new, unsaved Loop has Version 0 and is templated).
func (l *Loop) Templated() bool
```

- Every validation error wraps `ErrInvalid` and says what's wrong, e.g.
  `fmt.Errorf("%w: var key %q must be letters, digits, or '_', not starting with a digit", ErrInvalid, key)`.
- `FormatScriptTimeout`: take `d.String()`; if it ends in `"m0s"`, strip
  the trailing `"0s"`; then if it ends in `"h0m"`, strip the trailing
  `"0m"` (`"1m0s"` → `"1m"`, `"1m30s"` stays, `"45s"` stays,
  `"10m0s"` → `"10m"`). Don't reuse `FormatEvery`: it strips `"0s"` from
  `"10s"`.
- `Validate()` additionally checks, in this order: `len(Vars) <=
  MaxVars`, each key with `ValidateVarKey`, each value `<=
  MaxVarValueLen`; if `Script == ""` and `ScriptTimeout != ""` →
  `ErrInvalid` "script_timeout without a script"; if `Script != ""`,
  `l.Timeout()` parses; finally `l.CheckPrompt()`.

**`prompt.go`**

```go
// PromptData is what a run's prompt is rendered with besides the loop's
// name and vars: the gate script's trimmed output.
type PromptData struct {
	Stdout string
	Stderr string
}

// RenderPrompt is the prompt a run starts with. A Version1 prompt is
// returned as it is. Otherwise the prompt is parsed with text/template,
// Option("missingkey=error"), and executed with a map holding "Name",
// "Vars" (a map[string]string, never nil), and, only when l.Script != "",
// "Stdout" and "Stderr" from data. A parse or execute error, or a result
// that is blank after strings.TrimSpace, is ErrInvalid.
func (l *Loop) RenderPrompt(data PromptData) (string, error)

// CheckPrompt renders the prompt as RenderPrompt does with empty data,
// for ws loop add, but doesn't refuse a blank result (a prompt that is
// only {{.Stdout}} is blank until the script prints something).
func (l *Loop) CheckPrompt() error
```

- Share one unexported `render(data PromptData, allowBlank bool)`.
- Errors: `fmt.Errorf("%w: prompt: %w", ErrInvalid, err)` and
  `fmt.Errorf("%w: prompt renders empty", ErrInvalid)`.

**`store.go`**

- `Create` sets `l.Version = FormatVersion`, then saves as today.
- `Save` no longer overwrites a set version: if `l.Version == 0` it
  becomes `FormatVersion`; otherwise it's kept, so a `Version1` record
  stays version 1 (and its prompt literal) when `tick` rewrites it.
  Update `Save`'s doc comment to say so.
- `readLoop` keeps refusing `Version > FormatVersion` and `< 1`, and
  also refuses a `Version1` record that has `script`, `script_timeout`,
  `vars`, or `last_gate` set: `fmt.Errorf("parsing %s: version 1 record has version 2 keys", path)`.

**Acceptance**

- `ValidateVarKey`: `"A"`, `"REPO"`, `"_x"`, `"a1"`, 64 bytes ok; `""`,
  `"1a"`, `"my-key"`, `"a.b"`, 65 bytes → `ErrInvalid`.
- `ParseVars`: `["A=1", "B="]` → `{A:"1", B:""}`; `["U=a=b"]` →
  `{U:"a=b"}`; `nil` → `nil`; `["A"]`, `["=x"]`, `["A=1","A=2"]`, a
  4097-byte value, 33 vars → `ErrInvalid`.
- `ParseScriptTimeout`: `1s`, `45s`, `1m`, `1m30s`, `10m` ok; `0s`,
  `500ms`, `1.5s`, `11m`, `x`, `""` → `ErrInvalid`.
  `FormatScriptTimeout` round-trips each valid case.
- `Timeout`: empty → `DefaultScriptTimeout`; `"2m"` → 2 minutes.
- `Templated`: Version 0 and 2 → true; 1 → false.
- `Validate`: a loop with `ScriptTimeout "1m"` and no script →
  `ErrInvalid`; a bad var key → `ErrInvalid`; prompt `{{.Stdout}}` with no
  script → `ErrInvalid`; prompt `{{.Stdout}}` with a script → ok; prompt
  `{{.Vars.NOPE}}` → `ErrInvalid`; prompt `{{` → `ErrInvalid`.
- `RenderPrompt`:
  - `"Fix {{.Stdout}} ({{.Stderr}}) in {{.Vars.REPO}} for {{.Name}}"`
    with a script, vars `{REPO: "buff-bear"}`, data
    `{Stdout: "#1", Stderr: "warn"}` → `"Fix #1 (warn) in buff-bear for deps"`;
  - `{{"{{"}}` → `"{{"`;
  - a `Version1` loop with prompt `"literal {{ .Stdout }}"` → that exact
    string;
  - `"{{if .Stdout}}{{.Vars.NOPE}}{{end}}x"` with stdout `"y"` →
    `ErrInvalid`;
  - `"{{.Stdout}}"` with empty stdout → `ErrInvalid` ("renders empty");
    `CheckPrompt` of the same loop → nil.
- Store: Create then Get round-trips `Script`, `ScriptTimeout`, `Vars`,
  and `LastGate` (all fields), with `Version == 2`; a loop saved with
  `Version = Version1` reads back as version 1; a record with no script,
  vars, or gate has no `script`, `script_timeout`, `[vars]`, or
  `[last_gate]` in its file (check with `strings.Contains` on the file);
  a `version = 1` file with `script = "true"` gives one warning in
  `List` and is skipped; a `version = 3` file gives one warning.

**Verify**

```console
$ go test ./internal/loop/...
$ make lint
```

---

## T2. `internal/gate`: run a gate script

**Depends on:** nothing. **Parallel with:** T1.

**Create**

- `internal/gate/gate.go`
- `internal/gate/gate_test.go`

**Modify**

- `Makefile`, `lint` target: after the `gh` rule, add

  ```make
  	@# ADR 0049: loop scripts run only through internal/gate.
  	@! grep -rnE 'exec\.Command(Context)?\([^)]*"/bin/sh"' --include='*.go' . | grep -v '_test\.go:' | grep -v '^\./internal/gate/'
  ```

  (`internal/shell/shell_test.go` runs `/bin/sh` in a test, which the
  `_test.go` filter allows.)
- `README.md`: in the package tree, after the `loop/` line, add
  `  gate/                       # runs a loop's gate script (0049)`.

**`gate.go`**

```go
// Package gate runs a loop's gate script: /bin/sh -c with a timeout, its
// own process group, and capped output (ADR 0049).
package gate

// MaxOutput is how many bytes of stdout, and of stderr, Run keeps.
const MaxOutput = 64 << 10

// waitDelay bounds the wait for a killed script's pipes to close.
const waitDelay = 5 * time.Second

// ErrTimedOut is a script that ran past its Request.Timeout.
var ErrTimedOut = errors.New("script timed out")

// Request is one script to run.
type Request struct {
	Script  string
	Dir     string
	// Env holds KEY=VALUE pairs added to the runner's environment,
	// replacing any of the same name.
	Env     []string
	Timeout time.Duration
}

// Result is how a script that ran to the end exited, and what it printed.
type Result struct {
	// ExitCode is the exit status, or -1 when a signal killed it.
	ExitCode int
	// Stdout and Stderr are at most MaxOutput bytes each, invalid UTF-8
	// replaced with U+FFFD, and trailing '\n' and '\r' trimmed.
	Stdout, Stderr                   string
	StdoutTruncated, StderrTruncated bool
}

// Runner runs scripts with an environment, normally os.Environ().
type Runner struct{ environ []string }

func New(environ []string) *Runner

// Run runs req.Script as /bin/sh -c req.Script in req.Dir, with stdin
// empty. A non-zero exit is a Result, not an error. The error is
// ErrTimedOut (wrapped) when it ran past req.Timeout, ctx's error
// (wrapped) when ctx ended first, and otherwise why it couldn't run.
func (r *Runner) Run(ctx context.Context, req Request) (Result, error)
```

How `Run` works:

1. `tctx, cancel := context.WithTimeout(ctx, req.Timeout)`; `defer cancel()`.
2. `cmd := exec.CommandContext(tctx, "/bin/sh", "-c", req.Script)` (write
   the literal `"/bin/sh"` here: the lint rule looks for it);
   `cmd.Dir = req.Dir`; `cmd.Env = mergeEnv(r.environ, req.Env)`;
   leave `cmd.Stdin` nil (the null device);
   `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`;
   `cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }`;
   `cmd.WaitDelay = waitDelay`.
3. `cmd.Stdout` and `cmd.Stderr` are each a `*capWriter{limit: MaxOutput}`:
   its `Write` keeps bytes until `limit`, sets `truncated` once it drops
   any, and always returns `len(p), nil`.
4. `err := cmd.Run()`. Then, in this order: if `ctx.Err() != nil` →
   `fmt.Errorf("running script: %w", ctx.Err())`; if
   `errors.Is(tctx.Err(), context.DeadlineExceeded)` →
   `fmt.Errorf("%w after %s", ErrTimedOut, req.Timeout)`; if
   `errors.As(err, &exitErr)` (`*exec.ExitError`) → a Result with
   `ExitCode: exitErr.ExitCode()` and nil error; any other non-nil
   `err` → `fmt.Errorf("running script: %w", err)`; else a Result with
   `ExitCode 0`.
5. `clean(b []byte) string` is
   `strings.TrimRight(strings.ToValidUTF8(string(b), "�"), "\r\n")`.
6. `mergeEnv(base, extra []string) []string`: `base` without any entry
   whose name (before the first `=`) is set in `extra`, then `extra`.

**Acceptance** (`gate_test.go`: real `/bin/sh`; every subtest calls
`t.Parallel()`; `run := func(req Request) (Result, error)` with
`Timeout` defaulting to 10 s and `Dir` to `t.TempDir()`)

- `printf 'a\nb\n\n'` → `Stdout "a\nb"`, `ExitCode 0`, nil error.
- `echo oops >&2; exit 3` → `ExitCode 3`, `Stderr "oops"`, nil error.
- `kill -9 $$` → `ExitCode -1`, nil error.
- `printf %s "$WS_LOOP_NAME/$FOO"` with `New([]string{"FOO=1", "PATH=" + os.Getenv("PATH")})`
  and `Env: ["WS_LOOP_NAME=deps", "FOO=2"]` → `"deps/2"`.
- `pwd -P` with `Dir` a temp dir → `filepath.EvalSymlinks(dir)`.
- `cat` → `Stdout ""`, returns at once (stdin is empty).
- `head -c 70000 /dev/zero | tr '\0' a` → `len(Stdout) == MaxOutput`,
  `StdoutTruncated`; a short script → not truncated.
- `printf '\377ok'` → `Stdout "�ok"`.
- `Timeout: time.Second`, `sleep 60 & sleep 60` → `errors.Is(err,
  ErrTimedOut)`, and `Run` returned in under 8 s (measure with
  `time.Since`).
- A context cancelled after 100 ms, script `sleep 60` →
  `errors.Is(err, context.Canceled)`, not `ErrTimedOut`.
- `Dir` that doesn't exist → an error that is not `ErrTimedOut`.

**Verify**

```console
$ go test -race ./internal/gate/...
$ make lint
```

---

## T3. `ws loop add` flags and the loop JSON

**Depends on:** T1.

**Modify**

- `internal/cli/loop.go`
- `internal/cli/loop_test.go`
- `internal/cli/guides/ws-loop-add.md`, `ws-loop-list.md`, `ws-loop.md`

**`loop.go`**

```go
type loopAddOptions struct {
	every, prompt, model, permissionMode string
	script, scriptTimeout                string
	vars                                 []string
	keep                                 int
	paused                               bool
}

// gateJSON is a loop's last_gate, and a tick item's gate.
type gateJSON struct {
	At       string  `json:"at"`
	Outcome  string  `json:"outcome"`
	ExitCode *int    `json:"exit_code"` // nil when Gate.ExitCode < 0
	Message  *string `json:"message"`   // optional(Gate.Message)
}

// gateJSONOf is nil for nil.
func gateJSONOf(g *loop.Gate) *gateJSON
```

- `loopJSON` gains, after `LastRun`:

  ```go
  	Script        *string           `json:"script"`         // optional(l.Script)
  	ScriptTimeout *string           `json:"script_timeout"` // nil when no script; else FormatScriptTimeout(l.Timeout())
  	Vars          map[string]string `json:"vars"`           // never nil: {} when none
  	LastGate      *gateJSON         `json:"last_gate"`
  ```

  `loopJSONOf` fills them. If `l.Timeout()` errors (a hand-edited
  record), print `l.ScriptTimeout` as it is.
- `newLoopAddCmd` adds:

  ```go
  cmd.Flags().StringVar(&opts.script, "script", "", "a shell script, run with /bin/sh -c, that must exit 0 for a run to start; its output is {{.Stdout}} and {{.Stderr}} in the prompt")
  cmd.Flags().StringVar(&opts.scriptTimeout, "script-timeout", "", "how long the script may run, 1s to 10m (default 1m)")
  cmd.Flags().StringArrayVar(&opts.vars, "var", nil, "KEY=VALUE for {{.Vars.KEY}} in the prompt and $WS_LOOP_VAR_KEY in the script; repeatable")
  ```

  Use `StringArrayVar`, not `StringSliceVar`, so a value with a comma
  stays whole.
- `runLoopAdd`, after the `--prompt` check:
  1. `script := opts.script`; if `strings.TrimSpace(script) == ""`,
     `script = ""`.
  2. If `opts.scriptTimeout != ""` and `script == ""` →
     `usageErrorf("--script-timeout needs --script")`.
  3. `vars, err := loop.ParseVars(opts.vars)` → `domainError(err)`.
  4. If `script != ""`: `d := loop.DefaultScriptTimeout`; if
     `opts.scriptTimeout != ""`, `d, err = loop.ParseScriptTimeout(...)`
     (→ `domainError`); `ScriptTimeout = loop.FormatScriptTimeout(d)`.
  5. Build the `loop.Loop` as today plus `Version: loop.FormatVersion`,
     `Script: script`, `ScriptTimeout`, `Vars: vars`. `l.Validate()`
     (which now checks the template) runs as today.
- `loopLine` (text output) is unchanged.

**Guides**: `ws-loop-add.md` documents `--script`, `--script-timeout`,
and `--var` (the guide coverage test checks every flag is mentioned),
the four template values (`{{.Name}}`, `{{.Vars.KEY}}`, `{{.Stdout}}`,
`{{.Stderr}}`), `{{"{{"}}` for a literal `{{`, and that there's no
`ws loop edit` (rm and add again). Its JSON example and `ws-loop-list.md`'s
gain `script`, `script_timeout`, `vars`, and `last_gate`, as in
`docs/tools/ws-loop.md`. `ws-loop.md` (the topic) gets one paragraph on
scripts and variables. Copy wording from `docs/tools/ws-loop.md`.

**Acceptance** (in `loop_test.go`, as the existing add tests: `newTestApp()`,
`withLoopStore(t, app)`, `app.Run(ctx, []string{"loop", ...})`)

- `loop add deps --every 1h --script 'gh issue list | grep .' --var REPO=buff-bear --var 'Q=a,b=c' --prompt 'Triage {{.Vars.REPO}}: {{.Stdout}}' --json`
  exits 0 and prints `script "gh issue list | grep ."`,
  `script_timeout "1m"`, `vars {"REPO":"buff-bear","Q":"a,b=c"}`,
  `last_gate null`; the record file has `version = 2`.
- `--script-timeout 90s` → `script_timeout "1m30s"`.
- An add without `--script` or `--var` prints `script null`,
  `script_timeout null`, `vars {}` (assert the raw JSON contains
  `"vars":{}`).
- `--script-timeout 1m` without `--script` → exit 2, `usage`.
- `--script-timeout 11m` → exit 2 `loop_invalid`; `--var my-key=x` →
  exit 2 `loop_invalid`; `--var A=1 --var A=2` → exit 2 `loop_invalid`.
- `--prompt '{{.Stdout}}'` without `--script` → exit 2 `loop_invalid`;
  `--prompt '{{.Vars.NOPE}}'` → exit 2 `loop_invalid`; `--prompt 'a {{'`
  → exit 2 `loop_invalid`.
- `--script '   '` → `script null`.
- `loop list --json` of a loop whose record has a `LastGate{Outcome:
  "skipped", ExitCode: 1, Message: "no new issues"}` (write it with
  `store.Create`) shows `last_gate {"outcome":"skipped","exit_code":1,"message":"no new issues", "at": …}`;
  with `ExitCode -1` and `Outcome "timed_out"`, `exit_code null`.
- `go test ./internal/cli/ -run TestGuide` passes.

**Verify**

```console
$ go test ./internal/cli/... ./internal/loop/...
$ make test
$ make lint
$ make build && bin/ws loop add demo --every 15m --script 'true' --var REPO=x --prompt '{{.Vars.REPO}} {{.Stdout}}' --config-dir "$(mktemp -d)" --json
```

---

## T4. `ws loop tick` and `ws loop run`: run the gate

**Depends on:** T1, T2, T3. **Parallel with:** T5.

**Create**

- `internal/cli/loop_gate.go`
- `internal/cli/loop_gate_test.go`

**Modify**

- `internal/cli/interfaces.go`: add (not to the `go:generate` line)

  ```go
  // gateRunner runs a loop's gate script (ADR 0049).
  type gateRunner interface {
  	Run(ctx context.Context, req gate.Request) (gate.Result, error)
  }
  ```

- `internal/cli/app.go`: add to `App` `gate gateRunner` (comment
  `// gate runs loop scripts, for ws loop tick and run (ADR 0049).`) and
  `homeDir func() (string, error)`; in `New` set `gate: gate.New(environ)`
  and `homeDir: os.UserHomeDir`.
- `internal/cli/app_test.go`: add `fakeGate` (below), and in `newTestApp`,
  after `app.now = testNow`, set `app.gate = &fakeGate{}` and
  `app.homeDir = func() (string, error) { return "/home/me", nil }`.
  A test that needs a result sets `e.app.gate = &fakeGate{result: …}`
  (these fields aren't replaced by `withConfig`).
- `internal/cli/errors.go`: under `// From ADR 0049.` add
  `codeLoopSkipped errorCode = "loop_skipped"` (in `exitCode()`'s
  `exitRefused` case) and `codeLoopScriptFailed errorCode = "loop_script_failed"`
  (in the `exitFailed` case).
- `internal/cli/cache.go`, `codedDomainError`'s table: add
  `{loop.ErrSkipped, codeLoopSkipped}` and
  `{loop.ErrScriptFailed, codeLoopScriptFailed}`, before
  `{loop.ErrInvalid, codeLoopInvalid}`.
- `internal/cli/loop_tick.go` (below).
- `internal/cli/loop_tick_test.go`: existing tests keep passing; add the
  gate cases below.
- `internal/cli/guides/ws-loop-run.md` (document `--force`, `loop_skipped`,
  `loop_script_failed`), `ws-loop-tick.md` (the `skipped` action, the
  `gate` field, `--dry-run` runs no script), and `ws.md`: add
  `loop_script_failed` to the exit 1 row and `loop_skipped` to the exit 4
  row of "Output and exit codes".

**`loop_gate.go`**

```go
// loopSkipped is the tick action for a due loop whose script exited non-zero.
const loopSkipped loopAction = "skipped"

// maxGateMessage is how many bytes of stderr a skipped gate keeps.
const maxGateMessage = 200

// gateEnv is the environment a loop's script gets beyond ws's own:
// WS_LOOP_NAME=<name>, then WS_LOOP_VAR_<KEY>=<value> per var, by key.
func gateEnv(l *loop.Loop) []string

// runGate runs l's script, if it has one, sets l.LastGate to a new Gate
// with the outcome, and returns the prompt data for the run. With no
// script it returns empty data and nil, and leaves LastGate alone.
//   - exit 0: GatePassed, ExitCode 0, data from the result, nil error.
//   - other exit: GateSkipped, the exit code, Message skipMessage(stderr);
//     returns fmt.Errorf("%w: exit %d", loop.ErrSkipped, code), with
//     ": <message>" appended when there is one.
//   - Run's error: GateTimedOut when errors.Is(err, gate.ErrTimedOut),
//     else GateFailed; ExitCode -1; Message err.Error(); returns
//     fmt.Errorf("%w: %w", loop.ErrScriptFailed, err).
// A truncated stdout or stderr adds the warning
// "loop <name>: script <stdout|stderr> over 64 KiB was cut".
// The request is {Script: l.Script, Dir: a.homeDir() (an error → GateFailed),
// Env: gateEnv(l), Timeout: l.Timeout()}.
func (a *App) runGate(ctx context.Context, l *loop.Loop) (loop.PromptData, error)

// skipMessage is the last non-blank line of stderr, trimmed, cut to at
// most maxGateMessage bytes at a rune boundary.
func skipMessage(stderr string) string
```

**`loop_tick.go`** changes:

1. `tickJSON` gains, after `Error`, `Gate *gateJSON `json:"gate"``.
2. `loopClaudeArgs(l *loop.Loop, prompt string) []string` appends
   `prompt` instead of `l.Prompt`; `startLoopRun(ctx, l, prompt string)`
   passes it through. Update their callers and tests.
3. `attemptRun(ctx, l, check, force bool)`, in this order:
   1. `check.ready` fails → `failedRun`, as today.
   2. `data := loop.PromptData{}`; unless `force`, `data, err =
      a.runGate(ctx, l)`. If `errors.Is(err, loop.ErrSkipped)`, return
      `loop.Run{}, workspace.Workspace{}, err` (no run to record). Any
      other error → `failedRun(a.now(), err), workspace.Workspace{}, err`.
   3. `prompt, err := l.RenderPrompt(data)`; error → `failedRun`.
   4. `return a.startLoopRun(ctx, l, prompt)`.
4. `tickLoop`: the paused, waiting, asleep, busy, and dry-run cases stay
   first and unchanged, so no script runs for them. Then:

   ```go
   prev := l.LastGate
   run, _, startErr := a.attemptRun(ctx, l, &p.check, false)
   skipped := errors.Is(startErr, loop.ErrSkipped)
   if !skipped {
   	l.AddRun(run)
   }
   // Advance and Save as today.
   item := newTickJSON(l, loopStarted)
   if l.LastGate != prev {
   	item.Gate = gateJSONOf(l.LastGate)
   }
   if skipped {
   	item.Action = loopSkipped
   	return addTick(results, item, nil), nil
   }
   // pruneRuns, loopFailed, Workspace, Session as today.
   ```

5. `newLoopRunCmd` gains `--force` ("start the run without running the
   loop's script"); `runLoopRun(ctx, name string, force bool)` calls
   `attemptRun(ctx, &l, &check, force)`, calls `l.AddRun(run)` only when
   `!errors.Is(startErr, loop.ErrSkipped)`, and saves in every case (so
   `last_gate` is kept), then returns the error as today.
6. `--watch` still prints only `started` and `failed`.

**Acceptance** (`loop_gate_test.go` for `gateEnv`, `skipMessage`, and
`runGate`; `loop_tick_test.go` for tick and run. The `fakeGate` for
`app_test.go`:

```go
// fakeGate is a gateRunner that records each request and returns result, err.
type fakeGate struct {
	result   gate.Result
	err      error
	requests []gate.Request
}

func (f *fakeGate) Run(_ context.Context, req gate.Request) (gate.Result, error) {
	f.requests = append(f.requests, req)
	return f.result, f.err
}
```

Add an `addGatedLoop(t, prompt string, vars map[string]string)` beside
`addDueLoop` that adds `deps` due a minute ago with
`Script: "check"`.)

- `gateEnv` of a loop with vars `{B: "2", A: "1"}` →
  `["WS_LOOP_NAME=deps", "WS_LOOP_VAR_A=1", "WS_LOOP_VAR_B=2"]`.
- `skipMessage("x\nno new issues\n\n")` → `"no new issues"`; `""` →
  `""`; a 300-byte line → 200 bytes; a line of `é` (2 bytes each) of 300
  bytes → at most 200 bytes and valid UTF-8.
- tick, script exits 0 with stdout `"#1 Bug"`, prompt
  `"Fix {{.Stdout}} in {{.Vars.REPO}}"`, vars `{REPO: "buff-bear"}` →
  action `started`; `StartFunc`'s last arg is `"Fix #1 Bug in buff-bear"`;
  the request had `Script "check"`, `Dir "/home/me"`, `Timeout 1m`, and
  `Env` containing `WS_LOOP_VAR_REPO=buff-bear`; item `gate` is
  `{"outcome":"passed","exit_code":0,"message":null,…}`; record
  `LastGate.Outcome == "passed"`.
- tick, exit 1 with stderr `"no new issues"` → action `skipped`, command
  exits 0, `CreateFunc` and `StartFunc` not called, record has 0 runs,
  `next_run_at` = old + 1h, `LastGate {skipped, 1, "no new issues"}`.
- tick, `err: fmt.Errorf("%w after 1m0s", gate.ErrTimedOut)` → action
  `failed`, item `error.code "loop_script_failed"`, exit 1, record has 1
  run with `ErrorCode "loop_script_failed"`, `LastGate.Outcome
  "timed_out"`, `ExitCode -1`; item `gate.exit_code null`.
- tick, `err: errors.New("no such file")` → `LastGate.Outcome "failed"`.
- No request is made when the loop is paused, not due, asleep, busy, with
  `--dry-run` (action `would_start`), or when `CheckFunc` fails.
- A loop without a script: no request, item `gate null`, record
  `LastGate nil`.
- Prompt `"{{if .Stdout}}{{.Vars.NOPE}}{{end}}x"`, stdout `"y"` →
  `failed`, `error.code "loop_invalid"`, `StartFunc` not called.
- A `Version1` loop with prompt `"literal {{ .Stdout }}"` (create it,
  then set `l.Version = loop.Version1` and `Save`) → `started`, and
  `StartFunc`'s last arg is exactly that prompt; the record is still
  version 1 afterwards.
- `StdoutTruncated: true` → the warning is in `warnings`.
- `loop run deps --json`, script exits 1 → exit 4, `loop_skipped`,
  nothing started, record `LastGate.Outcome "skipped"`, 0 runs,
  `next_run_at` unchanged.
- `loop run --force deps --json`, prompt `"Go{{.Stdout}}"` → no request,
  `StartFunc`'s last arg `"Go"`, `LastGate` unchanged.

**Verify**

```console
$ go test ./internal/cli/... ./internal/loop/... ./internal/gate/...
$ make test
$ make lint
$ make build && D="$(mktemp -d)" && bin/ws loop add g --every 5m --script 'echo nothing >&2; exit 1' --prompt hi --config-dir "$D" && bin/ws loop run g --json --config-dir "$D"; echo "exit $?"   # expect loop_skipped, exit 4
$ bin/ws loop tick --dry-run --json --config-dir "$D"
```

---

## T5. Desktop backend: new add arguments and loop fields

**Depends on:** T3 (needs the new `ws loop add` flags and JSON). **Parallel with:** T4.

**Modify**

- `desktop/src-tauri/src/loops.rs` (and its `mod tests`)

**`loops.rs`**

```rust
use std::collections::BTreeMap;

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopGate {
    pub at: String,
    pub outcome: String,
    pub exit_code: Option<i64>,
    pub message: Option<String>,
}

// Loop gains, after last_run (missing fields default, for an older ws):
    #[serde(default)]
    pub script: Option<String>,
    #[serde(default)]
    pub script_timeout: Option<String>,
    #[serde(default)]
    pub vars: BTreeMap<String, String>,
    #[serde(default)]
    pub last_gate: Option<LoopGate>,

/// One row of the form's variables.
#[derive(Debug, Clone, Deserialize)]
pub struct LoopVar {
    pub key: String,
    pub value: String,
}

// NewLoop gains:
    pub script: Option<String>,
    pub script_timeout: Option<String>,
    #[serde(default)]
    pub vars: Vec<LoopVar>,
```

`add_args` appends, after `--permission-mode` and before `"--"`:

- `--script=<script>` when the script isn't blank, **untrimmed** (a
  script's whitespace matters);
- `--script-timeout=<t>` (trimmed) only when both the script and the
  timeout aren't blank;
- `--var=<key>=<value>` for each var whose trimmed key isn't blank, key
  trimmed, value as given, in the order sent.

Update `add_args`'s doc comment to list them. `vars` keys are serialized
as they are (`rename_all` doesn't touch map keys).

**Acceptance** (in `mod tests`)

- `add_args` for the existing `request()` is unchanged (the existing
  test passes).
- `add_args` with `script: Some("gh issue list | grep .\n")`,
  `script_timeout: Some(" 2m ")`, `vars: [{key: " REPO ", value: "a b"}, {key: " ", value: "x"}]`
  ends with `["--script=gh issue list | grep .\n", "--script-timeout=2m", "--var=REPO=a b", "--", "deps"]`.
- `script: Some("  ")` with `script_timeout: Some("2m")` adds neither flag.
- A fake `ws` printing the `ws loop list --json` example of
  `docs/tools/ws-loop.md` (with `script`, `vars`, and `last_gate`) parses,
  and `serde_json::to_value` of the loop has `scriptTimeout`,
  `lastGate.exitCode`, and `vars.BRANCH`.
- The old example without the four fields still parses, with `vars`
  empty and the rest `None`.

**Verify**

```console
$ cd desktop/src-tauri && cargo test loops && cargo clippy --all-targets -- -D warnings
$ make desktop-test
```

---

## T6. Desktop UI: script, variables, and last check

**Depends on:** T5.

**Create**

- `desktop/src/features/loops/VarsEditor.tsx`
- `desktop/src/features/loops/VarsEditor.test.tsx`
- `desktop/src/features/loops/gate.ts`
- `desktop/src/features/loops/gate.test.ts`

**Modify**

- `desktop/src/api.ts`
- `desktop/src/test/fixtures.ts`
- `desktop/src/features/loops/LoopForm.tsx`
- `desktop/src/features/loops/LoopsPane.tsx`
- `desktop/src/features/loops/LoopsPane.module.css` (only if a new class is needed)
- `desktop/src/features/loops/LoopsPane.test.tsx`

**`api.ts`**

```ts
export interface LoopGate {
  readonly at: string;
  readonly outcome: "passed" | "skipped" | "timed_out" | "failed";
  readonly exitCode: number | null;
  readonly message: string | null;
}

// Loop gains:
  readonly script: string | null;
  readonly scriptTimeout: string | null;
  readonly vars: Readonly<Record<string, string>>;
  readonly lastGate: LoopGate | null;

export interface LoopVar { readonly key: string; readonly value: string }

// NewLoop gains (all optional, so existing callers and tests still match):
  readonly script?: string;
  readonly scriptTimeout?: string;
  readonly vars?: readonly LoopVar[];
```

**`fixtures.ts`**: `loop()` gains `script: null, scriptTimeout: null,
vars: {}, lastGate: null`. `fakeApi`'s `loopsAdd` becomes
`vi.fn(async (request: NewLoop) => loop({ name: request.name, every: request.every, prompt: request.prompt, specs: request.specs, script: request.script ?? null }))`.

**`VarsEditor.tsx`**

```tsx
/** Key-value rows for a loop's variables. */
export const VarsEditor = ({ rows, onChange }: {
  readonly rows: readonly LoopVar[];
  readonly onChange: (rows: readonly LoopVar[]) => void;
}): JSX.Element
```

- A heading-less group (`role="group"`, `aria-label="Variables"`). Each
  row is two small MUI `TextField`s, **Key** (`aria-label` `Key <n>`,
  1-based) and **Value** (`Value <n>`), and an `IconButton`
  `aria-label="Remove variable <n>"`. Below, a `Button` **Add variable**
  that appends `{ key: "", value: "" }`. No validation (ws validates).

**`LoopForm.tsx`**: keep the existing fields and order, then add after
**Repos**:

- **Script** (`TextField`, optional, multiline, 3 rows, helper "Runs with
  /bin/sh -c at each run; the run starts only if it exits 0. Its output
  is {{.Stdout}} and {{.Stderr}} in the prompt");
- **Script timeout** (shown only while Script isn't blank, placeholder
  `1m`, helper "1s to 10m");
- `<VarsEditor />` with the form's rows (starts empty).

Give **Prompt** the helper text "A Go template: {{.Vars.KEY}},
{{.Stdout}}, {{.Stderr}}, {{.Name}}". On submit, add `script` only when
it isn't blank (send it untrimmed), `scriptTimeout` only when the script
and it aren't blank (trimmed), and `vars` only when at least one row
has a non-blank key (send those rows, keys trimmed). So a form that
leaves them empty sends exactly `{name, every, prompt, specs}`, as today.

**`LoopsPane.tsx`**, in `LoopDetail`'s rows after **Keep**:

- **Script**: the script in `styles.prompt` (pre-wrapped), or "none".
- **Script timeout**: `scriptTimeout`, only when `script !== null`.
- **Variables**: one `KEY=value` line per entry (pre-wrapped), or "none".
- **Last check**, only when `script !== null`: `gateText(lastGate)`.

**`gate.ts`** (a plain module, so component files export only components):

```ts
/** A loop's last gate, for the details: "never", or "<age> · <what>". */
export const gateText = (gate: LoopGate | null): string
```

`<what>` is `passed`; `skipped (exit <n>)` plus `: <message>` when there
is one; `timed out: <message>`; `failed: <message>`. Use `age(gate.at)`
from `format.ts`.

**Acceptance** (vitest + Testing Library, as the existing loops tests)

- `VarsEditor.test.tsx`: **Add variable** calls `onChange` with one
  empty row; typing in `Key 1` calls it with that key; **Remove variable
  1** calls it without that row.
- `LoopsPane.test.tsx`:
  - the existing "adds a loop" test still passes unchanged (it sends no
    script or vars);
  - filling Name, Every, Prompt, Script `gh issue list | grep .`, then
    **Add variable** with Key 1 `REPO` and Value 1 `buff-bear`, and
    **Add loop** → `loopsAdd` called with
    `{name, every, prompt, specs: [], script: "gh issue list | grep .", vars: [{key: "REPO", value: "buff-bear"}]}`;
  - Script timeout is not shown until Script has text;
  - a selected loop with `script: "check"`, `scriptTimeout: "1m"`,
    `vars: {REPO: "x"}`, and `lastGate: {outcome: "skipped", exitCode: 1, message: "no new issues", at: …}`
    shows `check`, `1m`, `REPO=x`, and text matching
    `skipped (exit 1): no new issues`;
  - a loop with `script: null` shows Script "none" and no Last check row.
- `gate.test.ts`: `gateText(null)` → `"never"`; `passed` → ends with
  `"passed"`; `skipped` with `exitCode 2` and `message null` → ends with
  `"skipped (exit 2)"`; `timed_out` with message `"script timed out after 1m0s"`
  → ends with `"timed out: script timed out after 1m0s"`.
- `npm run typecheck`, `npm test`, and `npm run build` pass.

**Verify**

```console
$ cd desktop && npm ci && npm run typecheck && npm test && npm run build
$ make desktop-test
```
