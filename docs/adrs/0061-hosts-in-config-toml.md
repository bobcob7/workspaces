# 0061. One ordered `[[hosts]]` list in `config.toml` holds every forge host; short specs try each host in turn, and Settings › Forges is its only editor

- Status: Proposed
- Date: 2026-11-13
- Supersedes: the host-configuration part of [0060](0060-review-filters-and-forges.md)
  (`desktop.json` `settings.forges.hosts.<host>.kind`, ADR 0060's forge rule 1);
  amends [0007](0007-repo-spec-syntax-and-resolution.md)'s resolution of short specs

## Context

### Problem

Two settings now name hosts. `config.toml` has one `defaults.host`, used by
`ws` to resolve `owner/name` and bare names ([0007](0007-repo-spec-syntax-and-resolution.md)),
edited in Settings › Repos › "Default host". `desktop.json` has
`settings.forges.hosts.<host>.kind`, used only by the desktop's Reviews
checker to pick GitHub or Forgejo ([0060](0060-review-filters-and-forges.md)),
edited in Settings › Forges. Someone with repos on github.com and on a
Forgejo host has to add the Forgejo host in Forges, and still can't type
`team/svc` for a repo there unless they give up github.com as the default.

Where should the one list of hosts live, how do short specs use more than
one host, and how does the desktop move to it without losing what people
set in Forges?

### What a solution needs

1. **One list, read by both programs**: `ws` (CLI, agents, Workspace
   Online) and the desktop app see the same hosts in the same order.
2. **Old files keep working**: a `config.toml` with only `defaults.host`
   (every generated file has `host = "github.com"`) resolves exactly as
   before; nothing set means github.com.
3. **No secrets in `config.toml`**: tokens stay in
   `WS_FORGEJO_TOKEN_<HOST>` and `forge-tokens.json` ([0060](0060-review-filters-and-forges.md)).
4. **Predictable resolution**: a short spec's answer depends only on the
   cache and the list order; an unreachable host doesn't turn into "not
   found", and doesn't hide a host that answers.
5. **`ws config` stays the only writer** of `config.toml`, keeping its
   comments ([0054](0054-settings-all.md)).

### Relevant facts

- `config.toml` is decoded with BurntSushi/toml. An array of tables
  `[[hosts]]` decodes into a Go slice; `MetaData.IsDefined("hosts")` is
  true for it and for an inline `hosts = [{…}]`; an unknown key inside a
  `[[hosts]]` table is reported by `Undecoded()` as `hosts.<key>`; an empty
  `[[hosts]]` table decodes as an entry with an empty name[^toml-probe].
- `ws config set` edits one `key = value` line in a named table and keeps
  every other line ([0054](0054-settings-all.md), `internal/config/edit.go`).
  It has no notion of an array of tables: its `headerName` reads
  `[[hosts]]` as a table named `[hosts`.
- Repo ids keep a non-default port in the host (`git.example.com:2222`,
  [0020](0020-repo-spec-edge-cases.md)); `defaults.host` refuses `:`;
  Settings › Forges accepts `host:port` (`forge::is_host`).
- The desktop never reads or writes `config.toml` itself; it runs
  `ws config list|set|unset --json` (`desktop/src-tauri/src/ws_config.rs`).
- Probing is `git ls-remote -q -- <url> HEAD` per candidate. A miss on
  github.com fails with `repository '…' not found` in 0.27–0.32 s, a hit
  takes 0.23–0.26 s; on codeberg.org (Forgejo) a miss fails with
  `could not read Username …: terminal prompts disabled` in 0.40–2.0 s and a
  hit takes 1.4–1.6 s; an unresolvable host fails with `Could not resolve
  host` in 19–44 ms. Both miss messages already match `repoMissing`;
  the DNS failure doesn't[^bench-ls-remote].
- Today an uncached `owner/name` is assumed to be on `defaults.host`
  without any network call (`source: "default"`); an uncached bare name is
  probed on `defaults.host` under each of `defaults.owners`, and any
  non-"missing" failure stops with `fetch_failed`
  (`internal/cache/resolve.go`).

## Decision

We will keep the hosts in `config.toml` as an ordered array of tables:

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

- **`name`** (required): a host as it appears in repo ids, lowercased:
  letters, digits, `.`, `-`, and an optional `:<port>` (1–65535), the rule
  repo ids already use ([0020](0020-repo-spec-edge-cases.md)). No scheme,
  user, path or spaces.
- **`kind`** (optional): `auto` (the default), `github` or `forgejo`.
  Only the desktop reads it, as ADR 0060's forge rule 1; `ws` checks it and
  otherwise ignores it.
- **No other keys.** A `token` key is an unknown key, and the error says
  tokens belong in `WS_FORGEJO_TOKEN_<HOST>` or Settings › Forges.
- A name listed twice is an error. Setting both `defaults.host` and
  `[[hosts]]` is an error that says to move the host into the list.
- **The effective list** is `[[hosts]]` when it has entries; otherwise
  `defaults.host` alone, kind `auto`; otherwise `github.com`. The first
  entry is the **primary host**.

**`ws config` gets one new key, `hosts`** (type `hosts`; value a list of
`{name, kind}`), set as a whole: `ws config set hosts github.com=github
git.example.com:3000=forgejo`. A value is `NAME` or `NAME=KIND`. Setting it
rewrites every `[[hosts]]` table as one block where the first one was (or
appends it), and removes `defaults.host`, in one write; comments and blank
lines stay. Setting no values, or `ws config unset hosts`, removes the
tables. `defaults.host` stays a key: it reads as the primary host, and
setting it fails while `[[hosts]]` is set. The exact edit rules, JSON and
error codes are in the [reference](../design/review-filters/reference-hosts.md).

**Resolving short specs** ([0007](0007-repo-spec-syntax-and-resolution.md)
rules 2 and 3, changed):

- **Cached** matches: unchanged, except that an `owner/name` matching on
  several hosts prefers the earliest host in the list (today: prefers
  `defaults.host`).
- **Uncached `owner/name` with one host**: unchanged, taken to be on it
  without a network call (`source: "default"`).
- **Uncached `owner/name` with two or more hosts, or an uncached bare
  name**: probe upstream with `git ls-remote`, for each host in list order,
  for each candidate owner in order (the spec's owner, or
  `defaults.owners`). The first hit wins (`source: "probe"`).
- **Also-there warning**: after a hit on host *i*, the same `owner/name`
  is probed on every later host; each that has it adds a warning
  (`bobcob7/api is also on git.example.com; using github.com/bobcob7/api.
  Name the host to use the other.`). A failure in this check is only
  logged.
- **Unreachable hosts**: a failure that isn't "repo missing" skips the rest
  of that host's owners, adds a warning, and moves on to the next host. A
  later hit still wins. If nothing hits and any host failed, the result is
  `fetch_failed` (with the first failure, as today), since an outage
  mustn't read as "not found". Only when every host answered "missing" is
  it `repo_not_found`.
- A spec that names a host is unchanged. `source` gains no values.

**The desktop**:

- **Settings › Forges is the one editor** of the list: add a host (name and
  kind), remove one, move one up or down (the first is the primary), change
  its kind, and set its token as before. It saves with a new command,
  `reviews_set_hosts`, which runs `ws config set hosts …`. Cached hosts not
  in the list are shown below it with an Add button.
- **Settings › Repos** keeps Default owners and the spec tester. Default
  host is replaced by a read-only Hosts row listing the effective hosts and
  a button that opens Forges.
- **The checker reads the list** with `ws config get --json hosts` at the
  start of every check, instead of `desktop.json`.
- **One-time migration**: when `desktop.json` has
  `settings.forges.hosts` entries, the checker copies them into
  `config.toml` if `hosts` isn't set there (the current primary host first,
  then the old entries in name order, keeping their kinds), then empties
  `settings.forges` in `desktop.json`. A crash between the two steps is
  safe: the next check finds `hosts` set, copies nothing, and empties it.
  After that `settings.forges` is ignored.
