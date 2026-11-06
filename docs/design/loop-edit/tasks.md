# Loop edit and structured forms: implementation tasks

Implements [0052](../../adrs/0052-loop-edit-and-structured-forms.md) and
[`ws loop edit`](../../tools/ws-loop.md#ws-loop-edit). Each task builds,
passes its tests, and can be merged on its own. Each task is one commit
on its own branch (conventional commit, imperative mood, for example
`feat: add ws loop edit (ADR 0052)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | `internal/loop`: `Edit` and `(*Loop).Apply` | nothing | small | T3, T4 |
| T2 | `ws loop edit` command, guide | T1 | large | T3, T4 |
| T3 | Tauri `loops_edit` command | nothing (the contract is in this file) | small | T1, T2, T4 |
| T4 | Desktop data layer: API, slice, period helpers, form values, mock | nothing (the contract is in this file) | large | T1, T2, T3 |
| T5 | Desktop `LoopForm`: one sectioned form for add and edit | T4 | large | none |
| T6 | Desktop detail view: cards, scrollable blocks, Edit flow | T5 | large | none |

T1 ∥ T3 ∥ T4, and T2 ∥ T3 ∥ T4: their files are disjoint (Go in
`internal/`, Rust in `desktop/src-tauri/`, TypeScript and the mock in
`desktop/src/` and `desktop/test/`). T5 and T6 both touch
`LoopsPane.test.tsx` and `LoopsPane.module.css`, so they run in order.

**Read before any task:** the "Decision" section of
`docs/adrs/0052-loop-edit-and-structured-forms.md` and the
`### ws loop edit` and `## Output` sections of `docs/tools/ws-loop.md`.
When this file and those two disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style (a short doc comment on every exported or `pub` item, no other
  comments unless the logic isn't obvious), file layout, and test style.
  Prefer early returns. Keep functions small.
- **Go** (`internal/`): errors are wrapped with `fmt.Errorf("…: %w",
  err)`, lower-case, no "failed to"; validation errors wrap
  `loop.ErrInvalid`. Tests use `github.com/stretchr/testify/assert` and
  `require`, one `TestX` function per function or method with `t.Run`
  subtests per behavior, and table-driven subtests (`tests := []struct{…}`
  then `for _, tt := range tests { t.Run(tt.name, …) }`) where several
  inputs share one check, as `internal/cli/loop_test.go` and
  `internal/loop/loop_test.go` do. Use `t.Context()` and `t.TempDir()`.
  Run `make fmt`; `make lint` must pass. Don't call `exec.Command`.
- **JSON** (Go CLI output) is unchanged: `ws loop edit` prints exactly the
  shape `add` prints (`loopResult` in `internal/cli/loop.go`). Don't
  change any existing JSON field, error code, or exit code.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean, `cargo clippy
  --all-targets -- -D warnings` clean, unit tests in the file's `#[cfg(test)]
  mod tests`, as the existing ones.
- **TypeScript** (`desktop/src/`): strict types, `readonly` fields on
  interfaces, no `any`. Components are `const X = (props): JSX.Element =>`,
  styles in the feature's CSS module. Tests use vitest with
  `@testing-library/react` and `@testing-library/user-event`, the helpers in
  `desktop/src/test/fixtures.ts` (`loop()`, `fakeApi()`, `testStore()`) and
  `desktop/src/test/render.tsx` (`renderWithStore`). Query by role and
  accessible name (`getByRole("textbox", { name: /Prompt/ })`), not by
  class. **No new npm dependencies**: MUI (`@mui/material`), which is
  already installed, has every component needed.
- Don't touch files a task doesn't list, except to fix a compile error
  the task itself caused (say so in your report).

## Contract shared by T3-T6

The desktop sends one request shape for add and edit. Rust already has
it (`loops::NewLoop`); T4 completes the TypeScript side.

```ts
// desktop/src/api.ts
export interface NewLoop {
  readonly name: string;
  readonly every: string;            // Go duration, e.g. "15m", "1h30m", "48h"
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly keep?: number;            // new in T4
  readonly model?: string;           // new in T4; "" = default
  readonly permissionMode?: string;  // new in T4; "" = default
  readonly script?: string;          // absent = no script
  readonly scriptTimeout?: string;   // Go duration, e.g. "1m", "45s"; only with script
  readonly vars?: readonly LoopVar[];
}
```

Tauri command `loops_edit`, invoked as `invoke("loops_edit", { request })`
with a `NewLoop`, returns a `Loop` (the same camelCase shape as
`loops_add`). It runs:

```
ws loop edit --every=<every> --prompt=<prompt> [--keep=<n>]
  --model=<model or ""> --permission-mode=<mode or "">
  (--script=<script> [--script-timeout=<t>] | --no-script)
  (--var=<k>=<v>... | --no-vars)
  (--no-specs)? -- <name> [<spec>...]
--json   (added by the runner)
```

---

## T1. `internal/loop`: `Edit` and `(*Loop).Apply`

- **Id:** T1. **Depends on:** nothing. **Size:** small. **Parallel with:** T3, T4.

**Read:** `internal/loop/loop.go`, `internal/loop/schedule.go`,
`internal/loop/prompt.go`, `internal/loop/loop_test.go` (for test style).

**Create**

- `internal/loop/edit.go`
- `internal/loop/edit_test.go`

**`edit.go`**

```go
package loop

// Edit is a change to a loop, for ws loop edit (ADR 0052). A nil field is
// left as it is.
type Edit struct {
	Every          *string
	Prompt         *string
	Specs          *[]string          // replaces the specs; an empty slice clears them
	Keep           *int
	Model          *string            // "" clears it
	PermissionMode *string            // "" clears it
	Script         *string            // blank after strings.TrimSpace removes the script
	ScriptTimeout  *string
	Vars           *map[string]string // replaces the vars; an empty map clears them
}

// Empty reports whether e changes nothing.
func (e *Edit) Empty() bool

// Apply changes l by e at now, and validates the result with Validate.
// On any error l is left unchanged. See ADR 0052 for each rule.
func (l *Loop) Apply(e *Edit, now time.Time) error
```

`Apply` works on a copy `c := *l` with `c.Specs = slices.Clone(l.Specs)`
and `c.Vars = maps.Clone(l.Vars)`, applies the steps below in order, calls
`c.Validate()`, and only then sets `*l = c`.

1. **Every**: `d, err := ParseEvery(*e.Every)` (return the error);
   `old, oldErr := l.Interval()`; `c.Every = FormatEvery(d)`. If `oldErr
   != nil || d != old`: `base := l.CreatedAt`, or `l.LastRun().StartedAt`
   when there is a last run; `next := base.Add(d)`; if `!next.After(now)`
   then `next = now.Add(d)`; `c.NextRunAt = next`. This applies to paused
   loops too (`Resume` handles a past `NextRunAt`).
2. **Prompt**: `c.Prompt = *e.Prompt` (Validate refuses a blank one).
3. **Specs**: `c.Specs = slices.Clone(*e.Specs)`; an empty result is `nil`.
4. **Keep**: `c.Keep = *e.Keep`.
5. **Model**, **PermissionMode**: `strings.TrimSpace` of the value.
6. **Script**: if `strings.TrimSpace(*e.Script) == ""`: `c.Script = ""`,
   `c.ScriptTimeout = ""`, `c.LastGate = nil`. Otherwise: if the text
   differs from `l.Script`, `c.LastGate = nil`; `c.Script = *e.Script`
   (not trimmed); if `c.ScriptTimeout == ""` and `e.ScriptTimeout ==
   nil`, `c.ScriptTimeout = FormatScriptTimeout(DefaultScriptTimeout)`.
7. **ScriptTimeout**: if `c.Script == ""`, return
   `fmt.Errorf("%w: script timeout without a script", ErrInvalid)`.
   Otherwise `d, err := ParseScriptTimeout(*e.ScriptTimeout)` and
   `c.ScriptTimeout = FormatScriptTimeout(d)`.
8. **Vars**: `c.Vars = maps.Clone(*e.Vars)`; an empty result is `nil`.
9. **Version**: if `c.Version == Version1` and any of `e.Prompt`,
   `e.Script`, `e.Vars` is non-nil, `c.Version = FormatVersion`.

Name, `Paused`, `CreatedAt`, `Runs`, and (except in step 6) `LastGate` are
never changed.

**Acceptance criteria** (one `t.Run` each in `TestLoop_Apply`, plus
`TestEdit_Empty`; build the loop with a helper like the existing tests'
`validLoop`, with `CreatedAt` and a run at fixed times):

- An empty `Edit` changes nothing; `Empty()` is true only for `&Edit{}`.
- Changing every to a different length from a loop whose last run
  started 2h before now: `24h → 6h` gives `NextRunAt = lastRun+6h`; `6h →
  1h` gives `now+1h` (base+1h is past); no runs → counts from `CreatedAt`.
- `--every 60m` on a `1h` loop keeps `NextRunAt`; `90m` is saved as `1h30m`.
- A bad every (`2m`), blank prompt, keep 0 or 51, bad var key, or a
  prompt using `{{.Vars.X}}` without var X returns an error wrapping
  `ErrInvalid` and leaves the loop equal to a copy taken before
  (`assert.Equal(t, before, l)`).
- Specs replaced; empty slice gives `nil`.
- Model `""` clears it; `" opus "` becomes `opus`.
- Script set on a loop without one gets `ScriptTimeout "1m"`; with
  `ScriptTimeout "45s"` it gets `45s`; changing script text clears
  `LastGate`; the same text keeps it; blank script clears script, timeout,
  and `LastGate`.
- `ScriptTimeout` alone on a loop without a script is `ErrInvalid`;
  `"90s"` on a scripted loop is saved as `1m30s`.
- Vars replaced; empty map gives `nil`; vars that the prompt uses can be
  replaced only together with a prompt that doesn't use the removed key.
- A `Version1` loop stays version 1 for an every-only edit, and becomes
  `FormatVersion` when `Prompt`, `Script`, or `Vars` is set; a version 1
  prompt containing `{{` with a `Vars` edit returns `ErrInvalid`.
- `Runs`, `CreatedAt`, `Paused`, and `Name` are unchanged after a
  successful edit of every field.

**Verify**

```
make fmt
go test ./internal/loop/...
make lint
```

---

## T2. `ws loop edit` command and guide

- **Id:** T2. **Depends on:** T1. **Size:** large (two code files plus
  guides). **Parallel with:** T3, T4.

**Read:** `internal/cli/loop.go` (all of it: `newLoopCmd`,
`newLoopAddCmd`, `runLoopAdd`, `scriptOptions`, `changeLoop`, `writeLoop`),
`internal/cli/args.go` (`validSpecs`, `usageErrorf`),
`internal/cli/loop_test.go` (`runLoop`, `withLoopStore`, the add tests),
`internal/cli/llm.go` (`guideTopics`), `internal/cli/guides/ws-loop-add.md`,
`internal/cli/guides/ws-loop.md`, `internal/cli/guides/ws.md` (the `ws llm
loop` lines), `internal/loop/edit.go` (from T1), and
`docs/tools/ws-loop.md` `### ws loop edit`.

**Create**

- `internal/cli/loop_edit.go`
- `internal/cli/loop_edit_test.go`
- `internal/cli/guides/ws-loop-edit.md`: same layout as
  `ws-loop-add.md` (synopsis, who runs it: "`ws loop edit` is for people
  and the desktop app. Agents don't run it.", `## Flags` naming every flag
  below, `## JSON output`, `## Exit codes`, `## Examples`), with the rules
  from `docs/tools/ws-loop.md`.

**Modify**

- `internal/cli/loop.go`: add `a.newLoopEditCmd(),` after
  `a.newLoopAddCmd(),` in `newLoopCmd`. Nothing else.
- `internal/cli/llm.go`: add `"loop edit",` after `"loop add",` in
  `guideTopics`.
- `internal/cli/guides/ws.md`: add
  `  - \`ws llm loop edit\`: change a loop` after the `loop add` line.
- `internal/cli/guides/ws-loop.md`: add the `ws loop edit` synopsis line
  after the `add` one.
- `internal/cli/guides/ws-loop-add.md`: replace the sentence "There is no
  `ws loop edit`: to change a loop's … add it again." with "To change a
  loop later, use `ws loop edit`."

**`loop_edit.go`**

```go
type loopEditOptions struct {
	every, prompt, model, permissionMode string
	script, scriptTimeout                string
	vars                                 []string
	keep                                 int
	noScript, noVars, noSpecs            bool
}

func (a *App) newLoopEditCmd() *cobra.Command
// Use: "edit [OPTIONS] <NAME> [<SPEC>...]", Short: "Change a loop in place",
// Args: cobra.MinimumNArgs(1), RunE: runE(a.withConfig(… a.runLoopEdit(cmd, args, &opts))).
// Flags: --every, --prompt, --keep, --model, --permission-mode, --script,
// --script-timeout, --var (StringArrayVar), --no-script, --no-vars,
// --no-specs, with help text in the style of add's.

// loopEditOf builds the loop.Edit from the flags that were set
// (cmd.Flags().Changed(name)) and the specs. It returns a usage error for
// conflicting flags or when nothing would change, spec_invalid for a bad
// spec (validSpecs), and loop_invalid for a bad --var (loop.ParseVars,
// wrapped with domainError).
func loopEditOf(cmd *cobra.Command, specs []string, opts *loopEditOptions) (loop.Edit, error)

func (a *App) runLoopEdit(cmd *cobra.Command, args []string, opts *loopEditOptions) error
// edit, err := loopEditOf(cmd, args[1:], opts); then
// l, err := a.changeLoop(cmd.Context(), args[0], func(l *loop.Loop) (bool, error) {
//     return true, l.Apply(&edit, a.now().UTC())
// })
// then a.writeLoop(&l).
```

`loopEditOf` rules:

- `--no-script` with `--script` or `--script-timeout`: usage error.
  `--no-vars` with `--var`: usage error. `--no-specs` with specs: usage.
- `--no-script` sets `Script` to a pointer to `""`. `--no-vars` sets
  `Vars` to a pointer to an empty map. `--no-specs` sets `Specs` to a
  pointer to an empty slice. Specs given set `Specs` to them.
- `--var` given (at least once) sets `Vars` to the parsed map.
- Each other flag sets its field only when `Changed`.
- If the resulting `Edit` is `Empty()`: `usageErrorf("nothing to change:
  give an option or a spec")`.

**Acceptance criteria** (`TestApp_loopEdit`, with subtests; add a loop
first with `runLoop(t, app, stdout, "add", …)`, then edit with `--json`):

- `edit deps --every 6h --json` exits 0 and prints the loop with `every
  "6h"`, the same `created_at`, and `"warnings": []`; the stored record
  (`store.Get`) has the new period and the next run as ADR 0052 says
  (with `testNow()` and no runs: `created_at + 6h`).
- `--prompt`, `--keep 3`, `--model opus`, `--model ""` (prints `null`),
  `--permission-mode acceptEdits`, specs `buff-bear api`, `--no-specs`
  (prints `[]`) each change only their field.
- `--script 'true'` sets the script and `script_timeout "1m"`;
  `--no-script` makes both `null`; `--var A=1 --var B=2` replaces the
  vars; `--no-vars` prints `{}`.
- Text output (no `--json`) is the `loopLine` text.
- Table-driven refusals, each with exit code, empty stdout, and the error
  code in stderr: nothing to change (`usage`, 2); `--no-script --script x`
  (`usage`); `--no-vars --var A=1` (`usage`); `--no-specs buff-bear`
  (`usage`); `--every 2m` (`loop_invalid`, 2); `--keep 51`
  (`loop_invalid`); `--var 1A=x` (`loop_invalid`); `/abs/path` spec
  (`spec_invalid`, 2); `--script-timeout 30s` on a loop with no script
  (`loop_invalid`); a missing loop (`loop_not_found`, 3).
- After a refused edit the stored record is unchanged
  (`assert.Equal(t, before, after)` from `store.Get`).
- `go test ./internal/cli/ -run TestGuideTopics` passes (the guide names
  every flag and documents the JSON).

**Verify**

```
make fmt
go test ./internal/loop/... ./internal/cli/...
make lint
go run ./cmd/ws loop edit --help
```

---

## T3. Tauri `loops_edit` command

- **Id:** T3. **Depends on:** nothing (it shells out to `ws`, and the
  contract is above). **Size:** small. **Parallel with:** T1, T2, T4.

**Read:** `desktop/src-tauri/src/loops.rs` (all of it),
`desktop/src-tauri/src/lib.rs` (the `invoke_handler` list), the
"Contract shared by T3-T6" section above.

**Modify**

- `desktop/src-tauri/src/loops.rs`
- `desktop/src-tauri/src/lib.rs`: register `loops::loops_edit,` after
  `loops::loops_add,`.

**`loops.rs`**

```rust
/// `ws loop edit` without `--json`, which the runner adds. Every field is
/// sent, so the request replaces the loop's editable fields (ADR 0052):
/// ["loop", "edit", "--every=<every>", "--prompt=<prompt>", ("--keep=<n>"),
///  "--model=<m>", "--permission-mode=<p>",
///  ("--script=<s>", ("--script-timeout=<t>")) | "--no-script",
///  ("--var=<k>=<v>")... | "--no-vars", ("--no-specs"),
///  "--", <name>, <specs...>].
/// Values are trimmed as in add_args (not the script or a var's value);
/// a blank model or permission mode is sent as "" to clear it.
pub fn edit_args(req: &NewLoop) -> Vec<String>

#[tauri::command]
pub async fn loops_edit(state: State<'_, AppState>, request: NewLoop) -> CmdResult<Loop> {
    state.runner.ws_json(&edit_args(&request)).await
}
```

Details: `--model=` and `--permission-mode=` are always present (empty
after `=` when blank or `None`). The script counts as set when
`!script.trim().is_empty()`; the timeout is sent only with a script and
only when non-blank. Vars with a blank key are skipped; if none are left,
send `--no-vars`. Blank specs are skipped; if none are left, send
`--no-specs` (before `--`). Reuse `non_blank`.

**Acceptance criteria** (new `#[test]`s in `mod tests`, using `request()`):

- `edit_args(&request())` is exactly `["loop", "edit", "--every=24h",
  "--prompt=hi", "--model=", "--permission-mode=", "--no-script",
  "--no-vars", "--no-specs", "--", "deps"]`.
- A full request (keep 3, model " opus ", permission mode "acceptEdits",
  script "gh issue list | grep .\n", timeout " 2m ", vars REPO="a b" and a
  blank key, specs ["buff-bear", " "]) gives `["loop", "edit",
  "--every=24h", "--prompt=hi", "--keep=3", "--model=opus",
  "--permission-mode=acceptEdits", "--script=gh issue list | grep .\n",
  "--script-timeout=2m", "--var=REPO=a b", "--", "deps", "buff-bear"]`.
- A blank script with a timeout gives `--no-script` and no timeout.
- A `#[tokio::test]` with `fake_ws` that prints a loop JSON (copy the one
  in `list_parses_script_vars_and_last_gate`, without the `{"loops":[`
  wrapper) parses through `runner.ws_json::<Loop>(&edit_args(&request()))`.

**Verify**

```
cd desktop/src-tauri && cargo fmt --check
cd desktop/src-tauri && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test loops
```

---

## T4. Desktop data layer: API, slice, period helpers, form values, mock

- **Id:** T4. **Depends on:** nothing (contract above). **Size:** large
  (several small files, no UI). **Parallel with:** T1, T2, T3.

**Read:** `desktop/src/api.ts` (the `Loop*` and `NewLoop` types and the
`loops*` functions near the end), `desktop/src/features/loops/loopsSlice.ts`,
`desktop/src/features/loops/loopsSlice.test.ts`,
`desktop/src/test/fixtures.ts` (`loop()` and the `loops*` entries in
`fakeApi`), `desktop/src/features/create/NewAgentDialog.tsx` (only the
`models` and `permissionModes` constants), `desktop/test/tauri-mock.js`,
and the contract above.

**Create**

- `desktop/src/features/loops/period.ts`, `period.test.ts`
- `desktop/src/features/loops/formValues.ts`, `formValues.test.ts`

**Modify**

- `desktop/src/api.ts`: add `keep?`, `model?`, `permissionMode?` to
  `NewLoop` (contract above), and
  `loopsEdit: async (request: NewLoop): Promise<Loop> => call<Loop>("loops_edit", { request }),`
  after `loopsAdd`. If `api.ts` declares an `Api` interface or type for
  these functions, add `loopsEdit` there too.
- `desktop/src/test/fixtures.ts`: `loopsEdit: vi.fn(async (request: NewLoop) =>
  loop({ name: request.name, every: request.every, prompt: request.prompt,
  specs: request.specs, script: request.script ?? null, scriptTimeout:
  request.scriptTimeout ?? null, keep: request.keep ?? 5 }))` in `fakeApi`.
- `desktop/src/features/loops/loopsSlice.ts`, `loopsSlice.test.ts`.
- `desktop/test/tauri-mock.js`.

**`period.ts`**

```ts
export type PeriodUnit = "m" | "h" | "d";
export type TimeoutUnit = "s" | "m";
/** A number-plus-unit input's state; amount is the text typed. */
export interface Amount<U extends string> { readonly amount: string; readonly unit: U }

/** Seconds in a Go duration made of h, m, and s parts ("1h30m", "90s", "24h"); null for anything else. */
export const parseGoDuration = (text: string): number | null;
/** A whole number of seconds as ws prints it: "1h30m", "24h", "15m", "1m30s", "45s". */
export const formatGoDuration = (seconds: number): string;

/** A loop's every as the largest unit that divides it: "48h" → 2 d, "90m" → 90 m, "6h" → 6 h. Unparseable → { amount: "1", unit: "h" }. */
export const periodOf = (every: string): Amount<PeriodUnit>;
/** The Go duration for a period, or null unless amount is a whole number ≥ 1 and the total is 5 minutes to 7 days. */
export const everyOf = (period: Amount<PeriodUnit>): string | null;

/** A script timeout like periodOf, in s or m; null or unparseable → { amount: "1", unit: "m" }. */
export const timeoutOf = (timeout: string | null): Amount<TimeoutUnit>;
/** The Go duration for a timeout, or null unless it is a whole number of 1 second to 10 minutes. */
export const scriptTimeoutOf = (timeout: Amount<TimeoutUnit>): string | null;

export const periodUnits: readonly { readonly value: PeriodUnit; readonly label: string }[];
// [{m, "minutes"}, {h, "hours"}, {d, "days"}]
export const timeoutUnits: readonly { readonly value: TimeoutUnit; readonly label: string }[];
// [{s, "seconds"}, {m, "minutes"}]
```

A whole number is `/^\d+$/` and > 0. `d` is 24 hours.

**`formValues.ts`**

```ts
import type { Loop, LoopVar, NewLoop } from "../../api";
import type { Amount, PeriodUnit, TimeoutUnit } from "./period";

/** What LoopForm edits. Text fields hold what was typed. */
export interface LoopFormValues {
  readonly name: string;
  readonly period: Amount<PeriodUnit>;
  readonly keep: string;
  readonly prompt: string;
  readonly specs: readonly string[];
  readonly model: string;           // "" = default
  readonly permissionMode: string;  // "" = default
  readonly script: string;
  readonly scriptTimeout: Amount<TimeoutUnit>;
  readonly vars: readonly LoopVar[];
}

/** Errors to show under each field; a missing key is no error. */
export interface LoopFormErrors {
  readonly name?: string;
  readonly every?: string;
  readonly keep?: string;
  readonly prompt?: string;
  readonly scriptTimeout?: string;
  readonly vars?: string;
}

/** A new loop's values: blank name and prompt, every 1 h, keep "5", timeout 1 m, nothing else. */
export const newLoopValues: LoopFormValues;
/** An existing loop's values, for the edit form. vars in key order. */
export const loopValues: (loop: Loop) => LoopFormValues;
/** What's wrong with values, as messages. */
export const formErrors: (values: LoopFormValues) => LoopFormErrors;
/** Whether formErrors is empty. */
export const isValid: (values: LoopFormValues) => boolean;
/** The request for values that are valid. */
export const requestOf: (values: LoopFormValues) => NewLoop;
```

`formErrors` messages (tests check them exactly):

- name blank: `"Required"`; name not `/^[a-z0-9][a-z0-9-]{0,39}$/`:
  `"1-40 of a-z, 0-9 and -, starting with a letter or digit"`.
- `everyOf(period) === null`: `"A whole number, 5 minutes to 7 days"`.
- keep not a whole number 1-50: `"1 to 50"`.
- prompt blank (after trim): `"Required"`.
- script set (non-blank) and `scriptTimeoutOf(...) === null`:
  `"A whole number, 1 second to 10 minutes"`.
- a var row whose key is non-blank and not `/^[A-Za-z_][A-Za-z0-9_]{0,63}$/`:
  `` `Bad key ${key}: letters, digits and _, not starting with a digit` ``;
  two rows with the same trimmed key: `` `${key} is given twice` `` (first
  problem found wins).

`requestOf`: `name` trimmed; `every` from `everyOf`; `prompt` as typed
(Rust trims it); `specs` as is; `keep: Number(keep)`; `model` and
`permissionMode` as is (may be `""`); `script` and `scriptTimeout` only
when the script is non-blank; `vars` only when at least one row has a
non-blank key (rows with blank keys dropped, keys trimmed, values as is).

**`loopsSlice.ts`**

- State: add `readonly editing: boolean` (the selected loop's edit form is
  shown) and `readonly submitting: boolean` (an add or edit is running);
  both `false` initially.
- `export const editLoop = createAppThunk<Loop, NewLoop>("loops/edit", …)`:
  `extra.api.loopsEdit(request)`, then `void dispatch(fetchLoops())`;
  errors via `rejectWithValue(toAppError(err))`, like `addLoop`.
- Reducers: `editingStarted` (`editing = true`, `creating = false`,
  `formError = null`), `editingCancelled` (`editing = false`, `formError =
  null`). `loopSelected` and `creatingStarted` also set `editing = false`.
- `addLoop.pending` and `editLoop.pending` set `submitting = true`;
  fulfilled and rejected set it back. `editLoop.fulfilled` replaces the
  loop in `loops` by name, sets `editing = false`, `formError = null`;
  `editLoop.rejected` sets `formError` (default message `"editing loop failed"`).
- Export `editLoop`, `editingStarted`, `editingCancelled`.

**`tauri-mock.js`**: replace `loops_list: () => ({ loops: [], warnings:
[] })` with a stateful set of handlers over `data.loops` (add `loops` to
the `data` object the file already has, next to `settings`):

- Two sample loops in the camelCase `Loop` shape (see `loop()` in
  `fixtures.ts` for every field). `deps-bump`: every `"24h"`, a prompt of
  at least 25 lines (so the block scrolls), specs
  `["buff-bear", "api"]`, permissionMode `"acceptEdits"`, keep 5, a
  last run with a workspace, script `"go list -m -u -f '{{if
  .Update}}{{.Path}}{{end}}' all | grep ."` plus a few more lines,
  scriptTimeout `"2m"`, vars `{ BRANCH: "main", LABEL: "deps" }`, a passed
  lastGate. `triage`: every `"1h"`, paused, `nextRunAt: null`, short
  prompt, no specs, no script, no vars, last run with an error.
- `loops_list` returns `{ loops: data.loops, warnings: [] }`.
- `loops_add({ request })` appends a loop built from the request
  (defaults as `loop()`), returns it. `loops_edit({ request })` replaces
  the matching loop's `every`, `prompt`, `specs`, `keep`, `model`,
  `permissionMode` (`""` → `null`), `script` (absent → `null`),
  `scriptTimeout` (absent → `null`, script without one → `"1m"`), `vars`
  (rows → object; absent → `{}`), returns it.
  `loops_set_paused({ name, paused })`, `loops_remove({ name })`,
  `loops_run_now({ name })` act on `data.loops` the obvious way
  (`run_now` returns `{ name, workspace: "loop-<name>-1a2b3c4d", session:
  "e8cdf5d8", path: "/w/loop-<name>-1a2b3c4d" }`).

**Acceptance criteria**

- `period.test.ts` (table-driven with `it.each`): `parseGoDuration` for
  `"15m"`, `"1h30m"`, `"24h"`, `"90s"`, `"1m30s"` and null for `""`,
  `"1d"`, `"abc"`, `"1.5h"`; `formatGoDuration` for 900, 5400, 86400, 90,
  45; `periodOf` for `"48h"` → 2 d, `"90m"` → 90 m, `"6h"` → 6 h, `"15m"`
  → 15 m, `"x"` → 1 h; `everyOf` for 15 m → `"15m"`, 90 m → `"1h30m"`,
  2 d → `"48h"`, 7 d → `"168h"`, and null for 4 m, 8 d, `"0"`, `"1.5"`,
  `""`; `timeoutOf`/`scriptTimeoutOf` for `"2m"`, `"45s"`, `"1m30s"` → 90
  s, null → 1 m, and null for 0 s, 11 m.
- `formValues.test.ts`: `loopValues(loop({ every: "48h", keep: 3, script:
  "true", scriptTimeout: "45s", vars: { B: "2", A: "1" } }))` gives period
  2 d, keep `"3"`, timeout 45 s, vars `[A, B]`; each `formErrors` message
  above; `requestOf` drops blank-key var rows, omits script fields when
  the script is blank, and includes `keep`, `model`, `permissionMode`.
- `loopsSlice.test.ts`: `editLoop` fulfilled replaces the loop, clears
  `editing` and `formError`; rejected sets `formError`; `submitting` is
  true while pending; `editingStarted` then `loopSelected` clears
  `editing`.
- The mock parses: `node --check desktop/test/tauri-mock.js`.

**Verify**

```
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run src/features/loops
node --check desktop/test/tauri-mock.js
```

---

## T5. Desktop `LoopForm`: one sectioned form for add and edit

- **Id:** T5. **Depends on:** T4. **Size:** large. **Parallel with:** none.

**Read:** `desktop/src/features/loops/LoopForm.tsx` (being replaced),
`VarsEditor.tsx`, `LoopsPane.tsx` (only the `LoopsPane` component at the
end), `LoopsPane.module.css`, `LoopsPane.test.tsx` (the four form tests), `period.ts` and `formValues.ts` (from T4),
`desktop/src/features/settings/ProfileEditor.tsx` (lines ~190-210, for a
multiline `TextField` with `minRows`/`maxRows`), and the `models` and
`permissionModes` constants in
`desktop/src/features/create/NewAgentDialog.tsx`.

**Create**

- `desktop/src/features/loops/AmountInput.tsx`: number plus unit select.
- `desktop/src/features/loops/SpecsInput.tsx`: repo specs as chips.
- `desktop/src/features/loops/LoopForm.test.tsx`.

**Modify**

- `desktop/src/features/loops/LoopForm.tsx`: rewrite.
- `desktop/src/features/loops/LoopsPane.tsx`: only the `creating` branch
  of `LoopsPane`, to the new props (below).
- `desktop/src/features/loops/LoopsPane.module.css`: form styles.
- `desktop/src/features/loops/LoopsPane.test.tsx`: update the four
  form tests ("adds a loop from the form and shows it selected", "shows
  the add error in the form", "adds a loop with a script and variables",
  "shows the script timeout only while there is a script")
  to the new inputs (below); leave the detail-view tests to T6.

**Components**

```tsx
/** A whole-number TextField (type="number", inputMode numeric) and a unit select side by side. */
export const AmountInput = <U extends string>(props: {
  readonly label: string;                       // e.g. "Every"; the number field's accessible name
  readonly value: Amount<U>;
  readonly units: readonly { readonly value: U; readonly label: string }[];
  readonly onChange: (value: Amount<U>) => void;
  readonly error?: string;                      // helperText in error state
  readonly helperText?: string;                 // shown when there's no error
}): JSX.Element;
// The unit select is a TextField select with label "Unit" and
// slotProps htmlInput aria-label `${label} unit`.

/** Repo specs as deletable chips. Enter, space, comma, or blur adds the typed spec; Backspace in an empty field removes the last. Duplicates are ignored. */
export const SpecsInput = (props: {
  readonly value: readonly string[];
  readonly onChange: (specs: readonly string[]) => void;
}): JSX.Element;
// A TextField labelled "Repos" with helperText
// "Repo specs, such as buff-bear or github.com/owner/repo@main. Press Enter to add."
// Above it, each spec is an MUI Chip with label=spec and
// onDelete; give the delete a name with
// slotProps or deleteIcon aria-label `Remove ${spec}` (if the MUI version
// makes that awkward, render the chip's delete as an IconButton with that
// aria-label). Pasting "a b" adds both.

export interface LoopFormProps {
  /** The loop to edit; absent for a new loop. */
  readonly loop?: Loop;
  readonly error: AppError | null;
  readonly submitting: boolean;
  readonly onSubmit: (request: NewLoop) => void;
  readonly onCancel: () => void;
}
export const LoopForm = (props: LoopFormProps): JSX.Element;
```

`LoopForm` keeps one `useState<LoopFormValues>` initialised once from
`props.loop === undefined ? newLoopValues : loopValues(props.loop)` (the
pane gives it `key={loop.name}`, so a list poll doesn't reset what's
typed). `errors = formErrors(values)`. Errors are shown only after the
field is changed or after a submit attempt (track a `touched` set and a
`submitted` flag). Submit is disabled while `submitting`; on submit with
errors, set `submitted` and don't call `onSubmit`; otherwise call
`onSubmit(requestOf(values))`.

Layout: `<form aria-label={loop ? "Edit loop" : "New loop"}>` with an
`h2` ("New loop" or `Edit ${loop.name}`), then one `<fieldset>` per
section with a `<legend>` (CSS: no browser border, legend styled as a
small uppercase section title), in this order:

1. **Schedule**: Name (`TextField`, required; `disabled` with
   helperText "A loop's name can't be changed" when editing); Every
   (`AmountInput` with `periodUnits`, helperText "5 minutes to 7 days");
   Keep (`TextField type="number"`, label "Keep runs", `htmlInput`
   min 1 max 50, helperText "How many runs' workspaces to keep").
2. **Prompt**: `TextField multiline minRows={4} maxRows={16}` label
   "Prompt", required, monospace input (CSS class on the input via
   `slotProps.htmlInput.className` or `sx`), helperText "A Go template:
   {{.Vars.KEY}}, {{.Stdout}}, {{.Stderr}}, {{.Name}}".
3. **Repos**: `SpecsInput`.
4. **Agent**: Model and Permission mode, each a `TextField select` over
   `models` / `permissionModes` from `NewAgentDialog.tsx` (label the `""`
   option "Default"). If the loop's current value isn't in the list, add
   it as an extra option so it isn't lost.
5. **Gate script**: `TextField multiline minRows={3} maxRows={12}` label
   "Script", monospace, helperText as the current form's. When the script
   is non-blank, an `AmountInput` labelled "Timeout" with `timeoutUnits`
   (helperText "1 second to 10 minutes").
6. **Variables**: the existing `VarsEditor`, then the vars error, if any,
   as MUI `FormHelperText error`.

Then the `formError` `Alert` (`props.error`), and buttons: Cancel
(`onCancel`) and submit, labelled "Add loop" or "Save changes".

The form element scrolls: `.form { overflow-y: auto; height: 100%;
box-sizing: border-box; }`. Fieldsets are separated by 16-20px; inputs
fill the width; `AmountInput`'s number field ~120px wide next to a
~140px select.

`LoopsPane`'s `creating` branch becomes:

```tsx
<LoopForm
  error={formError}
  submitting={submitting}
  onSubmit={(request) => void dispatch(addLoop(request))}
  onCancel={() => dispatch(creatingCancelled())}
/>
```

(selecting `formError` and `submitting` from `s.loops`).

**Acceptance criteria**

- `LoopForm.test.tsx` (render with `renderWithStore` or plain `render`;
  the form doesn't need the store):
  - New: typing name, every 90 + unit minutes, prompt, repos "buff-bear{Enter}api{Enter}",
    and clicking "Add loop" calls `onSubmit` with `{ name, every: "1h30m",
    prompt, specs: ["buff-bear", "api"], keep: 5, model: "",
    permissionMode: "" }` (use `toHaveBeenCalledWith(expect.objectContaining(...))` only if
    needed; prefer the exact object).
  - Edit: `loop={loop({ every: "48h", script: "true", scriptTimeout: "45s", vars: { A: "1" }, specs: ["buff-bear"] })}`
    shows Name disabled with "deps", Every 2 with unit days, the script,
    Timeout 45 seconds, the var row, a "buff-bear" chip, and the button
    "Save changes"; changing Keep to 3 and saving calls `onSubmit` with
    `name: "deps"`, `every: "48h"`, `keep: 3`, `script: "true"`,
    `scriptTimeout: "45s"`, `vars: [{ key: "A", value: "1" }]`.
  - Every 4 minutes shows "A whole number, 5 minutes to 7 days" and
    clicking submit doesn't call `onSubmit`.
  - Removing a chip ("Remove buff-bear") drops it from the request.
  - The Prompt and Script fields are multiline textboxes (`getByRole("textbox",
    { name: /Prompt/ })` is a `TEXTAREA`).
  - The six section legends are present (`getByRole("group", { name:
    "Schedule" })` etc.; a `fieldset` with a `legend` has role group).
  - `props.error` is shown as an alert; the submit button is disabled
    while `submitting`.
- `LoopsPane.test.tsx`: "adds a loop from the form and shows it selected"
  uses the new inputs (the "Every" number field already holds 1
  and the unit "hours" is the default, so leave it or clear and type "1";
  repos via Enter) and expects `loopsAdd` called with
  `{ name: "nightly", every: "1h", prompt: "Lint the repo.", specs:
  ["buff-bear", "api"], keep: 5, model: "", permissionMode: "" }`; "shows the add
  error in the form" types every 5 minutes (valid client-side) and makes
  `loopsAdd` throw; "adds a loop with a script and variables" expects
  the same request shape plus `script`, `scriptTimeout` (from the Timeout
  input, default `"1m"`), and `vars`; the timeout test looks for the "Timeout" number field
  (`getByRole("spinbutton", { name: /Timeout/ })`). All four pass.
- Note: a `type="number"` field has role `spinbutton`, not `textbox`, so
  query Every, Keep, and Timeout with `getByRole("spinbutton", { name: … })`,
  and a select with `getByRole("combobox", { name: … })` then pick the
  option with `getByRole("option", { name: … })`.

**Verify**

```
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run src/features/loops
```

---

## T6. Desktop detail view: cards, scrollable blocks, Edit flow

- **Id:** T6. **Depends on:** T5. **Size:** large. **Parallel with:** none.

**Read:** `desktop/src/features/loops/LoopsPane.tsx`,
`LoopsPane.module.css`, `LoopsPane.test.tsx`, `LoopForm.tsx` (props only),
`loopsSlice.ts` (`editing`, `submitting`, `editLoop`, `editingStarted`,
`editingCancelled`), `gate.ts`, `desktop/src/format.ts` (`age`, `until`),
and `desktop/README.md` (the Loops screenshots section, around line 135).

**Modify**

- `desktop/src/features/loops/LoopsPane.tsx`
- `desktop/src/features/loops/LoopsPane.module.css`
- `desktop/src/features/loops/LoopsPane.test.tsx`
- `desktop/README.md`: in the Loops part, one sentence saying a loop is
  edited with **Edit** in its header, in the same form that adds it. Don't
  add or change image links (the orchestrator adds screenshots).

**`LoopDetail`**

Header (unchanged layout): the name, a small status label ("Active" or
"Paused", MUI `Chip size="small"`), spacer, then buttons **Edit**,
Pause/Resume, Run now, Delete (all disabled while busy). Edit dispatches
`editingStarted()`.

Below the header, a scrolling body (`.body { overflow-y: auto; flex: 1;
padding: 16px 18px; display: flex; flex-direction: column; gap: 14px; }`)
of cards. Each card is

```tsx
<section className={styles.card} aria-labelledby={id}>
  <h2 id={id} className={styles.cardTitle}>Schedule</h2>
  …
</section>
```

with `.card { border: 1px solid var(--border); border-radius: 8px;
background: var(--panel); padding: 12px 14px; }` and ids from
`useId()`. Cards, in order:

1. **Schedule**: a small `<dl>` (reuse `.rows`): Every (`loop.every`),
   Next run (`nextRun(loop)`), Last run (`<LastRun>`), Keep
   (`` `${loop.keep} runs' workspaces` ``).
2. **Prompt**: `<CodeBlock label="Prompt" text={loop.prompt} />`.
3. **Repos**: the specs as `Chip size="small"` in a wrapping row, or
   "No repos: each run gets an empty workspace".
4. **Agent**: `<dl>` with Model (`loop.model ?? "Default"`) and
   Permission mode (`loop.permissionMode ?? "Default"`).
5. **Gate script**: always shown. With a script: `<CodeBlock
   label="Gate script" text={loop.script} />`, then a `<dl>` with Timeout
   (`loop.scriptTimeout ?? "1m"`) and Last check (`gateText(loop.lastGate)`).
   Without: a muted paragraph "No gate script: every due run starts."
6. **Variables**: a two-column `<table>` (`<th scope="col">` Key and
   Value; monospace cells) of `Object.entries(loop.vars)` sorted by key,
   or "No variables".

```tsx
/** A labelled, monospace, scrollable block of text. */
const CodeBlock = ({ label, text }: { readonly label: string; readonly text: string }): JSX.Element => (
  <pre className={styles.code} aria-label={label} tabIndex={0}>{text}</pre>
);
```

`.code { margin: 0; font-family: var(--font-mono); font-size: 12px; line-height: 1.5; white-space: pre-wrap;
overflow-wrap: anywhere; max-height: 320px; overflow: auto; padding: 10px
12px; border: 1px solid var(--border); border-radius: 6px; background: var(--bg); }` (the
variables are in `desktop/src/index.css`; don't add colours).
`tabIndex={0}` lets the keyboard scroll it.

**Edit flow** in `LoopsPane`:

```tsx
if (creating) { /* T5's add form */ }
if (loop !== undefined && editing) {
  return (
    <LoopForm
      key={loop.name}
      loop={loop}
      error={formError}
      submitting={submitting}
      onSubmit={(request) => void dispatch(editLoop(request))}
      onCancel={() => dispatch(editingCancelled())}
    />
  );
}
if (loop !== undefined) { return <LoopDetail key={loop.name} loop={loop} />; }
```

Remove the old `.prompt` class from the CSS if nothing uses it any more.

**Acceptance criteria** (`LoopsPane.test.tsx`, new `it`s; keep the
existing pause, run, and delete tests passing, and rewrite the two
detail-view script tests, which look for `REPO=x` and
`getByText("Script").nextElementSibling`, to the new cards):

- The detail view of `loop({ script: "go list | grep .", scriptTimeout:
  "2m", vars: { BRANCH: "main" }, model: "opus" })` has regions
  (`getByRole("region", { name: "Schedule" })` etc.; a `section` with
  `aria-labelledby` is a region) for all six cards; the prompt and script
  are in elements named "Prompt" and "Gate script" whose text is the
  loop's; the variables table has a row with `BRANCH` and `main`; Agent
  shows "opus".
- A loop without a script shows the Gate script card with "No gate
  script: every due run starts." and no element named "Gate script"
  other than the heading (query the `pre` with
  `queryByLabelText("Gate script", { selector: "pre" })` and expect null).
- A loop without specs shows "No repos: each run gets an empty workspace".
- Clicking **Edit** shows the form named "Edit loop" with the Name field
  disabled; changing Every to 6 hours and clicking "Save changes" calls
  `api.loopsEdit` with a request whose `name` is `"deps"` and `every` is
  `"6h"`, and then the detail view is back with Every "6h" (have the fake
  `loopsList` return the edited loop).
- An `editLoop` failure (`loopsEdit` throws `{ code: "loop_invalid",
  message: "prompt: …" }`) keeps the form open and shows the message.
- Cancel in the edit form goes back to the detail view without calling
  `loopsEdit`.

**Verify**

```
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run src/features/loops
cd desktop && npx vitest run
```
