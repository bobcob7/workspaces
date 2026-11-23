# ws machine progress set

```
ws machine progress set [--name <name>] [--total <n>] [--done <n>] [--note <text>]
```

Sets your task's short name, step count, finished steps, or note. Give at
least one flag; flags you leave out keep their value. An empty `--name` or
`--note` clears it. Prints your progress and the tree below you, as
`ws machine progress show` does.

## Options

| Option    | Description |
| --------- | ----------- |
| `--name`  | a short name, up to 40 characters, one line |
| `--total` | how many steps the task has; change it as the plan changes |
| `--done`  | how many steps are finished; at most `--total` |
| `--note`  | what you are doing now, up to 200 characters, one line |

## JSON output

Output is always JSON; `--json` changes nothing. Only your own record and
rollup are printed; `ws machine progress show` adds the descendants' `tree`:

```json
{"id": "plan-1a2b3c4d",
 "progress": {"name": "fix login", "total": 3, "done": 1, "note": null, "updated_at": "2026-11-22T20:00:00Z"},
 "rollup": {"total": 3, "done": 1, "fraction": 0.3333}, "warnings": []}
```

## Exit codes

| Situation                                      | Error code          | Exit |
| ---------------------------------------------- | ------------------- | ---- |
| Run outside a workspace                        | `outside_workspace` | 2    |
| A bad name or note, or `done` over `total`     | `progress_invalid`  | 2    |
| No flag given                                  | `usage`             | 2    |

## Examples

```console
$ ws machine progress set --name "fix login" --total 3
$ ws machine progress set --done 3 --note ""
```
