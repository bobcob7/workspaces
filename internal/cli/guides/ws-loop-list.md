# ws loop list

```
ws loop list [--json]
```

Lists every loop, by name. A loop file that can't be read, or that is from a
newer `ws`, is a warning and isn't listed.

`ws loop list` is for people and the desktop app. Agents don't run it.

## JSON output

```json
{"loops": [{"name": "deps-bump", "every": "24h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": false, "hidden": true, "created_at": "2026-11-05T18:00:00Z", "next_run_at": "2026-11-06T18:00:00Z", "last_run": null, "script": null, "script_timeout": null, "vars": {}, "last_gate": null}], "warnings": []}
```

`hidden` is `true` when each run's workspace starts hidden, the default, and
`false` for a loop added with `--show`.

## Exit codes

| Situation           | Error code | Exit |
| ------------------- | ---------- | ---- |
| Arguments were given | `usage`   | 2    |

## Examples

```console
$ ws loop list
deps-bump  every 24h  active  next 2026-11-06T18:00:00Z  last -
triage     every 1h   paused  next -                     last -
$ ws loop list --json
```
