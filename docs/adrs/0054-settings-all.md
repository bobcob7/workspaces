# 0054. Every setting is shown and edited in Settings, in the desktop app and Workspace Online; `ws config` is the only writer of `config.toml` and keeps the file's comments

- Status: Proposed
- Date: 2026-11-08

## Context

### Problem

`ws` and the desktop app are configured in several places. Only some of
them appear in the Settings section, and only the app's own settings
(`desktop.json`, [0034](0034-desktop-state-file.md)) can be changed
there. To change how short repo specs resolve, how long `ws review wait`
waits, or an agent role, the user has to find and hand-edit files under
`CONFIG_DIR`. Workspace Online ([0053](0053-workspace-online.md)) makes
this worse, because a phone has no shell to edit files with.

How do we make every setting visible and editable from Settings, in both
the desktop app and the web app, without the app writing `ws`'s files
itself, and without losing the comments in a hand-edited `config.toml`?

### What a solution needs

1. **Every setting is visible and editable** from Settings, in Tauri and
   in the browser. A setting that can't be changed while the program runs,
   such as an environment variable, is shown read-only, with where to
   change it.
2. **`ws` stays the only code that touches its own files**
   ([0029](0029-desktop-app.md), criterion 1). The desktop backend runs
   `ws` commands and parses their JSON.
3. **A write can't break the config.** A change is checked with the same
   rules as reading the file (`docs/config.md`). The file is replaced
   atomically, and two writers can't lose each other's change.
4. **A hand-edited file keeps its comments**, and every line the change
   doesn't touch stays byte for byte as it was.
5. **One place per setting.** The same setting isn't stored in two files
   with rules about which one wins.
6. **Small**: no new Go or npm dependency, and each piece can be built and
   reviewed on its own.

### Relevant facts

