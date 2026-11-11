# Loop history and memory: implementation tasks

Implements [ADR 0056](../../adrs/0056-loop-history-and-memory.md) to the
contract in [reference.md](reference.md). Each task builds, passes its
tests, and can be merged on its own. Each task is one commit on its own
branch (conventional commit, imperative mood, for example
`feat: add loop history and memory stores (ADR 0056)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | `internal/loop`: history, memory and run-mark stores; `memory` in prompts | nothing | large | T4, T6 |
| T2 | CLI: record and settle history in tick/run; `ws loop history`; gate env | T1 | large | T4, T5, T6 |
| T3 | CLI: `ws loop memory` and `ws machine memory` | T2 | large | T4, T5, T6 |
| T4 | Desktop bridge: Tauri/online commands, API client, slice, Tauri mock with screenshot data | nothing (contract in reference.md) | large | T1, T2, T3, T6 |
| T5 | Desktop UI: History and Memory cards in the loop view | T4 | large | T1, T2, T3, T6 |
| T6 | Docs: tools pages, state.md, ADR index, READMEs | nothing (contract in reference.md) | medium | everything |

T1 → T2 → T3 is the Go chain (T2 and T3 both edit `loop.go`, `llm.go`
and `errors`). T4 → T5 is the desktop chain. The chains touch disjoint
files (`internal/` vs `desktop/`), so T4 ∥ T1, T4 ∥ T2, T5 ∥ T2, T5 ∥ T3.
T6 touches only `docs/`, `README.md` and `desktop/README.md`, so T6 ∥
every task. No task is `small` (every one touches more than two files).

**Read before any task:** the "Decision" section of ADR 0056 and all of
`reference.md`. When this file and those two disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style (a short doc comment on every exported or `pub` item, no other
  comments unless the logic isn't obvious), file layout, test style.
  Prefer early returns. Keep functions small.
- **Go** (`internal/`): wrap errors with `fmt.Errorf("doing x: %w", err)`,
  lower-case, no "failed to". Tests: testify `require` for
  preconditions, `assert` for checks; one `TestX` per function or method
  (`TestDataStore_ReadHistory`, `TestEntry_Settle`) with `t.Run`
  subtests per behaviour; table-driven only for 3+ trivial cases; use
  `t.Context()` and `t.TempDir()`; `t.Parallel()` only on tests that
  touch the filesystem. Run `make fmt`; `make lint` must pass. Never call
  `exec.Command`. Keep `internal/loop` free of `internal/cli` imports.
- **JSON** output: exactly the shapes in `reference.md`. Don't change
  any existing field, error code or exit code.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean, `cargo clippy
  --no-default-features --features online --all-targets -- -D warnings`
  clean; unit tests in the file's `#[cfg(test)] mod tests`.
- **TypeScript** (`desktop/src/`): strict, `readonly` fields, no `any`;
  components are `const X = (props): JSX.Element =>`; styles in a CSS
  module. Tests: vitest, `@testing-library/react`, `user-event`, helpers
  in `desktop/src/test/fixtures.ts` and `desktop/src/test/render.tsx`.
  Query by role and accessible name. **No new npm or cargo dependencies.**
- **Phone width:** every new button, row action and link is at least
  40px tall (`min-height: 40px`) under `@media (max-width: 720px)`, as
  `SettingsPane.module.css` does.
- Don't touch files a task doesn't list, except to fix a compile error
  the task caused (say so in your report).

## T1. `internal/loop`: data stores and the `memory` prompt function

**Depends on:** nothing. **Parallel:** T1 ∥ T4 ∥ T6.

**Files:** new `internal/loop/data.go`, `internal/loop/data_test.go`,
`internal/loop/history.go`, `internal/loop/history_test.go`,
`internal/loop/memory.go`, `internal/loop/memory_test.go`; edit
`internal/loop/prompt.go`, `internal/loop/prompt_test.go`,
`internal/loop/loop.go` (only: add `json` tags to `Gate`).

Read first: `internal/loop/loop.go`, `store.go` (atomic write, version
checks), `prompt.go`, `internal/lockfile/lockfile.go`,
`internal/machine/store.go` (`writeAtomic`, to copy its pattern).

### `data.go`

```go
// DataStore keeps what ws records about loops besides their records:
// <stateDir>/.loops/<name>/ (history, reports, memory) and
// <stateDir>/<workspace>/loop.toml (the run mark), ADR 0056.
type DataStore struct{ stateDir string }

func NewDataStore(stateDir string) *DataStore
func (d *DataStore) StateDir() string
func (d *DataStore) Dir(name string) string            // <stateDir>/.loops/<name>
func (d *DataStore) HistoryPath(name string) string    // Dir/history.json
func (d *DataStore) MemoryPath(name string) string     // Dir/memory.json
func (d *DataStore) ReportPath(name string, seq int) string // Dir/reports/<seq>.md
// RemoveAll deletes Dir(name); a missing dir is fine. ValidateName first.
func (d *DataStore) RemoveAll(name string) error

// RunMark is <stateDir>/<workspace>/loop.toml: which loop started the run.
type RunMark struct {
	Version   int       `toml:"version"`
	Loop      string    `toml:"loop"`
	StartedAt time.Time `toml:"started_at"`
}
const RunMarkVersion = 1
// WriteRunMark writes the mark atomically, with Version = RunMarkVersion.
// The workspace's state dir must exist (it does once the workspace is created).
func (d *DataStore) WriteRunMark(workspaceID string, m RunMark) error
// ReadRunMark: found=false when the file is missing; version > 1 is ErrNewerData.
func (d *DataStore) ReadRunMark(workspaceID string) (m RunMark, found bool, err error)

// ErrNewerData is a history, memory or run-mark file from a newer ws.
var ErrNewerData = errors.New("loop data written by a newer ws; upgrade ws")
```

Add an unexported `writeFileAtomic(path string, data []byte) error`
(temp file in the same dir, `0o600`, rename; creates the dir `0o700`).
`workspaceID` must be one path element (reject `""`, `.`, `..`, `/`,
leading `.`) with `ErrInvalid`.

### `history.go`

```go
type RunStatus string
const (
	StatusRunning     RunStatus = "running"
	StatusDone        RunStatus = "done"
	StatusSucceeded   RunStatus = "succeeded"
	StatusFailed      RunStatus = "failed"
	StatusStartFailed RunStatus = "start_failed"
	StatusSkipped     RunStatus = "skipped"
	StatusGone        RunStatus = "gone"
)
type Trigger string
const (
	TriggerTick     Trigger = "tick"
	TriggerRun      Trigger = "run"
	TriggerForce    Trigger = "run_force"
	TriggerImported Trigger = "imported"
)
const (
	HistoryVersion = 1
	MaxHistory     = 200
	MaxReports     = 50
	MaxReportSize  = 1 << 20
)
type Verdict struct {
	Verdict    string    `json:"verdict"`
	Outcome    string    `json:"outcome"` // "pass" or "fail"
	RecordedAt time.Time `json:"recorded_at"`
	Report     bool      `json:"report"` // reports/<seq>.md was kept
}
type Entry struct { /* exactly the on-disk Entry in reference.md:
	Seq int; Status RunStatus; Trigger Trigger; StartedAt time.Time;
	FinishedAt *time.Time; Workspace, Session string; Closed bool;
	ErrorCode, ErrorMessage string; Gate *Gate; Repeats int; Verdict *Verdict */ }
type History struct {
	Version int     `json:"version"`
	NextSeq int     `json:"next_seq"`
	Entries []Entry `json:"entries"` // oldest first
}
// Observation is what a tick saw of an open run.
type Observation struct {
	Listed  bool     // the run's workspace is in the listing
	State   string   // its session's lifetime state, "" without a session
	Verdict *Verdict // from its result.toml, nil without one (Report is ignored)
}

// ReadHistory reads HistoryPath(name); found=false and an empty History
// (Version 1, NextSeq 1, Entries non-nil) when missing. Version >
// HistoryVersion is ErrNewerData; bad JSON is wrapped with the path.
func (d *DataStore) ReadHistory(name string) (h History, found bool, err error)
// WriteHistory sets Version, then calls h.trimReports and deletes each
// returned seq's report (missing is fine), then writes atomically
// (json.MarshalIndent, two spaces, trailing newline).
func (d *DataStore) WriteHistory(name string, h *History) error
// WriteReport writes reports/<seq>.md atomically; over MaxReportSize is ErrInvalid.
func (d *DataStore) WriteReport(name string, seq int, report []byte) error
// ReadReport: found=false when missing.
func (d *DataStore) ReadReport(name string, seq int) (report []byte, found bool, err error)

// Append adds e and returns a pointer to the stored entry. If e is
// skipped and the newest entry is skipped with the same Gate.ExitCode and
// Gate.Message, that entry's Repeats++ and FinishedAt = e.StartedAt, and
// no seq is used. Otherwise e.Seq = h.NextSeq, h.NextSeq++, Repeats = 1
// when 0, appended; then the oldest entries beyond MaxHistory are dropped.
func (h *History) Append(e Entry) *Entry
// Find is the entry with seq, or nil.
func (h *History) Find(seq int) *Entry
// FindWorkspace is the newest entry whose Workspace is id, or nil.
func (h *History) FindWorkspace(id string) *Entry
// trimReports: walking newest to oldest, the first MaxReports entries
// with Verdict.Report keep it; every later one gets Report=false and its
// seq is returned. Seqs of entries Append dropped are not tracked: the
// caller (WriteHistory) also deletes report files whose seq is no longer
// in h.Entries (list the reports dir; ignore names that aren't <int>.md).
func (h *History) trimReports() []int
// Open reports whether e still has a workspace to check: Workspace != "" && !Closed.
func (e *Entry) Open() bool
// Settle applies obs at now by the "Status rules" in reference.md and
// reports whether e changed.
func (e *Entry) Settle(obs Observation, now time.Time) bool
// SeedHistory is the history of a loop that has none yet: one entry per
// run, oldest first, Trigger imported; a run with ErrorCode is
// start_failed with FinishedAt = StartedAt, the rest running.
func SeedHistory(runs []Run) History
```

### `memory.go`

```go
const (
	MemoryVersion  = 1
	MaxMemoryKeys  = 100
	MaxMemoryValue = 64 << 10
	MaxMemoryBytes = 256 << 10
)
var (
	ErrMemoryInvalid  = errors.New("invalid memory key")
	ErrMemoryNotFound = errors.New("memory key not found")
	ErrMemoryConflict = errors.New("memory key changed")
	ErrMemoryFull     = errors.New("loop memory is full")
)
// AnyRev is the expectRev that skips the revision check.
const AnyRev = -1
type MemoryEntry struct {
	Value     string    `json:"value"`
	Rev       int       `json:"rev"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}
