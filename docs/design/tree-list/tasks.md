# Workspace tree and filters: implementation tasks

Implements [ADR 0058](../../adrs/0058-workspace-tree-and-filters.md): the
desktop Agents list (`desktop/src/features/agents/Sidebar.tsx`) shows a
Needs input group, a filter bar, and one tree of root workspaces with
their children nested beneath them. Frontend only: no Go, Rust, command
or API change, so it works the same in the Tauri app and Workspace
Online.

Each task is one commit on its own branch (conventional commit, imperative
mood, for example `feat: add workspace tree logic (ADR 0058)`), builds,
and passes its own verify commands.

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | Pure logic: `tree.ts`, `status.ts` helpers, `treeEntries` fixture | nothing | medium | T5 |
| T2 | Agents slice: expanded state, search, status filter | T1 | small | T3, T5 |
| T3 | `FilterBar` component | T1 | medium | T2, T5 |
| T4 | Sidebar: Needs input, filter bar, tree; drop the Hidden group | T1, T2, T3 | large | T5 |
| T5 | Tauri mock sample tree and README screenshot notes | nothing | small | every task |

T2 and T3 touch disjoint files (`T2 ∥ T3`). T5 touches only
`desktop/test/tauri-mock.js` and `desktop/README.md`, so it can run at
any time, but its data is for the screenshots taken after T4.

**Read before any task:** the "Decision" section of ADR 0058. When this
file and the ADR disagree, the ADR wins; say so in your report.

## Rules for every task

- Work in `desktop/`. If `desktop/node_modules` is missing, run
  `npm ci` there first.
- **Follow the existing code** in the files you edit: a short `/** */`
  doc comment on every exported item, no other comments unless the logic
  isn't obvious; components are `export const X = (props): JSX.Element =>`;
  `readonly` fields; no `any`; early returns; small functions. Styles go
  in the CSS module the task names. **No new npm dependencies.**
- **Tests:** Vitest, `@testing-library/react`, `@testing-library/user-event`;
  helpers from `desktop/src/test/fixtures.ts` (`entry`, `testStore`) and
  `desktop/src/test/render.tsx` (`renderWithStore`). Query by role and
  accessible name. Test reducers by calling the reducer with actions.
  One behaviour per `it`, with a descriptive name.
- **Phone width:** under `@media (max-width: 720px)`, every button,
  select, switch and text box is at least 40px tall (`min-height: 40px`).
- Don't touch files a task doesn't list, except to fix a compile error
  the task caused (say so in your report).
- Don't add a heading named exactly "Workspaces": `src/web/WebRoot.test.tsx`
  finds the brand by that name.

## Shared vocabulary

- **Parent** of an entry: the entry in the listing whose `workspaceId`
  equals its `parentId`, and isn't the entry itself. **Root**: an entry
  with no parent in the listing.
- **Match**: `matchesFilter(entry, filter)` is true.
- **Filtering**: `filter.query.trim() !== "" || filter.status !== "all"`.
  Show hidden on its own is not filtering.
- **Context row**: an entry shown only because it's an ancestor of a match
  or of the selected entry (`TreeNode.match === false`).

---

## T1. Pure logic: tree, filter and auto-expansion

**Depends on:** nothing. **Parallel:** T1 ∥ T5. **Size:** medium.

**Files:**

- edit `desktop/src/features/agents/status.ts`
- edit `desktop/src/features/agents/status.test.ts`
- new `desktop/src/features/agents/tree.ts`
- new `desktop/src/features/agents/tree.test.ts`
- edit `desktop/src/test/fixtures.ts` (add `treeEntries` only)

Read first: `status.ts`, `status.test.ts`, `src/api.ts` (`Entry`, `Repo`),
the `entry` helper in `src/test/fixtures.ts`.

### `status.ts` changes

Keep everything that's there (`groupEntries` and `hiddenEntries` are
removed later, in T4). Add or change:

```ts
/** Most recently active first, then by name (ADR 0058 orders roots and siblings by it). */
export const byRecency = (a: Entry, b: Entry): number => /* the existing body */;

/** What the Agents list's status filter can be set to: everything, or one group (ADR 0058). */
export type StatusFilter = "all" | GroupId;

/** The status filter's choices in display order: All statuses, then groupOrder with groupTitles. */
export const statusFilterOptions: readonly { readonly value: StatusFilter; readonly label: string }[];
// = [{ value: "all", label: "All statuses" }, { value: "attention", label: "Needs input" },
//    { value: "working", label: "Working" }, { value: "done", label: "Done" },
//    { value: "stopped", label: "Stopped" }, { value: "shells", label: "Shells" },
//    { value: "other", label: "Other" }]  -- build it from groupOrder and groupTitles

/** The Needs input group: every entry needsAttention accepts, children included, most recent first. */
export const attentionEntries = (entries: readonly Entry[]): readonly Entry[];
// = entries.filter(needsAttention).toSorted(byRecency)
```

