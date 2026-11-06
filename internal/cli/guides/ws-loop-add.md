# ws loop add

```
ws loop add [--json] --every <PERIOD> --prompt <TEXT> [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] <NAME> [<SPEC>...]
```

Adds a loop. Its first run starts one period after it's added; `ws loop run`
starts one at once. Specs are checked now and resolved at each run, so a run
fetches its repos as `ws claude` does. Without specs, each run gets an empty
workspace.

`ws loop add` is for people and the desktop app. Agents don't run it.

## Flags

- `--every <PERIOD>`: required. Whole minutes from `5m` to `168h`, as a Go
  duration: `15m`, `1h30m`, `24h`.
- `--prompt <TEXT>`: required. The prompt each run starts with.
- `--keep <N>`: how many runs' workspaces to keep, 1 to 50. Default 5.
- `--model <M>`: passed to `claude --model`.
- `--permission-mode <P>`: passed to `claude --permission-mode`.
- `--paused`: add the loop paused. Its `next_run_at` is then `null`.

## JSON output

```json
{"name": "deps-bump", "every": "24h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": false, "created_at": "2026-11-05T18:00:00Z", "next_run_at": "2026-11-06T18:00:00Z", "last_run": null, "warnings": []}
```

`model` and `permission_mode` are `null` when not set, and `last_run` is
`null` before the first run.

## Exit codes

| Situation                                       | Error code       | Exit |
| ----------------------------------------------- | ---------------- | ---- |
| Missing `--every` or `--prompt`, or a flag error | `usage`         | 2    |
| Bad name, period, prompt, or `--keep`           | `loop_invalid`   | 2    |
| A spec that can't be parsed                     | `spec_invalid`   | 2    |
| A loop with that name already exists            | `loop_exists`    | 4    |

## Examples

```console
$ ws loop add deps-bump --every 24h --permission-mode acceptEdits \
    --prompt "Update the Go and npm dependencies, run the tests, and open a PR" buff-bear
$ ws loop add deploy-watch --every 15m --keep 2 --prompt "Check the status page and summarize anything new"
$ ws loop add --paused triage --every 1h --prompt "Triage new issues" buff-bear
```
