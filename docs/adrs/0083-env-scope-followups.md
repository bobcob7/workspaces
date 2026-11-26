# 0083. Remote children count in their parent's rollup through the controller's ledger, `ws list` nests them under their parent, and the desktop's New agent grants a scope explicitly

- Status: Proposed
- Date: 2026-11-25

## Context

### Problem

[0082](0082-environment-scopes-for-children.md) lets an agent start a child
on another environment, and #90 nests that child under its parent in the
desktop tree. Three gaps remain, each cut from #90 on purpose
([env-scope-ui design §2](../design/env-scope-ui/design.md)):

1. A local parent's progress **rollup** ignores its remote children, so an
   orchestrator whose helpers run on `win-rig` shows "1/3 steps" while the
   helpers are done.
2. `ws list`'s table and `--parent` don't know the ledger: a remote child is
   a root under its own environment's rows, and `ws list --parent plan` omits
   it.
3. A person can grant a scope only with `ws claude --env-scope` or
   `config.toml`; the desktop's New agent dialog can't.

How should each be counted, shown and granted without weakening 0082's
rules?

### What a solution needs

1. **0082's security properties hold**: only a person grants a scope; the
   default is the agent's own environment; a child's scope only narrows;
   nothing an agent writes widens a scope through the new UI.
2. **No double counting**: each workspace's steps count once in any
   ancestor's rollup, whatever environments the tree spans, with cycles and
   hostile snapshots bounded.
3. **No new I/O without a grant**: a machine with no ledger behaves, reads
   and outputs as today.
4. **Stable JSON**: fields are only added; existing fields keep their types.
5. **Same answer everywhere**: `ws list --json`, `ws machine children`,
   `wait` and `progress show`, and the desktop agree on a rollup.

### Relevant facts

- Rollups are computed in one place, `machine.ProgressTree`, over the local
  workspace list only[^tree]. `ws list --json` computes one per entry with
  `workspaceProgress`[^list]. `ws machine progress show` adds remote children
  as leaves but never adds their steps to their ancestors[^nest]. A removed
  local child leaves the rollup (the tree walks the live list).
- A remote environment's snapshot holds its `ws list` objects, built with
  `listWorkspaces`, so `progress` and `rollup` are always `null` there[^snap].
  The agent rebuilds it every 10 s and publishes when it changes or every
  30 s; a snapshot over `envproto.MaxRequestBytes` (256 KiB) is dropped
  whole[^snapsize].
- Measured on this pod: a `ws list --json` object is 712 bytes on average
  (810 at most, 8 workspaces); `progress` plus `rollup` with a note is at
  most 196 bytes[^measured].
- `machine.record` returns a remote child's progress and the rollup its own
  environment computed (that environment's subtree only)[^record]. The
  copy-back keeps only `result.toml`, `spawn.toml` and `report.md`, never
  progress[^copy].
- The controller's `scope.children` reply already carries each child's
  snapshot entry; it lists direct ledger children of the caller and its
  same-environment descendants only[^children].
- `ws claude --env-scope a,b` grants `{own} ∪ {a,b}`; without the flag
  `[environments] default_scope` applies. `--env-scope <own environment>`
  grants nothing more and skips `default_scope`[^grant]. Inside a workspace
  the flag is refused (`usage`).
- The desktop starts agents with `ws claude` through the Tauri command
  `create_agent` (also reachable from Workspace Online's `/api` dispatch,
  behind its login). It already lists environments (`environments_list`)
  and reads `config.toml` (`config_list`)[^desktop]. The only other
  `NewAgent` builder is the review-fix flow (`reviews/fix.rs`).

## Decision

### D1. Rollups merge ledger children on the controller (F2)

- **Snapshots carry progress.** The agent's snapshot builds each object with
  `progress` (its `note` always `null`, to bound size) and `rollup`, as `ws
  list --json` does. Nothing else in the snapshot changes; no protocol bump.
- **One merge function**, `envscope.Merger` (pure, no I/O): a node is a
  workspace on an environment with its same-environment parent and its
  **base** rollup (its own environment's count of its environment-local
  subtree: `machine.ProgressTree` locally, the snapshot's `rollup`
  remotely). The **merged** rollup of a node is its base plus the merged
  rollup of every ledger entry whose parent is that node or one of its
  same-environment descendants.
  - Each ledger entry has one parent, so it counts once per ancestor chain;
    environment-local subtrees on different environments are disjoint, and a
    remote environment's own rollup never includes a ledger child (it is a
    root there, `parent: null`). So nothing counts twice.
  - Bounds (hostile snapshots, hand-edited ledger): at most 64 ledger hops,
    each ledger entry visited once (a cycle contributes nothing past the
    first visit), a remote base is used only when
    `0 ≤ done ≤ total ≤ 1 000 000 000` (else zero plus a warning), sums
    saturate at that cap, at most 4096 listed workspaces per environment.