`byRecency` already exists as a private `const`; just export it. Change
the doc comment of `needsAttention` to: `/** Hidden entries never count (ADR 0043); they're in the tree only with Show hidden (ADR 0058). */`.

### `tree.ts`

```ts
import type { Entry } from "../../api";
import { byRecency, groupOf, type StatusFilter } from "./status";

/** The Agents list's filters (ADR 0058). */
export interface TreeFilter {
  readonly query: string;
  readonly status: StatusFilter;
  readonly showHidden: boolean;
}

/** No search, every status, hidden entries left out. */
export const noFilter: TreeFilter = { query: "", status: "all", showHidden: false };

/** An entry in the tree, with the children that are shown. */
export interface TreeNode {
  readonly entry: Entry;
  /** 0 for a root. */
  readonly depth: number;
  /** False for a context row: shown only as an ancestor of a match or of the selected entry. */
  readonly match: boolean;
  readonly children: readonly TreeNode[];
}

/** The listed entry that entry was started from, or undefined. */
export const parentOf = (entries: readonly Entry[], entry: Entry): Entry | undefined;

/** Keys of the ancestors of the entry with key, nearest first; [] for a root or an unknown key. Stops before repeating a key, so a cycle ends. */
export const ancestorKeys = (entries: readonly Entry[], key: string): readonly string[];

/** True when a search or a status filter is set; Show hidden alone isn't filtering. */
export const isFiltering = (filter: TreeFilter): boolean;

/** Whether entry passes every filter. */
export const matchesFilter = (entry: Entry, filter: TreeFilter): boolean;

/** The roots, most recent first, each with its shown descendants. */
export const buildForest = (
  entries: readonly Entry[],
  filter: TreeFilter,
  selectedKey: string | null,
): readonly TreeNode[];

/** Keys of nodes opened without the user asking: ancestors of the selected entry, and while filtering, ancestors of every match. */
export const autoExpandedKeys = (
  entries: readonly Entry[],
  filter: TreeFilter,
  selectedKey: string | null,
): ReadonlySet<string>;

/** Whether a node is open: the user's last choice if there is one, else whether it's auto-expanded. */
export const isOpen = (
  key: string,
  expanded: Readonly<Record<string, boolean>>,
  auto: ReadonlySet<string>,
): boolean;
```

Rules:

- `parentOf`: `entry.parentId === null` → undefined; else the first entry
  `e` with `e.workspaceId === entry.parentId && e.key !== entry.key`.
- `ancestorKeys`: start at the entry with `key` (none → `[]`); repeatedly
  take `parentOf`; stop when there's none or its key is already in the
  result or equals `key`.
- `matchesFilter(entry, f)` is true only if all of:
  - `f.showHidden || !entry.hidden`;
  - `f.status === "all" || groupOf(entry) === f.status`;
  - `q = f.query.trim().toLowerCase()` is `""`, or `q` is a substring of
    the lower-cased `name`, `workspaceId`, `intent`, `detail`, or any
    `repos[i].id` (skip nulls).
- `buildForest`:
  1. **Shown keys** = every match, every `ancestorKeys` of a match, and,
     if `selectedKey` is an entry's key, that key and its `ancestorKeys`.
  2. Children of an entry = shown entries whose `parentOf` is it, sorted
     with `byRecency`.
  3. Walk depth-first from the shown entries with no parent, sorted with
     `byRecency`, keeping a `seen` set of keys so no entry is placed
     twice. Then any shown entry still not seen (it's on a `parentId`
     cycle) becomes a root: take them sorted with `byRecency`, skipping
     ones the previous walk placed, and walk each the same way.
  4. Each node gets `depth` (root 0), `match = matchesFilter(entry, filter)`
     and its `children` nodes.
- `autoExpandedKeys`: `ancestorKeys` of `selectedKey` (if not null), plus,
  when `isFiltering(filter)`, `ancestorKeys` of every match.
- `isOpen`: `expanded[key] ?? auto.has(key)`.

### `treeEntries` fixture

Add to `src/test/fixtures.ts`, after `entry`:

