# Loop visibility and blocked runs: command and API reference

The contract for [ADR 0057](../../adrs/0057-loop-visibility-and-blocked-runs.md).
Every task in [tasks.md](tasks.md) implements part of it; when they
disagree, this file wins. It extends
[loop-history/reference.md](../loop-history/reference.md); everything not
mentioned here is unchanged. JSON optional values are `null`, never
omitted.

## Loop record (`CONFIG_DIR/loops/<name>.toml`)

| Version | Keys |
| --- | --- |
| 1 | ADR 0048 keys; literal prompt |
| 2 | + `script`, `script_timeout`, `vars`, `last_gate`; templated prompt |
| 3 | + `hidden` (bool) |

Go (`internal/loop/loop.go`):

```go
const FormatVersion = 3 // was 2
const Version2 = 2

type Loop struct {
	// … every existing field, unchanged …
	// Hidden is whether runs start hidden (ADR 0057); nil (a version 1 or 2
	// record) is the default, hidden.
	Hidden *bool `toml:"hidden,omitempty"`
}

// RunsHidden is *l.Hidden, or true when it is nil.
func (l *Loop) RunsHidden() bool
```

- `readLoop`: `l.Version < 3 && l.Hidden != nil` is an error
  `parsing <path>: version <N> record has version 3 keys` (like the
  existing version 1 check, which stays).
- `Store.Create`: sets `Version = FormatVersion` (as today) and, when
  `Hidden == nil`, `Hidden = &true`, so new records always write the key.
- `Store.Save`: unchanged (keeps the version).
- `Edit` gains `Hidden *bool`; `Empty()` counts it. `applyTo`, when
  `e.Hidden != nil`: `c.Hidden = &value`; then
  - `c.Version == Version2` → `c.Version = FormatVersion`;
  - `c.Version == Version1`: if `e.Prompt != nil || e.Script != nil ||
    e.Vars != nil` the existing rule already bumps it (now to 3); else if
    `strings.Contains(c.Prompt, "{{")` → `fmt.Errorf("%w: loop %s has a
    literal version 1 prompt containing {{; give --prompt too to change
    whether its runs are hidden", ErrInvalid, c.Name)`; else
    `c.Version = FormatVersion`.
- The existing version 1 bump (`c.Version = FormatVersion`) now bumps to 3.
  That is fine: a version 3 record without `hidden` is impossible after
  `Create`, but `RunsHidden` treats nil as true anyway.

## Attention (`internal/loop/attention.go`, new)

```go
// Attention is what a loop run asks of a person (ADR 0057).
type Attention string

const (
	AttentionNone           Attention = ""
	AttentionBlocked        Attention = "blocked"
	AttentionNeedsAttention Attention = "needs_attention"
)

// VerdictBlocked is the verdict a run records to ask a person for help.
const VerdictBlocked = "BLOCKED"

// AttentionOf is what a run whose session is in state (a lifetime state:
// "working", "starting", "idle", "stopped", "unknown", or "" without a
// session) and whose recorded verdict is verdict ("" for none) asks of a
// person.
func AttentionOf(state, verdict string) Attention
```

| state | verdict | result |
| --- | --- | --- |
| `working`, `starting` | any | none |
| any other | `BLOCKED` | `blocked` |
| `idle` | `""` | `needs_attention` |
| any other | anything else | none |

`internal/loop` keeps no `internal/lifetime` import: states are strings.

## History

`internal/loop/history.go`:

```go
const (
	StatusBlocked        RunStatus = "blocked"
	StatusNeedsAttention RunStatus = "needs_attention"
)
```

`Verdict.Outcome` is `"pass"`, `"fail"` or `"blocked"`. `HistoryVersion`
stays 1. The on-disk `Entry` shape is unchanged.

### History status rules

`(*Entry).Settle(obs, now)` replaces loop-history's rules 2-7 with:

1. Entry not open (`Workspace == ""` or `Closed`): no change.
2. `!obs.Listed`: `Closed = true`, then by status:
   - `running` → `gone`, `FinishedAt = now`;
   - `needs_attention` → `done`, `FinishedAt = now`;
   - `blocked` → stays `blocked`, `FinishedAt = now` if nil;
   - others unchanged. Changed.
3. `obs.Verdict != nil` and differs from the stored one (by
   `RecordedAt`, `Verdict` or `Outcome`): store it. Outcome `pass` →
   `succeeded`, `fail` → `failed`, each with `FinishedAt =
   Verdict.RecordedAt`; return changed. Outcome `blocked`: go on to rule
   5 and return changed even if rule 5 changes nothing more.
