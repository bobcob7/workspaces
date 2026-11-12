# Loop visibility and blocked runs: implementation tasks

Implements [ADR 0057](../../adrs/0057-loop-visibility-and-blocked-runs.md)
to the contract in [reference.md](reference.md). Each task builds, passes
its tests, and can be merged on its own. Each task is one commit on its
own branch (conventional commit, imperative mood, for example
`feat: add loop attention and the hidden setting (ADR 0057)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | `internal/loop` + `internal/machine`: `hidden`, record v3, attention, history rules, `BLOCKED` outcome | nothing | medium | T4, T7 |
| T2 | CLI: `ws loop add/edit --hidden/--show`, loop JSON `hidden`, hidden runs, loop guides | T1 | medium | T3, T4, T5, T6, T7 |
| T3 | CLI: `ws machine result BLOCKED`, `ws list` `loop_run` and visibility, their guides | T1 | medium | T2, T4, T5, T6, T7 |
| T4 | Desktop bridge: Rust model and loops, TS types, fixtures, Tauri mock | nothing (contract in reference.md) | medium | T1, T2, T3, T7 |
| T5 | Tray and Web Push: one notification per raised run | T4 | medium | T2, T3, T6, T7 |
| T6 | Desktop UI: loop form switch, Runs row, History badges, Agents chips | T4 | medium | T2, T3, T5, T7 |
| T7 | Docs: ADR index, tools pages, state.md, tray design, READMEs | nothing (contract in reference.md) | small-ish | everything |

Two chains: Go (T1 → T2 ∥ T3) and desktop (T4 → T5 ∥ T6). T2 and T3
touch disjoint files (`loop*.go` vs `machine_result.go`/`list.go`), so
T2 ∥ T3. T5 touches only `desktop/src-tauri/src/tray/` and
`online/push.rs`; T6 only `desktop/src/features/`; so T5 ∥ T6. T7 touches
only `docs/`, `README.md` and `desktop/README.md`. No task is `small`
in the strict sense (each touches more than two files); T7 is the
cheapest and can be reviewed by Haiku.

**Read before any task:** the "Decision" section of ADR 0057 and the
parts of `reference.md` your task names. When this file and those two
disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style (a short doc comment on every exported or `pub` item, no other
  comments unless the logic isn't obvious), file layout, test style.
  Prefer early returns. Keep functions small.
- **Go** (`internal/`): wrap errors with `fmt.Errorf("doing x: %w", err)`,
  lower-case, no "failed to". Tests: testify `require` for
  preconditions, `assert` for checks; one `TestX` per function or method
  with `t.Run` subtests per behaviour; table-driven only for 3+ trivial
  cases; `t.Context()`, `t.TempDir()`; `t.Parallel()` only on tests
  that touch the filesystem. Run `make fmt`; `make lint` must pass.
  Never call `exec.Command`. `internal/loop` must not import
  `internal/cli`, `internal/machine` or `internal/lifetime`.
- **JSON**: exactly the shapes in `reference.md`. Don't change any
  existing field, error code or exit code.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean, `cargo clippy
  --no-default-features --features online --all-targets -- -D warnings`
  clean; unit tests in the file's `#[cfg(test)] mod tests`.
- **TypeScript** (`desktop/src/`): strict, `readonly` fields, no `any`;
  components are `const X = (props): JSX.Element =>`; styles in a CSS
  module. Tests: vitest, `@testing-library/react`, `user-event`, helpers
  in `desktop/src/test/fixtures.ts` and `desktop/src/test/render.tsx`.
  Query by role and accessible name. **No new npm or cargo dependencies.**
- **Phone width:** every new button, switch row, row action and link is
  at least 40px tall (`min-height: 40px`) under
  `@media (max-width: 720px)`, as `SettingsPane.module.css` does.
- Don't touch files a task doesn't list, except to fix a compile error
  the task caused (say so in your report).

## T1. `internal/loop` and `internal/machine`: the model

**Depends on:** nothing. **Parallel:** T1 ∥ T4 ∥ T7.

**Files:** edit `internal/loop/loop.go`, `internal/loop/loop_test.go`,
`internal/loop/store.go`, `internal/loop/store_test.go`,
`internal/loop/edit.go`, `internal/loop/edit_test.go`,
`internal/loop/history.go`, `internal/loop/history_test.go`,
`internal/machine/result.go`, `internal/machine/result_test.go`,
`internal/cli/loop_tick_test.go` (only the subtest "an idle run with no
record is done": rename it "an idle run with no record needs attention",
expect `loop.StatusNeedsAttention` and `FinishedAt` nil); new
`internal/loop/attention.go`, `internal/loop/attention_test.go`.

Read first: reference.md sections "Loop record", "Attention",
"History" (all of it), "Machine result" (the Go part only); then
`internal/loop/loop.go`, `store.go` (`readLoop`, `hasVersion2Keys`,
`Create`), `edit.go` (`applyTo`), `history.go` (`Settle`, `close`,
`recordVerdict`, `setState`), `internal/machine/result.go` (`OutcomeOf`).

### `loop.go`

- `FormatVersion = 3`; add `Version2 = 2` with a doc comment ("the record
  version of loops added before ADR 0057: no `hidden`").
- `Loop` gains, as its last field before `Runs` or after `LastGate`:
  `Hidden *bool \`toml:"hidden,omitempty"\`` with the doc comment in
  reference.md.
- `func (l *Loop) RunsHidden() bool`: `l.Hidden == nil || *l.Hidden`.

### `store.go`

- `readLoop`: after the version 1 check, `if l.Version < 3 && l.Hidden !=
  nil` → `fmt.Errorf("parsing %s: version %d record has version 3 keys",
  path, l.Version)`.
- `Create`: after `l.Version = FormatVersion`, `if l.Hidden == nil {
  hidden := true; l.Hidden = &hidden }`.

### `edit.go`

- `Edit` gains `Hidden *bool // whether runs start hidden (ADR 0057)`;
  `Empty` includes `e.Hidden == nil`.
- `applyTo`: after the existing version 1 bump, call a new
  `applyHidden(c *Loop, e *Edit) error` when `e.Hidden != nil`, which
  implements the rules in reference.md "Loop record" (v2 → 3; v1 → 3
  unless the prompt contains `{{` and none of prompt/script/vars is
  being edited, then the exact `ErrInvalid` message there).

### `attention.go`

`Attention`, the three constants, `VerdictBlocked`, and `AttentionOf`,
exactly as reference.md "Attention".

### `history.go`

- Add `StatusBlocked`, `StatusNeedsAttention`.
- Update `Verdict.Outcome`'s comment to `"pass", "fail" or "blocked"`.
- Rewrite `Settle`, `close` and `recordVerdict` to the rules in
  reference.md "History status rules". Keep `setState` and `sameTime`.
  Suggested shape: `close(now)` handles rule 2; `recordVerdict` returns
  whether it was terminal (pass/fail); a new `settleState(state string,
  now time.Time) bool` handles rule 5. Update `Settle`'s doc comment to
  point at ADR 0057.

### `internal/machine/result.go`

`OutcomeBlocked`, `VerdictBlocked` and the `OutcomeOf` change from
reference.md "Machine result". Update `Outcome`'s doc comment ("pass,
fail, or blocked").

### Acceptance

- `TestAttentionOf`: table with every row of the reference table
  (`working`+`BLOCKED` → none, `starting`+`""` → none, `idle`+`BLOCKED`
  → blocked, `stopped`+`BLOCKED` → blocked, `unknown`+`BLOCKED` →
  blocked, `idle`+`""` → needs_attention, `stopped`+`""` → none,
  `idle`+`PASS` → none, `idle`+`FAIL` → none, `""`+`""` → none).
- `TestEntry_Settle` keeps its existing cases that still hold and is
  updated for the new ones: idle with no verdict → `needs_attention`,
  `FinishedAt` nil; stopped with no verdict → `done` with
  `FinishedAt = now`; new `BLOCKED` verdict while working → verdict
  stored, `running`, changed; then idle → `blocked`; then working →
  `running`; then idle again (same verdict) → `blocked`; then a `PASS`
  verdict → `succeeded`, `FinishedAt = RecordedAt`; unlisted while
  `needs_attention` → closed, `done`; unlisted while `blocked` → closed,
  still `blocked`, `FinishedAt` set; unlisted while `running` → `gone`;
  a stored `PASS` and idle → no change.
- `TestStore_Get` (or the existing read test): a version 2 record with
  `hidden = false` is refused with "version 2 record has version 3
  keys"; a version 2 record without it reads with `RunsHidden() ==
  true`; a version 3 record with `hidden = false` reads with
  `RunsHidden() == false`.
- `TestStore_Create`: the written file has `version = 3` and
  `hidden = true` when `Hidden` was nil, and keeps `false` when given.
- `TestLoop_Apply`: `Hidden` on v2 → v3; on v1 with a plain prompt → v3;
  on v1 with `{{` in the prompt and no `Prompt` edit → `ErrInvalid`,
  loop unchanged; on v1 with `{{` and a `Prompt` edit → v3. `Edit{Hidden:
  &f}.Empty()` is false.
- `TestOutcomeOf`: `BLOCKED` with a nil spawn → `blocked`; with a spawn
  whose lists don't contain it → `fail`.

### Verify

```sh
go test ./internal/loop/... ./internal/machine/... && go test ./internal/... && make lint
```

`go test ./internal/...` must pass. The one `internal/cli` test known
to change is listed under Files; if another breaks because of the new
statuses, update only its expectation and say so in your report. Change
no `internal/cli` code.

## T2. CLI: hidden runs and the loop flags

**Depends on:** T1. **Parallel:** T2 ∥ T3, T4, T5, T6, T7.

**Files:** edit `internal/cli/loop.go`, `internal/cli/loop_test.go`,
`internal/cli/loop_edit.go`, `internal/cli/loop_edit_test.go`,
`internal/cli/loop_tick.go`, `internal/cli/loop_tick_test.go`,
`internal/cli/loop_history.go`, `internal/cli/loop_history_test.go`,
`internal/cli/guides/ws-loop.md`, `internal/cli/guides/ws-loop-add.md`,
`internal/cli/guides/ws-loop-edit.md`, `internal/cli/guides/ws-loop-list.md`,
`internal/cli/guides/ws-loop-history.md`.

Read first: reference.md "CLI" → "ws loop add / edit", "Loop JSON",
"ws loop run / tick", "ws loop history", "Prompt guidance (guides)";
then `internal/cli/loop.go` (`newLoopAddCmd`, `runLoopAdd`, `loopJSON`,
`loopJSONOf`), `loop_edit.go` (`loopEditOf`, `checkLoopEditFlags`,
`loopEditFields`), `loop_tick.go` (`startLoopRun`), `loop_history.go`
(`historyRunJSONOf`, duration).

### Changes

- `loopAddOptions` and `loopEditOptions` gain `hidden, show bool`.
  `add`: `cmd.Flags().BoolVar(&opts.hidden, "hidden", true, …)` and
  `BoolVar(&opts.show, "show", false, …)` with the help texts in
  reference.md. `edit`: same flags (`hidden` default false; only
  `Changed` matters).
- A shared helper in `loop.go`:
  ```go
  // hiddenFlag is the value --hidden or --show gives, and whether either
  // was given. Both is a usage error.
  func hiddenFlag(changed func(string) bool, hidden, show bool) (value, given bool, err error)
  ```
  `--show` given → `(false, true)`; `--hidden` given → `(hidden, true)`;
  neither → `(true, false)`; both → `usageErrorf("give --hidden or
  --show, not both")`.
- `runLoopAdd`: set `l.Hidden = &value` (always, given or not).
- `loopEditOf`: when given, `e.Hidden = &value`; the "nothing to change"
  check then counts it (via `Edit.Empty`).
- `loopJSON` gains `Hidden bool \`json:"hidden"\`` after `Paused`;
  `loopJSONOf` sets `l.RunsHidden()`.
- `startLoopRun`: `Hidden: l.RunsHidden()` in `CreateOptions`.
- `loop_history.go`: `duration_seconds` also for status `blocked` with
  `FinishedAt` set (reference.md "ws loop history"). No other change.
- Guides: the prompt-guidance section (verbatim from reference.md) in
  `ws-loop.md` and `ws-loop-add.md`, a one-sentence link to it in
  `ws-loop-edit.md`; `--hidden` / `--show` in the synopsis and Flags of
  `ws-loop-add.md` and `ws-loop-edit.md`; `"hidden": true` in every loop
  JSON example in `ws-loop-add.md`, `ws-loop-edit.md`, `ws-loop-list.md`
  with one line explaining it; `blocked` and `needs_attention` (and the
  new meaning of `done`) in `ws-loop-history.md`'s status list.

### Acceptance

- `ws loop add --json --every 1h --prompt p x` prints `"hidden": true`,
  and the record has `version = 3`, `hidden = true`.
- `--show` and `--hidden=false` give `"hidden": false`; `--show
  --hidden` is `usage` (exit 2).
- `ws loop edit --show x` on a version 2 record (written by the test
  directly, no `hidden` key) sets `hidden = false`, `version = 3`;
  `ws loop edit --hidden x` alone is a valid edit (not "nothing to
  change").
- A tick or `ws loop run` creates the run's workspace with
  `Hidden == true` for a default loop and `false` for a `--show` loop
  (assert on the fake workspace store's `CreateOptions`, as the existing
  `loop_tick_test.go` start tests do).
- `ws loop list --json` of a version 2 record shows `"hidden": true`.
- History JSON of a closed blocked run has `duration_seconds` set.
- `TestHiddenFlag` covers the four cases.

### Verify

```sh
go test ./internal/cli/... && go test ./internal/... && make lint
```

## T3. CLI: `ws machine result BLOCKED` and `ws list` `loop_run`

**Depends on:** T1. **Parallel:** T3 ∥ T2, T4, T5, T6, T7.

**Files:** edit `internal/cli/machine_result.go`,
`internal/cli/machine_result_test.go`, `internal/cli/list.go`,
`internal/cli/list_test.go`, `internal/cli/guides/ws-machine-result.md`,
`internal/cli/guides/ws-list.md`.

Read first: reference.md "Machine result" (the CLI part), "ws list",
and the `ws-machine-result.md` block in "Prompt guidance (guides)";
then `internal/cli/machine_result.go` (`runMachineResult`),
`internal/cli/list.go` (`runList`, `visibleEntries`, `listWorkspaces`,
`listWorkspace`, `reviewRecord`, `printList`, `nameText`),
`internal/loop/data.go` (`ReadRunMark`), `internal/cli/app_test.go`
(`withLoopData`), and the existing tests in both test files.

### `machine_result.go`

After `NormalizeVerdict`, when `v == machine.VerdictBlocked`, call a new
`func (a *App) checkBlocked(caller, root, file string) error` that
applies the three rules in reference.md in that order. Everything else
is unchanged.

### `list.go`

- `listLoopRunJSON`, `listWorkspaceJSON.LoopRun` (after `Review`),
  `loopRunInfo`, and the new `visibleEntries` signature, as reference.md
  "ws list".
- `runList` computes `raised := func(e *lifetime.Entry) bool { info :=
  a.loopRunInfo(e); return info != nil && info.Attention != nil }` and
  passes it. To avoid reading twice, cache `loopRunInfo` results by
  workspace id in a map local to `runList`, and pass the same map to
  `listWorkspaces` (add a `loopRuns map[string]*listLoopRunJSON`
  parameter, or a small cache type; `machine.go`'s call passes `nil`,
  and `listWorkspaces` then calls `loopRunInfo` itself).
- `loopRunInfo`: `a.loopData.ReadRunMark(id)`; not found → nil; error →
  warning, nil. Then `machine.NewStore(a.paths.State).Read(id, id)`;
  error → warning, verdict `""`. Then `loop.AttentionOf`.
- Text output: replace the name cell's `(hidden)` suffix with
  `(blocked)` / `(needs attention)` when attention is set (extend the
  existing `nameText` helper with an attention argument, or add
  `listNameText`).

### Guides

- `ws-machine-result.md`: the BLOCKED paragraph and the outcome note
  from reference.md; add `BLOCKED` to the examples:
  `$ ws machine result BLOCKED --file ../question.md`.
- `ws-list.md`: `loop_run` in the JSON example (`null` on the existing
  example, plus one sentence and a short example object), and the rule
  that a hidden loop run with attention is listed without `-a` and not
  counted in `hidden_count`.

### Acceptance

- `ws machine result BLOCKED --file q.md` from a loop run root (test:
  write a run mark with `withLoopData(...).WriteRunMark`) succeeds with
  `"outcome": "blocked"`; without `--file` → `result_invalid`; from a
  workspace with no run mark → `result_invalid`; from a child of a run
  (parent set) → `result_invalid`; `blocked` (lower case) normalises to
  `BLOCKED` and follows the same rules.
- `ws list --json`: a workspace without a run mark has `"loop_run":
  null`; a run with `BLOCKED` recorded and an idle session has
  `{"loop": "deps-bump", "attention": "blocked"}`; idle with no result
  → `needs_attention`; working → `"attention": null`; `PASS` → null.
- Without `-a`: a hidden run with attention is listed and
  `hidden_count` excludes it; a hidden run without attention is left
  out and counted.
- Text: the blocked run's name reads `loop-deps-bump (blocked)`.
- `TestVisibleEntries` covers all/raised/plain hidden.

### Verify

```sh
go test ./internal/cli/... && go test ./internal/... && make lint
```

## T4. Desktop bridge: model, loops, types, fixtures, mock

**Depends on:** nothing (contract in reference.md). **Parallel:** T4 ∥ T1, T2, T3, T7.

**Files:** edit `desktop/src-tauri/src/model.rs`,
`desktop/src-tauri/src/loops.rs`, `desktop/src-tauri/src/tray/attention.rs`
(only the `testing::entry` helper: add `loop_run: None`),
`desktop/src/api.ts`, `desktop/src/test/fixtures.ts`,
`desktop/test/tauri-mock.js`.

Read first: reference.md "Desktop (Tauri) and Workspace Online" →
"Rust model", "Rust loops", "TypeScript", "Tauri mock"; then
`model.rs` (`WsWorkspace`, `Entry`, `workspace_entry`, `session_entry`,
its tests), `loops.rs` (`Loop`, `NewLoop`, `add_args`, `edit_args`, their
tests), `api.ts` (`Entry`, `Loop`, `NewLoop`, `LoopRunStatus`,
`LoopRunVerdict`), `fixtures.ts` (`entry`, `loop`), and `tauri-mock.js`
(`data.entries`, `data.loops`, the history data, `loops_add`,
`loops_edit`, `loops_history_run`).

### Changes

- Rust exactly as reference.md. `Status` stays as is.
- `api.ts`: the types in reference.md "TypeScript". Doc comments on new
  items.
- `fixtures.ts`: `entry()` defaults `loopRun: null`; `loop()` defaults
  `hidden: true`. Fix any test file that builds an `Entry` or `Loop`
  literal without the fixture helpers only if `npm run typecheck`
  fails (say so in your report).
- `tauri-mock.js`: the data in reference.md "Tauri mock". Keep every
  existing entry, loop and history row.

### Acceptance (tests)

- `model.rs`: `merge_reads_loop_run` (a `ws list` JSON with
  `"loop_run": {"loop":"deps-bump","attention":"blocked"}` and
  `"hidden": true` gives `hidden == false`, `loop_run` set, and an idle
  session without a job state gives `Status::NeedsInput`); a hidden run
  with `"attention": null` stays hidden and `Idle`; `loop_run` missing
  → `None`; serialization of `Entry` has `"loopRun": {"loop":
  "deps-bump", "attention": "blocked"}`.
- `loops.rs`: `Loop` without `hidden` deserializes to `hidden: true`;
  `add_args` and `edit_args` with `hidden: Some(false)` contain
  `--hidden=false` before the script flags; with `None` contain no
  `--hidden`.
- `npm run typecheck` passes; existing vitest suites pass.

### Verify

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --no-default-features --features online --all-targets -- -D warnings && cargo test --no-default-features --features online
cd desktop && npm run typecheck && npm test -- --testTimeout=60000
```

## T5. Tray and Web Push: one notification per raised run

**Depends on:** T4. **Parallel:** T5 ∥ T2, T3, T6, T7.

**Files:** edit `desktop/src-tauri/src/tray/attention.rs`,
`desktop/src-tauri/src/tray/watcher.rs`,
`desktop/src-tauri/src/tray/view.rs`,
`desktop/src-tauri/src/online/push.rs`.

Read first: reference.md "Tray and notifications"; then all four files
and `docs/design/tray/README.md` "Behavior" and "Notification text".
`model.rs`'s `Entry.loop_run` (from T4) is `Option<WsLoopRun>` with
`attention: Option<String>`.

### Changes

As reference.md: the two `Attention` variants (handle them in every
`match`: `snippet` treats them like `NeedsInput`), `Candidate.loop_raised`
(set in `candidates`, and in every test helper that builds a
`Candidate`), `loop_attention`, `LoopSnapshot`, `loop_step`, the `due`
change in `step`, `Inner.loop_snapshot` and the merged events in
`Watcher::observe`, `wanted`, the two `notice` texts, and the push
payload kind `"loop"`. If `menu.rs` (desktop feature only) matches on
`Attention`, add arms there too; check with
`cargo clippy --all-targets -- -D warnings` if the desktop feature
builds in your environment, otherwise by reading it.

### Acceptance (tests)

- `loop_step`: first listing with a raised run → no event, key in
  `notified`; a run raised in the second listing → no event at 0 ms,
  one `Blocked` event once `DWELL_MS` has passed, none afterwards;
  a run that goes from `needs_attention` to `blocked` after being
  notified → no event; a raised run that is resolved and raised again
  → no event (no repeats); `needs_attention` gives `RunNeedsAttention`;
  an entry without a workspace id or without `loop_run` → nothing.
- `step`: a `loop_raised` candidate in `NeedsInput` past the dwell
  produces no regular event.
- `Watcher::observe` returns the loop event in `events` after the
  regular ones.
- `wanted`: loop events pass with `needs_input` on and are dropped with
  it off or with `enabled` off.
- `notices`: the two titles and default bodies; with a snippet, the
  snippet is the body.
- `push::payloads`: a `Blocked` event's tag is `loop:<key>` and url
  `/?agent=<key>`.

### Verify

```sh
cd desktop/src-tauri && cargo fmt --check && cargo clippy --no-default-features --features online --all-targets -- -D warnings && cargo test --no-default-features --features online
```

## T6. Desktop UI

**Depends on:** T4. **Parallel:** T6 ∥ T2, T3, T5, T7.

**Files:** edit `desktop/src/features/loops/formValues.ts`,
`formValues.test.ts`, `LoopForm.tsx`, `LoopForm.test.tsx`,
`LoopsPane.tsx`, `LoopsPane.test.tsx`, `LoopsPane.module.css` (the CSS
module both `LoopsPane.tsx` and `LoopForm.tsx` import: the switch row's
class and its 40px phone rule), `history.ts`, `history.test.ts`,
`RunDialog.tsx`, `HistoryCard.test.tsx`; `desktop/src/features/agents/EntryPane.tsx`,
`EntryPane.test.tsx`, `EntryRow.tsx`, `Sidebar.test.tsx`.

Read first: reference.md "Desktop UI" and "TypeScript"; then the files
above and `desktop/src/test/fixtures.ts`.

### Changes

As reference.md "Desktop UI". In `LoopForm.tsx`, import `Switch` and
`FormControlLabel` from `@mui/material` (already a dependency); the
helper text is a `FormHelperText` under it, or the label's secondary
line, whichever matches the form's existing helper style. `patch("hidden",
checked)`.

### Acceptance (tests)

- `formValues.test.ts`: `emptyValues.hidden` is true; `valuesOf` copies
  `hidden`; `requestOf` sends `hidden` true and false.
- `LoopForm.test.tsx`: the switch is found by role `switch` and name
  `Hide runs in Agents`, is on for a new loop, off when editing a loop
  with `hidden: false`, and toggling it changes the submitted request.
- `LoopsPane.test.tsx`: Agent card shows `Hidden unless blocked` for a
  hidden loop and `Shown in Agents` otherwise.
- `history.test.ts`: labels `Blocked`, `Needs attention`; colour
  `warning` for both.
- `HistoryCard.test.tsx` (or a RunDialog test): a blocked run's dialog
  shows the heading `Question`.
- `EntryPane.test.tsx`: an entry with `loopRun: { loop: "deps-bump",
  attention: "blocked" }` and `hidden: false` shows chips `Blocked` and
  `Loop deps-bump`, and no `Hide`/`Unhide` button; `attention: null`
  shows `Loop deps-bump` and the usual Hide button.
- `Sidebar.test.tsx`: a raised run (fixture `hidden: false`, status
  `needsInput`, `loopRun.attention: "blocked"`) is in the `Needs input`
  group with summary text starting `Blocked · `.
- Phone width: the switch's row has `min-height: 40px` under
  `@media (max-width: 720px)` (check the CSS rule exists; the screenshot
  step measures it).

### Verify

```sh
cd desktop && npm run typecheck && npm test -- --testTimeout=60000
```

## T7. Docs

**Depends on:** nothing (contract in reference.md). **Parallel:** T7 ∥ every task.

**Files:** edit `docs/adrs/README.md`, `docs/tools/ws-loop.md`,
`docs/tools/ws-list.md`, `docs/tools/ws-machine.md`, `docs/state.md`,
`docs/design/tray/README.md`, `docs/design/hidden-workspaces/README.md`,
`README.md`, `desktop/README.md`.

Read first: ADR 0057, reference.md (all), and each file above.

### Changes

- `docs/adrs/README.md`: a row for 0057 after 0056, in the table's
  style: "Loop runs are hidden by default (per loop); a run's `BLOCKED`
  verdict or an idle run with no verdict shows it in the Agents list and
  notifies once" | Proposed.
- `docs/tools/ws-loop.md`: `--hidden`/`--show`, `"hidden"` in JSON, the
  new history statuses, and the prompt-guidance section from
  reference.md.
- `docs/tools/ws-list.md`: `loop_run` and the visibility rule.
- `docs/tools/ws-machine.md`: `BLOCKED` under `result`.
- `docs/state.md`: the `loops/<name>.toml` row mentions `hidden`
  (version 3, ADR 0057); the `state/<id>/loop.toml` row says it also
  marks the workspace for `ws list`'s `loop_run`.
- `docs/design/tray/README.md`: a short "Loop runs (ADR 0057)" section:
  raised runs, the loop step, one notification per run, first listing
  seeds, texts.
- `docs/design/hidden-workspaces/README.md`: replace the out-of-scope
  bullet "Notifying about a hidden agent that needs input." with a
  pointer to ADR 0057 for loop runs.
- `README.md`: in the loops section, one paragraph: runs hidden by
  default, `--show`, `BLOCKED`, needs attention, notification.
- `desktop/README.md`: in the Loops screenshots area, a new table row
  pair linking exactly these files (they don't exist yet; the
  screenshot step creates them with these names):
  `docs/agents-loop-blocked.png`, `docs/loops-history-blocked.png`,
  `docs/loops-edit-hidden.png`, `docs/mobile-agents-loop-blocked.png`,
  each with a one-line caption.

### Acceptance

- Every link resolves (relative paths checked by hand or with `grep`),
  no file outside the list changes, and the four PNG names are exactly
  those above.

### Verify

```sh
git diff --stat main -- . ':!docs' ':!README.md' ':!desktop/README.md'   # must be empty
make lint
```

## Screenshot step (orchestrator or a screenshot child, after T6)

Run Vite against `desktop/test/tauri-mock.js` and save exactly the four
files listed in reference.md "Screenshots" under `desktop/docs/`, the
names `desktop/README.md` links (T7). Report named layout checks: the
switch row and every new chip/button at least 40px tall at 390 × 844,
no horizontal scroll, the Blocked chip not clipped, the raised runs
counted in the Agents rail badge and the window title.