- **Where it runs.** `ws list --json` on the controller, when a ledger
  exists, merges every listed entry's `rollup` (local and remote). The
  controller's `scope.children` reply gains `rollup` (merged) for each
  child, so a caller on any environment gets the same number;
  `ws machine children`, `wait` and `progress show` add those child rollups
  to the local ancestors' rollups.
- **States of a remote child.**

  | Child | Counted as |
  | --- | --- |
  | online, fresh snapshot | its merged rollup |
  | offline or stale snapshot | its last snapshot's merged rollup (the entry already says `stale`) |
  | never reported progress, no result | nothing (as a local workspace) |
  | finished with a result, no progress | 1 step, done if it passed, counted by its own environment (as locally) |
  | removed (gone from its snapshot, or ledger entry swept) | nothing, as a removed local child; its copied-back result stays readable |
  | environment removed (`ws env rm`) | nothing |
  | snapshot from an older `ws` (`rollup: null`) | nothing of its own; its ledger children still count |
  | its own same-environment descendants | inside its base (its environment counts them) |
  | its cross-environment descendants | merged through the ledger, recursively |

- **Unchanged:** `ws machine progress set` and `step` print a local-only
  rollup (they never call the controller, so a step stays fast and works
  offline); `machine.record`; the copy-back; the desktop's code (it reads
  `rollup` from `ws list`).

### D2. `ws list` nests remote children under their ledger parent (F3)

- **Table**: when a ledger exists, rows are ordered as one tree across
  environments: a row's parent is its same-environment parent, else its
  `scope_parent` when that row is listed. A remote child prints right under
  its parent, indented, with its own `ENV`. Roots keep today's order
  (`local` first, then environments by name). No ledger: the table is
  byte-identical to today.
- **`--parent <id>`**: the direct children of that workspace on **every**
  environment: same-environment children as today, plus ledger children
  whose parent is it. `--env`, if given, still names the parent's single
  environment. `-q` stays local-only.
- **JSON**: no new field. Entries keep today's order (by environment); the
  existing `parent` and `scope_parent` say where each nests.

### D3. New agent grants a scope explicitly (F1)

- The dialog shows **"May start children on"**, a checkbox per environment
  (`local` and each **enrolled** one; pending ones are not offered), when
  any environment is enrolled. The agent's own environment is always checked
  and can't be unchecked. Offline or unknown-status environments can be
  checked, with a note: a scope is permission, not placement, and `ws`
  checks only enrollment when granting; the target environment itself must
  be online (as today).
- **Initial state** each time the dialog opens: its own environment plus
  `default_scope`'s enrolled names (read with `config_list`); names in
  `default_scope` that aren't enrolled are left out with a warning. Never
  remembered between openings, never prefilled from anything else.
- **Request**: when shown, the dialog always sends `envScope` (own
  environment first) and `create_agent` passes `--env-scope=<a>,<b>` as one
  argument, so `default_scope` is never applied implicitly by the CLI and
  "own environment only" is expressible (`--env-scope=<own>`). Hidden (no
  environment enrolled): nothing is sent, as today.
- **Validation**: in TypeScript (names only from the list, at most 64) and
  again in Rust (1-64 names; each 1-64 bytes, no whitespace, comma, control
  character or leading `-`; no duplicates; an empty list is refused) before
  `ws` runs; `ws` re-checks enrollment (`environment_unknown`).
- **Errors** show in the dialog's existing alert, with hints for
  `environment_unknown` and `usage`.
- No new Tauri command and no CLI change.

### Why this option

1. Criterion 1 (F1): the grant still happens only in `ws claude` run by the
   person's app; the dialog only builds the flag. Agent-written data never
   reaches the scope field: the initial state is the person's own
   `config.toml` plus the checkboxes they tick, the review-fix flow builds
   `NewAgent` with no scope (tested), and nothing is remembered. Showing the
   preselected `default_scope` is stricter than the CLI, which applies it
   silently. Weak: an agent of the same uid can edit `config.toml` before a
   person opens the dialog; that is 0082's residual (0066), now at least
   visible as a pre-ticked box.
2. Criterion 2: proof above; tests pin a three-environment tree, a cycle,
   and hostile values.
3. Criterion 3: every new read is gated on `envscope.LedgerExists`, as #90's
   annotations are; the snapshot change is on the remote side.
4. Criterion 4: `rollup` keeps its shape; `scope.children` gains a field
   older callers ignore; `ws list` and the snapshot gain values in fields
   that were `null`.
5. Criterion 5: one merge function serves `ws list` and the controller.

## Alternatives

### Count remote children from `machine.record` polls