- **Every configurable thing**, found by an audit on 2026-11-08 (the full
  table is in the [design](../design/settings-all/README.md#audit))[^read-audit]:
  - `config.toml`: `defaults.host`, `defaults.owners`, `review.timeout`,
    `review.remove_finished`, and `review.profiles.<name>.when` and
    `.model`.
  - `desktop.json`: `settings.reviews` (check interval, skip drafts, skip
    mine, only requested, max auto-reviews, review agent model and
    permission mode, per-profile overrides), `settings.notifications`,
    and `settings.open`.
  - Files under `CONFIG_DIR`: review profiles, loops, and machine roles.
  - The environment: `WS_CONFIG_DIR` and `--config-dir`,
    `CLAUDE_CONFIG_DIR`, and Workspace Online's `WS_ONLINE_*` variables.
    The `WS_DESKTOP_*` variables are build-time or test-only.
- **Settings today** has four groups: Reviews, Notifications, Workspaces
  and About. They edit `desktop.json` and, through Reviewer profiles,
  `ws review profiles add/edit/rm`. The web app hides the Workspaces
  group. None of `config.toml` can be edited, and machine roles aren't
  shown at all[^read-audit].
- **Reviewer profiles are configured twice.** The desktop's Reviewer
  profiles table saves its changes in `desktop.json`
  (`settings.reviews.profiles`), and passes them as `--profile` flags to
  the reviews the app starts. `config.toml`'s `[review.profiles.<name>]`
  applies to every review, and `ws review profiles` lists the profiles
  with it applied[^read-profiles].
- **`config.toml` is read-only to `ws` today.** `ws` writes it once, if
  it's missing, and docs/config.md promises "It's never overwritten"[^read-config].
- **The TOML library can't keep comments.** BurntSushi/toml v1.6.0, the
  only TOML library `ws` depends on, encodes Go values to TOML and has no
  syntax tree to edit[^toml]. `pelletier/go-toml/v2` is in `go.sum` only
  as a golangci-lint dependency, and its position-aware parser is in an
  `unstable` package[^go-toml].
- **The config's shape is small.** It has six key shapes in three
  tables. Every value is a string, a list of strings, or a bool[^read-config].
- **Adding a command to the app touches four files.** Each Tauri command
  must also be in `online/dispatch.rs` (a test compares it with
  `generate_handler!`), and in `desktop/test/tauri-mock.js` for
  screenshots. `web-mock.js` forwards every command to the Tauri mock[^read-dispatch].
- **The app already resolves specs.** `cache_resolve` wraps `ws cache
  resolve` for the Repos section's Add dialog. Nothing wraps `ws cache
  search`[^read-dispatch].

## Decision

We will add **`ws config`**, which lists, reads, sets and unsets the keys
of `config.toml`, and have Settings show **every setting** through it and
the existing commands, in the desktop app and the web app.

**`ws config`** ([reference](../tools/ws-config.md)) has four operations:
`list`, `get <key>`, `set <key> <value>...` and `unset <key>`. The keys are
the six shapes above. A profile's key names its profile, as in
`review.profiles.security.model`. `set` checks the value by the rules in
docs/config.md and stores it the way `ws` reads it: lower-cased, without
duplicates, and with durations in short form (`"90m"` is stored as
`"1h30m"`). It refuses a profile name that `ws review profiles` doesn't
list. With `--json`, each operation prints the effective value, the
default, and whether the file sets the key.

**Editing the text, not the data.** `set` and `unset` change only the
lines that hold the key: the `key = value` line, or all the lines of a
multi-line array. A trailing comment on the last of those lines is kept.
A missing key is added after the last key in its table, and a missing
table is added at the end of the file. When `unset` leaves a
`[review.profiles.<name>]` table with no keys, its header is removed as
well. The new text is then parsed with the same parser and checks as
any other read, and must decode to the value that was asked for. If it
doesn't, for example because the file sets the key as a dotted key or
an inline table, the command fails with `config_uneditable` and changes
nothing. The read, edit and write happen under an exclusive lock on
`CONFIG_DIR/config.lock`. The file is written to a temp file next to
the real one (following a symlink), synced, given the old file's mode,
and renamed into place.

**Settings in the app.** A new **Repos** group edits `defaults.host` and
`defaults.owners`. It has a **Try a spec** box that resolves a spec with
`ws cache resolve`, showing the id, upstream, whether it's cached, and
which rule matched (`source`) or, for an ambiguous spec, the candidates.
The box also lists `ws cache search` matches as the user types. The
**Reviews** group gains `review.timeout` and `review.remove_finished`,
and its Reviewer profiles table now writes
`review.profiles.<name>.when` and `.model` through `ws config`. A new
**Agent roles** group lists, edits, adds and removes machine roles
through `ws machine roles`. **About** shows where each file is, the
environment variables in effect, and in the web app the server's
`WS_ONLINE_*` settings, without the password. All of these are
read-only, with a note on where to change them. The **Workspaces**
group is shown in the web app too, with a note that the web app has no
Open buttons.

**One place for profile settings.** `desktop.json`'s
`settings.reviews.profiles` is no longer written. If it holds overrides
(from an older app), the backend keeps applying them as before, and the
Reviews group shows a banner with **Move to ws config**. The button
writes each override with `ws config set`, then clears the field. No
data is lost, and nothing changes until the user moves the overrides.

**New desktop commands**: `config_list`, `config_set`, `config_unset`,
`cache_search`, `settings_environment`, `roles_list`, `roles_show`,
`roles_add`, `roles_edit` and `roles_remove`. Each is registered in
`lib.rs` and `online/dispatch.rs`, and handled in `tauri-mock.js`.
`web-mock.js` adds the online part of `settings_environment`. The exact
shapes are in the [tasks](../design/settings-all/tasks.md).

**New error codes** (ADR 0016 allows adding them): `config_key_unknown`
(exit 2), `config_value_invalid` (exit 2) and `config_uneditable`
(exit 1). `profile_unknown` (exit 2) is reused.

### Why this option

1. **Visible and editable everywhere**: every row in the audit maps to a
   control or a read-only row, and every one goes through `call()`, so the
   desktop app and the web app get the same controls.
2. **`ws` owns its files**: the backend runs `ws config` and `ws machine
   roles`, and never opens `config.toml` or a role file.
3. **Safe writes**: a change is parsed and checked like any other read
   before anything is written. The lock serializes writers, and the
   rename means a reader sees the old file or the new one, never part of
   one.
4. **Comments kept**: only the key's lines change. The weak spot is a key
   written in a form the editor doesn't look for (a dotted key or an inline
   table). Checking the result catches it and refuses, rather than writing
   a file that's wrong.
5. **One place per setting**: profile settings move to `config.toml`,
   which also covers reviews started from the CLI. The legacy field is only
   read, and the user can empty it with one click.
6. **Small**: the editor is about 200 lines of Go with no new dependency.
   The work splits into seven tasks of one or two layers each.

## Alternatives

### Re-encode the whole file with BurntSushi/toml

**Pros**

- Few lines of code, and the output is always valid TOML.

**Cons**

- Drops every comment, including the help text in the generated file.
  This fails criterion 4.

### Use `pelletier/go-toml/v2/unstable` for byte ranges

**Pros**

- A real parser finds a key's exact bytes, in any form.

**Cons**

- Adds a direct dependency on a package that calls itself unstable, to
  edit six key shapes that a line scanner handles. Fails criterion 6.

### Let the desktop backend edit `config.toml` in Rust

**Pros**

- No new CLI command.

**Cons**

- Puts a second writer of `ws`'s file in another language, with its own
  copy of the rules. Fails criterion 2, and the CLI user gets nothing.

### Keep profile overrides in `desktop.json`, beside `config.toml`'s

**Pros**

- No migration.

**Cons**

- The same setting in two files, where the app's copy wins only for the
  reviews the app starts. Fails criterion 5. The table can show only
  one of the two.

### `ws cache resolve --host/--owner`, to try specs against unsaved values

**Pros**

- Shows what a spec would resolve to before the change is saved.

**Cons**

- Adds flags to a command for one screen. Host and owners save on blur,
  and resolving again afterwards gives the same answer one step later.

## Consequences

- **docs/config.md changes its promise.** `ws` writes `config.toml` when
  it's missing, and when `ws config set` or `unset` runs. The generated
  file's first line says so. Comments and untouched lines are kept.
- **The Settings section grows to six groups**: Repos, Reviews, Agent
  roles, Notifications, Workspaces and About, the same in the browser.
- **Old `desktop.json` profile overrides** keep working until they're
  moved. A later ADR can drop the field once no app writes it.
- **An invalid `config.toml` still fails every `ws` command**, including
  `ws config`, with `config_invalid`. Settings shows the message, and the
  file has to be fixed by hand. Making `ws config` repair a broken file is
  left for later.
- **The environment stays read-only.** Changing `WS_CONFIG_DIR`,
  `CLAUDE_CONFIG_DIR` or `WS_ONLINE_*` needs a restart. The rows say so.
- **How we'll know the code follows this**: `internal/config` tests check
  that every untouched line and comment is kept byte for byte. The
  dispatch test checks the command list. The desktop tests check that
  each group calls the matching command. Screenshots at 390×844 and
  1280×800 show every group (T7).
- **Not verified yet**: how the editor handles files with CRLF line
  endings. The tasks require CRLF to be kept, and test it.

## References

[^read-audit]: Read 2026-11-08 at commit 1b2f205: `internal/config/config.go`, `internal/config/loader.go`, `desktop/src-tauri/src/reviews/store.rs` (`Settings`), `desktop/src/features/settings/*.tsx`, `desktop/src-tauri/src/online/config.rs`, `docs/tools/ws-machine.md` (roles), and `grep -rhoE '"(WS|CLAUDE)_[A-Z_]+"' internal cmd desktop/src-tauri/src`.
[^read-profiles]: Read 2026-11-08 at commit 1b2f205: `desktop/src-tauri/src/reviews/plan.rs` and `checker.rs` (`profiles::choices`), and `internal/cli/review_profiles.go` (`profileInfo` applies `a.config.Review.Profiles`).
[^read-config]: Read 2026-11-08 at commit 1b2f205: `docs/config.md` ("The generated file", "Schema") and `internal/config/config.go` (`defaultFile`, `normalize`, `check`).
[^read-dispatch]: Read 2026-11-08 at commit 1b2f205: `desktop/src-tauri/src/online/dispatch.rs` (`COMMANDS`, test `commands_match_generate_handler`), `desktop/test/web-mock.js` (`invokeRoute`), and `desktop/src-tauri/src/cache.rs` (`cache_resolve`).
[^toml]: BurntSushi, "toml" v1.6.0, package documentation. <https://pkg.go.dev/github.com/BurntSushi/toml@v1.6.0> (accessed 2026-11-08). It has `Decode`, `MetaData` and `Encoder`, which writes from Go values, and no type for a document's comments.
[^go-toml]: pelletier, "go-toml/v2/unstable." <https://pkg.go.dev/github.com/pelletier/go-toml/v2/unstable> (accessed 2026-11-08). "This package is unstable. Its API may change between minor versions." `go.mod` lists `github.com/pelletier/go-toml/v2 v2.4.3 // indirect`.