```ts
/** A sample tree for the Agents list (ADR 0058); keys equal workspace ids. */
export const treeEntries = (): readonly Entry[] => [
  entry({ key: "orch", workspaceId: "orch", name: "orchestrate", status: "working", updatedAt: "2026-11-11T09:00:00Z" }),
  entry({ key: "t1", workspaceId: "t1", name: "t1-impl", parentId: "orch", status: "done", updatedAt: "2026-11-11T08:00:00Z" }),
  entry({ key: "t1r", workspaceId: "t1r", name: "t1-review", parentId: "t1", status: "needsInput", updatedAt: "2026-11-11T08:30:00Z" }),
  entry({ key: "rev", workspaceId: "rev", name: "review-42", parentId: "orch", hidden: true, status: "working", updatedAt: "2026-11-11T08:45:00Z" }),
  entry({ key: "solo", workspaceId: "solo", name: "solo", status: "stopped", updatedAt: "2026-11-11T07:00:00Z" }),
  entry({ key: "hid", workspaceId: "hid", name: "lint-sweep", hidden: true, status: "idle", updatedAt: "2026-11-11T06:00:00Z" }),
  entry({ key: "hidkid", workspaceId: "hidkid", name: "lint-fix", parentId: "hid", status: "working", updatedAt: "2026-11-11T06:30:00Z" }),
  entry({ key: "shell", workspaceId: "shell", name: "scratch", kind: "shell", status: "running", updatedAt: "2026-11-11T05:00:00Z" }),
];
```

### Tests (`tree.test.ts`)

Use a helper `const shape = (nodes: readonly TreeNode[]): unknown[] => nodes.map((n) => (n.children.length > 0 ? [n.entry.key, shape(n.children)] : n.entry.key));`
and `const contextKeys = (nodes): string[]` (keys of every node, at any
depth, with `match === false`). With `all = treeEntries()`:

| Case | Call | Expected |
| ---- | ---- | -------- |
| roots only at top, hidden left out, hidden parent kept as context | `buildForest(all, noFilter, null)` | `shape` = `[["orch", [["t1", ["t1r"]]]], "solo", ["hid", ["hidkid"]], "shell"]`; `contextKeys` = `["hid"]` |
| Show hidden adds hidden entries as matches | `buildForest(all, { ...noFilter, showHidden: true }, null)` | `shape` = `[["orch", ["rev", ["t1", ["t1r"]]]], "solo", ["hid", ["hidkid"]], "shell"]`; `contextKeys` = `[]` |
| a search keeps ancestors as context | `buildForest(all, { ...noFilter, query: "review" }, null)` | `shape` = `[["orch", [["t1", ["t1r"]]]]]`; `contextKeys` = `["orch", "t1"]` |
| search is trimmed and case-insensitive | `matchesFilter(all[0], { ...noFilter, query: "  ORCH " })` | `true` |
| search matches a repo id | `matchesFilter(entry({ repos: [{ id: "github.com/bobcob7/buff-bear", path: "buff-bear", ref: "main", refKind: "branch" }] }), { ...noFilter, query: "buff" })` | `true` |
| status filter | `buildForest(all, { ...noFilter, status: "working" }, null)` | `shape` = `["orch", ["hid", ["hidkid"]]]`; `contextKeys` = `["hid"]` |
| shells filter | `buildForest(all, { ...noFilter, status: "shells" }, null)` | `shape` = `["shell"]` |
| selected entry is forced in with its ancestors | `buildForest(all, noFilter, "rev")` | `shape` = `[["orch", ["rev", ["t1", ["t1r"]]]], "solo", ["hid", ["hidkid"]], "shell"]`; `contextKeys` = `["rev", "hid"]` (order of `contextKeys` = depth-first order) |
| nothing matches | `buildForest(all, { ...noFilter, query: "zzz" }, null)` | `[]` |
| depth | `buildForest(all, noFilter, null)[0]?.children[0]?.children[0]?.depth` | `2` |
| unknown parent is a root | `buildForest([entry({ key: "c", workspaceId: "c", parentId: "gone" })], noFilter, null)` | `shape` = `["c"]` |
| cycle placed once | `x = entry({ key: "x", workspaceId: "x", parentId: "y", updatedAt: "2026-11-11T09:00:00Z" })`, `y = entry({ key: "y", workspaceId: "y", parentId: "x", updatedAt: "2026-11-11T08:00:00Z" })`; `buildForest([x, y], noFilter, null)` | `shape` = `[["x", ["y"]]]` |
| ancestors nearest first | `ancestorKeys(all, "t1r")` | `["t1", "orch"]` |
| ancestors of a root / unknown key | `ancestorKeys(all, "orch")`, `ancestorKeys(all, "nope")` | `[]`, `[]` |
| ancestors stop at a cycle | `ancestorKeys([x, y], "x")` | `["y"]` |
| auto-expand: selection only when not filtering | `autoExpandedKeys(all, noFilter, "t1r")` | set of `["t1", "orch"]` |
| auto-expand: ancestors of matches while filtering | `autoExpandedKeys(all, { ...noFilter, query: "lint-fix" }, null)` | set of `["hid"]` |
| Show hidden alone doesn't auto-expand | `autoExpandedKeys(all, { ...noFilter, showHidden: true }, null)` | empty set |
| isFiltering | `noFilter` / `{ ...noFilter, query: " " }` / `{ ...noFilter, status: "done" }` / `{ ...noFilter, showHidden: true }` | `false` / `false` / `true` / `false` |
| isOpen: explicit false beats auto | `isOpen("a", { a: false }, new Set(["a"]))` | `false` |
| isOpen: explicit true | `isOpen("a", { a: true }, new Set())` | `true` |
| isOpen: auto when no choice | `isOpen("a", {}, new Set(["a"]))`, `isOpen("a", {}, new Set())` | `true`, `false` |

