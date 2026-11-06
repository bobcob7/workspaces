# Agent loop scheduler: implementation tasks

Implements [0048](../../adrs/0048-agent-loop-scheduler.md) and
[`ws loop`](../../tools/ws-loop.md). Do the tasks in order; each one builds,
passes its tests, and can be merged on its own. Each task is one PR
(conventional commit, imperative mood, e.g. `feat: add internal/loop
records and store (ADR 0048)`).

**Read before any task:** `docs/tools/ws-loop.md` (the behavior and every
JSON shape) and the "Decision" section of `docs/adrs/0048-agent-loop-scheduler.md`.
When this file and those two disagree, they win.

**Rules for every task**

- Go: follow the existing code in the package you edit. Errors are
  wrapped with `fmt.Errorf("…: %w", err)`. Tests use
  `github.com/stretchr/testify/assert` and `require`, table tests where
  there are several cases, and `t.TempDir()` for files.
- Run `make fmt` before committing. `make lint` must pass: it also
  forbids running `git`, `claude`, or `gh` outside their packages, so
  never call `exec.Command` in new code.
- JSON fields are `snake_case`; times are `formatTime(t)` (RFC 3339, UTC,
  whole seconds); an empty optional string is `null` (use `optional(s)`
  from `internal/cli/cache.go`); lists are `[]`, never `null`.
- Don't change any existing JSON field, error code, or exit code.

---

## T1. `internal/loop`: records, validation, schedule, and store

**Depends on:** nothing.

**Create**

- `internal/loop/loop.go`
- `internal/loop/schedule.go`
- `internal/loop/store.go`
- `internal/loop/loop_test.go`
- `internal/loop/schedule_test.go`
- `internal/loop/store_test.go`

**Modify**

- `internal/config/paths.go`: add a field `Loops string` to `Paths`
  (comment: `// Loops holds the agent loops (ADR 0048).`) and set it in
  `NewPaths` to `filepath.Join(root, "loops")`.
- `internal/config/paths_test.go`: assert `Loops` is `<root>/loops`.

**`loop.go`**

```go
// Package loop keeps agent loops: a named prompt that ws starts in a new
// workspace every period (ADR 0048).
package loop

// FormatVersion is the newest loop record version this ws reads and writes.
const FormatVersion = 1

const (
	MinEvery    = 5 * time.Minute
	MaxEvery    = 168 * time.Hour
	DefaultKeep = 5
	MaxKeep     = 50
	// MaxRuns is how many runs a record keeps, oldest dropped first.
	MaxRuns = 20
	maxNameLen = 40
)

var (
	ErrInvalid  = errors.New("invalid loop")
	ErrNotFound = errors.New("loop not found")
	ErrExists   = errors.New("loop already exists")
)

// Run is one run of a loop: the workspace and session it started, or why
// it couldn't start.
type Run struct {
	StartedAt    time.Time `toml:"started_at"`
	Workspace    string    `toml:"workspace"`
	Session      string    `toml:"session"`
	ErrorCode    string    `toml:"error_code"`
	ErrorMessage string    `toml:"error_message"`
}

// Loop is CONFIG_DIR/loops/<Name>.toml.
type Loop struct {
	Version        int       `toml:"version"`
	Name           string    `toml:"name"`
	Every          string    `toml:"every"`
	Prompt         string    `toml:"prompt"`
	Specs          []string  `toml:"specs"`
	Model          string    `toml:"model"`
	PermissionMode string    `toml:"permission_mode"`
	Keep           int       `toml:"keep"`
	Paused         bool      `toml:"paused"`
	CreatedAt      time.Time `toml:"created_at"`
	NextRunAt      time.Time `toml:"next_run_at"`
	Runs           []Run     `toml:"runs"`
}

// ValidateName: 1–40 chars of a-z, 0-9, '-', first char a letter or digit.
func ValidateName(name string) error

// ParseEvery parses a Go duration of whole minutes in [MinEvery, MaxEvery].
func ParseEvery(s string) (time.Duration, error)

// FormatEvery prints d without zero units: 24h, 1h30m, 15m.
func FormatEvery(d time.Duration) string

// Interval is ParseEvery(l.Every).
func (l *Loop) Interval() (time.Duration, error)

// Validate checks Name, Every, Prompt (not empty after strings.TrimSpace),
// and Keep (1..MaxKeep).
func (l *Loop) Validate() error

// LastRun is the newest run, or nil.
func (l *Loop) LastRun() *Run
```

- Every validation error wraps `ErrInvalid` and says what's wrong, e.g.
  `fmt.Errorf("%w: period %q is under 5m", ErrInvalid, s)`.
- `FormatEvery`: take `d.String()`, then strip a trailing `"0s"`, then a
  trailing `"0m"` (`"24h0m0s"` → `"24h"`, `"1h30m0s"` → `"1h30m"`,
  `"15m0s"` → `"15m"`).
