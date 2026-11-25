# Tasks: environment scope follow-ups

Implements [ADR 0083](../../adrs/0083-env-scope-followups.md); shapes and
rules are in [design.md](design.md). **Every task first reads
[constraints.md](constraints.md)**: the Node 24 install, the verify commands
(§4), the pinned values a task can break (§3), the hostile-input limits (§5)
and the repo rules (§6).

## Rules for every implementer (they override the built-in role prompt)

- **No `Co-Authored-By` trailer** on any commit: the repo forbids it, and
  this overrides the implementer role. Conventional, imperative messages
  (`feat:`, `fix:`, `test:`, `docs:`).
- **Commit and push after each file** (and before the slowest suite), to the
  task branch the orchestrator named.
- **Heavy checks one at a time**: `go test -p 2`, `npm test --
  --maxWorkers=2 --testTimeout=60000` (or `npx vitest run --maxWorkers=2
  --testTimeout=60000`), `cargo test -j 2 ... -- --test-threads=1`.
- **Never wait on a permission prompt.** If a command is refused or can't
  run here, pick another way or list it under `## Not run here` in the
  report, and record `PASS` if everything that could run passed. Say so if a
  command was killed; never claim it passed.
- Never wait with a loop on `pgrep -f <pattern>`; run long jobs in the
  background and wait for the notice. No background job may still run when
  you call `ws machine result`.
- Failing-first: commit each new behaviour's test before the code that makes
  it pass (`test: …` then `feat: …`). Name tests exactly as listed.
- Go: no blank lines inside function bodies (`make lint` checks it).
- Stay in the files your task lists. If the spec looks wrong, stop and say
  so in the report rather than widening the change.

## Stack and order

```
feat/env-scope-f2-remote-rollup     (base: main)
  T1 ─┐ T1 ∥ T2            ┌─ T3 ─┐ T3 ∥ T4
  T2 ─┴────────────────────┤      ├─ T5 (docs, small)
                           └─ T4 ─┘  (T4: UI mock → screenshot pass "rollup-")
feat/env-scope-f3-list-nesting      (base: feat/env-scope-f2-remote-rollup)
  T6 ── T7 ── T8 (docs, small)                       (no UI, no screenshots)
feat/env-scope-f1-new-agent-scope   (base: feat/env-scope-f3-list-nesting)
  T9 ∥ T10 ∥ T11 ── T12 (UI → screenshot pass "scope-new-agent-") ── T13 (docs, small)
```

Parallel marks are only for disjoint files: T1 (`internal/envscope`,
`internal/envproto`, `internal/environment`) ∥ T2 (`internal/cli/env_agent*`);
T3 (`internal/machine`, `internal/cli/machine*`) ∥ T4
(`internal/cli/list*`, `desktop/test/tauri-mock.js`, `desktop/README.md`);
T9 (`internal/cli/claude_test.go`) ∥ T10 (`desktop/src-tauri`) ∥ T11
(`desktop/src`, `desktop/test`, `desktop/README.md`). Merge the second of a
parallel pair with `merge.sh --rebase` and compile (`go build ./...` or
`npx tsc --noEmit`).

Common verify (every Go task, after its own lines):

```bash
go build ./...
go vet ./internal/... && test -z "$(go tool golangci-lint fmt --diff ./internal/...)"
go tool golangci-lint run ./internal/...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
```

---

# F2 — `feat/env-scope-f2-remote-rollup`: remote children in the rollup

## T1 — `envscope.Merger` and `scope.children` `rollup`

Depends on: none. ∥ T2. Backend.

Files:
- `internal/envscope/rollup.go` (new), `internal/envscope/rollup_test.go` (new)
- `internal/envproto/scope.go` (`ScopeChild.Rollup`, `ScopeRollup`),
  `internal/envproto/scope_test.go` (only if a literal lists every field)
- `internal/environment/scope.go` (`scopeChildren`, new `rollupMerger`),
  `internal/environment/scope_test.go`

Signatures: exactly design §F2.3 and §F2.4. In `internal/environment/scope.go`:

