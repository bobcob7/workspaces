# ws machine progress show

```
ws machine progress show
```

Prints your own progress and the tree of your descendants. Workspaces that
haven't reported appear with null fields and pass their children's steps up.

Output is always JSON; `--json` changes nothing.

## JSON output

The same `id`, `progress`, `rollup` and `warnings` as `set` and `step` print, plus the `tree`:

```json
{"id": "plan-1a2b3c4d",
 "progress": {"name": "build auth", "total": 4, "done": 1, "note": "waiting on t1", "updated_at": "2026-11-22T20:00:00Z"},
 "rollup": {"total": 7, "done": 3, "fraction": 0.4286},
 "tree": {"id": "plan-1a2b3c4d",
          "progress": {"name": "build auth", "total": 4, "done": 1, "note": "waiting on t1", "updated_at": "2026-11-22T20:00:00Z"},
          "rollup": {"total": 7, "done": 3, "fraction": 0.4286},
          "children": [{"id": "t1-impl-d8efb24f", "progress": {"name": "login form", "total": 3, "done": 2, "note": null, "updated_at": "2026-11-22T20:01:00Z"},
                        "rollup": {"total": 3, "done": 2, "fraction": 0.6667}, "children": []}]},
 "warnings": []}
```

`rollup` is that workspace's steps plus everything below it.

## Remote children

A child you started on another environment (`ws machine spawn --env`) is
shown as a leaf under its parent, with the progress and rollup its environment
last reported. A child whose environment can't be asked has no progress.

Its `rollup` counts in its ancestors' rollups too. An offline environment's last
snapshot counts, and a removed child counts nothing.

## Exit codes

| Situation               | Error code          | Exit |
| ----------------------- | ------------------- | ---- |
| Run outside a workspace | `outside_workspace` | 2    |

## Examples

```console
$ ws machine progress show
```
