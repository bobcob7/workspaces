# ws loop rm

```
ws loop rm [--json] <NAME>
```

Deletes the loop, and prints it as it was. Its runs' workspaces stay until
they're removed as any workspace is, with `ws rm`.

`ws loop rm` is for people and the desktop app. Agents don't run it.

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
$ ws loop rm deps-bump
deps-bump  every 24h  active  next 2026-11-06T18:00:00Z  last -
$ ws loop rm --json deps-bump
```