```go
// rollupMerger is a Merger over every enrolled environment's snapshot and
// the ledger. A snapshot that can't be read or decoded is skipped.
func (c *Controller) rollupMerger(entries []envscope.Entry) *envscope.Merger
```

`scopeChildren` calls `c.rollupMerger(entries)` once (only when at least one
child is returned) and sets each child's `Rollup` from `Of(e.Environment,
e.Workspace)` (`nil` when not listed). `ScopeRollup{Total, Done}` from
`Steps`.

Tests, `internal/envscope/rollup_test.go` (`TestSteps_Add`,
`TestSteps_Valid`, `TestListedOf`, `TestMerger_Of`, `TestMerger_Below`):
- `Add saturates at MaxRollupSteps and keeps done at most total`
- `Valid refuses negative, done above total, and total above MaxRollupSteps`
- `ListedOf keeps id parent and rollup` / `skips ids that are empty, over 128 bytes or hold control characters` / `stops at MaxListedPerEnvironment` / `refuses an array over 1 MiB` / `returns an out-of-range rollup as zero and names it in invalid` / `a null rollup is zero`
- `Of adds a ledger child of the workspace` (A local base 3/1, ledger child on B 2/2 → 5/3)
- `Of adds a ledger child of a same-environment descendant` (child of A's local child)
- `Of follows ledger children recursively across three environments` (local P → B X → C Z: Of(local P) includes Z once)
- `Of counts each ledger entry once` (same entry reachable twice via a forged duplicate listing → counted once)
- `Of ends a ledger cycle` (X on B parent (C, Z), Z on C parent (B, X): finite, each once)
- `Of stops after MaxMergeDepth ledger hops` (a chain of 70 → only 64 hops counted)
- `Of ignores a ledger entry on its parent's own environment`
- `Of ends a same-environment parent cycle`
- `Of reports not listed` (ok false)
- `Below sums only the named workspaces' ledger children`

Tests, `internal/environment/scope_test.go`, in `TestController_Scope`:
- `children carry the merged rollup` (snapshot of B lists the child with
  rollup 2/1 and its ledger child on C 1/1 → reply rollup `{"total":3,"done":2}`)
- `children not in their snapshot have a null rollup`
- `a snapshot rollup out of range counts as zero`

Acceptance:
- `go test -p 2 -count=1 ./internal/envscope/ ./internal/environment/ ./internal/envproto/` passes; the new tests failed before the code (commit order).
- The reply JSON has `"rollup"` on every child (null or object).
- Reviewer checks the hostile-input limits in constraints §5 rows F2-T1: 1 MiB, 4096 per environment, id 1-128 bytes, `Valid`, saturation, 64 hops, each entry once, same-environment ledger entries ignored, no paths from ids.

Verify:
```bash
go test -p 2 -count=1 ./internal/envscope/ ./internal/environment/ ./internal/envproto/
go test -p 2 -count=1 ./internal/cli/ -run 'Remote|MachineChildren'
# + common verify
```

## T2 — snapshots carry `progress` (no note) and `rollup` (`small`)

Depends on: none. ∥ T1. Backend.

Files: `internal/cli/env_agent.go` (`agentSnapshots.Workspaces`),
`internal/cli/env_agent_test.go` (`TestAgentSnapshots_Workspaces`).

Change: build with `s.app.listedWorkspaces(entries, nil, loopRuns)` instead
of `listWorkspaces` (it errors only on listing; return the error), then set
`Progress.Note = nil` on every object that has a progress. Update the doc
comment (ADR 0083).

Tests (add to `TestAgentSnapshots_Workspaces`; keep the two existing):
- `a workspace's progress and rollup are included` (write a progress record
  with `machine.NewStore(app.paths.State).WriteProgress`, total 2 done 1 →
  `"progress":{"name":…,"total":2,"done":1,"note":null,…}`,
  `"rollup":{"total":2,"done":1,"fraction":0.5}`)
- `the progress note is left out` (record has a note → `"note":null`)
- `a child's steps reach its parent's rollup` (local parent + child records)

