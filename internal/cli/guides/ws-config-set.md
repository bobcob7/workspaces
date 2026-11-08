# ws config set

```
ws config set [--json] <key> <value>...
```

Checks the value, writes it into `config.toml`, and prints the key as
`ws config list` does. Only the key's own lines change, so the file's comments
stay. `defaults.owners` takes any number of values, and none empties the list.
Every other key takes exactly one value. Use `--` before a value that starts
with `-`.

## When to use it

To change a setting. Setting a value the file already has changes nothing.

## JSON output

```json
{"path": "/…/config.toml",
 "setting": {"key": "review.profiles.security.model", "type": "string", "value": "opus", "default": "", "set": true},
 "changed": true, "warnings": []}
```

`changed` is false when the file already said exactly that.

## Exit codes

| Situation                                                  | Error code             | Exit |
| ---------------------------------------------------------- | ---------------------- | ---- |
| Wrong number of values for the key                         | `usage`                | 2    |
| Not one of the keys in `ws llm config`                     | `config_key_unknown`   | 2    |
| A value that breaks the key's rule                         | `config_value_invalid` | 2    |
| A `review.profiles.<name>` key for a profile that doesn't exist | `profile_unknown` | 2    |
| `config.toml` doesn't parse, or breaks a rule, before the change | `config_invalid` | 1  |
| The file sets the key in a form `ws config` can't edit     | `config_uneditable`    | 1    |
| The file can't be read, locked, or written                 | `internal`             | 1    |

## Examples

```console
$ ws config set defaults.host gitlab.example.com
defaults.host = "gitlab.example.com"

$ ws config set defaults.owners platform platform/tools me
defaults.owners = ["platform", "platform/tools", "me"]

$ ws config set defaults.owners
defaults.owners = []

$ ws config --json set review.profiles.security.model opus
```
