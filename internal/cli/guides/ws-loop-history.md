# ws loop history

```
ws loop history [--json] [--limit <N>] [--run <SEQ> | --clear] <NAME>
```

Shows a loop's runs, newest first. Each attempt to start the loop is one run:
a run that started, a run whose script skipped it, or a run that couldn't
start. A run's verdict is the one it recorded with `ws machine result`. A
running run is checked each time `ws loop tick` runs, so its status can lag by
one tick. `ws loop history` only reads the history; it doesn't check runs.

## Statuses

| Status         | Meaning |
| -------------- | ------- |
| `running`      | the agent is working or starting |
| `needs_attention` | the agent is idle and recorded no verdict; the run is shown and notified until it records a verdict or its workspace is removed |
| `blocked`      | the agent recorded `BLOCKED` to ask a person for help; its `verdict` is `BLOCKED` and outcome `blocked`, until a later verdict replaces it |
| `done`         | the agent stopped and recorded no verdict, or the run needed attention when its workspace was removed |
| `succeeded`    | the verdict passed (`PASS` or `APPROVED`) |
| `failed`       | the verdict failed |
| `start_failed` | the run couldn't start; `error` says why |
| `skipped`      | the script exited non-zero; `repeats` counts consecutive skips with the same exit code and message |
| `gone`         | the workspace was removed while the run was `running` |

`trigger` is what started the run: `tick`, `run`, `run_force` (`--force`), or
`imported` for a run from before history was kept.

## Options

| Option    | Description |
| --------- | ----------- |
| `--limit` | how many runs to show, 1 to 200 (default 20) |
| `--run`   | show one run, with its saved report |
| `--clear` | delete the loop's run history and saved reports |

`--run` and `--clear` don't take `--limit`, and `--clear` doesn't take
`--run`. The history keeps the newest 200 runs; a saved report is kept for the
newest 50 runs that have a verdict.

`ws loop history` is for people and the desktop app. Agents don't run it.

## Text output

One line per run: sequence, status, started at, duration, workspace, and the
verdict, the error, or the gate's exit code and message.

```
42  succeeded     2026-11-10T08:00:00Z  14m30s  loop-deps-bump-1a2b3c4d  PASS
41  skipped ×12   2026-11-09T20:00:00Z  -       -                        exit 1: no outdated modules
40  start_failed  2026-11-09T08:00:00Z  -       -                        claude_missing: …
```

`--run` prints the run's line, then a blank line and its report.

## JSON output

A list:

```json
{"loop": "deps-bump", "runs": [{"seq": 42, "status": "succeeded", "trigger": "tick", "started_at": "2026-11-10T08:00:00Z", "finished_at": "2026-11-10T08:14:30Z", "duration_seconds": 870, "workspace": "loop-deps-bump-1a2b3c4d", "workspace_exists": true, "session": "e8cdf5d8", "error": null, "gate": null, "repeats": 1, "verdict": {"verdict": "PASS", "outcome": "pass", "recorded_at": "2026-11-10T08:14:30Z", "report": "/…/state/.loops/deps-bump/reports/42.md"}}], "total": 57, "warnings": []}
```

With `--run`, `run` holds one run of the same shape, and `report_text` is the
report, or `null` when there is none:

```json
{"loop": "deps-bump", "run": {"seq": 42}, "report_text": "# Bumped 3 modules\n", "warnings": []}
```

With `--clear`, `removed` is how many runs were deleted:

```json
{"loop": "deps-bump", "removed": 57, "warnings": []}
```

A run's `duration_seconds` is set only for `done`, `succeeded`, `failed`,
`gone`, and `blocked` runs that finished. `workspace_exists` is `false` once the workspace is
removed.

## Exit codes

| Situation                                                  | Error code       | Exit |
| ---------------------------------------------------------- | ---------------- | ---- |
| `--limit` out of range, `--run` below 1, or flags that can't be used together | `usage` | 2 |
| A history file from a newer ws                             | `loop_invalid`   | 2 |
| No loop has that name, or no run has that sequence number  | `loop_not_found` | 3 |

## Examples

```console
$ ws loop history deps-bump
$ ws loop history --limit 5 deps-bump
$ ws loop history --run 42 deps-bump
$ ws loop history --clear deps-bump
```