### Tests (`status.test.ts`)

Add: `attentionEntries` lists a needs-input child and leaves out a hidden
one, most recent first (with `treeEntries()`: `["t1r"]`; with an extra
`entry({ key: "w", status: "idle", updatedAt: "2026-11-11T10:00:00Z" })`:
`["w", "t1r"]`); `statusFilterOptions.map((o) => o.value)` is
`["all", "attention", "working", "done", "stopped", "shells", "other"]`
and the first label is `"All statuses"`. Keep every existing test.

### Acceptance criteria

- Every case above is a passing test.
- `tree.ts` imports nothing from React, Redux or the store.
- Existing tests still pass; `Sidebar.tsx` is unchanged.

### Verify

```sh
cd desktop && npm run typecheck && npx vitest run src/features/agents/tree.test.ts src/features/agents/status.test.ts src/features/agents/Sidebar.test.tsx
```

---

## T2. Agents slice: expanded state, search and status filter

**Depends on:** T1. **Parallel:** T2 ∥ T3 ∥ T5. **Size:** small.

**Files:** edit `desktop/src/features/agents/agentsSlice.ts`,
`desktop/src/features/agents/agentsSlice.test.ts`.

Read first: both files, `src/features/agents/tree.ts` (`ancestorKeys`),
`src/features/agents/status.ts` (`StatusFilter`), and the exports of
`src/features/ui/uiSlice.ts` (`selected`, `agentFocused`).

### State

Add to `AgentsState` and `initialState`:

```ts
  /** The user's last open (true) or closed (false) choice per entry key in the tree (ADR 0058); not persisted. */
  readonly expanded: Readonly<Record<string, boolean>>;   // initial {}
  /** The Agents list's search text (ADR 0058); not persisted. */
  readonly query: string;                                  // initial ""
  /** The Agents list's status filter (ADR 0058); not persisted. */
  readonly statusFilter: StatusFilter;                     // initial "all"
```

Change the doc comment of `showHidden` to `/** The Show hidden switch is on (ADR 0058); not persisted. */`. Keep `hiddenShown` as is.

### Reducers (add to `reducers`, export the action creators)

```ts
nodeToggled: (state, action: PayloadAction<{ readonly key: string; readonly open: boolean }>) => void
// expanded = { ...expanded, [key]: open }
queryChanged: (state, action: PayloadAction<string>) => void
// query = payload; expanded = withoutClosed(expanded)
statusFilterChanged: (state, action: PayloadAction<StatusFilter>) => void
// statusFilter = payload; expanded = withoutClosed(expanded)
filtersCleared: (state) => void
// query = ""; statusFilter = "all"; expanded = withoutClosed(expanded); showHidden unchanged
```

`withoutClosed` is a private helper that returns a new record with every
`false` value removed. Always assign a new object to `state.expanded`;
don't mutate keys on it (it's a `Readonly<Record>`).

### Extra reducers

- `fetchEntries.fulfilled`: also set `expanded` to the entries of the old
  record whose key is the `key` of some entry in `action.payload.entries`.
- `isAnyOf(selected, agentFocused)` matcher (from `../ui/uiSlice`): when
  the payload is a string, set `expanded[k] = true` (new object) for every
  `k` in `ancestorKeys(state.entries, payload)`. A `null` payload changes
  nothing. Put this `addMatcher` after all `addCase` calls (RTK requires
  cases before matchers).

### Tests (`agentsSlice.test.ts`, reducer-level; build entries with `treeEntries()` loaded via `fetchEntries.fulfilled`)

