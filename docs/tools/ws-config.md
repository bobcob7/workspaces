# ws config

List, read, and change the settings in `config.toml`, keeping its comments
([0054](../adrs/0054-settings-all.md)). What each key does and the rules for
its values are in [Configuration](../config.md#schema).

## Synopsis

```
ws config list
ws config get <KEY>
ws config set <KEY> <VALUE>...
ws config unset <KEY>
```

`ws config` is for people and the desktop app. Like every command, it
reads `config.toml` first, so a file that doesn't parse fails with
`config_invalid` and has to be fixed by hand. It never runs the removal
pass.

## Keys

| Key | Type | Default | `set` takes |
| --- | ---- | ------- | ----------- |
| `defaults.host` | string | `"github.com"` | one bare host name |
| `defaults.owners` | list of strings | `[]` | zero or more owners, in order (none empties the list) |
| `review.timeout` | duration | `"1h"` | one Go duration greater than zero, such as `90m` or `2h` |
| `review.remove_finished` | bool | `true` | `true` or `false` |
| `review.profiles.<name>.when` | string | `""` (the profile's own) | `always`, `auto`, or `off` |
| `review.profiles.<name>.model` | string | `""` (the session's) | one model alias or id without spaces, or `""` |

`<name>` is a review profile's name: lowercase letters, digits, and `-`.
`set` refuses a name that `ws review profiles` doesn't list. `unset`
accepts any name, so you can clean up after removing a profile.

Values are stored the way `ws` reads them. Host and owners are
lower-cased, repeated owners are dropped (the first is kept), and a
duration is written in short form, so `90m` is stored as `"1h30m"` and
`60m` as `"1h"`.

## Subcommands

### `list`

Every key, in the order of the table above. It includes a `when` and
a `model` row for each `[review.profiles.<name>]` table in the file,
sorted by name.

```console
$ ws config list
defaults.host = "github.com"
defaults.owners = ["bobcob7", "acme"]
review.timeout = "1h"  # default
review.remove_finished = true  # default
review.profiles.security.when = ""  # default
review.profiles.security.model = "opus"
```

```console
$ ws config list --json
{"path": "/Users/me/Library/Application Support/workspaces/config.toml",
 "settings": [
   {"key": "defaults.host", "type": "string", "value": "github.com", "default": "github.com", "set": true},
   {"key": "defaults.owners", "type": "strings", "value": ["bobcob7", "acme"], "default": [], "set": true},
   {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
   {"key": "review.remove_finished", "type": "bool", "value": true, "default": true, "set": false},
   {"key": "review.profiles.security.when", "type": "string", "value": "", "default": "", "set": false},
   {"key": "review.profiles.security.model", "type": "string", "value": "opus", "default": "", "set": true}
 ],
 "warnings": []}
```

- `type` is `string`, `strings`, `duration`, or `bool`. It is the JSON
  type of `value` and `default`, and a `duration` is a string.
- `value` is the effective value: what the file sets, or else the
  default.
- `set` is true when the file sets the key, even when it sets it to the
  default.

### `get`

One key. Without `--json` it prints the bare value: a list prints one
item per line, and an empty string prints an empty line.

```console
$ ws config get defaults.owners
bobcob7
acme

$ ws config get --json review.timeout
{"path": "/…/config.toml",
 "setting": {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
 "warnings": []}
```

A well-formed profile key whose table isn't in the file has `set: false`
and the default value.

### `set`

Checks the value, writes it into `config.toml`, and prints the key as
`list` does. With `--json`, the output is `get`'s plus `"changed"`,
which is false when the file already said exactly that.

```console
$ ws config set defaults.host gitlab.example.com
defaults.host = "gitlab.example.com"

$ ws config set defaults.owners platform platform/tools me
defaults.owners = ["platform", "platform/tools", "me"]

$ ws config set defaults.owners           # empty the list
defaults.owners = []

$ ws config set --json review.profiles.security.model opus
{"path": "/…/config.toml",
 "setting": {"key": "review.profiles.security.model", "type": "string", "value": "opus", "default": "", "set": true},
 "changed": true, "warnings": []}
```

Use `--` before a value that starts with `-`.

### `unset`

Removes the key from `config.toml`, so the default applies. Removing a
key that isn't set succeeds with `"changed": false`. Once a
`[review.profiles.<name>]` table has no keys left, its header is removed
too. The output is `set`'s.

```console
$ ws config unset review.timeout
review.timeout = "1h"  # default
```

## How the file is changed

- **Only the key's lines change.** That's the `key = value` line, or
  every line of a multi-line array. A comment after the value, on the
  same line, is kept. Every other line, comment and blank line stays byte
  for byte, including CRLF line endings.
- A key that isn't in the file is added after the last key of its table.
  If the table isn't there either, `[review]` or
  `[review.profiles.<name>]` is added at the end of the file, after a
  blank line.
- The new text is parsed and checked as at every read, and must decode
  to the value asked for. If it doesn't, nothing is written, and the
  command fails with `config_uneditable`. This happens when the file sets
  the key as a dotted key (`profiles.security.when = …` under
  `[review]`), in an inline table, or in a multi-line string.
- The read, the edit, and the write happen under an exclusive lock on
  `CONFIG_DIR/config.lock`. The new file is written next to the old one
  (next to the target, if `config.toml` is a symlink), synced, given
  the old file's permissions, and renamed into place.

## Errors

| Situation | Error code | Exit |
| --------- | ---------- | ---- |
| Wrong number of values for the key (one for all but `defaults.owners`) | `usage` | 2 |
| Not one of the keys above, or a bad profile name in the key | `config_key_unknown` | 2 |
| A value that breaks the key's rule | `config_value_invalid` | 2 |
| `set` of a `review.profiles.<name>` key for a profile that doesn't exist | `profile_unknown` | 2 |
| `config.toml` doesn't parse, or breaks a rule, before the change | `config_invalid` | 1 |
| The file sets the key in a form `ws config` can't edit | `config_uneditable` | 1 |
| The file can't be read, locked, or written | `internal` | 1 |

## See also

[Configuration](../config.md), [0007](../adrs/0007-repo-spec-syntax-and-resolution.md),
[0039](../adrs/0039-ws-review-command.md), [0054](../adrs/0054-settings-all.md)