4. Stored verdict with outcome `pass` or `fail`: no further change.
5. Otherwise (no verdict, or a `blocked` one), with
   `a := AttentionOf(obs.State, verdict)` where `verdict` is the stored
   `Verdict.Verdict` or `""`:
   - `obs.State` is `working` or `starting` → `running`, `FinishedAt = nil`;
   - `a == blocked` → `blocked`, `FinishedAt = nil`;
   - `a == needs_attention` → `needs_attention`, `FinishedAt = nil`;
   - `obs.State == "stopped"` (no verdict) → `done`, `FinishedAt = now`
     if it was nil;
   - anything else: no change.

`SeedHistory` and `Append` are unchanged.

`internal/cli/loop_tick.go` `observe` already builds `Verdict.Outcome`
from `rec.Outcome()`, which now returns `blocked` for `BLOCKED`
(below). `settleEntry` copies the report on every new verdict, so a
`BLOCKED` run's question is saved as `reports/<seq>.md`, and replaced by
a later verdict's report.

## Machine result (`internal/machine/result.go`)

```go
const OutcomeBlocked Outcome = "blocked"
// VerdictBlocked is a loop run's verdict asking a person for help (ADR 0057).
const VerdictBlocked = "BLOCKED"
```

`OutcomeOf(spawn, verdict)`: `spawn == nil && verdict == VerdictBlocked`
→ `OutcomeBlocked`; otherwise unchanged. (`internal/loop` and
`internal/machine` each define `VerdictBlocked`; neither imports the
other.)

`ws machine result` (`internal/cli/machine_result.go`), after
`NormalizeVerdict`, when `v == machine.VerdictBlocked`:

- `root != caller` → `result_invalid`, message `BLOCKED is only for a
  loop run's own verdict; record FAIL instead`.
- `a.loopData.ReadRunMark(caller)` not found → same error. A read error
  → `fmt.Errorf("reading %s's run mark: %w", caller, err)` (exit 1).
- `file == ""` → `result_invalid`, message `BLOCKED needs --file with
  your question`.

JSON output is unchanged in shape; `result.outcome` is `"blocked"`.

## CLI

### ws loop add / edit

New flags, on both:

```
      --hidden          start each run's workspace hidden; blocked runs are shown anyway (default true)
      --show            start each run's workspace visible; the same as --hidden=false
```

- `add`: `Hidden = &opts.hidden` (default true); `--show` sets it false.
- `edit`: `--hidden` or `--show` sets `Edit.Hidden`; neither leaves it.
- Both given: `usage` (exit 2), message `give --hidden or --show, not both`.
  (`--show --hidden=false` is also both; reject when both `Changed`.)
- The `Use:` line of each gains `[--hidden[=BOOL] | --show]` in the guide
  synopsis.

### Loop JSON (`loopJSON`, every `ws loop` command)

Adds, after `"paused"`:

```json
"hidden": true
```

`hidden` is `l.RunsHidden()`. Text output of `ws loop list` is unchanged.

### ws loop run / tick

`startLoopRun` creates the workspace with
`workspace.CreateOptions{Name: "loop-" + l.Name, Specs: parseSpecs(l.Specs), Hidden: l.RunsHidden()}`.
No output change.

### ws loop history

Statuses add `blocked` and `needs_attention`; `verdict.outcome` adds
`blocked`. `duration_seconds` is also set for `blocked` with
`finished_at` set (a closed blocked run). Text output: unchanged format;
the status column can now read `blocked` or `needs_attention`, the
verdict column `BLOCKED`.

```
45  blocked          2026-11-11T08:00:00Z  -       loop-deps-bump-2b3c4d5e  BLOCKED
44  needs_attention  2026-11-10T20:00:00Z  -       loop-deps-bump-1f2e3d4c  -
```

(The `%-12s` status column stays; longer statuses push the line.)

### ws list

Each workspace (and each `ws machine children` workspace, which shares
`listWorkspaceJSON`) gains, after `"review"`:

```json
"loop_run": {"loop": "deps-bump", "attention": "blocked"}
```

- `null` for a workspace with no `state/<id>/loop.toml`, and for
  unknown directories.
