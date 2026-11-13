# Hosts in config.toml: reference

The contract for [ADR 0061](../../adrs/0061-hosts-in-config-toml.md).
[tasks-hosts.md](tasks-hosts.md) splits the work. Where this file and the
ADR disagree, the ADR wins; where this file and the code disagree, this
file wins.

## `config.toml`

```toml
[defaults]
owners = ["bobcob7"]

[[hosts]]
name = "github.com"
kind = "github"

[[hosts]]
name = "git.example.com:3000"
kind = "forgejo"
```

| Key (per `[[hosts]]` table) | Type | Default | Rule |
| --- | --- | --- | --- |
| `name` | string | — (required) | lowercased, then `repo.ValidateHost`: letters, digits, `.`, `-`, optional `:<port>` with port 1–65535 written without leading zeros |
| `kind` | string | `"auto"` | `auto`, `github` or `forgejo`, exactly (not lowercased) |

Read-time rules (`config.parse` → `normalize`), each an `ErrInvalid`
(`config_invalid`, exit 1). Messages, with `%d` the 1-based table index:

| Situation | Message after `invalid config: ` |
| --- | --- |
| `name` missing or `""` | `hosts entry %d has no name` |
| `name` fails `repo.ValidateHost` | `hosts entry %d: name %q: <ValidateHost error>` |
| `kind` not one of the three | `hosts entry %d (%s): kind must be auto, github, or forgejo, not %q` |
| a name (after lowercasing) twice | `hosts lists %q twice` |
| `defaults.host` set and `hosts` non-empty | `set defaults.host or [[hosts]], not both; move %q into [[hosts]] with ws config set hosts` (`%q` = defaults.host) |
| unknown key `hosts.token` | the usual `unknown keys hosts.token` plus `; tokens don't go in config.toml: use WS_FORGEJO_TOKEN_<HOST> or Settings › Forges` |

`hosts = []` (inline, empty) is allowed and means "not set". Any other
unknown key under `hosts` is the existing unknown-keys error.

**Effective hosts** (`Config.EffectiveHosts`): `Hosts` when it has entries;
else one entry `{Name: Defaults.Host, Kind: "auto"}`; else (empty
`Defaults.Host`, for example `config.Config{}` in tests)
`{Name: "github.com", Kind: "auto"}`. Never empty. The first is the
**primary host**.

The generated `defaultFile` becomes (it must still parse to `Default()`):

```toml
# ws configuration. Edit it by hand, or with ws config set; ws keeps your comments.

[defaults]
# Owners to try, in order, for a bare repo name that isn't cached,
# for example ["your-name", "your-org"].
owners = []

# Hosts for repo specs without one, such as "owner/name", tried in order;
# the first is the primary host. With none, github.com is used.
# kind is auto, github, or forgejo (read by the desktop app's Reviews).
# Edit with ws config set hosts github.com git.example.com=forgejo
# [[hosts]]
# name = "github.com"
# kind = "auto"

# [review]
# How long ws review wait waits for a review by default.
# timeout = "60m"
#
# Remove a review's agent and workspace once every reviewer has reported;
# its findings are kept.
# remove_finished = true
#
# Override a review profile's when (always, auto, or off), and pin its model.
# [review.profiles.security]
# when = "always"
# model = "opus"
```

## Go: `internal/config` and `internal/repo`

```go
// internal/repo/id.go
// ValidateHost checks a host as repo ids hold it: a bare host name with an
// optional ":port" (ADR 0020). It doesn't lowercase.
func ValidateHost(hostPort string) error { return validateHostPort(hostPort) }

// internal/config/config.go
// Host kinds (ADR 0061). Only the desktop acts on them.
const (
	KindAuto    = "auto"
	KindGithub  = "github"
	KindForgejo = "forgejo"
)

// Host is one [[hosts]] table (ADR 0061).
type Host struct {
	Name string `toml:"name" json:"name"`
	Kind string `toml:"kind" json:"kind"`
}

type Config struct {
	Defaults Defaults `toml:"defaults"`
	Hosts    []Host   `toml:"hosts"`
	Review   Review   `toml:"review"`
}

// EffectiveHosts is [[hosts]], or defaults.host, or github.com; never empty.
func (c Config) EffectiveHosts() []Host

// SpecDefaults is what short specs resolve against (ADRs 0007, 0061).
type SpecDefaults struct {
	Hosts  []string // effective host names, in order; never empty
	Owners []string // defaults.owners
}

// SpecDefaults returns the effective hosts' names and defaults.owners.
func (c Config) SpecDefaults() SpecDefaults

// normalizeHosts lowercases names, fills an empty kind with "auto", and
// checks the rules above. Shared by parse and by ws config set hosts.
func normalizeHosts(hosts []Host) ([]Host, error)
```

