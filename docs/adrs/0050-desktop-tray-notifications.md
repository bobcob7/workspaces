# 0050. The desktop app has a menu-bar tray that counts top-level agents needing input, and sends a native notification when one starts needing input or finishes; closing the window leaves it running there

- Status: Proposed
- Date: 2026-11-06

## Context

### Problem

The desktop app shows which agents need you only while its window is in
front: the top bar, the window title, and the rail badge
([0029](0029-desktop-app.md), [0043](0043-hidden-workspaces.md)). When an
agent asks a question or finishes while you're in another app, nothing
tells you, and closing the window quits the app. How does the app tell
you, without the window, that one of *your* agents is waiting, and only
about yours?

### What a solution needs

1. **Only the user's own agents.** A workspace that another agent started
   (a child, [0044](0044-workspace-trees.md)) or that is hidden
   ([0043](0043-hidden-workspaces.md)) never notifies and never counts.
   Its parent reports for it.
2. **One alert per change.** Notify when an agent *starts* needing input
   or *finishes*: not when the app starts, not again while it stays that
   way, and not because one listing came back odd. An agent that finishes,
   gets a new message, and finishes again notifies twice.
3. **Visible without the window.** The count is always on screen, and
   one click opens the agent.
4. **Under the user's control.** Notifications can be turned off, or kept
   for only one of the two kinds.
5. **No new `ws` surface.** Everything already comes from the listing
   the app polls ([0029](0029-desktop-app.md)).

### Relevant facts

- The frontend lists entries every 4 s with `list_entries`, which runs
  `ws list --all --json` and `claude agents --json --all` and merges them
  in `model::merge`. Each `Entry` has `status`, `hidden`, and `parent_id`
  (`desktop/src-tauri/src/model.rs`, `desktop/src/App.tsx`).
- An idle agent's status is `needsInput` when its job state is `blocked`,
  `done` when it's `done`, and `idle` ("Waiting") otherwise. The Agents
  list puts `needsInput` and `idle` together under **Needs input**
  (`desktop/src/features/agents/status.ts`).
- The job state is written by Claude Code after a turn ends, so a listing
  taken just after a turn can show `idle` and the next one `done`
  ([0029](0029-desktop-app.md): the job file is read best-effort).
- A hidden webview's timers can be slowed down by the system, so the
  frontend's 4 s poll can't be relied on while the window is closed.
- Tauri 2 tray icons need the `tray-icon` feature of the `tauri`
  crate[^tauri-tray]; `TrayIcon` has `set_title`, `set_tooltip`, and
  `set_menu`, and is removed when its last handle is dropped[^tauri-tray-icon].
- Native notifications come from `tauri-plugin-notification`, sent from
  Rust with `app.notification().builder().title(..).body(..).show()`[^tauri-notification].
  Its desktop API has no click callback: actions are mobile only[^tauri-notification].
- The repo locks `tauri` 2.12.5, and `tauri-plugin-notification` 2.8.0 is
  the newest 2.x, requiring `tauri ^2.12` and Rust 1.90; `tauri` 2.12.5
  itself already requires Rust 1.90[^crates].

## Decision

We will add a tray icon in the macOS menu bar and native notifications,
both driven by a backend watcher over the same listing the frontend uses.
`ws` doesn't change.

**Who counts.** An entry is *tracked* when it is an agent
(`kind == agent`) in a workspace (`workspace_id` set), with no parent and
not hidden. Background sessions outside any workspace, shells, children,
and hidden workspaces are never tracked.

**What it is.** A tracked agent *needs input* when its status is
`needsInput` or `idle` (the Agents list's **Needs input** group), is
*done* when it's `done`, and is *busy* otherwise.

**When it notifies.** The watcher keeps, per tracked agent, its last
state, when it entered it, and whether that was announced.

- An agent first seen (at app start, or when it's created, unhidden, or
  made a root by `ws rm --keep-children`) is taken as it is: no
  notification.
- A change of state restarts the clock and clears "announced".
- Needs input or done, unannounced, and unchanged for at least 3 s
  (so seen in two listings in a row): notify once and mark it announced.
  A one-listing flicker, such as `idle` just before `done`, never
  notifies on its own.
- An agent no longer listed is forgotten. A rename changes nothing: the
  key is the workspace id.
- Notifications are filtered by the settings after this, so turning one
  on never sends a backlog.

The decision is one pure function, previous snapshot plus current
listing in, next snapshot plus events out, unit-tested on its own.

**Where the listing comes from.** `list_entries` hands each listing to
the watcher. The watcher also runs every 4 s, and lists by itself only
when nothing handed it one in the last 4 s, so with the window open
there's still one `ws list` per 4 s.

**The tray.** Its title is the number of tracked agents that need input,
empty at 0, and its tooltip says the same in words. Its menu lists up to
10 tracked agents that need input and 10 that are done, most recently
active first, as `name — snippet`, then **Open Workspaces** and **Quit**.
Clicking an agent shows and focuses the window, switches to Agents, and
selects it. The menu is rebuilt only when what it shows changes.

**Notifications.** One per event, `<name> needs input` or `<name> is
done`, with the agent's detail, intent, or result as the body. More than
3 at once are one notification, `<N> agents need you`. Clicking one
brings the app forward; it can't select the agent (no desktop click
callback).