- `attention` is `loop.AttentionOf(state, verdict)`, `null` for none.
  `state` is the session's `State` (`""` without a session); `verdict`
  is `machine.NewStore(state dir).Read(id, id)`'s `Result.Verdict`, or
  `""`. A run mark or machine record that can't be read is a warning,
  `couldn't read workspace <id>'s loop run: <err>`; with an unreadable
  run mark `loop_run` is `null`, with an unreadable machine record
  `attention` is computed with verdict `""`.
- Without `-a`, a hidden workspace whose `loop_run.attention` is not
  null is listed, and not counted in `hidden_count`.
- Text output: such a workspace's NAME column reads `<name> (blocked)`
  or `<name> (needs attention)` in place of `<name> (hidden)`.

Go:

```go
type listLoopRunJSON struct {
	Loop      string  `json:"loop"`
	Attention *string `json:"attention"`
}
// listWorkspaceJSON gains:
	LoopRun *listLoopRunJSON `json:"loop_run"`

// loopRunInfo is the loop_run of the listed workspace e, or nil when it
// isn't a loop run. Failures are warnings.
func (a *App) loopRunInfo(e *lifetime.Entry) *listLoopRunJSON
// visibleEntries leaves out hidden workspaces unless all, except loop runs
// that need attention (raised), and counts the ones it left out.
func visibleEntries(entries []lifetime.Entry, all bool, raised func(*lifetime.Entry) bool) ([]lifetime.Entry, int)
```

### Prompt guidance (guides)

Add this section, verbatim apart from heading level, to
`internal/cli/guides/ws-loop.md` (after "Scripts and variables") and to
`ws-loop-add.md` (after "Prompt templates"); `ws-loop-edit.md` links to
it with one sentence:

```markdown
## Ending a run, and asking for help

A run's agent should end by recording a verdict with `ws machine result`,
so the run's history says how it went:

- `ws machine result PASS --file ../report.md` when it did its job, or
  `FAIL` when it couldn't.
- To ask a person, write the question to a file and run
  `ws machine result BLOCKED --file ../question.md`, then stop. The run
  is shown in the Agents list, even when its loop hides runs, and the
  app notifies once. When the person answers in the conversation, the
  agent carries on and records `PASS`, `FAIL` or `BLOCKED` again; until
  it does, the run stays blocked.

A run that goes idle without recording a verdict needs attention: it is
shown and notified the same way. End the prompt with the verdict step,
for example:

> When you're done, write a short report to ../report.md and run
> `ws machine result PASS --file ../report.md`, or `FAIL` if you
> couldn't finish. If you need a decision from a person, write your
> question to ../question.md, run
> `ws machine result BLOCKED --file ../question.md`, and stop.

Runs start hidden unless the loop was added with `--show`; see `--hidden`.
```

`internal/cli/guides/ws-machine-result.md` gains, after its first
paragraph group, a `BLOCKED` paragraph:

```markdown
A loop run (a workspace `ws loop` started, not one of its children) can
record `BLOCKED` to ask a person for help: `--file` is required and holds
the question. Its outcome is `blocked`. Only a later `ws machine result`
clears it. Anywhere else `BLOCKED` is `result_invalid`.
```

and its JSON note becomes "`outcome` is `pass` or `fail`, or `blocked`
for a loop run's `BLOCKED`". `ws-loop-history.md` lists the two new
statuses; `ws-list.md` documents `loop_run` and the visibility rule;
`ws-loop-list.md`, `ws-loop-add.md` and `ws-loop-edit.md` show
`"hidden": true` in their JSON examples and the new flags.

## Desktop (Tauri) and Workspace Online

**No new commands or routes.** The shapes of existing ones change; the
online `POST /api/invoke/<command>` and `/api/ws` frames carry them as
they are.

| Command | Change |
| --- | --- |
| `list_entries` | each `Entry` gains `loopRun`; `hidden` is effective (below); `status` raised (below) |
| `loops_list`, `loops_add`, `loops_edit`, `loops_set_paused`, `loops_remove` | `Loop` gains `hidden: bool` |
| `loops_add`, `loops_edit` | request `NewLoop` gains `hidden: boolean \| undefined` |
| `loops_history`, `loops_history_run` | `status` may be `blocked` / `needs_attention`; `verdict.outcome` may be `blocked` |
| socket event `notify`, Web Push | new payloads for loop events (below) |

### Rust model (`desktop/src-tauri/src/model.rs`)

