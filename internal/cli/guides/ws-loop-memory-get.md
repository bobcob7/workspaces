# ws loop memory get

```
ws loop memory get [--json] <NAME> <KEY>
```

Prints one key's value. Text output is the value exactly as stored, with no
trailing newline added.

Output is text by default, or JSON with `--json`.

## JSON output

```json
{"loop": "deps-bump", "entry": {"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": null}, "warnings": []}
```

## Exit codes

| Situation                        | Error code         | Exit |
| -------------------------------- | ------------------ | ---- |
| Not exactly a loop name and a key | `usage`           | 2    |
| A bad key                        | `memory_invalid`   | 2    |
| No loop has that name            | `loop_not_found`   | 3    |
| The key isn't set               | `memory_not_found` | 3    |

## Examples

```console
$ ws loop memory get deps-bump seen
$ ws loop memory get --json deps-bump seen
```
