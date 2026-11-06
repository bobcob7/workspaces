# Desktop tray and notifications: implementation tasks

Implements [0050](../../adrs/0050-desktop-tray-notifications.md) as
designed in [README.md](README.md). Each task builds, passes its tests,
and can be merged on its own. Each task is one commit (conventional,
imperative mood, e.g. `feat: add tray attention tracking (ADR 0050)`).

**Order and parallelism**

| Task | Title | Depends on | Can run in parallel with |
| ---- | ----- | ---------- | ------------------------ |
| T1 | `tray::attention`: tracked agents and the transition step (pure) | nothing | nothing (T3 also edits `lib.rs`) |
| T2 | `tray::view`: tray menu model and notification text (pure) | T1 | T3 |
| T3 | Notification settings in `desktop.json` and `settings_set_notifications` | nothing | T2 |
| T4 | Watcher and native notifications | T1, T2, T3 | T6 |
| T5 | Tray icon, menu, and keeping the app alive when the window closes | T4 | T6 |
| T6 | Frontend: Notifications settings group and tray selection | T3 | T4, T5 |

**Read before any task:** `docs/design/tray/README.md` (behavior, menu,
notification text, settings, edge cases) and the "Decision" section of
`docs/adrs/0050-desktop-tray-notifications.md`. When this file and those
two disagree, they win.

**Rules for every task**

- Rust: follow the existing code in `desktop/src-tauri/src`. Doc comments
  (`///`, `//!`) on every public item, in the style of `model.rs`. Unit
  tests go in a `#[cfg(test)] mod tests` at the bottom of the file they
  test, named after the behavior (`step_notifies_once_after_dwell`), using
  plain `assert_eq!`/`assert!`. No new crates except the ones a task names.
  Errors from native calls (tray, notification, window) are logged with
  `eprintln!` and never panic; no `unwrap()`/`expect()` outside tests and
  outside the existing `.expect("error while building the app")`.