```rust
/// A loop run's `loop_run` from `ws list` (ADR 0057).
#[derive(Debug, Clone, Deserialize, Serialize, PartialEq)]
pub struct WsLoopRun {
    #[serde(rename = "loop")]
    pub loop_name: String,
    #[serde(default)]
    pub attention: Option<String>, // "blocked" | "needs_attention"
}
// WsWorkspace gains:
    #[serde(default)]
    pub loop_run: Option<WsLoopRun>,
// Entry gains (serialized camelCase as "loopRun": {"loop": …, "attention": …}):
    pub loop_run: Option<WsLoopRun>,
```

In `workspace_entry`:

- `raised = ws.loop_run.as_ref().is_some_and(|r| r.attention.is_some())`;
- `hidden: ws.hidden && !raised`;
- when `raised` and the computed status is `Status::Idle` or
  `Status::Done`, the status is `Status::NeedsInput`.

`session_entry` sets `loop_run: None`.

### Rust loops (`desktop/src-tauri/src/loops.rs`)

```rust
fn default_true() -> bool { true }
// Loop gains:
    #[serde(default = "default_true")]
    pub hidden: bool,
// NewLoop gains:
    #[serde(default)]
    pub hidden: Option<bool>,
```

`add_args` and `edit_args`: when `req.hidden` is `Some(b)`, push
`--hidden=<b>` (`--hidden=true` / `--hidden=false`) right after
`--permission-mode…` and before the script flags.

### Tray and notifications (`desktop/src-tauri/src/tray/`, `online/push.rs`)

`attention.rs`:

```rust
pub enum Attention { NeedsInput, Done, Busy, Blocked, RunNeedsAttention }

// Candidate gains:
    /// The entry is a loop run that is blocked or needs attention; its
    /// notification comes from loop_step, not step (ADR 0057).
    pub loop_raised: bool,

/// What loop_step remembers.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct LoopSnapshot {
    /// A listing has been stepped (the first one notifies nothing).
    pub seeded: bool,
    /// Raised runs not yet notified: key → when first seen raised (ms).
    pub pending: BTreeMap<String, i64>,
    /// Runs already notified, or raised in the first listing; never pruned.
    pub notified: BTreeSet<String>,
}

/// The loop attention of entry: Blocked for "blocked", RunNeedsAttention
/// for "needs_attention", None otherwise or without a workspace.
pub fn loop_attention(entry: &Entry) -> Option<Attention>

/// One notification per raised loop run (ADR 0057).
pub fn loop_step(prev: &LoopSnapshot, entries: &[Entry], now_ms: i64) -> (LoopSnapshot, Vec<Event>)
```

`loop_step`, for each entry with `Some(a) = loop_attention(entry)`, in
listing order, starting from `next = { seeded: true, pending: {}, notified: prev.notified.clone() }`:

1. key in `notified` → skip.
2. `!prev.seeded` → insert into `next.notified`; skip.
3. `since = prev.pending.get(key).copied().unwrap_or(now_ms)`; if
   `now_ms - since >= DWELL_MS` → push `Event { key, name: entry.name,
   snippet: snippet(entry, Attention::NeedsInput), attention: a }` and
   insert into `next.notified`; else `next.pending.insert(key, since)`.

`candidates` sets `loop_raised = loop_attention(entry).is_some()`.
`step`'s `due` also requires `!candidate.loop_raised`. `attention_of`
never returns the two new variants.

`watcher.rs`: `Inner` gains `loop_snapshot: LoopSnapshot`; `observe`
runs `loop_step` after `step` and returns
`Observed { candidates, events: step events then loop events }`.
`wanted`: `Blocked | RunNeedsAttention` pass when `settings.enabled &&
settings.needs_input`.

`view.rs` `notice`:

| attention | title | body (snippet, else) |
| --- | --- | --- |
| `Blocked` | `<name> is blocked` | `A loop run is waiting for your answer` |
| `RunNeedsAttention` | `<name> needs attention` | `A loop run stopped without a verdict` |

`summary` is unchanged ("N agents need you"). `tray_view` is unchanged:
a raised run's status is `NeedsInput`, so it already counts.

`online/push.rs` `event_payload`: kind `"loop"` for `Blocked` and
`RunNeedsAttention` (tag `loop:<key>`, url `/?agent=<key>`).

### TypeScript (`desktop/src/api.ts`)

