# Configuration

Where `ws` keeps its files, what `config.toml` may contain, and how both are
found and checked. The decisions behind this are in
[0011](adrs/0011-config-dir.md) (locations),
[0007](adrs/0007-repo-spec-syntax-and-resolution.md) (what the keys do),
[0019](adrs/0019-https-upstream-urls.md) (why there's no protocol key), and
[0054](adrs/0054-settings-all.md) (changing it with `ws config`).

## The config directory

Everything `ws` stores lives under one directory, `CONFIG_DIR`. It's chosen
in this order:

1. `--config-dir <path>`
2. the `WS_CONFIG_DIR` environment variable
3. the platform default: `os.UserConfigDir()/workspaces`

| Platform | Default `CONFIG_DIR`                                        |
| -------- | ----------------------------------------------------------- |
| macOS    | `~/Library/Application Support/workspaces`                  |
| Linux    | `$XDG_CONFIG_HOME/workspaces`, else `~/.config/workspaces`  |

- An empty value counts as unset: `WS_CONFIG_DIR=` and `--config-dir ""`
  fall through to the next choice, as `XDG_CONFIG_HOME` does.
- A relative path is made absolute against the current directory.
- If none of the three gives a directory (no home directory, for example),
  the command fails and asks you to set `WS_CONFIG_DIR`.

To run `ws` fully isolated, for tests or a sandboxed agent, point it at a
fresh directory:

```console
$ WS_CONFIG_DIR="$(mktemp -d)" ws list
```

### Layout

```
CONFIG_DIR/
  config.toml          settings; see below
  cache/               one bare clone per upstream repo (0008)
  workspaces/          one directory per workspace, your repos only (0012)
    CLAUDE.md          tells agents to run `ws llm` (0014)
  state/<id>/          ws's records and locks per workspace
  reviews/             submitted reviews, by repo and commit (0039)
  review/profiles/     your review profiles, <name>.md (0038, 0042)
  loops/               agent loops, <name>.toml (0048)
  machine/roles/       your ws machine roles, <name>.md (0050)
```

What `ws` keeps under `state/` and `reviews/`, and when, is described in
[Workspace state](state.md). `review/profiles/` is yours: `ws` reads it,
and writes it only when you run `ws review profiles add`, `edit`, or `rm`
([0042](adrs/0042-review-profile-management.md));
[`ws review`](tools/ws-review.md#ws-review-prime) describes the files.
`machine/roles/` is yours too: `ws` writes it only for `ws machine roles
add`, `edit`, and `rm`
([0050](adrs/0050-machine-wait-results-and-roles.md));
[`ws machine`](tools/ws-machine.md#ws-machine-roles) describes the files.

`CONFIG_DIR` and `config.toml` are created by the first command that needs
them. The other entries are created by the commands that use them. On
macOS, `cache/` and `workspaces/` are excluded from Time Machine when
they're created. `tmutil` takes several seconds, so it runs in the
background; a failure shows only with `-v`. The cache
and workspaces must share a filesystem so that workspace clones can hard-link
the cache's objects ([0009](adrs/0009-workspace-repository-remotes.md)),
which is why they're siblings.

## When the config is read

Every command reads `config.toml` before doing anything else, except:

- `ws llm`, `ws help`, `ws completion`, `--help`, and `--version`, which
  never touch `CONFIG_DIR`
- a command whose arguments are wrong: usage errors (exit 2) are reported
  first, so a mistyped command creates nothing on disk

## The generated file

If `config.toml` doesn't exist, `ws` writes this one:

```toml
# ws configuration. Edit it by hand, or with ws config set; ws keeps your comments.

[defaults]
# Host for repo specs without one, such as "owner/name".
host = "github.com"

# Owners to try, in order, for a bare repo name that isn't cached,
# for example ["your-name", "your-org"].
owners = []

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

- **Only `ws config` changes it.** Edit it freely. Apart from creating
  it, `ws` writes it only when you run `ws config set` or `ws config
  unset`, and then changes only the lines of the key you named, keeping
  every comment and other line (see below). To get the default back,
  delete it.
- **It appears whole or not at all.** `ws` writes a temporary file in
  `CONFIG_DIR` and links it into place, so two `ws` commands starting at once
  can't leave a partial file, and neither replaces a file that's already
  there.

## Changing it with `ws config`

[`ws config`](tools/ws-config.md) lists, reads, sets and unsets the keys
below. It is also how the desktop app and Workspace Online edit them in
Settings ([0054](adrs/0054-settings-all.md)).

```console
$ ws config set defaults.owners platform platform/tools me
$ ws config set review.timeout 90m
$ ws config unset review.profiles.security.model
$ ws config list --json
```

A change is checked with the rules below before anything is written. It
is made under a lock on `CONFIG_DIR/config.lock`, and written to a temp
file that is renamed into place, so a reader never sees half a file. A
key written in a form `ws config` doesn't edit, such as a dotted key or
an inline table, fails with `config_uneditable` and leaves the file as
it was.

## Schema

`config.toml` is [TOML](https://toml.io/en/v1.0.0). Every key is optional;
a missing key takes its default, and an empty file means all defaults.

### `[defaults]`

How short repo specs resolve
([0007](adrs/0007-repo-spec-syntax-and-resolution.md)).

| Key      | Type             | Default        | Used for |
| -------- | ---------------- | -------------- | -------- |
| `host`   | string           | `"github.com"` | the host of a spec without one, such as `owner/name`; also preferred when `owner/name` matches cached repos on several hosts |
| `owners` | array of strings | `[]`           | for a bare `name`: preferred when it matches cached repos under several owners, and tried in order on `host` when it isn't cached |

Rules, checked every time the file is read:

- **`host`** must be a bare host name: not empty, and without spaces, `/`,
  `@`, or `:`. `github.com` and `gitlab.example.com` are fine;
  `https://github.com` and `git@github.com` aren't.
- **Each owner** must be non-empty and without spaces, `@`, or `:`. `/`
  is allowed for nested groups (`gitlab-org/api`), but not at either end or
  twice in a row.
- **Both are lowercased**, since repo ids are lowercase (0007). Repeated
  owners are dropped, keeping the first.

Example, for someone whose repos are mostly on a company GitLab:

```toml
[defaults]
host = "gitlab.example.com"
owners = ["platform", "platform/tools", "me"]
```

### `[review]`

How `ws review` runs reviews ([0038](adrs/0038-review-fans-out-to-profile-subagents.md),
[0039](adrs/0039-ws-review-command.md)).

| Key       | Type     | Default | Used for |
| --------- | -------- | ------- | -------- |
| `timeout` | duration | `"60m"` | how long [`ws review wait`](tools/ws-review.md#ws-review-wait) waits without `--timeout`; a Go duration such as `"90m"` or `"2h"` |
| `remove_finished` | bool | `true` | whether the removal pass removes a review's agent and workspace once its agent is idle or stopped and every profile has submitted ([0045](adrs/0045-finished-reviews-are-removed.md)); the findings are kept either way |

### `[review.profiles.<name>]`

One table per review profile you want to change; `ws review profiles`
lists the names. `ws review start`'s `--profile` and `--auto` replace the
set these choose, but a profile they name still takes its `model` from
here unless the flag gives one.

| Key     | Type   | Default            | Used for |
| ------- | ------ | ------------------ | -------- |
| `when`  | string | the profile's own  | `always`: every review; `auto`: a review whose changed files match the profile's patterns; `off`: only when asked for with `--profile` |
| `model` | string | `""`, the session's | the model of the profile's reviewer subagent: `sonnet`, `opus`, `haiku`, `fable`, a full model id, or `inherit` |

Rules: `timeout` can't be negative; `when` must be `always`, `auto`, or
`off`. A `<name>` that isn't a profile makes `ws review start` fail with
`profile_unknown`.

```toml
[review]
timeout = "90m"

[review.profiles.security]
model = "opus"

[review.profiles.efficiency]
when = "auto"
```

### Keys that aren't here

- **Unknown keys are an error**, naming each one, so a typo such as `owner`
  for `owners` is caught rather than silently ignored.
- **There's no protocol setting.** `ws` builds `https://` URLs and leaves
  the protocol to git. To use SSH, set it in git, which also covers every
  other tool ([0019](adrs/0019-https-upstream-urls.md)):

  ```console
  $ git config --global url."git@github.com:".insteadOf https://github.com/
  # or SSH for pushes only
  $ git config --global url."git@github.com:".pushInsteadOf https://github.com/
  ```

## Errors

| Situation | Error code | Exit |
| --------- | ---------- | ---- |
| `config.toml` isn't valid TOML, has a value of the wrong type, an unknown key, or a value that breaks the rules above | `config_invalid` | 1 |
| `CONFIG_DIR` can't be found, created, or read, or the default file can't be written | `internal` | 1 |
| `ws config`: an unknown key, a bad value, or a key set in a form it can't edit | `config_key_unknown`, `config_value_invalid` (exit 2), `config_uneditable` | 1 or 2 |

The message starts with the path of the file at fault, for example:

```
ws: /Users/me/Library/Application Support/workspaces/config.toml: invalid config: unknown keys defaults.protocol
```

With `--json`, the same text is in `error.message`
([0016](adrs/0016-output-contract.md)).
