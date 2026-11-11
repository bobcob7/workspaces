# ws machine memory set

```
ws machine memory set [--expect-rev <N>] [--file <PATH> | --file -] <KEY> [<VALUE>]
```

Stores a value under a key of your loop's memory. Give the value as `<VALUE>`,
or with `--file` to read it from a file, or from stdin with `--file -`. Give
exactly one of them. The key's revision goes up by one, and `updated_by` is
your workspace.

Output is always JSON; `--json` changes nothing.

## Options

| Option          | Description |
| --------------- | ----------- |
| `--expect-rev`  | fail with `memory_conflict` unless the key's revision is N; 0 means the key must not exist (default: don't check) |
| `--file`        | read the value from this file; `-` reads stdin |

## JSON output

```json
{"loop": "deps-bump", "entry": {"key": "seen", "value": "#412 #415", "rev": 4, "updated_at": "2026-11-10T08:20:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}, "warnings": []}
```

## Exit codes

| Situation                                                     | Error code          | Exit |
| ------------------------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                                       | `outside_workspace` | 2    |
| A bad key                                                     | `memory_invalid`    | 2    |
| Both a value and `--file`, or neither; a file that can't be read; a flag error | `usage` | 2 |
| Your workspace isn't a loop run, or its loop was removed      | `not_loop_run`      | 4    |
| `--expect-rev` didn't match the key's revision                | `memory_conflict`   | 4    |
| A value over 64 KiB, a 101st key, or over 256 KiB in all      | `memory_full`       | 4    |

## Examples

```console
$ ws machine memory set seen "#412 #415"
$ ws machine memory set --expect-rev 0 seen "#412"
$ ws machine memory set --file ../seen.txt seen
```
