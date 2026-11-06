# ws loop run

```
ws loop run [--json] [--force] <NAME>
```

Starts a run of the loop now, in a new workspace, whether or not the loop
is paused, due, or busy. The loop's `next_run_at` and its pause state stay
as they are. The run is recorded with the loop, as a tick's run is, and
prints `ws claude`'s result with the loop's name.

If the loop has a script, it runs first, as it does for a tick. A script
that exits non-zero starts nothing, and the command fails with
`loop_skipped`. A script that times out or can't start fails with
`loop_script_failed`. Either way the script's outcome is saved as the
loop's `last_gate`.

## Options

| Option    | Description |
| --------- | ----------- |
| `--force` | Don't run the loop's script; `{{.Stdout}}` and `{{.Stderr}}` are empty, and `last_gate` is unchanged |

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
| The script exited non-zero                         | `loop_skipped`   | 4    |
| The script timed out or couldn't start             | `loop_script_failed` | 1 |
| The prompt couldn't be rendered                    | `loop_invalid`   | 2    |

## Examples

```console
$ ws loop run deps-bump
loop-deps-bump-1a2b3c4d  e8cdf5d8  /…/workspaces/loop-deps-bump-1a2b3c4d
$ ws loop run --json deps-bump
```