- Tokens don't move: `WS_FORGEJO_TOKEN_<HOST>`, then `forge-tokens.json`
  (0600), never `config.toml` and never the webview.

### Why this option

1. `config.toml` is the file both programs already share, through `ws
   config`; the desktop needs no new file access, and Workspace Online
   gets the list from the same place.
2. An old file has no `[[hosts]]`, so its effective list is its
   `defaults.host`, and a one-host list keeps the no-network `default`
   answer for `owner/name`. Nothing changes until someone adds a second
   host.
3. Only names and kinds are in the list; the schema rejects `token`.
4. Host order then owner order, with the also-there warning, makes a short
   spec's answer predictable and tells you when it could have meant
   something else. Treating "unreachable" separately keeps 0007's rule that
   an outage isn't "not found".
5. A whole-list `set` fits `ws config`'s one-key-at-a-time model and its
   lock; the edit keeps comments outside the `[[hosts]]` key lines.

It's weak in three ways. An uncached `owner/name` with two hosts now costs
a network call per host (0.25–1.6 s each, measured above), where it cost
none. The also-there check probes every later host even after a hit. And
a comment written after a value on a `name =` or `kind =` line is lost when
the list is rewritten.

## Alternatives

### Keep two lists, and let `ws` read the desktop's forges

**Pros:** no migration; Forges keeps writing `desktop.json`.
**Cons:** `ws` would read the desktop's private state file
([0034](0034-desktop-state-file.md)), which doesn't exist in a CLI-only
setup or a fresh Workspace Online pod; the order and the primary host would
still live in `config.toml`.

