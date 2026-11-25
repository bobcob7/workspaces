# Tasks: environment scopes in the desktop app

Design: [design.md](design.md). **Every task reads
[constraints.md](constraints.md) first**: Node 22 install, verify commands,
pinned values, repo rules (no `Co-Authored-By` trailer; conventional
`feat:`/`fix:`/`test:`/`docs:` commits).

Order and parallelism:

```
wave 1:  T1 ∥ T2 ∥ T3        (disjoint files: Go / Rust / TS types+mock)
wave 2:  T4                  (needs T3)
wave 3:  T5                  (needs T4)
wave 4:  T6  small           (needs T5)
```

T1 is the only backend task and is **additive** (two `ws list --json`
fields). T2 and T3 code against the JSON shapes below, not against T1's code,
so they can run beside it. Screenshots are the orchestrator's pass after T6,
with the mock from T3.

Shared shapes (all tasks):

```jsonc
// ws list --json, on every entry (T1 produces, T2 consumes)
"scope_parent": {"environment": "local", "workspace": "plan-scope-4d5e6f7a"} | null,
"scope": [{"name": "local", "id": "local", "state": "online", "current": true}, ...] | null
// state: "online" | "offline" | "removed"   (T2 maps anything else to "unknown")
```

```ts
// desktop/src/api.ts (T3 adds; T4-T6 use)
export interface ScopeParent { readonly environment: string; readonly workspaceId: string; }
export type ScopeState = "online" | "offline" | "removed" | "unknown";
export interface ScopeEnvironment { readonly id: string; readonly name: string; readonly state: ScopeState; readonly current: boolean; }
// Entry: readonly scopeParent: ScopeParent | null; readonly scope: readonly ScopeEnvironment[] | null;
```

---

## T1 — backend: `scope_parent` and `scope` on `ws list --json`

**Marked: backend change, additive.** Depends on: none. ∥ T2, T3.

Files:
- `internal/cli/list.go`: two fields on `listEntryJSON`; one call in `runList`.
- `internal/cli/list_scope.go` (new): the annotation.
- `internal/cli/list_scope_test.go` (new).
- `internal/cli/list_test.go`: the 12 `JSONEq` strings (constraints §3).
- `internal/cli/machine_envs.go`: extract the sort.
- `docs/tools/ws-list.md`, `internal/cli/guides/ws-list.md`: document both fields.

Code:

```go
// list.go, listEntryJSON, after AgentState:
// ScopeParent is a cross-environment child's parent from the controller's
// scope ledger, or null (ADR 0082).
ScopeParent *scopeParentJSON `json:"scope_parent"`
// Scope is the environments the workspace may start children on when wider
// than its own, or null (ADR 0082).
Scope []envproto.ScopeEnv `json:"scope"`

// runList, JSON branch only, before writeJSON:
case a.global.json:
    a.annotateScopes(ctx, entries)
    return a.writeJSON(...)

// list_scope.go
type scopeParentJSON struct {
    Environment string `json:"environment"` // "local" or an environment name
    Workspace   string `json:"workspace"`
}
type ledgerKey struct{ env, workspace string }

// annotateScopes resets and fills ScopeParent and Scope on every entry.
func (a *App) annotateScopes(ctx context.Context, entries []listEntryJSON)
// readLedger is the ledger by (environment id, workspace); nil when there is
// none (envscope.LedgerExists false) or, with a warning, when it can't be read.
func (a *App) readLedger() map[ledgerKey]envscope.Entry
// scopeStates names environment ids and gives their state; presence is asked
// of the daemon at most once, on the first remote id.
type scopeStates struct {
    app      *App
    byID     map[string]environment.Record // from a.envStore().List()
    presence map[string]string             // nil until asked
}
func (s *scopeStates) env(ctx context.Context, ref envscope.Ref, self string) envproto.ScopeEnv
func (s *scopeStates) nameOf(id string) (string, bool) // current name, false when not in the store

// machine_envs.go: move the SortFunc in scopeEnvsOf into
func sortScopeEnvs(envs []envproto.ScopeEnv) // current first, then by name
```

