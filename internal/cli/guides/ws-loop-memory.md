# ws loop memory

```
ws loop memory list  [--json] <NAME>
ws loop memory get   [--json] <NAME> <KEY>
ws loop memory set   [--json] [--expect-rev <N>] [--file <PATH> | --file -] <NAME> <KEY> [<VALUE>]
ws loop memory rm    [--json] [--expect-rev <N>] <NAME> <KEY>
ws loop memory clear [--json] <NAME>
```

Reads and changes a loop's memory: the values its runs keep from one run to
the next. A prompt reads a value with `{{memory "KEY"}}`, and a gate script
reads the file at `$WS_LOOP_MEMORY`. Runs change it with `ws machine memory`.
A person can use these commands to add, correct, or remove what the runs
remember.

`ws loop memory` is for people and the desktop app. Agents use
`ws machine memory` instead.

## Subcommands

- `list`: every key, with its value's revision and last change.
- `get`: one key's value, printed exactly as stored.
- `set`: store a value under a key. Give the value as an argument, or with
  `--file`, never both.
- `rm`: remove one key.
- `clear`: remove every key.

Keys are 1 to 64 characters of `A-Z`, `a-z`, `0-9`, and `_`, and don't start
with a digit. A loop keeps at most 100 keys, 64 KiB per value, and 256 KiB in
all.

## Options

| Option          | Description |
| --------------- | ----------- |
| `--expect-rev`  | fail with `memory_conflict` unless the key's revision is N; 0 means the key must not exist (default: don't check) |
| `--file`        | read the value from this file; `-` reads stdin |

`--expect-rev` makes a `set` or `rm` a compare-and-swap, which keeps two
overlapping writers from overwriting each other. Without it, the last writer
wins.

## Exit codes

| Situation                                                    | Error code       | Exit |
| ------------------------------------------------------------ | ---------------- | ---- |
| A bad key                                                    | `memory_invalid` | 2    |
| A data file from a newer ws                                  | `loop_invalid`   | 2    |
| `set` with both a value and `--file`, or with neither, or a flag error | `usage` | 2 |
| No loop has that name                                        | `loop_not_found` | 3    |
| `get` or `rm` of a key that isn't set                        | `memory_not_found` | 3  |
| `--expect-rev` didn't match the key's revision               | `memory_conflict` | 4   |
| A value over 64 KiB, a 101st key, or over 256 KiB in all     | `memory_full`    | 4    |

## Examples

```console
$ ws loop memory set deps-bump seen "#412"
$ ws loop memory set --expect-rev 1 deps-bump seen "#412 #415"
$ ws loop memory get deps-bump seen
$ ws loop memory list deps-bump
$ ws loop memory rm deps-bump seen
$ ws loop memory clear deps-bump
```
