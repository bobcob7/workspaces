# ws machine memory get

```
ws machine memory get <KEY>
```

Prints one key's value from your loop's memory. The value is in `entry.value`
of the JSON.

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"loop": "deps-bump", "entry": {"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}, "warnings": []}
```

## Exit codes

| Situation                                                | Error code          | Exit |
| -------------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                                  | `outside_workspace` | 2    |
| Not exactly one key, or a bad key                        | `usage` or `memory_invalid` | 2 |
| Your workspace isn't a loop run, or its loop was removed | `not_loop_run`      | 4    |
| The key isn't set                                        | `memory_not_found`  | 3    |

## Examples

```console
$ ws machine memory get seen
```
