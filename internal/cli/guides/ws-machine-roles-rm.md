# ws machine roles rm

```
ws machine roles rm <name>
```

Deletes the user's role file. Run it only when a person asks you to. For
an `override`, the built-in of that name is back.

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"name": "reviewer", "path": "/…/machine/roles/reviewer.md", "restored": true, "warnings": []}
```

`restored` is true when a built-in of the name is back.

## Exit codes

| Situation                          | Error code     | Exit |
| ---------------------------------- | -------------- | ---- |
| Not exactly one name               | `usage`        | 2    |
| No role has the name               | `role_unknown` | 3    |
| A built-in with no override        | `role_builtin` | 4    |

## Examples

```console
$ ws machine roles rm reviewer
```
