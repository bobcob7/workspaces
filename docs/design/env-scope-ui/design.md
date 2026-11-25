# Design: environment scopes in the desktop app

Builds the part [ADR 0082](../../adrs/0082-environment-scopes-for-children.md)
left out ("Not in this ADR: the desktop tree showing cross-environment
parents") on top of the backend in [env-scope](../env-scope/design.md)
(PR #89). Environment facts, timings and pinned values are in
[constraints.md](constraints.md); the build order is in [tasks.md](tasks.md).

## 1. What is built

1. **Remote children in the tree.** A child that `ws machine spawn --env <name>`
   started is recorded only in the controller's ledger
   (`CONFIG_DIR/environments/scopes.json`). Its own snapshot entry has
   `parent: null`, so today the desktop shows it as a root. After this feature
   it sits under its parent in the Agents tree, in the parent's "Started from
   this workspace" card, in the Remove dialog's list, and the tray no longer
   counts it as a top-level agent. Its row keeps the environment badge it
   already has, and its summary line says where the parent is:
   `↳ plan-scope on local · Report the private IP`.
2. **Each agent's scope, with state.** An agent whose scope is wider than its
   own environment shows a **Scope** line in its pane: one badge per
   environment, labelled `win-rig · offline`, `old-rig · removed`, or the bare
   name when online. An agent with the default scope (its own environment
   only) shows nothing, as today.

No new Tauri command. Everything arrives through the existing
`list_entries` (`ws list --all --json`), which gains two fields.

## 2. What is cut, and why

| Cut | Why |
| --- | --- |
| Showing an `environment_out_of_scope` refusal | Nothing the desktop runs can get it. The refusal is returned only to `ws machine spawn --env` (an agent's command) and to the controller's `scope.spawn` reply to that agent. The desktop starts agents with `ws claude [--env]`, which never checks a scope, and #89 records no refusal anywhere the desktop can read it (no event, no ledger row, no snapshot field). It shows up in the agent's own transcript, which the desktop already shows for local agents. Showing it would need a new backend record of refusals: not small, and a semantic change. |
| Granting a scope from the desktop (`--env-scope` in New agent) | Not asked for; it is a person's grant UI, with its own validation and copy. A follow-up. |
| Remote children in a local parent's progress rollup | `rollup` is computed by `ws list` from local records; a remote child's progress reaches the parent only via `machine.record` polls inside `ws machine wait`/`children`, which the desktop doesn't run. The child's own bar still shows on its row. Adding it means reading copied-back records in `ws list`: a backend change beyond what the UI needs. |
| `ws list --parent` and the text table nesting remote children | CLI behaviour, not UI. `--parent` keeps meaning same-environment children. |
| Scope in the Agents list rows | Badges for every environment on every row crowd a 360 px row; the pane is enough. |
| Remote → remote children whose parent environment's snapshot is missing | They stay roots (the parent isn't listed); the pane note names the parent instead (§5). |

## 3. Data flow

```
CONFIG_DIR/environments/scopes.json  (ledger, controller only)   ─┐
state/<id>/scope.json                (local workspaces)          ─┤
CONFIG_DIR/environments/*.toml       (env store: id → name, enrolled) ─┤
daemon status presence               (online/offline by name)    ─┘
        │  T1: ws list --json adds scope_parent, scope
        ▼
desktop/src-tauri commands::list → model::WsWorkspace → model::Entry   (T2: decode, bound, serialize camelCase)
        │  list_entries (and Workspace Online's dispatch "list_entries", unchanged)
        ▼
desktop/src/api.ts Entry.scopeParent, Entry.scope                      (T3: type mirror, fixtures, mock)
        ▼
status.ts isChildOf/childrenOf, tree.ts parentOf/hasParent             (T4)
  → Sidebar/EntryRow, ChildrenCard, RemoveDialog, EntryPane, RootProgress
EntryPane → ScopeBadges                                                (T5)
```

### 3.1 `ws list --json` (T1, Go, additive)

Every entry gains two fields, always present (`null` when empty, the repo's
JSON rule):

```json
{
  "id": "ip-win-2e3f4a5b",
  "environment": "win-rig",
  "parent": null,
  "scope_parent": {"environment": "local", "workspace": "plan-scope-4d5e6f7a"},
  "scope": [
    {"name": "win-rig", "id": "env-2b3c4d5e", "state": "offline", "current": true},
    {"name": "linux-server", "id": "env-1a2b3c4d", "state": "online", "current": false}
  ]
}
```

- `scope_parent`: for a **remote** entry with a ledger entry
  `(environment id, workspace)` whose `parent` is non-null: `environment` is
  `"local"` or the parent environment's **current name** from the env store;
  `workspace` is the parent's id. `null` for every local entry, a remote entry
  not in the ledger, a person grant (`parent: null` in the ledger), or a
  parent environment that is no longer in the store.
- `scope`: the entry's scope when it is wider than its own environment, as
  `envproto.ScopeEnv` (`name`, `id`, `state`, `current`), `current` first then
  by name. Local entries: from `state/<id>/scope.json` (`envscope.Read`).
  Remote entries: from the ledger entry's `scope` ids. `null` when there is no
  file or ledger entry, or the scope is the entry's own environment only.
  `state`: `local` is `online`; an id not in the env store, or not
  `enrolled`, is `removed` (name = the name stored in `scope.json`, or the id
  for a ledger scope); else `online` when daemon presence says so, otherwise
  `offline`. `current` is the entry's own environment.
- **Remote values never come from the snapshot.** A remote snapshot is
  decoded into the same struct, so a hostile environment could send its own
  `scope_parent`/`scope`; T1 resets both to `nil` for every remote entry
  before filling them from the ledger.
- Cost: the ledger is read once per `ws list --json`, and only when
  `envscope.LedgerExists`. Presence is asked of the daemon at most once more,
  and only when some scope names a remote environment. With no grant on this
  machine (no ledger, no `scope.json`), `ws list` does no new I/O beyond one
  `stat` per local workspace for `scope.json` and outputs two `null`s per
  entry. Text output (`ws list` table, `-q`) is unchanged.
- Errors: an unreadable or invalid ledger → both fields `null` on remote
  entries and the warning `couldn't read the scope ledger: <err>`; an invalid
  `scope.json` → `scope: null` and `ignoring the scope of <id>: <err>`
  (the wording `workspaceScope` uses). Never an exit-code change.

### 3.2 Rust model (T2)

`WsWorkspace` decodes the two fields with `#[serde(default)]` (an older `ws`
without them still works). `Entry` gains:

```rust
pub scope_parent: Option<ScopeParent>,   // {"environment", "workspaceId"}
pub scope: Option<Vec<ScopeEnv>>,        // [{"id","name","state","current"}]
```

Bounded on the way in (§6). `tray::attention::is_tracked` also requires
`scope_parent.is_none()`, so a remote child doesn't notify as a top-level agent.

### 3.3 TypeScript (T3, T4, T5)

```ts
export interface ScopeParent { readonly environment: string; readonly workspaceId: string; }
export type ScopeState = "online" | "offline" | "removed" | "unknown";
export interface ScopeEnvironment { readonly id: string; readonly name: string; readonly state: ScopeState; readonly current: boolean; }
// Entry gains:
readonly scopeParent: ScopeParent | null;
readonly scope: readonly ScopeEnvironment[] | null;
```

Parent rule (T4), one predicate used by the tree, the children card, the
Remove dialog and the pane: an entry with `parentId` is a child of the
same-environment entry with that workspace id (as today); an entry with
`parentId === null` and a `scopeParent` is a child of the entry whose
`environment` and `workspaceId` match it. `parentId` wins when both are set.

## 4. UI behaviour

### Desktop width (> 720 px, the pane's `PHONE_QUERY` is `(max-width:720px)`)

- **Agents tree**: the remote child is nested under its parent, sorted by
  recency with its siblings; the parent's chevron counts it. Row: status dot,
  name, the environment badge (`win-rig`), the `offline`/`stale` badges as
  today, then `↳ plan-scope on local · <summary>` on the second line. The
  environment badge's `title` on a cross-environment child is `Runs on
  win-rig; started from local with ws machine spawn --env`.
- **Pane** of a cross-environment child: the parent chip `↳ plan-scope`
  (click selects the parent) and the note `Started on win-rig from plan-scope
  on local` instead of `Runs on win-rig, like its parent`. If the parent isn't
  listed, no chip, and the note `Started from plan-scope-4d5e6f7a on local`.
  No root progress bar on a cross-environment child.
- **Pane of a parent**: "Started from this workspace" lists the remote child
  with its environment badge, Open and Remove (Remove already routes
  `ws rm --env`).
- **Remove dialog of a parent**: remote children are listed with the others.
  Their rows are never drawn as "kept", and one line under the list says:
  `Workspaces on other environments (win-rig) are removed by the controller
  about a minute after this one, once their environment is online, even if you
  keep the others.` The button count includes them. Only **direct**
  cross-environment children are swept when "Keep" is ticked (their ledger
  parent is the removed entry); deeper ones belong to a kept child and are
  kept (design 2.6 of env-scope).
- **Scope line** in the pane's details (after the parent chip): the word
  `Scope` and one badge per environment. Online: the name. Otherwise
  `name · offline`, `name · removed`, `name · unknown`, drawn with the warn
  style. The entry's own environment is first. `title` explains each state
  (T5). Hidden when `scope` is null.

### Phone width (≤ 720 px; screenshots at 360 px, Workspace Online and narrow windows)

- Tree rows are unchanged in layout: the badge is already there and the
  `↳ parent on env ·` prefix sits in the summary line, which already
  ellipsizes. Nesting indent is the existing one.
- The Scope badges wrap (`flex-wrap: wrap`, `gap: 4px`); no horizontal scroll
  at 360 px with 4 environments of 12-character names. Badges are not
  interactive, so the 40 px tap-target rule doesn't apply to them; the state
  is in the label text, because a `title` tooltip can't be read on touch.
- The Remove dialog note wraps as body text.

## 5. Failure states

| State | What the user sees |
| --- | --- |
| Child's environment offline / snapshot stale | Child still nested (the ledger link is local); the row is dimmed with `offline`/`stale` as today; actions disabled with the existing reason |
| Parent's environment offline (remote → remote) | Parent's rows are still listed from its last snapshot, so the child stays nested |
| Parent removed, child not yet swept | Child becomes a root; pane note `Started from <id> on <env>` with no chip; it disappears on the sweep (≤ 60 s once online) |
| Parent environment removed (`ws env rm`) | `scope_parent: null` (T1), so the child is a root; it is dropped from the ledger by the sweep |
| Ledger unreadable/invalid | Tree as before this feature (remote children are roots); the listing warning shows in the existing warnings area |
| `scope.json` invalid | No Scope line; warning `ignoring the scope of <id>: …` |
| Scope environment removed | Badge `old-rig · removed` (warn) |
| Daemon down (no presence) | Every remote scope environment reads `offline`, as `ws list` already reports remote entries |
| Older `ws` without the fields | Rust defaults both to `None`; UI identical to today |
| Cycle in links (hostile/buggy ledger: A's parent is B, B's is A) | Both shown; the existing `seen` guards in `buildForest`, `ancestorKeys`, `descendantsOf` stop the loop (T4 adds a test) |

## 6. Hostile-input limits

The new values are produced by the local `ws` from local files, but the
entries they attach to come from remote snapshots, and the ledger is a file
any process of the uid can write. So:

- **T1 (Go)**: remote `scope_parent`/`scope` are always reset before filling
  (snapshot values ignored). The ledger reader already bounds the file
  (≤ 8 MiB, ≤ 4096 entries, validated ids, strict decode); `scope.json` is
  bounded by `envscope.Read` (≤ 16 KiB, ≤ 64 entries). Ids go into no path
  in T1 (`scope.json` is looked up by a local workspace id `ws` itself
  listed).
- **T2 (Rust)**: `scope` takes at most **64** items, the rest dropped; an item
  whose `id` or `name` is empty, over **128 bytes**, or holds a control
  character is dropped; a `state` outside `online|offline|removed` becomes
  `unknown`; an empty result is `None`. `scope_parent` with an empty,
  over-128-byte or control-character `environment` or `workspace` is `None`.
- **T4 (TS)**: lookups are by exact `(environment, workspaceId)` equality, an
  entry is never its own parent (`key` check), and the existing `seen` sets
  end cycles. Labels are rendered as React text (no HTML).
