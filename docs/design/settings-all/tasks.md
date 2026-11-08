# Settings for everything: implementation tasks

These tasks implement [0054](../../adrs/0054-settings-all.md),
[`ws config`](../../tools/ws-config.md), and the [design](README.md).
Each task builds, passes its tests, and can be merged on its own. Each is
one commit on its own branch, with a conventional commit message in the
imperative mood, for example `feat: add ws config (ADR 0054)`.

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| T1 | `internal/config`: edit `config.toml` text, keeping comments | nothing | small | T3, T4 |
| T2 | `ws config list/get/set/unset`, with guides | T1 | medium | T3, T4, T5 |
| T3 | Tauri and online: `config_*`, `cache_search`, `settings_environment`, and the mock | nothing (the contract is in this file) | medium | T1, T2, T4 |
| T4 | Desktop data layer, and the Repos settings group | nothing (the contract is in this file) | large | T1, T2, T3 |
| T5 | Desktop Reviews, Workspaces and About groups | T4 | medium | T2 |
| T6 | Agent roles: Tauri commands, mock, and the settings group | T3, T5 | large | none |
| T7 | Phone and desktop layouts ready for screenshots | T6 | small | none |

T1 ∥ T3 ∥ T4 and T2 ∥ T3 ∥ T4: their files don't overlap. T1 and T2 are
Go in `internal/`, T3 is Rust in `desktop/src-tauri/` plus
`desktop/test/*.js`, and T4 is TypeScript in `desktop/src/`. T5 and T6 both
change `SettingsPane.tsx`, `settingsSlice.ts`, `api.ts` and the fixtures,
so they run in order. T6 also changes T3's Rust files.

**Read before any task:** the "Decision" section of
`docs/adrs/0054-settings-all.md`, and `docs/tools/ws-config.md`. If this
file and those two disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style, file layout and test style. Every exported or `pub` item gets a
  short doc comment. Add no other comments unless the logic isn't
  obvious. Prefer early returns, and keep functions small.
- **Go** (`internal/`): wrap errors with `fmt.Errorf("…: %w", err)`, in
  lower case, without "failed to". Tests use
  `github.com/stretchr/testify/assert` and `require`. Write one `TestX`
  per function or method, with `t.Run` subtests per behavior, and
  table-driven subtests (`tests := []struct{…}`) where several inputs
  share one check. Use `t.Context()` and `t.TempDir()`. Run `make fmt`.
  `make lint` must pass. Don't call `exec.Command`.
- **JSON** from the Go CLI is exactly the shape in
  `docs/tools/ws-config.md`. Don't change any existing JSON field, error
  code or exit code.
- **Rust** (`desktop/src-tauri/`): `cargo fmt` clean, and `cargo clippy
  --all-targets -- -D warnings` clean in both builds. Every command is
  `#[cfg_attr(feature = "desktop", tauri::command)]`. Unit tests go in the
  file's `#[cfg(test)] mod tests`, using the fake-`ws` helpers that file
  or `cache.rs` already has.
- **TypeScript** (`desktop/src/`): strict types, `readonly` fields on
  interfaces, no `any`. Components are `const X = (props): JSX.Element =>`,
  with styles in the feature's CSS module. Tests use vitest with
  `@testing-library/react` and `user-event`, the helpers in
  `desktop/src/test/fixtures.ts` (`fakeApi()`, `testStore()`) and
  `desktop/src/test/render.tsx` (`renderWithStore`). Query by role and
  accessible name, not by class. **No new npm dependencies.** MUI is
  already installed.
- **Web parity**: never hide a settings control with `isWeb()`. Where
  something works differently in the browser, show a one-line note
  instead.
- Don't touch files a task doesn't list, except to fix a compile error
  the task itself caused. Say so in your report.

**Verify commands.** Every task runs the commands its section lists, from
the repo root. `make lint` and `make test` cover Go. `make desktop-test`
covers the frontend, the Rust desktop build and the Rust online build. If
vitest times out under load, rerun once with `cd desktop && npx vitest run
--testTimeout=30000`. Report both runs.

## Contract shared by T2-T7

### `ws config` JSON (T2 prints it; T3 parses it)

```jsonc
// ws config --json list
{"path": "/…/config.toml", "settings": [Setting, …], "warnings": []}
// ws config --json get <key>
{"path": "/…/config.toml", "setting": Setting, "warnings": []}
// ws config --json set -- <key> <value>...   and   ws config --json unset -- <key>
{"path": "/…/config.toml", "setting": Setting, "changed": true, "warnings": []}
// Setting
{"key": "defaults.owners", "type": "strings", "value": ["a"], "default": [], "set": true}
```

`type` is one of `"string" | "strings" | "duration" | "bool"`. Errors are
the usual `{"error": {"code", "message", "details"}}` on stderr. The
desktop runner already turns that into an `AppError`.

### Tauri commands (T3 adds them; T4 and T5 call them)

Arguments are camelCase JSON, as `invoke` sends them.

| Command | Args | Returns | Runs |
| ------- | ---- | ------- | ---- |
| `config_list` | none | `ConfigListing` | `ws config --json list` |
| `config_set` | `{ key: string, values: string[] }` | `ConfigChange` | `ws config --json set -- <key> <values...>` |
| `config_unset` | `{ key: string }` | `ConfigChange` | `ws config --json unset -- <key>` |
| `cache_search` | `{ query: string, refs: boolean, limit: number }` | `SearchMatch[]` | `ws cache --json search --limit=<limit> [--refs] -- <query>`; a blank query returns `[]` without running `ws` |
| `settings_environment` | none | `Environment` | nothing: it reads the process environment and the app state |

```ts
// desktop/src/api.ts (T4 adds these; T3 serializes the same shapes)
export type ConfigType = "string" | "strings" | "duration" | "bool";
export type ConfigValue = string | boolean | readonly string[];
export interface ConfigSetting {
  readonly key: string;
  readonly type: ConfigType;
  readonly value: ConfigValue;
  readonly default: ConfigValue;
  readonly set: boolean;
}
export interface ConfigListing {
  readonly path: string;
  readonly settings: readonly ConfigSetting[];
}
export interface ConfigChange {
  readonly path: string;
  readonly setting: ConfigSetting;
  readonly changed: boolean;
}
export interface SearchMatch {
  readonly id: string;
  readonly ref: string | null;
}
/** Workspace Online's WS_ONLINE_* settings; never the password itself. */
export interface OnlineSettings {
  readonly username: string;
  readonly addr: string;               // "0.0.0.0:8080"
  readonly publicUrl: string | null;   // the origin, e.g. "https://ws.example.com"
  readonly secureCookie: boolean;
  readonly sessionDays: number;
  readonly trustProxy: boolean;
  readonly dataDir: string;
  readonly webDir: string;
  readonly vapidSubject: string;
  readonly password: "plain" | "hash";
}
export interface Environment {
  /** desktop.json's path; null when the app keeps settings in memory only. */
  readonly stateFile: string | null;
  /** WS_CONFIG_DIR, or null when unset or empty. */
  readonly wsConfigDir: string | null;
  /** CLAUDE_CONFIG_DIR, or null when unset or empty. */
  readonly claudeConfigDir: string | null;
  /** The PATH the app runs ws and claude with. */
  readonly path: string;
  /** Set only by ws-online. */
  readonly online: OnlineSettings | null;
}
```

The `api` object (T4) gains:

```ts
configList: async (): Promise<ConfigListing> => call<ConfigListing>("config_list"),
configSet: async (key: string, values: readonly string[]): Promise<ConfigChange> =>
  call<ConfigChange>("config_set", { key, values }),
configUnset: async (key: string): Promise<ConfigChange> => call<ConfigChange>("config_unset", { key }),
cacheSearch: async (query: string, refs: boolean, limit: number): Promise<readonly SearchMatch[]> =>
  call<SearchMatch[]>("cache_search", { query, refs, limit }),
settingsEnvironment: async (): Promise<Environment> => call<Environment>("settings_environment"),
```

### Settings groups (T4 adds them; T5 and T6 fill them in)

```ts
// settingsSlice.ts
export type SettingsGroup = "repos" | "reviews" | "roles" | "notifications" | "workspaces" | "about";
```