- initial state has `expanded: {}`, `query: ""`, `statusFilter: "all"`.
- `nodeToggled({ key: "orch", open: true })` then `({ key: "orch", open: false })` leaves `expanded` = `{ orch: false }`.
- `selected("t1r")` sets `expanded` to `{ t1: true, orch: true }`, even when `orch` was `false`.
- `agentFocused("hidkid")` sets `expanded.hid` to `true`.
- `selected(null)` and `selected("solo")` (a root) leave `expanded` unchanged.
- `queryChanged("x")` sets `query` and drops `false` values but keeps `true` ones (`{ a: true, b: false }` → `{ a: true }`).
- `statusFilterChanged("done")` sets `statusFilter` and drops `false` values.
- `filtersCleared()` resets `query` and `statusFilter`, drops `false`, and leaves `showHidden` as it was.
- a later `fetchEntries.fulfilled` without `t1` drops the `t1` key from `expanded`.

### Acceptance criteria

- All tests above pass; existing slice tests still pass.
- No component file changes.

### Verify

```sh
cd desktop && npm run typecheck && npx vitest run src/features/agents/agentsSlice.test.ts src/features/agents/Sidebar.test.tsx
```

---

## T3. `FilterBar` component

**Depends on:** T1. **Parallel:** T3 ∥ T2 ∥ T5. **Size:** medium.

**Files:** new `desktop/src/features/agents/FilterBar.tsx`,
`desktop/src/features/agents/FilterBar.module.css`,
`desktop/src/features/agents/FilterBar.test.tsx`.

Read first: `src/features/agents/status.ts` (`StatusFilter`,
`statusFilterOptions`), `src/features/reviews/RepoSlots.tsx` (how the app
uses MUI `Switch` with `FormControlLabel`), `src/features/create/NewShellDialog.tsx`
(MUI `TextField`), `src/features/agents/Sidebar.module.css` (spacing and
colour variables).

A presentational component: it reads nothing from the store.

```tsx
export interface FilterBarProps {
  readonly query: string;
  readonly status: StatusFilter;
  readonly showHidden: boolean;
  /** How many entries are hidden, shown in the switch's label. */
  readonly hiddenCount: number;
  readonly onQueryChange: (query: string) => void;
  readonly onStatusChange: (status: StatusFilter) => void;
  readonly onShowHiddenChange: (showHidden: boolean) => void;
}

/** The Agents list's search box, status filter and Show hidden switch (ADR 0058). */
export const FilterBar = (props: FilterBarProps): JSX.Element;
```

Markup (MUI 9, already a dependency):

- Outer `<div role="search" aria-label="Filter workspaces" className={styles.bar}>`.
- `TextField` `size="small"`, `type="search"`, `label="Search"`,
  `value={query}`, `onChange` → `onQueryChange(e.target.value)`,
  `className={styles.search}`, `autoComplete="off"`.
- `TextField` `select`, `size="small"`, `label="Status"`,
  `slotProps={{ select: { native: true } }}`, `value={status}`,
  `onChange` → `onStatusChange(e.target.value as StatusFilter)`,
  `className={styles.status}`; children are
  `statusFilterOptions.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)`.
- `FormControlLabel` with `control={<Switch size="small" checked={showHidden} onChange={(e) => onShowHiddenChange(e.target.checked)} />}`
  and `label={`Show hidden (${hiddenCount})`}`, `className={styles.hidden}`.

CSS (`FilterBar.module.css`):

- `.bar`: `display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 8px 16px 4px; box-sizing: border-box; max-width: 100%;`
- `.search`: `flex: 1 1 160px; min-width: 0;`
- `.status`: `flex: 0 0 auto; min-width: 128px;`
- `.hidden`: `margin: 0; white-space: nowrap;`
- `@media (max-width: 720px)`: `.search { flex-basis: 100%; }`; the
  inputs, the native select and the switch's label row get
  `min-height: 40px` (target `.bar input, .bar select` and `.hidden`).

### Tests (`FilterBar.test.tsx`; render with `render` from Testing Library, wrapped in MUI's `ThemeProvider` with `theme` from `src/theme`, and `vi.fn()` callbacks)

