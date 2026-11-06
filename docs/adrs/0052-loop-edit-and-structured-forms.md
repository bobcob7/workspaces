# 0052. A loop is changed in place with `ws loop edit`, and the desktop app edits it in the same sectioned form that adds it

- Status: Proposed
- Date: 2026-11-06

## Context

### Problem

A loop ([0048](0048-agent-loop-scheduler.md),
[0049](0049-loop-scripts-and-variables.md)) can't be changed. To change
its period, prompt, repos, script, or variables, you `ws loop rm` it and
`ws loop add` it again, which loses its run history, its `last_gate`, and
its schedule. In the desktop app the new-loop form is one flat column of
free-text fields (the period is a free-text "15m, 1h, 24h" box, repos a
space-separated string), and the detail view is a flat `<dl>` where a
long prompt or script wraps across the page with no clear edges, and the
script row is easy to miss. How should a loop be edited, from the CLI and
the app, and how should the app present a loop's settings so they can be
read and changed?

### What a solution needs

1. **Same rules as add.** An edited loop is checked exactly as `ws loop
   add` checks a new one (period, prompt template, keep, script timeout,
   vars, specs), and an edit that fails changes nothing.
2. **Atomic and locked.** An edit can't race `ws loop tick` or another
   edit, and a crash mid-write never leaves a half-written record.
3. **Keeps what wasn't changed**: run history, `created_at`, the paused
   state, and, unless the script changed, `last_gate`.
4. **A predictable schedule** when the period changes.
5. **One form** for add and edit in the app, so the two never drift.
6. **Long text is readable**: the prompt and script are scrollable,
   monospace, labelled blocks, in both the detail view and the form.
7. **No new npm dependencies.**

### Relevant facts

- Every command that changes a loop already goes through
  `changeLoop` in `internal/cli/loop.go`, which takes
  `CONFIG_DIR/loops/.lock`, reads the record, applies a change, and saves
  it with `Store.Save` (write `<name>.toml.tmp`, then rename)[^code].
- A run's workspace is named `loop-<NAME>-<8 hex>`, and its record file is
  `<NAME>.toml`; `tick` matches a loop's past runs to workspaces by name[^code].
- A version 1 record's prompt is literal; a version 2 record's prompt is
  a template[^adr49]. `Store.Save` keeps a record's version.
- The desktop's `NewLoop` request (Rust `loops::NewLoop`) already carries
  every editable field: `every`, `prompt`, `specs`, `model`,
  `permission_mode`, `keep`, `script`, `script_timeout`, `vars`[^code].
- MUI (already a dependency) has `TextField` with `multiline`,
  `minRows`, and `maxRows`, `select` text fields, `Chip`, and `Paper`[^mui].
- The loops list polls `ws loop list` every few seconds while the
  section is shown (`LoopsList.tsx`)[^code].

## Decision

We will add **`ws loop edit <NAME> [OPTIONS] [<SPEC>...]`**, documented in
[`ws loop`](../tools/ws-loop.md#ws-loop-edit):

- **Flags:** `--every`, `--prompt`, `--keep`, `--model`,
  `--permission-mode`, `--script`, `--script-timeout`, `--var` (repeatable),
  and `--no-script`, `--no-vars`, `--no-specs`. Specs given as arguments
  replace the loop's specs. Only what's given changes. `--model ""` and
  `--permission-mode ""` clear those; a blank `--script` is the same as
  `--no-script`. `--var` replaces the whole set of variables (the desktop
  always sends the full set; from a shell, repeat every `--var` you want
  kept).
- **The name can't be edited.** It is the record's file name and the
  prefix of every run's workspace; renaming would orphan past runs.
  `ws loop rm` and `add` remain the way to rename.
- **Paused isn't an edit flag**: `pause` and `resume` already do it.
- **Same checks as add**: the edited record goes through
  `Loop.Validate` (including the prompt template) and the specs through
  `validSpecs`, under the loops lock, then `Store.Save`. Any error leaves
  the record as it was. Giving no change at all is a `usage` error.
- **Schedule:** when the period's duration changes, `next_run_at` becomes
  the last run's `started_at` (or `created_at` if it never ran) plus the
  new period, and if that isn't after now, now plus the new period. So
  shortening a period never starts a run immediately, and lengthening it
  counts from the last run. An unchanged duration (`60m` vs `1h`) leaves
  `next_run_at` alone.
- **Script:** setting a script on a loop that had none gives it the
  default 1m timeout unless `--script-timeout` is given; `--no-script`
  clears the timeout too. Changing or removing the script clears
  `last_gate`, since it described the old script.
- **Version:** a version 1 record stays version 1 unless the edit sets
  `--prompt`, `--script`, `--no-script`, `--var`, or `--no-vars`; then it
  becomes version 2 and its prompt is checked as a template.
- **Output:** the same JSON as `add` (the loop's fields plus
  `"warnings"`), and the same text line.

The logic lives in `internal/loop` as `Edit` and `(*Loop).Apply`, so it
is tested without the CLI.

In the desktop app:

- A **`loops_edit` Tauri command** takes the same `NewLoop` request as
  `loops_add` and runs `ws loop edit` with every field set (and
  `--no-script`, `--no-vars`, `--no-specs` for empty ones), so the form's
  state fully replaces the loop's editable fields.
- **One `LoopForm`**, given an optional loop to edit, is used for both.
  It's grouped into sections: **Schedule** (name, read-only when editing;
  period as a number plus a minutes/hours/days select; keep as a number
  1-50), **Prompt** (multiline, monospace, `minRows` 4, `maxRows` 16),
  **Repos** (specs as chips: type and press Enter or space), **Agent**
  (model and permission-mode selects), **Gate script** (multiline,
  monospace, `minRows` 3, `maxRows` 12; timeout as a number plus a
  seconds/minutes select, shown when there is a script), and
  **Variables** (the existing `VarsEditor`). It checks the period, keep,
  timeout, and variable keys as you type; `ws` still has the last word.
