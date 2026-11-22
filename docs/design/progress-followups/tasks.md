# Progress follow-ups: implementation tasks

Builds on [ADR 0077](../../adrs/0077-agent-progress-reporting.md) and the
`ws machine progress` command already on this branch
(`internal/machine/progress.go`, `internal/cli/machine_progress.go`). Each
task builds, passes its tests, and is one commit on its own branch
(conventional commit, imperative mood, for example
`feat: let the runner's run tool call ws machine progress`).

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | Runner: allow `ws machine progress` in the bash guard and preamble | nothing | small | T2, T3, T4 |
| T2 | Roles: tell implementer, reviewer and verifier to report progress | nothing | small | T1, T3, T4 |
| T3 | `ws list --json`: add `progress` and `rollup` to each workspace | nothing | medium | T1, T2, T4 |
| T4 | Desktop: carry the two fields through, and draw progress bars | contract below | large | T1, T2, T3 |

The tasks touch disjoint files, so all four may run at once.

## Rules for every task

- Follow the existing code: naming, comment style (a short doc comment on every
  exported or `pub` item, no other comments unless the logic isn't obvious),
  file layout, test style. Prefer early returns. Keep functions small.
- **Go**: wrap errors `fmt.Errorf("doing x: %w", err)`, lower-case, no "failed
  to". Tests: testify (`require` for preconditions, `assert` for checks), one
  `TestX` per function or method with `t.Run` subtests per behaviour,
  table-driven only for 3+ trivial cases, `t.Context()`, `t.TempDir()`,
  `t.Parallel()` only on tests that touch the filesystem. `gofmt`, and
  `go tool golangci-lint run ./internal/...` must pass. Never call `exec.Command`.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean; `cargo clippy --all-targets
  -- -D warnings` and the same with `--no-default-features --features online`;
  unit tests in the file's `#[cfg(test)] mod tests`.
- **TypeScript** (`desktop/src/`): strict, `readonly` fields, no `any`;
  components are `const X = (props): JSX.Element =>`; styles in a CSS module.
  Tests: vitest, `@testing-library/react`, helpers in `desktop/src/test/`.
  Query by role and accessible name. **No new npm or cargo dependencies**
  (`@mui/material` is already one).
- **Commits**: do NOT add a `Co-Authored-By` trailer (a rule of this repo);
  end the message with nothing extra.
- **Environment**: Node here is 20; the desktop needs 22.12+ or 24.12+.
  Install Node from the official tarball into `$CLAUDE_JOB_DIR/tmp` (check its
  SHA256), put it on `PATH` for the shell you run npm in, and never wait on a
  permission prompt: if a command is refused, pick another way or list it as
  unverified in your report. Run heavy checks one at a time (`go test -p 2`,
  `npm test -- --maxWorkers=2`). Commit and push after each file and before
  the slowest suite.
- Don't touch files a task doesn't list, except to fix a compile error the
  task caused (say so in your report).

## Contract for `ws list --json` (T3 writes it, T4 reads it)

Every object in `workspaces[]` gains two keys. Nothing else changes.

```json
{"progress": {"name": "build auth", "total": 4, "done": 1, "note": "tests", "updated_at": "2026-11-22T20:00:00Z"},
 "rollup":   {"total": 7, "done": 3, "fraction": 0.4286}}
```

- `progress` is the workspace's own `progress.toml` record, with `name` and
  `note` null when empty, or `null` when it has no record.
- `rollup` is that workspace's steps plus its descendants', by
  `machine.ProgressTree` (a finished child with no record counts as one step,
  done if it passed). It is `null` when `total` is 0. `fraction` is
  `done/total`.
- A line of text from an unlisted directory (`unknown`) has both `null`.

## T1. Runner: allow `ws machine progress`

The ws runner's `run` tool refuses every `ws` command except `ws machine
result` (`internal/agent/tools/guard.go`, `CheckCommand`/`checkWS`).

- Allow `ws machine progress set`, `ws machine progress step` and `ws machine
  progress show` as well. `ws machine progress` alone, any other `ws machine`
  subcommand, and any other `ws` command stay refused. Update the error text:
  `command refused: ws may only run "ws machine result" or "ws machine progress set|step|show"`.
- `internal/agent/preamble.md`: change the rule to
  `Do not run ws (except `ws machine result` and `ws machine progress`), claude, or sudo; …`,
  and add one line after the budget line telling the agent: on a task of more
  than a few steps, run `ws machine progress set --name "<short name>" --total <n>`
  once, then `ws machine progress step` as each step finishes.
- Update the golden or test for the preamble if one pins its text.
- Docs: in `docs/adrs/0065-runner-agent-loop-tools-and-budgets.md` (the line
  near 235 that names the one allowed `ws` command) and
  `docs/adrs/0066-runner-security-boundaries.md` if it names it, say the
  runner may also run `ws machine progress`. ADRs are normally immutable once
  accepted; these are Proposed, so edit them in place.
- Tests in `internal/agent/tools/guard_test.go`: each allowed form passes; the
  bare `ws machine progress`, `ws machine spawn`, `ws list` and `ws machine
  progressx set` are refused; chained commands (`ws machine progress step; ws
  rm x`) are refused.
- Files: `internal/agent/tools/guard.go`, `guard_test.go`,
  `internal/agent/preamble.md`, the two ADRs, and any test that pins the
  preamble.
- Verify: `go test ./internal/agent/... ./internal/runner/...`,
  `gofmt -l internal`, `go tool golangci-lint run ./internal/...`.

## T2. Roles: report progress

The built-in roles are `internal/machine/role/roles/{implementer,reviewer,verifier}.md`.

- **implementer**: before step 1's work starts, add a step: for a task of more
  than a few steps, run `ws machine progress set --name "{{.Vars.TASK}} <3-5 word summary>" --total <n>`,
  then `ws machine progress step` after each finished step (code, tests,
  formatting, push). Keep the numbering consistent; "As your last action"
  stays last.
- **reviewer** and **verifier**: the same, shorter: set a name (`review
  {{.Vars.TASK}}`, `verify {{.Vars.BRANCH}}`) and a total once the checks are
  known, and `step` per check.
- State that progress is for people watching the run, never a reason to delay
  the verdict, and that a failed progress command must not stop the task.
- Update `internal/machine/role/testdata/implementer.golden` with `go test
  ./internal/machine/role -update`, then check the diff shows only your lines.
- Docs: `docs/tools/ws-machine.md`'s roles section, only if it quotes a built-in
  prompt.
- Files: the three role files, the golden, and a test only if one pins a prompt.
- Verify: `go test ./internal/machine/...`, `gofmt -l internal`,
  `go tool golangci-lint run ./internal/...`.

## T3. `ws list --json`: `progress` and `rollup`

Implement the contract above.

- `internal/cli/list.go`: add `Progress *progressJSON` and `Rollup *rollupJSON`
  (types are in `internal/cli/machine_progress.go`) to `listWorkspaceJSON`,
  tagged `progress` and `rollup`. Fill them in `a.listWorkspaces`.
- Compute rollups over **every** workspace from `a.workspaces.List()`, not
  only the entries that survive `ws list`'s filters, so a hidden child still
  counts. Reuse `progressTreeOf` (`machine_progress.go`) per workspace, with
  the root from `machine.Root`. A record that can't be read is a warning
  (`a.warned`), not a failure, and leaves both fields null.
- `listWorkspaceJSON` is embedded in `machineChildJSON`
  (`internal/cli/machine_result.go`), which already has `progress` and
  `rollup` fields; the outer ones win. Add a test that `ws machine children`
  output has each key once and carries the machine child's values.
- Text output of `ws list` is unchanged.
- Docs: `docs/tools/ws-list.md` (the JSON object and one sentence on the two
  fields), `internal/cli/guides/ws-list.md` if it exists. Do not add a
  command or flag.
- Tests in `internal/cli/list_test.go`: a workspace with a record has `progress`
  and `rollup`; a parent's rollup includes a hidden child; one with none has
  both null; `fraction` is null-free when total > 0.
- Verify: `go test ./internal/cli/...`, `gofmt -l internal`,
  `go tool golangci-lint run ./internal/...`.

## T4. Desktop: progress bars

Read the contract above. The desktop reads `ws list --json` in Rust
(`desktop/src-tauri/src/model.rs` and neighbours) and hands entries to the
frontend (`desktop/src/api.ts`, `desktop/src/store.ts`).

1. **Rust**: carry `progress` and `rollup` from `ws list` into the `Entry` the
   frontend gets, as optional fields (absent in older `ws` output means
   `None`). Do it for the Tauri build and the online server path; find every
   place `Entry` is built or serialised (`grep -rn "wsRunner\|ws_runner"
   desktop/src-tauri/src`) and update each. Unit tests parse a `ws list`
   object with and without the keys.
2. **TypeScript**: in `api.ts` add `readonly progress: EntryProgress | null`
   and `readonly rollup: EntryRollup | null` to `Entry` (`EntryProgress`: `name`,
   `total`, `done`, `note`, `updatedAt`, all nullable except `total`/`done`;
   `EntryRollup`: `total`, `done`, `fraction`). Update `test/fixtures.ts`, the
   transport mapping, and every literal `Entry` that no longer compiles.
3. **UI**, using `LinearProgress` from `@mui/material` (determinate) and no
   new dependency:
   - `EntryRow.tsx`: under a row whose `rollup` is non-null, a thin bar with
     `role="progressbar"`, `aria-label="<entry name> progress"`,
     `aria-valuenow` = done, `aria-valuemin` 0, `aria-valuemax` = total, and
     the text `done/total` beside it. Rows with `rollup === null` are unchanged.
   - `ChildrenCard.tsx`: each child row shows the same bar with the child's
     `progress.name` when it has one, else its entry name.
   - `EntryPane.tsx`: a root entry (no `parentId`) with a rollup shows one
     overall bar under its header with `done/total steps`, and the `note` of
     the most recently updated descendant's progress when there is one.
   - A failed child (status failed, per `features/agents/status.ts`) draws its
     bar in the error colour; the rest use the theme's primary colour. Bars are
     not interactive, so the 40px tap-target rule doesn't apply, but they must
     not cause horizontal scroll at 360px.
4. **Screenshot data**: add progress to the existing mock entries used for
   screenshots (find them with `grep -rni "mock" desktop/src --include=*.ts*
   -l`): an orchestrator with 4 steps (1 done), two children mid-run, one
   finished, and one failed.
5. **Tests**: vitest for each of the three components (the bar's role, name
   and values; absent when `rollup` is null; error colour for a failed child),
   and for the mapping.
6. Docs: `desktop/README.md` gets one short paragraph on the bars.
- Verify: `cd desktop && npm ci && npm run typecheck && npm test`, then
  `cd desktop/src-tauri && cargo test && cargo clippy --all-targets -- -D warnings`
  and the `--no-default-features --features online` pair.
