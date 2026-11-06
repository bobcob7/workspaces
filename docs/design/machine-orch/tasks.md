# ws machine wait, result, and roles: implementation tasks

Implements [0050](../../adrs/0050-machine-wait-results-and-roles.md) and
the new parts of [`ws machine`](../../tools/ws-machine.md). Each task
builds, passes its tests, and can be merged on its own. Each task is one
commit (conventional, imperative, e.g. `feat: add machine result records
(ADR 0050)`). No desktop app changes.

**Order and parallelism**

| Task | Title | Depends on | Can run in parallel with |
| ---- | ----- | ---------- | ------------------------ |
| T1 | `internal/machine`: spawn and result records, outcomes, the root | nothing | T2 |
| T2 | `internal/machine/role`: role files, built-ins, rendering | nothing | T1 |
| T3 | `ws machine result`, and `role` and `result` in `ws machine children` | T1 | none |
| T4 | `ws machine wait` | T3 | none |
| T5 | `ws machine roles` (list, show, add, edit, rm) | T2, T4 | none |
| T6 | `ws machine spawn --role --var`, and the overview guides | T1, T2, T5 | none |

T3 to T6 each edit the same lists in `internal/cli/machine.go`,
`internal/cli/errors.go`, `internal/cli/llm.go` and
`internal/cli/guides/ws.md`, so they run one after another.

**Read before any task:** `docs/tools/ws-machine.md` (the behavior, every
JSON shape, every error) and the "Decision" section of
`docs/adrs/0050-machine-wait-results-and-roles.md`. When this file and
those two disagree, they win. Don't read whole packages: read the files
each task names.

**Rules for every task**

- Go: follow the existing code in the package you edit. Errors are
  wrapped with `fmt.Errorf("…: %w", err)`, lower-case, no "failed to".
  Tests use `github.com/stretchr/testify/assert` and `require`, one test
  function per function or method with `t.Run` subtests per behavior,
  `t.Context()` instead of `context.Background()`, and `t.TempDir()` for
  files. Call `t.Parallel()` only in tests that touch files.
- Run `make fmt` before committing. `make lint` must pass. Never call
  `exec.Command` in new code.
- JSON fields are `snake_case`; times are `formatTime(t)` (RFC 3339, UTC,
  whole seconds, `internal/cli/cache.go`); an empty optional string is
  `null` (use `optional(s)` from `internal/cli/cache.go`); lists are `[]`
  and maps `{}`, never `null`. Every object has `warnings` (use
  `a.warnings()`).
- Don't change any existing JSON field, error code, or exit code. New
  JSON fields go after the existing ones.
- Guides: `internal/cli/llm_test.go`'s `TestGuideTopics` checks that every
  command has a guide in `internal/cli/guides/` and a topic in
  `guideTopics()` (`internal/cli/llm.go`), that each guide mentions every
  local flag and `--json`, has a ` ```json ` block under `## JSON output`,
  `## Exit codes` and `## Examples`, and that `internal/cli/guides/ws.md`
  links every guide (`` `ws llm <topic>` ``) and lists every error code in
  its exit-code table. The finished guide texts are in
  `docs/design/machine-orch/guides/`: copy the one a task names with `cp`,
  unchanged.
- Run `go test ./internal/cli/ -run TestGuideTopics` in every task that
  touches `internal/cli`.

---

## T1. `internal/machine`: spawn and result records, outcomes, the root

**Depends on:** nothing. **Parallel with:** T2.

**Read:** `internal/workspace/record.go` (the `Workspace` type, and how a
TOML record is written through a temp file and renamed), `internal/cli/machine.go`
(`descendants`, for the walk style).

**Create**

- `internal/machine/result.go`
- `internal/machine/result_test.go`
- `internal/machine/store.go`
- `internal/machine/store_test.go`

**`result.go`**

```go
// Package machine keeps the records of ws machine children under the root
// of their tree: what each was spawned as, and the verdict it recorded
// (ADR 0050).
package machine

// FormatVersion is the newest spawn.toml and result.toml version this ws
// reads and writes.
const FormatVersion = 1

// MaxReportSize is the largest report ws machine result keeps.
const MaxReportSize = 1 << 20

var (
	// ErrInvalid is a malformed verdict, id, or record.
	ErrInvalid = errors.New("invalid machine record")
	// ErrNewer is a record whose version is above FormatVersion.
	ErrNewer = errors.New("machine record from a newer ws")
)

// Outcome is what a verdict means: pass or fail.
type Outcome string

const (
	OutcomePass Outcome = "pass"
	OutcomeFail Outcome = "fail"
)

// DefaultPass and DefaultFail judge a child spawned without a role.
var (
	DefaultPass = []string{"PASS", "APPROVED"}
	DefaultFail = []string{"FAIL", "CHANGES_REQUESTED"}
)

// NormalizeVerdict upper-cases s and checks it is 1-32 characters of
// [A-Za-z0-9_] starting with a letter; anything else is ErrInvalid.
func NormalizeVerdict(s string) (string, error)

// Spawn is spawn.toml: what ws machine spawn --role started a child as.
type Spawn struct {
	Version   int               `toml:"version"`
	Role      string            `toml:"role"`
	Model     string            `toml:"model,omitempty"`
	Pass      []string          `toml:"pass"`
	Fail      []string          `toml:"fail"`
	Vars      map[string]string `toml:"vars,omitempty"`
	Ancestors []string          `toml:"ancestors"`
	SpawnedAt time.Time         `toml:"spawned_at"`
}

// Allows reports whether verdict is one of the spawn's pass or fail verdicts.
func (s *Spawn) Allows(verdict string) bool

// Result is result.toml: the verdict a child recorded.
type Result struct {
	Version    int       `toml:"version"`
	Verdict    string    `toml:"verdict"`
	Ancestors  []string  `toml:"ancestors"`
	RecordedAt time.Time `toml:"recorded_at"`
	// Report is set when report.md was kept with the result.
	Report bool `toml:"report"`
}

// OutcomeOf is pass when verdict is in spawn's Pass list, or with a nil
// spawn in DefaultPass; every other verdict is fail.
func OutcomeOf(spawn *Spawn, verdict string) Outcome

// Record is what the store keeps for one child. Spawn and Result are nil
// when their file doesn't exist.
type Record struct {
	Child  string
	Spawn  *Spawn
	Result *Result
	// ReportPath is report.md's absolute path, or "" when Result is nil or
	// has no report.
	ReportPath string
}

// Found reports whether either file exists.
func (r *Record) Found() bool

// Role is Spawn.Role, or "" without a spawn record.
func (r *Record) Role() string

// Ancestors is Result's ancestors, else Spawn's, else nil.
func (r *Record) Ancestors() []string

// Outcome is OutcomeOf(r.Spawn, r.Result.Verdict); ok is false when Result is nil.
func (r *Record) Outcome() (outcome Outcome, ok bool)

// Ancestors is id's parent, its parent, and so on up to the root, from
// list. It stops at a workspace with no parent, a parent not in list, or
// an id already seen (a cycle). An id not in list has none.
func Ancestors(list []workspace.Workspace, id string) []string

// Root is the last of Ancestors(list, id), or id itself when it has none.
func Root(list []workspace.Workspace, id string) string
```