Acceptance: both old subtests unchanged and passing; snapshot size note:
the reviewer confirms no other field changed (diff a before/after object).

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestAgentSnapshots|TestApp_runList|EnvAgent'
# + common verify
```

## T3 — `ws machine children`, `wait`, `progress show` add remote rollups

Depends on: T1. ∥ T4. Backend.

Files:
- `internal/machine/progress.go` (`AddRemote`), `internal/machine/progress_test.go`
- `internal/cli/machine_remote.go` (`remoteChild.rollup`, `remoteChildrenOf`,
  `remoteMachineChild`, `nestRemoteProgress`, `remoteLeaf`, `addLeaf`)
- `internal/cli/machine_progress.go` (`childProgress` gains `remote []remoteChild`)
- `internal/cli/machine.go` (`runMachineChildren`), `internal/cli/machine_result.go` (`machineChild`), `internal/cli/machine_wait.go` (items)
- `internal/cli/machine_remote_test.go`, `internal/cli/machine_test.go`, `internal/cli/machine_progress_test.go`

Signatures: design §F2.5, plus:

```go
// remoteSteps is rc's rollup from the controller, or the zero Rollup when
// it sent none or one that isn't Valid.
func remoteSteps(rc *remoteChild) machine.Rollup
// addLeaf appends leaf under parent and adds leaf.Rollup to every node on
// the path, recomputing fraction (replaces today's addLeaf).
func addLeaf(n *progressNodeJSON, parent string, leaf progressNodeJSON) bool
```

Rules:
- `remoteChildrenOf` copies `c.Rollup` into `remoteChild.rollup` when
  non-nil and `0 ≤ done ≤ total ≤ envscope.MaxRollupSteps`; otherwise nil
  plus the warning `ignoring the rollup of <id> on <env>: out of range`.
- `childProgress(..., remote)`: after `progressTreeOf`, for each rc whose
  parent is in the tree, `tree.AddRemote(rc.parent, remoteSteps(&rc))`.
- `runMachineChildren`: fetch remote children **first**, always with
  `descendantIDs(list, caller)` (so local children's rollups see their
  remote descendants); list only `directRemoteIDs` ones when `!all` (today's
  output set). Pass the list to `machineChild` → `childProgress`.
- Remote child item: `Rollup` = `remoteSteps` as rollupJSON when rc.rollup
  is non-nil, else today's `machine.record` rollup.
- `wait`: before building items, fetch the remote list again **only when**
  the first fetch returned children; use it for every item's
  `childProgress`.
- `progress show`: leaf rollup = rc.rollup when non-nil, else the
  record's; `addLeaf` raises ancestors and the caller node; the top-level
  `rollup` is the caller node's after nesting.
- `progress set/step` unchanged.

Tests:
- `TestProgressNode_AddRemote`: `adds to the parent and every ancestor`, `reports false for an unknown parent`, `leaves other branches alone`.
- `machine_remote_test.go` (reuse `serveChildHost`, `remoteHelperChild`; give the child `Rollup: &envproto.ScopeRollup{Total: 2, Done: 1}`):
  - `TestApp_runMachineChildren` (`machine_test.go`): `a local child's rollup includes its remote descendants`, `a remote child's rollup is the controller's`, `without --all only direct remote children are listed`.
  - `TestApp_nestRemoteProgress` (`machine_remote_test.go`): `remote leaves raise their ancestors' rollups` (caller own 3/1 + remote 2/1 → caller rollup 5/2, fraction 0.4), `a remote child with a null rollup adds nothing`, `an out-of-range rollup is ignored with a warning`.
  - `TestApp_runMachineWait_remoteChild`: `wait items include remote descendants in a local child's rollup`.
- Existing tests in `machine_remote_test.go`, `machine_progress_test.go`, `machine_wait_test.go` pass unedited except where a literal must name the new field.

Acceptance: no daemon call is added when there is no ledger and the scope is
default (an existing "no daemon" test still passes); hostile-input checks:
`Valid` range, warning text, saturation via `Steps.Add`.

Verify:
```bash
go test -p 2 -count=1 ./internal/machine/
go test -p 2 -count=1 ./internal/cli/ -run 'Machine|Progress|Remote|Wait'
# + common verify
```

## T4 — `ws list --json` merges remote rollups; mock fixture (UI fixture)

Depends on: T1. ∥ T3. Backend + **UI fixture** (screenshot pass after it).