- `ParseEvery` rejects a duration that isn't a whole number of minutes
  (`d%time.Minute != 0`).

**`schedule.go`**

```go
// Due reports whether the loop should start a run at now: not paused, and
// NextRunAt is not after now.
func (l *Loop) Due(now time.Time) bool

// Advance moves NextRunAt forward one interval after a run started or
// failed. If that is not after now, NextRunAt becomes now + interval.
func (l *Loop) Advance(now time.Time) error

// Resume clears Paused; if NextRunAt is before now it becomes now + interval.
func (l *Loop) Resume(now time.Time) error

// AddRun appends r and drops the oldest runs beyond MaxRuns.
func (l *Loop) AddRun(r Run)
```

**`store.go`**

```go
// Store keeps loop records in dir, one <name>.toml each.
type Store struct{ dir string }

func NewStore(dir string) *Store

// Lock takes dir/.lock exclusively; RLock takes it shared. Both create dir
// (0o700) first and return the unlock function. Use internal/lockfile.
func (s *Store) Lock(ctx context.Context) (func(), error)
func (s *Store) RLock(ctx context.Context) (func(), error)

// List reads every <name>.toml, sorted by name. Names starting with "."
// and files not ending in ".toml" are ignored. A missing dir is no loops.
// A file that doesn't parse, or whose Version > FormatVersion, is skipped
// and described in a warning that names the file.
func (s *Store) List() (loops []Loop, warnings []string, err error)

// Get reads one loop; ErrNotFound when the file doesn't exist.
func (s *Store) Get(name string) (Loop, error)

// Create writes a new record; ErrExists when the file exists.
func (s *Store) Create(l *Loop) error

// Save overwrites a record. Both Create and Save set Version to
// FormatVersion, then write <name>.toml.tmp and rename it into place.
func (s *Store) Save(l *Loop) error

// Remove deletes the record; ErrNotFound when it doesn't exist.
func (s *Store) Remove(name string) error
```

- TOML through `github.com/BurntSushi/toml`, as `internal/workspace/record.go` does.
- `Get`, `Create`, `Save`, and `Remove` call `ValidateName(name)` first,
  so a name can never be a path.
- The store doesn't lock; callers hold `Lock` around read-modify-write.

**Acceptance**

- `ValidateName`: `"a"`, `"deps-bump"`, 40 chars ok; `""`, `"-x"`,
  `"A"`, `"a_b"`, `"a/b"`, 41 chars → `ErrInvalid`.
- `ParseEvery`: `5m`, `15m`, `1h30m`, `24h`, `168h` ok; `4m`, `169h`,
  `90s`, `1h0m30s`, `x`, `""` → `ErrInvalid`.
- `FormatEvery` round-trips each valid case above.
- `Due`: false when paused; true at exactly `NextRunAt`; false one second before.
- `Advance`: from `NextRunAt = 10:00`, `every = 1h`, `now = 10:00:30` →
  `11:00`; `now = 13:20` → `14:20`.
- `Resume`: overdue → `now + every`; future `NextRunAt` unchanged.
- `AddRun` keeps the newest 20.
- Store: Create then Get round-trips every field; Create twice →
  `ErrExists`; Get/Remove of a missing loop → `ErrNotFound`; List of a
  missing dir is empty with no error; a garbage `bad.toml` and a
  `version = 2` file each give one warning and are skipped; `.lock` and
  `x.toml.tmp` are ignored; no `.tmp` file remains after Save.

**Verify**

```console
$ go test ./internal/loop/... ./internal/config/...
$ make lint
```

---

## T2. `ws loop add | list | pause | resume | rm`

**Depends on:** T1.

**Create**

- `internal/cli/loop.go`
- `internal/cli/loop_test.go`
- `internal/cli/guides/ws-loop.md`, `ws-loop-add.md`, `ws-loop-list.md`,
  `ws-loop-pause.md`, `ws-loop-resume.md`, `ws-loop-rm.md`

**Modify**

- `internal/cli/errors.go`: add, under a comment `// From ADR 0048.`,
  `codeLoopInvalid = "loop_invalid"`, `codeLoopNotFound = "loop_not_found"`,
  `codeLoopExists = "loop_exists"`, and put them in `exitCode()`:
  `loop_invalid` → `exitUsage`, `loop_not_found` → `exitNotFound`,
  `loop_exists` → `exitRefused`.
- `internal/cli/cache.go`, `codedDomainError`'s table: add
  `{loop.ErrInvalid, codeLoopInvalid}`, `{loop.ErrNotFound, codeLoopNotFound}`,
  `{loop.ErrExists, codeLoopExists}`.