```ts
/** What a loop run asks of a person (ADR 0057). */
export type LoopAttention = "blocked" | "needs_attention";

/** A workspace that is a run of a loop, from ws list's loop_run. */
export interface EntryLoopRun {
  readonly loop: string;
  readonly attention: LoopAttention | null;
}

// Entry gains:
  /** Set for a loop run's workspace (ADR 0057). */
  readonly loopRun: EntryLoopRun | null;
// Entry.hidden's doc comment: hidden and not a raised loop run.

// Loop gains:
  /** Whether runs start hidden (ADR 0057). */
  readonly hidden: boolean;
// NewLoop gains:
  readonly hidden?: boolean;

export type LoopRunStatus =
  "running" | "done" | "succeeded" | "failed" | "start_failed" | "skipped" | "gone" | "blocked" | "needs_attention";

// LoopRunVerdict.outcome:
  readonly outcome: "pass" | "fail" | "blocked";
```

### Desktop UI

- **Loop form** (`LoopForm.tsx`, Agent fieldset, after Permission mode):
  an MUI `FormControlLabel` + `Switch`, label **Hide runs in Agents**,
  helper text `Blocked runs and runs that need attention are shown
  anyway`. `LoopFormValues.hidden: boolean`, `emptyValues.hidden = true`,
  `valuesOf(loop).hidden = loop.hidden`, `requestOf` always sends
  `hidden`.
- **Loop view** (`LoopsPane.tsx`, Agent card): a row `Runs` →
  `Hidden unless blocked` when `loop.hidden`, else `Shown in Agents`.
- **History** (`history.ts`): `statusLabel` `blocked` → `Blocked`,
  `needs_attention` → `Needs attention`; `statusColor` both →
  `warning`; `runSummary` unchanged (shows `BLOCKED`). The run dialog
  (`RunDialog.tsx`) titles the report **Question** when the run's
  verdict outcome is `blocked`, else as today.
- **Agents** (`EntryPane.tsx`): when `entry.loopRun?.attention` is set,
  the meta line shows a `Chip` `color="warning"` labelled `Blocked` or
  `Needs attention`, title `A run of loop <loop>; shown until it's
  resolved`, and the Hide/Unhide button and menu item are not rendered.
  When `entry.loopRun` is set (any attention), the meta line also shows
  an outlined `Chip` `Loop <loop>`. `EntryRow.tsx`: the summary line is
  prefixed with `Blocked · ` or `Needs attention · ` for a raised run.
- **Phone width**: the switch row is at least 40px tall under
  `@media (max-width: 720px)`.

### Tauri mock (`desktop/test/tauri-mock.js`) and screenshot data

- `data.entries` adds two hidden loop runs:
  - `loop-deps-bump-2b3c4d5e`: `hidden: false` (effective),
    `status: "needsInput"`, `loopRun: { loop: "deps-bump", attention:
    "blocked" }`, `detail: "Which major version of eslint should I target?"`.
  - `loop-status-check-7c8d9e0f`: `hidden: false`, `status:
    "needsInput"`, `loopRun: { loop: "status-check", attention:
    "needs_attention" }`.
  - and one resolved run, `loop-deps-bump-9f8e7d6c`: `hidden: true`,
    `status: "done"`, `loopRun: { loop: "deps-bump", attention: null }`.
  Every other entry gets `loopRun: null`.
- Every loop in `data.loops` gets `hidden` (`deps-bump`: `true`; any
  other: `false` for at least one, to show both rows).
- `deps-bump`'s history adds, newest first, seq 58 `blocked` (verdict
  `BLOCKED`, outcome `blocked`, report path set, workspace
  `loop-deps-bump-2b3c4d5e`, `finishedAt: null`, `durationSeconds:
  null`) and seq 57 `needs_attention` (no verdict). `loops_history_run`
  for seq 58 returns `reportText: "# Question\n\nWhich major version of
  eslint should I target? v9 needs a flat config.\n"`.
- `loops_add` / `loops_edit` echo `hidden` from the request (default
  `true`).

### Screenshots (taken after the last UI task)

| File (under `desktop/docs/`) | View |
| --- | --- |
| `agents-loop-blocked.png` | 1280 × 800, Agents, the blocked `loop-deps-bump-2b3c4d5e` selected |
| `loops-history-blocked.png` | 1280 × 800, Loops → deps-bump, History card with Blocked and Needs attention |
| `loops-edit-hidden.png` | 1280 × 800, Loops → deps-bump → Edit, scrolled to the Agent section with the switch |
| `mobile-agents-loop-blocked.png` | 390 × 844, Agents list with the two raised runs |
