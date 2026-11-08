# Settings for everything

Design for [0054](../../adrs/0054-settings-all.md): every setting `ws`
and the desktop app have can be seen in Settings, and changed there when it
can change while the app runs. This works in the desktop app (Tauri) and in
Workspace Online, which is the same React app over HTTP. The
implementation is split into [tasks](tasks.md). The `ws config` command is
described in [docs/tools/ws-config.md](../../tools/ws-config.md).

## Audit

Audited on 2026-11-08 at commit 1b2f205. "Today" is what Settings did
before this change. "After" is the group and the control that cover the
setting.

### `config.toml` (owned by `ws`)

| Setting | Today | After |
| ------- | ----- | ----- |
| `defaults.host` | not shown | **Repos** › Default host: a text field, saved on blur with `config_set` |
| `defaults.owners` | not shown | **Repos** › Default owners: an ordered list with add, remove, and move up and down, saved with `config_set` |
| `review.timeout` | not shown | **Reviews** › Wait for a review: a select (15m, 30m, 1h, 90m, 2h, 4h, plus the current value), saved with `config_set` |
| `review.remove_finished` | not shown | **Reviews** › Clean up finished reviews: a checkbox, saved with `config_set` |
| `review.profiles.<name>.when` | shown as the baseline in Reviewer profiles; changes went to `desktop.json` | **Reviews** › Reviewer profiles › Runs: saved with `config_set` |
| `review.profiles.<name>.model` | same | **Reviews** › Reviewer profiles › Model: saved with `config_set`, or `config_unset` for Default |
| (repo resolution, a tool) | Repos section › Add repo previews one spec | **Repos** › Try a spec: `cache_resolve` on Enter or the Resolve button, and `cache_search` as you type |

### `desktop.json` (owned by the app; ADR 0034)

| Setting | Today | After |
| ------- | ----- | ----- |
| `reviews.checkIntervalMinutes`, `skipDrafts`, `skipMine`, `onlyRequested`, `maxAutoReviews`, `model`, `permissionMode` | Reviews | unchanged |
| `reviews.profiles` (per-profile overrides) | Reviewer profiles table | no longer written. If it isn't empty, a banner offers **Move to ws config** |
| `notifications.enabled`, `needsInput`, `done` | Notifications | unchanged |
| `open.fileManager`, `open.editor` | Workspaces (desktop only) | Workspaces, also in the web app, with a note that the web app has no Open buttons |

### Files under `CONFIG_DIR` (owned by `ws`)

| What | Today | After |
| ---- | ----- | ----- |
| `review/profiles/<name>.md` | Reviewer profiles › New, Edit, Remove (`ws review profiles add/edit/rm`) | unchanged |
| `loops/<name>.toml` | the Loops section (`ws loop add/edit/rm/pause/resume`) | unchanged, and About links to it |
| `machine/roles/<name>.md` | not shown | **Agent roles**: list, add, edit and remove (`ws machine roles`) |

### Environment and flags (read-only while running)

| Setting | Today | After |
| ------- | ----- | ----- |
| `--config-dir` / `WS_CONFIG_DIR` → `CONFIG_DIR` | not shown | **About** › Locations: `config.toml`'s path (from `config_list`) and `WS_CONFIG_DIR` |
| `CLAUDE_CONFIG_DIR` | not shown | **About** › Locations |
| `desktop.json`'s path | not shown | **About** › Locations |
| app PATH, `ws`, `claude` | About | unchanged |
| `WS_ONLINE_USERNAME`, `_ADDR`, `_PUBLIC_URL`, `_SECURE_COOKIE`, `_SESSION_DAYS`, `_TRUST_PROXY`, `_DATA_DIR`, `_WEB_DIR`, `_VAPID_SUBJECT` | not shown | **About** › Server, web app only, read-only. The password is never sent |
| `WS_ONLINE_PASSWORD`, `_PASSWORD_HASH` | not shown | only whether it's a plain password or a hash |
| `WS_DESKTOP_COMMIT` (build time), `WS_DESKTOP_ATTACH` and `_WATCH` (tests), `WS_ATTACH_HELPER` (internal), `WS_LOOP_VAR_*` (set by `ws` for scripts) | not shown | not settings, so not shown |
| Global flags `--json`, `--quiet`, `-v` | not settings | not shown |
| Tauri config (`tauri.conf.json`: window size, identifier) | build time | not shown |

## Settings groups after the change

The list is in this order, the same on desktop and web:

1. **Repos**: Default host, Default owners, Try a spec.
2. **Reviews**: Check for pull requests, Wait for a review (new), Clean up
   finished reviews (new), Reviewer profiles (now in `config.toml`),
   Review agent, Auto-review only, At most.
3. **Agent roles** (new): the roles table, and a role editor in a
   dialog.
4. **Notifications**: unchanged. The web app adds This browser.
5. **Workspaces**: File manager, Editor, also in the web app.
6. **About**: versions (unchanged), Locations (new), and Server (new,
   web app only).

Every `config.toml` row shows a small **from config.toml** caption with
the file's path in a tooltip. A row whose key isn't set shows
"default", and a Reset button appears once it is set (`config_unset`).
Errors from `ws` (`config_value_invalid`, `config_uneditable`,
`config_invalid`, `profile_unknown`) appear under the control, and the
control goes back to the saved value.

## Data flow

```
SettingsPane ──► settingsSlice thunks ──► api.ts call() ──┬─► Tauri invoke ──► desktop/src-tauri/src/ws_config.rs ─► ws config … --json
                                                          └─► POST /api/invoke/<cmd> ─► online/dispatch.rs ──┘
```

`config_list` is fetched when Settings opens, and again after every
`config_set` and `config_unset`. Each returns the changed setting, and
the slice swaps it into the list by key. After a Reviewer profiles
change, the slice fetches `reviews_profiles` again, because the listed
`when` and `model` come from `config.toml`.

## Screenshots

T7 makes the layouts ready to screenshot. The orchestrator's screenshot
child then saves them to `desktop/docs/` with Playwright, using
`desktop/test/tauri-mock.js` (and `web-mock.js` for the web shots):

| File | Viewport | State |
| ---- | -------- | ----- |
| `settings-repos.png` | 1280×800 | Repos with 2 owners, and `buff-bear` resolved (source `cache`) |
| `settings-repos-ambiguous.png` | 1280×800 | Try a spec with `api`: the ambiguous candidates |
| `settings-reviews-config.png` | 1280×800 | Reviews, with the timeout and clean-up rows and the move banner |
| `settings-roles.png` | 1280×800 | Agent roles, with the three built-ins and one override |
| `settings-role-edit.png` | 1280×800 | The role editor open on `reviewer` |
| `settings-about-locations.png` | 1280×800 | About › Locations |
| `online-settings-repos-mobile.png` | 390×844, web | Repos on a phone |
| `online-settings-roles-mobile.png` | 390×844, web | Agent roles on a phone |
| `online-settings-about-mobile.png` | 390×844, web | About with Server on a phone |

The named layout checks are listed in [T7](tasks.md#t7-phone-and-desktop-layouts-ready-for-screenshots).