**`store.go`**

```go
// Store reads and writes CONFIG_DIR/state/<root>/machine/<child>/.
type Store struct{ stateDir string }

// NewStore returns a Store over stateDir, normally config.Paths.State.
func NewStore(stateDir string) *Store

// Dir is <stateDir>/<root>/machine/<child>.
func (s *Store) Dir(root, child string) string

// WriteSpawn writes spawn.toml, creating the directory (0o700), with
// sp.Version set to FormatVersion.
func (s *Store) WriteSpawn(root, child string, sp *Spawn) error

// WriteResult writes report.md when report is non-nil, or deletes it (a
// missing file is fine) when report is nil, then result.toml with
// r.Version = FormatVersion and r.Report = report != nil. It creates the
// directory (0o700).
func (s *Store) WriteResult(root, child string, r *Result, report []byte) error

// Read reads both files; a missing file leaves its field nil, and a
// missing directory is an empty Record with Child set. A version above
// FormatVersion is ErrNewer, a file that doesn't parse ErrInvalid, both
// wrapped with the file's path.
func (s *Store) Read(root, child string) (Record, error)
```

- `root` and `child` must each be a non-empty path component: no `/`, not
  `.` or `..`, not starting with `.`; otherwise `ErrInvalid`, before any
  file is touched.
- Every file is written with `os.CreateTemp` in the same directory (a name
  starting with `.`), then `os.Rename`d into place; files are 0o600.
- TOML through `github.com/BurntSushi/toml` (already in `go.mod`).

**Acceptance**

- `NormalizeVerdict`: `"pass"` → `"PASS"`; `"CHANGES_REQUESTED"` kept;
  `""`, `"1PASS"`, `"PA SS"`, `"PASS-1"`, and a 33-character verdict are
  `ErrInvalid`.
- `OutcomeOf`: nil spawn: `PASS` and `APPROVED` pass, `FAIL`,
  `CHANGES_REQUESTED`, and `WHATEVER` fail; with
  `Spawn{Pass: ["PASS","FLAKY"], Fail: ["FAIL"]}`: `FLAKY` passes,
  `APPROVED` fails.
