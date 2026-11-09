# ws config list

```
ws config list [--json]
```

Prints every setting in `config.toml`, in the order of the keys table, with
its value and default. A `when` and a `model` row is listed for each review
profile table in the file, sorted by name.

## When to use it

To see the effective configuration, or which settings the file sets. A
setting marked `# default` isn't in the file.

## JSON output

```json
{"path": "/…/config.toml",
 "settings": [
   {"key": "defaults.host", "type": "string", "value": "github.com", "default": "github.com", "set": true},
   {"key": "defaults.owners", "type": "strings", "value": ["bobcob7", "acme"], "default": [], "set": true},
   {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
   {"key": "review.remove_finished", "type": "bool", "value": true, "default": true, "set": false},
   {"key": "review.profiles.security.model", "type": "string", "value": "opus", "default": "", "set": true}
 ],
 "warnings": []}
```

- `type` is `string`, `strings`, `duration`, or `bool`. A `duration` is a string.
- `value` is what the file sets, or else the default.
- `set` is true when the file sets the key, even to the default.

## Exit codes

| Situation                                | Error code     | Exit |
| ---------------------------------------- | -------------- | ---- |
| Arguments given                          | `usage`        | 2    |
| `config.toml` doesn't parse              | `config_invalid` | 1  |
| The file can't be read                   | `internal`     | 1    |

## Examples

```console
$ ws config list
$ ws config --json list
```
