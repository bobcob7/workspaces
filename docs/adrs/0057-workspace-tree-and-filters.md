# 0057. The Agents list shows root workspaces as a tree with their children nested beneath them, filtered by a search box, a status filter and a Show hidden switch; the Hidden group goes, and Needs input stays at the top

- Status: Proposed
- Date: 2026-11-11

## Context

### Problem

The desktop app's Agents list (`Sidebar`) puts every entry in one of six
status groups (Needs input, Working, Done, Stopped, Shells, Other) and
hidden ones in a Hidden group at the end ([0043](0043-hidden-workspaces.md)).
A child workspace ([0044](0044-workspace-trees.md)) is listed flat, in
its own status group, with `↳ <parent>` in its summary line. An
orchestrating agent starts one design child, one implementer and one
reviewer per task, and a verifier[^orchestration], so one feature puts
a dozen or more children beside the user's own agents, scattered across
groups by status. How should the list show which workspace belongs to
which, let the user find one, and still put whatever needs an answer
first?

### What a solution needs

1. **Structure**: only root workspaces (no listed parent) at the top
   level; children nested beneath their parent, to any depth, each node
   expanded or collapsed on its own.
2. **Needs input first, as today**: every entry that needs input,
   children included, in one group at the top whatever its place in the
   tree, and the same count in the badge, title and tray.
3. **Finding**: a text search, a status filter and a Show hidden switch
   that work with the tree (a match is never shown without its
   ancestors), replacing the Hidden group.
4. **State that survives** switching sections, selecting an entry and
   coming back on a phone, and a refresh of the listing every few
   seconds; a selected entry is never buried in a collapsed node.
5. **Phone width and both transports**: usable at 360px, and the same in
   the Tauri app and Workspace Online ([0053](0053-workspace-online.md)).
6. **No backend change**: `list_entries` already gives every entry with
   `parentId` and `hidden`.

### Relevant facts

- `Entry` has `parentId` (the parent's workspace id or null) and
  `hidden`; the app lists `ws list --all --json`, so hidden entries are
  always in the listing[^entry].
- The list is built in `Sidebar.tsx` from `groupEntries` and
  `hiddenEntries` in `status.ts`; `needsAttention` (not hidden, an agent,
  `needsInput` or `idle`) drives the top-bar badge, the window title and
  the rail badge; `childrenOf` and `descendantsOf` already walk trees and
  survive cycles[^status].
- Whether the Hidden group is open is `agents.showHidden`, in Redux and
  not persisted; the app has no `localStorage` use, and the web build
  runs the same React store over HTTP instead of Tauri `invoke`, so
  in-memory UI state behaves the same in both[^persist].
- Below 720px the list and the pane are one column, and selecting an
  entry hides the list (`display: none`) rather than unmounting it;
  choosing another section does unmount it[^layout].
- WAI-ARIA defines a disclosure as "a widget that enables content to be
  either collapsed (hidden) or expanded (visible)", a `button` with
  `aria-expanded`, toggled by Enter or Space[^apg-disclosure]. A full
  `tree` widget adds arrow-key navigation ("When focus is on a closed
  node, opens the node") and type-ahead[^apg-tree].
- VS Code's tree views filter as you type ("You can filter the files and
  folders in the Explorer view", "available for all tree views")[^vscode].
- `WebRoot.test.tsx` finds the brand by `heading` named "Workspaces", so
  the list can't add a second heading with that name[^webroot].

## Decision

We will replace the status groups and the Hidden group with one tree of
root workspaces under a filter bar, and keep the Needs input group above
it. It's frontend only: no command, record or backend change.

**Layout** (top to bottom):

1. **Needs input**: unchanged. `needsAttention` entries (hidden ones
   never), most recent first, each with `↳ <parent>` when it's a child.
   Shown only when not empty. Search, status and Show hidden don't
   affect it. An entry here is also in the tree.
2. **Filter bar** (`role="search"`, "Filter workspaces"), shown when the
   listing isn't empty:
   - **Search** box: case-insensitive substring of the trimmed text in
     the name, workspace id, intent, detail, or any repo id.
   - **Status** select: All statuses, then the status groups from
     `status.ts` in their order: Needs input, Working, Done, Stopped,
     Shells, Other (`groupOf`).
   - **Show hidden** switch, with the number of hidden entries beside it.
     It replaces the Hidden group and is `agents.showHidden`.
3. **All workspaces**: a heading with the number of matching entries,
   then the tree.

**The tree.**

- An entry's parent is the listed entry whose `workspaceId` equals its
  `parentId`. An entry with no listed parent is a **root**. A parent
  removed with `--keep-children`, or not listed, leaves a root.
- Roots, and each node's children, are most recent first (the existing
  `byRecency`). Entries on a `parentId` cycle are shown once: the walk
  from real roots goes first, then any entry not yet placed becomes a
  root, most recent first.
- Each row is the existing `EntryRow`, without the `↳ <parent>` prefix
  (nesting shows it), after a disclosure button ("Expand <name>" /
  "Collapse <name>", `aria-expanded`, `aria-controls`) when it has
  children shown, or a spacer of the same width when not. Children are a
  nested list. Indentation is 12px a level (8px under 720px), capped at
  5 levels so deep trees don't squeeze names; a hairline on the left of
  each nested list shows the level.
- It's the disclosure pattern, not an ARIA `tree`: rows and toggles are
  ordinary buttons in tab order. Arrow-key navigation is deferred.

**Filtering.** An entry **matches** when Show hidden is on or it isn't
hidden, and the status is All or its group, and the search is empty or
found in it. The tree shows:

- every match, and **every ancestor of a match**, even one that doesn't
  match, is hidden, or has another status: such a **context row** is
  dimmed (`data-context`), still selectable;
- the **selected entry and its ancestors**, whatever the filters (this is
  what used to open the Hidden group on a cross-link);
- nothing else: a non-matching child of a match isn't shown, so a node
  has a disclosure only for children that are shown.

So with no search or status filter and Show hidden off: a visible parent
with only hidden children has no disclosure; a hidden parent with a
visible child is shown dimmed, as context, with that child beneath it; a
hidden entry with no visible descendant isn't shown. Hidden rows that
are shown (Show hidden on) keep the existing 0.78 opacity.

When the tree has no rows but the listing isn't empty, it says "No
workspaces match the filters." with **Clear filters** (search and status
back to defaults) if a search or status is set, else "Every workspace is
hidden." "No workspaces yet." stays for an empty listing.