Rules (design §3.1):
1. For **every** entry, first set `ScopeParent = nil` and `Scope = nil`
   (remote snapshot values are discarded; a hostile snapshot can carry them).
2. Local entry (`Environment == "local"`, `ID != nil`): `envscope.Read(a.paths.State, *ID, self)`
   with `self` from `a.selfRef()` (read once; on error `localRef()`). Missing
   file or `!scope.WiderThanSelf()` → `Scope` stays nil. Error →
   `a.warned = append(a.warned, fmt.Sprintf("ignoring the scope of %s: %v", id, err))`, nil.
3. Remote entry: its id is the record id of `Environment` (by name in
   `byID`); look up `ledger[ledgerKey{envID, *ID}]`. If found:
   - `Parent != nil` → `ScopeParent = {Environment: "local" | nameOf(Parent.Environment), Workspace: Parent.Workspace}`;
     if `nameOf` is false (environment removed) → nil.
   - `len(Scope) > 1` → `Scope` = each id as a ref (`Name` = `nameOf` or the id
     itself when removed), mapped by `env`, then `sortScopeEnvs`.
4. `env`: `local` → `online`; id not in `byID` or `State != environment.StateEnrolled`
   → `removed` (keep the ref's name); else presence by current name:
   `"online"` → `online`, otherwise `offline`. `Current = ref.ID == self`.
5. Ledger read error → `couldn't read the scope ledger: <err>` warning, and the
   remote fields stay nil. No exit-code change. Table and `-q` output unchanged.
6. Don't call the daemon when no scope names a remote environment.

Hostile-input limits: rely on `envscope` (ledger ≤ 8 MiB, ≤ 4096 entries,
strict decode, validated ids; `scope.json` ≤ 16 KiB, ≤ 64 entries,
single-path-element id check). Build no path from ledger values.

Tests (`TestApp_annotateScopes`, one `t.Run` each; reuse
`newRemoteListApp`, `addLinuxServer`/`addEnvironment`, `enrollEnvironment`,
`writeRemoteSnapshot`, `remoteWorkspace(s)`, `listenDaemon`/`acceptDialled`
from `list_env_test.go`; write the ledger with
`envscope.NewLedger(app.paths.Environments).Put(t.Context(), &envscope.Entry{...})`
and scope files with `envscope.Write(app.paths.State, id, envscope.FileFor(...))`):
- `a remote child gets scope_parent local from the ledger`
- `a remote-to-remote parent is named by its environment's name`
- `a person grant has scope but no scope_parent`
- `snapshot scope_parent and scope are ignored` (snapshot JSON carries both; no ledger → both null)
- `a local scope file gives scope with states, current first` (`local` online current, an enrolled env offline, a removed id `removed` with its stored name)
- `a scope of only its own environment is null`
- `an invalid scope file is a warning and null`
- `an unreadable ledger is a warning and null`
- `without a ledger or scope files nothing dials the daemon` (`acceptDialled` returns an error)
- `a removed parent environment gives a null scope_parent`

Acceptance:
- Every `list_test.go` `JSONEq` string gains `"scope_parent":null,"scope":null` per entry; no other change to them.
- `docs/tools/ws-list.md`: the `--json` row names the two fields; the JSON
  example shows `"scope_parent": null, "scope": null`; a paragraph after
  `agent_state` describes them (copy design §3.1 bullets 1-2 in two or three
  sentences). `internal/cli/guides/ws-list.md`: two field bullets.

Verify (constraints §4, Go block):
```bash
go test -p 2 -count=1 ./internal/cli/ -run 'TestApp_annotateScopes|TestApp_runList|TestApp_runMachineEnvs|TestGuideTopics|TestApp_runLLM'
go test -p 2 -count=1 ./internal/cli/ ./internal/envscope/
go vet ./internal/cli/ && test -z "$(go tool golangci-lint fmt --diff ./internal/cli/...)"
go tool golangci-lint run ./internal/cli/...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
```

---

## T2 — Rust model: decode, bound and serialize the scope fields

Depends on: none (codes against the shared shape). ∥ T1, T3.

Files:
- `desktop/src-tauri/src/model.rs`
- `desktop/src-tauri/src/tray/attention.rs`
- the other 7 `Entry` literals: `reviews/ask.rs`, `reviews/fix.rs`,
  `reviews/runs.rs`, `reviews/view.rs`, `tray/attention.rs` (test helper),
  `tray/watcher.rs` (constraints §3; `grep -rn "snapshot_stale:" desktop/src-tauri/src`).

Code (model.rs):

```rust
/// Most scope environments kept per entry (envscope.MaxEntries).
const MAX_SCOPE_ENVS: usize = 64;
/// Longest environment name, id or workspace id kept.
const MAX_SCOPE_FIELD: usize = 128;

/// A cross-environment child's parent, from `ws list`'s `scope_parent` (ADR 0082).
#[derive(Debug, Deserialize)]
pub struct WsScopeParent { pub environment: String, pub workspace: String }

/// One environment of a workspace's scope, from `ws list`'s `scope`.
#[derive(Debug, Deserialize)]
pub struct WsScopeEnv {
    pub id: String,
    pub name: String,
    pub state: String,
    #[serde(default)]
    pub current: bool,
}

// WsWorkspace gains:
#[serde(default)] pub scope_parent: Option<WsScopeParent>,
#[serde(default)] pub scope: Option<Vec<WsScopeEnv>>,

#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ScopeParent { pub environment: String, pub workspace_id: String }

#[derive(Debug, Clone, Serialize, PartialEq)]
pub struct ScopeEnv { pub id: String, pub name: String, pub state: String, pub current: bool }

// Entry gains (serialized as scopeParent, scope):
pub scope_parent: Option<ScopeParent>,
pub scope: Option<Vec<ScopeEnv>>,

/// Whether s is non-empty, at most MAX_SCOPE_FIELD bytes, with no control character.
fn plain_field(s: &str) -> bool
/// The parent, or None when a field isn't plain.
fn scope_parent_of(parent: Option<WsScopeParent>) -> Option<ScopeParent>
/// At most MAX_SCOPE_ENVS items whose id and name are plain; a state other
/// than online, offline or removed reads "unknown"; None when nothing is left.
fn scope_of(scope: Option<Vec<WsScopeEnv>>) -> Option<Vec<ScopeEnv>>
```

`workspace_entry` sets both from `ws.scope_parent` / `ws.scope` through
these; `session_entry` and the 7 other literals set `None`.
`tray::attention::is_tracked` adds `&& entry.scope_parent.is_none()`.

Tests (inline `#[cfg(test)]`, existing style in `model.rs`):
- a remote entry with `scope_parent` and `scope` → `Entry` has them; serialized JSON has `"scopeParent":{"environment":"local","workspaceId":"plan"}` and `"scope":[{"id":..,"name":..,"state":..,"current":..}]`
- missing fields (older `ws`) → both `None`, serialized as `null`
- 70 scope items → 64 kept; an item with a 129-byte name, an empty id, or a `\n` is dropped; `state: "weird"` → `"unknown"`; all dropped → `None`
- `scope_parent` with an empty workspace or a control character → `None`
- `tray/attention.rs`: `is_tracked` is false for an agent with `scope_parent` set (next to the `parent_id` test at ~377)

Acceptance: no change to any existing test; the online feature builds.

Verify:
```bash
cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings
cd desktop/src-tauri && cargo test -j 2 --lib model && cargo test -j 2 --lib tray && cargo test -j 2 --lib reviews
cd desktop/src-tauri && cargo clippy --no-default-features --features online --all-targets -- -D warnings
```

---

## T3 — TS type mirror, fixtures and the screenshot mock (`small`)

Depends on: none. ∥ T1, T2.

Files: `desktop/src/api.ts`, `desktop/src/test/fixtures.ts`,
`desktop/test/tauri-mock.js`, `desktop/README.md` (mock paragraph only).

1. `api.ts`: add `ScopeParent`, `ScopeState`, `ScopeEnvironment` (shared
   shapes above, with one-line doc comments naming ADR 0082) and the two
   `Entry` fields after `snapshotStale`.
2. `fixtures.ts` `entry()`: `scopeParent: null, scope: null`.
3. `tauri-mock.js` `entry()`: `scopeParent: null, scope: null` defaults
   (`old-rig` is only a removed id in a scope; add no environment for it). Append to
   `data.entries` (the listing at ~line 710), using `entry`/`remoteEntry`:

```js
entry({
  workspaceId: "plan-scope-4d5e6f7a", sessionId: "4d5e6f7a", path: "/w/plan-scope-4d5e6f7a",
  status: "working", intent: "Compare each machine's private IP",
  rollup: { total: 3, done: 1, fraction: 1 / 3 },
  scope: [
    { id: "local", name: "local", state: "online", current: true },
    { id: "env_7f3a", name: "linux-server", state: "online", current: false },
    { id: "env_0c1d", name: "old-rig", state: "removed", current: false },
    { id: "env_b20d", name: "win-rig", state: "offline", current: false },
  ],
}),
remoteEntry("linux-server", {
  workspaceId: "ip-linux-8a9b0c1d", sessionId: "8a9b0c1d", status: "done", result: "PASS",
  intent: "Report the private IP", rollup: leafRollup(true),
  scopeParent: { environment: "local", workspaceId: "plan-scope-4d5e6f7a" },
  scope: [
    { id: "env_7f3a", name: "linux-server", state: "online", current: true },
    { id: "env_b20d", name: "win-rig", state: "offline", current: false },
  ],
}),
remoteEntry("win-rig", {
  workspaceId: "ip-win-2e3f4a5b", sessionId: "2e3f4a5b", status: "idle",
  environmentOnline: false, snapshotStale: true, intent: "Report the private IP",
  scopeParent: { environment: "local", workspaceId: "plan-scope-4d5e6f7a" },
}),
remoteEntry("win-rig", {
  workspaceId: "ip-probe-6c7d8e9f", sessionId: "6c7d8e9f", status: "idle",
  environmentOnline: false, snapshotStale: true, intent: "Probe the route from win-rig",
  scopeParent: { environment: "linux-server", workspaceId: "ip-linux-8a9b0c1d" },
}),
```

   Check `leafRollup` is in scope at that point (it is a hoisted function).
   Mirror the same four entries in no other file.
4. `README.md` "Screenshots without the backend": one paragraph: "`plan-scope`
   has an environment scope (local, linux-server, win-rig offline, old-rig
   removed) and three children on other environments from `ws machine spawn
   --env`: `ip-linux` on linux-server (online, done), `ip-win` on win-rig
   (offline), and `ip-probe` on win-rig, started by `ip-linux`."

Acceptance: `tsc` passes (no other `Entry` literal exists outside factories;
if one does, add the two nulls there and list it in the report); the whole
vitest suite passes unchanged; `node --check test/tauri-mock.js` passes.

Verify (Node 22, constraints §1):
```bash
cd desktop && npx tsc --noEmit && node --check test/tauri-mock.js
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000
```

---

## T4 — tree links: remote children under their parent

Depends on: T3.

Files: `desktop/src/features/agents/status.ts` + `status.test.ts`,
`tree.ts` + `tree.test.ts`, `Sidebar.tsx` + `Sidebar.test.tsx`,
`EntryRow.tsx` + `EntryRow.test.tsx`, `ChildrenCard.test.tsx`.

Code:

```ts
// status.ts
/** Whether child was started from parent: by parentId on the same environment,
 * or, with no parentId, by its scopeParent (ADR 0082). Never its own parent. */
export const isChildOf = (child: Entry, parent: Entry): boolean
// childrenOf(entries, entry) = entry.workspaceId === null ? [] : entries.filter((e) => isChildOf(e, entry)).toSorted(byRecency)

// tree.ts
export const parentOf = (entries, entry) => entries.find((e) => isChildOf(entry, e))   // undefined for a root
/** Whether entry has a parent link, listed or not. */
export const hasParent = (entry: Entry): boolean => entry.parentId !== null || entry.scopeParent !== null;
```

`isChildOf(child, parent)`: `parent.workspaceId !== null && child.key !== parent.key` and
either `child.parentId !== null && child.parentId === parent.workspaceId && child.environment === parent.environment`,
or `child.parentId === null && child.scopeParent !== null && child.scopeParent.workspaceId === parent.workspaceId && child.scopeParent.environment === parent.environment`.

`EntryRow` gains `readonly parentEnvironment?: string | null` (default
null): set only when the row's parent is on **another** environment.
When set:
- the environment badge's `title` is `Runs on ${entry.environment}; started from ${parentEnvironment} with ws machine spawn --env`;
- the summary prefix is `↳ ${parentName} on ${parentEnvironment} · ` when `parentName` is given, else `from ${parentEnvironment} · `.

`Sidebar`: `TreeList` and `TreeItem` take `readonly parentEnvironment?: string`;
a node's child list gets `parentEnvironment={node.entry.environment}`;
`TreeItem` passes `parentEnvironment` to `EntryRow` only when it differs from
`node.entry.environment`. The Needs input group passes
`parentEnvironment` = `parentOf(entries, entry)?.environment` when it differs.

Tests:
- `status.test.ts`: `isChildOf` table (same-env parentId; scopeParent match;
  scopeParent env mismatch; parentId wins over scopeParent; never itself);
  `childrenOf` includes a scopeParent child; `descendantsOf` walks
  local → linux-server → win-rig.
- `tree.test.ts`: `buildForest` nests a remote child under a local parent and
  a remote → remote grandchild; a scopeParent naming an unlisted parent stays a
  root; a cycle (A.scopeParent → B, B.scopeParent → A) terminates and lists
  both; `ancestorKeys` follows scopeParent; `hasParent`.
- `EntryRow.test.tsx`: the `from linux-server ·` prefix and the badge `title`.
- `Sidebar.test.tsx`: rendering the T3-style entries, `ip-win` is inside
  `plan-scope`'s child list (`within(...).getByRole("button", { name: /ip-win/ })` after expanding).
- `ChildrenCard.test.tsx`: the card lists a cross-environment child with its badge.

Acceptance: all existing tests in the five files pass unedited.

Verify:
```bash
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/agents
```

---

## T5 — pane, root bar and Remove dialog for cross-environment children

Depends on: T4.

Files: `desktop/src/features/agents/EntryPane.tsx` + `EntryPane.test.tsx`,
`RootProgress.tsx` + `RootProgress.test.tsx`, `RemoveDialog.tsx` (its tests
live in `EntryPane.test.tsx`).

1. `EntryPane`: replace the inline parent finder (~line 167) with
   `useAppSelector((s) => parentOf(s.agents.entries, entry))`.
   `const crossParent = parent !== undefined && parent.environment !== entry.environment;`
   - replace the note at ~line 518: if `crossParent` →
     `Started on ${entry.environment} from ${parent.name} on ${parent.environment}`;
     else if `remote && parent !== undefined` → the existing `Runs on …, like its parent`;
     else if `parent === undefined && entry.scopeParent !== null` →
     `Started from ${entry.scopeParent.workspaceId} on ${entry.scopeParent.environment}`.
   - the parent chip's `title` when `crossParent`: `Started from this workspace; removed by the controller after it is removed`.
2. `RootProgress`: `if (hasParent(entry) || rollup === null) return null;`
3. `RemoveDialog`:
   - `const swept = (d) => d.depth === 1 && d.entry.parentId === null && d.entry.scopeParent !== null;`
     (direct cross-environment children; design §4).
   - a row is drawn `kept` only when `keepChildren && !swept(d)`.
   - `removing = keepChildren ? 1 + descendants.filter(swept).length : descendants.length + 1`.
   - when any descendant is cross-environment (`d.entry.environment !== entry.environment`),
     a `DialogContentText` after the checkbox:
     `Workspaces on other environments (${names}) are removed by the controller about a minute after this one, once their environment is online, even if you keep the others.`
     `names` = the distinct environments, sorted, comma-separated.

Tests:
- pane of `ip-win` (parent `plan-scope` local listed): note `Started on win-rig from plan-scope on local`; chip `↳ plan-scope` selects the parent.
- pane with an unlisted scope parent: note `Started from plan-x on local`, no chip.
- same-env remote child still shows `Runs on linux-server, like its parent` (existing test unchanged).
- `RootProgress` renders nothing for an entry with only `scopeParent`.
- Remove dialog of `plan-scope`: lists `ip-linux`, `ip-win`, `ip-probe`; shows the note with `linux-server, win-rig`; with Keep ticked the button reads `Remove 3 workspaces` (itself + the two direct cross-env children) and `ip-linux`/`ip-win` rows are not `kept`.

Acceptance: existing tests pass unedited.

Verify:
```bash
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000 src/features/agents
```

---

## T6 — Scope badges in the pane, and README (`small`)

Depends on: T5.

Files: `desktop/src/features/agents/ScopeBadges.tsx` (new),
`ScopeBadges.module.css` (new), `ScopeBadges.test.tsx` (new),
`EntryPane.tsx` (+ one test in `EntryPane.test.tsx`), `desktop/README.md`.

```tsx
export interface ScopeBadgesProps { readonly scope: readonly ScopeEnvironment[]; }
/** The environments an agent may start children on, each with its state (ADR 0082). */
export const ScopeBadges = ({ scope }: ScopeBadgesProps): JSX.Element
```

- Markup: `<span className={styles.scope}><span className={styles.label}>Scope</span><ul aria-label="Environments it may start children on" className={styles.list}>` then one
  `<li><EnvBadge label=... warn={state !== "online"} title=... /></li>` per item, in the given order.
- Label: `name` when online, else `${name} · ${state}`.
- Title: online `${name} is online`; offline `${name} is offline; a child can't start there now`;
  removed `${name} was removed with ws env rm; a child can't start there`;
  unknown `${name}'s state is unknown`. When `current`, append ` (its own environment)`.
- CSS: `.scope { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 4px; min-width: 0; }`,
  `.list { display: contents; list-style: none; margin: 0; padding: 0; }`, `.label` muted
  like the pane's `.note`. No fixed widths.
- `EntryPane`: render `{entry.scope !== null && <ScopeBadges scope={entry.scope} />}` right after the parent chip.
- `README.md`, section "Environments", after the Agents-list paragraph, a
  subsection `### Environment scopes`: two sentences (remote children of
  `ws machine spawn --env` sit under their parent and say where it runs; an
  agent's pane shows its scope with each environment's state) and a table of
  four images, exactly these names (the screenshot pass creates them):
  `docs/scope-tree.png` (Agents at 1100 px, `plan-scope` expanded),
  `docs/scope-tree-mobile.png` (360 px), `docs/scope-pane.png` (`plan-scope`
  pane: Scope line and children card), `docs/scope-remove.png` (Remove
  dialog with the note). Then: "Not shown: a refusal for an environment out
  of scope; it goes to the agent that ran `ws machine spawn --env`."

Tests (`ScopeBadges.test.tsx`): four states' labels and titles; the current
suffix; order kept; `EntryPane` shows the list for `plan-scope` and nothing
for an entry with `scope: null`.

Acceptance: whole suite passes.

Verify:
```bash
cd desktop && npx tsc --noEmit
cd desktop && npx vitest run --maxWorkers=2 --testTimeout=60000
```

---

## After T6 (orchestrator, not a task)

Screenshot pass with `desktop/test/tauri-mock.js` (constraints §1,
Playwright): the four PNGs above at the stated widths. Layout checks: at
360 px no horizontal scroll (`document.documentElement.scrollWidth <=
innerWidth`), the Scope badges wrap inside the pane, the tree row's `↳ … on
local ·` prefix ellipsizes without overlapping the age, tap targets of the
row and the chevron stay ≥ 40 px.