- `internal/cli/interfaces.go`: add

  ```go
  // loopStore keeps agent loops (ADR 0048).
  type loopStore interface {
  	Lock(ctx context.Context) (func(), error)
  	RLock(ctx context.Context) (func(), error)
  	List() ([]loop.Loop, []string, error)
  	Get(name string) (loop.Loop, error)
  	Create(l *loop.Loop) error
  	Save(l *loop.Loop) error
  	Remove(name string) error
  }
  ```

  Don't add it to the `go:generate` line; tests use a real `loop.Store`.
- `internal/cli/app.go`: add `loops loopStore` to `App` and to `stores`;
  in `New`'s `newStores` set `loops: loop.NewStore(paths.Loops)`; in
  `configured` set `a.loops = s.loops`; add `a.newLoopCmd()` to the list
  of `peopleGroup` commands, after `a.newContinueCmd()`.
- `internal/cli/llm.go`, `guideTopics()`: after `"continue"`, add
  `"loop", "loop add", "loop list", "loop pause", "loop resume", "loop rm"`.
- `internal/cli/guides/ws.md`: add a bullet `` - `ws llm loop`: … `` and
  one per subcommand next to `ws llm continue`'s; add `loop_invalid` to
  the exit 2 row, `loop_not_found` to exit 3, `loop_exists` to exit 4 of
  "Output and exit codes".
- `internal/cli/app_test.go`: add the helper below. `withConfig` replaces
  `a.loops` from `a.newStores` on every run, so setting `app.loops`
  directly in a test does nothing.

  ```go
  // withLoopStore makes app's commands use a real loop store in a temp dir.
  func withLoopStore(t *testing.T, app *App) *loop.Store {
  	t.Helper()
  	store := loop.NewStore(t.TempDir())
  	prev := app.newStores
  	app.newStores = func(p config.Paths, c config.Config) stores {
  		s := prev(p, c)
  		s.loops = store
  		return s
  	}
  	return store
  }
  ```

**`loop.go`**

```go
func (a *App) newLoopCmd() *cobra.Command      // "loop", Short: "Start an agent in a new workspace every period"; subcommands below
func (a *App) newLoopAddCmd() *cobra.Command   // Args: cobra.MinimumNArgs(1): NAME then SPECs
func (a *App) newLoopListCmd() *cobra.Command  // Args: cobra.NoArgs
func (a *App) newLoopPauseCmd() *cobra.Command // Args: cobra.ExactArgs(1)
func (a *App) newLoopResumeCmd() *cobra.Command
func (a *App) newLoopRmCmd() *cobra.Command

type loopAddOptions struct {
	every, prompt, model, permissionMode string
	keep   int  // default loop.DefaultKeep
	paused bool
}

// loopJSON is a loop in ws loop's output (docs/tools/ws-loop.md).
type loopJSON struct {
	Name           string       `json:"name"`
	Every          string       `json:"every"`
	Prompt         string       `json:"prompt"`
	Specs          []string     `json:"specs"`
	Model          *string      `json:"model"`
	PermissionMode *string      `json:"permission_mode"`
	Keep           int          `json:"keep"`
	Paused         bool         `json:"paused"`
	CreatedAt      string       `json:"created_at"`
	NextRunAt      *string      `json:"next_run_at"`
	LastRun        *loopRunJSON `json:"last_run"`
}

type loopRunJSON struct {
	StartedAt string         `json:"started_at"`
	Workspace *string        `json:"workspace"`
	Session   *string        `json:"session"`
	Error     *itemErrorJSON `json:"error"`
}

// loopResult is add/pause/resume/rm's output: the loop's fields and warnings.
type loopResult struct {
	loopJSON
	Warnings []string `json:"warnings"`
}

func loopJSONOf(l *loop.Loop) loopJSON  // NextRunAt nil when paused; Specs never nil
func loopLine(l *loop.Loop) string       // the text line shown in docs/tools/ws-loop.md
```

Every subcommand's `RunE` is `runE(a.withConfig(...))`.

- **add**: build a `loop.Loop` with `Name: args[0]`, `Specs: args[1:]`,
  `Every: loop.FormatEvery(<parsed --every>)`, `CreatedAt: a.now().UTC()`,
  `NextRunAt: CreatedAt + interval`, `Paused: --paused`. Check specs with
  `validSpecs(cmd, args[1:])` (in `internal/cli/args.go`), then
  `l.Validate()`. `--every` and `--prompt` are required: if empty, return
  `usageErrorf("--every is required")` / `--prompt`. Then `Lock`,
  `Create`, unlock, print.
- **list**: `RLock`, `List`, unlock; add its warnings to `a.warned`;
  print `{"loops": [...], "warnings"}` with `a.writeJSON`, or one
  `loopLine` per loop.