`SettingsList` shows, in order: Repos, Reviews, Agent roles,
Notifications, Workspaces, About. All six appear in the web app too. The
initial group stays `"reviews"`.

---

## T1. `internal/config`: edit `config.toml` text, keeping comments

- **Id:** T1. **Depends on:** nothing. **Size:** small. **Parallel with:** T3, T4.

**Read:** `internal/config/config.go`, `internal/config/loader.go`,
`internal/config/config_test.go` (for the test style),
`docs/tools/ws-config.md` ("Keys" and "How the file is changed").

**Create**

- `internal/config/edit.go`
- `internal/config/edit_test.go`

**Change**

- `internal/config/config.go`: change only the first line of
  `defaultFile` to `# ws configuration. Edit it by hand, or with ws config set; ws keeps your comments.`
  The existing test that `defaultFile` parses to `Default()` must still
  pass.

**`edit.go`: exported API**

```go
package config

// ErrKeyUnknown marks a key that isn't one of ws config's keys.
var ErrKeyUnknown = errors.New("unknown config key")

// ErrValueInvalid marks a value that breaks its key's rule.
var ErrValueInvalid = errors.New("invalid config value")

// ErrUneditable marks a file that sets the key in a form Set and Unset
// don't edit, such as a dotted key or an inline table.
var ErrUneditable = errors.New("config key can't be edited in place")

// Type is a setting's value type, as ws config --json prints it.
type Type string

const (
	TypeString   Type = "string"
	TypeStrings  Type = "strings"
	TypeDuration Type = "duration"
	TypeBool     Type = "bool"
)

// Setting is one key of config.toml, as ws config lists it. Value is the
// effective value: the file's, or Default. Value and Default are string,
// []string, or bool; a duration is its short string ("1h30m").
type Setting struct {
	Key     string `json:"key"`
	Type    Type   `json:"type"`
	Value   any    `json:"value"`
	Default any    `json:"default"`
	Set     bool   `json:"set"`
}

// Settings parses data as config.toml, with the same checks as Load, and
// returns every key in docs/tools/ws-config.md order: defaults.host,
// defaults.owners, review.timeout, review.remove_finished, then .when and
// .model for each [review.profiles.<name>] table, sorted by name.
func Settings(data []byte) ([]Setting, error)

// Lookup returns key's Setting in data. A well-formed profile key whose
// table isn't in data has Set false and the default. An unknown key
// wraps ErrKeyUnknown.
func Lookup(data []byte, key string) (Setting, error)

// Set returns data with key set to values, plus whether the text changed.
// Values are checked and normalized by the key's rule (ErrValueInvalid).
// Only key's lines change (see docs/tools/ws-config.md). The result is
// parsed again and must decode to the normalized value, or Set returns
// ErrUneditable.
func Set(data []byte, key string, values []string) ([]byte, bool, error)

// Unset returns data without key, plus whether the text changed. A key
// that isn't set leaves data as it is. A [review.profiles.<name>] table
// left with no keys loses its header line too.
func Unset(data []byte, key string) ([]byte, bool, error)

// WriteFile replaces path with data atomically. It follows a symlink at
// path, writes a temp file in the target's directory, syncs it, gives it
// the old file's mode (0o644 if there is none), and renames it into place.
func WriteFile(path string, data []byte) error

// FormatDuration is d in short form: "1h30m", "1h", "45s", "0s".
func FormatDuration(d time.Duration) string
```

**Keys and rules.** A key is one of `defaults.host`,
`defaults.owners`, `review.timeout`, `review.remove_finished`,
`review.profiles.<name>.when` and `review.profiles.<name>.model`.
`<name>` must match `^[a-z0-9-]+$`. Anything else wraps `ErrKeyUnknown`,
naming the key. `Set` takes these values:

| Key | Values | Normalized and checked | Written as |
| --- | ------ | ---------------------- | ---------- |
| `defaults.host` | exactly 1 | lower-cased, then `validateHost` | `host = "x"` |
| `defaults.owners` | 0 or more | each lower-cased, then `validateOwner`; repeats dropped, the first kept | `owners = ["a", "b"]`, or `owners = []` |
| `review.timeout` | exactly 1 | `time.ParseDuration`; must be > 0 | `timeout = "<FormatDuration>"` |
| `review.remove_finished` | exactly 1 | `true` or `false` exactly | `remove_finished = true` |
| `…<name>.when` | exactly 1 | `always`, `auto`, or `off` | `when = "auto"` |
| `…<name>.model` | exactly 1 | may be `""`; no whitespace | `model = "opus"` |

