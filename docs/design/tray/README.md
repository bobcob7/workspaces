# Desktop tray and notifications

Design for [ADR 0050](../../adrs/0050-desktop-tray-notifications.md). The
tasks are in [tasks.md](tasks.md).

## Who is tracked

An `Entry` (`desktop/src-tauri/src/model.rs`) is **tracked** when all of:

- `kind == Kind::Agent`
- `workspace_id.is_some()` (a `ws` workspace, not a bare background session)
- `parent_id.is_none()` (top-level, [0044](../../adrs/0044-workspace-trees.md))
- `!hidden` ([0043](../../adrs/0043-hidden-workspaces.md))

Everything else (shells, children, hidden workspaces, reviews, sessions
outside workspaces) never notifies and never counts.

| Entry `status`          | Attention    |
| ----------------------- | ------------ |
| `needsInput`, `idle`    | needs input  |
| `done`                  | done         |
| anything else           | busy         |

## Behavior

The watcher keeps one record per tracked agent, keyed by `Entry.key` (the
workspace id): its attention, the time it entered it, and whether it has
been announced. On each listing:

| Case                                                        | Result                                         |
| ----------------------------------------------------------- | ---------------------------------------------- |
| Key not seen before (app start, new agent, unhide, `--keep-children`) | record as is, announced: **no notification** |
| Same attention, not busy, unannounced, held for ≥ 3 s       | **notify**, mark announced                    |
| Same attention otherwise                                    | nothing                                        |
| Attention changed                                           | restart the clock, unannounced                 |
| Key gone (removed, hidden, became a child)                  | forget it                                      |

So with 4 s listings, a state must show in two listings in a row to
notify; an alert comes 4–8 s after the change.

Examples (one column per listing, 4 s apart):

```
working  idle  done  done                 → one "is done" (third → fourth listing)
working  done  done  working  done  done  → "is done" twice
working  needsInput needsInput needsInput → one "needs input"
done (app start)  done  done              → nothing
done  hidden  ...  unhidden(done)  done   → nothing
```

Settings filter the events afterwards; the tracker runs either way, so
switching a setting on never sends old events.

### Listing source