`ws list` (and the controller) would ask each remote child's environment.
**Pros:** freshest numbers. **Cons:** network calls in `ws list` (the desktop
runs it every few seconds), slow or failing when an environment is offline,
and `machine.record`'s rollup still stops at its environment's edge.

### Store the last polled progress in the copy-back

**Pros:** no snapshot change. **Cons:** updated only while someone runs
`wait` or `children`, so the desktop's bar freezes; another file to keep
consistent; still misses cross-environment grandchildren.

### Merge in the CLI only, with a transitive `scope.children`

**Pros:** no new reply field. **Cons:** each caller (on any environment)
would need every environment's snapshot to merge; only the controller has
them.

### Keep notes in the snapshot

**Pros:** the desktop's latest-note line works for remote children. **Cons:**
up to about 800 bytes more per workspace against a 256 KiB snapshot that is
dropped whole when too large.

### `--parent` stays same-environment (as #90 said)

**Pros:** no change to a person's command. **Cons:** disagrees with the
desktop tree and with `ws machine children`; a person can't list what an
orchestrator started.

### A free-text scope field, or a new `--no-default-scope` flag

**Pros:** mirrors the CLI. **Cons:** free text invites typos and names that
aren't enrolled; a new flag is a CLI change when `--env-scope=<own>`
already says "own environment only".

### Remember the last scope like the last environment

**Pros:** fewer clicks. **Cons:** a widened scope silently re-granted to the
next agent; fails criterion 1's spirit.

## Consequences

- Amends [0082](0082-environment-scopes-for-children.md): "Not in this ADR:
  the desktop tree showing cross-environment parents" is done (#90); the
  rollup, `ws list` nesting and the desktop grant are decided here. Amends
  [0077](0077-agent-progress-reporting.md): a rollup includes descendants on
  other environments, through the ledger, on the controller.
- Changes the meaning of `ws list --parent` (now every environment); the
  `ws llm` guide for `ws list` and `docs/tools/ws-list.md` say so.
- `envproto.ScopeChild` gains `rollup`; the snapshot's `progress` and
  `rollup` are no longer always `null`.
- Easier: one rollup everywhere; a person can grant a scope from the desktop
  and see what they grant.
- Harder: `ws list --json` with a ledger reads every enrolled snapshot even
  with `--env`; snapshots grow about 200 bytes per workspace (measured) and
  publish on every progress step.
- Residual: remote numbers lag up to one snapshot (10-30 s), so
  `ws machine progress show` can show a leaf's live progress beside a
  rollup from the last snapshot.
- Residual: an environment can lie about its own workspaces' steps (0082
  threat 17); the bounds cap the damage to that tree's bar.
- **Unverified here:** a real second environment (the pod has one); the
  snapshot size under a large remote tree.
- Design, constraints and tasks:
  [docs/design/env-scope-followups](../design/env-scope-followups/design.md).

## References

[^tree]: `internal/machine/progress.go` at `f87fd52`, `ProgressTree`: "kids[list[i].Parent] = append(...)"; nodes come from the local list only.
[^list]: `internal/cli/list.go` at `f87fd52`, `listedWorkspaces` and `workspaceProgress`.
[^nest]: `internal/cli/machine_remote.go` at `f87fd52`, `nestRemoteProgress` and `addLeaf`: the leaf is appended; no ancestor's `Rollup` changes.
[^snap]: `internal/cli/env_agent.go` at `f87fd52`, `agentSnapshots.Workspaces`: `json.Marshal(s.app.listWorkspaces(entries, nil, loopRuns))`.
[^snapsize]: `internal/envagent/agent.go` at `f87fd52`, `SnapshotCheck: 10 * time.Second`, `SnapshotEvery: 30 * time.Second`, and `refreshSnapshot`: "snapshot too large"; `internal/envproto/payloads.go`: `MaxRequestBytes = 256 << 10`.
[^measured]: `./bin/ws list --json --all` on the designer's pod, 2026-11-25, each object re-encoded compactly; see constraints.md §1.
[^record]: `internal/cli/env_agent_ops.go` at `f87fd52`, `machineRecord`: `progressTreeOf(store, root, list, id)`.
[^copy]: `internal/cli/machine_remote.go` at `f87fd52`, `copyBack`: `WriteRemote(root, envID, id, spawn, &result, report)`.
[^children]: `internal/environment/scope.go` at `f87fd52`, `scopeChildren` and `childEntry`.
[^grant]: `internal/cli/claude.go` at `f87fd52`, `remoteGrant` ("ref.Name != env") and `internal/cli/scope.go`, `requestedRefs` ("if len(names) == 0 { names = ...DefaultScope }").
[^desktop]: `desktop/src-tauri/src/commands.rs` at `f87fd52`, `create_agent` and `new_agent_args`; `ws_config.rs`, `config_list`; `environments.rs`, `environments_list`; `online/dispatch.rs`, `COMMANDS`.
