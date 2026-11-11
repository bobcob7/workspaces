# ws machine memory list

```
ws machine memory list
```

Lists every key of your loop's memory, sorted by key. Your loop is the one
your run belongs to; see `ws llm machine memory`.

Output is always JSON; `--json` changes nothing.

## JSON output

```json
{"loop": "deps-bump", "entries": [{"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}], "bytes": 9, "warnings": []}
```

`updated_by` is the workspace that made the last change.

## Exit codes

| Situation                                | Error code          | Exit |
| ---------------------------------------- | ------------------- | ---- |
| Run outside a workspace                  | `outside_workspace` | 2    |
| Arguments given                          | `usage`             | 2    |
| Your workspace isn't a loop run, or its loop was removed | `not_loop_run` | 4 |

## Examples

```console
$ ws machine memory list
```
