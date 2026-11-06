# ws loop add

```
ws loop add [--json] --every <PERIOD> --prompt <TEXT> [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] [--script <TEXT>] [--script-timeout <DUR>] [--var <KEY>=<VALUE>]... <NAME> [<SPEC>...]
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
- `--script <TEXT>`: a shell script that must exit 0 for a run to start. It
  runs with `/bin/sh -c`, from your home directory, and its stdout and stderr
  can be used in the prompt. Blank is no script.
- `--script-timeout <DUR>`: how long the script may run: whole seconds from
  `1s` to `10m`, as a Go duration. Default `1m`. Only with `--script`.
- `--var <KEY>=<VALUE>`: a value for the prompt (`{{.Vars.KEY}}`) and for the
  script (`$WS_LOOP_VAR_KEY`). Repeat for more, up to 32. `KEY` is
  `[A-Za-z_][A-Za-z0-9_]*`, at most 64 bytes. `VALUE` is at most 4096 bytes
  and may be empty or contain `=`.

## Prompt templates

The prompt is a Go `text/template`. These values are available:

| Value | What it is |
| ----- | ---------- |
| `{{.Name}}` | The loop's name |
| `{{.Vars.KEY}}` | The value of `--var KEY=…` |
| `{{.Stdout}}` | The script's stdout; only with `--script` |
| `{{.Stderr}}` | The script's stderr; only with `--script` |

A missing value is an error. `add` refuses `{{.Stdout}}` without `--script`,
and `{{.Vars.X}}` without `--var X=…`. Write a literal `{{` as `{{"{{"}}`.

The prompt is checked when the loop is added, so a bad template is refused
now, not at the first run. There is no `ws loop edit`: to change a loop's
script, variables, or prompt, `ws loop rm` it and add it again.

## JSON output

```json
{"name": "deps-bump", "every": "24h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": false, "created_at": "2026-11-05T18:00:00Z", "next_run_at": "2026-11-06T18:00:00Z", "last_run": null, "script": "gh issue list --label needs-triage --json number | grep .", "script_timeout": "2m", "vars": {"REPO": "buff-bear"}, "last_gate": null, "warnings": []}
```

`model` and `permission_mode` are `null` when not set, and `last_run` is
`null` before the first run.

## Exit codes

| Situation                                       | Error code       | Exit |
| ----------------------------------------------- | ---------------- | ---- |
| Missing `--every` or `--prompt`, or a flag error | `usage`         | 2    |
| Bad name, period, prompt, `--keep`, `--script-timeout`, or `--var`; a prompt template that doesn't parse or uses a missing value | `loop_invalid` | 2 |
| `--script-timeout` without `--script`           | `usage`          | 2    |
| A spec that can't be parsed                     | `spec_invalid`   | 2    |
| A loop with that name already exists            | `loop_exists`    | 4    |

## Examples

```console
$ ws loop add deps-bump --every 24h --permission-mode acceptEdits \
    --prompt "Update the Go and npm dependencies, run the tests, and open a PR" buff-bear
$ ws loop add deploy-watch --every 15m --keep 2 --prompt "Check the status page and summarize anything new"
$ ws loop add --paused triage --every 1h --prompt "Triage new issues" buff-bear
$ ws loop add triage --every 1h --var REPO=buff-bear \
    --script 'gh issue list --repo "$WS_LOOP_VAR_REPO" --label needs-triage --json number --jq ".[].number" | grep .' \
    --prompt "Triage these issues in {{.Vars.REPO}}: {{.Stdout}}"
```
