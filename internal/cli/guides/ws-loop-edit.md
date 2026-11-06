# ws loop edit

```
ws loop edit [--json] <NAME> [--every <PERIOD>] [--prompt <TEXT>] [--keep <N>] [--model <M>] [--permission-mode <P>] [--script <TEXT>] [--script-timeout <DUR>] [--var <KEY>=<VALUE>]... [--no-script] [--no-vars] [--no-specs] [<SPEC>...]
```

Changes a loop in place. Only what's given changes: the loop's runs,
`created_at`, and paused state are kept. The edited loop is checked exactly
as `ws loop add` checks a new one, and an edit that fails changes nothing.
Giving no option and no spec is a usage error.

`ws loop edit` is for people and the desktop app. Agents don't run it.

## Flags

- `--every <PERIOD>`: a new period, as for `add`: whole minutes from `5m` to
  `168h`, as a Go duration. When its length changes, `next_run_at` becomes the
  last run's start (or `created_at`, if it never ran) plus the new period; if
  that isn't after now, it's now plus the new period.
- `--prompt <TEXT>`: a new prompt, checked as for `add`. A loop added before
  scripts existed becomes version 2 when this is given; see Old records below.
- `--keep <N>`: how many runs' workspaces to keep, 1 to 50.
- `--model <M>`: passed to `claude --model`. `--model ""` clears it.
- `--permission-mode <P>`: passed to `claude --permission-mode`. `""` clears
  it.
- `--script <TEXT>`: a new gate script, a shell script that must exit 0 for a
  run to start. Blank is the same as `--no-script`. Changing the script clears
  `last_gate`.
- `--script-timeout <DUR>`: how long the script may run: whole seconds from
  `1s` to `10m`, as a Go duration. Needs a script: the loop's, or the one
  given with `--script`.
- `--var <KEY>=<VALUE>`: replaces **all** the loop's variables with the ones
  given. Repeat for more, as for `add`.
- `--no-script`: remove the script, its timeout, and `last_gate`. Can't be
  given with `--script` or `--script-timeout`.
- `--no-vars`: remove every variable. Can't be given with `--var`.
- `--no-specs`: remove every spec, so runs get an empty workspace. Can't be
  given with specs.
- `<SPEC>...`: replace the loop's specs.

The name can't be changed: it names the record and every run's workspace.
To rename, `ws loop rm` the loop and `ws loop add` it again. Pausing is
`ws loop pause` and `ws loop resume`, not an edit.

## Schedule and script

- **Schedule.** A period of the same length written differently (`60m` for
  `1h`) leaves `next_run_at` as it is. `every` is saved as `add` saves it
  (`90m` becomes `1h30m`). An edit never starts a run by itself.
- **Script.** Setting a script on a loop that had none gives it the default
  `1m` timeout, unless `--script-timeout` is given. Changing the script keeps
  its timeout. Changing or removing the script clears `last_gate`.

## Old records

A loop added before scripts existed (record version 1) stays version 1, with
a literal prompt, unless the edit gives `--prompt`, `--script`, `--no-script`,
`--var`, or `--no-vars`. Then it becomes version 2 and its prompt is checked
as a template: write a literal `{{` as `{{"{{"}}`.

## JSON output

The loop, as `ws loop add --json` prints it, with `warnings`:

```json
{"name": "deps", "every": "6h", "prompt": "Update the Go and npm dependencies, run the tests, and open a PR", "specs": ["buff-bear"], "model": null, "permission_mode": "acceptEdits", "keep": 5, "paused": false, "created_at": "2026-11-05T18:00:00Z", "next_run_at": "2026-11-06T00:00:00Z", "last_run": null, "script": null, "script_timeout": null, "vars": {}, "last_gate": null, "warnings": []}
```

`model` and `permission_mode` are `null` when not set, and `last_run` is
`null` before the first run.

## Exit codes

| Situation                                       | Error code       | Exit |
| ----------------------------------------------- | ---------------- | ---- |
| No option and no spec given, a flag error, or conflicting flags (`--no-script` with `--script`, `--no-vars` with `--var`, `--no-specs` with specs) | `usage` | 2 |
| A bad period, prompt, `--keep`, `--script-timeout`, or `--var`; a prompt template that doesn't parse or uses a missing value; `--script-timeout` on a loop with no script | `loop_invalid` | 2 |
| A spec that can't be parsed                     | `spec_invalid`   | 2    |
| No loop has that name                           | `loop_not_found` | 3    |

## Examples

```console
$ ws loop edit deps --every 6h
$ ws loop edit deps --prompt "Update the Go dependencies and open a PR" --keep 3
$ ws loop edit deps --model opus --permission-mode acceptEdits
$ ws loop edit deps buff-bear api
$ ws loop edit deps --no-specs
$ ws loop edit triage --script 'gh issue list --repo "$WS_LOOP_VAR_REPO" --json number --jq ".[].number" | grep .' --script-timeout 2m
$ ws loop edit triage --var REPO=bobcob7/api --var LABEL=bug
$ ws loop edit deploy-watch --every 30m --keep 4 --no-script
```
