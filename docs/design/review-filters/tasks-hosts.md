# Hosts in config.toml: implementation tasks

Implements [ADR 0061](../../adrs/0061-hosts-in-config-toml.md) to the
contract in [reference-hosts.md](reference-hosts.md). These follow T1–T6
of [tasks.md](tasks.md) (already merged on `feat/review-filters`), so the
ids are U1–U5. Each task builds, passes its tests, and is one commit on its
own branch (conventional commit, imperative mood, for example
`feat: add [[hosts]] to config.toml (ADR 0061)`).

**Order and parallelism**

| Task | Title | Depends on | Size | Parallel with |
| ---- | ----- | ---------- | ---- | ------------- |
| U1 | `[[hosts]]` schema, effective hosts, read side of `ws config` | nothing | medium | — |
| U2 | `ws config set`/`unset hosts`, keeping comments | U1 | medium | U3 |
| U3 | Resolve short specs across hosts | U1 | medium | U2 |
| U4 | Desktop backend: hosts from `config.toml`, migration, `reviews_set_hosts` | U2 | large | U3 (memory permitting) |
| U5 | Settings › Forges edits hosts; Repos points to it; mock | U4 | medium | — |

U2 and U3 touch disjoint files (`internal/config/edit.go` and
`internal/cli/config.go` against `internal/cache/*` and
`internal/cli/app.go`): **U2 ∥ U3**. U4 needs only U2's JSON (it tests
against a fake `ws`), so it can run beside U3; on an 8 GB host don't run a
Go and a Rust task at once (handbook lesson "background waits and children
are killed under memory pressure"). No task is `small`. Backend (Go U1–U3)
comes before the desktop backend (Rust U4) before the UI (TS U5). U5 is
the only UI task and updates the Tauri mock for screenshots.

**Read before any task:** the "Decision" section of ADR 0061 and the part
of `reference-hosts.md` your task names. When this file and those two
disagree, they win.

## Rules for every task

- **Follow the existing code** in the files you edit: naming, comment
  style, test style. Prefer early returns and small functions. Don't touch
  files a task doesn't list, except to fix a compile error the task caused
  (say so in your report).
- **Go** (`internal/`, `cmd/`): `gofumpt`-clean (`make lint` checks it).
  Errors wrap with `%w` and the package's sentinel (`ErrInvalid`,
  `ErrValueInvalid`, `ErrUneditable`, `ErrRepoNotFound`, `ErrFetchFailed`).
  Tests: one `Test<Receiver>_<Method>` (or `Test<Func>`) per function, in
  the source file's `_test.go`, `t.Run` subtests per behaviour, testify
  `require` for preconditions and `assert` for checks, `t.Context()`.
  `t.Parallel()` only where the test does I/O (files, git), as the existing
  tests in the file do. Mocks: the existing moq `gitRunnerMock` in
  `internal/cache`.
- **Rust** (`desktop/src-tauri/`): as in [tasks.md](tasks.md) › "Rules for
  every task" (fmt, clippy `-D warnings` for both feature sets, no
  `unwrap()`/`expect()` outside tests, `AppError::new(code, message)`,
  inline `#[cfg(test)] mod tests`, `#[tokio::test]` for async).
- **TypeScript** (`desktop/src/`): as in [tasks.md](tasks.md) (strict,
  `readonly`, no `any`/`!`/`enum`, `export const X = (props): JSX.Element =>`,
  CSS modules, vitest + Testing Library queries by role and name, every
  button and select at least 40px tall under `@media (max-width: 720px)`).
- **No new dependencies** (Go modules, crates or npm packages).
- **Secrets:** no token is ever written to `config.toml`, a log, an error,
  or the frontend.

### Verify (Go tasks)

```sh
make lint && make test
```

### Verify (Rust tasks)

```sh
cd desktop/src-tauri && cargo fmt --check \
  && cargo clippy --all-targets -- -D warnings && cargo test \
  && cargo clippy --no-default-features --features online --all-targets -- -D warnings \
  && cargo test --no-default-features --features online
```

### Verify (TypeScript tasks)

```sh
cd desktop && npm ci && npm run typecheck && npm test -- --testTimeout=60000
```

