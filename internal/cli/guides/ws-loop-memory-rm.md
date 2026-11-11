# ws loop memory rm

```
ws loop memory rm [--json] [--expect-rev <N>] <NAME> <KEY>
```

Removes one key from a loop's memory.

Output is text by default, or JSON with `--json`.

## Options

| Option          | Description |
| --------------- | ----------- |
| `--expect-rev`  | fail with `memory_conflict` unless the key's revision is N (default: don't check) |

## JSON output

```json
{"loop": "deps-bump", "key": "seen", "removed": true, "warnings": []}
```

## Exit codes

| Situation                          | Error code         | Exit |
| ---------------------------------- | ------------------ | ---- |
| Not exactly a loop name and a key  | `usage`            | 2    |
| A bad key                          | `memory_invalid`   | 2    |
| No loop has that name              | `loop_not_found`   | 3    |
| The key isn't set                  | `memory_not_found` | 3    |
| `--expect-rev` didn't match        | `memory_conflict`  | 4    |

## Examples

```console
$ ws loop memory rm deps-bump seen
$ ws loop memory rm --expect-rev 3 deps-bump seen
```