### `defaults.hosts = ["github.com", "git.example.com"]` plus a separate `[forges]` table for kinds

**Pros:** a plain array edits with the existing one-line code, like
`defaults.owners`.
**Cons:** a host's kind and its place in the list live in two keys that
can disagree (a kind for a host that isn't listed); adding a field later
(say a clone URL scheme) needs a third key.

### Per-host keys, `hosts.<name>.kind`, like `review.profiles.<name>`

**Pros:** `ws config set hosts.git.example.com.kind forgejo` edits one line.
**Cons:** TOML tables have no order, so the primary host and the probe
order need yet another key; dotted host names make the keys ambiguous
(`hosts.git.example.com.kind`) unless quoted.

### Probe every host in parallel and take the first by list order

**Pros:** a two-host miss costs one round trip instead of two.
**Cons:** always spends a call on every host even when the first answers;
`ls-remote` against several hosts at once makes failures interleave in
logs. Could be added later inside the same rule (the answer doesn't
change, only the timing).

## Consequences

- `config.toml` gains `[[hosts]]`; the generated file stops writing
  `host = "github.com"` and shows a commented `[[hosts]]` example instead.
  An older `ws` reading a file with `[[hosts]]` fails with `config_invalid`
  (unknown key `hosts`), as it would for any newer key.
- `ws config` gains the `hosts` key (type `hosts`); `ws config set
  defaults.host` fails with `config_value_invalid` while `[[hosts]]` is set.
- `ws cache resolve` and every command that resolves specs probe more
  hosts; their `warnings` can name an also-there or unreachable host.
  `source` keeps its four values; `default` now only happens with one host.
- `cache.New` takes `config.SpecDefaults` (hosts and owners) instead of
  `config.Defaults`.
- The desktop: `reviews_set_forge` is replaced by `reviews_set_hosts`;
  `HostView` gains `configured` and `primary`, and lists configured hosts
  first in list order; `desktop.json`'s `settings.forges` is read once for
  the migration and then left empty. An older app that still writes
  `settings.forges` after the migration has no effect on a newer one. A
  desktop app paired with an older `ws` (no `hosts` key) gets the warning
  `ws config get hosts: …` on each check and treats every host as `auto`.
- Conformance: `go test ./internal/config/...` covers parsing, validation,
  the effective list and the `hosts` edits (comments kept, `defaults.host`
  removed, uneditable forms refused); `go test ./internal/cache/...` covers
  the host×owner order, also-there warnings and the unreachable rules with
  the moq git runner; `cargo test` covers reading and writing hosts through
  a fake `ws`, the migration's idempotence, and the checker's use of the
  list; `npm test` covers the Forges editor and the Repos pointer.

## References

[^toml-probe]: Checked 2026-11-13 with a throwaway test in `internal/config` (BurntSushi/toml as pinned in `go.mod`, Go 1.27.3): decoding `[[hosts]]\nname = "a"\ntoken = "x"\n\n[[hosts]]\nname = "b"` gave two entries, `IsDefined("hosts")` true and `Undecoded()` `[hosts.token]`; `hosts = [{name = "a"}]` gave one entry with `IsDefined("hosts")` true; a lone `[[hosts]]` gave one entry with an empty name; a file without hosts gave `IsDefined("hosts")` false.
[^bench-ls-remote]: Measured 2026-11-13 in a Linux pod, git 2.47.3, `GIT_TERMINAL_PROMPT=0`, 3 runs each of `git ls-remote -q -- <url> HEAD`, wall time from `date +%s%N`: `https://github.com/bobcob7/workspaces.git` 233–255 ms; `https://github.com/bobcob7/no-such-repo-xyz.git` 274–318 ms, `fatal: repository '…' not found`; `https://codeberg.org/forgejo/forgejo.git` 1392–1619 ms; `https://codeberg.org/forgejo/no-such-repo-xyz.git` 403–1986 ms, `fatal: could not read Username for 'https://codeberg.org': terminal prompts disabled`; `https://unreachable.invalid/o/n.git` 19–44 ms, `Could not resolve host: unreachable.invalid`. `repoMissingPattern` in `internal/cache/classify.go` matches the first two failure messages and not the third. Network-bound.