Files:
- `internal/cli/list_rollup.go` (new), `internal/cli/list_rollup_test.go` (new)
- `internal/cli/list.go` (one call in `runList`'s JSON branch, after `annotateScopes`)
- `desktop/test/tauri-mock.js` (the `plan-scope` family only), `desktop/README.md` (one sentence in "Screenshots without the backend")

Signature: design §F2.6:

```go
func (a *App) mergeRollups(entries []listEntryJSON)
// snapshotListed is every enrolled environment's snapshot as Listed, read
// once each; a snapshot that can't be read or decoded is a warning.
func (a *App) snapshotListed() []envscope.Listed
```

Rules: design §F2.6 bullets. Gate on `envscope.LedgerExists`; no ledger →
return at once (no reads). Local rollup + `Below` uses the full workspace
list (`a.workspaces.List()`, hidden included). Remote entry rollup =
`Of`, null when total 0. Steps → `rollupJSON` with fraction.

Mock (`desktop/test/tauri-mock.js`), exact values (design §F2.5 example):
- `plan-scope-4d5e6f7a`: `progress: { name: "compare IPs", total: 3, done: 1, note: "Waiting for win-rig", updatedAt: "2026-11-25T10:00:00Z" }`, `rollup: { total: 8, done: 4, fraction: 0.5 }`
- `ip-linux-8a9b0c1d`: `progress: { name: "private IP", total: 2, done: 2, note: null, updatedAt: "2026-11-25T09:58:00Z" }`, `rollup: { total: 3, done: 2, fraction: 2 / 3 }`
- `ip-win-2e3f4a5b`: `progress: { name: "private IP", total: 2, done: 1, note: null, updatedAt: "2026-11-25T09:40:00Z" }`, `rollup: { total: 2, done: 1, fraction: 0.5 }`
- `ip-probe-6c7d8e9f`: `progress: { name: "route", total: 1, done: 0, note: null, updatedAt: "2026-11-25T09:41:00Z" }`, `rollup: { total: 1, done: 0, fraction: 0 }`

Tests (`TestApp_mergeRollups`, one `t.Run` each; reuse `newRemoteListApp`,
`addLinuxServer`/`addEnvironment`, `enrollEnvironment`, `writeRemoteSnapshot`
with objects carrying `"rollup":{…}`, and `envscope.NewLedger(...).Put`):
- `without a ledger nothing is read` (snapshot unreadable → no warning, rollups unchanged)
- `a local parent's rollup includes its remote child`
- `a local grandparent's rollup includes a remote child of its local child`
- `a remote child's rollup includes its own remote child` (B → C)
- `a remote child counts once` (listed under `--env` and default)
- `an offline environment's last snapshot still counts`
- `a child missing from its snapshot counts nothing`
- `a removed environment counts nothing`
- `an out-of-range snapshot rollup is a warning and counts nothing`
- `an unreadable ledger leaves rollups local with one warning`
- `hidden and --env-filtered entries still count`

Acceptance: the 12 `list_test.go` `JSONEq` strings and `list_env_test.go`
pass **unedited**; `node --check desktop/test/tauri-mock.js` ok;
hostile-input checks as T1 (via `ListedOf`) plus the warning text.

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_mergeRollups|TestApp_runList|TestApp_annotateScopes|List'
export PATH="$CLAUDE_JOB_DIR/tmp/node/node-v24.22.3-linux-x64/bin:$PATH"; cd desktop && node --check test/tauri-mock.js
# + common verify
```

**Screenshot pass after T4 (orchestrator's Sonnet child)**, mock as above,
PNGs under `desktop/docs/`:
- `rollup-plan-scope.png` — 1100×750, Agents, `plan-scope` selected: the
  pane's root bar reads `4/8 steps` with the note `Waiting for win-rig`.
- `rollup-plan-scope-mobile.png` — 360×780, the same pane.
Layout checks (report each by name): `rollup-caption-visible` (the
`4/8 steps` caption isn't clipped: its `scrollWidth <= clientWidth`),
`rollup-bar-width` (bar width > 0 and ≤ the pane's), `no-horizontal-scroll`
at 360 px (`document.documentElement.scrollWidth <= innerWidth`),
`tap-targets-40px` (row and chevron ≥ 40 px tall at 360 px).

## T5 — docs for F2 (`small`)

Depends on: T3, T4.

Files: `internal/cli/guides/ws-machine-children.md`, `ws-machine-wait.md`,
`ws-machine-progress-show.md`, `ws-machine-progress.md`, `ws-list.md`;
`docs/tools/ws-machine.md`, `docs/tools/ws-list.md`, `docs/state.md`;
`desktop/README.md` (a two-row image table under "Environment scopes":
`docs/rollup-plan-scope.png`, `docs/rollup-plan-scope-mobile.png`).

Content: one or two sentences each: a rollup includes children on other
environments, merged by the controller from their snapshots (≤ 30 s old),
counted once; offline children count their last snapshot; removed ones
count nothing; `ws machine progress set`/`step` print a local-only rollup.
`docs/state.md`: snapshot objects carry `progress` (note always null) and
`rollup`. Keep every string `llm_test.go` pins (constraints §3).

Acceptance and verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestGuideTopics|TestApp_runLLM|TestApp_newLLMCmd'
```
(Docs-only: the orchestrator may skip the task review, procedure E8.)

---

# F3 — `feat/env-scope-f3-list-nesting`: `ws list` table and `--parent`

## T6 — the table nests remote children under their ledger parent

Depends on: T5 (F2 merged into the stack). Backend (CLI text).

Files:
- `internal/cli/list_tree.go` (new), `internal/cli/list_tree_test.go` (new)
- `internal/cli/list.go` (`listRow` fields; `runList` table branch; `localRows`; `printList`)
- `internal/cli/list_env.go` (`remoteRows`; `envText` moves into `printList`)
- `internal/cli/list_scope.go` (extract `fillScopeParents(entries)` from `annotateScopes`, which then calls it)

Signatures: design §F3.1:

```go
// listRow gains:
parent      string // same-environment parent id, "" for none
scopeParent string // rowKey of scope_parent, "" for none
stale       bool   // the ENV column adds " (offline)"
// list_tree.go
func rowKey(env, id string) string
func crossOrder(rows []listRow) []listRow
// list_scope.go
// fillScopeParents sets ScopeParent on remote entries from the ledger, and
// resets it everywhere; it never asks the daemon.
func (a *App) fillScopeParents(entries []listEntryJSON)
```

Rules: table branch only: when `envscope.LedgerExists`, call
`fillScopeParents(entries)`, copy each remote entry's scope parent into its
row (`rowKey(sp.Environment, sp.Workspace)`), then `rows =
crossOrder(rows)`. No ledger: skip both (output byte-identical). Rows keep
`env` bare; `printList` writes `env + " (offline)"` when `stale`.

Tests:
- `TestCrossOrder`: `nests a remote child under its local parent`, `nests a remote grandchild two levels deep`, `prefers the same-environment parent`, `keeps a child whose parent isn't listed as a root in place`, `ends a cycle`, `keeps roots in input order`.
- `TestApp_runList` (table): `nests remote children under their ledger parent` (expected text with `└ ` and two-space indents, ENV column), `without a ledger the table is unchanged` (existing table tests also unedited).

Acceptance: existing table and JSON tests unedited; reviewer checks no daemon
call is added in table mode (`acceptDialled` returns an error in the test).

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestCrossOrder|TestApp_runList|List'
# + common verify
```

## T7 — `--parent` lists children on every environment

Depends on: T6. Backend (CLI).

Files: `internal/cli/list_env.go` (`listedEnvironments`,
`remoteEnvListing`, new `parentFilter` type), `internal/cli/list.go`
(`runList`'s `--parent` check; `localListing`; rename `parentFilter` method
to `parentID`), `internal/cli/list_env_test.go`.

Signatures: design §F3.2 (`parentFilter`, `keeps`). Plus:

```go
// childEnvironments is the environments of the ledger entries whose parent
// is (envID, id), by name, sorted; nil without a ledger.
func (a *App) childEnvironments(envID, id string) []string
```

Rules: design §F3.2 bullets. `runList`'s check becomes
`if opts.parent != "" && len(opts.envs) > 1`. `-q` unchanged.

Tests (`TestApp_runList_parent` or subtests of the existing `--parent` test):
- `--parent of a local workspace lists its remote children`
- `--parent of a remote workspace lists its children on a third environment`
- `--parent still lists same-environment children` (existing, unedited)
- `--parent with --env names the parent's environment and still lists remote children`
- `--parent without a ledger reads only the parent's environment` (no ledger, unchanged)
- `two --env with --parent is still usage`
- `-q --parent stays local`

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_runList|List'
# + common verify
```

## T8 — docs for F3 (`small`)

Depends on: T7.

Files: `internal/cli/guides/ws-list.md` (`--parent` row: "its direct
children on every environment, including those it started with
`ws machine spawn --env`"; one sentence on table nesting),
`docs/tools/ws-list.md` (same, plus the table example from design §F3.1),
`README.md` (one sentence in the remote environments section).

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestGuideTopics|TestApp_runLLM'
```

---

# F1 — `feat/env-scope-f1-new-agent-scope`: grant a scope from New agent

## T9 — pin "own environment only" in `ws claude` (`small`)

Depends on: T8 (stack). ∥ T10 ∥ T11. Backend (tests only).

Files: `internal/cli/claude_test.go` only.

These pin behaviour that already works and that the desktop relies on, so
they pass at once: say so in the report. The reviewer must mutate
`remoteGrant`'s `ref.Name != env` and `requestedRefs`'s
`len(names) == 0` to see them fail.

Tests (reuse `newClaudeEnv`, `addEnvironmentForScope`, `scopeFileOf`,
`linuxRef`, `winRef`):
- in `TestApp_runClaude`'s scope subtests: `an --env-scope of local alone skips default_scope` (DefaultScope = [linux-server], `envScope: ["local"]` → no `scope.json`)
- `TestApp_remoteGrant` (new): `its own environment alone grants nothing and skips default_scope` (env linux-server, names [linux-server], DefaultScope [win-rig] → empty), `names besides its own are granted` (→ [win-rig]), `empty names use default_scope` (→ [win-rig]), `inside a workspace nothing is granted`

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_runClaude|TestApp_remoteGrant|TestApp_grantScope'
# + common verify
```

## T10 — Rust: `NewAgent.env_scope`, validation, `--env-scope=` (`small`)

Depends on: T8 (stack). ∥ T9 ∥ T11. Backend.

Files: `desktop/src-tauri/src/commands.rs`, `desktop/src-tauri/src/reviews/fix.rs`.

Signatures: design §F1.2 (`env_scope`, `MAX_SCOPE_NAMES`,
`MAX_SCOPE_NAME_BYTES`, `validate_env_scope`). `new_agent_args` pushes
`format!("--env-scope={}", names.join(","))` right after the `--env=`
argument (or after `"claude"`); `create_agent` runs
`validate_env_scope` first when `Some` and returns its `AppError` (code
`usage`) without running `ws`. All 7 `NewAgent` literals get
`env_scope: None` (constraints §3).

Security tests (inline `#[cfg(test)]`, exact names):
- `new_agent_args_without_env_scope_sends_no_env_scope_flag`
- `new_agent_args_sends_env_scope_as_one_argument` (local: `["claude", "--env-scope=local,linux-server", …]`)
- `new_agent_args_puts_env_scope_after_env` (`--env=linux-server`, then `--env-scope=linux-server`)
- `validate_env_scope_accepts_enrolled_style_names`
- `validate_env_scope_refuses_an_empty_list`
- `validate_env_scope_refuses_unsafe_names` (`"a,b"`, `"a b"`, `"-x"`, `"a\nb"`, `""`, 65 bytes)
- `validate_env_scope_refuses_more_than_64`
- `validate_env_scope_refuses_duplicates`
- `review_fix_agent_never_grants_a_scope` (in `reviews/fix.rs` tests: `new_agent(...)` has `env_scope == None` and its args contain no `--env-scope`)

Acceptance: the existing `new_agent_args_*` tests pass unedited except the
added field; `online` feature clippy clean (dispatch deserializes the new
field with `#[serde(default)]`).

Verify:
```bash
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --lib commands -- --test-threads=1
cd desktop/src-tauri && cargo test -j 2 --lib reviews::fix -- --test-threads=1
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

## T11 — TS types, pure scope helpers, fixtures and mock (UI support)

Depends on: T8 (stack). ∥ T9 ∥ T10. UI support (no component).

Files:
- `desktop/src/api.ts` (`NewAgent.envScope?`)
- `desktop/src/features/environments/environments.ts` (+ `environments.test.ts`): `ScopeChoice`, `scopeChoices`, `splitDefaultScope`, `defaultScopeOf` (design §F1.3)
- `desktop/src/test/fixtures.ts`: `configListingWith(settings: readonly ConfigSetting[]): ConfigListing` (appends to `configListing()`'s settings)
- `desktop/test/tauri-mock.js`: in `data.config.settings` add `{ key: "environments.default_scope", type: "strings", value: ["linux-server", "old-rig"], default: [], set: true }`; in `data.environments` add a pending `{ id: "env_5e6f", name: "lab-pi", state: "pending", status: "pending", lastSeen: null, createdAt: "2026-11-24T18:00:00Z", enrolledAt: null, certNotAfter: null, certCa: null, hello: null, workspaces: null }`; `create_agent: ({ request }) => { data.lastCreateAgent = request; if (request?.name === "fail-scope") { throw { code: "environment_unknown", message: "no such environment: old-rig (ws env list shows them)", details: { environment: "old-rig" } }; } return { id: "new-agent-1a2b3c4d", session: "1a2b3c4d", path: "/w/new-agent-1a2b3c4d" }; }`
- `desktop/README.md`: one sentence in "Screenshots without the backend" naming `default_scope`, `lab-pi` and the `fail-scope` name.

Rules (`ScopeChoice` = `{ name, status: "online" | "offline" | "unknown", own }`):
- `scopeChoices(items, target)`: `local` (status `online`) and each `state === "enrolled"` item (status `online`; `unknown` stays `unknown`; anything else `offline`); `own` = `name === target`; order: own first, then `local`, then by name.
- `splitDefaultScope(defaults, items)`: `kept` = names that are `local` or an enrolled item's name, in order, deduplicated; `leftOut` = the rest.
- `defaultScopeOf(listing)`: the `value` of `environments.default_scope` when it is an array of strings (at most 64 kept), else `[]`.

Tests (`environments.test.ts`):
- `scopeChoices leaves out pending environments`
- `scopeChoices puts its own environment first and marks it`
- `scopeChoices keeps an offline or unknown environment selectable`
- `splitDefaultScope keeps local and enrolled names and leaves out the rest`
- `defaultScopeOf reads the strings and ignores anything else`

Acceptance: `tsc` clean; existing tests unedited; `node --check` on the mock.

Verify:
```bash
export PATH="$CLAUDE_JOB_DIR/tmp/node/node-v24.22.3-linux-x64/bin:$PATH"
cd desktop && npm ci && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/environments src/features/create
cd desktop && node --check test/tauri-mock.js
```

## T12 — the "May start children on" field in New agent (UI)

Depends on: T11 (and T10 for the end-to-end path; the tests mock `api`).

Files:
- `desktop/src/features/create/ScopeField.tsx` (new), `ScopeField.test.tsx` (new)
- `desktop/src/features/create/NewAgentDialog.tsx` (+ tests in `NewAgentDialog.test.tsx`)
- `desktop/src/features/create/NewDialog.module.css` only if a class is needed

Signatures and behaviour: design §F1.3 (`ScopePick`, `useScopePick(open,
target)`, `ScopeField`), table and copy exactly as written there.
`useScopePick` calls `api.configList()` once per opening (`open` false →
true); while it loads, the selection is the own environment only. The
selection is component state, reset on every opening; it never reads
`lastChoice`, `usePrefilledSpecs` or any stored value. `NewAgentDialog`
renders `<ScopeField pick={scope} />` right after `EnvironmentField`, sends
`envScope: scope.selected` only when `scope.visible`, disables Start when
`scope.tooMany`, and extends `errorHint` with the two hints in design §F1.3.

Security and behaviour tests (exact names; `NewAgentDialog.test.tsx` unless
noted; use `openWithEnvironments`, `fakeApi({ configList: vi.fn(async () =>
configListingWith([...])) })`):
- `defaults to its own environment when default_scope is empty` (sends `envScope: ["local"]`)
- `preselects default_scope's enrolled names and warns about the rest`
- `never remembers a widened scope between openings` (check `linux-server`, Cancel, reopen → unchecked, sends `["local"]`)
- `prefilled specs never touch the scope` (prefilled specs present; scope still `["local"]`)
- `its own environment is always checked and can't be unchecked`
- `switching the target doesn't carry the old target into the scope`
- `pending environments aren't offered`
- `offline environments can be chosen with a note`
- `omits envScope when no environment is enrolled`
- `sends envScope with its own environment first`
- `shows environment_unknown with a hint`
- `a failed config read falls back to its own environment with a warning`
- `ScopeField.test.tsx`: `labels offline and unknown environments`, `renders nothing when not visible`, `checkbox labels are clickable`

Acceptance: every existing `NewAgentDialog.test.tsx` test passes unedited;
whole suite passes once at the end.

Verify:
```bash
export PATH="$CLAUDE_JOB_DIR/tmp/node/node-v24.22.3-linux-x64/bin:$PATH"
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/create src/features/environments
cd desktop && npm test -- --maxWorkers=2 --testTimeout=60000
```

**Screenshot pass after T12 (orchestrator's Sonnet child)**, mock from T11,
PNGs under `desktop/docs/`:
- `scope-new-agent-default.png` — 1100×750, New agent open: `local · its own environment` checked and disabled, `linux-server` checked, the `old-rig` left-out warning.
- `scope-new-agent-remote.png` — Environment `linux-server`: `linux-server · its own environment` checked and locked, `local` unchecked.
- `scope-new-agent-offline.png` — `win-rig · offline` checked, with the offline note.
- `scope-new-agent-error.png` — Name `fail-scope`, Start pressed: the `environment_unknown` alert with its hint.
- `scope-new-agent-mobile.png` — 360×780, the dialog scrolled to the field.
Layout checks (report each by name): `scope-no-clipped-labels`
(each checkbox label's `scrollWidth <= clientWidth`, or it wraps),
`scope-no-overlap` (no two labels' bounding boxes intersect),
`no-horizontal-scroll` at 360 px, `scope-tap-targets-40px` (each checkbox's
hit area ≥ 40×40 px at 360 px), `scope-alert-wraps` (alerts within the
dialog's width). Also confirm `window.__TAURI_INTERNALS__` mock logged no
unhandled command and `data.lastCreateAgent.envScope` for the default shot
is `["local", "linux-server"]`.

## T13 — docs for F1 (`small`)

Depends on: T12, T9.

Files: `desktop/README.md` (New agent section: the field, the four rules —
own environment always, starts from `default_scope`, never remembered,
offline allowed — and an image table of the five `scope-new-agent-*.png`);
`docs/config.md` (`environments.default_scope`: the desktop preselects it,
leaving out names that aren't enrolled); `internal/cli/guides/ws-claude.md`
and `docs/tools/ws-claude.md` (`--env-scope <its own environment>` grants
nothing more and skips `default_scope`).

Verify:
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestGuideTopics|TestApp_runLLM'
```

---

## Reviewer checklist (all tasks)

- Files stay within the task's list; no `Co-Authored-By` trailer.
- New tests were committed before the code (except T9, which pins existing behaviour and is checked by mutation).
- Hostile-input limits the task names (constraints §5) are enforced and tested: T1 (1 MiB, 4096, id 1-128, `Valid`, saturation, 64 hops, once per entry, same-environment ledger entries ignored), T3 (reply `Valid` + warning), T4 (snapshot rollup range warning), T6 (cycle), T10 (name rules, ≤ 64, non-empty, one argument), T11/T12 (only enrolled or `local` preselected; nothing remembered).
- No new I/O or daemon call without a ledger (T3, T4, T6, T7).
- No new Tauri command; `ws claude` unchanged (T9-T12).
