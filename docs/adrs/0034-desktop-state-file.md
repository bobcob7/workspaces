# 0034. The desktop app keeps watched repos, dismissals, auto-review records, and settings in one `desktop.json` in its own config directory

- Status: Proposed
- Date: 2026-10-24

## Context

### Problem

The Reviews section ([0031](0031-desktop-sections-rail.md),
[design](../design/desktop-sections/reviews.md)) needs state that neither
`ws` nor `claude` records:

- which cached repos are **watched**, and which have **auto-review** on;
- which pull requests you **dismissed**, keyed by repo, number, and head
  commit so a push un-dismisses;
- which reviews were **started automatically**, so the app can label them
  "auto", count them against the auto-review limit, and not start one again
  after you remove it;
- the **settings** in the Settings section's Reviews group.

The app keeps no state today ([0029](0029-desktop-app.md)). Where should
this live, and who writes it?

### What a solution needs

1. **Survives a restart**, and a crash mid-write doesn't lose or corrupt it.
2. **Doesn't blur ownership** with `ws`: `ws` owns `CONFIG_DIR` and its
   formats ([0011](0011-config-dir.md), [0012](0012-workspace-layout.md)).
3. **Changes apply as you make them** (Settings has no Save button), and two
   quick toggles don't overwrite each other.
4. **Stays small**: dismissals and auto-review records don't grow without
   bound.

### Relevant facts

- Tauri's `PathResolver::app_config_dir` "Returns the path to the suggested
  directory for your app's config files" and "Resolves to
  `config_dir/${bundle_identifier}`"; on macOS `config_dir` is
  `$HOME/Library/Application Support`[^tauri-path]. The app's identifier is
  `com.github.bobcob7.workspaces` (`desktop/src-tauri/tauri.conf.json`).
- `ws`'s `CONFIG_DIR` is `$WS_CONFIG_DIR`, else
  `os.UserConfigDir()/workspaces` ([0011](0011-config-dir.md)), which on
  macOS is also under `~/Library/Application Support`. `ws` doesn't print
  it, and has no command to set config keys ([config.md](../config.md)).
- `ws` writes its own records by writing a temp file and renaming it into
  place ([0011](0011-config-dir.md), [0022](0022-workspace-record-before-rename.md)).
- A record from a newer `ws` is left alone rather than rewritten
  ([0027](0027-respawn-lock-and-record-versions.md)).
- Repo ids in the cache are `host/owner/name`
  ([0008](0008-repository-cache.md)); the Reviews section only watches cached
  repos.

## Decision

We will keep one JSON file, `desktop.json`, in the app's config directory
(`~/Library/Application Support/com.github.bobcob7.workspaces/` on macOS),
read and written only by the app's Rust backend:

```json
{
  "version": 1,
  "settings": {
    "reviews": {
      "checkIntervalMinutes": 5,
      "skipDrafts": true,
      "skipMine": true,
      "onlyRequested": false,
      "maxAutoReviews": 2,
      "taskTemplate": "Review pull request {repo}#{number}, …",
      "model": "",
      "permissionMode": ""
    }
  },
  "reviews": {
    "repos": { "github.com/bobcob7/buff-bear": { "watch": true, "autoReview": true } },
    "dismissed": [{ "repo": "github.com/bobcob7/buff-bear", "number": 40, "head": "<40-hex>" }],
    "autoStarted": [{ "repo": "github.com/bobcob7/buff-bear", "number": 42, "head": "<40-hex>", "workspace": "review-buff-bear-42-3f2a9c1" }]
  }
}
```

- **One writer.** The backend loads the file at start and holds it behind a
  mutex. Each change (a switch, a dismissal, a setting) is a command that
  updates it in memory and writes the whole file to `desktop.json.tmp`,
  then renames it over `desktop.json`. A failed write returns an error and
  keeps the old value in memory, so the control shows the error.
- **Missing or unreadable** file: defaults, and the next change writes a
  fresh file. A missing key takes its default, so adding a setting needs no
  migration.
- **A file with a newer `version`** is read but never written; changes then
  fail with `state_newer`, as `ws` does with newer records
  ([0027](0027-respawn-lock-and-record-versions.md)).
- **Pruning.** After a repo is checked successfully, dismissals and
  auto-review records for its pull requests that are no longer open, or
  whose head has moved, are dropped. A watched repo that's no longer cached
  is ignored, not deleted, so re-adding it keeps its switches.
- **`ws`'s settings stay in `ws`.** Nothing here duplicates `config.toml`;
  the Settings section edits only `desktop.json`.

### Why this option

1. The temp-and-rename write is the one `ws` uses for its records, so a crash
   leaves the old file or the new one, never half of one.
2. The file lives under the app's own identifier, beside, not inside,
   `CONFIG_DIR`. `ws` never sees it, and the app doesn't need to know where
   `CONFIG_DIR` is.
3. A single writer behind a mutex serializes changes, so two quick toggles
   both land. Writes are rare (a click each), so rewriting the whole file is
   cheap.
4. Pruning on each successful check keeps only records for open pull
   requests at their current heads.

It's weak in one way: someone who runs two `ws` setups with different
`WS_CONFIG_DIR`s shares one `desktop.json` between them. Watching is keyed by
repo id, so the same repo in both is watched in both. We think that's
acceptable for a tool one person runs, and arguably right.

## Alternatives

### `desktop.json` in `CONFIG_DIR`

**Pros:** one directory to back up or wipe; `WS_CONFIG_DIR` isolates the
app's state along with `ws`'s.
**Cons:** the app would write a file into a directory `ws` owns, in a format
`ws` doesn't know; and it would have to learn `CONFIG_DIR`, which `ws`
doesn't print, or re-implement [0011](0011-config-dir.md)'s resolution.
Could be revisited if `ws` gains a `ws config path` command.

### New keys in `ws`'s `config.toml`, with a `ws` command to set them

**Pros:** one config for everything; settings could be edited by hand.
**Cons:** dismissals and auto-review records are state, not config, and
change on every check; `ws` would have to carry Reviews concepts it never
uses, and gain a command ([0014](0014-command-set.md)) only the app calls.

### Browser storage in the webview (`localStorage`)

**Pros:** no Rust code; easy to use from Redux.
**Cons:** the checker and auto-review run in the backend
([0035](0035-desktop-review-checker.md)) and need the watched repos and rules
without a round trip to the webview; and the data sits in WebKit's storage,
which isn't a file anyone can read or back up.

## Consequences

- The app now has on-disk state. Removing the app doesn't remove
  `desktop.json`; deleting it resets Reviews to nothing watched and default
  settings.
- [0029](0029-desktop-app.md)'s "the app adds no on-disk state" no longer
  holds for Reviews; agents and workspaces are still read only from `ws` and
  `claude`.
- Conformance: `cargo test` in `desktop/src-tauri` covers defaults for a
  missing file and missing keys, the atomic write, refusing to write a newer
  version, and pruning (`reviews::store`).
- Adding a setting means adding a key with a default; removing one leaves
  the old key ignored in existing files.

## References

[^tauri-path]: Tauri 2 API docs, `tauri::path::PathResolver`, `app_config_dir` and `config_dir`. <https://docs.rs/tauri/latest/tauri/path/struct.PathResolver.html> (accessed 2026-10-24). "Returns the path to the suggested directory for your app's config files." "Resolves to `config_dir/${bundle_identifier}`, unless overridden with the app > appDirectoriesOverride config." `config_dir`: "macOS: Resolves to `$HOME/Library/Application Support`."