- Pure logic stays in `attention.rs` and `view.rs`, with no `tauri`
  imports. Native code (`menu.rs`, `mod.rs::publish`, `watcher.rs`'s loop)
  stays thin; it can't be unit tested and is verified by `cargo build` and
  `cargo clippy`, and by hand where a task says so.
- TypeScript: follow the existing code in `desktop/src`; `readonly`
  fields, arrow-function components, tests with vitest and Testing
  Library as in the neighboring `*.test.tsx`. Component files export only
  components.
- JSON to the frontend is camelCase (`#[serde(rename_all = "camelCase")]`).
  Don't change any existing field, command, or event.
- Every Rust task runs, from `desktop/src-tauri`:
  `cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test`.
  Run `cargo fmt` first if `--check` fails. Every task also runs
  `make desktop-test` from the repo root.
- There is no `lint` script in `desktop/package.json`; the frontend checks
  are `npm run typecheck`, `npm test`, and `npm run build`.

---

## T1. `tray::attention`: tracked agents and the transition step (pure)

**Depends on:** nothing. **Parallel with:** nothing.

**Create**

- `desktop/src-tauri/src/tray/mod.rs`
- `desktop/src-tauri/src/tray/attention.rs`

**Modify**

- `desktop/src-tauri/src/lib.rs`: add, after `mod transcript;`:

  ```rust
  // Wired up in T4 and T5 of docs/design/tray/tasks.md (ADR 0050).
  #[allow(dead_code)]
  mod tray;
  ```

**`tray/mod.rs`**

```rust
//! The menu-bar tray and native notifications for top-level agents that
//! need input or are done (ADR 0050).

pub mod attention;
```

**`tray/attention.rs`**

```rust
//! Which agents the tray tracks, and when one starts needing the user
//! (ADR 0050). Pure: no I/O, no tauri.

use crate::model::{Entry, Kind, Status};
use std::collections::BTreeMap;

/// How long a state must hold before it's announced, so a state seen in
/// only one listing (4 s apart) never notifies.
pub const DWELL_MS: i64 = 3_000;

/// Longest snippet, in characters, before it's cut with `…`.
pub const SNIPPET_MAX: usize = 80;

/// What a tracked agent asks of the user.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Attention {
    NeedsInput,
    Done,
    Busy,
}

/// `needsInput` and `idle` need input (the Agents list's Needs input
/// group), `done` is done, everything else is busy.
pub fn attention_of(status: Status) -> Attention;

/// An agent in a workspace, with no parent, not hidden (ADRs 0043, 0044).
pub fn is_tracked(entry: &Entry) -> bool;

/// `s` if it has at most `max` chars, else its first `max - 1` chars
/// followed by `…`. Counts chars, not bytes.
pub fn truncate(s: &str, max: usize) -> String;

/// The first non-empty line, trimmed, of: for NeedsInput and Busy,
/// `detail` then `intent`; for Done, `result` then `detail` then
/// `intent`. Cut to SNIPPET_MAX. "" when none has text.
pub fn snippet(entry: &Entry, attention: Attention) -> String;

/// A tracked agent as the tray and notifications see it.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Candidate {
    pub key: String,
    pub name: String,
    pub snippet: String,
    pub attention: Attention,
    /// `updated_at`, else `created_at`, else "" (RFC 3339, so it sorts).
    pub active_at: String,
}

/// The tracked entries, in listing order.
pub fn candidates(entries: &[Entry]) -> Vec<Candidate>;

/// What the watcher remembers of one agent.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Tracked {
    pub attention: Attention,
    /// When it entered `attention` (ms since the epoch).
    pub since_ms: i64,
    /// A notification for this stretch was sent, or it was first seen in it.
    pub announced: bool,
}

/// Every tracked agent's record, by `Candidate::key`.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Snapshot {
    pub tracked: BTreeMap<String, Tracked>,
}

/// An agent that started needing input (`NeedsInput`) or finished (`Done`).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Event {
    pub key: String,
    pub name: String,
    pub snippet: String,
    pub attention: Attention,
}

/// The next snapshot and the events for one listing (README "Behavior").
pub fn step(prev: &Snapshot, current: &[Candidate], now_ms: i64) -> (Snapshot, Vec<Event>);
```

`step`, for each candidate in `current`, in order:

1. Not in `prev`: record `{attention, since_ms: now_ms, announced: true}`.
   No event.
2. In `prev` with a different attention: record `{attention, since_ms:
   now_ms, announced: false}`. No event (DWELL_MS > 0).
3. In `prev` with the same attention: keep the record. If `attention !=
   Busy && !announced && now_ms - since_ms >= DWELL_MS`, emit an `Event`
   (current name and snippet) and set `announced = true`.

Keys in `prev` but not in `current` are dropped from the next snapshot.

Also add, at the bottom of the file, a helper the later tasks' tests use:

```rust
#[cfg(test)]
pub(crate) mod testing {
    use crate::model::{Entry, Kind, Status};

    /// A tracked agent `key` with `status`; tests change fields as needed.
    pub fn entry(key: &str, status: Status) -> Entry {
        Entry {
            key: key.to_string(),
            kind: Kind::Agent,
            name: key.to_string(),
            workspace_id: Some(key.to_string()),
            session_id: Some(format!("s-{key}")),
            path: format!("/w/{key}"),
            status,
            stale: Some(false),
            created_at: Some("2026-11-06T10:00:00Z".to_string()),
            updated_at: None,
            detail: None,
            intent: None,
            result: None,
            links: Vec::new(),
            repos: Vec::new(),
            review: None,
            hidden: false,
            parent_id: None,
        }
    }
}
```

**Acceptance** (in `attention.rs`'s `mod tests`, using `testing::entry`)

- `attention_of`: `NeedsInput` and `Idle` → `NeedsInput`; `Done` →
  `Done`; `Working`, `Starting`, `Stopped`, `Running`, `Pending`,
  `Unknown` → `Busy`.
- `is_tracked`: true for `entry("a", Status::Done)`; false with
  `hidden = true`, with `parent_id = Some("p")`, with `workspace_id =
  None`, and with `kind = Kind::Shell`.
- `truncate("abc", 3)` → `"abc"`; `truncate("abcd", 3)` → `"ab…"`;
  `truncate("ééééé", 4)` → `"ééé…"`.
- `snippet`: Done with `result: Some("\n  shipped PR #4  \nmore")` →
  `"shipped PR #4"`; Done with no result and `detail: Some("d")` → `"d"`;
  NeedsInput with `detail: Some("Allow Bash?")` and `result: Some("r")`
  → `"Allow Bash?"`; NeedsInput with only `intent: Some("i")` → `"i"`;
  nothing set → `""`; a 100-char detail → 80 chars ending in `…`.
- `candidates` of `[a (Done), b (hidden), c (child), d (Working)]` → keys
  `["a", "d"]` with attentions `[Done, Busy]`; `active_at` is
  `updated_at` when set, else `created_at`.
- `step` (one test each; `c(key, attention)` is a small local helper
  building a `Candidate`):
  - first seen in NeedsInput at 0 → no events; the record is announced.
  - Busy at 0, NeedsInput at 4000 → none; NeedsInput at 8000 → one
    NeedsInput event; NeedsInput at 12000 → none.
  - Busy at 0, Done at 4000, Done at 8000 → one Done event.
  - Done announced at 0, Busy at 4000, Done at 8000, Done at 12000 → one
    Done event at 12000 (done → working → done notifies again).
  - Busy at 0, NeedsInput at 4000, Done at 8000, Done at 12000 → only
    one Done event; no NeedsInput event (one-listing flicker).
  - Busy at 0, NeedsInput at 1000, NeedsInput at 3999 → none; at 4000 →
    one event (dwell is `>=`).
  - Busy is never an event, however long it holds.
  - A key missing from `current` is dropped; seen again later in Done →
    no event.
  - The event carries the current name: rename the candidate between
    the change and the dwell → the event has the new name.

**Verify**

```console
$ cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test tray::attention
$ cd desktop/src-tauri && cargo test
$ make desktop-test
```

---

## T2. `tray::view`: tray menu model and notification text (pure)

**Depends on:** T1. **Parallel with:** T3.

**Create**

- `desktop/src-tauri/src/tray/view.rs`

**Modify**

- `desktop/src-tauri/src/tray/mod.rs`: add `pub mod view;`

**`tray/view.rs`**

```rust
//! What the tray shows and what notifications say (ADR 0050). Pure.

use super::attention::{truncate, Attention, Candidate, Event};

/// Most agents listed per menu group; the rest are "and N more".
pub const GROUP_MAX: usize = 10;
/// Longest menu label, in characters.
pub const LABEL_MAX: usize = 60;
/// More events than this in one listing become one summary notification.
pub const SUMMARY_OVER: usize = 3;

/// One agent row in the tray menu.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TrayItem {
    pub key: String,
    pub label: String,
}

/// A menu group: at most GROUP_MAX items, and how many were left out.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct TrayGroup {
    pub items: Vec<TrayItem>,
    pub more: usize,
}

/// Everything the tray shows.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct TrayView {
    /// The needs-input count, or None at 0.
    pub title: Option<String>,
    pub tooltip: String,
    pub needs_input: TrayGroup,
    pub done: TrayGroup,
}

/// `name — snippet`, or `name` when the snippet is empty, cut to LABEL_MAX.
pub fn label(candidate: &Candidate) -> String;

/// The tray for these candidates (README "Tray menu").
pub fn tray_view(candidates: &[Candidate]) -> TrayView;

/// One native notification.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Notice {
    pub title: String,
    pub body: String,
}

/// The notifications for one listing's events (README "Notification text").
pub fn notices(events: &[Event]) -> Vec<Notice>;
```

- `tray_view`: each group holds the candidates with that attention, sorted
  by `active_at` descending, then `name` ascending; the first GROUP_MAX
  become items, `more` is the rest. `title` is `Some(n.to_string())` for
  n > 0 needs-input candidates (all of them, not only the listed ten).
  `tooltip`: `"Workspaces: no agents need input"`,
  `"Workspaces: 1 agent needs input"`, `"Workspaces: {n} agents need input"`.
  The separator in labels is ` — ` (space, U+2014, space).
- `notices`: Busy events are ignored. With at most SUMMARY_OVER (non-Busy)
  events, one `Notice` each, in order: NeedsInput → title
  `"{name} needs input"`, body the snippet or `"Waiting for your answer"`;
  Done → title `"{name} is done"`, body the snippet or `"Finished its
  turn"`. With more, one `Notice`: title `"{n} agents need you"`, body
  the first three names joined by `", "` then `" and {n - 3} more"`.

**Acceptance** (`view.rs`'s `mod tests`)

- `label` of name `fix-login`, snippet `Allow Bash?` → `"fix-login — Allow Bash?"`;
  empty snippet → `"fix-login"`; a 100-char result → 60 chars ending `…`.
- `tray_view(&[])` → title `None`, tooltip `"Workspaces: no agents need input"`,
  both groups empty with `more == 0`.
- 1 needs-input, 2 done, 1 busy → title `Some("1")`, tooltip
  `"Workspaces: 1 agent needs input"`, `needs_input.items.len() == 1`,
  `done.items.len() == 2`, busy nowhere.
- Done candidates with `active_at` `"2026-11-06T10:00:00Z"` (name b),
  `"2026-11-06T11:00:00Z"` (name a), `"2026-11-06T10:00:00Z"` (name a2)
  → item keys in order a, a2, b.
- 12 needs-input candidates → title `Some("12")`, tooltip
  `"Workspaces: 12 agents need input"`, 10 items, `more == 2`.
- `notices` of one NeedsInput event with empty snippet →
  `[Notice { title: "fix-login needs input", body: "Waiting for your answer" }]`;
  one Done with snippet `"PR #4"` → `"fix-login is done"` / `"PR #4"`;
  3 events → 3 notices in order; 5 events named a..e →
  one `Notice { title: "5 agents need you", body: "a, b, c and 2 more" }`;
  a Busy event alone → `[]`.

**Verify**

```console
$ cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test tray::view
$ cd desktop/src-tauri && cargo test
$ make desktop-test
```

---

## T3. Notification settings in `desktop.json` and `settings_set_notifications`

**Depends on:** nothing. **Parallel with:** T2.

**Modify**

- `desktop/src-tauri/src/reviews/store.rs` (and its `mod tests`)
- `desktop/src-tauri/src/reviews/mod.rs`
- `desktop/src-tauri/src/lib.rs` (register the command only)

**`store.rs`**: add, before `Settings`:

```rust
/// Which native notifications the app sends (ADR 0050). All on by default.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct NotificationSettings {
    /// Off sends none; the tray still updates.
    pub enabled: bool,
    /// When a top-level agent starts needing input.
    pub needs_input: bool,
    /// When a top-level agent finishes.
    pub done: bool,
}

impl Default for NotificationSettings {
    fn default() -> Self {
        Self { enabled: true, needs_input: true, done: true }
    }
}
```

and add `pub notifications: NotificationSettings,` to `Settings` after
`reviews`. `VERSION` stays 1.

**`reviews/mod.rs`**: import `NotificationSettings` from `store` and add,
after `settings_set_reviews`:

```rust
/// Saves the Notifications settings group (ADR 0050).
#[tauri::command]
pub fn settings_set_notifications(
    state: State<'_, AppState>,
    notifications: NotificationSettings,
) -> CmdResult<Settings> {
    state.store.update(|d| {
        d.settings.notifications = notifications;
        d.settings.clone()
    })
}
```

**`lib.rs`**: add `reviews::settings_set_notifications,` after
`reviews::settings_set_reviews,` in `generate_handler!`.

**Acceptance** (`store.rs`'s `mod tests`)

- `Settings::default().notifications` is all `true`.
- A file `{"version":1,"settings":{"reviews":{"skipDrafts":false}}}`
  opens with notifications all `true` (and `skip_drafts == false`).
- A file `{"version":1,"settings":{"notifications":{"done":false}}}`
  opens with `enabled == true`, `needs_input == true`, `done == false`,
  and reviews at their defaults.
- `serde_json::to_value(NotificationSettings { enabled: false, needs_input: true, done: false })`
  is `{"enabled":false,"needsInput":true,"done":false}`, and it parses
  back to the same value.
- `update` that sets `d.settings.notifications.done = false` writes a file
  that reopens with `done == false` (use `tempfile` as the existing
  `update_writes_file_that_reopens_the_same` does).
- All existing store tests still pass unchanged.

**Verify**

```console
$ cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test store
$ cd desktop/src-tauri && cargo test
$ make desktop-test
```

---

## T4. Watcher and native notifications

**Depends on:** T1, T2, T3. **Parallel with:** T6.

**Create**

- `desktop/src-tauri/src/tray/watcher.rs`

**Modify**

- `desktop/src-tauri/Cargo.toml`: `rust-version = "1.90"` (the locked
  `tauri` 2.12.5 already needs it), and under `[dependencies]`
  `tauri-plugin-notification = "2.8"`. Commit the updated `Cargo.lock`.
- `desktop/README.md`: in Requirements, `Rust 1.80+` → `Rust 1.90+`.
- `desktop/src-tauri/src/tray/mod.rs`: `pub mod watcher;` and `publish`.
- `desktop/src-tauri/src/commands.rs`: `AppState` field, `now_ms`
  visibility, `list_entries`.
- `desktop/src-tauri/src/lib.rs`: plugin, state, and the loop.

**`tray/watcher.rs`**

```rust
//! Feeds each listing to the attention step, and lists by itself when the
//! frontend hasn't for a while, as when the window is closed (ADR 0050).

use super::attention::{self, Attention, Candidate, Event, Snapshot};
use crate::model::Entry;
use crate::reviews::store::NotificationSettings;
use std::sync::{Mutex, PoisonError};
use std::time::Duration;
use tauri::{AppHandle, Manager};

/// How often the watcher wakes, the same as the frontend's poll.
pub const WATCH_INTERVAL: Duration = Duration::from_secs(4);
const WATCH_INTERVAL_MS: i64 = 4_000;

/// One listing's tracked agents and the events it produced.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Observed {
    pub candidates: Vec<Candidate>,
    pub events: Vec<Event>,
}

/// The snapshot and when the last listing arrived, shared by
/// `list_entries` and `watch_forever`.
#[derive(Debug, Default)]
pub struct Watcher {
    inner: Mutex<Inner>,
}

#[derive(Debug, Default)]
struct Inner {
    snapshot: Snapshot,
    last_observed_ms: Option<i64>,
}

impl Watcher {
    /// Steps the snapshot with `entries` and records `now_ms` as the last listing.
    pub fn observe(&self, entries: &[Entry], now_ms: i64) -> Observed;

    /// No listing yet, or none in the last WATCH_INTERVAL.
    pub fn is_stale(&self, now_ms: i64) -> bool;
}

/// The events the settings allow: NeedsInput when `enabled && needs_input`,
/// Done when `enabled && done`, in order.
pub fn wanted(events: &[Event], settings: &NotificationSettings) -> Vec<Event>;

/// Every WATCH_INTERVAL, when the watcher is stale, runs `commands::list`
/// and publishes what it observed. Listing errors are logged with
/// `eprintln!("tray: listing failed: {}: {}", err.code, err.message)` and skipped.
pub async fn watch_forever(app: AppHandle);
```

- `observe` locks with `.lock().unwrap_or_else(PoisonError::into_inner)`,
  computes `attention::candidates(entries)`, calls `attention::step`,
  stores the new snapshot and `Some(now_ms)`, and returns both.
- `is_stale` is `last_observed_ms.is_none_or(|t| now_ms - t >= WATCH_INTERVAL_MS)`.
- `watch_forever`: `tokio::time::interval(WATCH_INTERVAL)` with
  `MissedTickBehavior::Skip`, as `loops::tick_forever` does. On each
  tick: `let state = app.state::<AppState>();` if
  `state.watcher.is_stale(now_ms())`, run
  `crate::commands::list(&state.runner, state.jobs_dir.as_deref()).await`;
  on `Ok(listing)`, `let observed = state.watcher.observe(&listing.entries, now_ms());`
  then `super::publish(&app, &observed)`.

**`tray/mod.rs`**: add

```rust
pub mod watcher;

use crate::commands::AppState;
use tauri::{AppHandle, Manager};
use tauri_plugin_notification::NotificationExt;
use watcher::Observed;

/// Sends the notifications the settings allow for one listing. Native and
/// thin: verified by cargo build and clippy, and by hand.
pub fn publish(app: &AppHandle, observed: &Observed) {
    let settings = app.state::<AppState>().store.get().settings.notifications;
    for notice in view::notices(&watcher::wanted(&observed.events, &settings)) {
        if let Err(err) = app
            .notification()
            .builder()
            .title(&notice.title)
            .body(&notice.body)
            .show()
        {
            eprintln!("tray: notification failed: {err}");
        }
    }
}
```

**`commands.rs`**

- `AppState` gains `pub watcher: Arc<crate::tray::watcher::Watcher>,`
  (last field).
- `fn now_ms()` becomes `pub(crate) fn now_ms()`.
- `list_entries` becomes:

```rust
/// Lists workspaces and background sessions, and hands the listing to the
/// tray's watcher (ADR 0050).
#[tauri::command]
pub async fn list_entries(app: tauri::AppHandle, state: State<'_, AppState>) -> CmdResult<Listing> {
    let listing = list(&state.runner, state.jobs_dir.as_deref()).await?;
    state.reviews.set_entries(listing.entries.clone());
    let observed = state.watcher.observe(&listing.entries, now_ms());
    crate::tray::publish(&app, &observed);
    Ok(listing)
}
```

**`lib.rs`**

- `.plugin(tauri_plugin_notification::init())` after the opener plugin.
- `watcher: Arc::new(tray::watcher::Watcher::default()),` in `AppState`.
- After `app.manage(state);`:
  `tauri::async_runtime::spawn(tray::watcher::watch_forever(app.handle().clone()));`
- Keep `#[allow(dead_code)]` on `mod tray;` (T5 removes it: the tray
  view is unused until then).

**Acceptance** (`watcher.rs`'s `mod tests`, with
`crate::tray::attention::testing::entry`)

- A new `Watcher` is stale at any time; after `observe(.., 10_000)` it's
  not stale at 13_999 and is stale at 14_000.
- `observe` of `[entry("a", Working)]` at 0, then `[entry("a", Done)]` at
  4000, then again at 8000 → the third returns one Done event for `a`;
  the first two return none. Each `Observed.candidates` has `a`.
- `observe` of `[entry("a", NeedsInput)]` at 0 (first seen) → no events.
- `wanted`: with one NeedsInput and one Done event, default settings → both;
  `needs_input: false` → only Done; `done: false` → only NeedsInput;
  `enabled: false` → none.
- `cargo build` succeeds (the native `publish` and `watch_forever` have no
  unit tests).

**By hand** (record the outcome in the report; skip if no GUI): `make
desktop-dev`, start an agent with a short task from the app, switch to
another app, and see one "… is done" notification about 4–8 s after it
finishes, and none on the next listings.

**Verify**

```console
$ cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test
$ cd desktop/src-tauri && cargo build
$ make desktop-test
```

---

## T5. Tray icon, menu, and keeping the app alive when the window closes

**Depends on:** T4. **Parallel with:** T6.

**Create**

- `desktop/src-tauri/src/tray/menu.rs`

**Modify**

- `desktop/src-tauri/Cargo.toml`: `tauri = { version = "2", features = ["tray-icon"] }`.
  Commit the updated `Cargo.lock` if it changes.
- `desktop/src-tauri/src/tray/mod.rs`: `pub mod menu;`, and `publish`
  refreshes the tray.
- `desktop/src-tauri/src/tray/watcher.rs`: remember the last view.
- `desktop/src-tauri/src/lib.rs`: create the tray, hide on close, show on
  Dock reopen; remove `#[allow(dead_code)]` (and its comment) from `mod tray;`.

**`tray/menu.rs`**

```rust
//! The tray icon and its menu (ADR 0050). Native and thin: only
//! `MenuAction::parse` is unit tested; the rest is checked by cargo build,
//! clippy, and by hand.

use super::view::{TrayGroup, TrayView};
use serde::Serialize;
use tauri::menu::{Menu, MenuEvent, MenuItem, PredefinedMenuItem};
use tauri::tray::{TrayIcon, TrayIconBuilder};
use tauri::{AppHandle, Emitter, Manager, Wry};

/// The tray's id.
pub const TRAY_ID: &str = "main";
/// The event the frontend listens for to select an agent.
pub const SELECT_EVENT: &str = "tray-select";
/// The window the tray shows.
pub const MAIN_WINDOW: &str = "main";

/// What a menu item does, from its id.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum MenuAction {
    Open,
    Quit,
    Select(String),
}

impl MenuAction {
    /// `"open"`, `"quit"`, or `"agent:<key>"` with a non-empty key; any
    /// other id (headers, "and N more") is None.
    pub fn parse(id: &str) -> Option<Self>;
}

/// The `tray-select` payload: `{"key": "<entry key>"}`.
#[derive(Debug, Clone, Serialize)]
pub struct TraySelect {
    pub key: String,
}

/// Keeps the tray alive: it's removed when its last handle drops.
pub struct TrayHandle(pub TrayIcon);

/// Builds the tray with an empty view and manages its handle.
pub fn create(app: &AppHandle) -> tauri::Result<()>;

/// Sets the title, tooltip, and menu from `view`. Errors are logged.
pub fn refresh(app: &AppHandle, view: &TrayView);

/// Shows, unminimizes, and focuses the main window. Errors are logged.
pub fn show_main(app: &AppHandle);

fn build_menu(app: &AppHandle, view: &TrayView) -> tauri::Result<Menu<Wry>>;

fn on_menu_event(app: &AppHandle, event: MenuEvent);
```

- `create`: `TrayIconBuilder::with_id(TRAY_ID)`, `.icon(icon.clone())`
  when `app.default_window_icon()` is `Some(icon)`, `.tooltip(..)` and
  `.menu(&build_menu(app, &view)?)` for `view = super::view::tray_view(&[])`,
  `.show_menu_on_left_click(true)`, `.on_menu_event(on_menu_event)`,
  `.build(app)?`; then `app.manage(TrayHandle(tray))`.
- `refresh`: `let Some(tray) = app.try_state::<TrayHandle>() else { return };`
  then `tray.0.set_title(view.title.as_deref())`,
  `tray.0.set_tooltip(Some(&view.tooltip))`, and, when
  `build_menu(app, view)` is `Ok(menu)`, `tray.0.set_menu(Some(menu))`;
  log each error (including `build_menu`'s) with
  `eprintln!("tray: …: {err}")`. `refresh` returns `()`, so no `?`.
- `build_menu`, in order (README "Tray menu"): for each non-empty group
  (needs input, then done), a disabled header item (`header:needs` /
  `header:done`, text `Needs input` / `Done`), one enabled item per
  `TrayItem` (id `agent:<key>`, text `label`), a disabled `and {more}
  more` item (`more:needs` / `more:done`) when `more > 0`, then
  `PredefinedMenuItem::separator(app)?`; finally `Open Workspaces`
  (`open`) and `Quit` (`quit`). Items are
  `MenuItem::with_id(app, id, text, enabled, None::<&str>)?`, appended
  with `menu.append(&item)?` to `Menu::new(app)?`. A small private
  `fn append_group(menu: &Menu<Wry>, app: &AppHandle, group: &TrayGroup, header: (&str, &str), more_id: &str) -> tauri::Result<()>`
  avoids repeating it.
- `on_menu_event`: match `MenuAction::parse(event.id.as_ref())` (as in Tauri's system tray guide):
  `Open` → `show_main(app)`; `Quit` → `app.exit(0)`;
  `Select(key)` → `show_main(app)` then
  `app.emit_to(MAIN_WINDOW, SELECT_EVENT, TraySelect { key })`, logging
  an error; `None` → nothing.
- `show_main`: `let Some(window) = app.get_webview_window(MAIN_WINDOW) else { return };`
  then `show()`, `unminimize()`, `set_focus()`, logging errors.

**`tray/watcher.rs`**: `Inner` gains `last_view: Option<TrayView>`, and

```rust
impl Watcher {
    /// True, and remembers `view`, when it differs from the last one.
    pub fn view_changed(&self, view: &TrayView) -> bool;
}
```

**`tray/mod.rs`**: `publish` begins with

```rust
let state = app.state::<AppState>();
let view = view::tray_view(&observed.candidates);
if state.watcher.view_changed(&view) {
    menu::refresh(app, &view);
}
```

and then sends notifications as before.

**`lib.rs`**

- In `setup`, after `app.manage(state);` and before spawning the watcher:
  `tray::menu::create(app.handle())?;`
- On the builder, before `.invoke_handler`:

  ```rust
  .on_window_event(|window, event| {
      if let tauri::WindowEvent::CloseRequested { api, .. } = event {
          if window.label() == tray::menu::MAIN_WINDOW {
              api.prevent_close();
              if let Err(err) = window.hide() {
                  eprintln!("tray: hiding the window failed: {err}");
              }
          }
      }
  })
  ```
- The `.run` closure becomes a `match event`: `RunEvent::Exit` closes
  terminals as today; `#[cfg(target_os = "macos")] RunEvent::Reopen { .. }`
  calls `tray::menu::show_main(app)`; `_ => {}`.

**Acceptance**

- `menu.rs`'s `mod tests`: `parse("open") == Some(Open)`,
  `parse("quit") == Some(Quit)`, `parse("agent:fix-login-1a2b3c4d") ==
  Some(Select("fix-login-1a2b3c4d"))`, `parse("agent:") == None`,
  `parse("header:needs") == None`, `parse("more:done") == None`.
- `serde_json::to_value(TraySelect { key: "k".into() })` is `{"key":"k"}`.
- `watcher.rs`: `view_changed` is true the first time, false for an equal
  view, true after a change.
- `grep -rn "allow(dead_code)" desktop/src-tauri/src` prints nothing.
- `cargo build` succeeds.

**By hand** (record the outcome in the report; skip if no GUI): `make
desktop-dev`. The menu bar shows the app icon with the needs-input count;
the menu lists agents as in the README mockup; choosing one brings the
window forward on that agent once T6 is in (before T6, the window shows);
closing the window leaves the tray; clicking the Dock icon or Open
Workspaces shows it; Quit and ⌘Q quit.

**Verify**

```console
$ cd desktop/src-tauri && cargo fmt --check && cargo clippy --all-targets -- -D warnings && cargo test
$ cd desktop/src-tauri && cargo build
$ grep -rn "allow(dead_code)" desktop/src-tauri/src; test $? -eq 1
$ make desktop-test
```

---

## T6. Frontend: Notifications settings group and tray selection

**Depends on:** T3 (the `settings_set_notifications` command and the
`notifications` settings JSON). The `tray-select` event comes from T5,
but tests fake it, so T6 doesn't wait for T5. **Parallel with:** T4, T5.

**Create**

- `desktop/src/features/ui/tray.ts`
- `desktop/src/features/ui/tray.test.ts`

**Modify**

- `desktop/src/api.ts`
- `desktop/src/test/fixtures.ts`
- `desktop/src/features/ui/uiSlice.ts` and `uiSlice.test.ts`
- `desktop/src/features/settings/settingsSlice.ts`
- `desktop/src/features/settings/SettingsList.tsx`
- `desktop/src/features/settings/SettingsPane.tsx` and `SettingsPane.test.tsx`
- `desktop/src/App.tsx` and `App.test.tsx`
- Any other file `npm run typecheck` flags for a `Settings` literal
  missing `notifications` (add `notifications: notificationSettings()`).

**`api.ts`**

```ts
import { listen } from "@tauri-apps/api/event";

/** Which native notifications the app sends (ADR 0050). */
export interface NotificationSettings {
  readonly enabled: boolean;
  readonly needsInput: boolean;
  readonly done: boolean;
}

export interface Settings {
  readonly reviews: ReviewSettings;
  readonly notifications: NotificationSettings;
}

// In `api`, after settingsSetReviews:
  settingsSetNotifications: async (notifications: NotificationSettings): Promise<Settings> =>
    call<Settings>("settings_set_notifications", { notifications }),
  /** Calls handler with the agent's key each time the tray menu selects one; resolves to an unsubscribe. */
  onTraySelect: async (handler: (key: string) => void): Promise<() => void> =>
    listen<{ readonly key: string }>("tray-select", (event) => handler(event.payload.key)),
```

**`fixtures.ts`**

```ts
export const notificationSettings = (overrides: Partial<NotificationSettings> = {}): NotificationSettings => ({
  enabled: true,
  needsInput: true,
  done: true,
  ...overrides,
});
```

`fakeApi`: `settingsGet` and `settingsSetReviews` (and
`reviewsProfileRemove`'s `settings`) add `notifications: notificationSettings()`;
add `settingsSetNotifications: vi.fn(async (notifications: NotificationSettings) => ({ reviews: reviewSettings(), notifications }))`
and `onTraySelect: vi.fn(async () => () => undefined)`.

**`uiSlice.ts`**: add a reducer and export it:

```ts
/** Shows Agents with key selected, as the tray menu asks (ADR 0050). */
agentFocused: (state, action: PayloadAction<string>) => {
  state.section = "agents";
  state.selectedKey = action.payload;
},
```

**`features/ui/tray.ts`**

```ts
import type { ThunkAction, UnknownAction } from "@reduxjs/toolkit";
import type { RootState, ThunkExtra } from "../../store";
import { agentFocused } from "./uiSlice";

/** Follows the tray menu's agent choices; resolves to an unsubscribe (ADR 0050). */
export const followTray =
  (): ThunkAction<Promise<() => void>, RootState, ThunkExtra, UnknownAction> =>
  async (dispatch, _getState, { api }) =>
    api.onTraySelect((key) => dispatch(agentFocused(key)));
```

**`App.tsx`**: in `App`, add

```ts
useEffect(() => {
  const stop = dispatch(followTray());
  return () => {
    void stop.then((unlisten) => unlisten());
  };
}, [dispatch]);
```

**`settingsSlice.ts`**

- `SettingsGroup` becomes `"reviews" | "notifications" | "about"`.
- `SettingsState` gains `readonly notificationsError: string | null`
  (initially `null`).
- Add:

  ```ts
  /** Saves a change to the Notifications settings as it's made. */
  export const saveNotificationSettings = createAppThunk<Settings, Partial<NotificationSettings>>(
    "settings/saveNotifications",
    async (change, { extra, getState, rejectWithValue }) => { /* as saveReviewSettings, with base.notifications and settingsSetNotifications */ },
  );
  ```

  `fulfilled` sets `settings` and clears `notificationsError`; `rejected`
  sets `notificationsError` to the error message (`action.payload?.message
  ?? action.error.message ?? "saving failed"`), keeping the old settings.

**`SettingsList.tsx`**: groups become Reviews, Notifications, About.

**`SettingsPane.tsx`**: add a `NotificationsGroup` component
(`<section aria-label="Notifications">` with `<h1 className={styles.title}>Notifications</h1>`,
like `ReviewsGroup`) holding one `Row` labelled `Notify me`, help
`"When one of your agents needs input or is done. Children and hidden workspaces never notify."`,
errors `[notificationsError ?? undefined]`, and three MUI
`FormControlLabel` + `Checkbox` controls (size small):

| Label                       | Field        | Disabled when       |
| --------------------------- | ------------ | ------------------- |
| `Show notifications`        | `enabled`    | never               |
| `When an agent needs input` | `needsInput` | `!enabled`          |
| `When an agent is done`     | `done`       | `!enabled`          |

Each change dispatches `saveNotificationSettings({ <field>: checked })`.
`SettingsPane` renders it when `group === "notifications"` (after the
`settings === null` loading check, in `styles.root`, like Reviews).

**Acceptance** (vitest + Testing Library)

- `uiSlice.test.ts`: `agentFocused("fix-login")` from the Repos section
  → `section === "agents"`, `selectedKey === "fix-login"`.
- `tray.test.ts`: with `fakeApi({ onTraySelect: vi.fn(async (h) => { handler = h; return stop; }) })`
  and `testStore({ api })`, `await store.dispatch(followTray())` resolves
  to `stop`; calling `handler("fix-login")` sets section `agents` and
  `selectedKey` `fix-login`.
- `SettingsPane.test.tsx` (set up as the existing tests there, with the
  group set to `notifications`):
  - the three checkboxes are shown and checked by default;
  - clicking `When an agent is done` calls `settingsSetNotifications`
    with `{ enabled: true, needsInput: true, done: false }`;
  - with `settingsGet` returning `enabled: false`, the other two
    checkboxes are disabled;
  - when `settingsSetNotifications` rejects with `{ code: "state_newer", message: "newer" }`,
    `newer` is shown as an alert.
- `SettingsList` lists Reviews, Notifications, About in that order (in
  an existing settings test or a new assertion).
- `App.test.tsx`: rendering `App` with `fakeApi()` calls `onTraySelect`
  once.
- If importing `@tauri-apps/api/event` breaks a test that mocks
  `@tauri-apps/api/core`, add `transformCallback: vi.fn()` to that mock;
  don't mock `api.ts` itself.
- `npm run typecheck`, `npm test`, and `npm run build` pass.

**Verify**

```console
$ cd desktop && npm ci && npm run typecheck && npm test && npm run build
$ make desktop-test
```
