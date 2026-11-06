# ws loop pause

```
ws loop pause [--json] <NAME>
```

Stops a loop from starting runs. A run already started keeps going. Pausing
a paused loop changes nothing.

`ws loop pause` is for people and the desktop app. Agents don't run it.

## JSON output

```json
{"name": "deps-bump", "every": "24h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": true, "created_at": "2026-11-05T18:00:00Z", "next_run_at": null, "last_run": null, "warnings": []}
```

`next_run_at` is `null` while the loop is paused.

## Exit codes

| Situation                | Error code       | Exit |
| ------------------------ | ---------------- | ---- |
| Not exactly one name     | `usage`          | 2    |
| No loop has that name    | `loop_not_found` | 3    |

## Examples

```console
$ ws loop pause deps-bump
deps-bump  every 24h  paused  next -  last -
$ ws loop pause --json deps-bump
```