**Expanded or collapsed.** `agents.expanded` maps entry keys to the
user's last choice. A node is open when its key is `true` there, closed
when `false`, and otherwise open only if it's **auto-expanded**:

- an ancestor of the selected entry; or
- while **filtering** (a search or a status other than All; Show hidden
  alone isn't filtering), an ancestor of a match.

Otherwise a node starts collapsed, so the top level is roots only.
Selecting an entry (`selected` or the tray's `agentFocused`) sets each of
its ancestors to `true`, so a cross-link opens the way to it even past a
node the user had closed. Changing the search or status, or clearing
them, drops every `false`, so new matches aren't hidden under a node
closed earlier; a node closed while filtering stays closed until the
next change. Each listing refresh drops keys of entries that are gone.

**State.** `agents` gains `expanded`, `query` and `statusFilter`;
`showHidden` changes meaning from "the Hidden group is open" to "Show
hidden is on". None is persisted, as `showHidden` wasn't: they last
until the app or page reloads, in the Tauri app and in Workspace Online
alike, and survive section changes and a phone's list/detail switch
because they're in the store, not the component.

**Phone width.** Under 720px the filter bar wraps: the search box takes
the first line, the status select and the switch the second. The
disclosure button, select, switch and search box are at least 40px tall,
and the list never scrolls sideways.

**Unchanged.** The badge, window title, rail badge and tray count
(`needsAttention`); the entry pane's "Started from this workspace" card;
the Remove dialog's descendant list; Hide and Unhide.

### Why this option

1. Nesting by `parentId` puts an orchestrator's children under it, a
   level per generation, and leaves the top level to the user's own
   work, collapsed until opened.
2. The Needs input group is the same code path as today's attention
   group, so it keeps listing children and keeps matching the badges.
3. "A match keeps its ancestors" means a search never shows a child
   without where it came from, and the same rule answers the hidden
   cases: a hidden parent is just a context row. Auto-expanding toward
   matches and the selection means a found or selected entry is always
   on screen, without overwriting the user's open nodes.
4. In the store, the state lives through section switches and the
   phone's list/detail swap, and the refresh only prunes it.
5. No new command or storage, so the web transport needs nothing, and
   the layout rules are CSS under the existing 720px breakpoint.

It's weak in that:

- status at a glance is now the dot on each row and the filter; the
  Working, Done and Stopped headings, and their counts, are gone;
- expanded state is lost on reload;
- an entry needing input is listed twice, in Needs input and the tree;
- the disclosure pattern makes a long tree slower to move through by
  keyboard than an ARIA tree would.

## Alternatives

### Keep the status groups, nest children inside each group

Each status group shows trees whose roots have that status.

**Pros**

- Keeps status headings.

**Cons**

- A child with a different status than its root either leaves its tree
  (and the problem is back) or sits under a heading that's wrong for it.

### A full ARIA `tree` widget

`role="tree"`/`treeitem`, one tab stop, arrow keys and type-ahead.

**Pros**

- Faster keyboard navigation, as the APG pattern describes.

**Cons**

- Roving focus, arrow and type-ahead handling, and their tests, for a
  list that is mostly clicked or tapped; rows also hold their own
  button. Can be added later on top of the same `TreeNode` model.

### Show matches only, without their ancestors

Filtering flattens to the matching rows.

**Pros**

- Simplest to build.

**Cons**

- A search for "review" lists `review-…` rows with no sign of which
  agent asked for them, which is the information the tree adds.

### Hidden parents promote their visible children to roots

**Pros**

- No dimmed rows.

**Cons**

- The same entry moves between the top level and a subtree when Show
  hidden flips, and it breaks the one rule (matches keep ancestors) the
  rest of the filtering follows.

### Persist expanded state and filters

In `desktop.json` ([0034](0034-desktop-state-file.md)) or
`localStorage`.

**Pros**

- Survives reloads.

**Cons**

- `desktop.json` needs a new command in the Tauri and web backends and a
  write per click; `localStorage` is per browser origin and would be the
  app's first use. Both keep keys of workspaces long removed. Deferred:
  the state is cheap to rebuild, and auto-expansion covers the selection.

## Consequences

- Partly supersedes [0043](0043-hidden-workspaces.md): the desktop
  Agents list's **Hidden group** is replaced by the Show hidden switch.
  Everything else in 0043 (the record, `ws hide`, `ws list -a`, hidden
  entries not counting toward the badge) stands. Amends
  [0031](0031-desktop-sections-rail.md)'s Agents list and the desktop
  part of [0044](0044-workspace-trees.md) (`↳ <parent>` only in Needs
  input).
- New pure module `features/agents/tree.ts` (tree, filter,
  auto-expansion); `status.ts` loses `groupEntries` and `hiddenEntries`
  and gains `attentionEntries`, `byRecency` (exported), `StatusFilter`
  and `statusFilterOptions`; a presentational `FilterBar`; `Sidebar`
  rewritten. The design and tasks are in
  [docs/design/tree-list](../design/tree-list/tasks.md).
- Conformance: Vitest unit tests of `tree.ts` for roots, nesting, order,
  cycles, each filter, ancestors kept as context, the selected entry
  forced in, and auto-expansion; reducer tests for the new actions,
  selection expanding ancestors, filter changes dropping `false`, and
  pruning; Sidebar tests for the Needs input group with a child, the
  absence of a Hidden group, expanding and collapsing, search and status
  filtering with context rows, Show hidden, and the empty messages.
  Screenshots at desktop and phone width from `test/tauri-mock.js`, whose
  sample data gains a tree with hidden members.
- **Unverified**: how a tree of 50+ entries feels at phone width; the
  cap of 5 indent levels is a guess to revisit with the screenshots.

## References

[^orchestration]: Agent Handbook, "Procedure: multi-agent feature build" (accessed 2026-11-11): "Each task gets a Haiku child to implement it and a reviewer child (Sonnet, or Haiku for small tasks) to review it."
[^entry]: `desktop/src/api.ts` (`Entry`), `desktop/src/test/fixtures.ts` (`entry`), and ADR [0043](0043-hidden-workspaces.md): "The desktop app runs `ws list --all --json`". Read 2026-11-11 at `0c2017a`.
[^status]: `desktop/src/features/agents/status.ts` and `Sidebar.tsx`, `App.tsx` line 173, `rail/Rail.tsx` line 90, read 2026-11-11 at `0c2017a`.
[^persist]: `desktop/src/features/agents/agentsSlice.ts` ("The Hidden group is open (ADR 0043); not persisted"); `grep -rn localStorage desktop/src` returned nothing on 2026-11-11 at `0c2017a`; `desktop/src/transport/`.
[^layout]: `desktop/src/App.module.css` (`@media (max-width: 720px)`, `.body[data-narrow-pane="detail"] .sidebar { display: none; }`) and `App.tsx` (`SectionView.list`), read 2026-11-11 at `0c2017a`.
[^apg-disclosure]: W3C WAI, "Disclosure (Show/Hide) Pattern." <https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/> (accessed 2026-11-11). "A disclosure is a widget that enables content to be either collapsed (hidden) or expanded (visible)."
[^apg-tree]: W3C WAI, "Tree View Pattern." <https://www.w3.org/WAI/ARIA/apg/patterns/treeview/> (accessed 2026-11-11). "When focus is on a closed node, opens the node; focus does not move."
[^vscode]: Microsoft, "User interface", Visual Studio Code docs. <https://code.visualstudio.com/docs/getstarted/userinterface> (accessed 2026-11-11). "You can filter the files and folders in the Explorer view."
[^webroot]: `desktop/src/web/WebRoot.test.tsx` line 49, `findByRole("heading", { name: "Workspaces" })`, read 2026-11-11 at `0c2017a`.