- `Spawn.Allows`: in either list → true; otherwise false.
- `Ancestors`/`Root` on `boss ← plan ← a ← a1` plus `other ← boss`
  (`a1`'s parent `a`, …): `Ancestors(a1)` = `[a, plan, boss]`,
  `Root(a1)` = `boss`; a workspace with no parent → `nil`, root itself; a
  parent missing from the list stops the walk; a cycle `x ↔ y` terminates.
- `Store`: `WriteResult` then `Read` round-trips the verdict, ancestors,
  time, and `ReportPath` (the file holds the bytes given); a second
  `WriteResult` with nil report deletes `report.md` and clears
  `ReportPath`; `WriteSpawn` then `Read` round-trips every field;
  `Read` of a missing directory → `Record{Child: "x"}`, `Found()` false;
  `version = 2` in either file → `ErrNewer`; `child` `"../x"` or `".x"` →
  `ErrInvalid` and nothing is created; files are 0o600.

**Verify**

```console
$ go test ./internal/machine/
$ make lint
```

---

## T2. `internal/machine/role`: role files, built-ins, rendering

**Depends on:** nothing. **Parallel with:** T1.

**Read:** `internal/review/profile/profile.go`, `set.go`, `write.go` (copy
their structure: `Reader`, `Set`, `Writer`, `Changes`, front-matter
parsing, `validName`, the lock, temp-file-and-read-back writes);
`internal/loop/prompt.go` (templating); `ValidateVarKey` and `MaxVars` in
`internal/loop/loop.go`.

**Create**

- `internal/machine/role/role.go`, `role_test.go`
- `internal/machine/role/set.go`, `set_test.go`
- `internal/machine/role/write.go`, `write_test.go`
- `internal/machine/role/roles/implementer.md`, `reviewer.md`,
  `verifier.md`: `cp docs/design/machine-orch/roles/*.md internal/machine/role/roles/`

**`role.go`**

```go
// Package role reads and writes ws machine roles: one Markdown file per
// role, <name>.md, with front matter (description, model, pass, fail,
// vars, claude_args) and a prompt template as the body. ws builds three
// in and reads more, or overrides, from CONFIG_DIR/machine/roles/ (ADR 0050).
package role

//go:embed roles/*.md
var builtinFiles embed.FS

// Source says where a role comes from.
type Source string

const (
	SourceBuiltin  Source = "builtin"
	SourceUser     Source = "user"
	SourceOverride Source = "override"
)

var (
	ErrInvalid = errors.New("invalid role")          // can't be parsed or written
	ErrExists  = errors.New("role exists")           // add of a taken name
	ErrBuiltin = errors.New("role is built in")      // rm of a built-in without override
	ErrUnknown = errors.New("unknown role")          // edit, rm of an unknown name
	ErrBroken  = errors.New("role file is broken")   // edit of a user file that doesn't parse
	ErrRender  = errors.New("role prompt doesn't render") // Render: bad vars or template error
)

// Role is one role. Prompt is the body after the closing fence, trimmed.
type Role struct {
	Name        string
	Description string
	Model       string
	Pass        []string
	Fail        []string
	Vars        []string
	ClaudeArgs  []string
	Prompt      string
	Source      Source
}

// Parse reads a role file. Front matter is the lines between two "---"
// lines at the top; each is "key: value". description and model are the
// trimmed rest of the line; pass, fail, vars, and claude_args are JSON
// arrays of strings. An unknown or repeated key, or a missing fence, is
// ErrInvalid. Absent pass is ["PASS"], absent fail ["FAIL"], absent vars
// and claude_args []. The result must pass Validate.
func Parse(name string, raw []byte) (Role, error)

// Format writes a role file Parse reads back to the same Role: model is
// omitted when empty, lists are always written as JSON arrays, then a
// blank line and the prompt with one trailing newline.
func Format(r *Role) ([]byte, error)

// Validate checks, each failure ErrInvalid with the reason: the name
// (validName: profile's validName without the reserved name, plus 1-64
// bytes and a first character that is a letter or digit); description non-empty, one line, at most
// 120 runes; model without whitespace or control characters; pass and
// fail each non-empty, every verdict matching ^[A-Z][A-Z0-9_]{0,31}$, no
// duplicates, no verdict in both; vars valid per loop.ValidateVarKey, no
// duplicates, at most loop.MaxVars; no claude_args item equal to
// "--model" or "--settings" or starting with "--model=" or "--settings=";
// a prompt that isn't blank, parses as text/template with
// Option("missingkey=error"), and executes with {"Role": name, "Vars":
// every var mapped to "x"}.
func (r *Role) Validate() error

// Render is the prompt a child starts with. Every key in vars must be in
// r.Vars ("var %q isn't one of role %s's: A, B"). The template executes
// with {"Role": r.Name, "Vars": vars (an empty map when nil)} and
// missingkey=error. A result blank after TrimSpace is an error. When
// extra isn't blank, the result is rendered + "\n\n" + extra. Every
// error wraps ErrRender.
func (r *Role) Render(vars map[string]string, extra string) (string, error)

// Args are the arguments for claude: prompt, then "--model", r.Model when
// r.Model isn't empty, then r.ClaudeArgs, then after.
func (r *Role) Args(prompt string, after []string) []string
```

**`set.go`**: `Reader` (`New(dir)`, `Builtin()`, `Load`, `List`) and
`Set` (`NewSet(userDir)`, `Load(name) (Role, bool, error)`,
`List() (roles []Role, broken []string, err error)`, `isBuiltin`), exactly
as `internal/review/profile` does it, including: a user file overrides the
built-in of its name (`SourceOverride`); a user file that doesn't parse
hides the built-in in `List` and is an error from `Load`; names are
matched byte for byte against the directory listing; `List` sorts by name.

**`write.go`**: `Writer` (`NewWriter(dir)`, `Path(name)`,
`Add(ctx, Role) (Role, error)`, `Edit(ctx, name, *Changes) (Role, error)`,
`Remove(ctx, name) (Source, error)`) as profile's, with the lock file
`.lock` through `internal/lockfile`, and:

```go
// Changes are the fields an edit sets; nil leaves a field as it is.
type Changes struct {
	Description *string
	Model       *string
	Pass        *[]string
	Fail        *[]string
	Vars        *[]string
	ClaudeArgs  *[]string
	Prompt      *string
}
```

Every write calls `Validate` first (`ErrInvalid`), writes a temp file,
`Parse`s it back, and renames it into place.

**Acceptance**

- `Builtin().List()` returns `implementer`, `reviewer`, `verifier`, each
  valid, with the models, verdicts, and vars in the ADR's table and
  `ClaudeArgs` `["--permission-mode", "auto"]`.
- `implementer` rendered with TASK, TASKS, BASE, BRANCH contains
  `ws machine result PASS --file ../report.md` and the BRANCH value;
  rendered without BRANCH is `ErrRender`.
- `reviewer` rendered without KNOWN_ISSUES doesn't contain
  `known problems`; with `KNOWN_ISSUES=/x/base.md` contains
  `/x/base.md are known problems`.
- `Render`: a var not in `Vars` → `ErrRender` naming it; extra `"fix 1."`
  is appended after exactly one blank line; blank extra adds nothing.
- `Args("p", []string{"--model", "opus"})` on implementer →
  `["p", "--model", "haiku", "--permission-mode", "auto", "--model", "opus"]`;
  a role with no model and no args → `["p"]`.
- `Validate` refuses, each with `ErrInvalid`: `Name: "Bad"`, empty
  description, a 121-rune description, `Pass: []`, `Pass: ["pass"]`,
  `Pass: ["X"], Fail: ["X"]`, `Vars: ["1A"]`,
  `ClaudeArgs: ["--settings", "x"]`, `ClaudeArgs: ["--model=opus"]`, a
  blank prompt, `{{.Vars.NOPE}}` with `Vars: []`, and `{{ .Vars.X` (parse
  error).
- `Format` then `Parse` round-trips every field, for a role with and
  without a model.
- `Set`: a user `reviewer.md` → `List` has it as `override`; a user
  `tester.md` → `user`; an unparsable user `verifier.md` hides the
  built-in and is in `broken`.
- `Writer`: `Add` of `implementer` → `ErrExists`; `Add` of a new role then
  `Load` → `user`; `Edit("reviewer", Model: "opus")` writes an override
  keeping the built-in's prompt; `Remove("reviewer")` then → `override`
  and the built-in is back; `Remove("implementer")` → `ErrBuiltin`;
  `Remove("nope")`, `Edit("nope")` → `ErrUnknown`; an `Edit` that would
  be invalid leaves the file unchanged; `Edit` of an unparsable user file
  → `ErrBroken`.

**Verify**

```console
$ go test ./internal/machine/...
$ make lint
```

---

## T3. `ws machine result`, and `role` and `result` in `ws machine children`

**Depends on:** T1. **Parallel with:** none.

**Read:** `internal/cli/machine.go`, `internal/cli/machine_test.go` (the
first 100 lines: `machineTree`, `newMachineApp`, `runMachine`), `listJSON`
and `listWorkspaceJSON` in `internal/cli/list.go`, `exactlyOneArg` and
`readProfileGuide` in `internal/cli/review_profiles.go`,
`internal/cli/errors.go`, `internal/machine/result.go`.

**Create**

- `internal/cli/machine_result.go`, `internal/cli/machine_result_test.go`
- `internal/cli/guides/ws-machine-result.md`: `cp docs/design/machine-orch/guides/ws-machine-result.md internal/cli/guides/`

**Modify**

- `internal/cli/guides/ws-machine-children.md`: `cp docs/design/machine-orch/guides/ws-machine-children.md internal/cli/guides/`
- `internal/cli/machine.go`: add `a.newMachineResultCmd()` after
  `a.newMachineSpawnCmd()` in `newMachineCmd`; `runMachineChildren` prints
  `machineChildrenJSON`.
- `internal/cli/machine_test.go`: `TestApp_runMachineChildren` asserts
  `role` and `result`.
- `internal/cli/errors.go`: `codeResultInvalid errorCode = "result_invalid"`
  (comment `// Machine codes from ADR 0050.`), exit 2 in `exitCode`.
- `internal/cli/errors_test.go`: `codeResultInvalid` in "usage errors exit 2".
- `internal/cli/llm.go`: `"machine result"` after `"machine spawn"` in
  `guideTopics`.
- `internal/cli/guides/ws.md`: under "For agents", after the
  `ws llm machine spawn` line, add
  `  - \`ws llm machine result\`: record your verdict and report for the agent that started you`;
  add `` `result_invalid` `` at the end of the exit-2 row.

**Code**

```go
// machineResultInfoJSON is a child's recorded result (ADR 0050).
type machineResultInfoJSON struct {
	Verdict    string  `json:"verdict"`
	Outcome    string  `json:"outcome"`
	Report     *string `json:"report"`
	RecordedAt string  `json:"recorded_at"`
}

// resultInfo is rec's result, or nil when it has none.
func resultInfo(rec *machine.Record) *machineResultInfoJSON

// machineResultJSON is ws machine result's output.
type machineResultJSON struct {
	ID       string                `json:"id"`
	Root     string                `json:"root"`
	Role     *string               `json:"role"`
	Result   machineResultInfoJSON `json:"result"`
	Warnings []string              `json:"warnings"`
}

// machineChildJSON is ws list's workspace plus its role and result.
type machineChildJSON struct {
	listWorkspaceJSON
	Role   *string                `json:"role"`
	Result *machineResultInfoJSON `json:"result"`
}

type machineChildrenJSON struct {
	Workspaces  []machineChildJSON `json:"workspaces"`
	HiddenCount int                `json:"hidden_count"`
	Warnings    []string           `json:"warnings"`
}

func (a *App) newMachineResultCmd() *cobra.Command // Use "result <VERDICT> [--file <PATH> | --file -]", Args exactlyOneArg("a verdict"), RunE runE(a.withConfig(…)), flag --file (string)
func (a *App) runMachineResult(verdict, file string) error
// readReport reads --file: "" is no report (nil, nil); "-" is a.stdin;
// anything else the file. Empty, over machine.MaxReportSize, or
// unreadable is result_invalid. Read through io.LimitReader(MaxReportSize+1).
func (a *App) readReport(file string) ([]byte, error)
```

`runMachineResult`: `a.caller()`; `list, err := a.workspaces.List()`;
`root := machine.Root(list, caller)`; `machine.NormalizeVerdict` (error →
`result_invalid`); `store := machine.NewStore(a.paths.State)`;
`rec, err := store.Read(root, caller)`; if `rec.Spawn != nil &&
!rec.Spawn.Allows(v)` → `result_invalid`, message
`verdict X isn't one of role R's: PASS, FAIL` (pass then fail, joined with
`, `); `readReport`; `store.WriteResult(root, caller, &machine.Result{Verdict:
v, Ancestors: machine.Ancestors(list, caller), RecordedAt: a.now().UTC().Truncate(time.Second)}, report)`;
`store.Read` again and print `machineResultJSON` (role `optional(rec.Role())`).

`runMachineChildren`: as now, then `root := machine.Root(<the listing's
workspaces>, caller)`, and for each `listWorkspaceJSON` item read
`store.Read(root, *item.ID)`; a read error is a warning
(`a.warned = append(a.warned, …)`) and leaves `role`/`result` null.
`Outcome` in `resultInfo` is `string(outcome)`; `Report` is
`optional(rec.ReportPath)`.

**Acceptance** (use `newMachineApp`, with
`e.app.paths = config.NewPaths(t.TempDir())`)

- From `plan` (root `boss`): `result pass --file <tmp>/r.md` exits 0,
  prints `{"id":"plan","root":"boss","role":null,"result":{"verdict":"PASS","outcome":"pass","report":"<state>/boss/machine/plan/report.md","recorded_at":…},"warnings":[]}`,
  and the report file holds `r.md`'s bytes.
- `result FAIL` with no `--file` after one with a report: `report` is
  null and `report.md` is gone.
- `--file -` reads stdin (`e.app.stdin = strings.NewReader("ok")`).
- `result_invalid` (exit 2): `result "no way"`; an empty report file; a
  report of `MaxReportSize+1` bytes; a missing file; a verdict not in a
  `spawn.toml` written beforehand with `Pass ["PASS","FLAKY"] Fail ["FAIL"]`
  (`APPROVED` refused, message names `PASS, FLAKY, FAIL`; `flaky` accepted).
- No argument, or two → `usage`; outside a workspace → `outside_workspace`.
- `children` from `plan`: each workspace has `"role":null,"result":null`
  by default, and `a`'s `role` and `result` once `spawn.toml`
  (`Role: "implementer"`) and a `FAIL` result are written under `boss`;
  `outcome` `fail`; the keys `role` and `result` come after `review`.
- `go test ./internal/cli/ -run 'TestGuideTopics|TestErrorCode_exitCode'` passes.

**Verify**

```console
$ go test ./internal/cli/ -run 'TestApp_runMachineResult|TestApp_readReport|TestResultInfo|TestApp_runMachineChildren|TestGuideTopics|TestErrorCode_exitCode'
$ go test ./internal/cli/ ./internal/machine/
$ make lint
```

---

## T4. `ws machine wait`

**Depends on:** T3. **Parallel with:** none.

**Read:** `internal/cli/machine.go`, `internal/cli/machine_result.go`,
`runReviewWait` and `reviewFinished` in `internal/cli/review_report.go`,
`itemResults`, `itemErrorJSON`, and `finishItems` in `internal/cli/cache.go`
(lines 215-280), `State` constants in `internal/lifetime/lifetime.go`
(lines 95-145), `internal/cli/machine_test.go` (first 100 lines).

**Create**

- `internal/cli/machine_wait.go`, `internal/cli/machine_wait_test.go`
- `internal/cli/guides/ws-machine-wait.md`: `cp docs/design/machine-orch/guides/ws-machine-wait.md internal/cli/guides/`

**Modify**

- `internal/cli/machine.go`: add `a.newMachineWaitCmd()` after
  `a.newMachineResultCmd()`.
- `internal/cli/app.go`: field `machinePoll time.Duration` next to
  `reviewPoll`, set to `defaultMachinePoll` (5 s) in `New`.
- `internal/cli/errors.go`: `codeChildFailed errorCode = "child_failed"`,
  `codeWaitTimeout errorCode = "wait_timeout"`, both exit 1.
- `internal/cli/errors_test.go`: both in "failures exit 1".
- `internal/cli/llm.go`: `"machine wait"` after `"machine result"`.
- `internal/cli/guides/ws.md`: after the `ws llm machine result` line add
  `  - \`ws llm machine wait\`: wait for children to finish, then print each one's verdict`;
  add `` `child_failed`, `wait_timeout` `` at the end of the exit-1 row.

**Code**

```go
// defaultMachinePoll is how often ws machine wait reads the sessions.
const defaultMachinePoll = 5 * time.Second

// defaultMachineWaitTimeout is ws machine wait's --timeout default.
const defaultMachineWaitTimeout = 60 * time.Minute

type machineWaitOptions struct {
	any     bool
	timeout time.Duration
}

// waitTarget is a child ws machine wait waits on.
type waitTarget struct {
	id   string
	gone bool // named by id after its workspace was removed
	// quiet is whether the previous poll saw it idle or stopped with no
	// busy descendant.
	quiet bool
}

// machineWaitItemJSON is one child in ws machine wait's output.
type machineWaitItemJSON struct {
	ID       string                 `json:"id"`
	Session  *string                `json:"session"`
	Role     *string                `json:"role"`
	Status   string                 `json:"status"`
	Finished bool                   `json:"finished"`
	Outcome  string                 `json:"outcome"`
	Result   *machineResultInfoJSON `json:"result"`
	Error    *itemErrorJSON         `json:"error"`
}

func (a *App) newMachineWaitCmd() *cobra.Command // Use "wait [--any] [--timeout <DURATION>] <WORKSPACE>...", flags --any, --timeout (default defaultMachineWaitTimeout); no args → usage "needs a workspace"
func (a *App) runMachineWait(ctx context.Context, args []string, opts *machineWaitOptions) error
// waitTargets resolves args, in order, without duplicates (see below).
func (a *App) waitTargets(args []string, list []workspace.Workspace, caller, root string, store *machine.Store) ([]waitTarget, error)
// childState is one poll's view of a target.
type childState struct {
	status   string
	session  string
	quiet    bool // idle or stopped, and no descendant working or starting
	finished bool
	record   machine.Record
}
func pollChild(t *waitTarget, entries []lifetime.Entry, rec machine.Record) childState
```

**Behavior**

1. `--timeout` ≤ 0 → `usage`. `caller := a.caller()`.
   `list, err := a.workspaces.List()`; `root := machine.Root(list, caller)`;
   `below := descendants(list, caller)`.
2. `waitTargets`, for each arg: `a.workspaces.Find(arg)`. Found and in
   `below` → its id. Found and not in `below` →
   `not_descendant` ("%s is not a workspace this one started"), failing
   the command. Not found (`errors.Is(err, workspace.ErrNotFound)`):
   `store.Read(root, arg)`; if `rec.Found()` and `slices.Contains(rec.Ancestors(), caller)`
   → `waitTarget{id: arg, gone: true}`; otherwise `workspace_not_found`
   (use `domainError(err)`). Skip an id already listed.
3. Loop: a `time.NewTimer(opts.timeout)`; each poll calls
   `a.lifetime.List(ctx)` and, for each target,
   `store.Read(root, id)` (an error is one warning per target and an empty
   record) and `pollChild`:
   - target gone (flag, or no entry with its id) → status `gone`,
     `finished` true;
   - status is `string(entry.Session.State)`, or `starting` when
     `Session` is nil; session is `entry.Session.ID` or empty;
   - `quiet` = status is `idle` or `stopped`, and no entry that is a
     descendant of the target (use `descendants` over the listing's
     workspaces) has state `working` or `starting` (or a nil Session);
   - `finished` = `quiet && (record.Result != nil || t.quiet)`; then set
     `t.quiet = quiet` for the next poll.
4. Return when every target is finished, or with `--any` when one is.
   Otherwise `select` on `ctx.Done()` (return the wrapped `ctx.Err()`),
   the timer (stop, marking `timedOut`), or `time.After(a.machinePoll)`.
5. Output through `itemResults` and `a.finishItems("children", items, &results)`.
   Per item: `outcome` is `pending` when not finished; `missing` when
   finished with no result; else the record's outcome. `role` and
   `result` from the record (`optional`, `resultInfo`). Its error:
   finished with `fail` or `missing` → `child_failed`
   ("%s finished with verdict %s" / "%s finished without recording a verdict");
   not finished at the timeout → `wait_timeout`
   ("%s is still %s after %s; run ws machine wait again to keep waiting"),
   except with `--any` when another child finished, then no error.

**Acceptance** (`newMachineApp`, paths in `t.TempDir()`,
`e.app.machinePoll = time.Millisecond`; script the listing per poll with a
counter in `e.lifetime.ListFunc`, setting `Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "s-a", State: …}`)

- `a` idle with a `PASS` result on the first poll → returns after one
  poll, exit 0, item `{"id":"a","session":"s-a","role":null,"status":"idle","finished":true,"outcome":"pass",…,"error":null}`.
- `a` idle with no result: not finished on poll 1, finished on poll 2 →
  `outcome` `missing`, `error.code` `child_failed`, exit 1.
- `a` idle with a result while its child `a1` is `working` → not finished
  until `a1` is `idle`.
- `a` working → working → idle+result: returns on the third poll.
- `--timeout 20ms` with `a` always working → exit 1, `wait_timeout`,
  `finished` false, `outcome` `pending`.
- `--any a b`: `b` finishes with `PASS`, `a` still working → exit 0, `a`
  has no error; `--any` with neither finished at the timeout → both
  `wait_timeout`.
- `a` finished with `FAIL` and `b` with `PASS` → exit 1, only `a` has an
  error.
- Gone: `a` not in `Find` but `spawn.toml`/`result.toml` with
  `Ancestors: [plan, boss]` under `boss` → `status` `gone`, finished at
  once; with `Ancestors: [other, boss]` → `workspace_not_found`; an
  unknown id → `workspace_not_found` (exit 3); `other` (exists, not
  below `plan`) → `not_descendant` (exit 4); `plan` itself →
  `not_descendant`.
- `wait a a` waits on one item; no args → `usage`; `--timeout 0` →
  `usage`; outside a workspace → `outside_workspace`.
- The cancelled context returns an error without output.

**Verify**

```console
$ go test ./internal/cli/ -run 'TestApp_runMachineWait|TestApp_waitTargets|TestPollChild|TestGuideTopics|TestErrorCode_exitCode'
$ go test ./internal/cli/
$ make lint
```

---

## T5. `ws machine roles` (list, show, add, edit, rm)

**Depends on:** T2, T4. **Parallel with:** none.

**Read:** `internal/cli/review_profiles.go` (mirror it),
`internal/cli/review_profiles_test.go` (lines 1-130, the test harness),
`internal/config/paths.go`, `internal/machine/role/role.go`, `write.go`.

**Create**

- `internal/cli/machine_roles.go`, `internal/cli/machine_roles_test.go`
- `internal/cli/guides/`: `cp docs/design/machine-orch/guides/ws-machine-roles*.md internal/cli/guides/`
  (five files: `roles`, `roles-show`, `roles-add`, `roles-edit`, `roles-rm`)

**Modify**

- `internal/config/paths.go`: `MachineRoles string` (comment
  `// MachineRoles holds the user's ws machine roles (ADR 0050).`), set to
  `filepath.Join(root, "machine", "roles")`; its test, if `paths_test.go`
  lists fields.
- `internal/cli/machine.go`: add `a.newMachineRolesCmd()` last in
  `newMachineCmd`.
- `internal/cli/errors.go`: `codeRoleInvalid` (`role_invalid`, exit 2),
  `codeRoleUnknown` (`role_unknown`, exit 3), `codeRoleExists`
  (`role_exists`, exit 4), `codeRoleBuiltin` (`role_builtin`, exit 4);
  `errors_test.go` accordingly.
- `internal/cli/llm.go`: after `"machine rm"`: `"machine roles"`,
  `"machine roles show"`, `"machine roles add"`, `"machine roles edit"`,
  `"machine roles rm"`.
- `internal/cli/guides/ws.md`: after the `ws llm machine rm` line add

  ```
    - `ws llm machine roles`: the roles children can be started as
    - `ws llm machine roles show`: one role, with its prompt template
    - `ws llm machine roles add`: add a role
    - `ws llm machine roles edit`: edit a role, or override a built-in
    - `ws llm machine roles rm`: remove a role, or restore a built-in
  ```

  and add `` `role_invalid` `` to the exit-2 row, `` `role_unknown` `` to
  exit 3, `` `role_exists`, `role_builtin` `` to exit 4.

**Code**

```go
type roleOptions struct {
	description, model, promptFile string
	pass, fail, vars               []string // StringSliceVar: repeatable or comma-separated
	claudeArgs                     []string // StringArrayVar "claude-arg": one argument each, kept in order
	noVars, noClaudeArgs           bool
}

func (a *App) newMachineRolesCmd() *cobra.Command     // Use "roles", runnable (lists), noArgs, with the four subcommands
func (a *App) newMachineRolesShowCmd() *cobra.Command // "show <NAME>", exactlyOneArg("a role name")
func (a *App) newMachineRolesAddCmd() *cobra.Command  // "add <NAME> --description <D> … (--prompt-file <F> | -)", roleArgs
func (a *App) newMachineRolesEditCmd() *cobra.Command // "edit <NAME> … [--prompt-file <F> | -]", roleArgs
func (a *App) newMachineRolesRmCmd() *cobra.Command   // "rm <NAME>", exactlyOneArg("a role name")
func roleArgs(cmd *cobra.Command, args []string) error  // a name, then optionally "-"
func (a *App) newRole(cmd *cobra.Command, args []string, opts *roleOptions) (role.Role, error)
func (a *App) roleChanges(cmd *cobra.Command, args []string, opts *roleOptions) (*role.Changes, error)
func (a *App) runMachineRoles() error
func (a *App) runMachineRolesShow(name string) error
func (a *App) runMachineRolesAdd(ctx context.Context, r *role.Role) error
func (a *App) runMachineRolesEdit(ctx context.Context, name string, changes *role.Changes) error
func (a *App) runMachineRolesRm(ctx context.Context, name string) error
func roleWriteError(err error) error // as profileWriteError: ErrInvalid→role_invalid, ErrExists→role_exists, ErrBuiltin→role_builtin, ErrUnknown→role_unknown, ErrBroken→config_invalid

type machineRoleInfoJSON struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Model       string   `json:"model"`
	Pass        []string `json:"pass"`
	Fail        []string `json:"fail"`
	Vars        []string `json:"vars"`
	ClaudeArgs  []string `json:"claude_args"`
	Source      string   `json:"source"`
}
type machineRolesJSON struct {
	Roles    []machineRoleInfoJSON `json:"roles"`
	Warnings []string              `json:"warnings"`
}
type machineRoleDetailJSON struct {
	machineRoleInfoJSON
	Prompt   string   `json:"prompt"`
	Path     *string  `json:"path"` // null for SourceBuiltin
	Warnings []string `json:"warnings"`
}
type machineRoleRemovedJSON struct {
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Restored bool     `json:"restored"`
	Warnings []string `json:"warnings"`
}
```

- The roles commands don't call `a.caller()`: they work outside a
  workspace. Use `a.withConfig` like `ws review profiles`.
- `role.NewSet(a.paths.MachineRoles)` and `role.NewWriter(a.paths.MachineRoles)`.
  Each `broken` file from `List` is a warning
  (`"skipped role file %s: it can't be parsed"`).
- `add`: `--description` required (`usage`); a prompt from `--prompt-file`
  or `-` (`usage` for neither, and for both); `--pass` default `PASS`,
  `--fail` default `FAIL` when not given; verdicts upper-cased.
  `edit`: only flags with `cmd.Flags().Changed` become changes (so
  `--model ""` clears); nothing changed → `usage`; `--vars` with
  `--no-vars`, or `--claude-arg` with `--no-claude-args` → `usage`.
- `show`/`edit`/`rm` of an unknown name → `role_unknown`.
- A `role.ErrRender` never comes from these commands.

**Acceptance** (`config.NewPaths(t.TempDir())`, as in the profiles tests)

- `roles` lists `implementer`, `reviewer`, `verifier` with `source`
  `builtin`, `model`s `haiku`, `sonnet`, `haiku`, and the `vars`,
  `pass`, `fail`, `claude_args` of the ADR; works with `getwd` outside
  any workspace.
- `roles show implementer`: `prompt` contains `ws machine result`,
  `path` null.
- `roles add tester --description Smoke --vars X --pass ok --fail bad --prompt-file p.md`
  (p.md `Do {{.Vars.X}}.`) → `source` `user`, `pass` `["OK"]`,
  `fail` `["BAD"]`, `path` `<dir>/machine/roles/tester.md`; again →
  `role_exists` (exit 4); with stdin `-` works; prompt
  `Do {{.Vars.Y}}.` with `--vars X` → `role_invalid` (exit 2); no
  `--description` → `usage`.
- `roles edit reviewer --model opus` → `source` `override`, `model`
  `opus`, prompt unchanged; `roles edit reviewer` alone → `usage`;
  `roles edit tester --no-vars` (prompt uses X) → `role_invalid`, file
  unchanged.
- `roles rm reviewer` → `restored` true, then `show reviewer` is
  `builtin`; `roles rm implementer` → `role_builtin` (exit 4);
  `roles rm nope` → `role_unknown` (exit 3).
- An unparsable `verifier.md` in the user dir → listed in `warnings`,
  and `verifier` absent from `roles`.

**Verify**

```console
$ go test ./internal/cli/ -run 'TestApp_newMachineRoles|TestApp_runMachineRoles|TestRoleArgs|TestRoleWriteError|TestGuideTopics|TestErrorCode_exitCode'
$ go test ./internal/cli/ ./internal/config/ ./internal/machine/...
$ make lint
```

---

## T6. `ws machine spawn --role --var`, and the overview guides

**Depends on:** T1, T2, T5. **Parallel with:** none.

**Read:** `internal/cli/machine.go`, `internal/cli/claude.go` (lines
1-110 and 185-260: `claudeOptions`, `runClaude`, `printClaudeResult`,
`claudeResultOf`, `claudeArgsAfterDash`), `TestApp_runMachineSpawn` in
`internal/cli/machine_test.go`, `ParseVars` in `internal/loop/loop.go`,
`internal/machine/role/role.go`, `internal/machine/store.go`.

**Create**

- nothing new in Go; tests go in `internal/cli/machine_test.go`.

**Modify**

- `internal/cli/claude.go`: two optional hooks on `claudeOptions`:

  ```go
  // started, when set, runs after the session is recorded and before the
  // result is printed (ADR 0050).
  started func(ws *workspace.Workspace, session string)
  // result, when set, is what --json prints instead of r.
  result func(r claudeResult) any
  ```

  `runClaude` calls `opts.started(&ws, started.ID)` right after
  `lock.Unlock()`; `printClaudeResult` takes the hook and, with `--json`,
  writes `opts.result(a.claudeResultOf(ws, session))` when it's set. With
  neither hook set, `ws claude` behaves and prints exactly as before.
- `internal/cli/machine.go`: `spawn` gains `--role <name>` and
  `--var KEY=VALUE` (StringArrayVar); its `Args` no longer use
  `promptAfterDash` directly but `machineSpawnArgs`, below.
- `internal/cli/guides/ws-machine-spawn.md` and `ws-machine.md`:
  `cp docs/design/machine-orch/guides/ws-machine-spawn.md docs/design/machine-orch/guides/ws-machine.md internal/cli/guides/`.
- `internal/cli/guides/ws.md`:
  - "Rules for agents": replace the bullet starting "To hand work to
    another agent" with:

    ```
    - To hand work to another agent, use `ws machine spawn <spec>... --
      "<prompt>"`, or `--role <name> --var KEY=VALUE...` for a stored role
      (`ws machine roles`), and keep the returned id. Wait for it with
      `ws machine wait <id>` as a background command, not by polling;
      follow it with `claude logs <session>`.
    - If an agent started you to do a task, finish with `ws machine result
      <VERDICT> --file <report>`: the verdicts your prompt names, else
      `PASS` or `FAIL`.
    ```
  - "For agents": replace the `ws llm machine` line's description with
    `the commands to use: start, wait for, list, and remove the workspaces yours started, and record your verdict:`.
  - "Use cases": after the `claude logs e8cdf5d8` line add

    ```
    $ ws machine wait fix-login-d8efb24f     # as a background command

    # Start a child from a stored role, and record a verdict as a child
    $ ws machine spawn --name t1-impl --role implementer --var TASK=T1 --var TASKS=docs/tasks.md --var BASE=feat/x --var BRANCH=feat/x-t1 buff-bear@feat/x
    $ ws machine result PASS --file ../report.md
    ```
- `internal/cli/app.go`: none (the `ws --help` text already points agents
  to `ws machine`).

**Code**

```go
// machineSpawnOptions is ws machine spawn's: ws claude's plus a role.
type machineSpawnOptions struct {
	claudeOptions
	role string
	vars []string
}

// machineSpawnJSON is ws claude --json's object plus the role.
type machineSpawnJSON struct {
	claudeResult
	Role *string `json:"role"`
}

// machineSpawnArgs is specsBeforeDash and claudeArgsAfterDash, and
// promptAfterDash only when --role isn't given.
func machineSpawnArgs(role *string) cobra.PositionalArgs

// spawnRole loads the role, checks and renders the prompt, and rewrites
// opts.claudeArgs to role.Args(prompt, <args after the prompt>). It runs
// before runClaude, so nothing is created on an error.
func (a *App) spawnRole(opts *machineSpawnOptions) (role.Role, map[string]string, error)
```

`runMachineSpawn(ctx, opts *machineSpawnOptions)`:

1. `caller := a.caller()`.
2. `--var` without `--role` → `usage` ("--var needs --role").
3. With `--role`: `role.NewSet(a.paths.MachineRoles).Load(name)`: not
   found → `role_unknown`; a load error → `config_invalid`.
   `loop.ParseVars(opts.vars)` (error → `usage`); `extra` is
   `claudeArgs[0]` when there is one, else `""`; `r.Render(vars, extra)`
   (error → `usage`); `opts.claudeArgs = r.Args(prompt, claudeArgs[1:])`.
4. `opts.parent = caller`; `opts.result` wraps the result in
   `machineSpawnJSON` with `Role: optional(name)` (also without a role, so
   `role` is always present, `null`); with a role, `opts.started` writes
   `machine.NewStore(a.paths.State).WriteSpawn(root, ws.ID, &machine.Spawn{Role, Model, Pass, Fail, Vars, Ancestors: machine.Ancestors(list, ws.ID)…, SpawnedAt: a.now().UTC().Truncate(time.Second)})`,
   where `list` is `a.workspaces.List()` read in the hook and `root` is
   `machine.Root(list, ws.ID)`; any error there is appended to `a.warned`
   (`"couldn't record role %s for %s: %v"`), never returned.
5. `a.runClaude(ctx, &opts.claudeOptions)`.

**Acceptance** (`newMachineApp` with paths in `t.TempDir()`; assert on
the arguments `e.claude` received, as `TestApp_runMachineSpawn` does)

- `spawn --role implementer --var TASK=T1 --var TASKS=t.md --var BASE=feat/x --var BRANCH=feat/x-t1 buff-bear`
  → `claude` gets the rendered prompt (contains `feat/x-t1` and
  `ws machine result PASS`), then `--model haiku --permission-mode auto`;
  output has `"role":"implementer"` after `warnings`; `spawn.toml` exists
  under `<state>/boss/machine/<new id>/` with `role`, `pass ["PASS"]`,
  `fail ["FAIL"]`, the four vars, and `ancestors` `["plan","boss"]`.
- `… buff-bear -- "Fix item 1." --model opus` → prompt ends with
  `"\n\nFix item 1."`, and the args end `--permission-mode auto --model opus`.
- `--role implementer` without `--var BRANCH` → `usage`, nothing created
  (`e.store.CreateFunc` not called); `--var NOPE=1` → `usage`;
  `--var TASK=1 --var TASK=2` → `usage`; `--role nope` → `role_unknown`
  (exit 3); `--var X=1` without `--role` → `usage`; no prompt and no
  `--role` → `usage` (as before).
- Without `--role`, the output has `"role":null` and no `spawn.toml` is
  written; `ws claude --json` output is unchanged (no `role` key).
- A `WriteSpawn` failure (make `<state>/boss` a file) → exit 0 with a
  warning naming the role.
- `go test ./internal/cli/ -run TestGuideTopics` passes with the new
  `ws-machine.md` and `ws-machine-spawn.md`.

**Verify**

```console
$ go test ./internal/cli/ -run 'TestApp_runMachineSpawn|TestApp_newMachineSpawnCmd|TestMachineSpawnArgs|TestApp_spawnRole|TestApp_runClaude|TestGuideTopics'
$ go test ./...
$ make lint
```

---

## Smoke test

For the final verifier, on the merged `feat/machine-orch`. It needs no
`claude` session: it builds hand-made workspace records, which `ws` keeps
for a minute before the removal pass treats them as abandoned, so run the
block in one go. Each line must succeed (`set -e`); the ones expected to
fail check their exit code.

```zsh
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
BIN=$(mktemp -d)/ws; go build -o "$BIN" ./cmd/ws
export WS_CONFIG_DIR=$(mktemp -d)
ws() { "$BIN" "$@"; }
code() { set +e; "$@" >/dev/null 2>&1; local c=$?; set -e; echo $c; }

# Guides
for t in machine "machine wait" "machine result" "machine children" "machine spawn" \
         "machine roles" "machine roles show" "machine roles add" "machine roles edit" "machine roles rm"; do
  ws llm ${=t} >/dev/null
done
ws machine --help >/dev/null; ws machine wait --help >/dev/null; ws machine result --help >/dev/null

# Roles, outside any workspace
cd "$WS_CONFIG_DIR"
ws machine roles | jq -e '[.roles[].name] == ["implementer","reviewer","verifier"] and all(.roles[]; .source=="builtin")'
ws machine roles show implementer | jq -e '.model=="haiku" and .path==null and (.prompt|contains("ws machine result"))'
ws machine roles show verifier | jq -e '.pass==["PASS","FLAKY"] and .fail==["FAIL"]'
printf 'Do {{.Vars.X}}.\n' > p.md; printf 'Do {{.Vars.Y}}.\n' > bad.md
ws machine roles add tester --description Smoke --vars X --pass ok --fail bad --prompt-file p.md | jq -e '.source=="user" and .pass==["OK"]'
[[ $(code ws machine roles add tester --description Smoke --vars X --prompt-file p.md) == 4 ]]
[[ $(code ws machine roles add broken --description Smoke --vars X --prompt-file bad.md) == 2 ]]
ws machine roles edit reviewer --model opus | jq -e '.source=="override" and .model=="opus"'
ws machine roles rm reviewer | jq -e '.restored==true'
[[ $(code ws machine roles rm implementer) == 4 ]]
[[ $(code ws machine roles show nope) == 3 ]]
[[ $(code ws machine result PASS) == 2 ]]          # outside_workspace

# A hand-made tree: boss <- kid, boss <- kid2
mk() { local id=$1 parent=$2 v=1 extra=""
  mkdir -p "$WS_CONFIG_DIR/state/$id" "$WS_CONFIG_DIR/workspaces/$id"
  [[ -n $parent ]] && { v=3; extra="parent = \"$parent\""; }
  printf 'version = %s\nid = "%s"\nname = "%s"\npath = "%s"\ncreated_at = 2026-11-06T00:00:00Z\n%s\nrepos = []\n' \
    $v $id $id "$WS_CONFIG_DIR/workspaces/$id" "$extra" > "$WS_CONFIG_DIR/state/$id/workspace.toml"; }
ws list --json >/dev/null
mk boss ""; mk kid boss; mk kid2 boss
echo "all good" > "$WS_CONFIG_DIR/r.md"

cd "$WS_CONFIG_DIR/workspaces/kid"
ws machine result pass --file "$WS_CONFIG_DIR/r.md" | jq -e '.root=="boss" and .result.verdict=="PASS" and .result.outcome=="pass" and (.result.report|endswith("/state/boss/machine/kid/report.md"))'
[[ $(code ws machine result "no way") == 2 ]]
[[ $(code ws machine wait boss) == 4 ]]            # not_descendant
cd "$WS_CONFIG_DIR/workspaces/kid2"
ws machine result FAIL | jq -e '.result.outcome=="fail" and .result.report==null'

cd "$WS_CONFIG_DIR/workspaces/boss"
ws machine children | jq -e '[.workspaces[] | {id, role, v: .result.verdict}] | sort_by(.id) == [{"id":"kid","role":null,"v":"PASS"},{"id":"kid2","role":null,"v":"FAIL"}]'
[[ $(code ws machine wait nope) == 3 ]]            # workspace_not_found
[[ $(code ws machine wait --timeout 0s kid) == 2 ]]
# kid has no session, so it reads as starting and never finishes
set +e; out=$(ws machine wait --timeout 2s kid); c=$?; set -e
[[ $c == 1 ]]; echo "$out" | jq -e '.children[0].error.code=="wait_timeout" and .children[0].outcome=="pending"'
# Once removed, a child is gone and its kept result is read
rm -rf "$WS_CONFIG_DIR/workspaces/kid" "$WS_CONFIG_DIR/state/kid" "$WS_CONFIG_DIR/workspaces/kid2" "$WS_CONFIG_DIR/state/kid2"
ws machine wait kid | jq -e '.children[0].status=="gone" and .children[0].outcome=="pass" and .children[0].error==null'
set +e; out=$(ws machine wait kid kid2); c=$?; set -e
[[ $c == 1 ]]; echo "$out" | jq -e '.children[1].error.code=="child_failed" and .children[0].error==null'

# spawn --role checks that fail before anything is created (no claude needed)
[[ $(code ws machine spawn --role implementer --var TASK=T1) == 2 ]]       # BRANCH etc. missing
[[ $(code ws machine spawn --role implementer --var NOPE=1) == 2 ]]
[[ $(code ws machine spawn --role nope -- "x") == 3 ]]
[[ $(code ws machine spawn --var X=1 -- "x") == 2 ]]
[[ $(ls "$WS_CONFIG_DIR/workspaces" | grep -cv -e '^boss$' -e '^CLAUDE.md$' || true) == 0 ]]   # nothing was created
echo SMOKE PASS
```

If a real `claude` is available and trusted, also run, from inside a
hand-made workspace, `ws machine spawn --role verifier --var BRANCH=main
--var CHECKS="true" -- "" --model haiku` against a cached repo, then
`ws machine wait <id>` in the background: it should exit 0 with
`"verdict":"PASS"`. This is optional; the verifier notes whether it ran.
