# ws machine memory

```
ws machine memory list
ws machine memory get <KEY>
ws machine memory set [--expect-rev <N>] [--file <PATH> | --file -] <KEY> [<VALUE>]
ws machine memory rm [--expect-rev <N>] <KEY>
```

Reads and changes the memory of your loop: the values that the loop's runs
keep from one run to the next. Your loop is the one your run belongs to, found
from your root workspace, so children of a run share its loop's memory. Use it
to keep notes for the loop's next runs, such as which items a run has already
handled.

Output is always JSON; `--json` changes nothing. Errors are one JSON line on
stderr.

## Subcommands

- `list`: every key of your loop's memory.
- `get`: one key's value.
- `set`: store a value under a key, from `<VALUE>` or `--file`.
- `rm`: remove one key.

There is no `clear`. Keys are 1 to 64 characters of `A-Z`, `a-z`, `0-9`, and
`_`, and don't start with a digit. A loop keeps at most 100 keys, 64 KiB per
value, and 256 KiB in all.

`--expect-rev` makes a `set` or `rm` a compare-and-swap, for when runs overlap.
Without it, the last writer wins for that key.

## Exit codes

| Situation                                                    | Error code         | Exit |
| ------------------------------------------------------------ | ------------------ | ---- |
| Run outside a workspace                                      | `outside_workspace` | 2   |
| A bad key, or wrong arguments or flags                       | `memory_invalid` or `usage` | 2 |
| Your workspace isn't a loop run, or its loop was removed     | `not_loop_run`     | 4    |
| A key that isn't set, for `get` or `rm`                      | `memory_not_found` | 3    |
| `--expect-rev` didn't match the key's revision               | `memory_conflict`  | 4    |
| A value over 64 KiB, a 101st key, or over 256 KiB in all     | `memory_full`      | 4    |

## Examples

```console
$ ws machine memory set seen "#412 #415"
$ ws machine memory set --expect-rev 3 seen "#412 #415 #418"
$ ws machine memory get seen
$ ws machine memory list
$ ws machine memory rm seen
```