`list_entries` (the frontend's 4 s poll) gives each listing to the
watcher. A backend loop wakes every 4 s and runs `commands::list` itself
only when no listing arrived in the last 4 s, as happens when the window
is closed and the webview is throttled. Listing errors are logged with
`eprintln!` and skipped (the snapshot is kept).

## Tray menu

The tray uses the app icon. Title: the count of tracked agents that need
input, or none at 0. Tooltip: `Workspaces: no agents need input`,
`Workspaces: 1 agent needs input`, `Workspaces: 3 agents need input`.

```
 [icon] 2
 ┌──────────────────────────────────────────────┐
 │ Needs input                       (disabled) │
 │   fix-login — Allow Bash(git push)?          │
 │   docs-pass — Which ADR number should I use? │
 │ ──────────────────────────────────────────── │
 │ Done                              (disabled) │
 │   tray-design — Pushed feat/tray             │
 │   and 4 more                      (disabled) │
 │ ──────────────────────────────────────────── │
 │ Open Workspaces                              │
 │ Quit                                         │
 └──────────────────────────────────────────────┘
```

- Each group shows at most 10 agents, most recently active first
  (`updated_at`, then `created_at`, descending; then name), then
  `and N more` when there are more. An empty group is left out, header
  and separator included. With neither, the menu is only **Open
  Workspaces** and **Quit**.
- A label is `name — snippet`, or `name` when the snippet is empty,
  cut to 60 characters with `…`.
- Snippet: for needs input, `detail`, else `intent`; for done, `result`,
  else `detail`, else `intent`. First non-empty line, trimmed, at most 80
  characters with `…`.
- Menu ids: `agent:<key>`, `open`, `quit`; headers and `and N more` are
  disabled items with ids `header:needs`, `header:done`, `more:needs`,
  `more:done`.
- Clicking an agent: show, unminimize, and focus the `main` window, then
  emit `tray-select` with `{"key": "<key>"}` to it. The frontend switches
  to Agents and selects that key.
- **Open Workspaces** shows and focuses the window. **Quit** exits.

## Notification text

| Events in one listing | Title                       | Body                                    |
| --------------------- | --------------------------- | --------------------------------------- |
| needs input           | `fix-login needs input`     | snippet, or `Waiting for your answer`   |
| done                  | `fix-login is done`         | snippet, or `Finished its turn`         |
| more than 3           | `5 agents need you`         | `fix-login, docs-pass, tray-design and 2 more` |

1–3 events give one notification each, in listing order. Clicking a
notification brings the app forward (macOS), but can't select the agent:
the plugin has no desktop click callback.

## Settings

`desktop.json` ([0034](../../adrs/0034-desktop-state-file.md)):

```json
{
  "version": 1,
  "settings": {
    "reviews": { "...": "unchanged" },
    "notifications": { "enabled": true, "needsInput": true, "done": true }
  }
}
```

A missing key takes its default (all `true`), so no version bump. A
needs-input event is sent when `enabled && needsInput`; a done event when
`enabled && done`.

Settings gets a **Notifications** group, between Reviews and About:

```
 Notifications
 ─────────────────────────────────────────────────────────────
 Show notifications          [x]  When one of your agents needs you.
                                  Children and hidden workspaces never notify.
 When an agent needs input   [x]   (disabled while Show notifications is off)
 When an agent is done       [x]   (disabled while Show notifications is off)
```

Changes save as they're made, with `settings_set_notifications`, as the
Reviews group does.

## Window

- Closing the `main` window hides it (`CloseRequested` → `prevent_close`
  + `hide`). The app, its terminals, and its shells keep running.
- The Dock icon (`RunEvent::Reopen`) and **Open Workspaces** show it.
- ⌘Q and **Quit** quit, closing terminals as today (`RunEvent::Exit`).

## Edge cases

- **App start**: every tracked agent is first seen, so nothing notifies;
  the tray shows the current count at once.
- **Agent removed**: forgotten; its menu row goes on the next listing.
  Re-created with the same id later, it's first seen again.
- **Child or hidden**: never tracked. An agent hidden while waiting
  leaves the menu; unhidden, it's first seen again and doesn't notify.
  A child made a root by `ws rm --keep-children` likewise.
- **Rename**: same key, so no event; the menu and later notifications
  use the new name.
- **`ws list` fails**: `list` still answers with sessions only, so
  every workspace agent disappears and comes back as first seen: no
  replayed alerts, but a change during the outage is missed.
- **Agent needs input before its first listing** (within 4 s of
  creation): first seen as is, so not announced. It still counts in
  the tray.
- **Many agents at once** (e.g. after a reboot restore): first seen, so
  nothing; if more than 3 change in one listing, one summary.
- **Notifications off**: the tray still updates.
- **Dev builds**: macOS may attribute notifications to another app or
  ask for permission once; check in a bundled build (`make desktop`).

## Code layout

```
desktop/src-tauri/src/tray/
  mod.rs        # re-exports; publish(): view + notify after each listing (T4, T5)
  attention.rs  # T1: tracked(), snippet(), candidates(), Snapshot, step()   (pure)
  view.rs       # T2: tray_view(), notices()                                 (pure)
  watcher.rs    # T4: Watcher (shared snapshot + last listing time), wanted(), watch_forever()
  menu.rs       # T5: MenuAction::parse() (pure), create(), refresh(), show_main()  (native, thin)
desktop/src-tauri/src/reviews/store.rs   # T3: NotificationSettings
desktop/src/features/ui/tray.ts          # T6: followTray()
desktop/src/features/settings/           # T6: Notifications group
```
