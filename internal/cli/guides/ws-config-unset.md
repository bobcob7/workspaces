# ws config unset

```
ws config unset [--json] <key>
```

Removes the key from `config.toml`, so its default applies. Removing a key
that isn't set succeeds with `changed` false. Once a
`[review.profiles.<name>]` table has no keys left, its header is removed too.
Only the key's own lines change, so the file's comments stay.

## When to use it

To go back to a default, or to remove a review profile's overrides after it
has been removed. `unset` accepts any profile name, so it works for a profile
that no longer exists.

## JSON output

```json
{"path": "/…/config.toml",
 "setting": {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
 "changed": true, "warnings": []}
```

## Exit codes

| Situation                                               | Error code           | Exit |
| ------------------------------------------------------- | -------------------- | ---- |
| Not exactly one key                                     | `usage`              | 2    |
| Not one of the keys in `ws llm config`                  | `config_key_unknown` | 2    |
| `config.toml` doesn't parse, or breaks a rule           | `config_invalid`     | 1    |
| The file sets the key in a form `ws config` can't edit  | `config_uneditable`  | 1    |
| The file can't be read, locked, or written              | `internal`           | 1    |

## Examples

```console
$ ws config unset review.timeout
review.timeout = "1h"  # default

$ ws config --json unset review.timeout
```