---

## U1. `[[hosts]]` schema, effective hosts, read side of `ws config`

**Depends on:** nothing. **Parallel with:** nothing. **Size:** medium.

### Files

- `internal/repo/id.go`, `internal/repo/id_test.go`
- `internal/config/config.go`, `internal/config/config_test.go`,
  `internal/config/loader_test.go` (only if a test pins the default file's text)
- `internal/config/edit.go`, `internal/config/edit_test.go` (read side only)
- `internal/cli/config.go`, `internal/cli/config_test.go` (printing only)
- `docs/config.md`

### What to do

1. **`repo.ValidateHost`**: add exactly the function in reference-hosts.md
   › "Go" (it calls the existing `validateHostPort`).
2. **`config.go`**:
   - Add `KindAuto`, `KindGithub`, `KindForgejo`, `Host`, the `Hosts`
     field of `Config` (between `Defaults` and `Review`), `SpecDefaults`,
     `Config.EffectiveHosts`, `Config.SpecDefaults`, and `normalizeHosts`,
     with the signatures in reference-hosts.md.
   - `normalizeHosts(hosts []Host) ([]Host, error)`: for each entry (index
     `i`, reported as `i+1`): lowercase `Name`; empty → `hosts entry %d has
     no name`; `repo.ValidateHost` error → `hosts entry %d: name %q: %v`;
     empty `Kind` → `KindAuto`; a kind not in the three → `hosts entry %d
     (%s): kind must be auto, github, or forgejo, not %q`; a name already
     seen → `hosts lists %q twice`. Each error wraps `ErrInvalid` (callers
     that need `ErrValueInvalid` wrap again). Returns `nil` for an empty
     input.
   - `parse`: keep the `toml.MetaData`; in the unknown-keys error, when one
     of the keys is `hosts.token`, append `; tokens don't go in
     config.toml: use WS_FORGEJO_TOKEN_<HOST> or Settings › Forges`. Pass
     `meta.IsDefined("defaults", "host")` to `normalize`
     (`func (c Config) normalize(hostSet bool) (Config, error)`).
   - `normalize`: also run `normalizeHosts`; if the result is non-empty and
     `hostSet`, return `%w: set defaults.host or [[hosts]], not both; move
     %q into [[hosts]] with ws config set hosts` (the lowercased
     defaults.host). Keep `Hosts` in the returned `Config`.
   - `defaultFile`: replace it with the text in reference-hosts.md ›
     "`config.toml`" (no `host = …` line). It must still parse to
     `Default()`.
3. **`edit.go`** (read side; `Set`/`Unset` of `hosts` come in U2):
   - `TypeHosts Type = "hosts"`.
   - `fixedRules`: insert `{key: "hosts", typ: TypeHosts, table: "", leaf: "hosts"}`
     after `defaults.owners`.
   - `valueOf`: `"hosts"` → `cfg.EffectiveHosts()`; `"defaults.host"` →
     `cfg.EffectiveHosts()[0].Name`.
   - `defaultOf`: `"hosts"` → `[]Host{{Name: defaultHost, Kind: KindAuto}}`.
   - `settingFor`: for `hosts`, `Set` is `meta.IsDefined("hosts") && len(cfg.Hosts) > 0`.
   - `Set` and `Unset` with key `hosts`: return
     `fmt.Errorf("%w: hosts", ErrUneditable)` before any other work (U2
     replaces this).
4. **`internal/cli/config.go`** (printing only):
   - `configTOML`: a `[]config.Host` prints as
     `[{name = "<n>", kind = "<k>"}, …]`, each string with `strconv.Quote`,
     joined by `", "`; an empty slice prints `[]`.
   - `printConfigValue`: a `[]config.Host` prints one `name=kind` per line.
5. **`docs/config.md`**: in "The generated file", show the new default
   file; under "Schema" add a `### [[hosts]]` section after `[defaults]`
   with the table and read-time rules from reference-hosts.md, the
   effective-list rule, and an example; in `[defaults]`, say `host` is the
   old single-host form, read as the primary host when `[[hosts]]` isn't
   set, and that setting both is an error. Link ADR 0061.

### Tests

`internal/repo/id_test.go` — `TestValidateHost`: accepts `github.com`,
`git.example.com:3000`; rejects `""`, `a/b`, `a@b`, `h:0`, `h:65536`,
`h:03`.

`internal/config/config_test.go`:

- `TestParse`, new subtests: `hosts in order` (two tables, kinds kept);
  `kind defaults to auto`; `names are lowercased` (`Git.Example.COM`);
  `a port is allowed` (`git.example.com:3000`); `empty name is invalid`
  (a lone `[[hosts]]`, `ErrorIs ErrInvalid`, `ErrorContains "hosts entry 1
  has no name"`); `bad name is invalid` (`https://x`); `unknown kind is
  invalid` (`gitlab`); `repeated name is invalid` (`GitHub.com` and
  `github.com`, message `hosts lists "github.com" twice`); `token is an
  unknown key with a hint` (`ErrorContains "WS_FORGEJO_TOKEN_<HOST>"`);
  `defaults.host and hosts together are invalid`; `defaults.host alone
  still works` (`EffectiveHosts()` = `[{gitlab.example.com auto}]`);
  `the default file parses to Default()` (keep or add).
- `TestConfig_EffectiveHosts`: hosts set → hosts; only `Defaults.Host` →
  one auto entry; `Config{}` → `github.com` auto.
- `TestConfig_SpecDefaults`: names in order plus owners; `Config{}` →
  `Hosts: ["github.com"]`.
- `TestNormalizeHosts`: `nil` → `nil`; table of the error messages above.

`internal/config/edit_test.go`:

- `TestSettings`: the key order has `hosts` third; for a file with two
  `[[hosts]]` the `hosts` setting has `Type "hosts"`, `Set true`, `Value`
  the two `Host`s; `defaults.host` has `Value` the first name and `Set
  false`. For the default file, `hosts` has `Set false` and `Value`
  `[{github.com auto}]`.
- `TestLookup`: `Lookup(nil, "hosts")` works (no error, default value).
- `TestSet`/`TestUnset`: key `hosts` returns `ErrUneditable` (for now).

`internal/cli/config_test.go`:

- `TestConfigLine`: a hosts setting prints
  `hosts = [{name = "github.com", kind = "github"}, {name = "git.example.com", kind = "forgejo"}]`.
- `TestApp_printConfigValue`: hosts print as `github.com=github\ngit.example.com=forgejo\n`.

### Acceptance

- Old files (with `defaults.host`, without `[[hosts]]`) load and list
  exactly as before, except that `ws config list` shows the `hosts` row.
- `ws config get --json hosts` prints the JSON in reference-hosts.md.
- Verify (Go tasks) passes.

---

## U2. `ws config set`/`unset hosts`, keeping comments

**Depends on:** U1. **Parallel with:** U3. **Size:** medium.

### Files

- `internal/config/edit.go`, `internal/config/edit_test.go`
- `internal/cli/config.go`, `internal/cli/config_test.go`
- `docs/tools/ws-config.md`
- `internal/cli/guides/ws-config.md`, `ws-config-get.md`, `ws-config-list.md`,
  `ws-config-set.md`, `ws-config-unset.md`

### What to do

1. **`edit.go`**, replacing U1's `ErrUneditable` stubs:
   - `func parseHostValues(values []string) ([]Host, error)`: each value
     split at the first `=` (`strings.Cut`); no `=` → kind `auto`. Then
     `normalizeHosts`; wrap its error as `fmt.Errorf("%w: %w",
     ErrValueInvalid, err)`. (`checkText` already ran in `normalize`.)
   - In `normalize(rule, values)`: `hosts` → `parseHostValues(values)`
     (any number of values, like owners).
   - `Set` for `hosts`: `rewriteHosts(splitLines(data), want)`, then
     `verifyHosts(out, want)`. `Set` for `defaults.host` when the file
     sets `[[hosts]]` with entries: `fmt.Errorf("%w: defaults.host can't
     be set while [[hosts]] is; use ws config set hosts", ErrValueInvalid)`.
   - `Unset` for `hosts`: if the file doesn't set it, return `data, false,
     nil`; else `rewriteHosts(lines, nil)` and `verifyHosts(out, nil)`.
   - `func rewriteHosts(lines []string, hosts []Host) ([]string, error)`:
     exactly the five steps in reference-hosts.md › "`rewriteHosts`".
     Add `func isHostsHeader(text string) bool` (text with spaces and tabs
     removed starts with `[[hosts]]`). Step 1 needs to know whether the
     file defines `hosts`: decode `strings.Join(lines, "")` with
     `toml.Decode` into `&Config{}` and use `meta.IsDefined("hosts")`.
   - `func verifyHosts(out []byte, want []Host) error`: per
     reference-hosts.md (`Set == (len(want) > 0)`, `Value` deep-equals
     `want` when set, `defaults.host` not set); otherwise
     `fmt.Errorf("%w: hosts", ErrUneditable)`.
   - Update the `Set`/`Unset` doc comments to mention `hosts`.
2. **`internal/cli/config.go`**: add `const configHostsKey = "hosts"`;
   `configSetArgs` allows any number of values when `key ==
   configOwnersKey || key == configHostsKey`.
3. **Docs**: `docs/tools/ws-config.md` — the Keys table gains `hosts`
   (type `hosts`, default `[{name = "github.com", kind = "auto"}]`, `set`
   takes zero or more `NAME[=KIND]`, none removes the list); the
   `defaults.host` row says it reads as the primary host and can't be set
   while `[[hosts]]` is; `type` list gains `hosts` (a JSON array of
   `{name, kind}`); "How the file is changed" gains a bullet for `hosts`
   (the whole list is rewritten as one block where the first `[[hosts]]`
   was, or appended; `defaults.host` is removed in the same write; comment
   lines are kept, a comment after a `name`/`kind` value isn't; an inline
   `hosts = [...]` is `config_uneditable`); the Errors table gains the
   rows in reference-hosts.md › "Errors (additions)"; add the examples
   from reference-hosts.md. Make the same changes, shorter, in the guides
   listed above (`ws-config.md` key table and list example;
   `ws-config-set.md` text, error table and an example
   `ws config set hosts github.com git.example.com=forgejo`;
   `ws-config-unset.md`: unsetting `hosts` falls back to `github.com`;
   `ws-config-get.md`: hosts print as `NAME=KIND` lines;
   `ws-config-list.md`: the JSON example gains the `hosts` row).

### Tests

`internal/config/edit_test.go`:

- `TestSet`, subtests:
  - `hosts appended to a file without them`: the old generated file (with
    `host = "github.com"` and its comment) → `host =` line gone, its
    comment kept, the block of reference-hosts.md appended after one blank
    line; `changed` true; `Settings` of the result shows `hosts` set.
  - `hosts replaced in place`: a file `[defaults]\nowners = []\n\n# mine\n[[hosts]]\nname = "a.com"\n# keep me\nkind = "github"\n\n[review]\ntimeout = "2h"\n`,
    set to `b.com=forgejo a.com` → the block is where the first header was,
    `# mine` above it, `# keep me` kept, `[review]` untouched.
  - `interleaved tables`: two `[[hosts]]` with `[review]` between → one
    block at the first position; `[review]` and its keys unchanged.
  - `same list twice is unchanged`: second `Set` returns `changed` false.
  - `CRLF kept`: a CRLF file stays CRLF.
  - `bad values`: `a/b`, `x=gitlab`, `a.com a.com` → `ErrValueInvalid`.
  - `no values removes the list`: like `Unset`.
  - `inline hosts is uneditable`: `hosts = [{name = "a.com"}]\n` → `ErrUneditable`.
  - `defaults.host refused while hosts is set` → `ErrValueInvalid`.
- `TestUnset`: removes every `[[hosts]]` table and keeps comments; on a
  file without hosts returns the data unchanged and `false`; afterwards
  `Lookup(out, "hosts")` has `Set false` and value `[{github.com auto}]`.
- `TestRewriteHosts` and `TestIsHostsHeader`: the step rules directly
  (`[[ hosts ]]` with spaces is a header; `[hosts]` and `[[hostsx]]` are not).

`internal/cli/config_test.go` — `TestApp_configSet`: `ws config set
hosts a.com b.com=forgejo` writes the file and prints the hosts line;
`--json` output has `"type":"hosts"` and the two entries; `ws config set
hosts` with no values succeeds (`changed` false on a file without hosts).

### Acceptance

- `ws config set hosts github.com git.example.com=forgejo` on a generated
  file gives the TOML in reference-hosts.md, keeps every comment, and the
  file still loads.
- `ws config set defaults.host x` fails with `config_value_invalid` while
  `[[hosts]]` is set; `ws config unset hosts` then lets it succeed.
- Verify (Go tasks) passes.

---

## U3. Resolve short specs across hosts

**Depends on:** U1. **Parallel with:** U2. **Size:** medium.

### Files

- `internal/cache/cache.go`, `internal/cache/cache_test.go`
- `internal/cache/resolve.go`, `internal/cache/resolve_test.go`
- `internal/cli/app.go`
- `internal/workspace/store_test.go` (the one `cache.New` call)
- `internal/cli/guides/ws-cache-resolve.md`, `docs/tools/ws-cache.md`

### What to do

1. **`cache.go`**: `defaults` becomes `config.SpecDefaults`; `New` takes
   `defaults config.SpecDefaults` (doc: "defaults drive short-name
   resolution: hosts in order, then owners"). Add
   `func (c *Cache) hosts() []string` returning `c.defaults.Hosts`, or
   `[]string{"github.com"}` when it's empty.
2. **`internal/cli/app.go`**: `cache.New(log, runner, paths.Cache, cfg.SpecDefaults())`.
3. **`resolve.go`**, per the table in reference-hosts.md › "Resolution":
   - Update the `Source` comments: `SourceDefault` "an uncached owner/name
     when one host is configured, on that host"; `SourceProbe` "found
     upstream: a bare name under one of defaults.owners, or an owner/name
     when several hosts are configured, on the first host that has it".
   - `resolveOwnerName(ctx, ids, sp, probe)` (now takes `ctx` and
     `probe`): several cached matches → for each host in `c.hosts()`,
     the first host with any match wins (`preferred` per host until one
     narrows it); none cached → one host or `!probe`: today's `default`
     answer on `c.hosts()[0]`; else `c.probeHosts(ctx, sp.Raw, []string{sp.Owner}, sp.Name)`.
     Update its caller in `resolve`.
   - `resolveName`: the no-match `probe` case calls
     `c.probeHosts(ctx, sp.Raw, c.defaults.Owners, sp.Name)`.
   - Replace `probe` with `probeHosts` and add `alsoOn`, with exactly the
     steps, messages and warning texts in reference-hosts.md.
   - Update the `Resolve` doc comment to describe hosts in order.
4. **`cache_test.go`, `store_test.go`**: construct with
   `config.SpecDefaults{Hosts: []string{gittest.Host}, Owners: []string{"o"}}`
   (and `config.SpecDefaults{}` where `config.Defaults{}` was). In
   `resolve_test.go`, `c.defaults = config.Defaults{Host: h, Owners: o}`
   becomes `c.defaults = config.SpecDefaults{Hosts: []string{h}, Owners: o}`.
5. **Docs**: `ws-cache-resolve.md` — the intro says a short spec that
   isn't cached is probed on each configured host in order; the `source`
   bullets per reference-hosts.md; add the also-there and unreachable
   warnings; the exit-code row for `fetch_failed` reads "No configured host
   has it, and at least one couldn't be reached". `docs/tools/ws-cache.md`
   line about `default` the same way.

### Tests (`resolve_test.go`; use `gitRunnerMock` and `gitFailure` as the existing probe tests do; `args[3]` is the URL)

- `TestCache_resolveOwnerName` (update to `SpecDefaults`): `prefers the
  earliest host` (hosts `[gitlab.com, github.com]`, matches on both →
  gitlab.com); `uncached with one host is default` (no git call: the mock
  fails the test if called); `uncached with Lookup is default on the first
  host`; `uncached with two hosts probes` (second host has it → `probe`,
  id on the second host).
- `TestCache_probeHosts` (port every `TestCache_probe` subtest, then add):
  - `host order before owner order`: hosts `[h1, h2]`, owners `[a, b]`;
    `h1/b/n` and `h2/a/n` exist → `h1/b/n`; the calls are `h1/a`, `h1/b`,
    then `h2/b` (the also-on check).
  - `also on a later host warns`: `h1/a/n` and `h2/a/n` exist → id
    `h1/a/n`, one warning equal to
    `a/n is also on h2; using h1/a/n. Name the host to use the other.`
    (read with `c.Warnings()`).
  - `also-on failures don't warn`: `h2` fails with a DNS error during the
    also-on check → no warning.
  - `unreachable host is skipped`: owners `[a, b]`; `h1` fails with
    `Could not resolve host` → only one call to `h1` (`h1/a`), none to `h1/b`; `h2/a/n` exists →
    found, and a warning starting `couldn't reach h1 while looking for n`.
  - `unreachable and missing everywhere is fetch failed`: `h1` DNS error,
    `h2` "Repository not found" → `ErrorIs ErrFetchFailed`, message
    contains `looking for h1/a/n`.
  - `missing everywhere is not found`: message `"n" isn't cached, and no
    owner in defaults.owners has it on h1, h2`; for `owner/name`:
    `"a/n" isn't cached, and none of h1, h2 has it`.
  - `one host keeps today's messages`: the not-found and fetch-failed
    texts equal the old ones.
- `TestCache_hosts`: empty `Hosts` → `["github.com"]`.

### Acceptance

- With one host (every existing config), `ws cache resolve` gives the same
  ids, sources and errors as before.
- With two hosts, `owner/name` and bare names resolve as the table says,
  and the warnings appear in `ws cache resolve --json`'s `warnings`.
- Verify (Go tasks) passes.

---

## U4. Desktop backend: hosts from `config.toml`, migration, `reviews_set_hosts`

**Depends on:** U2. **Parallel with:** U3 (memory permitting). **Size:** large.

### Files

- `desktop/src-tauri/src/ws_config.rs`
- `desktop/src-tauri/src/reviews/hosts.rs` (new), `reviews/mod.rs`
- `desktop/src-tauri/src/reviews/store.rs`
- `desktop/src-tauri/src/reviews/checker.rs`, `reviews/view.rs`
- `desktop/src-tauri/src/lib.rs`, `desktop/src-tauri/src/online/dispatch.rs`

### What to do

Everything in reference-hosts.md › "Desktop backend (Rust)":

1. **`ws_config.rs`**: `ConfigGet`, `pub(crate) async fn get`; make `set`
   and `unset` `pub(crate)`.
2. **`reviews/hosts.rs`**: `ConfiguredHost`, `HostList`, `read`, `write`,
   `choice`, `migrate`, built on `ws_config::{get, set, unset}`. Register
   `pub mod hosts;` in `reviews/mod.rs` (alphabetical).
3. **`store.rs`**: remove `ForgeSettings::choice` and `set_choice`; doc
   the struct as legacy; replace the `set_choice` test with
   `legacy_forges_still_load` (the JSON in ADR 0060's reference parses to
   one `git.example.com` → `Forgejo` entry).
4. **`view.rs`**: `CheckState.configured`; `HostView.configured`,
   `HostView.primary`; `hosts_view` order (configured in list order, then
   other cached hosts sorted) and `choice` from `configured`. `build`
   keeps its signature.
5. **`checker.rs`**: in `check_steps`, `warnings` first, then
   `hosts::migrate` and `hosts::read` with the warning texts in the
   reference; store `configured`; `known_hosts()` replaces
   `cached_hosts()` in `probe_hosts` and `decide_hosts`;
   `hosts::choice(&configured, &host)` replaces
   `settings.forges.choice(&host)`. Add
   `pub fn set_configured(&self, hosts: Vec<ConfiguredHost>)`.
6. **`mod.rs`**: replace `reviews_set_forge` with `reviews_set_hosts`
   (signature and behaviour in the reference); for each name,
   `check_host` and a duplicate check (`setting_invalid`,
   `"<host>" is listed twice`). **`lib.rs`** and **`online/dispatch.rs`**:
   swap the command in `generate_handler!`, `COMMANDS` and the `match`.

### Tests

`reviews/hosts.rs` (fake `ws` script, as in `ws_config.rs`'s tests,
writing its args to a file):

- `read_parses_the_hosts_setting`: `ws` prints the `get` JSON of
  reference-hosts.md → `HostList { hosts: [github.com Auto], set: false }`,
  args `config --json get -- hosts`.
- `write_passes_name_equals_kind`: args
  `config --json set -- hosts github.com=github git.example.com=forgejo`.
- `write_of_nothing_unsets`: args `config --json unset -- hosts`.
- `read_keeps_the_ws_error_code`: `ws` fails with `config_invalid` → the
  `AppError` has that code.
- `choice_is_auto_when_unlisted`.
- `migrate_without_legacy_does_nothing`: no `ws` call (the fake fails the
  test if called), `Ok(false)`.
- `migrate_copies_kinds_after_the_primary_and_clears_desktop_json`: legacy
  `{git.example.com: Forgejo, github.com: Github}`, `ws config get`
  answers `set: false` with `[{github.com auto}]` → the set call is
  `hosts github.com=github git.example.com=forgejo`; afterwards
  `store.get().settings.forges` is empty.
- `migrate_when_hosts_are_set_only_clears`: `get` answers `set: true` →
  no `set` call, forges emptied, `Ok(true)`.
- `migrate_twice_writes_once`.

`reviews/checker.rs` (extend the fake `ws` in `fake_programs_cached` with
`config) echo '<get JSON>' ;;`, default answer github.com auto, set: false;
add a variant taking the answer):

- `hosts_come_from_config_toml`: the answer lists `SVC_HOST` as
  `forgejo` → the repo on `SVC_HOST` is checked through Forgejo (replaces
  the test that used `set_choice`).
- `configured_hosts_come_first_in_the_view`: configured
  `[git.example.com, github.com]` → `view.hosts` order and
  `configured`/`primary` flags as configured; an unconfigured cached host
  follows with `configured: false`.
- `config_failure_is_a_warning`: `ws config` fails → the check finishes,
  `warnings` contains `ws config get hosts: …`, hosts are all Auto.
- `migration_runs_in_the_check`: legacy forges in the store → after
  `check`, the store's forges are empty and the fake saw the `set` call.

`reviews/mod.rs` or `view.rs`: `host_view_serializes_configured_and_primary`
(the JSON in the reference).

Existing tests must keep passing; update fakes, not assertions, unless an
assertion was about `desktop.json` forges.

### Acceptance

- `grep -rn "set_choice\|reviews_set_forge" desktop/src-tauri/src` prints nothing.
- The checker never reads `settings.forges` except in `hosts::migrate`.
- Verify (Rust tasks) passes.

---

## U5. Settings › Forges edits hosts; Repos points to it; mock

**Depends on:** U4. **Parallel with:** nothing. **Size:** medium.

### Files

- `desktop/src/api.ts`
- `desktop/src/features/reviews/reviewsSlice.ts`, `reviewsSlice.test.ts`
- `desktop/src/features/settings/ForgesGroup.tsx`, `ForgesGroup.module.css`, `ForgesGroup.test.tsx`
- `desktop/src/features/settings/RepoGroup.tsx`, `RepoGroup.test.tsx`
- `desktop/src/test/fixtures.ts`
- `desktop/test/tauri-mock.js`
- `desktop/README.md` (the Repos and Forges settings bullets only)

### What to do

Everything in reference-hosts.md › "Frontend (TypeScript)":

1. **`api.ts`**: `ConfiguredHost`, `ConfigType`/`ConfigValue` additions,
   `HostView.configured`/`primary`; replace `reviewsSetForge` with
   `reviewsSetHosts`. Fix any type narrowing `npm run typecheck` then
   reports in the listed files only.
2. **`reviewsSlice.ts`**: replace `setForge`/`ForgeChange` with `setHosts`.
3. **`ForgesGroup.tsx`**: the Hosts table, Other hosts table, Add a host
   form, caption and error handling as specified. Keep `TokenDialog` and
   the token buttons as they are. Helper (exported for tests):
   ```ts
   /** The configured hosts as config.toml holds them, in order. */
   export const configuredHosts = (hosts: readonly HostView[]): readonly ConfiguredHost[];
   /** The list with the item at index moved by one place. */
   export const moved = <T,>(list: readonly T[], index: number, by: -1 | 1): readonly T[];
   /** "" when name can be added, else the helper text. */
   export const hostProblem = (name: string, configured: readonly ConfiguredHost[]): string;
   ```
4. **`RepoGroup.tsx`**: remove `HostRow`; add `HostsRow`; update
   `matchedBy`. The `RepoGroup` doc comment says "the default owners and
   the hosts in config.toml (edited in Forges)".
5. **`fixtures.ts`**: `hostView` defaults `configured: true, primary:
   false`; `fakeApi` gets `reviewsSetHosts` (resolves `reviewsView()`) and
   loses `reviewsSetForge`; `configListing()` gains the `hosts` setting
   (`[{ name: "github.com", kind: "auto" }]`, `set: false`) after
   `defaults.owners`.
6. **`tauri-mock.js`**: per reference-hosts.md › "Tauri mock". Log any
   unhandled command as the mock already does.
7. **`desktop/README.md`**: the "Repos settings" bullet says Repos keeps
   the default owners and the spec tester and lists the hosts; the Forges
   bullet says it edits the ordered host list in `config.toml` (`[[hosts]]`),
   kinds and tokens.

### Tests

`ForgesGroup.test.tsx` (replace the `setForge` tests):

- `lists configured hosts in order with the primary marked`.
- `changing a kind sends the whole list` (`reviewsSetHosts` called with
  both hosts, the second's kind changed).
- `moves a host up` and `removes a host` (calls with the reordered /
  shortened list; the first row has no "Move … up", the last no "Move …
  down").
- `adds a typed host` (`Git.Example.com:3000` + Forgejo → appended
  lowercased with kind forgejo; Enter also adds).
- `refuses a bad or repeated host` (`a/b` → `Not a host name`;
  an existing host → `Already in the list`; no API call).
- `adds an other host` (an unconfigured cached host's `Add <host>`
  appends it with `auto`).
- `shows the error when saving fails` (`reviewsSetHosts` rejects with
  `config_value_invalid` → an alert with the message).
- `refreshes the config after saving` (`configList` called again).
- Keep the token tests.

`RepoGroup.test.tsx`: `no default host field` (no textbox named "Default
host"); `lists the hosts with the primary marked`; `Edit in Forges opens
the Forges group` (store `settings.group` is `forges`); the spec tester's
`default`/`probe` texts.

`reviewsSlice.test.ts`: `setHosts stores the returned view`.

`ForgesGroup.module.css`: the order/remove buttons and the Add button are
at least 40px tall under `@media (max-width: 720px)`.

### Screenshots (taken by the orchestrator after U5, with the mock)

Retake, same file names: `desktop/docs/reviews-forges-settings.png`
(Forges with two configured hosts, one Forgejo with a saved token, and one
Other host), `desktop/docs/settings-repos.png` and
`desktop/docs/settings-repos-ambiguous.png` (no Default host; Hosts row),
`desktop/docs/online-settings-repos-mobile.png`. New:
`desktop/docs/settings-forges-add.png` (the Add form showing `Already in
the list`) and `desktop/docs/mobile-settings-forges.png` (phone width,
390px). Layout checks: no clipped host names or selects, order buttons
not overlapping the Account column, nothing wider than the viewport at
390px. Add the two new shots to `desktop/README.md` beside
`reviews-forges-settings.png`.

### Acceptance

- Settings › Repos has no editable host; Settings › Forges adds, removes,
  reorders and re-kinds hosts, and each change is one
  `reviews_set_hosts` call with the whole list.
- `grep -rn "setForge\|reviewsSetForge\|reviews_set_forge" desktop/src desktop/test` prints nothing.
- Verify (TypeScript tasks) passes.