type Memory struct {
	Version int                    `json:"version"`
	Entries map[string]MemoryEntry `json:"entries"` // never nil after Read
}
// ValidateMemoryKey is ValidateVarKey's rule, wrapping ErrMemoryInvalid.
func ValidateMemoryKey(key string) error
// ReadMemory reads MemoryPath(name) without locking; a missing file is
// an empty Memory. Version > MemoryVersion is ErrNewerData.
func (d *DataStore) ReadMemory(name string) (Memory, error)
// SetMemory, under the memory lock: validates key; value over
// MaxMemoryValue, a new key past MaxMemoryKeys, or a total past
// MaxMemoryBytes is ErrMemoryFull; expectRev != AnyRev and != the
// current rev (0 when missing) is ErrMemoryConflict; then Rev = old+1,
// UpdatedAt = now.UTC().Truncate(time.Second), UpdatedBy = by; writes.
func (d *DataStore) SetMemory(ctx context.Context, name, key, value, by string, expectRev int, now time.Time) (MemoryEntry, error)
// RemoveMemory, under the lock: missing key is ErrMemoryNotFound;
// expectRev as in SetMemory.
func (d *DataStore) RemoveMemory(ctx context.Context, name, key string, expectRev int) error
// ClearMemory, under the lock, removes every key and returns how many.
func (d *DataStore) ClearMemory(ctx context.Context, name string) (int, error)
// Values is key → value, never nil.
func (m *Memory) Values() map[string]string
// Bytes is the sum of len(value).
func (m *Memory) Bytes() int
// Keys is the keys, sorted.
func (m *Memory) Keys() []string
```

The lock is `lockfile.Exclusive(ctx, Dir(name)+"/.memory.lock")` after
`os.MkdirAll(Dir(name), 0o700)`; `ValidateName(name)` first in every
method. The ErrMemory* errors wrap the key: `fmt.Errorf("%w: %s", ErrMemoryNotFound, key)`.

### `prompt.go`

`PromptData` gains `Memory map[string]string`. `render` parses with
`.Funcs(template.FuncMap{"memory": func(key string) string { return data.Memory[key] }})`
before `Parse`, for every templated prompt (a nil map is fine). Update the
`RenderPrompt` doc comment to mention `memory`.

### `loop.go`

Only add `json:"at"`, `json:"outcome"`, `json:"exit_code"`, `json:"message"`
to `Gate` beside the toml tags.

### Acceptance

- `TestEntry_Settle` covers every rule 1-7 in reference.md, including a
  re-recorded verdict (new `RecordedAt`) flipping `succeeded`→`failed`.
- `TestHistory_Append`: seqs 1,2,3; skipped coalescing (same code and
  message → one entry, `Repeats` 2, `FinishedAt` the second time;
  different message → new entry); `MaxHistory+1` appends keep 200 with
  the oldest dropped.
- `TestDataStore_WriteHistory`: round-trips through `ReadHistory`;
  report of the 51st-newest reported entry is deleted and its
  `Verdict.Report` false; a report whose seq was dropped is deleted;
  version 2 on disk → `ErrNewerData`.
- `TestDataStore_SetMemory`: rev increments; `expectRev` 0 on a new key
  works and on an existing key is `ErrMemoryConflict`; value of 65537
  bytes, the 101st key, and a total past 256 KiB are `ErrMemoryFull`; a
  bad key is `ErrMemoryInvalid`; two goroutines setting different keys
  20 times each end with both keys at rev 20.
- `TestDataStore_RemoveMemory`, `TestDataStore_ClearMemory`,
  `TestDataStore_WriteRunMark`/`ReadRunMark`, `TestSeedHistory`,
  `TestLoop_RenderPrompt` (`{{memory "seen"}}` with and without the
  key; `CheckPrompt` passes for a prompt using `memory`).

### Verify

```sh
go test ./internal/loop/... && make lint
```

## T2. CLI: record and settle history; `ws loop history`; gate env

**Depends on:** T1. **Parallel:** T2 ∥ T4, T5, T6.

**Files:** edit `internal/cli/app.go`, `internal/cli/app_test.go`
(helper only), `internal/cli/loop_tick.go`, `internal/cli/loop_tick_test.go`,
`internal/cli/loop_gate.go`, `internal/cli/loop_gate_test.go`,
`internal/cli/loop.go`, `internal/cli/loop_test.go`,
`internal/cli/cache.go` (error map only), `internal/cli/llm.go`
(topic list only), `internal/cli/guides/ws-loop.md`; new
`internal/cli/loop_history.go`, `internal/cli/loop_history_test.go`,
`internal/cli/guides/ws-loop-history.md`.

Read first: `internal/cli/loop_tick.go`, `loop_gate.go`, `loop.go`,
`machine_result.go` (`resultInfo`), `internal/machine/store.go`
(`Store.Read`, `Record.Outcome`), `internal/cli/loop_tick_test.go`
(`newTickEnv`), `internal/cli/app_test.go` (`withLoopStore`).

### Wiring

- `stores` and `App` get `loopData *loop.DataStore`; `newStores` sets
  `loopData: loop.NewDataStore(paths.State)`, and the `stores`
  assignment in `app.go` copies it. Add test helper
  `withLoopData(t *testing.T, app *App) *loop.DataStore` in
  `app_test.go` (like `withLoopStore`, over `t.TempDir()`); call it in
  `newTickEnv` and store it as `e.data`.
- `cache.go`'s error map: `{loop.ErrNewerData, codeLoopInvalid}`.

### Recording (`loop_tick.go`)

```go
// historyEntry is the entry for an attempt: skipped (Gate = l.LastGate),
// start_failed (ErrorCode/Message from run, FinishedAt = StartedAt), or
// running (Workspace, Session from run).
func historyEntry(l *loop.Loop, run *loop.Run, startErr error, trigger loop.Trigger, now time.Time) loop.Entry
// recordAttempt reads l's history (seeding it from l.Runs when not
// found), appends the entry, and writes it. A failure is a warning.
func (a *App) recordAttempt(l *loop.Loop, e loop.Entry)
// settleHistory reads l's history (seeding when not found), settles each
// open entry with observe, copies new reports, and writes it if anything
// changed. A failure is a warning.
func (a *App) settleHistory(l *loop.Loop, listing lifetime.Listing)
// observe is what listing and the run's machine record say about e.
// The record is machine.NewStore(a.loopData.StateDir()).Read(e.Workspace, e.Workspace);
// a read error is a warning and leaves Verdict nil.
func (a *App) observe(e *loop.Entry, listing lifetime.Listing) (loop.Observation, string /* report path */)
```

- A skipped attempt's `StartedAt` is `l.LastGate.At`; others use
  `run.StartedAt`. Triggers: tick → `TriggerTick`; `ws loop run` →
  `TriggerRun`, or `TriggerForce` with `--force`.
- When `observe` returns a verdict whose `RecordedAt` differs from the
  stored one and the record has a report path, read it (cap
  `loop.MaxReportSize`), `WriteReport(name, e.Seq, …)`, and set
  `Verdict.Report = true` after `Settle`.
- `tickLoop` calls `recordAttempt` after `attemptRun` for every outcome
  (skipped included), before `Save`. `runLoopRun` does the same.
- `tick`: when not `dryRun`, after `List`, if any loop has an open entry
  (read each history; seed when missing), call `readPass` up front;
  then call `settleHistory(l, p.listing)` for every loop before its
  `tickLoop`. A dry run never writes history.
- `pruneRuns`: right before `RemoveTree` of a run, settle that run's
  entry once more with the same listing entry (re-reading the machine
  record) and, after a successful removal, set `Closed = true`; write
  the history once at the end of `pruneRuns` when anything changed.
- `startLoopRun`: after `Create` succeeds and before `startAgent`,
  `a.loopData.WriteRunMark(ws.ID, loop.RunMark{Loop: l.Name, StartedAt: a.now().UTC()})`;
  a failure is a warning.
- `attemptRun`: before rendering, `mem, err := a.loopData.ReadMemory(l.Name)`
  (an error is a warning and leaves memory empty); set
  `data.Memory = mem.Values()`.

### Gate env (`loop_gate.go`)

`gateEnv(l *loop.Loop, data *loop.DataStore) []string`:
`WS_LOOP_NAME`, `WS_LOOP_HISTORY=<data.HistoryPath>`,
`WS_LOOP_MEMORY=<data.MemoryPath>`, then the `WS_LOOP_VAR_*` as today.
Update the doc comment and the existing test.

### `ws loop history` (`loop_history.go`)

```go
func (a *App) newLoopHistoryCmd() *cobra.Command // flags --limit (20), --run (0 = none), --clear
func (a *App) runLoopHistory(ctx context.Context, name string, opts loopHistoryOptions) error
type loopHistoryJSON struct{ Loop string; Runs []loopHistoryRunJSON; Total int; Warnings []string }
type loopHistoryRunJSON struct{ /* RunJSON in reference.md */ }
type loopVerdictJSON struct{ Verdict, Outcome, RecordedAt string; Report *string }
type loopHistoryDetailJSON struct{ Loop string; Run loopHistoryRunJSON; ReportText *string; Warnings []string } // `report_text`
type loopClearedJSON struct{ Loop string; Removed int; Warnings []string }
func (a *App) historyRunJSONOf(name string, e *loop.Entry) loopHistoryRunJSON
```

- The loop must exist: `a.loops.Get(name)` (no lock) → `loop_not_found`.
- List and `--run` read the history without a lock (seed from the
  record's `Runs` in memory when not found, without writing). `--clear`
  takes `a.loops.Lock`, writes an empty history that keeps `NextSeq`,
  and deletes `reports/`; `removed` is the number of entries.
- Text lines as in reference.md: `%-4d  %-12s  %-20s  %-7s  %-24s  %s`
  (seq; status, with ` ×N` appended when `Repeats > 1`; started_at;
  duration as `14m30s` via `time.Duration.String()` trimmed like
  `loop.FormatEvery`, or `-`; workspace or `-`; verdict, else
  `error_code: message`, else `exit N: message` for skipped, else `-`).
- Register in `newLoopCmd` (alphabetical: after `edit`). Add
  `"loop history"` to `guideTopicsBeforeReview` after `"loop edit"`.
- `ws loop rm`: after `Remove`, `a.loopData.RemoveAll(name)` (failure is a
  warning). `ws loop add` (`createLoop`): only after `Create` succeeds
  (never when it fails with `loop_exists`), the same, so a re-added name
  starts empty.
- `guides/ws-loop-history.md`: synopsis, description, statuses table,
  options, JSON output, exit codes, examples, in the style of
  `ws-loop-run.md`. In `guides/ws-loop.md` add the synopsis line and a
  "History" paragraph saying a run's verdict comes from
  `ws machine result`, so prompts should end with "finish with `ws
  machine result PASS|FAIL --file <report>`".

### Acceptance

- Tick: a started run appends a `running` entry with trigger `tick`; a
  skipped gate appends `skipped` (twice → one entry, `repeats` 2); a
  failed Claude check appends `start_failed`.
- Settle: with the listing reporting the run idle and no record → `done`;
  with a `result.toml` + `report.md` written by
  `machine.NewStore(e.data.StateDir()).WriteResult(ws, ws, …)` →
  `succeeded`, `reports/<seq>.md` holds the report; a run missing from
  the listing → `closed` (and `gone` if it was running).
- Pruning a kept-out run whose verdict appeared after the pass's first
  settle still copies the verdict before `RemoveTree` is called (assert
  order with the lifetime mock).
- `startLoopRun` writes `state/<id>/loop.toml` with the loop name.
- The gate script's env holds `WS_LOOP_MEMORY` and `WS_LOOP_HISTORY`.
- A templated prompt `{{memory "seen"}}` renders the stored value.
- `ws loop history --json deps` prints the reference shape newest first,
  `--limit 1` one run, `--run 2` the run plus `report_text`, `--run 99`
  → `loop_not_found` exit 3, `--run 1 --clear` → `usage` exit 2,
  `--clear` → `removed` and an empty list afterwards.
- `ws loop rm` deletes `state/.loops/<name>/`.
- `TestGuideTopics` (the llm coverage test) passes.

### Verify

```sh
go test ./internal/... && make lint
```

## T3. CLI: `ws loop memory` and `ws machine memory`

**Depends on:** T2. **Parallel:** T3 ∥ T4, T5, T6.

**Files:** edit `internal/cli/errors.go`, `internal/cli/errors_test.go`,
`internal/cli/cache.go` (error map only), `internal/cli/loop.go`
(register only), `internal/cli/machine.go` (register only),
`internal/cli/llm.go` (topic lists only), `internal/cli/guides/ws-loop.md`,
`internal/cli/guides/ws-machine.md`; new `internal/cli/loop_memory.go`,
`internal/cli/loop_memory_test.go`, `internal/cli/machine_memory.go`,
`internal/cli/machine_memory_test.go`, and guides
`ws-loop-memory.md`, `ws-loop-memory-list.md`, `ws-loop-memory-get.md`,
`ws-loop-memory-set.md`, `ws-loop-memory-rm.md`, `ws-loop-memory-clear.md`,
`ws-machine-memory.md`, `ws-machine-memory-list.md`,
`ws-machine-memory-get.md`, `ws-machine-memory-set.md`,
`ws-machine-memory-rm.md` under `internal/cli/guides/`.

Read first: `internal/cli/errors.go`, `machine_result.go` (`caller`,
`readReport` for `--file`), `machine.go` (`newMachineCmd`,
`knownOperation`), `loop.go`, `llm.go`, `guides/ws-machine-result.md`.

### Errors

New codes in `errors.go` with exits from reference.md:
`codeMemoryInvalid` (2), `codeMemoryNotFound` (3), `codeMemoryConflict`
(4), `codeMemoryFull` (4), `codeNotLoopRun` (4). In `cache.go`'s map:
`loop.ErrMemoryInvalid`, `ErrMemoryNotFound`, `ErrMemoryConflict`,
`ErrMemoryFull` → those codes. `errNotLoopRun = errors.New("this
workspace isn't a loop run")` in `machine_memory.go`, returned as
`newError(codeNotLoopRun, …)`.

### Shared code (`loop_memory.go`)

```go
type memoryEntryJSON struct{ Key, Value string; Rev int; UpdatedAt string; UpdatedBy *string } // json tags per reference.md
type memoryListJSON struct{ Loop string; Entries []memoryEntryJSON; Bytes int; Warnings []string }
type memoryEntryResultJSON struct{ Loop string; Entry memoryEntryJSON; Warnings []string }
type memoryRemovedJSON struct{ Loop, Key string; Removed bool; Warnings []string }
type memorySetOptions struct{ expectRev int; file string }
func memoryEntryJSONOf(key string, e loop.MemoryEntry) memoryEntryJSON
// memoryValue is args[0] or the --file contents (via a.readReport-style
// reading, capped at loop.MaxMemoryValue+1); exactly one is required, else usage.
func (a *App) memoryValue(args []string, file string) (string, error)
func (a *App) memoryList(name string) error
func (a *App) memoryGet(name, key string) error
func (a *App) memorySet(ctx context.Context, name, key, value, by string, expectRev int) error
func (a *App) memoryRm(ctx context.Context, name, key string, expectRev int) error
```

The four `memory*` helpers print JSON when `a.global.json` (always for
machine) or the text in reference.md. `memoryGet` of a missing key is
`ErrMemoryNotFound`.

### `ws loop memory`

`newLoopMemoryCmd()` with `list`, `get`, `set`, `rm`, `clear`
subcommands, `Args: knownOperation` on the parent as `machine` does.
Each checks the loop exists with `a.loops.Get` (no lock), then calls the
helper with `by = ""`. `clear` prints `{"loop","removed","warnings"}`
(reuse T2's `loopClearedJSON`). Register after `history` in
`newLoopCmd`.

### `ws machine memory`

```go
// callerLoop is the loop of the caller's root: its run mark's loop, which
// must still exist; else errNotLoopRun.
func (a *App) callerLoop() (name, caller string, err error)
```

Uses `a.caller()`, `a.workspaces.List()`, `machine.Root(list, caller)`,
`a.loopData.ReadRunMark(root)`, `a.loops.Get(mark.Loop)` (not found →
`not_loop_run`, message `loop <name> was removed`). Subcommands `list`,
`get`, `set`, `rm` (no `clear`); always JSON (automatic: `isAgentCommand`
in `app.go` sets `a.global.json` for every `ws machine` command; don't
set it yourself); `by = caller`. Register
after `roles` in `newMachineCmd`.

### Guides

Add topics: `"loop memory"`, `"loop memory list"`, `"loop memory get"`,
`"loop memory set"`, `"loop memory rm"`, `"loop memory clear"` after
`"loop history"`; `"machine memory"`, `"machine memory list"`, `"machine
memory get"`, `"machine memory set"`, `"machine memory rm"` after
`"machine roles rm"`. Each guide: synopsis, what it does, options, JSON
output, exit codes, example, in the style of `ws-machine-result.md`.
`ws-machine.md`: add the synopsis line, a "When to use it" bullet ("a
loop run: keep notes for the loop's next runs with `ws machine memory
set`; read them with `get` or `list`; use `--expect-rev` when runs
overlap"), and the new error rows. `ws-loop.md`: add the synopsis lines
and a "Memory" paragraph (`{{memory "KEY"}}`, `$WS_LOOP_MEMORY`, limits).

### Acceptance

- `ws loop memory set deps seen "#412"` then `get` prints `#412`; `--json
  list` matches reference.md with `bytes` 4 and `updated_by` null.
- `set --expect-rev 0` on an existing key → `memory_conflict` exit 4;
  `set` with both VALUE and `--file` → `usage` exit 2; `--file -` reads
  stdin; a 65537-byte value → `memory_full` exit 4; key `1x` →
  `memory_invalid` exit 2; `rm` of a missing key → `memory_not_found`
  exit 3; unknown loop → `loop_not_found` exit 3.
- `ws machine memory set seen x` from a workspace whose root has a run
  mark for `deps` writes `deps`'s memory with `updated_by` = the caller;
  from a child of that run too; from a workspace with no mark →
  `not_loop_run` exit 4; outside a workspace → `outside_workspace`.
- `TestGuideTopics` passes; `ws llm machine memory set` prints the guide.

### Verify

```sh
go test ./internal/... && make lint
```

## T4. Desktop bridge: commands, API client, slice, Tauri mock

**Depends on:** nothing (reference.md is the contract). **Parallel:**
T4 ∥ T1, T2, T3, T6.

**Files:** edit `desktop/src-tauri/src/loops.rs`,
`desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/src/online/dispatch.rs`,
`desktop/src/api.ts`, `desktop/src/store.ts`,
`desktop/src/test/fixtures.ts`, `desktop/test/tauri-mock.js`; new
`desktop/src/features/loops/loopDataSlice.ts`,
`desktop/src/features/loops/loopDataSlice.test.ts`.

Read first: `loops.rs` (structs, `*_args` builders and their tests),
`roles.rs` (`ws_json_with_input`), `online/dispatch.rs` (`COMMANDS`,
`args!`, the tests at the bottom), `lib.rs` (`generate_handler!`),
`api.ts` (loops section, `call`), `loopsSlice.ts`, `fixtures.ts`
(`loop`, `fakeApi`), `test/tauri-mock.js` (`data.loops`, `loops_*`).

### Rust (`loops.rs`)

Structs, `#[serde(rename_all(deserialize = "snake_case", serialize = "camelCase"))]`:
`LoopRunVerdict { verdict, outcome, recorded_at, report: Option<String> }`,
`LoopHistoryRun { seq: u32, status, trigger, started_at, finished_at: Option<String>, duration_seconds: Option<u64>, workspace: Option<String>, workspace_exists: bool, session: Option<String>, error: Option<LoopError>, gate: Option<LoopGate>, repeats: u32, verdict: Option<LoopRunVerdict> }`,
`LoopHistory { loop_name (serde rename "loop"), runs, total: u32, warnings }`,
`LoopRunDetail { loop_name ("loop"), run, report_text: Option<String>, warnings }`,
`LoopMemoryEntry { key, value, rev: u32, updated_at, updated_by: Option<String> }`,
`LoopMemory { loop_name ("loop"), entries, bytes: u64, warnings }`,
`LoopMemorySet { loop_name ("loop"), entry, warnings }`,
`LoopMemoryRemoved { loop_name ("loop"), key, removed: bool, warnings }`,
`LoopCleared { loop_name ("loop"), removed: u32, warnings }`.
(`loop` is a Rust keyword: use `#[serde(rename = "loop")] pub loop_name: String`.)

Arg builders (without `--json`, which `ws_json` inserts, except the
stdin one):

```rust
pub fn history_args(name: &str, limit: Option<u32>) -> Vec<String>      // loop history [--limit=N] -- name
pub fn history_run_args(name: &str, seq: u32) -> Vec<String>              // loop history --run=SEQ -- name
pub fn history_clear_args(name: &str) -> Vec<String>                      // loop history --clear -- name
pub fn memory_list_args(name: &str) -> Vec<String>                        // loop memory list -- name
pub fn memory_set_args(name: &str, key: &str, expect_rev: Option<i64>) -> Vec<String>
    // loop memory set --json [--expect-rev=N] --file=- -- name key
pub fn memory_remove_args(name: &str, key: &str) -> Vec<String>           // loop memory rm -- name key
pub fn memory_clear_args(name: &str) -> Vec<String>                       // loop memory clear -- name
```

Commands (`#[cfg_attr(feature = "desktop", tauri::command)] pub async fn`):
`loops_history(state, name: String, limit: Option<u32>) -> CmdResult<LoopHistory>`,
`loops_history_run(state, name, seq: u32) -> CmdResult<LoopRunDetail>`,
`loops_history_clear(state, name) -> CmdResult<LoopCleared>`,
`loops_memory(state, name) -> CmdResult<LoopMemory>`,
`loops_memory_set(state, name, key, value: String, expect_rev: Option<i64>) -> CmdResult<LoopMemorySet>`
(`runner.ws_json_with_input(&memory_set_args(..), Some(value.as_bytes()))`),
`loops_memory_remove(state, name, key) -> CmdResult<LoopMemoryRemoved>`,
`loops_memory_clear(state, name) -> CmdResult<LoopCleared>`.

Add them in this order after `loops::loops_run_now` in `lib.rs`'s
`generate_handler!` and at the end of `COMMANDS`, with `dispatch` arms
(`args!(args, { name: String, limit: Option<u32> })`, etc.; camelCase
`expectRev`). Unit tests: one per arg builder, and one that a
`LoopHistory` JSON sample from reference.md deserializes and serializes
with `workspaceExists`, `durationSeconds`, `loop`.

### TypeScript

- `api.ts`: the interfaces in reference.md, and the seven `api` methods
  (`call<LoopHistory>("loops_history", { name, limit })`, …,
  `loopsMemorySet: (name, key, value, expectRev: number | null)` →
  `call("loops_memory_set", { name, key, value, expectRev })`).
- `fixtures.ts`: `loopHistoryRun(overrides)`, `loopMemoryEntry(overrides)`
  factories; `fakeApi` gets `vi.fn` defaults for the seven methods
  (empty history `{loop: name, runs: [], total: 0, warnings: []}`,
  empty memory, set echoing an entry with rev 1, etc.).
- `loopDataSlice.ts` (registered as `loopData` in `store.ts`):

```ts
export interface LoopDataState {
  readonly loop: string | null;            // whose data is held; reset when it changes
  readonly history: LoopHistory | null;
  readonly historyLimit: number;           // 20, +20 per "Show more"
  readonly historyError: AppError | null;
  readonly run: LoopRunDetail | null;      // the run open in the dialog
  readonly runLoading: boolean;
  readonly memory: LoopMemory | null;
  readonly memoryError: AppError | null;
  readonly saving: boolean;
  readonly saveError: AppError | null;     // memory_conflict shows a Reload action
}
export const fetchLoopHistory: AsyncThunk<LoopHistory, { name: string; limit: number }>;
export const fetchLoopRun: AsyncThunk<LoopRunDetail, { name: string; seq: number }>;
export const clearLoopHistory: AsyncThunk<LoopCleared, string>;   // then refetches history
export const fetchLoopMemory: AsyncThunk<LoopMemory, string>;
export const saveLoopMemory: AsyncThunk<LoopMemorySet, { name: string; key: string; value: string; expectRev: number | null }>; // then refetches memory
export const removeLoopMemory: AsyncThunk<LoopMemoryRemoved, { name: string; key: string }>; // then refetches
export const clearLoopMemory: AsyncThunk<LoopCleared, string>;    // then refetches
export const { runClosed, saveErrorCleared } = slice.actions;
```

Built with `createAppThunk` and `rejectWithValue(toAppError(err))` as
`loopsSlice.ts`; failures of clear/remove dispatch `noticeShown` with
`severity: "error"`. A fulfilled fetch for a different `name` than
`state.loop` replaces everything.

### Tauri mock (`desktop/test/tauri-mock.js`)

Add `data.loopHistory` and `data.loopMemory` keyed by loop name, and
handlers for the seven commands (memory set honours `expectRev`:
mismatch throws `{code: "memory_conflict", message: "seen changed (rev 3)"}`).
Screenshot data for `deps-bump`, newest first, `total: 57`:

| seq | status | started | duration | workspace (exists) | verdict / error |
| --- | --- | --- | --- | --- | --- |
| 57 | running | 2026-10-24T08:00:00Z | – | loop-deps-bump-1a2b3c4d (yes) | – |
| 56 | succeeded | 2026-10-23T08:00:00Z | 870 | loop-deps-bump-9f8e7d6c (yes) | PASS, report |
| 55 | failed | 2026-10-22T08:00:00Z | 1520 | loop-deps-bump-5a4b3c2d (no) | FAIL, report |
| 54 | skipped ×12 | 2026-10-21T08:00:00Z | – | – | gate exit 1 "no outdated modules" |
| 53 | done | 2026-10-20T08:00:00Z | 640 | loop-deps-bump-0e1f2a3b (no) | – |
| 52 | start_failed | 2026-10-19T08:00:00Z | – | – | claude_missing "Claude Code isn't installed" |
| 51 | gone | 2026-10-18T08:00:00Z | 300 | loop-deps-bump-77aa88bb (no) | – |

`loops_history_run` for 56 returns a markdown report ("## Bumped 3
modules", a bullet list, a PR link); for 55 a FAIL report; others
`reportText: null`. Memory for `deps-bump`: `seen` = `"#412 #415 #418"`
(rev 3, by `loop-deps-bump-9f8e7d6c`), `last_pr` = `"https://github.com/bobcob7/buff-bear/pull/88"`
(rev 1, by the same), `notes` = a 6-line markdown note (rev 7, by
null). `triage` gets empty history and memory.

### Acceptance

- `commands_match_generate_handler` and `every_command_is_handled` pass.
- Each arg builder test asserts the exact vector, e.g.
  `memory_set_args("deps","seen",Some(3))` ==
  `["loop","memory","set","--json","--expect-rev=3","--file=-","--","deps","seen"]`.
- `loopDataSlice.test.ts`: fetch history fulfilled/rejected; switching
  loops resets state; save conflict sets `saveError.code ===
  "memory_conflict"`; successful save refetches memory.
- `npm run typecheck` passes with the new `fakeApi` methods.

### Verify

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --no-default-features --features online --all-targets -- -D warnings && cargo test --no-default-features --features online
cd desktop && npm run typecheck && npm test -- --testTimeout=60000
```

## T5. Desktop UI: History and Memory cards

**Depends on:** T4. **Parallel:** T5 ∥ T1, T2, T3, T6.

**Files:** new `desktop/src/features/loops/Card.tsx` (`Card` and
`CodeBlock` moved out of `LoopsPane.tsx`, unchanged, so the new cards
can use them without a circular import),
`desktop/src/features/loops/HistoryCard.tsx`, `HistoryCard.module.css`, `HistoryCard.test.tsx`, `RunDialog.tsx`,
`MemoryCard.tsx`, `MemoryCard.module.css`, `MemoryCard.test.tsx`,
`MemoryDialog.tsx`, `history.ts`, `history.test.ts` (all under
`desktop/src/features/loops/`); edit `LoopsPane.tsx`,
`LoopsPane.test.tsx`.

Read first: `LoopsPane.tsx` (the `Card`, `CodeBlock`, `LastRun`
components), `LoopsPane.module.css`, `loopDataSlice.ts` (T4),
`api.ts` loop types, `../conversation/ConversationView.tsx` (how
markdown is rendered with `react-markdown` + `remark-gfm`), `../../format.ts`.

### `history.ts`

```ts
export const statusLabel: (run: LoopHistoryRun) => string;   // "Succeeded", "Skipped ×12", "Couldn't start", "Workspace removed" (gone), "Running", "Finished, no verdict" (done), "Failed"
export const statusColor: (status: LoopRunStatus) => "success" | "error" | "info" | "warning" | "default"; // succeeded success; failed, start_failed error; running info; gone warning; done, skipped default
export const formatDuration: (seconds: number | null) => string; // null → "–"; 59 → "59s"; 870 → "14m 30s"; 3720 → "1h 2m"
export const runSummary: (run: LoopHistoryRun) => string;  // verdict, else "code: message", else "exit N: message", else ""
```

### Components

- `Card.tsx`: move `Card` and `CodeBlock` from `LoopsPane.tsx`
  (exported; they keep importing `LoopsPane.module.css`); `LoopsPane.tsx`
  imports them.
- `HistoryCard({ loop })`: a `Card` titled "History". On mount, and
  whenever `loop.name` or `loop.lastRun?.startedAt` changes, dispatch
  `fetchLoopHistory({ name, limit: historyLimit })`. Renders
  `<ul aria-label="Run history">`; each `<li>` holds one full-width
  `button` (accessible name starts with `Run <seq>`) showing a MUI
  `Chip` (`statusLabel`, `statusColor`), the start time
  (`toLocaleString` + `age`), `formatDuration`, the workspace, and
  `runSummary`. Clicking opens `RunDialog`. A workspace that exists is
  also a separate link button ("Open <workspace>") that does what
  `LastRun` does; one that doesn't shows "<workspace> (removed)".
  Header buttons: "Refresh", "Clear history" (confirm dialog "Clear the
  history of <label>? Saved reports are deleted too."). Footer: "Show
  more" when `runs.length < total` (limit +20). Empty: "No runs yet.".
  Error: the message and a "Retry" button.
- `RunDialog`: MUI `Dialog` titled `Run <seq> · <statusLabel>`;
  dispatches `fetchLoopRun` on open; shows a `dl` (trigger, started,
  finished, duration, workspace, session, gate, error, verdict) and the
  report rendered as markdown in a scrollable block, or "No report was
  recorded." Close dispatches `runClosed`.
- `MemoryCard({ loop })`: a `Card` titled "Memory"; fetches on mount and on name
  change. A table (`aria-label="Memory"`) with Key (monospace), Value
  (first line, cut to 120 chars, monospace), Updated (`age` + "by
  <updatedBy>" or "by you"), and per-row "Edit <key>" and "Delete <key>"
  buttons. Header: "Add", "Clear memory" (confirm). Footer: `formatSize`
  of `bytes` of 256 KiB, `count(entries.length, "key")` of 100. Empty:
  "Nothing remembered yet. Runs write here with ws machine memory set."
  On phone width the table becomes stacked rows (CSS grid), no
  horizontal scroll.
- `MemoryDialog`: Add/Edit. Key `TextField` (disabled when editing;
  validated client-side `^[A-Za-z_][A-Za-z0-9_]{0,63}$` with helper text);
  Value multiline `TextField` (`minRows={4}`, `maxRows={16}`, monospace);
  a byte counter `n / 65,536 bytes` (`new TextEncoder().encode(v).length`),
  Save disabled over the limit. Save dispatches `saveLoopMemory` with
  `expectRev` = the entry's `rev` when editing, `0` when adding. On
  `memory_conflict`, an `Alert` "A run changed <key> since you opened
  it." with "Reload" (refetch memory, reload the dialog's value, clear
  the error).
- `LoopsPane.tsx`: render `<HistoryCard loop={loop} />` right after
  `ScheduleCard`, and `<MemoryCard loop={loop} />` after `VarsCard`.

### Acceptance

- `history.test.ts`: every status label and colour, `formatDuration`
  cases above, `runSummary` for verdict, error and skipped.
- `HistoryCard.test.tsx`: renders the runs from a `fakeApi` history;
  clicking "Run 56" calls `loopsHistoryRun("deps", 56)` and shows the
  report heading; "Show more" calls `loopsHistory("deps", 40)`; "Clear
  history" → confirm → `loopsHistoryClear("deps")`; "Open <ws>" selects
  the agent (`ui.section === "agents"`).
- `MemoryCard.test.tsx`: lists entries; Add with key `seen` and value
  `x` calls `loopsMemorySet("deps","seen","x",0)`; Edit sends the
  entry's rev; a `memory_conflict` rejection shows the alert and Reload
  refetches; Delete → `loopsMemoryRemove`; the byte counter disables
  Save at 65,537 bytes; key `1x` shows the helper error and disables Save.
- `LoopsPane.test.tsx` still passes and asserts both cards are present
  (`getByRole("region", { name: "History" })`, `"Memory"`).
- CSS: under `@media (max-width: 720px)` every button in both cards has
  `min-height: 40px`, the history row wraps (no fixed widths), and the
  memory table has no horizontal scroll.

### Screenshot checklist (for the orchestrator's screenshot child)

With `desktop/test/tauri-mock.js` (and `web-mock.js` for online), select
loop "Dependency bumps" and capture: `desktop/docs/loops-history.png`
(1280×800, History card), `loops-run-report.png` (Run 56 dialog),
`loops-memory.png` (Memory card), `loops-memory-edit.png` (editing
`notes`), `online-loops-history-mobile.png` and
`online-loops-memory-mobile.png` (390×844). Named checks with
`getBoundingClientRect`: every button in both cards ≥ 40px tall at
390px; `document.documentElement.scrollWidth <= 390`; no chip overlaps
the row text; the dialog's report block scrolls instead of growing past
the viewport.

### Verify

```sh
cd desktop && npm run typecheck && npm test -- --testTimeout=60000
```

## T6. Docs: tools pages, state, ADR index, READMEs

**Depends on:** nothing (reference.md is the contract). **Parallel:**
T6 ∥ every task.

**Files:** edit `docs/tools/ws-loop.md`, `docs/tools/ws-machine.md`,
`docs/state.md`, `docs/adrs/README.md`, `README.md`,
`desktop/README.md`.

- `docs/tools/ws-loop.md`: synopsis lines for `history` and the five
  `memory` subcommands; new sections `### ws loop history` and
  `### ws loop memory` (description, options table, statuses table);
  JSON shapes under `## Output` (copy from reference.md); the
  `memory` template function and `WS_LOOP_MEMORY`/`WS_LOOP_HISTORY` in
  the scripts and variables part; `ws loop rm` now deletes history and
  memory; new rows in `## Errors`; two examples (read history; a
  prompt that uses `{{memory "seen"}}` and ends with `ws machine result`).
- `docs/tools/ws-machine.md`: `ws machine memory` section and error rows.
- `docs/state.md`: Layout rows for `state/.loops/<name>/` (history.json,
  reports/, memory.json, .memory.lock; who writes each; deleted by
  `ws loop rm`) and `state/<id>/loop.toml`; a "Loop history and memory"
  paragraph under Records (JSON, versions, limits, retention); add
  `ws loop history --clear` and `ws loop memory` to the `loops/.lock`
  row where they take it (`--clear` does; memory doesn't).
- `docs/adrs/README.md`: index row for 0056, status Proposed; append
  `; history and memory added by [0056](0056-loop-history-and-memory.md)`
  to 0048's status cell.
- `README.md`: one or two sentences in the loops part mentioning run
  history and shared memory, linking `docs/tools/ws-loop.md`.
- `desktop/README.md`: a paragraph on the History and Memory cards and a
  screenshot table referencing the six PNG names in T5's checklist
  (they're added by the orchestrator's screenshot step).

### Acceptance

- Every command, flag, JSON field and error code in reference.md appears
  in `docs/tools/ws-loop.md` or `ws-machine.md`, with identical names.
- Relative links resolve: `grep -o '](\([^)]*\.md\)' -r docs README.md desktop/README.md`
  targets exist.

### Verify

```sh
# every relative .md link in the edited files resolves
for f in docs/tools/ws-loop.md docs/tools/ws-machine.md docs/state.md docs/adrs/README.md README.md desktop/README.md; do
  d=$(dirname "$f")
  grep -oE '\]\([^)#: ]+\.md' "$f" | sed 's/^](//' | while read -r l; do
    [ -e "$d/$l" ] || echo "broken: $f -> $l"
  done
done
go test ./internal/cli/ -run TestGuideTopics
```

The loop must print nothing; `TestGuideTopics` must still pass (T6
changes no guide).
