# Configuration

Where `ws` keeps its files, what `config.toml` may contain, and how both are
found and checked. The decisions behind this are in
[0011](adrs/0011-config-dir.md) (locations),
[0007](adrs/0007-repo-spec-syntax-and-resolution.md) (what the keys do), and
[0019](adrs/0019-https-upstream-urls.md) (why there's no protocol key).

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
  review/profiles/     your review profiles, <name>.md (0038)
```

What `ws` keeps under `state/` and `reviews/`, and when, is described in
[Workspace state](state.md). `review/profiles/` is yours: `ws` reads it
and never writes it; [`ws review`](tools/ws-review.md#ws-review-prime)
describes the files.

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
# ws configuration. ws writes this file once and never overwrites it.

[defaults]
# Host for repo specs without one, such as "owner/name".
host = "github.com"

# Owners to try, in order, for a bare repo name that isn't cached,
# for example ["your-name", "your-org"].
owners = []
```

- **It's never overwritten.** Edit it freely; `ws` only reads it from then
  on. To get the default back, delete it.
- **It appears whole or not at all.** `ws` writes a temporary file in
  `CONFIG_DIR` and links it into place, so two `ws` commands starting at once
  can't leave a partial file, and neither replaces a file that's already
  there.

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

The message starts with the path of the file at fault, for example:

```
ws: /Users/me/Library/Application Support/workspaces/config.toml: invalid config: unknown keys defaults.protocol
```

With `--json`, the same text is in `error.message`
([0016](adrs/0016-output-contract.md)).