- **pause**: `Lock`, `Get`, set `Paused = true`, `Save`. **resume**:
  `Lock`, `Get`, `Resume(a.now())`, `Save`. Already in that state: save
  nothing, print it anyway.
- **rm**: `Lock`, `Get`, `Remove`; print the loop as it was.
- Errors from the store go through `domainError(err)`.
- Text output for add/pause/resume/rm is the `loopLine`.

**Guides** (`internal/cli/guides/ws-loop-*.md`): copy the structure of
`ws-hide.md`. Each must start with `# ws loop <sub>\n`, contain a
synopsis line starting `ws loop <sub>`, mention every flag of the
command (e.g. `--every`), contain the sentence
"`ws loop <sub>` is for people and the desktop app. Agents don't run it",
and have `## JSON output` (with a ```json block), `## Exit codes`, and
`## Examples` sections. Exit codes may name only real codes.
`ws-loop.md` (topic `loop`) summarizes the subcommands and needs
`## Exit codes` and `## Examples` too.

**Acceptance** (in `loop_test.go`; build the app with `newTestApp()`,
then `withLoopStore(t, app)`, and run commands with
`app.Run(ctx, []string{"loop", ...})`, reading the stdout/stderr buffers)

- `loop add deps --every 24h --prompt hi buff-bear --json` exits 0 and
  prints `name "deps"`, `every "24h"`, `specs ["buff-bear"]`, `model
  null`, `keep 5`, `paused false`, `next_run_at` = `testNow()` + 24h,
  `last_run null`, `warnings []`.
- `--every 2m` → exit 2, stderr JSON `"code":"loop_invalid"`; missing
  `--prompt` → exit 2 `usage`; adding `deps` twice → exit 4 `loop_exists`.
- `loop list --json` with two loops → `{"loops":[a, b]}` sorted by name.
- `loop pause deps --json` → `paused true`, `next_run_at null`;
  `loop resume deps` afterwards → `paused false`.
- `loop rm nope --json` → exit 3 `loop_not_found`; `loop rm deps` then
  `loop list` → empty.
- `go test ./internal/cli/ -run TestGuide` passes (guide coverage).

**Verify**

```console
$ go test ./internal/cli/... ./internal/loop/...
$ make test
$ make lint
$ make build && bin/ws loop add demo --every 15m --prompt "say hi" --config-dir "$(mktemp -d)" --json
```

---

## T3. `ws loop tick` and `ws loop run`: the scheduler

**Depends on:** T2.

**Create**

- `internal/cli/loop_tick.go`
- `internal/cli/loop_tick_test.go`
- `internal/cli/guides/ws-loop-run.md`, `internal/cli/guides/ws-loop-tick.md`

**Modify**

- `internal/cli/interfaces.go`: add

  ```go
  // awakeReader reports whether the Mac is fully awake (ADR 0047).
  type awakeReader interface {
  	Awake(ctx context.Context) (bool, error)
  }
  ```

- `internal/cli/app.go`: add `awake awakeReader` (set to `power.Reader{}`
  in `New`) and `loopPoll time.Duration` (set to `defaultLoopPoll`, a
  `time.Minute` const in `loop_tick.go`); register `a.newLoopRunCmd()` and
  `a.newLoopTickCmd()` in `newLoopCmd`.
- `internal/cli/llm.go`: add `"loop run", "loop tick"` after `"loop rm"`.
- `internal/cli/guides/ws.md`: bullets for `ws llm loop run` and
  `ws llm loop tick`; add `ws loop tick` to the list of commands that act
  on several items.
- `README.md`: tick the `ws loop` roadmap box (`- [ ]` → `- [x]`) and
  add `  loop/                       # agent loops: records, schedule, store (0048)`
  to the package tree, after `lifetime/`.

**`loop_tick.go`**

```go
type loopAction string

const (
	loopStarted    loopAction = "started"
	loopWouldStart loopAction = "would_start"
	loopWaiting    loopAction = "waiting"
	loopPaused     loopAction = "paused"
	loopBusy       loopAction = "busy"
	loopAsleep     loopAction = "asleep"
	loopFailed     loopAction = "failed"
)

// tickJSON is one loop in ws loop tick's output.
type tickJSON struct {
	Name      string         `json:"name"`
	Action    loopAction     `json:"action"`
	Workspace *string        `json:"workspace"`
	Session   *string        `json:"session"`
	NextRunAt *string        `json:"next_run_at"`
	Error     *itemErrorJSON `json:"error"`
}

// loopRunResult is ws loop run's output.
type loopRunResult struct {
	Name      string   `json:"name"`
	Workspace string   `json:"workspace"`
	Session   string   `json:"session"`
	Path      string   `json:"path"`
	Warnings  []string `json:"warnings"`
}

func (a *App) newLoopTickCmd() *cobra.Command // flags --dry-run, --watch; Args: cobra.NoArgs
func (a *App) newLoopRunCmd() *cobra.Command  // Args: cobra.ExactArgs(1)

// tick runs one pass (ADR 0048, decision 4) and returns one item per loop.
func (a *App) tick(ctx context.Context, dryRun bool) ([]tickJSON, *itemResults, error)

// startLoopRun creates the run's workspace and starts its agent, as ws
// claude does, and returns the run to record. A failure is in the Run's
// ErrorCode/ErrorMessage and also returned.
func (a *App) startLoopRun(ctx context.Context, l *loop.Loop) (loop.Run, workspace.Workspace, error)

// loopClaudeArgs: ["--model", M]? ["--permission-mode", P]? prompt
func loopClaudeArgs(l *loop.Loop) []string

// pruneRuns removes old runs' workspaces beyond l.Keep (decision 5).
func (a *App) pruneRuns(ctx context.Context, l *loop.Loop, listing lifetime.Listing)
```

**`startLoopRun`**, reusing `internal/cli/claude.go`:

1. `ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: "loop-" + l.Name, Specs: parseSpecs(l.Specs)})`
   (no `Parent`, not hidden). On error return it (via `domainError`).
2. `defer lock.Unlock()`; `started, err := a.startAgent(ctx, &ws, loopClaudeArgs(l))`.
   `startAgent` already removes the workspace on failure.
3. Return `loop.Run{StartedAt: a.now().UTC(), Workspace: ws.ID, Session: started.ID}`.
   On any error, return a Run with only `StartedAt`, `ErrorCode =
   string(asCLIError(domainError(err)).code)`, and `ErrorMessage = err.Error()`.

**`tick`**, in this order:

1. `unlock, err := a.loops.Lock(ctx)` (`RLock` when `dryRun`); `defer unlock()`.
2. `List()`; its warnings go to `a.warned`.
3. Find the due loops (`l.Due(a.now())`). If none, every item is
   `paused` or `waiting`; return without listing sessions.
4. Otherwise: `awake, err := a.awake.Awake(ctx)`; an error is a warning
   and counts as not awake. `listing, err := a.lifetime.List(ctx)`; on
   error return it (nothing starts; exit with its code). Call
   `a.claude.Check(ctx)` once before the first start; on error every due
   loop is `failed` with that error.
5. For each loop, in name order:
   - paused → `paused`; not due → `waiting`;
   - not awake → `asleep`;
   - its `LastRun()` workspace is in `listing.Workspaces` with
     `Session != nil` and `State` `lifetime.StateWorking` or
     `lifetime.StateStarting` → `busy`;
   - `dryRun` → `would_start`;
   - else `run, ws, err := a.startLoopRun(ctx, &l)`; `l.AddRun(run)`;
     `l.Advance(a.now())`; `a.loops.Save(&l)`; action `started`, or
     `failed` with the item error; after a start, `a.pruneRuns(ctx, &l, listing)`.
   - `next_run_at` in the item is the loop's value after this step
     (`null` when paused).
6. Text line per item: `<action>  <name>  <workspace or ->  next <time or ->`.

**`pruneRuns`**: walk `l.Runs` newest first, skipping runs with no
`Workspace`; count those whose workspace is in the listing; once the
count passes `l.Keep`, call `a.lifetime.RemoveTree(ctx, &entry.Workspace,
lifetime.TreeOptions{})` for each such run whose session isn't working
or starting. A failure is appended to `a.warned`, never returned.

**`run`**: `Lock`, `Get` (→ `loop_not_found`), `a.claude.Check`,
`startLoopRun`, `AddRun`, `Save` (don't call `Advance`), print
`loopRunResult` or `name  workspace  session  path`. A failed start is
saved in the record and returned as the command's error.

**`--watch`**: refuse `--json` with
`usageErrorf("--watch prints text only; run ws loop tick --json without --watch")`,
then loop like `watchContinue` in `continue.go`, every `a.loopPoll`,
printing only `started` and `failed` lines.

**Acceptance** (`loop_tick_test.go`; build the app with the mocks
below, then call `withLoopStore(t, app)` last, and set a test `awakeReader`
returning a fixed value, and the moq mocks already used in
`claude_test.go` for `workspaces` (`CreateFunc`, `RecordSessionFunc`),
`claude` (`CheckFunc`, `StartFunc`), and `lifetime` (`ListFunc`,
`RemoveTreeFunc`); `newClaudeApp`-style helpers in `claude_test.go` show
how)

- A loop with `NextRunAt` in the past: `loop tick --json` → action
  `started`, workspace and session from the mocks, `next_run_at` =
  old + every; the record now has 1 run. `StartFunc` got args ending in
  the prompt, with `--model` when set; `CreateFunc` got `Name "loop-<name>"`
  and `Parent ""`.
- Not due → `waiting`, `ListFunc` and `StartFunc` never called.
- Paused → `paused`, `next_run_at null`.
- Awake false → `asleep`, nothing started, record unchanged.
- Last run's workspace `working` in the listing → `busy`.
- `--dry-run` → `would_start`, record unchanged, `StartFunc` not called.
- `StartFunc` fails → `failed`, item `error.code "claude_failed"`, exit 1,
  record has a run with `error_code`, `next_run_at` advanced.
- `keep = 1` and two earlier idle runs in the listing → `RemoveTreeFunc`
  called for both older runs, not for the new one.
- Two `tick`s in a row with one due loop start exactly one run.
- `loop run deps --json` on a paused loop starts a run and leaves
  `paused` and `next_run_at` unchanged.
- `loop tick --watch --json` → exit 2 `usage`.

**Verify**

```console
$ go test ./internal/cli/... ./internal/loop/...
$ make test
$ make lint
$ make build && bin/ws loop tick --dry-run --json --config-dir "$(mktemp -d)"
```

---

## T4. Desktop backend: Tauri commands for loops and a tick every minute

**Depends on:** T3 (needs `ws loop … --json`).

**Create**

- `desktop/src-tauri/src/loops.rs`

**Modify**

- `desktop/src-tauri/src/lib.rs`: `mod loops;`; in `setup`, after the
  review checker is spawned, `tauri::async_runtime::spawn(loops::tick_forever(state.runner.clone()));`
  (before `app.manage(state)`); add `loops::loops_list`, `loops::loops_add`,
  `loops::loops_set_paused`, `loops::loops_remove`, `loops::loops_run_now`
  to `generate_handler!`.

**`loops.rs`**

```rust
//! Agent loops (ADR 0048): `ws loop … --json`, and a `ws loop tick` every
//! minute while the app is open.

use crate::commands::AppState;
use crate::runner::{AppError, Runner};
use serde::{Deserialize, Serialize};
use std::time::Duration;
use tauri::State;

type CmdResult<T> = Result<T, AppError>;

/// How often the app runs `ws loop tick`.
pub const TICK_INTERVAL: Duration = Duration::from_secs(60);

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopError { pub code: String, pub message: String }

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct LoopRun {
    pub started_at: String,
    pub workspace: Option<String>,
    pub session: Option<String>,
    pub error: Option<LoopError>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]
pub struct Loop {
    pub name: String,
    pub every: String,
    pub prompt: String,
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    pub keep: u32,
    pub paused: bool,
    pub created_at: String,
    pub next_run_at: Option<String>,
    pub last_run: Option<LoopRun>,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct Loops { pub loops: Vec<Loop>, pub warnings: Vec<String> }

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct LoopStarted { pub name: String, pub workspace: String, pub session: String, pub path: String }

/// What the Loops form sends.
#[derive(Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct NewLoop {
    pub name: String,
    pub every: String,
    pub prompt: String,
    #[serde(default)]
    pub specs: Vec<String>,
    pub model: Option<String>,
    pub permission_mode: Option<String>,
    pub keep: Option<u32>,
}

/// `ws loop add` without `--json`, which the runner adds:
/// ["loop", "add", "--every=<every>", "--prompt=<prompt>", ("--keep=<n>"),
///  ("--model=<m>"), ("--permission-mode=<p>"), "--", <name>, <specs...>].
/// Blank optional values, and blank specs, are left out; values are trimmed.
pub fn add_args(req: &NewLoop) -> Vec<String>;

/// ["loop", "pause" | "resume", "--", <name>]
pub fn pause_args(name: &str, paused: bool) -> Vec<String>;

#[tauri::command] pub async fn loops_list(state: State<'_, AppState>) -> CmdResult<Loops>;              // ["loop", "list"]
#[tauri::command] pub async fn loops_add(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop>;
#[tauri::command] pub async fn loops_set_paused(state: State<'_, AppState>, name: String, paused: bool) -> CmdResult<Loop>;
#[tauri::command] pub async fn loops_remove(state: State<'_, AppState>, name: String) -> CmdResult<Loop>; // ["loop", "rm", "--", name]
#[tauri::command] pub async fn loops_run_now(state: State<'_, AppState>, name: String) -> CmdResult<LoopStarted>; // ["loop", "run", "--", name]

/// Runs `ws loop tick --quiet --json` now and then every TICK_INTERVAL,
/// forever, ignoring its result (failures are logged with eprintln!).
/// Use tokio::time::interval with MissedTickBehavior::Skip.
pub async fn tick_forever(runner: Runner);
```

Every command calls `state.runner.ws_json(&args)` (it inserts `--json`
after the first argument, so `["loop", "list"]` runs `ws loop --json list`).

**Acceptance** (`#[cfg(test)] mod tests` in `loops.rs`; copy the
`fake_programs` helper from the tests in `commands.rs`)

- `add_args` for `{name: "deps", every: "24h", prompt: "hi", specs: ["buff-bear", " "], model: Some(""), permission_mode: Some("acceptEdits"), keep: Some(3)}`
  is `["loop","add","--every=24h","--prompt=hi","--keep=3","--permission-mode=acceptEdits","--","deps","buff-bear"]`.
- `pause_args("deps", true)` is `["loop","pause","--","deps"]`; `false` gives `resume`.
- With a fake `ws` that echoes the `ws loop list --json` example from
  `docs/tools/ws-loop.md`, a `Runner` on that PATH parses it into
  `Loops`, and `serde_json::to_value` of the first loop has the keys
  `nextRunAt`, `permissionMode`, and `lastRun.startedAt`.
- A fake `ws` that prints `{"error":{"code":"loop_exists","message":"x"}}`
  on stderr and exits 4 gives `AppError { code: "loop_exists", .. }`.

**Verify**

```console
$ cd desktop/src-tauri && cargo test loops && cargo clippy --all-targets -- -D warnings
$ make desktop-test
```

---

## T5. Desktop UI: the Loops section

**Depends on:** T4.

**Create**

- `desktop/src/features/loops/loopsSlice.ts`
- `desktop/src/features/loops/loopsSlice.test.ts`
- `desktop/src/features/loops/LoopsActions.tsx`
- `desktop/src/features/loops/LoopsList.tsx`
- `desktop/src/features/loops/LoopsList.module.css`
- `desktop/src/features/loops/LoopsPane.tsx`
- `desktop/src/features/loops/LoopsPane.module.css`
- `desktop/src/features/loops/LoopForm.tsx`
- `desktop/src/features/loops/LoopsPane.test.tsx`

**Modify**

- `desktop/src/api.ts`: add the types and five calls below.
- `desktop/src/test/fixtures.ts`: add a `loop(overrides)` fixture and the
  five calls to `fakeApi` (`loopsList: vi.fn(async () => ({ loops: [], warnings: [] }))`, etc.).
- `desktop/src/store.ts`: `loops: loopsReducer` in `rootReducer`.
- `desktop/src/features/ui/uiSlice.ts`: `Section` gains `"loops"`.
- `desktop/src/features/rail/Rail.tsx`: an icon for `loops` (a circular
  arrow: `<path d="M20 12a8 8 0 1 1-2.3-5.6" /><path d="M20 4v4h-4" />`),
  label `"Loops"`; `railOrder` and `shownSections` become
  `["agents", "repos", "reviews", "loops", "settings"]`; update the
  ⌘1–⌘4 comments to ⌘1–⌘5.
- `desktop/src/features/rail/Rail.test.tsx`: labels now
  `["Agents", "Repos", "Reviews", "Loops", "Settings"]`; Settings is ⌘5;
  add "chooses Loops on ⌘4".
- `desktop/src/App.tsx`: `loops: { actions: LoopsActions, list: LoopsList, pane: LoopsPane }`
  in `sectionViews`; in `Warnings`, `case "loops": return s.loops.warnings;`.

**`api.ts`**

```ts
export interface LoopError { readonly code: string; readonly message: string }

export interface LoopRun {
  readonly startedAt: string;
  readonly workspace: string | null;
  readonly session: string | null;
  readonly error: LoopError | null;
}

export interface Loop {
  readonly name: string;
  readonly every: string;
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly model: string | null;
  readonly permissionMode: string | null;
  readonly keep: number;
  readonly paused: boolean;
  readonly createdAt: string;
  readonly nextRunAt: string | null;
  readonly lastRun: LoopRun | null;
}

export interface LoopListing { readonly loops: readonly Loop[]; readonly warnings: readonly string[] }

export interface NewLoop {
  readonly name: string;
  readonly every: string;
  readonly prompt: string;
  readonly specs: readonly string[];
}

export interface LoopStarted { readonly name: string; readonly workspace: string; readonly session: string; readonly path: string }

// in `api`:
loopsList: async (): Promise<LoopListing> => call<LoopListing>("loops_list"),
loopsAdd: async (request: NewLoop): Promise<Loop> => call<Loop>("loops_add", { request }),
loopsSetPaused: async (name: string, paused: boolean): Promise<Loop> => call<Loop>("loops_set_paused", { name, paused }),
loopsRemove: async (name: string): Promise<Loop> => call<Loop>("loops_remove", { name }),
loopsRunNow: async (name: string): Promise<LoopStarted> => call<LoopStarted>("loops_run_now", { name }),
```

**`loopsSlice.ts`** (follow `features/settings/settingsSlice.ts`)

```ts
export interface LoopsState {
  readonly loops: readonly Loop[];
  readonly warnings: readonly string[];
  readonly error: AppError | null;          // listing failed
  readonly selected: string | null;         // a loop's name
  readonly creating: boolean;               // the form is shown in the pane
  readonly formError: AppError | null;      // the last add failed
  readonly busy: string | null;             // name of the loop an action is running on
}

export const fetchLoops = createAppThunk<LoopListing, void>("loops/fetch", …);
export const addLoop = createAppThunk<Loop, NewLoop>("loops/add", …);          // then dispatch(fetchLoops())
export const setLoopPaused = createAppThunk<Loop, { name: string; paused: boolean }>("loops/setPaused", …);
export const removeLoop = createAppThunk<Loop, string>("loops/remove", …);      // then dispatch(fetchLoops())
export const runLoopNow = createAppThunk<LoopStarted, string>("loops/runNow", …); // then dispatch(fetchLoops())

// reducers: loopSelected(name | null), creatingStarted(), creatingCancelled()
```

- `fetchLoops.fulfilled` sets `loops`, `warnings`, clears `error`, and
  clears `selected` if that loop is gone.
- `addLoop.fulfilled`: `creating = false`, `formError = null`,
  `selected = <new name>`. `addLoop.rejected`: `formError = payload`.
- `setLoopPaused.fulfilled` replaces that loop in `loops`.
- `removeLoop.fulfilled`: `selected = null`.
- `runLoopNow.fulfilled` shows a notice with
  `noticeShown({ severity: "success", message: "Started <workspace>", action: { label: "Open", agent: <workspace> } })`
  from the thunk (`dispatch`), as the reviews thunks do.
- Any action's rejection shows `noticeShown({ severity: "error", message })`,
  except `addLoop`, whose error shows in the form.
- `pending`/`fulfilled`/`rejected` of the per-loop actions set and clear `busy`.

**Components**

- `LoopsActions`: one MUI `Button` "New loop" → `creatingStarted()`.
- `LoopsList`: on mount, and every 10 s while mounted, `fetchLoops()`.
  One row per loop (a `button`, `aria-current` when selected): the name,
  `every <every>`, "Paused" when paused, and "next <age-style time>" via
  `age(nextRunAt)` from `format.ts`, or "—". Empty: "No loops yet."
- `LoopsPane`:
  - `creating` → `<LoopForm />`;
  - a selected loop → a header with its name and buttons **Pause** /
    **Resume** (by `paused`), **Run now**, **Delete** (opens an MUI
    `Dialog` "Delete loop <name>? Its runs' workspaces stay."; confirm →
    `removeLoop`); then rows: Every, Prompt (pre-wrapped), Repos (or
    "none"), Keep, Next run (`nextRunAt` or "paused"), Last run
    (`startedAt`, workspace as a link that dispatches
    `sectionChosen("agents")` and `selected(workspace)`, or its error
    message), all disabled while `busy === name`;
  - else "Select a loop, or add one."
- `LoopForm`: MUI `TextField`s **Name** (required), **Every**
  (required, placeholder `15m, 1h, 24h`, helper "Whole minutes, 5m to
  168h"), **Prompt** (required, multiline, 4 rows), **Repos** (optional,
  helper "Repo specs, separated by spaces"); buttons **Cancel**
  (`creatingCancelled()`) and **Add loop** (disabled until the three
  required fields are non-blank). Submit → `addLoop({ name, every,
  prompt, specs: repos.split(/\s+/).filter(Boolean) })`. Show
  `formError.message` in an `Alert severity="error"` above the buttons.
  Validation is `ws`'s; don't re-implement it.

**Acceptance** (vitest + Testing Library; build stores with
`createStore({ api: fakeApi({...}), terminals: … })` and render with
`renderWithStore`, as `ReviewsPane.test.tsx` does)

- `loopsSlice.test.ts`: `fetchLoops` fills `loops`; a selected loop
  that's gone is unselected; `addLoop` rejected with
  `{code: "loop_invalid", message: "period \"2m\" is under 5m"}` sets
  `formError` and keeps `creating`.
- `LoopsPane.test.tsx`:
  - filling Name, Every, Prompt, Repos `buff-bear  api` and clicking
    **Add loop** calls `loopsAdd` with
    `{name, every, prompt, specs: ["buff-bear", "api"]}` and then shows the
    new loop selected;
  - the add error message appears in the form;
  - a selected active loop's **Pause** calls `loopsSetPaused("deps", true)`
    and the button becomes **Resume**;
  - **Run now** calls `loopsRunNow("deps")`;
  - **Delete** then confirming calls `loopsRemove("deps")`.
- `Rail.test.tsx` passes with the five sections.
- `npm run typecheck`, `npm test`, and `npm run build` pass.

**Verify**

```console
$ cd desktop && npm ci && npm run typecheck && npm test && npm run build
$ make desktop-test
```
