# ws config get

```
ws config get [--json] <key>
```

Prints one setting's value. Without `--json` the value is bare: a list prints
one item per line, a bool prints `true` or `false`, and an empty string prints
an empty line.

## When to use it

To read one setting in a script, for example `ws config get defaults.host`.

## JSON output

```json
{"path": "/…/config.toml",
 "setting": {"key": "review.timeout", "type": "duration", "value": "1h", "default": "1h", "set": false},
 "warnings": []}
```

A well-formed profile key whose table isn't in the file has `set: false` and
the default value.

## Exit codes

| Situation                                | Error code           | Exit |
| ---------------------------------------- | -------------------- | ---- |
| Not exactly one key                      | `usage`              | 2    |
| Not one of the keys in `ws llm config`   | `config_key_unknown` | 2    |
| `config.toml` doesn't parse              | `config_invalid`     | 1    |
| The file can't be read                   | `internal`           | 1    |

## Examples

```console
$ ws config get defaults.owners
bobcob7
acme

$ ws config --json get review.timeout
```
