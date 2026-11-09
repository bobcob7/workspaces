# ws config

```
ws config list [--json]
ws config get [--json] <key>
ws config set [--json] <key> <value>...
ws config unset [--json] <key>
```

Lists, reads, and changes the settings in `config.toml`, keeping the file's
comments. Each key does what the [configuration](../config.md#schema)
page says. Every command reads the file first, so a file that doesn't parse
fails with `config_invalid` and has to be fixed by hand.

`ws config` is for people and the desktop app. Agents don't run it: tell a
person what you need.

## When to use it

- `ws config list` to see every setting, its value, and whether the file sets
  it or leaves its default.
- `ws config set` and `ws config unset` to change a setting without losing
  the file's comments.
- `ws config get` to read one value in a script.

Run `ws llm config <op>` for each one's guide.

## Keys

| Key                               | Type            | Default               | `set` takes                                              |
| --------------------------------- | --------------- | --------------------- | -------------------------------------------------------- |
| `defaults.host`                   | string          | `"github.com"`        | one bare host name                                       |
| `defaults.owners`                 | list of strings | `[]`                  | zero or more owners, in order (none empties the list)    |
| `review.timeout`                  | duration        | `"1h"`                | one Go duration greater than zero, such as `90m` or `2h` |
| `review.remove_finished`          | bool            | `true`                | `true` or `false`                                        |
| `review.profiles.<name>.when`     | string          | `""` (the profile's own) | `always`, `auto`, or `off`                            |
| `review.profiles.<name>.model`    | string          | `""` (the session's)  | one model alias or id without spaces, or `""`            |

`<name>` is a review profile's name. `set` refuses a name that
`ws review profiles` doesn't list. `unset` accepts any name, so you can clean
up after removing a profile.

Values are stored the way `ws` reads them: host and owners are lower-cased,
repeated owners are dropped, and a duration is written in short form, so
`90m` is stored as `"1h30m"`.

## How the file is changed

Only the key's own lines change: the `key = value` line, or every line of a
multi-line array. Every other line stays byte for byte, including comments.
A key that isn't in the file is added after the last key of its table, and a
missing table is added at the end of the file. A file that sets the key in a
form these commands can't edit, such as a dotted key or an inline table, is
left unchanged and fails with `config_uneditable`.

## JSON output

`--json` prints one object per command. See each subcommand's guide.

## Exit codes

| Situation                                                            | Error code             | Exit |
| -------------------------------------------------------------------- | ---------------------- | ---- |
| Wrong number of values for the key, or wrong arguments               | `usage`                | 2    |
| Not one of the keys above, or a bad profile name in the key          | `config_key_unknown`   | 2    |
| A value that breaks the key's rule                                   | `config_value_invalid` | 2    |
| `set` of a `review.profiles.<name>` key for a profile that doesn't exist | `profile_unknown`  | 2    |
| `config.toml` doesn't parse, or breaks a rule, before the change     | `config_invalid`       | 1    |
| The file sets the key in a form `ws config` can't edit               | `config_uneditable`    | 1    |
| The file can't be read, locked, or written                           | `internal`             | 1    |

## Examples

```console
$ ws config list
defaults.host = "github.com"  # default
defaults.owners = ["bobcob7", "acme"]
review.timeout = "1h"  # default
review.remove_finished = true  # default
review.profiles.security.when = ""  # default
review.profiles.security.model = "opus"

$ ws config set defaults.owners platform platform/tools me
defaults.owners = ["platform", "platform/tools", "me"]

$ ws config unset review.timeout
review.timeout = "1h"  # default
```
