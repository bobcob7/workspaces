# ws loop run

```
ws loop run [--json] <NAME>
```

Starts a run of the loop now, in a new workspace, whether or not the loop
is paused, due, or busy. The loop's `next_run_at` and its pause state stay
as they are. The run is recorded with the loop, as a tick's run is, and
prints `ws claude`'s result with the loop's name.

`ws loop run` is for people and the desktop app. Agents don't run it.

## JSON output

```json
{"name": "deps-bump", "workspace": "loop-deps-bump-1a2b3c4d", "session": "e8cdf5d8", "path": "/…/workspaces/loop-deps-bump-1a2b3c4d", "warnings": []}
```

If the run can't start, the run is still recorded with its error, and the
command fails with that error's code.

## Exit codes

| Situation                                          | Error code       | Exit |
| -------------------------------------------------- | ---------------- | ---- |
| Not exactly one name                               | `usage`          | 2    |
| No loop has that name                              | `loop_not_found` | 3    |
| The run couldn't start                             | `ws claude`'s code | 1–4 |

## Examples

```console
$ ws loop run deps-bump
loop-deps-bump-1a2b3c4d  e8cdf5d8  /…/workspaces/loop-deps-bump-1a2b3c4d
$ ws loop run --json deps-bump
```
