# 0031. The desktop app is split into sections chosen from a left rail: Agents, Repos, Reviews, and Settings

- Status: Proposed
- Date: 2026-10-24

## Context

### Problem

The desktop app ([0029](0029-desktop-app.md)) is one screen: a top bar, a
list of agents and shells, and the selected one's pane. The maintainer wants
two more things in it, and room for a settings page later:

- **Repos**: list and manage the cached repos and their branches
  ([0008](0008-repository-cache.md)).
- **Reviews**: check cached repos for open pull requests, and offer to start
  an agent to review each one, or start it automatically.

Both have their own list (repos, pull requests) and their own detail. How
should the app hold several such views, and how does someone move between
them without losing track of agents waiting for them?

### What a solution needs

1. **Agents stay one click away**, and "waiting for you" stays visible from
   every view; it's what the app is for.
2. **Each view keeps the list-and-detail layout** people already know from
   Agents, so a new section doesn't need a new way of working.
3. **Room to grow**: Settings now, and later sections, without reworking the
   frame each time.
4. **Cheap to build in parallel**: two PRs, one per section, should touch as
   little shared code as possible.

### Relevant facts

- The app's frame is a top bar over a 300 px list and a detail pane, in
  `desktop/src/App.tsx`. The top bar holds the brand, the "N waiting for
  you" badge, and the Agents actions (Restore stopped, New shell, New
  agent).
- The window opens at 1280 × 820 and can shrink to 820 × 520
  (`desktop/src-tauri/tauri.conf.json`).
- VS Code puts its top-level views in an Activity Bar on the far left that
  "lets you switch between views and gives you additional context-specific
  indicators, like the number of outgoing changes"[^vscode]. Slack, Linear,
  and GitHub Desktop do the same with a narrow column of icons or a
  sidebar **(secondary; observed, not documented)**.
- Selection, dialogs, and the agent view live in `uiSlice`; nothing in the
  store says which view is shown, because there is only one.

## Decision

We will add a **section rail**: a 72 px column at the window's left edge,
between the top bar and the list, with one button per section.

- **Sections, top to bottom**: Agents, Repos, Reviews; Settings pinned to
  the bottom. Each button is an icon over a short label. The current one is
  highlighted, with an accent bar on its left edge.
- **Badges**: Agents shows how many agents need input (the same count as
  the top bar's badge, in its amber). Reviews shows how many pull requests
  are waiting to be reviewed (accent blue). No other section has a badge.
- **Every section is list and detail**, like Agents: a 300 px list with
  grouped, counted headings, and a detail pane with a header (title, status,
  actions on the right, a meta line) over a body. Settings uses the same
  frame, with a list of settings groups.
- **The top bar stays**, with the brand and the "N waiting for you" badge on
  every section. Its right side holds the current section's actions:
  Agents keeps Restore stopped, New shell, and New agent; Repos has Fetch
  all and Add repo; Reviews has Check now and Watched repos; Settings has
  none.
- **Each section keeps its own selection.** Switching sections and back
  shows the same repo, pull request, or agent. A link from one section to
  another (Show in Reviews, Open agent) switches section and selects the
  target.
- **Keyboard**: ⌘1 to ⌘4 choose the sections in rail order.
- **Background work doesn't depend on the section shown.** The agent list
  keeps refreshing every 4 s, and Reviews keeps checking on its own
  schedule, whichever section is open.
- **State**: `uiSlice` gains `section: "agents" | "repos" | "reviews" |
  "settings"` (initially `"agents"`). Each section's selection moves into
  that section's own slice or a per-section map, so `selectedKey` keeps
  meaning "the selected agent". The chosen section isn't persisted; the app
  opens on Agents.

The screens are in the [design doc](../design/desktop-sections/README.md).

### Why this option

1. The rail is on screen in every section, and its Agents badge repeats the
   top bar's, so an agent waiting for input is visible wherever you are.
2. Each section reuses the Agents layout, list styles, and header, so the
   new sections are built from parts that already exist.
3. A rail grows by one button per section, and has room for about eight in
   an 820 px window, and about five at the 520 px minimum height. Settings at the bottom keeps it apart from the
   sections people work in.
4. The rail and `section` are the only shared code. Each section lives in
   its own `features/<section>/` directory with its own slice, so the
   Repos and Reviews PRs touch `App.tsx` and `uiSlice.ts` only to register
   themselves.

It costs 72 px of width on every screen, Agents included, leaving the
detail pane 908 px at the default size instead of 980 px.

## Alternatives

### Tabs in the top bar

**Pros:** no width lost; familiar from browsers.
**Cons:** the top bar already holds the brand, the waiting badge, and up to
three actions per section; with four tabs as well, it is full at 1280 px,
and it has no room for counts on each tab. Could complement the rail later
if the rail is hidden on narrow windows.

### One combined sidebar, with repos and pull requests as more groups

**Pros:** one list to scan; no new navigation.
**Cons:** the list already has five status groups for agents; adding seven
repos and a dozen pull requests makes it scroll, and a repo or pull request
needs a different detail pane and different actions from an agent, so the
"selected item" would mean three different things.

### A separate window per section

**Pros:** sections side by side on a large screen.
**Cons:** Tauri windows each load the frontend again, with their own store
**(unverified)**, so selection and badges would have to be synchronized
across windows; and "Open agent" from Reviews would have to find and raise
another window.

### A command palette instead of navigation

**Pros:** fast for keyboard users; no screen space.
**Cons:** not discoverable, and it shows no badges, so it doesn't meet
criterion 1. Could be added later on top of the rail.

## Consequences

- Every section must keep the list-and-detail shape and put its actions in
  the top bar, so the app stays consistent. Code review checks new sections
  against the [design doc](../design/desktop-sections/README.md).
- The Repos and Reviews PRs are built in parallel; whichever merges second
  rebases onto the rail the first one added. The design doc says which
  files each owns.
- The agents list now loses 72 px of width to the rail; long agent names
  are already ellipsized.
- Settings is reserved in the rail but only gets content when a section
  needs a setting. The Reviews section adds its Reviews group first.
- At the 820 px minimum width, the rail and list leave the detail pane
  448 px. The Repos branch table hides its Author column below 1000 px of
  window width so it still fits.

## References

[^vscode]: Visual Studio Code documentation, "User interface", Basic layout. <https://code.visualstudio.com/docs/getstarted/userinterface> (accessed 2026-10-24). "Activity Bar - Located on the far left-hand side. Lets you switch between views and gives you additional context-specific indicators, like the number of outgoing changes when Git is enabled."
