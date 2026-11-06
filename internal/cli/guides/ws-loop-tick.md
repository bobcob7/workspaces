# ws loop tick

```
ws loop tick [--json] [--dry-run]
ws loop tick [--dry-run] --watch
```

Runs one pass of the scheduler: starts a run of every loop that is due,
records each run, and removes old runs' workspaces beyond each loop's
`keep`. Exits 0 when nothing was due.

Nothing in `ws` runs a pass by itself. Something must run `ws loop tick`:
a launchd agent (every 60 seconds), the desktop app while it's open, or
`--watch` in a terminal. `ws loop tick` is for those, not for agents.

A loop is due when it isn't paused and its `next_run_at` has passed. A due
loop is left for a later pass, and its `next_run_at` stays as it is, when:

- **asleep**: the Mac isn't fully awake, as on a macOS dark wake. Nothing
  starts; the first tick after a real wake starts it.
- **busy**: its newest run's agent is still working or starting. It starts
  on the first tick after that agent finishes.

A run that can't start is `failed`: its error is in the item, and
`next_run_at` still moves on, so the loop isn't retried until its next
period.

## Options

| Option      | Description |
| ----------- | ----------- |
| `--dry-run` | Report what would start, as `would_start`; start, write, and remove nothing |
| `--watch`   | Run a pass every minute until interrupted, printing only the runs it started and the failures it had. Text only: `--json` with `--watch` is a usage error |

## JSON output

```json
{
  "loops": [
    {"name": "deps-bump", "action": "started", "workspace": "loop-deps-bump-1a2b3c4d",
     "session": "e8cdf5d8", "next_run_at": "2026-11-06T18:00:00Z", "error": null},
    {"name": "triage", "action": "waiting", "workspace": null, "session": null,
     "next_run_at": "2026-11-05T19:00:00Z", "error": null}
  ],
  "warnings": []
}
```

Each loop's `action` is one of:

| `action`      | Meaning |
| ------------- | ------- |
| `started`     | A run started; `workspace` and `session` are the new run's |
| `would_start` | `--dry-run`: it's due and would start |
| `waiting`     | Not due yet |
| `paused`      | Paused; `next_run_at` is `null` |
| `busy`        | Due, but its newest run is still working |
| `asleep`      | Due, but the Mac isn't fully awake |
| `failed`      | Due, but the run couldn't start; `error` says why |

`loops` is `[]` when there are no loops. Some items failing doesn't stop
the others; the exit code is the first failure's.

## Text output

One line per loop, with `--watch` printing the started and failed ones:

```console
$ ws loop tick
started  deps-bump  loop-deps-bump-1a2b3c4d  next 2026-11-06T18:00:00Z
waiting  triage     -                        next 2026-11-05T19:00:00Z
```

## Exit codes

| Situation                                        | Error code | Exit |
| ------------------------------------------------ | ---------- | ---- |
| Arguments given, or a flag error                 | `usage`    | 2    |
| `--json` with `--watch`                          | `usage`    | 2    |
| A due run couldn't start                         | `ws claude`'s code, in its item | 1–4 |

## Examples

```console
# What would start now, without starting anything
$ ws loop tick --dry-run --json

# Run the scheduler in a terminal
$ ws loop tick --watch
```