A string that contains `"`, `\` or a control character wraps
`ErrValueInvalid`. That way a string is written as `"` + s + `"`. A wrong
number of values also wraps `ErrValueInvalid`. The CLI checks the count
first and reports `usage` (T2). Validation errors from `validateHost` and
`validateOwner` wrap `ErrInvalid` today. Wrap them again, as
`fmt.Errorf("%w: %w", ErrValueInvalid, err)`, so `errors.Is` matches both.

**`FormatDuration`**: `s := d.String()`. If `s` ends in `"m0s"`, drop
the `"0s"`. Then, if it ends in `"h0m"`, drop the `"0m"`. So 90m gives
`1h30m`, 60m gives `1h`, 45s gives `45s` and 0 gives `0s`.

**Effective values** (`Settings`, `Lookup`): decode with `toml.Decode`
into `Config` (start from `Default()`), and use the `MetaData` for `Set`
(`meta.IsDefined("review", "timeout")`, and so on). Run the same checks
`parse` runs: unknown keys and `normalize`. On failure, return the
`ErrInvalid` error unchanged. Value and default per key:

- host: `cfg.Defaults.Host`, default `"github.com"`.
- owners: the normalized owners (never nil, use `[]string{}`), default
  `[]string{}`.
- timeout: `FormatDuration(cfg.Review.WaitTimeout())`, default `"1h"`.
- remove_finished: `cfg.Review.RemovesFinished()`, default `true`.
- when and model: the profile's field, default `""`.

**The text editor** works on lines. Split `data` after each `"\n"`, and
keep each line's ending (`"\n"`, `"\r\n"`, or none on the last line). If
the file uses `"\r\n"`, new lines get `"\r\n"` too.

1. **Sections.** A line whose trimmed text starts with `[` but not `[[` is a
   table header. Its name is the text between the first `[` and the next
   `]`, with every space and tab removed (`[ review ]` is `review`). Lines
   before the first header belong to the root section, `""`. A key's
   table is `defaults`, `review`, or `review.profiles.<name>`, and its leaf
   is the last part of the key (`host`, `owners`, `timeout`,
   `remove_finished`, `when`, `model`).
2. **Find the key.** In its table's section (all sections with that name, in
   order), find the first line matching `^[ \t]*<leaf>[ \t]*=`. Its
   *span* starts at that line. Scan the value from after the `=`,
   tracking `"…"` (with `\` escapes) and `'…'` strings and `#` comments
   (a comment runs to the end of the line). If the value's first
   non-space characters are `"""` or `'''`, return `ErrUneditable`. If
   the value starts with `[`, the span continues over the following lines
   until the brackets balance. Otherwise it is one line. The *trailing
   comment* is the `#…` text after the value on the span's last line, if
   any.
3. **Set, key found**: replace the span's lines with one line: the first
   line's leading whitespace, `<leaf> = <encoded>`, then
   `" " + trailingComment` if there was one, then the line ending of the
   span's last line.
4. **Set, key not found, table found**: insert `<leaf> = <encoded>` plus a
   line ending right after the end of the span of the last key line in
   the table's last section. If the table has no key lines, insert it
   right after the header line.
5. **Set, table not found**: if `data` doesn't end with a line ending,
   add one. If it isn't empty, add one blank line. Then append
   `[<table>]` and the key line, each with a line ending.
6. **Unset**: delete the span's lines. If the table is
   `review.profiles.<name>` and its sections now have no key lines, also
   delete the header line, and one blank line right before it if there is
   one. Comment lines are kept.
7. **Check**: after `Set` or `Unset`, run `Settings` on the new text.
   For `Set`, `Lookup(new, key)` must give `Set: true` and the normalized
   value. For `Unset`, it must give `Set: false`. Otherwise return
   `ErrUneditable`, naming the key, and not the new text. The `changed`
   result is `!bytes.Equal(old, new)`.

**Tests (`edit_test.go`)** must cover at least:

- `TestFormatDuration`: 90m, 60m, 2h, 45s, 1m30s, 0.
- `TestSettings`: the default file gives all four fixed keys with
  `Set` matching what it sets (host and owners true, the others false),
  and owners `[]string{}`. A file with `[review.profiles.security]
  model = "opus"` adds two rows after the fixed ones. An invalid file
  returns an error that is `ErrInvalid`.
- `TestLookup`: a fixed key. A profile key without a table (`Set` false).
  Unknown keys `defaults.protocol`, `review.profiles.Bad.when` and
  `review.profiles.x.color` are `ErrKeyUnknown`.
- `TestSet`, table-driven over `(input, key, values) → want text`:
  - replace `host` in the default file, where every other line is
    byte-identical
  - replace a multi-line `owners = [\n  "a",\n  "b", # two\n]` with one line
  - keep a trailing comment: `host = "a" # mine` becomes
    `host = "b" # mine`
  - add `timeout` to an existing `[review]` after its last key
  - add `[review]` at the end of the default file
  - add `[review.profiles.security]` with `model`
  - keep CRLF line endings
  - a file with no trailing newline
  - `changed` false when setting the same value again
  - normalization: `Gitlab.Example.COM`, owners `["A", "a", "b"]`, and
    timeout `90m` written as `"1h30m"`
  - errors: `ErrValueInvalid` for `https://x`, `a b`, `-1m`, `0s`, `yes`,
    `sometimes`, a model with a space, a value with `"`, and 2 values for
    host
  - `ErrUneditable` for `[review]\nprofiles.security.when = "auto"` and
    then a set of `review.profiles.security.when`
  - `ErrUneditable` for a multi-line string value
- `TestUnset`: removes a key line, and a multi-line array. Removes an
  emptied profile table header with its blank line. Keeps a profile table
  that still has `when`. A missing key gives `changed` false. A comment
  line inside the table is kept.
- `TestWriteFile`: writes a new file. Replaces a file and keeps its mode
  (0o600 stays 0o600). Writes through a symlink, so the link is still a
  link and the target holds the new data. Leaves no temp file behind.

**Verify**

```
make fmt && make lint && go test ./internal/config/...
```

**Acceptance**

- Every case above passes, and `make lint` is clean.
- No new module in `go.mod`.

---

## T2. `ws config list/get/set/unset`, with guides

- **Id:** T2. **Depends on:** T1. **Size:** medium. **Parallel with:** T3, T4, T5.

**Read:** `docs/tools/ws-config.md` (all of it), `internal/cli/app.go`
(`newRootCmd`, `withConfigUnswept`), `internal/cli/rename.go` and
`rename_test.go` (a small people command and its tests),
`internal/cli/errors.go`, `internal/cli/output.go`, `internal/cli/llm.go`
(`guideTopics`), `internal/cli/review_start.go` (`reviewProfiles`), and
`internal/lockfile/lockfile.go` (`Exclusive`).

**Create**

- `internal/cli/config.go`
- `internal/cli/config_test.go`
- `internal/cli/guides/ws-config.md`, `ws-config-list.md`,
  `ws-config-get.md`, `ws-config-set.md`, `ws-config-unset.md`. Model
  them on `guides/ws-cache-resolve.md`: synopsis, what it does, "When to
  use it", JSON output, exit codes, and examples, all taken from
  `docs/tools/ws-config.md`.

**Change**

- `internal/cli/app.go`: add `a.newConfigCmd()` to the people commands,
  right after `a.newCacheCmd()`.
- `internal/cli/errors.go`: add `// From ADR 0054.` codes
  `codeConfigKeyUnknown = "config_key_unknown"` and
  `codeConfigValueInvalid = "config_value_invalid"` (both `exitUsage`),
  and `codeConfigUneditable = "config_uneditable"` (`exitFailed`).
- `internal/cli/llm.go`: add the topics `"config"`, `"config list"`,
  `"config get"`, `"config set"` and `"config unset"` to `guideTopics()`,
  after the cache topics.
- `internal/cli/guides/ws.md`: in the command list, after the `ws llm
  cache` block, add:
  ```
  - `ws llm config`: config.toml's settings, and its subcommands:
    - `ws llm config list`: every setting, with its value and default
    - `ws llm config get`: one setting
    - `ws llm config set`: change a setting, keeping the file's comments
    - `ws llm config unset`: remove a setting, so its default applies
  ```

**`config.go`**

```go
// newConfigCmd is ws config: list, get, set, and unset config.toml's keys (ADR 0054).
func (a *App) newConfigCmd() *cobra.Command

// configSettingJSON is ws config get's output.
type configSettingJSON struct {
	Path     string         `json:"path"`
	Setting  config.Setting `json:"setting"`
	Warnings []string       `json:"warnings"`
}

// configListJSON is ws config list's output.
type configListJSON struct {
	Path     string           `json:"path"`
	Settings []config.Setting `json:"settings"`
	Warnings []string         `json:"warnings"`
}

// configChangeJSON is ws config set's and unset's output.
type configChangeJSON struct {
	Path     string         `json:"path"`
	Setting  config.Setting `json:"setting"`
	Changed  bool           `json:"changed"`
	Warnings []string       `json:"warnings"`
}
```

- Subcommands `list` (no args), `get <key>` (exactly 1 arg), `set <key>
  <value>...` (at least 1 arg) and `unset <key>` (exactly 1 arg). Each
  `RunE` is `runE(a.withConfigUnswept(...))`. Use cobra's `Args`, so a
  wrong count is a usage error. `ws config` with no subcommand prints
  help, as `ws cache` does.
- **Value count** for `set`: `defaults.owners` takes 0 or more values,
  and every other key exactly 1. A wrong count is `usageErrorf(…)`,
  checked before reading the file. A key that doesn't parse is checked
  first, with `config.Lookup(nil, key)`, and reported as
  `config_key_unknown`.
- **Profile check** for `set` of a `review.profiles.<name>.*` key: if no
  profile from `a.reviewProfiles()` has that name, return
  `newError(codeProfileUnknown, fmt.Errorf("no review profile named %q", name))`.
  `unset` doesn't check.
- **Write path** (`set` and `unset`): take
  `lockfile.Exclusive(ctx, filepath.Join(a.paths.Root, "config.lock"))`.
  Read `a.paths.ConfigFile`, call `config.Set` or `config.Unset`, call
  `config.WriteFile` only if `changed`, then unlock. Then `config.Lookup`
  the new text for the output.
- **Error mapping**: `errors.Is(err, config.ErrKeyUnknown)` →
  `codeConfigKeyUnknown`. `ErrValueInvalid` → `codeConfigValueInvalid`.
  Check it before `ErrInvalid`, because value errors wrap both.
  `ErrUneditable` → `codeConfigUneditable`. `ErrInvalid` →
  `codeConfigInvalid`. Anything else → `codeInternal`.
- **Text output** (no `--json`):
  - `list` prints one line per setting: `<key> = <toml>`, plus
    `  # default` when `!Set`. `<toml>` is `"s"` for a string or
    duration, `true` or `false` for a bool, and `["a", "b"]` or `[]` for a
    list.
  - `get` prints the bare value: one owner per line, `true` or `false`,
    the string itself, or an empty line for `""`.
  - `set` and `unset` print the key's `list` line.
- `Warnings` is `a.warnings()`, as other commands use it, and never nil.

**Tests (`config_test.go`)**: use `newTestApp()`, then point the loader
at a temp dir: `dir := t.TempDir()`, write `dir/config.toml`, and set
`app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths,
config.Config, error) { return config.NewPaths(dir), testConfig(), nil }}`.
The CLI reads the raw file itself, so the `config.Config` returned there
doesn't matter. Table-driven where they fit:

- `list` text and `--json` on the default file, and on a file with a
  profile table.
- `get`: an owners list in text, one per line. A bool. An unknown key
  gives exit 2 and `config_key_unknown` in the `--json` error.
- `set host` rewrites the file and keeps the comments: compare the whole
  file with an expected string. `--json` has `changed: true`, and running
  it again gives `changed: false` and leaves the file's mtime unchanged.
- `set defaults.owners` with 0 values gives `owners = []`.
- `set review.timeout 1h 2h` is `usage` (exit 2). `set review.timeout
  nope` is `config_value_invalid`.
- `set review.profiles.nosuch.model opus` is `profile_unknown` (exit 2).
  `set review.profiles.security.model opus` works. `security` is a
  built-in profile.
- `unset review.timeout` after a set. `unset` of a key that isn't set has
  `changed: false`.
- A dotted-key file gives `config_uneditable` (exit 1), and the file is
  unchanged.

**Verify**

```
make fmt && make lint && make test && go build -o /tmp/ws-t2 ./cmd/ws && \
  d=$(mktemp -d) && WS_CONFIG_DIR=$d /tmp/ws-t2 config list && \
  WS_CONFIG_DIR=$d /tmp/ws-t2 config set defaults.owners Me acme && \
  WS_CONFIG_DIR=$d /tmp/ws-t2 config --json get defaults.owners && \
  WS_CONFIG_DIR=$d /tmp/ws-t2 config unset defaults.owners && cat $d/config.toml
```

**Acceptance**

- The smoke test prints `defaults.owners = ["me", "acme"]` after `set`.
  The final `cat` shows the generated file's comments intact, with
  `owners` gone.
- `TestGuideTopics` passes with the five new guides.
- No existing JSON, error code or exit code changes.

---

## T3. Tauri and online: `config_*`, `cache_search`, `settings_environment`, and the mock

- **Id:** T3. **Depends on:** nothing (the contract is in this file). **Size:** medium. **Parallel with:** T1, T2, T4.

**Read:** `desktop/src-tauri/src/cache.rs` (commands and fake-`ws`
tests), `desktop/src-tauri/src/about.rs`,
`desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/src/online/dispatch.rs`
(`COMMANDS`, `dispatch`, `Ctx`, tests),
`desktop/src-tauri/src/online/server.rs` (where `Ctx` is built),
`desktop/src-tauri/src/online/config.rs` (`Config`, `Password`),
`desktop/src-tauri/src/reviews/store.rs` (`Store`),
`desktop/src-tauri/src/runner.rs` (`ws_json`, `path`),
`desktop/test/tauri-mock.js` and `desktop/test/web-mock.js`.

**Create**

- `desktop/src-tauri/src/ws_config.rs`

**Change**

- `desktop/src-tauri/src/lib.rs`: add `mod ws_config;` and register
  `ws_config::config_list`, `ws_config::config_set`,
  `ws_config::config_unset`, `cache::cache_search` and
  `about::settings_environment` in `generate_handler!`, right after
  `open::settings_set_open`, in that order.
- `desktop/src-tauri/src/cache.rs`: `cache_search`.
- `desktop/src-tauri/src/about.rs`: `Environment`, `OnlineSettings`,
  `environment()` and `settings_environment`.
- `desktop/src-tauri/src/reviews/store.rs`: `pub fn path(&self) ->
  Option<&Path>` on `Store`.
- `desktop/src-tauri/src/online/dispatch.rs`: the five names in
  `COMMANDS`, in the same position as in `lib.rs`. Add their match arms,
  and `pub online: Option<about::OnlineSettings>` on `Ctx`. Set it to
  `None` in the test `ctx_with`.
- `desktop/src-tauri/src/online/config.rs`: `pub fn summary(&self) ->
  crate::about::OnlineSettings` on `Config`.
- `desktop/src-tauri/src/online/server.rs`: build `Ctx` with
  `online: Some(config.summary())`.
- `desktop/test/tauri-mock.js` and `desktop/test/web-mock.js`: see
  below.

**`ws_config.rs`**

```rust
//! config.toml's settings through `ws config` (ADR 0054). The app never
//! reads or writes config.toml itself.

/// One key, as `ws config --json` prints it. `value` and `default` are a
/// string, a list of strings, or a bool.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigSetting {
    pub key: String,
    #[serde(rename = "type")]
    pub kind: String,
    pub value: serde_json::Value,
    pub default: serde_json::Value,
    pub set: bool,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigListing { pub path: String, pub settings: Vec<ConfigSetting> }

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigChange { pub path: String, pub setting: ConfigSetting, pub changed: bool }

/// Every key of config.toml (`ws config list`).
pub async fn config_list(state: State<'_, AppState>) -> CmdResult<ConfigListing>;
/// Sets a key (`ws config set -- <key> <values...>`).
pub async fn config_set(state: State<'_, AppState>, key: String, values: Vec<String>) -> CmdResult<ConfigChange>;
/// Removes a key (`ws config unset -- <key>`).
pub async fn config_unset(state: State<'_, AppState>, key: String) -> CmdResult<ConfigChange>;
```

Each delegates to a private `async fn …(runner: &Runner, …)`, as
`cache.rs` does, and calls `runner.ws_json(&["config", "list"])`,
`["config", "set", "--", key, values...]` or `["config", "unset", "--",
key]`. `ws_json` inserts `--json`. All three structs use the field names
above, which are already camelCase, so no rename attribute is needed
except `type`.

**`cache.rs`**

```rust
/// A `ws cache search` match: a repo, or a branch or tag of one.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct SearchMatch { pub id: String, #[serde(rename = "ref")] pub reference: Option<String> }

/// Searches cached repos (and refs with `refs`) for Settings › Repos › Try a spec.
pub async fn cache_search(state: State<'_, AppState>, query: String, refs: bool, limit: u32) -> CmdResult<Vec<SearchMatch>>;
```

It runs `["cache", "search", "--limit=<limit>", ("--refs")?, "--", query]`
and returns `.matches`. A query that is blank after `trim()` returns
`Ok(vec![])` without running `ws`.

**`about.rs`**

```rust
/// Workspace Online's settings from WS_ONLINE_*, without the password.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OnlineSettings {
    pub username: String, pub addr: String, pub public_url: Option<String>,
    pub secure_cookie: bool, pub session_days: u32, pub trust_proxy: bool,
    pub data_dir: String, pub web_dir: String, pub vapid_subject: String,
    /// "plain" or "hash".
    pub password: &'static str,
}

/// Where the app's and ws's files are, and the environment it runs with.
#[derive(Debug, Clone, Serialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct Environment {
    pub state_file: Option<String>, pub ws_config_dir: Option<String>,
    pub claude_config_dir: Option<String>, pub path: String,
    pub online: Option<OnlineSettings>,
}

/// The environment for Settings › About, with `online` as given.
pub fn environment(state: &AppState, online: Option<OnlineSettings>) -> Environment;

/// Settings › About › Locations in the desktop app (`online` is null).
#[cfg_attr(feature = "desktop", tauri::command)]
pub fn settings_environment(state: State<'_, AppState>) -> Environment;
```

`environment` reads `WS_CONFIG_DIR` and `CLAUDE_CONFIG_DIR` with
`std::env::var`, treats an empty value as `None`, and takes
`state.store.path()` and `state.runner.path()`. Put the env lookup behind
a small `fn env_dir(get: impl Fn(&str) -> Option<String>, name: &str) ->
Option<String>`, so it can be tested without touching the process
environment. `Config::summary` fills `OnlineSettings` from its fields:
`addr.to_string()`, `public_origin.clone()`, paths with `.display()`,
and `password` = `"plain"` or `"hash"` from the `Password` variant.

**Dispatch arms**

```rust
"config_list" => result(ws_config::config_list(State::new(&ctx.state)).await),
"config_set" => { let (key, values) = args!(args, { key: String, values: Vec<String> });
                  result(ws_config::config_set(State::new(&ctx.state), key, values).await) }
"config_unset" => { let key = args!(args, { key: String });
                    result(ws_config::config_unset(State::new(&ctx.state), key).await) }
"cache_search" => { let (query, refs, limit) = args!(args, { query: String, refs: bool, limit: u32 });
                    result(cache::cache_search(State::new(&ctx.state), query, refs, limit).await) }
"settings_environment" => json(about::environment(&ctx.state, ctx.online.clone())),
```

**Rust tests**

- `ws_config.rs`: with a fake `ws` that records its arguments (copy the
  helper in `cache.rs`), `config_set` passes `config --json set --
  defaults.owners a b`. `config_unset` passes `config --json unset --
  review.timeout`. `config_list` parses a sample with all four `type`s.
  A failing `ws` that prints `{"error":{"code":"config_value_invalid",…}}`
  gives that `AppError` code.
- `cache.rs`: `cache_search` passes `--limit=8 --refs -- bear`, and
  parses `{"matches":[{"id":"x","ref":null},{"id":"x","ref":"main"}]}`. A
  blank query returns `[]` and doesn't run `ws` (no args file written).
- `about.rs`: `env_dir` returns `None` for unset and empty values.
  `Environment` serializes as camelCase (`stateFile`, `wsConfigDir`).
- `online/config.rs`: `summary()` of a hash-configured `Config` has
  `password == "hash"`, and the JSON contains no `WS_ONLINE_PASSWORD`
  value.
- `online/dispatch.rs`: the existing `commands_match_generate_handler`
  and `every_command_is_handled` pass. Add
  `settings_environment_includes_online`: a `Ctx` with `online: Some(…)`
  returns it.

**Mock (`desktop/test/tauri-mock.js`)**

- `data.config`: a `ConfigListing` with path
  `/Users/me/Library/Application Support/workspaces/config.toml` and
  these settings: `defaults.host` `"github.com"` (set),
  `defaults.owners` `["bobcob7", "acme"]` (set), `review.timeout` `"1h"`
  (not set), `review.remove_finished` `true` (not set), and
  `review.profiles.security.when` `""` (not set) and `.model` `"opus"`
  (set).
- `config_list` returns `data.config`. `config_set({key, values})`:
  - throws `{code: "config_key_unknown", message: "unknown config key "+key}`
    for a key that isn't one of the six shapes
  - throws `{code: "config_value_invalid", message: …}` for a host
    containing `/`, `:`, `@` or a space
  - otherwise lower-cases host and owners, formats `90m` as `"1h30m"`
    (only that one case is needed), maps `"true"`/`"false"` to booleans,
    and upserts the setting with `set: true`. Profile keys append both
    rows if they're missing. Returns `{path, setting, changed: true}`.
- `config_unset({key})` sets `set: false` and `value` to `default`, and
  returns `{path, setting, changed}`.
- `cache_search({query, refs, limit})` filters the mock's cached repo ids
  (`buffBear`, `api`, `workspaces`, plus `github.com/acme/api`) by a
  case-insensitive substring. With `refs`, it adds `{id: buffBear, ref:
  "feat/rate-limit"}` when it matches. It returns at most `limit`
  matches.
- `cache_resolve({spec})`: if the mock already has it, keep it. Make
  `"api"` throw `{code: "repo_ambiguous", message: "api matches 2 cached
  repos", details: {candidates: ["github.com/bobcob7/api",
  "github.com/acme/api"]}}`. Make `"buff-bear"` resolve with `source:
  "cache"` and `cached: true`.
- `settings_environment` returns `{stateFile: "/Users/me/Library/Application Support/com.bobcob7.workspaces/desktop.json",
  wsConfigDir: null, claudeConfigDir: null, path: "/opt/homebrew/bin:/usr/bin:/bin", online: null}`.
- `desktop/test/web-mock.js`: in `invokeRoute`, when `command ===
  "settings_environment"`, return the value with `online` set to
  `{username: "me", addr: "0.0.0.0:8080", publicUrl: "https://ws.example.com",
  secureCookie: true, sessionDays: 30, trustProxy: true, dataDir: "/data/online",
  webDir: "/app/dist", vapidSubject: "mailto:me@example.com", password: "hash"}`.

**Verify**

```
make desktop-test
node --check desktop/test/tauri-mock.js && node --check desktop/test/web-mock.js
```

**Acceptance**

- Both cargo builds and their clippy runs are clean, and every test
  passes.
- `COMMANDS` matches `generate_handler!`, and every new command is
  handled.
- No change under `desktop/src/`.

---

## T4. Desktop data layer, and the Repos settings group

- **Id:** T4. **Depends on:** nothing (the contract is in this file). **Size:** large. **Parallel with:** T1, T2, T3.

**Read:** `desktop/src/api.ts` (types near `Resolution` and `Settings`,
and the `api` object), `desktop/src/features/settings/settingsSlice.ts`,
`SettingsList.tsx`, `SettingsPane.tsx`, `SettingsPane.module.css`,
`Row.tsx`, `SettingsPane.test.tsx`, `desktop/src/test/fixtures.ts`
(`fakeApi`) and `desktop/src/test/render.tsx`.

**Create**

- `desktop/src/features/settings/RepoGroup.tsx`
- `desktop/src/features/settings/RepoGroup.module.css`
- `desktop/src/features/settings/RepoGroup.test.tsx`
- `desktop/src/features/settings/ConfigSource.tsx`

**Change**

- `desktop/src/api.ts`: the types and `api` methods from the contract.
- `desktop/src/test/fixtures.ts`: `fakeApi` defaults for the five new
  methods. `configList` returns `configListing()`, a new exported fixture
  with the same six settings as the mock in T3. `configSet` and
  `configUnset` echo a `ConfigChange` for the key. `cacheSearch` returns
  `[]`. `settingsEnvironment` returns an `Environment` with `online:
  null`.
- `desktop/src/features/settings/settingsSlice.ts`: new state, thunks
  and the `SettingsGroup` union from the contract.
- `desktop/src/features/settings/SettingsList.tsx`: the six groups in
  order. Remove the `isWeb()` filter.
- `desktop/src/features/settings/SettingsPane.tsx`: route
  `group === "repos"` to `<RepoGroup />`. Route `"roles"` to a
  placeholder `<section aria-label="Agent roles"><h1>Agent roles</h1></section>`,
  which T6 replaces.
- `desktop/src/features/settings/SettingsPane.test.tsx`: update the
  group list expectations.

**Slice additions**

```ts
export interface ResolveState {
  readonly spec: string;
  readonly resolution: Resolution | null;
  readonly error: AppError | null;
  readonly pending: boolean;
}

// SettingsState gains:
readonly config: ConfigListing | null;
readonly configError: AppError | null;
/** The error under each config.toml control whose last change failed, by key. */
readonly configErrors: Readonly<Record<string, string>>;
readonly resolve: ResolveState;            // initial { spec: "", resolution: null, error: null, pending: false }
readonly searchMatches: readonly SearchMatch[];
readonly environment: Environment | null;
readonly environmentError: AppError | null;

export const fetchConfig: AsyncThunk<ConfigListing, void>;                       // api.configList
export const setConfig: AsyncThunk<ConfigChange, { key: string; values: readonly string[] }>;
export const unsetConfig: AsyncThunk<ConfigChange, string>;
export const resolveSpec: AsyncThunk<Resolution, string>;                        // api.cacheResolve
export const searchSpecs: AsyncThunk<readonly SearchMatch[], string>;           // api.cacheSearch(q, true, 8)
export const fetchEnvironment: AsyncThunk<Environment, void>;
/** The listing's setting for key, or undefined. */
export const settingOf = (config: ConfigListing | null, key: string): ConfigSetting | undefined;
```

Write each thunk the way `createAppThunk` is used in the file, with
`rejectWithValue(toAppError(err))`. The reducers:

- `setConfig` and `unsetConfig` fulfilled: replace the setting with the
  same key in `config.settings`, or append it. Set `config.path`, and
  clear `configErrors[key]`.
- rejected: set `configErrors[key] = error.message`. Use
  `action.meta.arg.key` for `setConfig`, and `action.meta.arg` for
  `unsetConfig`.
- `resolveSpec` pending: `resolve = {spec: arg, resolution: null, error:
  null, pending: true}`. Fulfilled and rejected fill in `resolution` or
  `error`. Ignore a result whose `meta.arg` isn't the current
  `resolve.spec`, because it's stale.

**`ConfigSource.tsx`**: `export const ConfigSource = ({ setting, path
}: { readonly setting: ConfigSetting | undefined; readonly path: string
}): JSX.Element`. It renders a caption, "from config.toml", with
`title={path}`. When `setting?.set === false`, it adds " · default".
Rows for `config.toml` keys put it in `help`, after their help text. T5
uses it too.

**`RepoGroup.tsx`**: `<section aria-label="Repos">` with a `<h1>` of
"Repos". It dispatches `fetchConfig()` on mount if `config === null`. If
`configError` is set, it shows the message in an `Alert` and no controls.
Its rows:

1. **Default host** (`Row`, label "Default host"): a `TextField`
   (`aria-label` "Default host"), drafted with the same `useDraft`
   pattern as `SettingsPane`. On blur, a trimmed value that differs
   dispatches `setConfig({key: "defaults.host", values: [v]})`, and a
   rejected save puts the saved value back. Help: "For specs without one,
   such as owner/name." plus `<ConfigSource>`. A **Reset** button
   (`aria-label` "Reset default host") shows when `set` is true, and
   dispatches `unsetConfig("defaults.host")`. The error comes from
   `configErrors["defaults.host"]`.
2. **Default owners** (`Row wide`, label "Default owners"): an ordered
   list, one item per owner (`role="list"`, items `role="listitem"`).
   Each item has the owner in monospace and `IconButton`s labelled
   `Move <owner> up`, `Move <owner> down` and `Remove <owner>`. The first
   item has no Up button and the last no Down button. Below the list is a
   `TextField` (`aria-label` "Add owner") with an **Add** button. Enter
   adds too. A blank or duplicate entry does nothing. Every change
   dispatches `setConfig({key: "defaults.owners", values: next})` with
   the whole new list. Help: "Tried in order for a bare repo name that
   isn't cached." plus `<ConfigSource>`.
3. **Try a spec** (`Row wide`, label "Try a spec"): a `TextField`
   (`aria-label` "Repo spec", placeholder `owner/name, name@branch or a URL`)
   and a **Resolve** button. Enter resolves as well.
   - While typing, debounce 250 ms (`setTimeout` in a `useEffect`), then
     dispatch `searchSpecs(text)` when `text.trim().length >= 2`. Show up
     to 8 matches as a list (`aria-label` "Cached matches"). Each match is
     a button showing `id` or `id@ref`, and clicking it puts that text in
     the field and resolves it.
   - The result (`role="status"`, `aria-label` "Resolution") shows, as
     label/value pairs: Repo (`id`), Ref (`ref ?? "default branch"`),
     Upstream, Cached (Yes/No), and **Matched by**, a sentence for
     `source`:
     - `spec`: "The spec names its host."
     - `cache`: "Found by name in the cache."
     - `default`: "Not cached: taken to be on the default host."
     - `probe`: "Found upstream under a default owner."
   - On `repo_ambiguous`, show "Matches more than one cached repo:" and
     each `details.candidates` entry as a button that resolves it. Any
     other error shows its message in an `Alert severity="warning"`.
   - Help: "Shows what a spec resolves to with the settings above,
     without cloning. Uses ws cache resolve and ws cache search."

**Tests (`RepoGroup.test.tsx`)**: use `renderWithStore` with
`fakeApi(...)` overrides, and the state's group set to `"repos"`.

- It renders the host and both owners from `configListing()`.
- Changing the host and blurring calls `configSet("defaults.host",
  ["gitlab.example.com"])`.
- A rejected save shows the error under the control and puts the old
  value back.
- Reset calls `configUnset("defaults.host")`.
- Moving `acme` up calls `configSet("defaults.owners", ["acme",
  "bobcob7"])`. Remove calls it without that owner. Add with Enter
  appends.
- Resolve shows "Found by name in the cache." for a `cache` resolution.
- `repo_ambiguous` lists both candidates, and clicking one calls
  `cacheResolve` with it.
- Typing `bea` calls `cacheSearch("bea", true, 8)` after the debounce.
  Use `vi.useFakeTimers({ shouldAdvanceTime: true })`.
- In `SettingsPane.test.tsx`: the list shows the six group names in
  order. With `isWeb` mocked to true, Workspaces is still listed.

**Verify**

```
cd desktop && npm ci && npm run typecheck && npx vitest run src/features/settings
make desktop-test
```

**Acceptance**

- Typecheck is clean, and every test passes, old and new.
- No `any`, and no new npm dependency.
- The Repos group works in web mode: there is no `isWeb()` branch in
  `RepoGroup.tsx`.

---

## T5. Desktop Reviews, Workspaces and About groups

- **Id:** T5. **Depends on:** T4. **Size:** medium. **Parallel with:** T2.

**Read:** `desktop/src/features/settings/SettingsPane.tsx`,
`ReviewerProfiles.tsx`, `ReviewerProfiles.test.tsx`, `AboutGroup.tsx`,
`AboutGroup.test.tsx`, `settingsSlice.ts` (as T4 left it),
`ConfigSource.tsx`, and `desktop/src/test/fixtures.ts`.

**Change**

- `desktop/src/features/settings/SettingsPane.tsx`: `ReviewsGroup` and
  `WorkspacesGroup`.
- `desktop/src/features/settings/ReviewerProfiles.tsx` and its test.
- `desktop/src/features/settings/AboutGroup.tsx` and its test.
- `desktop/src/features/settings/settingsSlice.ts`: the
  `moveProfileOverrides` thunk.
- `desktop/src/features/settings/SettingsPane.test.tsx`.

**Reviews group**: `ReviewsGroup` dispatches `fetchConfig()` on mount
when `config === null`. Add these rows right after "Check for pull
requests":

1. **Wait for a review** (`review.timeout`): a select (`aria-label`
   "Review wait timeout") with the options `15m`, `30m`, `1h`, `1h30m`,
   `2h` and `4h`, plus the current value if it isn't one of them. Their
   labels are "15 minutes", "30 minutes", "1 hour", "1½ hours", "2
   hours" and "4 hours", and the raw value for anything else. Choosing
   one dispatches `setConfig({key: "review.timeout", values: [v]})`.
   Help: "How long ws review wait waits without --timeout." plus
   `<ConfigSource>`, and a Reset button when it's set.
2. **Clean up finished reviews** (`review.remove_finished`): a checkbox
   labelled "Remove a review's agent and workspace once every reviewer has
   reported". It dispatches `setConfig({key: "review.remove_finished",
   values: [String(checked)]})`. Help: "The findings are kept." plus
   `<ConfigSource>`.

**Reviewer profiles** now writes `config.toml`. The new props are:

```ts
interface ReviewerProfilesProps {
  readonly profiles: readonly ListedProfile[] | null;
  readonly error: string | null;
  /** config.toml's review.profiles.* settings, to know which rows are set. */
  readonly config: ConfigListing | null;
  /** Called with the row's new Runs or Model. */
  readonly onRowChange: (profile: ListedProfile, change: { readonly runs?: Runs; readonly model?: string }) => void;
  /** Called by a row's Reset: unset both keys. */
  readonly onRowReset: (profile: ListedProfile) => void;
}
```

- A row shows `profile.when` and `profile.model` as `ws review profiles`
  lists them, since they already include `config.toml`. Delete `rowOf`
  and `withRow`, and their tests.
- `ReviewsGroup` implements `onRowChange` as follows:
  - `runs` dispatches `setConfig({key:
    "review.profiles.<name>.when", values: [runs]})`.
  - `model` of `""` dispatches `unsetConfig("review.profiles.<name>.model")`.
  - any other model dispatches `setConfig({key: "…model", values: [model]})`.
  - When the save is fulfilled, dispatch `fetchProfiles()`.
- A row whose `review.profiles.<name>.when` or `.model` setting has
  `set: true` shows a **Reset** button (`aria-label` "Reset <name>").
  `onRowReset` unsets the keys that are set, then dispatches
  `fetchProfiles()`.
- The old "reset all" button now unsets every set `review.profiles.*`
  key. It is disabled when none is set.
- Row errors come from `configErrors` for the row's keys.
- **Legacy overrides banner**: when `settings.reviews.profiles` has any
  entries, show above the table `<Alert severity="info">`: "This app
  still overrides N profile(s) for the reviews it starts. Move them to
  config.toml so the command line uses them too." The Alert has a **Move
  to ws config** button that dispatches `moveProfileOverrides()`.

```ts
/** Writes each desktop.json profile override with ws config, then clears them (ADR 0054). */
export const moveProfileOverrides: AsyncThunk<Settings, void>;
```

For each `[name, {runs, model}]`, it calls `api.configSet(<when key>,
[runs])`, then `api.configSet(<model key>, [model])` when `model !== ""`,
or `api.configUnset(<model key>)` when it is. Then it calls
`api.settingsSetReviews({...reviews, profiles: {}})`, and finally
dispatches `fetchConfig()` and `fetchProfiles()`. It stops at the first
error and rejects with it, and the Alert shows that message. On success,
the `Settings` it returns replaces `state.settings`.

The Review agent, Auto-review only and At most rows don't change.

**Workspaces group**: listed in web mode (T4 did the list). In
`WorkspacesGroup`, when `isWeb()`, add a first line under the title: `<p
className={styles.help}>The web app has no Open buttons. These commands
are kept on the server for parity with the desktop app.</p>`. The
controls don't change.

**About group**: after the version rows, dispatch `fetchEnvironment()`
and `fetchConfig()` on mount, and add:

- **Locations** (`<h2>` "Locations"), one read-only row each, with the
  value in monospace and a **Copy** button (`aria-label` "Copy <label>")
  that uses `navigator.clipboard.writeText`. The rows:
  - "ws config file" (`config.path`; help "Change it with Settings or ws
    config set")
  - "App settings file" (`environment.stateFile ?? "Kept in memory
    only"`)
  - "WS_CONFIG_DIR" (`wsConfigDir ?? "not set (the platform default)"`)
  - "CLAUDE_CONFIG_DIR" (`claudeConfigDir ?? "not set (~/.claude)"`)
  - "PATH" (`path`; help "The PATH the app runs ws and claude with")
  - The help for the env rows: "Set in the environment the app starts
    with; restart the app to change it."
- **Server** (`<h2>` "Server"), only when `environment.online !== null`
  (data, not `isWeb()`). It lists Username, Address, Public URL, Secure
  cookie (Yes/No), Session length ("30 days"), Trust proxy, Data
  directory, Web directory, Push contact (`vapidSubject`) and Password
  ("Set as a hash" or "Set in plain text"), each read-only. The help
  line: "From WS_ONLINE_* variables; change them and restart the
  container."

**Tests**

- Reviews: choosing "2 hours" calls `configSet("review.timeout",
  ["2h"])`. Unchecking clean-up calls `configSet("review.remove_finished",
  ["false"])`.
- Reviewer profiles: changing a row's Runs calls `configSet` with the
  `when` key and then `reviewsProfiles` again. Model "Default" calls
  `configUnset`. Reset appears only for `security`, whose `model` is
  set in `configListing()`, and unsets it.
- Banner: with `settings.reviews.profiles = { go: { runs: "off", model:
  "" } }`, clicking Move calls `configSet("review.profiles.go.when",
  ["off"])`, then `configUnset("review.profiles.go.model")`, then
  `settingsSetReviews` with `profiles: {}`. The banner goes away.
- Workspaces: with `isWeb` mocked to true, the note and both fields are
  shown.
- About: Locations shows the config path from `configListing()`. Server
  is absent when `online` is null, and shows the username and "Set as a
  hash" when it's set.

**Verify**

```
cd desktop && npm run typecheck && npx vitest run src/features/settings
make desktop-test
```

**Acceptance**

- Every test passes. Nothing writes `settings.reviews.profiles` except
  `moveProfileOverrides`, which clears it.
- The Rust backend is unchanged: legacy overrides still apply until
  they're moved.

---

## T6. Agent roles: Tauri commands, mock, and the settings group

- **Id:** T6. **Depends on:** T3, T5. **Size:** large. **Parallel with:** none.

**Read:** `docs/tools/ws-machine.md` ("ws machine roles" to the end of
`rm`), `desktop/src-tauri/src/reviews/profiles.rs` (the pattern to copy:
list, show, add/edit with stdin, rm),
`desktop/src-tauri/src/reviews/mod.rs` (the `reviews_profile_*`
commands), `desktop/src/features/settings/ReviewerProfiles.tsx`,
`ProfileEditor.tsx`, `RemoveProfileDialog.tsx` and their tests (the UI
pattern to copy), `desktop/src-tauri/src/lib.rs`,
`desktop/src-tauri/src/online/dispatch.rs` and `desktop/test/tauri-mock.js`.

**Create**

- `desktop/src-tauri/src/roles.rs`
- `desktop/src/features/settings/RolesGroup.tsx`, `RolesGroup.module.css`
  and `RolesGroup.test.tsx`
- `desktop/src/features/settings/RoleEditor.tsx` and `RoleEditor.test.tsx`

**Change**

- `desktop/src-tauri/src/lib.rs`: `mod roles;`, and register
  `roles::roles_list`, `roles_show`, `roles_add`, `roles_edit` and
  `roles_remove` after `about::settings_environment`.
- `desktop/src-tauri/src/online/dispatch.rs`: the same five names in
  `COMMANDS`, and their arms.
- `desktop/test/tauri-mock.js`: the role commands.
- `desktop/src/api.ts`, `desktop/src/test/fixtures.ts`,
  `settingsSlice.ts` and `SettingsPane.tsx` (replace T4's placeholder).

**Rust (`roles.rs`)**

```rust
/// A role as `ws machine roles --json` lists it (ADR 0050).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all(serialize = "camelCase"))]
pub struct ListedRole {
    pub name: String, #[serde(default)] pub description: String, #[serde(default)] pub model: String,
    #[serde(default)] pub pass: Vec<String>, #[serde(default)] pub fail: Vec<String>,
    #[serde(default)] pub vars: Vec<String>, #[serde(default)] pub claude_args: Vec<String>,
    pub source: String, // "builtin" | "user" | "override"
}
/// `show`: the role, its prompt template, and its file (null for a built-in).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RoleDetail { #[serde(flatten)] pub role: ListedRole, #[serde(default)] pub prompt: String, #[serde(default)] pub path: Option<String> }
/// What the editor saves; every field is sent. `prompt` goes on stdin.
#[derive(Debug, Clone, Default, PartialEq, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RoleDraft { pub description: String, pub model: String, pub pass: Vec<String>, pub fail: Vec<String>,
                       pub vars: Vec<String>, pub claude_args: Vec<String>, pub prompt: String }
/// `rm`: the file removed, and whether a built-in is back.
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct RoleRemoved { pub name: String, #[serde(default)] pub path: String, pub restored: bool }

pub async fn roles_list(state: State<'_, AppState>) -> CmdResult<Vec<ListedRole>>;          // ws machine roles --json
pub async fn roles_show(state: State<'_, AppState>, name: String) -> CmdResult<RoleDetail>;   // … show --json -- <name>
pub async fn roles_add(state: State<'_, AppState>, name: String, draft: RoleDraft) -> CmdResult<RoleDetail>;
pub async fn roles_edit(state: State<'_, AppState>, name: String, draft: RoleDraft) -> CmdResult<RoleDetail>;
pub async fn roles_remove(state: State<'_, AppState>, name: String) -> CmdResult<RoleRemoved>;
```

The arguments for `add` and `edit` are `["machine", "roles", op,
"--json", "--description", d, "--model", m, ...flags, "--", name, "-"]`,
with `draft.prompt` on stdin through `ws_json_with_input`. The flags are
built like this:

- one `--pass V` per pass verdict, and one `--fail V` per fail verdict
- for vars: one `--vars K` each, or `--no-vars` on `edit` when the list
  is empty
- for claude args: one `--claude-arg A` each, or `--no-claude-args` on
  `edit` when the list is empty

`add` with empty lists passes no flag for them. Tests, with a fake `ws`
that records its args and stdin as in `profiles.rs`:

- `add` passes every flag, and the prompt on stdin.
- `edit` with empty vars passes `--no-vars`.
- `roles_list` parses the sample in `ws-machine.md` and serializes
  `claudeArgs`.
- `rm` parses `restored`.

Dispatch arms: `roles_show` and `roles_remove` take `{ name }`.
`roles_add` and `roles_edit` take `{ name, draft: roles::RoleDraft }`.

**TypeScript.** Add these to `api.ts`:

```ts
export type RoleSource = "builtin" | "user" | "override";
export interface ListedRole { readonly name: string; readonly description: string; readonly model: string;
  readonly pass: readonly string[]; readonly fail: readonly string[]; readonly vars: readonly string[];
  readonly claudeArgs: readonly string[]; readonly source: RoleSource; }
export interface RoleDetail extends ListedRole { readonly prompt: string; readonly path: string | null; }
export interface RoleDraft { readonly description: string; readonly model: string; readonly pass: readonly string[];
  readonly fail: readonly string[]; readonly vars: readonly string[]; readonly claudeArgs: readonly string[]; readonly prompt: string; }
export interface RoleRemoved { readonly name: string; readonly path: string; readonly restored: boolean; }
// api:
rolesList: async (): Promise<readonly ListedRole[]> => call<ListedRole[]>("roles_list"),
rolesShow: async (name: string): Promise<RoleDetail> => call<RoleDetail>("roles_show", { name }),
rolesAdd: async (name: string, draft: RoleDraft): Promise<RoleDetail> => call<RoleDetail>("roles_add", { name, draft }),
rolesEdit: async (name: string, draft: RoleDraft): Promise<RoleDetail> => call<RoleDetail>("roles_edit", { name, draft }),
rolesRemove: async (name: string): Promise<RoleRemoved> => call<RoleRemoved>("roles_remove", { name }),
```

Slice: `roles: readonly ListedRole[] | null`, `rolesError: AppError |
null`, and the thunks `fetchRoles`, `fetchRoleDetail(name)`,
`saveRole({ name, draft, isNew })` and `removeRole(name)`. Both
`saveRole` and `removeRole` dispatch `fetchRoles()` when they succeed.
Add `fakeApi` defaults that return the three built-ins.

**`RolesGroup`** (`<section aria-label="Agent roles">`, `<h1>` "Agent
roles"): help: "What ws machine spawn --role starts a child as. Stored
in machine/roles/ under the ws config directory." Below it is a table
(`aria-label` "Roles"), with one row per role:

- name, with a badge: none for builtin, "custom" for user, "edited" for
  override
- description
- model (or "Default")
- pass/fail verdicts as small chips
- **Edit** (`aria-label` "Edit <name>"), which opens `RoleEditor`
- **Remove** (`aria-label` "Remove <name>"), for `user` and `override`
  only. It asks for confirmation in a dialog modeled on
  `RemoveProfileDialog`. For an override the text says "The built-in
  comes back".

A **New role** button opens `RoleEditor` empty.

**`RoleEditor`**: a MUI `Dialog`, `fullScreen` below 720px (use
`useMediaQuery("(max-width: 720px)")`, as `ProfileEditor` does if it
does). Fields:

- Name: only for new roles. Check `^[a-z0-9][a-z0-9-]{0,63}$` on the
  client.
- Description: required, at most 120 characters, with a counter.
- Model: the same `models` select as the profiles, plus "Type a model
  id".
- Pass verdicts and Fail verdicts: comma-separated text fields,
  upper-cased on blur.
- Vars: comma-separated.
- Claude arguments: one per line in a multiline field.
- Prompt template: a multiline monospace field, at least 12 rows.

Save calls `saveRole`. A `role_invalid`, `role_exists` or
`config_invalid` error shows its message in an `Alert` inside the dialog,
and the dialog stays open. Editing a builtin shows the note: "Saving
writes your own copy; Remove brings the built-in back."

**Mock (`tauri-mock.js`)**: `data.roles` holds the three built-ins from
`ws-machine.md` (`implementer`, `reviewer`, `verifier`). Make `reviewer`
an `override` with model `"opus"` and path
`/Users/me/Library/Application Support/workspaces/machine/roles/reviewer.md`.
Give each a short prompt. Handle `roles_list`, `roles_show`, `roles_add`
(throws `role_exists` for a taken name), `roles_edit` (a builtin becomes
`override`) and `roles_remove` (an override goes back to builtin with
`restored: true`, and a user role is deleted).

**Tests**

- `RolesGroup`: lists three roles, and the reviewer row shows "edited"
  and "opus". Remove on reviewer confirms, then calls `rolesRemove`.
  Built-ins have no Remove.
- `RoleEditor`: New with the name `docs-writer`, a description and a
  prompt calls `rolesAdd("docs-writer", {…, pass: ["PASS"], fail:
  ["FAIL"]})`. Editing verifier's vars to empty sends `vars: []`. A
  `role_invalid` error keeps the dialog open and shows the message.

**Verify**

```
make desktop-test
node --check desktop/test/tauri-mock.js
```

**Acceptance**

- Every Rust and TS test passes, in both cargo builds.
- `COMMANDS` matches `generate_handler!`.
- The Agent roles group works in web mode, with no `isWeb()` branch.

---

## T7. Phone and desktop layouts ready for screenshots

- **Id:** T7. **Depends on:** T6. **Size:** small. **Parallel with:** none.

**Read:** `desktop/src/features/settings/*.module.css`,
`desktop/src/App.module.css` (the 720px breakpoint), `RepoGroup.tsx`,
`RolesGroup.tsx`, `AboutGroup.tsx`, the handbook lesson "phone UI needs a
screenshot pass for tap targets" (summarized below), and
`docs/design/settings-all/README.md#screenshots`.

**Change** (CSS and markup only; no behavior change)

- `desktop/src/features/settings/SettingsPane.module.css`,
  `RepoGroup.module.css` and `RolesGroup.module.css`
- `desktop/src/features/settings/RepoGroup.tsx`, `RolesGroup.tsx` and
  `AboutGroup.tsx`: class names only
- `desktop/README.md`: in the Settings rows, add one line each for Repos,
  Agent roles and About › Locations, and reference the screenshot files
  from the design's table. The orchestrator commits the PNGs.

**Layout rules**

- **Desktop (≥ 721px)**: rows keep the 260px label column. The owners
  list, the Try a spec result and the roles table are at most 820px wide
  (`.group`). The Resolution shows its pairs in a two-column grid
  (`grid-template-columns: max-content 1fr`).
- **Phone (≤ 720px)**:
  - Every `.row` stacks: `flex-direction: column`, with the label column
    `width: auto`.
  - Text fields are `width: 100%`.
  - The Resolution grid becomes one column.
  - The roles table turns into cards: each row is a block with its name
    and badges on the first line, the description wrapping below, and
    the chips and buttons on the last line.
  - Long values (paths, upstream URLs, model ids) use `overflow-wrap:
    anywhere`, so nothing widens the page.
  - Every `IconButton` and `Button` in these groups is at least 40×40px
    on a phone (`min-width: 40px; min-height: 40px`).
- `.root` keeps `overflow-y: auto`, and `overflow-x` must stay `hidden`
  or the content must fit.

**Named layout checks.** The screenshot child measures these with
Playwright and `getBoundingClientRect`, at 390×844 (web, with
`web-mock.js`) and 1280×800 (Tauri mock):

1. `document.documentElement.scrollWidth <= innerWidth`, in every group.
2. No two controls in the same row overlap.
3. Every button and icon button in Settings is at least 40px tall and
   wide at 390px.
4. The Repos › Default host input is at least 200px wide at 390px, and
   isn't clipped.
5. Each candidate in the ambiguous list is fully visible without
   horizontal scroll.
6. The RoleEditor dialog fills the screen at 390px, and its Save button
   is visible without scrolling the page body.

**Tests**: no new unit tests. `getComputedStyle` checks belong in the
screenshot pass, not in jsdom, as the run 6 lesson says.

**Verify**

```
make desktop-test
```

Then, for the orchestrator's screenshot child:

```
cd desktop && npx vite --port 1420   # Playwright: addInitScript(test/tauri-mock.js) [+ test/web-mock.js]
```

**Acceptance**

- `make desktop-test` passes.
- All six named checks pass in the screenshot child's report.
- The nine PNGs listed in the design are produced.
