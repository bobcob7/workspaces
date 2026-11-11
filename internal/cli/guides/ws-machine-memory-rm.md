# ws machine memory rm

```
ws machine memory rm [--expect-rev <N>] <KEY>
```

Removes one key from your loop's memory.

Output is always JSON; `--json` changes nothing.

## Options

| Option          | Description |
| --------------- | ----------- |
| `--expect-rev`  | fail with `memory_conflict` unless the key's revision is N (default: don't check) |

## JSON output

```json
{"loop": "deps-bump", "key": "seen", "removed": true, "warnings": []}
```

## Exit codes

| Situation                                                | Error code          | Exit |
| -------------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                                  | `outside_workspace` | 2    |
| Not exactly one key, or a bad key                        | `usage` or `memory_invalid` | 2 |
| Your workspace isn't a loop run, or its loop was removed | `not_loop_run`      | 4    |
| The key isn't set                                        | `memory_not_found`  | 3    |
| `--expect-rev` didn't match the key's revision           | `memory_conflict`   | 4    |

## Examples

```console
$ ws machine memory rm seen
$ ws machine memory rm --expect-rev 3 seen
```
