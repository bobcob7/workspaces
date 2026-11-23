# ws machine progress step

```
ws machine progress step [--by <n>] [--note <text>]
```

Finishes steps: adds `--by` (default 1) to your done count. It fails with
`progress_invalid` if that would pass your total; raise it with
`ws machine progress set --total` first. Prints your progress and the tree
below you.

## Options

| Option   | Description |
| -------- | ----------- |
| `--by`   | how many steps finished (default 1, at least 1) |
| `--note` | what you are doing now; empty clears it |

## JSON output

Output is always JSON; `--json` changes nothing. The same object as `ws machine progress show`:

```json
{"id": "plan-1a2b3c4d", "progress": {"name": "fix login", "total": 3, "done": 1, "note": null, "updated_at": "2026-11-22T20:00:00Z"},
 "tree": {"id": "plan-1a2b3c4d", "progress": {"name": "fix login", "total": 3, "done": 1, "note": null, "updated_at": "2026-11-22T20:00:00Z"},
          "rollup": {"total": 3, "done": 1, "fraction": 0.3333}, "children": []}, "warnings": []}
```

## Exit codes

| Situation                                | Error code          | Exit |
| ---------------------------------------- | ------------------- | ---- |
| Run outside a workspace                  | `outside_workspace` | 2    |
| Done would pass total                    | `progress_invalid`  | 2    |
| `--by` under 1                           | `usage`             | 2    |

## Examples

```console
$ ws machine progress step
$ ws machine progress step --by 2 --note "reviews back"
```