- The **detail view** is a column of outlined cards with the same
  section names. The prompt and script are `<pre>` blocks with a label,
  monospace font, `white-space: pre-wrap`, and a max height of 320px
  with their own scrollbar. The Gate script card is always shown, with
  "No gate script" when there isn't one. An **Edit** button in the
  header opens the form in place of the cards.
- The Tauri mock (`desktop/test/tauri-mock.js`) gets sample loops and
  handles every `loops_*` command, `loops_edit` included, so screenshots
  of the detail view and the edit form can be taken.

### Why this option

1. **Same rules as add**: `Apply` ends in the same `Validate` and the CLI
   in the same `validSpecs`.
2. **Atomic and locked**: it reuses `changeLoop` and `Store.Save`.
3. **Keeps what wasn't changed**: `Apply` only touches the fields given;
   runs, `created_at`, and paused are never touched.
4. **Predictable schedule**: counting from the last run is what a person
   expects ("every 6h, and it last ran 2h ago, so 4h from now"). Its weak
   spot: lengthening a period on a loop that ran long ago schedules it
   one new period from now, not immediately.
5. **One form**: `LoopForm` takes the loop as a prop.
6. **Readable text**: bounded, scrollable blocks in both places.
7. **No new dependencies**: everything is MUI and CSS modules.

The weak spot of `--var` replacing the whole set is shell use: adding one
variable means repeating the others. The desktop, which is the main
editor, always has the full set, and a merge flag can be added later.

## Alternatives

### Keep rm + add

**Pros**

- No new code.

**Cons**

- Loses run history and `last_gate`, and resets the schedule.
- In the app, there's no way to see the old values while typing the new.

### Edit the TOML file by hand (`ws loop edit` opens `$EDITOR`)

**Pros**

- Every field at once, no flags.

**Cons**

- The desktop can't drive it; no validation until the next tick; a
  half-saved file is read by `tick`.

### `--var` merges, with `--unset-var KEY`

**Pros**

- Easier from a shell.

**Cons**

- Two ways to say the same thing, and the desktop would have to diff the
  old and new sets. Can be added later without breaking this.

### Free-text period with presets

**Pros**

- Keeps values like `1h30m` exactly.

**Cons**

- Still easy to type something invalid; the number-plus-unit input shows
  `1h30m` as 90 minutes, which is the same period.

## Consequences

- Changing a loop no longer loses its history. The schedule rule is
  documented in `ws loop edit`'s reference.
- The JSON is unchanged; `edit` prints what `add` prints.
- The desktop form and detail view get new tests (vitest, testing
  library); the mock covers `loops_edit` for screenshots.
- `ws llm loop edit` gets a guide, like every subcommand.
- Unverified: none of the UI has been built yet; the screenshots taken
  from the mock after T6 are the check that the cards and blocks read
  well.

## Addendum: titles

A loop's `name` stays its stable ID: the record file name, the workspace
prefix `loop-<name>`, and the CLI argument, with the same `a-z0-9-` rule.
A new optional `title` is the display name: free text with upper case and
spaces, 1–80 characters after trimming, with no control characters (no
newlines or tabs), and editable later. An unset title is left out of the
TOML record and is `""` in JSON. Displays use the title when it is set and
the name otherwise. `ws loop add --title` and `ws loop edit --title` set it
(`--title ""` clears it), and the prompt template gets `{{.Title}}`, which
is the title or the name. The record's format version doesn't change. The
desktop form asks for the title and derives the ID from it.

## References

[^code]: Read on 2026-11-06 at commit 624089f: `internal/cli/loop.go`, `internal/loop/store.go`, `internal/cli/loop_tick.go`, `desktop/src-tauri/src/loops.rs`, `desktop/src/features/loops/LoopsList.tsx`.
[^adr49]: [0049](0049-loop-scripts-and-variables.md), "Decision": version 1 prompts are literal, version 2 prompts are Go templates.
[^mui]: Read on 2026-11-06: `desktop/src/features/settings/ProfileEditor.tsx` already uses a multiline `TextField` with `minRows={10}` and `maxRows={18}`, and `desktop/src/features/terminals/ReplyBox.tsx` one with `maxRows={6}`, on the MUI version in `desktop/package.json` (`@mui/material` ^9.5.0).