`parse` needs the `toml.MetaData` to tell "defaults.host set" from the
default: use `meta.IsDefined("defaults", "host")` and `len(cfg.Hosts) > 0`.
`normalize` keeps `Hosts` (normalized; `nil` when empty). `Default()` is
unchanged (`Defaults.Host = "github.com"`, `Hosts` nil).

## `ws config`: the `hosts` key

New `Type`: `TypeHosts Type = "hosts"`. Key order in `list`:
`defaults.host`, `defaults.owners`, `hosts`, `review.timeout`,
`review.remove_finished`, then profiles.

| Key | `value` | `default` | `set` |
| --- | --- | --- | --- |
| `hosts` | `EffectiveHosts()` as `[]config.Host` | `[{"name":"github.com","kind":"auto"}]` | `meta.IsDefined("hosts")` and the file has entries |
| `defaults.host` | `EffectiveHosts()[0].Name` (changed: the primary host) | `"github.com"` | `meta.IsDefined("defaults","host")` (unchanged) |

`ws config list --json` (with the file at the top of this page):

```json
{"path": "/…/config.toml",
 "settings": [
   {"key": "defaults.host", "type": "string", "value": "github.com", "default": "github.com", "set": false},
   {"key": "defaults.owners", "type": "strings", "value": ["bobcob7"], "default": [], "set": true},
   {"key": "hosts", "type": "hosts",
    "value": [{"name": "github.com", "kind": "github"}, {"name": "git.example.com:3000", "kind": "forgejo"}],
    "default": [{"name": "github.com", "kind": "auto"}], "set": true},
   {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
   {"key": "review.remove_finished", "type": "bool", "value": true, "default": true, "set": false}
 ],
 "warnings": []}
```

`ws config get --json hosts`:

```json
{"path": "/…/config.toml",
 "setting": {"key": "hosts", "type": "hosts",
             "value": [{"name": "github.com", "kind": "auto"}],
             "default": [{"name": "github.com", "kind": "auto"}], "set": false},
 "warnings": []}
```

Text output:

```console
$ ws config list
…
hosts = [{name = "github.com", kind = "github"}, {name = "git.example.com:3000", kind = "forgejo"}]
…
$ ws config get hosts          # one NAME=KIND per line, the form set takes
github.com=github
git.example.com:3000=forgejo
```

`configTOML` (cli) prints a `[]config.Host` as
`[{name = "<n>", kind = "<k>"}, …]` (names and kinds with `strconv.Quote`);
`printConfigValue` prints one `name=kind` per line.

### `ws config set hosts [NAME[=KIND]]...`

- Any number of values, like `defaults.owners` (`configSetArgs`).
- Each value: split at the first `=`; no `=` means kind `auto`. Then
  `normalizeHosts`. Errors are `ErrValueInvalid` (`config_value_invalid`,
  exit 2) with the same messages as at read time, `hosts entry %d` counting
  the values from 1. A value with a quote, backslash or control character
  fails `checkText` first, as today.
- Zero values: same as `ws config unset hosts`.
- The edit (`rewriteHosts`, below), then verification: `Lookup(out,
  "hosts")` must have `Set == (len(want) > 0)` and, when set, `Value`
  deep-equal to `want`; and `Lookup(out, "defaults.host").Set` must be
  false. Otherwise `ErrUneditable` (`config_uneditable`, exit 1),
  `config key can't be edited in place: hosts`.
