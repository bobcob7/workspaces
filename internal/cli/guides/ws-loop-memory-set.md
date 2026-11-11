# ws loop memory set

```
ws loop memory set [--json] [--expect-rev <N>] [--file <PATH> | --file -] <NAME> <KEY> [<VALUE>]
```

Stores a value under a key of a loop's memory, and prints the key's new
revision. Give the value as `<VALUE>`, or with `--file` to read it from a file,
or from stdin with `--file -`. Give exactly one of them. The revision goes up by
one on every set.

Output is text by default, or JSON with `--json`.

## Options

| Option          | Description |
| --------------- | ----------- |
| `--expect-rev`  | fail with `memory_conflict` unless the key's revision is N; 0 means the key must not exist (default: don't check) |
| `--file`        | read the value from this file; `-` reads stdin |

## JSON output

```json
{"loop": "deps-bump", "entry": {"key": "seen", "value": "#412 #415", "rev": 4, "updated_at": "2026-11-10T08:20:00Z", "updated_by": null}, "warnings": []}
```

## Exit codes

| Situation                                                     | Error code        | Exit |
| ------------------------------------------------------------- | ----------------- | ---- |
| A bad key                                                     | `memory_invalid`  | 2    |
| A data file from a newer ws                                   | `loop_invalid`    | 2    |
| Both a value and `--file`, or neither; a file that can't be read; a flag error | `usage` | 2 |
| No loop has that name                                         | `loop_not_found`  | 3    |
| `--expect-rev` didn't match the key's revision                | `memory_conflict` | 4    |
| A value over 64 KiB, a 101st key, or over 256 KiB in all      | `memory_full`     | 4    |

## Examples

```console
$ ws loop memory set deps-bump seen "#412"
$ ws loop memory set --expect-rev 0 deps-bump seen "#412"
$ ws loop memory set --file notes.txt deps-bump notes
$ cat notes.txt | ws loop memory set --file - deps-bump notes
```
