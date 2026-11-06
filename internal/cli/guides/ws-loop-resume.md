# ws loop resume

```
ws loop resume [--json] <NAME>
```

Starts a paused loop again. If its `next_run_at` passed while it was paused,
the next run is one period after now. Resuming a loop that isn't paused
changes nothing.

`ws loop resume` is for people and the desktop app. Agents don't run it.

## JSON output

```json
{"name": "deps-bump", "every": "24h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": false, "created_at": "2026-11-05T18:00:00Z", "next_run_at": "2026-11-06T18:00:00Z", "last_run": null, "warnings": []}
```

## Exit codes

| Situation             | Error code       | Exit |
| --------------------- | ---------------- | ---- |
| Not exactly one name  | `usage`          | 2    |
| No loop has that name | `loop_not_found` | 3    |

## Examples

```console
$ ws loop resume deps-bump
deps-bump  every 24h  active  next 2026-11-06T18:00:00Z  last -
$ ws loop resume --json deps-bump
```