- Output: as for any key (`set`'s JSON with `"changed"`).

`ws config unset hosts`: `rewriteHosts(lines, nil)` and the same
verification (it does **not** restore `defaults.host`; the effective list
falls back to `github.com`). Not set: `"changed": false`, file untouched.

`ws config set defaults.host X` while the file sets `hosts` with entries:
`ErrValueInvalid`, `defaults.host can't be set while [[hosts]] is; use ws
config set hosts` (exit 2). `ws config unset defaults.host` is unchanged.

### `rewriteHosts(lines []string, hosts []Host) ([]string, error)`

A hosts **header** is a line whose text, with spaces and tabs removed,
starts with `[[hosts]]` (`headerName` returns `"[hosts"` for it, so
`sectionNames` names its section `"[hosts"`).

1. If `load(data)`'s meta says `hosts` is defined but no line is a hosts
   header (an inline `hosts = [...]`), return `ErrUneditable`.
2. Mark for removal: every hosts header line; every key line in a
   `"[hosts"` section, with all lines of its value (`spanAt`); and the span
   of `defaults.host` (`findSpan` with its rule), if any. A span that
   `spanAt` can't read is `ErrUneditable`.
3. Keep every other line in order: comments, blank lines and the lines
   of other tables, including comment lines inside a hosts section.
4. Build the block, using the file's `newline`: for each host, a blank line
   between tables, then `[[hosts]]`, `name = "<name>"`, `kind = "<kind>"`
   (kind always written).
5. If there was a hosts header, insert the block where the first one was
   (the index among kept lines); if the next kept line exists and isn't
   blank, add one blank line after the block. Otherwise, when `hosts` is
   non-empty, append it at the end the way `appendTable` does (end the last
   line, one blank line, then the block). An empty `hosts` inserts nothing.

Example: `ws config set hosts github.com git.example.com=forgejo` on the
old generated file (with `host = "github.com"`) removes the `host = …` line,
keeps its comment above it, and appends at the end of the file:

```toml

[[hosts]]
name = "github.com"
kind = "auto"

[[hosts]]
name = "git.example.com"
kind = "forgejo"
```

### Errors (additions)

| Situation | Code | Exit |
| --- | --- | --- |
| bad `[[hosts]]` in the file (any read-time rule above) | `config_invalid` | 1 |
| `set hosts` value breaks a rule, or names a host twice | `config_value_invalid` | 2 |
| `set defaults.host` while `[[hosts]]` is set | `config_value_invalid` | 2 |
| `set`/`unset hosts` on an inline `hosts = [...]`, or the result doesn't verify | `config_uneditable` | 1 |

## Resolution: `internal/cache`

```go
// cache.go
type Cache struct { …; defaults config.SpecDefaults; … }
func New(log *slog.Logger, git gitRunner, root string, defaults config.SpecDefaults) *Cache
// internal/cli/app.go: cache.New(log, runner, paths.Cache, cfg.SpecDefaults())
```

`c.defaults.Hosts` is never empty in production; code that indexes it must
still treat an empty slice as `["github.com"]` (helper `func (c *Cache)
hosts() []string`).

| Spec | Cached matches | Result |
| --- | --- | --- |
| `host/owner/name`, URL | — | unchanged (`spec`) |
| `owner/name` | 1 | that repo (`cache`) |
| `owner/name` | several | keep those on the earliest host in `hosts()` that has any; still several → `repo_ambiguous` |
| `owner/name` | 0, one host | `hosts()[0]/owner/name`, no network (`default`) — unchanged |
| `owner/name` | 0, 2+ hosts, `Resolve` | `probeHosts(ctx, sp.Raw, []string{owner}, name)` |
| `owner/name` | 0, 2+ hosts, `Lookup` | `hosts()[0]/owner/name` (`default`), so `Lookup` reports not cached — unchanged |
| `name` | 1 / several | unchanged (owners preferred in order, else ambiguous) |
| `name` | 0, `Resolve` | `probeHosts(ctx, sp.Raw, defaults.Owners, name)` |
| `name` | 0, `Lookup` | unchanged: `no cached repo is named %q` |

```go
// probeHosts tries host/owner/name with git ls-remote for each host in
// order, and each owner in order within a host (ADR 0061).
func (c *Cache) probeHosts(ctx context.Context, spec string, owners []string, name string) (Resolution, error)
```

1. `c.ensureRoot()`.
2. For each host `h` (index `i`), for each owner `o`:
   `id, err := repo.NewID(h, o, name)` (an error returns at once, as
   today); run `ls-remote -q -- <id.UpstreamURL()> HEAD` in `c.root`.
   - success: `c.alsoOn(ctx, id, hosts[i+1:])`; return
     `Resolution{ID: id, Upstream: id.UpstreamURL(), Source: SourceProbe}`.
   - `isGitUnusable(err) || ctx.Err() != nil`: return `err`.
   - `repoMissing(err)`: debug log `probe missed`, next owner.
   - anything else: remember the first such `(id, err)`; `c.warn("couldn't
     reach %s while looking for %s: %v", h, spec, err)`; skip the rest of
     `h`'s owners.
3. Nothing hit:
   - a failure was remembered: `fmt.Errorf("%w: looking for %s: %w",
     ErrFetchFailed, firstID, firstErr)` (`fetch_failed`, exit 1; same text
     as today for one host).
   - bare name: `fmt.Errorf("%w: %q isn't cached, and no owner in
     defaults.owners has it on %s", ErrRepoNotFound, name, strings.Join(hosts, ", "))`
     (same text as today for one host).
   - `owner/name`: `fmt.Errorf("%w: %q isn't cached, and none of %s has it",
     ErrRepoNotFound, spec, strings.Join(hosts, ", "))`.

```go
// alsoOn probes id's owner/name on each later host and warns for each that
// has it. Failures are only logged at debug level.
func (c *Cache) alsoOn(ctx context.Context, id repo.ID, later []string)
```

Warning text: `%s/%s is also on %s; using %s. Name the host to use the
other.` with owner, name, the later host, `id`. One warning per later host
that has it.

`ws cache resolve --json` with hosts `github.com`, `git.example.com`,
owners `["bobcob7"]`, `api` uncached, on both hosts, and
`git.example.com` answering:

```json
{"repos": [{"id": "github.com/bobcob7/api", "ref": null,
            "upstream": "https://github.com/bobcob7/api.git",
            "cached": false, "source": "probe"}],
 "warnings": ["bobcob7/api is also on git.example.com; using github.com/bobcob7/api. Name the host to use the other."]}
```

With github.com unreachable and `git.example.com` having it:
`"id": "git.example.com/bobcob7/api"`, `"source": "probe"`, and
`"warnings": ["couldn't reach github.com while looking for api: <git error>"]`.

`source` values and their meaning (guide `ws-cache-resolve.md`):
`spec`; `cache`; `default` (an uncached `owner/name` when only one host is
configured, taken to be on it); `probe` (found upstream: a bare name under
a default owner, or an `owner/name` when two or more hosts are configured,
on the first host that has it).

## Desktop backend (Rust)

### `ws_config.rs`

Make `set` and `unset` `pub(crate)`, and add:

```rust
/// `ws config get` (one key).
#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
pub struct ConfigGet { pub path: String, pub setting: ConfigSetting }

/// One key (`ws config get -- <key>`).
pub(crate) async fn get(runner: &Runner, key: &str) -> CmdResult<ConfigGet>
```

Arguments: `["config", "get", "--", key]` through `runner.ws_json` (which
adds `--json`, so the fake `ws` sees `config --json get -- hosts`).

### `reviews/hosts.rs` (new)

```rust
//! config.toml's [[hosts]], read and written through `ws config` (ADR 0061).

/// One [[hosts]] entry.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ConfiguredHost { pub name: String, pub kind: ForgeChoice }

/// The effective hosts, and whether config.toml sets them.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct HostList { pub hosts: Vec<ConfiguredHost>, pub set: bool }

/// `ws config get --json hosts`.
pub async fn read(runner: &Runner) -> Result<HostList, AppError>;

/// `ws config set --json -- hosts name=kind…`; an empty list runs
/// `ws config unset --json -- hosts`. Returns the list as ws reports it.
pub async fn write(runner: &Runner, hosts: &[ConfiguredHost]) -> Result<HostList, AppError>;

/// The host's kind when listed, else Auto.
pub fn choice(hosts: &[ConfiguredHost], host: &str) -> ForgeChoice;

/// Copies desktop.json's settings.forges into config.toml once (ADR 0061).
/// Ok(true) when it moved or cleared anything.
pub async fn migrate(runner: &Runner, store: &Store) -> Result<bool, AppError>;
```

- `read`/`write` parse `setting.value` with `serde_json::from_value::<Vec<ConfiguredHost>>`;
  a value that doesn't parse is `AppError::new("config_invalid", "ws config: hosts isn't a list of hosts")`.
- `write` argument for each host: `format!("{}={}", name, kind)` with kind
  lowercase (`auto`, `github`, `forgejo`).
- `migrate`:
  1. `legacy = store.get().settings.forges.hosts`; empty → `Ok(false)`.
  2. `current = read(runner).await?`.
  3. If `!current.set`: start from `current.hosts` (the implied primary);
     for each `(name, entry)` of `legacy` in key order, lowercased: if
     already in the list, set its `kind`; else push it. `write(runner, &list).await?`.
  4. `store.update(|d| d.settings.forges = ForgeSettings::default())?`;
     `Ok(true)`.

  Running it twice, or after a crash between 3 and 4, writes nothing the
  second time.

### `reviews::store`

`ForgeHost`/`ForgeSettings` stay (read for the migration). Remove
`ForgeSettings::choice` and `set_choice`; mark the struct's doc
`/// Legacy (ADR 0060): moved to config.toml by ADR 0061; read once by hosts::migrate.`

### Checker and view

- `view::CheckState` gains `pub configured: Vec<ConfiguredHost>` (the
  effective list from the last check).
- `Checker::check_steps`, before `github::signed_in_hosts`:
  `hosts::migrate` (an error becomes the warning
  `moving forge settings to config.toml: <message>`), then `hosts::read`
  (an error becomes the warning `ws config get hosts: <message>`, and the
  list is empty). Store the list in `CheckState.configured`. Move
  `let mut warnings = Vec::new();` to the top of `check_steps` so these
  warnings are kept.
- **Known hosts** (replaces `cached_hosts` in `probe_hosts` and
  `decide_hosts`): the configured names in list order, then the cached
  hosts not configured, sorted.
- Every `settings.forges.choice(&host)` becomes
  `hosts::choice(&configured, &host)`.
- `HostView` gains `configured: bool` (listed in `CheckState.configured`)
  and `primary: bool` (it's `configured[0]`); `hosts_view` lists the known
  hosts in the order above, and takes `choice` from `CheckState.configured`.

```json
{ "host": "github.com", "forge": "github", "choice": "github", "detected": false,
  "configured": true, "primary": true,
  "login": "bobcob7", "token": null, "tokenEnv": null, "error": null }
```

### Commands

| Command | Args | Returns | Errors |
| --- | --- | --- | --- |
| `reviews_set_hosts` (new) | `{ "hosts": [{ "name": "github.com", "kind": "github" }, …] }` (`[]` unsets) | `ReviewsView` | `setting_invalid` (a name failing `forge::is_host`, or listed twice), any `ws config` error code as is (`config_value_invalid`, `config_invalid`, `config_uneditable`, `internal`) |
| `reviews_set_forge` | removed | | |
| `reviews_set_token` | unchanged | | |

```rust
/// Replaces config.toml's [[hosts]] (Settings › Forges, ADR 0061).
#[cfg_attr(feature = "desktop", tauri::command)]
pub async fn reviews_set_hosts(state: State<'_, AppState>, hosts: Vec<ConfiguredHost>) -> CmdResult<ReviewsView>
```

It checks every name, calls `hosts::write`, sets `CheckState.configured`
to the result (`Checker::set_configured(&self, hosts: Vec<ConfiguredHost>)`),
calls `forget_host` for every host in the old or new list, `check_now()`,
and returns `view(&state)`. Registered in `lib.rs` `generate_handler!` and
`online/dispatch.rs` (`COMMANDS` and the `match`:
`let hosts = args!(args, { hosts: Vec<reviews::hosts::ConfiguredHost> });`
following the old `reviews_set_forge` arm).

## Frontend (TypeScript)

```ts
// api.ts
export interface ConfiguredHost { readonly name: string; readonly kind: ForgeChoice }
export type ConfigType = "string" | "strings" | "duration" | "bool" | "hosts";
export type ConfigValue = string | boolean | readonly string[] | readonly ConfiguredHost[];
// HostView gains: readonly configured: boolean; readonly primary: boolean;
// api: reviewsSetForge removed;
//   reviewsSetHosts(hosts: readonly ConfiguredHost[]): Promise<ReviewsView> -> "reviews_set_hosts", { hosts }
```

`reviewsSlice`: `setForge`/`ForgeChange` removed;
`setHosts = createAppThunk<ReviewsView, readonly ConfiguredHost[]>("reviews/setHosts", …)`
calling `extra.api.reviewsSetHosts`, rejecting with `failed(err, dispatch)`
like `setToken`; `.addCase(setHosts.fulfilled, setView)`.

### Settings › Forges (`ForgesGroup.tsx`)

- **Hosts table** (`aria-label="Hosts"`): the `configured` hosts, in
  order. Columns: Host (name, plus the text `primary` on the first row),
  Forge (the existing select, `aria-label="Forge for <host>"`), Token (as
  today, Forgejo only), Account (as today), and order buttons:
  `Move <host> up` (not on the first), `Move <host> down` (not on the
  last), `Remove <host>`.
- **Other hosts** (`aria-label="Other hosts"`, only when there are any):
  the hosts with `configured: false` (cached repos on hosts not in the
  list), each with Host, Forge (`Auto (detected: …)` text only), Account
  and an `Add <host>` button (appends `{ name, kind: "auto" }`).
- **Add a host**: a text field `aria-label="New host"`, a select
  `aria-label="Forge for new host"` (Auto/GitHub/Forgejo, default Auto) and
  an `Add` button. The name is trimmed and lowercased; it must match
  `/^[a-z0-9.-]+(:[0-9]{1,5})?$/` (else the helper text `Not a host name`)
  and not already be configured (else `Already in the list`). Enter adds.
- Every change sends the whole new list: `setHosts(next)` where `next` is
  the configured hosts as `{ name: host.host, kind: host.choice }` with
  the change applied. On success, also `dispatch(fetchConfig())` so Repos
  shows the new list; on failure, an error `Alert` above the table with the
  message.
- Caption under the tables: `Short specs such as owner/name are tried on
  these hosts in order; the first is the primary. Saved in config.toml as
  [[hosts]]. GitHub hosts use gh's login. Forgejo tokens come from
  WS_FORGEJO_TOKEN_<HOST> or from the token saved here, which stays on this
  machine and is never shown again.`

### Settings › Repos (`RepoGroup.tsx`)

- `HostRow` (Default host) is removed.
- New `HostsRow` (label `Hosts`): the names from the `hosts` setting's
  value, in order, as a mono list (`aria-label="Hosts"`), the first marked
  `primary`, help text `Short specs are tried on these hosts in order.
  <ConfigSource …/>`, and a button `Edit in Forges` that dispatches
  `groupChosen("forges")`.
- `matchedBy.default`: `Not cached: taken to be on the only host.`;
  `matchedBy.probe`: `Found upstream on a configured host.`

### Tauri mock (`desktop/test/tauri-mock.js`)

- `data.config` gains the `hosts` setting (value
  `[{name:"github.com",kind:"auto"},{name:"git.example.com",kind:"forgejo"}]`,
  `set: true`); `defaults.host` becomes `value: "github.com", set: false`.
- `config_set` with key `hosts` parses `name=kind` values into the
  setting (`config_get` isn't a Tauri command; only the Rust side runs it).
- `hosts` view entries gain `configured`/`primary`; `reviews_set_forge` is
  replaced by `reviews_set_hosts({ hosts })`, which reorders and updates
  `configured`, `primary` and `choice` and also updates `data.config`'s
  `hosts` setting.