- typing `a` into `getByLabelText("Search")` calls `onQueryChange("a")` (the value is controlled, so keep the test to one character).
- `userEvent.selectOptions(getByLabelText("Status"), "done")` calls `onStatusChange("done")`.
- the Status select lists the seven options in `statusFilterOptions` order (assert the `option` roles' names: `All statuses`, `Needs input`, `Working`, `Done`, `Stopped`, `Shells`, `Other`).
- clicking `getByLabelText(/Show hidden/)` calls `onShowHiddenChange(true)` when `showHidden` is false.
- the switch's label shows the count: `getByText("Show hidden (3)")` with `hiddenCount={3}`.
- `getByRole("search", { name: "Filter workspaces" })` exists.

### Acceptance criteria

- All tests pass; the component has no `useAppSelector`/`useAppDispatch`.
- Nothing else imports it yet (T4 does).

### Verify

```sh
cd desktop && npm run typecheck && npx vitest run src/features/agents/FilterBar.test.tsx
```

---

## T4. Sidebar: Needs input, filter bar and tree; no Hidden group

**Depends on:** T1, T2, T3. **Parallel:** T4 ∥ T5. **Size:** large.

**Files:**

- rewrite `desktop/src/features/agents/Sidebar.tsx`
- edit `desktop/src/features/agents/Sidebar.module.css`
- rewrite `desktop/src/features/agents/Sidebar.test.tsx`
- edit `desktop/src/features/agents/status.ts` and `status.test.ts`:
  remove `groupEntries`, `hiddenEntries` and their tests (keep `groupOf`,
  `groupTitles`, `groupOrder` and everything else)
- edit `desktop/src/api.ts`: only the doc comment of `Entry.hidden`, to
  `/** Left out of the Agents list unless Show hidden is on, and not counted as waiting (ADR 0043, 0058). */`

Read first: ADR 0058 "Decision", the current `Sidebar.tsx`, `EntryRow.tsx`
(unchanged by this task), `tree.ts`, `FilterBar.tsx`, `agentsSlice.ts`
(the actions from T2), `Sidebar.module.css`.

### `Sidebar.tsx`

```tsx
/** The Agents list: Needs input, the filter bar, and the tree of root workspaces (ADR 0058). */
export const Sidebar = (): JSX.Element;
```

- Select `entries`, `loaded`, `selectedKey` (`s.ui.selectedKey`),
  `query`, `statusFilter`, `showHidden` and `expanded` with **separate**
  `useAppSelector` calls (never return a new object from a selector).
  Build `filter: TreeFilter` with `useMemo`. Compute with `useMemo`:
  `attention = attentionEntries(entries)`,
  `forest = buildForest(entries, filter, selectedKey)`,
  `auto = autoExpandedKeys(entries, filter, selectedKey)`,
  `matchCount = entries.filter((e) => matchesFilter(e, filter)).length`,
  `hiddenCount = entries.filter((e) => e.hidden).length`.
- Render `<nav className={styles.root} aria-label="Workspaces">` containing, in order:
  1. `loaded && entries.length === 0`: `<p className={styles.empty}>No workspaces yet.</p>` (and nothing else below).
  2. `attention.length > 0`: `<section className={styles.group} aria-label="Needs input">` with
     `<h2 className={styles.heading}>Needs input<span className={styles.count}>{attention.length}</span></h2>`
     and a `<ul className={styles.list}>` of `EntryRow`s, each with
     `parentName` = the `name` of `parentOf(entries, entry)` or `null`,
     `current={entry.key === selectedKey}`, `onClick` → `dispatch(selected(entry.key))`.
  3. `entries.length > 0`: `<FilterBar>` wired to `queryChanged`,
     `statusFilterChanged`, `hiddenShown`, with `hiddenCount`.
  4. `entries.length > 0`: `<section className={styles.group} aria-label="All workspaces">` with
     `<h2 className={styles.heading}>All workspaces<span className={styles.count}>{matchCount}</span></h2>`, then:
     - `forest.length > 0`: `<TreeList nodes={forest} depth={0} … />`;
     - else `<div className={styles.empty}>`: if `isFiltering(filter)`,
       `<p>No workspaces match the filters.</p>` and an MUI
       `<Button size="small" onClick={() => dispatch(filtersCleared())}>Clear filters</Button>`;
       otherwise `<p>Every workspace is hidden.</p>`.
- Private components in the same file:

```tsx
interface TreeListProps {
  readonly nodes: readonly TreeNode[];
  readonly depth: number;
  readonly id?: string;
  readonly expanded: Readonly<Record<string, boolean>>;
  readonly auto: ReadonlySet<string>;
  readonly selectedKey: string | null;
}
const TreeList = (props: TreeListProps): JSX.Element;
// <ul id={id} className={depth === 0 ? styles.list : styles.children}
//     data-capped={depth > 5 ? "true" : undefined}>  one <TreeItem> per node, key = entry.key

interface TreeItemProps extends Omit<TreeListProps, "nodes" | "id"> {
  readonly node: TreeNode;
}
const TreeItem = (props: TreeItemProps): JSX.Element;
```

  `TreeItem`:
  - `open = isOpen(node.entry.key, expanded, auto)`; `childListId = useId()`.
  - `<li data-context={node.match ? undefined : "true"} data-hidden={node.entry.hidden ? "true" : undefined}>`
  - `<div className={styles.node}>`: when `node.children.length > 0`, a
    `<button type="button" className={styles.disclosure} aria-expanded={open}
    aria-controls={open ? childListId : undefined}
    aria-label={`${open ? "Collapse" : "Expand"} ${node.entry.name}`}
    onClick={() => dispatch(nodeToggled({ key: node.entry.key, open: !open }))}>`
    holding `<span className={styles.caret} data-open={open} aria-hidden="true">▸</span>`;
    otherwise `<span className={styles.spacer} aria-hidden="true" />`.
    Then `<EntryRow entry={node.entry} current={node.entry.key === selectedKey} onClick={() => dispatch(selected(node.entry.key))} />`
    (no `parentName`).
  - After the div, when `open && node.children.length > 0`:
    `<TreeList nodes={node.children} depth={depth + 1} id={childListId} … />`.

### `Sidebar.module.css`

Remove `.toggle`, `.toggle:focus-visible`, `.show` and `.hidden` (only
the Hidden group used them). Keep the rest. Add:

```css
.node { display: flex; align-items: stretch; min-width: 0; }
.node > .item { flex: 1; min-width: 0; }
.disclosure { flex: none; width: 24px; padding: 0; border: 0; border-radius: 6px; background: none; color: var(--text-muted); font: inherit; cursor: pointer; }
.disclosure:hover { background: var(--panel-raised); }
.disclosure:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.spacer { flex: none; width: 24px; }
.children { list-style: none; margin: 0 0 0 12px; padding: 0 0 0 0; border-left: 1px solid var(--border); }
.children[data-capped="true"] { margin-left: 0; border-left: 0; }
li[data-hidden="true"] > .node { opacity: 0.78; }
li[data-context="true"] > .node { opacity: 0.55; }
@media (max-width: 720px) {
  .children { margin-left: 8px; }
  .disclosure { min-height: 40px; }
  .empty button { min-height: 40px; }
}
```

The `.caret` rule already rotates on `data-open="true"`; reuse it. The
`.empty` paragraph margins: give `.empty p { margin: 0 0 8px; }`.

### Tests (`Sidebar.test.tsx`, rewrite; keep the `loadedStore` helper, and add a `loadedTree()` helper that dispatches `fetchEntries.fulfilled({ entries: [...treeEntries()], warnings: [] }, "req")` on a `testStore()`)

A row's accessible name is its whole text (name, age and summary), and a
child's name also appears in Needs input as `↳ <parent>`, so: scope tree
queries with `const tree = () => screen.getByRole("region", { name: "All workspaces" })`,
check a row is there with `within(tree()).queryByText("t1-impl")` (the
name span's exact text), and name disclosure buttons exactly
(`{ name: "Expand orchestrate" }`), never with a regex.

Keep (adjusting queries as needed): Needs input lists an agent that needs
input; shows what the agent is doing; selects an entry when clicked;
"No workspaces yet." for an empty listing. Add:

- **Needs input includes a child, with its parent**: with `treeEntries()`,
  `within(getByRole("region", { name: "Needs input" }))` has a button
  with text `↳ t1-impl · ` (the `t1-review` row; use `toHaveTextContent(/↳ t1-impl/)`).
- **No Hidden group**: `queryByRole("region", { name: "Hidden" })` is null.
- **Only roots at first**: in `region "All workspaces"`, `orchestrate`,
  `solo`, `lint-sweep` and `scratch` buttons are present and `t1-impl`
  is not.
- **Expand and collapse**: clicking `getByRole("button", { name: "Expand orchestrate" })`
  shows `t1-impl` and sets `store.getState().agents.expanded.orch` to
  `true`; the button is then named `Collapse orchestrate` with
  `aria-expanded="true"`; clicking it hides `t1-impl` again.
- **Hidden root kept as context**: the `li` holding the `lint-sweep`
  button has `data-context="true"` (use `closest("li")`).
- **Show hidden**: clicking `getByLabelText(/Show hidden/)` sets
  `agents.showHidden` to `true`; after expanding `orchestrate`,
  `review-42` is shown and its `li` has `data-hidden="true"`.
- **Search auto-expands to matches**: typing `t1-review` into
  `getByLabelText("Search")` shows the `t1-review` row inside
  `region "All workspaces"` without clicking any Expand button, and the
  `orchestrate` row's `li` has `data-context="true"`; `solo` is gone.
- **Status filter**: `selectOptions(getByLabelText("Status"), "stopped")`
  leaves only `solo` in `region "All workspaces"`.
- **Nothing matches**: typing `zzz` shows `No workspaces match the filters.`;
  clicking `Clear filters` empties the search box and the tree is back.
- **Every workspace hidden**: with one entry `{ key: "r", hidden: true }`,
  `Every workspace is hidden.` is shown and `No workspaces yet.` isn't.
- **Selecting a child expands its ancestors**: `store.dispatch(selected("t1r"))`
  before rendering; the `t1-review` row in `region "All workspaces"` has
  `aria-current="true"`.
- **Selected hidden entry is shown**: `store.dispatch(selected("rev"))`;
  `review-42` is in `region "All workspaces"` with Show hidden off.

### Acceptance criteria

- All tests above pass; `status.ts` no longer exports `groupEntries` or
  `hiddenEntries`, and nothing imports them.
- The whole desktop suite passes (other tests don't depend on the old
  groups; `src/web/WebRoot.test.tsx` still finds one heading "Workspaces").
- No horizontal scroll at 360px: the filter bar wraps and rows ellipsize
  (checked by the orchestrator's screenshots; don't add Playwright here).

### Verify

```sh
cd desktop && npm run typecheck && npx vitest run src/features/agents/ src/web/WebRoot.test.tsx src/App.test.tsx
```

---

## T5. Tauri mock sample tree and README screenshot notes

**Depends on:** nothing (disjoint files). **Parallel:** with every task.
**Size:** small.

**Files:** edit `desktop/test/tauri-mock.js` (only the `entries` array in
`data`), `desktop/README.md` (only the "Screenshots without the backend"
section).

`desktop/test/tauri-mock.js` already stands in for the backend (and
`test/web-mock.js` serves the same data for Workspace Online), so no new
mock is needed. Its `entry(overrides)` helper sets `key` and `name` from
`workspaceId`.

### Mock data

Keep the four existing entries. Give `fix-login-d8efb24f` an
`updatedAt: "2026-10-24T09:40:00Z"` so it's the most recent root, and
append (each through `entry({...})`, with `path: "/w/<workspaceId>"`):

| workspaceId | sessionId | parentId | status | hidden | intent | updatedAt |
| ----------- | --------- | -------- | ------ | ------ | ------ | --------- |
| `fix-login-t1-impl-2b7c9e10` | `2b7c9e10` | `fix-login-d8efb24f` | `working` | | `Implement T1: token bucket` | `2026-10-24T09:35:00Z` |
| `rate-limit-tests-4e5f6a7b` | `4e5f6a7b` | `fix-login-t1-impl-2b7c9e10` | `needsInput` | | `Write rate limit tests` | `2026-10-24T09:32:00Z` |
| `fix-login-t1-review-6d0a4f21` | `6d0a4f21` | `fix-login-d8efb24f` | `done` | | `Review T1` | `2026-10-24T09:20:00Z` |
| `review-buff-bear-42-3f2a9c1-1f0c33d8` | `1f0c33d8` | `fix-login-d8efb24f` | `working` | `true` | `Review pull request #42` | `2026-10-24T09:25:00Z` |
| `lint-sweep-5b1e93aa` | `5b1e93aa` | | `idle` | `true` | `Fix every lint warning` | `2026-10-24T08:00:00Z` |
| `lint-fix-api-7c8d9e0f` | `7c8d9e0f` | `lint-sweep-5b1e93aa` | `working` | | `Fix lint in api` | `2026-10-24T08:10:00Z` |

Every one gets `repos: [repo(buffBear, "buff-bear", "main", "branch")]`.

### README

In "Screenshots without the backend", after the paragraph about
`window.__TAURI_MOCK__`, add: "The sample listing is a tree for the
Agents list ([ADR 0058](../../adrs/0058-workspace-tree-and-filters.md)):
`fix-login` has two generations of children, one of which needs input,
and a hidden review child; `lint-sweep` is hidden and has a visible
child, so it shows as a dimmed context row until Show hidden is on."

### Acceptance criteria

- The mock still parses and every `parentId` names a `workspaceId` in the list.
- No other mock data or handler changes.

### Verify

```sh
cd desktop && node --check test/tauri-mock.js && node --input-type=module -e "globalThis.window = globalThis; await import('./test/tauri-mock.js'); const es = window.__TAURI_MOCK__.data.entries; const ids = new Set(es.map((e) => e.workspaceId)); const bad = es.filter((e) => e.parentId !== null && !ids.has(e.parentId)); if (es.length !== 10 || bad.length > 0) { console.error('entries', es.length, 'bad', bad.map((e) => e.key)); process.exit(1); } console.log('ok', es.length);" && npm run typecheck
```
