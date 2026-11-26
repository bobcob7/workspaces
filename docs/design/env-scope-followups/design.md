# Design: environment scope follow-ups (rollup, `ws list` nesting, New agent scope)

Implements [ADR 0083](../../adrs/0083-env-scope-followups.md), on top of
[env-scope](../env-scope/design.md) (#89) and
[env-scope-ui](../env-scope-ui/design.md) (#90). Environment facts, timings
and pinned values are in [constraints.md](constraints.md); the build order is
in [tasks.md](tasks.md).

Stack (each a PR on the one before):

| Order | Branch | Feature | Why here |
| --- | --- | --- | --- |
| 1 | `feat/env-scope-f2-remote-rollup` | F2: remote children in rollups | Backend and wire (snapshot values, a reply field). Riskiest, so it soaks longest; F3 and F1 don't need it |
| 2 | `feat/env-scope-f3-list-nesting` | F3: `ws list` table and `--parent` | CLI only, touches the same `internal/cli/list*.go` files as F2, so it goes after F2 to avoid rebasing over it |
| 3 | `feat/env-scope-f1-new-agent-scope` | F1: grant a scope from New agent | Mostly UI; independent of F2/F3. Last, so the screenshot pass runs on the final mock (F2 also edits `desktop/test/tauri-mock.js`), and so a stuck UI review can't hold the backend PRs |

Terms are 0082's: **controller**, **ledger**
(`CONFIG_DIR/environments/scopes.json`), **environment id** (`local` or
`env-…`), **snapshot** (a remote environment's last `ws list` objects).

---

## F2. Remote children in a local parent's rollup

### F2.1 Today

| Where | Code | Remote children |
| --- | --- | --- |
| `ws list --json` `rollup` | `list.go` `listedWorkspaces` → `workspaceProgress` → `progressTreeOf` → `machine.ProgressTree` | ignored |
| `ws machine children` (local child's `rollup`) | `machine_result.go` `machineChild` → `childProgress` | ignored |
| `ws machine children` (remote child's `rollup`) | `machine_remote.go` `remoteMachineChild` → `machine.record` | its own environment's count only |
| `ws machine wait` items | `machine_wait.go` (~l. 398) → `childProgress` | ignored |
| `ws machine progress show` | `machine_progress.go` show → `callerProgress`, then `nestRemoteProgress` adds leaves | leaves shown, **ancestors' rollups not raised** |
| `ws machine progress set/step` | `updateProgress` → `callerProgress` | ignored (unchanged by F2) |
| Remote snapshot objects | `env_agent.go` `agentSnapshots.Workspaces` → `listWorkspaces` | `progress`, `rollup` always `null` |
| Desktop | `RootProgress.tsx` reads `entry.rollup` from `ws list --json` | follows `ws list` |

### F2.2 Snapshot (agent side)

`agentSnapshots.Workspaces` builds the objects with `listedWorkspaces` (each
gets `progress` and `rollup` as `ws list --json` does), then sets every
`progress.note` to `null`. Example object (other fields unchanged):

```json
{"id": "ip-win-2e3f4a5b", "parent": null,
 "progress": {"name": "private IP", "total": 2, "done": 1, "note": null, "updated_at": "2026-11-25T10:02:00Z"},
 "rollup": {"total": 2, "done": 1, "fraction": 0.5}}
```

Budget: about 200 bytes more per workspace (measured ≤ 196 with a note,
constraints §1). The 256 KiB snapshot cap is unchanged; the agent still
drops an oversize snapshot whole and logs `snapshot too large`.

### F2.3 `envscope.Merger` (pure)

File `internal/envscope/rollup.go`:

```go
const (
    // MaxRollupSteps caps a remote base and every merged sum (ADR 0083).
    MaxRollupSteps = 1_000_000_000
    // MaxMergeDepth is the most ledger hops a merge follows.
    MaxMergeDepth = 64
    // MaxListedPerEnvironment is the most snapshot workspaces read per environment.
    MaxListedPerEnvironment = 4096
    // maxListedBytes bounds the snapshot array ListedOf decodes.
    maxListedBytes = 1 << 20
    // maxListedID is the longest workspace id ListedOf keeps.
    maxListedID = 128
)

// Steps is a rollup: the steps planned and done in a workspace and below it.
type Steps struct {
    Total int `json:"total"`
    Done  int `json:"done"`
}

// Add is s plus o, saturating at MaxRollupSteps, with Done never above Total.
func (s Steps) Add(o Steps) Steps

// Valid reports whether 0 ≤ Done ≤ Total ≤ MaxRollupSteps.
func (s Steps) Valid() bool

// Listed is one workspace of an environment: its same-environment parent
// ("" for none) and its base rollup, as its own environment counted it.
type Listed struct {
    Environment string // "local" or an environment id
    Workspace   string
    Parent      string
    Base        Steps
}

// ListedOf decodes a snapshot's workspaces array (ws list objects) for
// environment envID: id, parent and rollup only. Over maxListedBytes is an
// error; entries past MaxListedPerEnvironment, with an empty, over-128-byte
// or control-character id, are skipped; a rollup that isn't Valid is zero and
// its id is returned in invalid.
func ListedOf(envID string, workspaces json.RawMessage) (listed []Listed, invalid []string, err error)

// Merger merges ledger children into rollups (ADR 0083 D1).
type Merger struct { /* unexported */ }

// NewMerger indexes listed (by environment and workspace; a later duplicate
// key is ignored) and the ledger entries that have a parent.
func NewMerger(listed []Listed, ledger []Entry) *Merger

// Of is the merged rollup of a listed workspace: its base plus Below over
// itself and its same-environment descendants. ok is false when it isn't
// listed.
func (m *Merger) Of(env, workspace string) (Steps, bool)

// Below is the sum of the merged rollups of the ledger entries whose parent
// is (env, w) for any w in workspaces. A ledger entry that isn't listed adds
// only its own Below (its base is zero).
func (m *Merger) Below(env string, workspaces []string) Steps
```

Rules:

1. Same-environment descendants are found from `Listed.Parent` within one
   environment, breadth-first with a seen set (cycles end).
2. Each ledger key `(environment, workspace)` is merged at most once per
   call to `Of`/`Below` (memoized; a key met again while it is being merged
   adds zero), and at most `MaxMergeDepth` ledger hops deep (deeper adds
   zero).
3. A ledger entry whose environment equals its parent's environment is
   ignored (0082 never creates one; a hand-edited one must not count twice).
4. No path is built from any id; ids are map keys only.

### F2.4 Controller: `scope.children` gains `rollup`

`internal/envproto/scope.go`:

```go
type ScopeChild struct {
    Environment string          `json:"environment"`
    Workspace   string          `json:"workspace"`
    Parent      string          `json:"parent"`
    Role        *string         `json:"role"`
    Entry       json.RawMessage `json:"entry"`
    // Rollup is the child's steps with everything below it on every
    // environment, merged by the controller; null when its environment's
    // snapshot doesn't list it (ADR 0083).
    Rollup *ScopeRollup `json:"rollup"`
}

type ScopeRollup struct {
    Total int `json:"total"`
    Done  int `json:"done"`
}
```

Reply (one child):

```json
{"children": [{"environment": "linux-server", "workspace": "ip-linux-8a9b0c1d", "parent": "plan-scope-4d5e6f7a",
  "role": null, "entry": {"id": "ip-linux-8a9b0c1d", "...": "..."}, "rollup": {"total": 3, "done": 2}}]}
```

`Controller.scopeChildren` (`internal/environment/scope.go`) builds one
`Merger` per call: every **enrolled** record's snapshot (`c.store.Snapshot`)
through `envscope.ListedOf(rec.ID, snap.Workspaces)`, and `c.ledger.List()`.
Each returned child's `Rollup` = `Of(e.Environment, e.Workspace)`, `nil`
when not listed. A snapshot that can't be read or decoded is skipped (its
children get `rollup: null`); no new error.

### F2.5 CLI: machine commands

`remoteChild` gains `rollup *envscope.Steps` (from the reply; `nil` when
absent, an older controller, or not `Valid()`).

```go
// internal/machine/progress.go
// AddRemote adds steps to the node with id parent and to each of its
// ancestors in n, as a child on another environment (ADR 0083). It reports
// whether it found parent.
func (n *ProgressNode) AddRemote(parent string, steps Rollup) bool

// internal/cli/machine_progress.go (new parameter)
func (a *App) childProgress(store *machine.Store, root string, list []workspace.Workspace, id string, remote []remoteChild) (*progressJSON, *rollupJSON)
```

| Command | Change |
| --- | --- |
| `ws machine children [--all]` | local child X: `rollup` = local + Σ `rc.rollup` for remote children whose parent is X or below. Remote child: `rollup` = `rc.rollup` when non-null, else the `machine.record` rollup (today's). The remote list is fetched **before** the local items are built |
| `ws machine wait` | same per item; the remote list is fetched again once, right before the items are built (the start-of-wait list is stale after a long wait). No extra call when the first fetch made none (no ledger, default scope) |
| `ws machine progress show` | each remote leaf's `rollup` = `rc.rollup` when non-null, else `machine.record`'s; its `rollup` is added to every ancestor node and to the top-level `rollup` |
| `ws machine progress set/step` | unchanged (local only) |

`rollupJSON` keeps its shape `{"total", "done", "fraction"}`; `fraction` is
recomputed after adding. A remote child whose rollup is `null` adds nothing.

Example, `ws machine progress show` on `plan-scope` (own 3/1, remote
`ip-linux` 2/2 with a `win-rig` grandchild `ip-probe` 1/0, remote `ip-win`
2/1, stale):

```json
{"id": "plan-scope-4d5e6f7a",
 "progress": {"name": "compare IPs", "total": 3, "done": 1, "note": null, "updated_at": "2026-11-25T10:00:00Z"},
 "rollup": {"total": 8, "done": 4, "fraction": 0.5}, "warnings": [],
 "tree": {"id": "plan-scope-4d5e6f7a", "progress": {"...": "..."}, "rollup": {"total": 8, "done": 4, "fraction": 0.5},
  "children": [
   {"id": "ip-linux-8a9b0c1d", "progress": {"total": 2, "done": 2, "...": "..."}, "rollup": {"total": 3, "done": 2, "fraction": 0.6666666666666666}, "children": []},
   {"id": "ip-win-2e3f4a5b", "progress": {"total": 2, "done": 1, "...": "..."}, "rollup": {"total": 2, "done": 1, "fraction": 0.5}, "children": []}]}}
```

`ip-probe` is not a leaf of `plan-scope`'s tree (it is `ip-linux`'s child,
which `scope.children` doesn't list for `plan-scope`); it is counted inside
`ip-linux`'s merged rollup.

### F2.6 `ws list --json`

When `envscope.LedgerExists` (else nothing changes, no new I/O), after
`annotateScopes`:

```go
// internal/cli/list_rollup.go
// mergeRollups adds remote children's steps to every entry's rollup
// (ADR 0083 D1): local entries get Below over themselves and their
// descendants; remote entries get the merger's Of.
func (a *App) mergeRollups(entries []listEntryJSON)
```

- Listed set: **every** enrolled environment's snapshot (not only those
  `--env` selected, and hidden entries included), each read once, through
  `envscope.ListedOf`; and the ledger (`envscope.NewLedger(...).List()`).
- Local entry E (`Environment == "local"`): `rollup` = E's local rollup
  `.Add(m.Below("local", [E] + descendantIDs(list, E)))`.
- Remote entry R on environment name N (id from the store):
  `rollup` = `m.Of(id, R)`; `null` when `Total == 0`.
- A `Steps` that isn't `Valid()` in a snapshot: warning
  `ignoring the rollup of <id> on <env>: out of range` (once per entry).
- Ledger unreadable: rollups stay local-only; warning is #90's
  `couldn't read the scope ledger: <err>` (only once per run).
- Table and `-q` are unchanged by F2.

### F2.7 Desktop

No code change. The mock's `plan-scope` family shows the merged numbers
(tasks F2-T4) so the screenshot shows `4/8 steps`.

---

## F3. `ws list` table and `--parent`

### F3.1 Table

With a ledger, `runList`'s table branch fills `ScopeParent` (no `scope`, so
no presence call) and orders all rows as one forest:

```go
// listRow gains:
parent      string // same-environment parent id, "" for none
scopeParent string // "<env name>/<workspace>" from scope_parent, "" for none

// internal/cli/list_tree.go (new)
// crossOrder orders rows as one tree across environments: a row's parent is
// its same-environment parent when listed, else its scope parent when
// listed; others are roots in input order. Depth is recomputed. Cycles end
// (orderTree's seen set).
func crossOrder(rows []listRow) []listRow
// rowKey is "<env>/<id>"; env is the row's environment name, "local" here.
func rowKey(env, id string) string
```

`orderTree` is reused with keys `rowKey(env, id)`. `envText` adds
`" (offline)"` to `env` for display, so the key uses the bare name (add a
`envName` field to `listRow`, or keep `env` bare and add the suffix in
`printList`; T1 picks the second: `listRow.stale bool`).

Example (`ws list` on the controller):

```
ID                       ENV                 NAME         CREATED   SESSION                    REPOS
plan-scope-4d5e6f7a      local               plan-scope   5m ago    agent 4d5e6f7a: running
└ ip-linux-8a9b0c1d      linux-server        -            4m ago    agent 8a9b0c1d: done
  └ ip-probe-6c7d8e9f    win-rig (offline)   -            3m ago    agent 6c7d8e9f: idle, stale
└ ip-win-2e3f4a5b        win-rig (offline)   -            4m ago    agent 2e3f4a5b: idle, stale
api-fix-1a2b3c4d         local               api-fix      2h ago    agent 1a2b3c4d: running    api
└ api-fix-t1-impl-…      local               -            2h ago    …
build-rig-9c0d1e2f       win-rig (offline)   -            1d ago    agent 9c0d1e2f: idle
```

- No ledger: byte-identical to today (pinned by the existing table tests).
- A child whose parent isn't listed (filtered by `--env`, `--repo`, hidden)
  stays a root among its environment's rows.
- `--json` entry order is unchanged.

### F3.2 `--parent <id>`

```
ws list [--env <name>] --parent <id> [--json]
```

- The parent's environment is found as today (`parentEnvironment`; `--env`,
  at most one, names it).
- Read environments: the parent's, plus, when a ledger exists, the
  environment of each ledger entry whose parent is `(parent env id, id)`
  (deduplicated, `local` first then by name).
- Kept entries: same-environment entries with `parent == id` (today), plus
  remote entries whose ledger entry's parent is `(parent env id, id)`.
- The `usage` check "`--parent` takes one `--env`" counts `opts.envs`, not
  the environments read.
- `-q --parent` stays local-only (unchanged; `-q` reads local alone).
- JSON: the same objects as `ws list --json` (with `environment`,
  `scope_parent`); no new field.

```go
// internal/cli/list_env.go
// parentFilter is what --parent keeps: same-environment children of id, and
// ledger children whose parent is id on its environment.
type parentFilter struct {
    env    string // the parent's environment name
    envID  string // "local" or its id
    id     string
    ledger map[ledgerKey]envscope.Entry // nil without a ledger
}
func (f *parentFilter) keeps(env, envID string, entryParent *string, id string) bool
```

(`parentFilter` the method on `App` today returns the parent id; T2 renames
it `parentID` to free the name.)

---

## F1. Granting a scope from New agent

### F1.1 Path

```
NewAgentDialog ─(ScopeField state)─▶ createAgent thunk ─▶ api.createAgent(NewAgent{…, envScope})
  ─▶ Tauri invoke("create_agent", {request})  ┐
  ─▶ Workspace Online POST /api dispatch       ┘─▶ commands::create_agent
       validate_env_scope(&names)?  ─▶ new_agent_args ─▶ ws claude [--env=<e>] --env-scope=<a>,<b> … 
       ─▶ (local) grantScope / (remote) remoteGrant → scope.spawn (ledger)        [unchanged CLI]
```

No new Tauri command, no CLI change. `config_list` (existing) gives
`environments.default_scope`; `environments_list` (existing, polled) gives
the environments.

### F1.2 Types

```ts
// desktop/src/api.ts, NewAgent gains:
/** Environments the agent may start children on, its own first; left out when no environment is enrolled (ADR 0083). */
readonly envScope?: readonly string[];
```

```rust
// desktop/src-tauri/src/commands.rs, NewAgent gains:
/// Environments the agent may start children on (`--env-scope`); None sends no flag (ADR 0083).
#[serde(default)]
pub env_scope: Option<Vec<String>>,

/// Most names in a scope (envscope.MaxEntries).
const MAX_SCOPE_NAMES: usize = 64;
/// Longest environment name (config's default_scope rule).
const MAX_SCOPE_NAME_BYTES: usize = 64;

/// Checks a requested scope: 1-64 names, each 1-64 bytes with no whitespace,
/// comma, control character or leading '-', none repeated.
pub fn validate_env_scope(names: &[String]) -> Result<(), AppError> // code "usage"
```

`new_agent_args` adds `--env-scope=<names joined with ",">` right after
`--env=<e>` (or after `claude` for local), only when `env_scope` is `Some`.
`create_agent` calls `validate_env_scope` first when `Some`.
`reviews/fix.rs` `new_agent` sets `env_scope: None`.

Wire (`invoke("create_agent", {request})`):

```json
{"request": {"name": "compare-ips", "specs": [], "prompt": "Compare each machine's private IP",
  "model": "", "permissionMode": "", "offline": false, "runner": "claude", "endpoint": "",
  "envScope": ["local", "linux-server", "win-rig"]}}
```

→ `ws --json claude --env-scope=local,linux-server,win-rig --name compare-ips -- "Compare each machine's private IP"`

Remote target: `"environment": "linux-server", "envScope": ["linux-server"]`
→ `ws --json claude --env=linux-server --env-scope=linux-server --name …`
(own environment only; `default_scope` not applied).

### F1.3 The field

`desktop/src/features/create/ScopeField.tsx`:

```ts
export interface ScopeChoice {
  readonly name: string;           // "local" or an enrolled environment's name
  readonly status: "online" | "offline" | "unknown";
  readonly own: boolean;           // the agent's target environment: checked and disabled
}
export interface ScopePick {
  readonly visible: boolean;                 // same as the Environment picker: some environment enrolled
  readonly choices: readonly ScopeChoice[];  // own first, then local, then by name
  readonly selected: readonly string[];      // own first, then by name
  readonly leftOut: readonly string[];       // default_scope names not enrolled
  readonly tooMany: boolean;                 // selected.length > 64
  readonly toggle: (name: string) => void;
}
/** The scope a new agent gets; starts from its own environment plus default_scope on every opening. */
export const useScopePick = (open: boolean, target: string): ScopePick
export const ScopeField = ({ pick }: { readonly pick: ScopePick }): JSX.Element | null
```

Pure helpers in `desktop/src/features/environments/environments.ts`:

```ts
/** local and each enrolled environment, as scope choices; pending ones are left out. */
export const scopeChoices = (items: readonly RemoteEnvironment[], target: string): readonly ScopeChoice[]
/** default_scope's names split into the enrolled ones (or local) and the rest. */
export const splitDefaultScope = (defaults: readonly string[], items: readonly RemoteEnvironment[]): { readonly kept: readonly string[]; readonly leftOut: readonly string[] }
/** environments.default_scope from a ws config list; [] when absent or not a list of strings. */
export const defaultScopeOf = (listing: ConfigListing): readonly string[]
```

Behaviour:

| Situation | Shown | Sent |
| --- | --- | --- |
| No environment enrolled | nothing (as today) | no `envScope` |
| `default_scope` empty | own environment checked (disabled), others unchecked | `[own]` |
| `default_scope = ["linux-server","old-rig"]`, old-rig not enrolled | own + linux-server checked; warning `default_scope lists old-rig, which isn't enrolled; it's left out.` | `[own, "linux-server"]` |
| Person checks `win-rig` (offline) | checked; info `win-rig is offline: a child can start there once it's back online.` | includes `win-rig` |
| Status `unknown` (daemon down) | label `<name> · status unknown`; same info line | allowed |
| Pending environment | not offered | — |
| Target changes local → linux-server | linux-server becomes own (checked, disabled); `local` stays checked only if the person (or default_scope) checked it | `["linux-server", …]` |
| Dialog closed and reopened | reset to own + default_scope | — |
| `config_list` fails | own only, warning `couldn't read default_scope: <message>` | `[own]` |
| More than 64 selected | Start disabled, error text `At most 64 environments.` | — |

Copy:

- Legend: `May start children on`.
- Option label: the name; `win-rig · offline`, `lab · status unknown`;
  own: `local · its own environment`.
- Helper under the group: `Only you grant this: the agent and its children can narrow it, never widen it. Starts from default_scope in config.toml.`

Markup: MUI `FormControl component="fieldset"` + `FormLabel component="legend"`
+ `FormGroup` of `FormControlLabel`/`Checkbox` (checkbox tap target 42 px),
`FormHelperText`, MUI `Alert` for warnings. Placed right after
`EnvironmentField`.

Error hints (`NewAgentDialog` `errorHint`):

- `environment_unknown`: `An environment in the scope was removed or never enrolled; reopen New agent to refresh the list.`
- `usage` whose message contains `grants a scope`: `ws refuses a scope from inside a workspace; start the desktop app outside one.`

### F1.4 Security properties and tests

| Property (0082) | How F1 keeps it | Test |
| --- | --- | --- |
| Only a person grants | the field is in the person's dialog; `create_agent` is reached only by the desktop window and Workspace Online's logged-in `/api`; `ws claude` still refuses `--env-scope` inside a workspace | Go `TestApp_runClaude` existing "refuses --env-scope inside a workspace" (unchanged) |
| Default is own environment | empty `default_scope` → `[own]`; hidden field → no flag (CLI default) | TS `defaults to its own environment when default_scope is empty` |
| Never widened by agent data | initial state = own + person's `config.toml`; never remembered; never prefilled (`usePrefilledSpecs` sets specs only); review-fix sends `None` | TS `never remembers a widened scope between openings`, `prefilled specs never touch the scope`; Rust `review_fix_agent_never_grants_a_scope` |
| A scope can only narrow below a parent | the desktop starts parentless agents only; `ws claude` from the desktop is outside any workspace | existing Go tests |
| Own environment always in scope | own checked and disabled; Rust refuses an empty list | TS `its own environment is always checked and can't be unchecked`; Rust `validate_env_scope_refuses_an_empty_list` |
| No injected flags | names only from the list; Rust refuses comma, whitespace, control, leading `-`; one `--env-scope=` argument | Rust `validate_env_scope_refuses_unsafe_names`, `new_agent_args_sends_env_scope_as_one_argument` |
| "own only" really means own only | `--env-scope=<own>` skips `default_scope` | Go `TestApp_runClaude` "an --env-scope of local alone skips default_scope", `TestApp_remoteGrant` "its own environment alone grants nothing and skips default_scope" |

Residual (0082/0066): a process of the person's uid can edit `config.toml`
before the dialog opens; the preselected boxes make that visible.

---

## Hostile-input limits (reviewers check these)

| Input | Who controls it | Limit | Where |
| --- | --- | --- | --- |
| Snapshot `workspaces` array | the remote environment | ≤ 1 MiB decoded by `ListedOf` (snapshots are ≤ 256 KiB on receipt), ≤ 4096 entries per environment, id 1-128 bytes, no control characters | F2-T1 |
| Snapshot `rollup` values | the remote environment | used only when `0 ≤ done ≤ total ≤ 1e9`; else zero plus a warning | F2-T1, F2-T4 |
| Snapshot `parent` links | the remote environment | same-environment only; seen set ends cycles | F2-T1 |
| Ledger entries | any process of the uid | existing `envscope` limits (≤ 8 MiB, ≤ 4096, strict decode, validated ids); merge ≤ 64 hops, each entry once; same-environment entries ignored | F2-T1 |
| Merged sums | derived | saturate at 1e9; `done ≤ total` | F2-T1 |
| `scope.children` `rollup` | the controller (trusted, but over a socket) | `Valid()` or ignored | F2-T3 |
| `scope_parent` for nesting | derived from the ledger by `ws` | exact key match; orderTree seen set | F3-T1 |
| `envScope` from the web or the window | the person's client | 1-64 names, each 1-64 bytes, no whitespace/comma/control/leading `-`, no duplicates, non-empty | F1-T2 |
| `default_scope` | `config.toml` (uid-writable) | only enrolled names or `local` are preselected; others shown as left out | F1-T3/T4 |

No path is built from a snapshot id, a ledger id or a scope name in these
features.

## Docs to change

- F2: `internal/cli/guides/ws-machine-children.md`, `ws-machine-wait.md`,
  `ws-machine-progress-show.md`, `ws-machine-progress.md`, `ws-list.md`
  (rollup includes remote children; set/step stay local);
  `docs/tools/ws-machine.md`, `docs/tools/ws-list.md`, `docs/state.md`
  (snapshot carries progress without notes).
- F3: `internal/cli/guides/ws-list.md` (`--parent` spans environments, the
  table nests), `docs/tools/ws-list.md`, `README.md` (one sentence in the
  remote environments section).
- F1: `desktop/README.md` (New agent scope, screenshots),
  `docs/config.md` (`default_scope` is preselected in New agent),
  `internal/cli/guides/ws-claude.md` and `docs/tools/ws-claude.md`
  (`--env-scope <own environment>` grants nothing more and skips
  `default_scope`).