**Settings.** `desktop.json` gains `settings.notifications`:
`{"enabled": true, "needsInput": true, "done": true}`, all on by default,
in a new **Notifications** group in Settings. They govern notifications
only; the tray is always there.

**The window.** Closing it hides it; the app keeps running in the tray,
with its terminals and shells. Clicking the Dock icon or **Open
Workspaces** shows it again. **Quit** in the tray, or ⌘Q, quits.

### Why this option

1. Counting only roots that aren't hidden matches what 0043 and 0044
   already mean: hidden is out of the way, and a child's parent is who
   follows it. It also means the tray count can be lower than the
   window title's, which counts children too.
2. Snapshot-and-dwell gives one alert per change and survives the
   job-file lag. It costs up to one extra poll (4–8 s) before an alert.
   An agent first seen already waiting is never announced, which is what
   keeps app start, unhiding, and a failed `ws list` from replaying
   alerts; it also misses an agent that needs input before its first
   listing.
3. The tray is always visible and one click away; keeping the app alive
   on close is what makes the tray worth having.
4. Three switches cover "off", "only questions", and "only finished".
5. No `ws` change; the backend reuses `commands::list`.

## Alternatives

### Notify from the frontend

Do the transition logic in TypeScript on each poll, and use the
notification plugin's JS API.

**Pros**

- Next to the existing `needsAttention` logic and Redux state.

**Cons**

- The webview's timers slow down while the window is hidden, which is
  when notifications matter; the tray would also need its state from JS.

### Count every non-hidden agent, like the window title

**Pros**

- One number everywhere.

**Cons**

- Helpers and reviewers that an agent started notify you about work
  their parent is handling; a busy tree spams the menu bar.

### Notify on every status change, without a dwell

**Pros**

- Fastest alert; simplest logic.

**Cons**

- The `idle`-then-`done` lag produces "needs input" followed by "done"
  for every finished turn.

### Only `needsInput`, not `idle`, counts as needing input

**Pros**

- Only agents Claude Code says are blocked.

**Cons**

- Disagrees with the Agents list's Needs input group, and agents whose
  job file is missing or changed format would never notify.

### Quit when the window closes; tray only while open

**Pros**

- Today's behaviour; no hidden running app.

**Cons**

- No alerts once the window is closed, which defeats the feature.

### A `ws` command or event stream for status changes

**Pros**

- Other clients could subscribe.

**Cons**

- New CLI surface and a daemon or long-lived process; the app already
  polls everything it needs.

## Consequences

- The app keeps running after its window closes. People used to closing
  the window to quit now use ⌘Q or **Quit**; shells opened by the app
  stay open until then.
- The tray count and the window title can differ (children). If that
  confuses, a later change can align the title.
- `rust-version` in `desktop/src-tauri/Cargo.toml` becomes 1.90, which
  the locked `tauri` already needed.
- The tray uses the app icon; a monochrome template icon for the menu
  bar is a follow-up.
- Notifications from `npm run tauri dev` may show as another app's, or
  need permission once in System Settings > Notifications; verified only
  in a bundled build.
- How we'll know the code follows this: `tray::attention::step` and
  `tray::view` have unit tests for every rule above; the native tray and
  notification calls are a thin layer checked by `cargo build` and
  `cargo clippy`, then by hand ([design](../design/tray/README.md)).

## References

[^tauri-tray]: Tauri, "System Tray." <https://v2.tauri.app/learn/system-tray/> (accessed 2026-11-06). "First of all, update your `Cargo.toml` to include the necessary feature for the system tray."
[^tauri-tray-icon]: Tauri, "TrayIcon" and "TrayIconBuilder," docs.rs, tauri 2.12.5. <https://docs.rs/tauri/2.12.5/tauri/tray/struct.TrayIconBuilder.html> (accessed 2026-11-06). "Builds and adds a new TrayIcon to the system tray."
[^tauri-notification]: Tauri, "Notifications." <https://v2.tauri.app/plugin/notification/> (accessed 2026-11-06). "Send native notifications to your user using the notification plugin."
[^crates]: Measured 2026-11-06 on macOS 26 (Darwin 25.6.0), from `desktop/src-tauri/Cargo.lock` and the crates.io API (`/api/v1/crates/tauri-plugin-notification/versions`, `/2.8.0/dependencies`, `/api/v1/crates/tauri/2.12.5`): `rust_version` 1.90 for both, `tauri ^2.12` for the plugin. 3.0.0 is still alpha.
