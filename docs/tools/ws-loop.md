# ws loop

Start a Claude Code agent in a new workspace every period, with the same
prompt.

## Synopsis

```
ws loop add <NAME> --every <PERIOD> --prompt <TEXT> [OPTIONS] [<SPEC>...]
ws loop list
ws loop pause <NAME>
ws loop resume <NAME>
ws loop rm <NAME>
ws loop run [--force] <NAME>
ws loop tick [--dry-run] [--watch]
```

## Description

A **loop** is a named prompt with a period. Every period, `ws` starts a
new agent for it, exactly as

```
ws claude --name loop-<NAME> --parent none [<SPEC>...] -- [--model <M>] [--permission-mode <P>] <PROMPT>
```

would (with `<PROMPT>` rendered as a template, below), so each run is an ordinary workspace (`loop-<NAME>-<8 hex>`) that
`ws list`, `ws rm`, `ws continue`, and the desktop app show
([0048](../adrs/0048-agent-loop-scheduler.md)).

Loops are kept in `CONFIG_DIR/loops/<NAME>.toml`. They run only when
something runs **`ws loop tick`**: the launchd agent below, `ws loop tick
--watch` in a terminal, or the desktop app while it's open. Nothing in
`ws` runs in the background by itself.

**Schedule.** A new loop's first run is one period after it's added; use
`ws loop run` to start one now. A loop is **due** when it isn't paused and
its `next_run_at` has passed. After each run, `next_run_at` moves forward
one period; if the Mac slept through several periods, the loop runs once
and the next run is one period later. Missed runs are skipped, not made up.

`ws loop tick` starts every due loop, except:

- **`asleep`**: on macOS, when the Mac isn't fully awake (a dark wake),
  read as [`ws continue`](ws-continue.md) reads it. Nothing starts; the
  first tick after a real wake starts it.
- **`busy`**: its last run's agent is still `working` or `starting`. The
  loop waits for it, and starts on the first tick after it finishes.
- **`skipped`**: the loop has a script and it exited non-zero (see
  below). `next_run_at` moves on one period, as after a run.

**Scripts.** A loop can have a **gate script**
([0049](../adrs/0049-loop-scripts-and-variables.md)). When the loop is
due, and isn't paused, asleep, or busy, `tick` runs it before creating
the run's workspace:

- exit 0: the run starts, and the script's stdout and stderr can be used
  in the prompt;
- any other exit, or killed by a signal: nothing starts; the item is
  `skipped`;
- it runs past `--script-timeout`, or can't start: nothing starts; the
  item is `failed` with `loop_script_failed`, and a failed run is
  recorded.

The script is run as `/bin/sh -c <SCRIPT>`, from your home directory,
with stdin empty and `ws`'s environment plus `WS_LOOP_NAME=<NAME>` and
`WS_LOOP_VAR_<KEY>=<VALUE>` for each variable. At the timeout its whole
process group is killed. Stdout and stderr are each kept up to 64 KiB
(the rest is dropped, with a warning), invalid UTF-8 becomes `�`, and
trailing newlines are trimmed. They're used only in the prompt; the
record keeps only the outcome, in `last_gate`.

**Prompt templates.** A loop added with this `ws` has a prompt that is a
Go [`text/template`](https://pkg.go.dev/text/template):

| Value | What it is |
| ----- | ---------- |
| `{{.Name}}` | The loop's name |
| `{{.Vars.KEY}}` | The value of `--var KEY=…` |
| `{{.Stdout}}` | The script's stdout; only in a loop with `--script` |
| `{{.Stderr}}` | The script's stderr; only in a loop with `--script` |

A missing key is an error: `add` refuses a prompt that uses `{{.Stdout}}`
without `--script`, or `{{.Vars.X}}` without `--var X=…`. Write a
literal `{{` as `{{"{{"}}`. Loops added before scripts existed (record
version 1) keep their prompts literally, `{{` and all.

**Retention.** After a run starts, older runs' workspaces beyond the
newest `--keep` (default 5) are removed as [`ws rm`](ws-rm.md) removes
them, unless their agent is still working. Removing a loop leaves its
runs' workspaces alone.

**Locking.** `tick`, `run`, and every command that changes a loop hold
`CONFIG_DIR/loops/.lock`, so launchd and the desktop app can both tick
without starting a run twice.

### ws loop add

Adds a loop. Specs are checked now and resolved at each run, so a run
fetches its repos as `ws claude` does. Without specs, each run gets an
empty workspace.

| Option | Description |
| ------ | ----------- |
| `--every <PERIOD>` | Required. Whole minutes from `5m` to `168h`, as a Go duration: `15m`, `1h30m`, `24h` |
| `--prompt <TEXT>` | Required. The prompt each run starts with |
| `--keep <N>` | How many runs' workspaces to keep, 1–50; default 5 |
| `--model <M>` | Passed to `claude --model` |
| `--permission-mode <P>` | Passed to `claude --permission-mode` |
| `--paused` | Add it paused |
| `--script <TEXT>` | A shell script that must exit 0 for a run to start; run with `/bin/sh -c`. Blank is no script |
| `--script-timeout <DUR>` | How long the script may run: whole seconds from `1s` to `10m`, as a Go duration; default `1m`. Only with `--script` |
| `--var <KEY>=<VALUE>` | A value for the prompt (`{{.Vars.KEY}}`) and the script (`$WS_LOOP_VAR_KEY`). Repeat for more, up to 32. `KEY` is `[A-Za-z_][A-Za-z0-9_]*`, at most 64 bytes; `VALUE` at most 4096 bytes and may be empty or contain `=` |

A name is 1–40 characters of `a-z`, `0-9`, and `-`, starting with a
letter or digit. The prompt is parsed and rendered with the variables
(and an empty stdout and stderr) when the loop is added, so a bad
template or a missing variable is `loop_invalid` now, not at the first
run. A key used only inside `{{if}}`, or through `index`, can still be
missing at a run, which then fails with `loop_invalid`.

There is no `ws loop edit`: to change a loop's script, variables, or
prompt, `ws loop rm` it and add it again.

### ws loop list

Lists every loop, by name. A loop file that can't be read, or is from a
newer `ws`, is a warning and isn't listed.

### ws loop pause, ws loop resume

`pause` stops a loop from running; a run already started keeps going.
`resume` starts it again; if its `next_run_at` passed while it was paused,
the next run is one period after now. Pausing a paused loop, or resuming
a running one, changes nothing.

### ws loop rm

Deletes the loop. Its runs' workspaces stay until they're removed as any
workspace is.

### ws loop run

Starts a run now, whether or not the loop is paused, due, or busy, and
leaves `next_run_at` as it is. Prints `ws claude`'s result with the loop's
name.

A loop's script runs first, as in `tick`. If it exits non-zero, nothing
starts and `run` fails with `loop_skipped`; a timeout is
`loop_script_failed`. Either way `last_gate` is saved.

| Option | Description |
| ------ | ----------- |
| `--force` | Don't run the script; `{{.Stdout}}` and `{{.Stderr}}` are empty, and `last_gate` is unchanged |

### ws loop tick

One pass: starts every due loop, records each run, and prunes old runs.
Exits 0 when nothing was due.

| Option | Description |
| ------ | ----------- |
| `--dry-run` | Report what would start; start, write, and remove nothing, and run no script. A due loop with a script is `would_start` |
| `--watch` | Run a pass every minute until interrupted, printing only what started or failed. Text only: `--json` with `--watch` is a usage error |

## Output

Every subcommand accepts `--json`, except `tick --watch`.

A loop (`add`, `pause`, `resume`, `rm`, and each item of `list`):

```json
{
  "name": "deps-bump",
  "every": "24h",
  "prompt": "Update the Go and npm dependencies, run the tests, and open a PR",
  "specs": ["buff-bear"],
  "model": null,
  "permission_mode": "acceptEdits",
  "keep": 5,
  "paused": false,
  "created_at": "2026-11-05T18:00:00Z",
  "next_run_at": "2026-11-06T18:00:00Z",
  "last_run": {
    "started_at": "2026-11-05T18:00:04Z",
    "workspace": "loop-deps-bump-1a2b3c4d",
    "session": "e8cdf5d8",
    "error": null
  },
  "script": "go list -m -u -f '{{if .Update}}{{.Path}}{{end}}' all | grep .",
  "script_timeout": "2m",
  "vars": {"BRANCH": "main"},
  "last_gate": {
    "at": "2026-11-05T18:00:02Z",
    "outcome": "passed",
    "exit_code": 0,
    "message": null
  }
}
```

- `add`, `pause`, `resume`, and `rm` print the loop's fields at the top
  level, plus `"warnings": []`. `rm` prints the loop as it was.
- `list` prints `{"loops": [<loop>...], "warnings": []}`.
- `model` and `permission_mode` are `null` when not set.
- `next_run_at` is `null` while the loop is paused.
- `last_run` is `null` before the first run. Its `error` is `null` or
  `{"code", "message"}` when that run couldn't start; `workspace` and
  `session` are then `null`.
- `script` is `null` when the loop has none; `script_timeout` is then
  `null` too, and otherwise a Go duration without zero units (`1m`,
  `90s` prints as `1m30s`).
- `vars` is an object of strings, `{}` when there are none.
- `last_gate` is `null` until the script first runs. `outcome` is
  `passed`, `skipped`, `timed_out`, or `failed`; `exit_code` is the
  script's exit status, or `null` when it timed out, was killed by a
  signal, or couldn't start; `message` is, for `skipped`, the last
  non-blank line of stderr (at most 200 bytes), for `timed_out` and
  `failed` the error, and otherwise `null`.

`run`:

```json
{"name": "deps-bump", "workspace": "loop-deps-bump-1a2b3c4d", "session": "e8cdf5d8", "path": "/…/workspaces/loop-deps-bump-1a2b3c4d", "warnings": []}
```

`tick`:

```json
{
  "loops": [
    {"name": "deps-bump", "action": "started", "workspace": "loop-deps-bump-1a2b3c4d",
     "session": "e8cdf5d8", "next_run_at": "2026-11-06T18:00:00Z", "error": null,
     "gate": {"at": "2026-11-05T18:00:02Z", "outcome": "passed", "exit_code": 0, "message": null}},
    {"name": "triage", "action": "waiting", "workspace": null, "session": null,
     "next_run_at": "2026-11-05T19:00:00Z", "error": null, "gate": null},
    {"name": "new-issues", "action": "skipped", "workspace": null, "session": null,
     "next_run_at": "2026-11-05T19:15:00Z", "error": null,
     "gate": {"at": "2026-11-05T19:00:01Z", "outcome": "skipped", "exit_code": 1, "message": "no new issues"}}
  ],
  "warnings": []
}
```

| `action` | Meaning |
| -------- | ------- |
| `started` | A run started; `workspace` and `session` are the new run's |
| `would_start` | `--dry-run`: it's due and would start |
| `waiting` | Not due yet |
| `paused` | Paused |
| `busy` | Due, but its last run is still working |
| `asleep` | Due, but the Mac isn't fully awake |
| `skipped` | Due, but its script exited non-zero; `gate` says how, and `next_run_at` moves on |
| `failed` | Due, but the run couldn't start, its script timed out or couldn't start, or its prompt couldn't be rendered; `error` says why, and `next_run_at` still moves on |

`gate` is the `last_gate` object this pass wrote, or `null` when no
script ran.

Text output is one line per loop:

```console
$ ws loop list
deps-bump  every 24h  active  next 2026-11-06T18:00:00Z  last loop-deps-bump-1a2b3c4d
triage     every 1h   paused  next -                     last -
```

## Examples

```console
# Every morning-ish: bump dependencies in buff-bear
$ ws loop add deps-bump --every 24h --permission-mode acceptEdits \
    --prompt "Update the Go and npm dependencies, run the tests, and open a PR" buff-bear

# Try it now instead of waiting a day
$ ws loop run deps-bump

# Every 15 minutes, in an empty workspace, keeping only the last 2 runs
$ ws loop add deploy-watch --every 15m --keep 2 --prompt "Check https://status.example.com and summarize anything new"

$ ws loop pause deploy-watch
$ ws loop resume deploy-watch
$ ws loop rm deploy-watch

# Every 15 minutes, only when there are open issues labelled needs-triage,
# with their numbers and titles in the prompt
$ ws loop add triage --every 15m --var REPO=bobcob7/buff-bear --var LABEL=needs-triage \
    --script 'gh issue list --repo "$WS_LOOP_VAR_REPO" --label "$WS_LOOP_VAR_LABEL" --json number,title --jq ".[] | \"#\(.number) \(.title)\"" | grep .' \
    --prompt 'Triage these {{.Vars.LABEL}} issues in {{.Vars.REPO}}:
{{.Stdout}}' buff-bear

# Run it now even if the script would say there's nothing to do
$ ws loop run --force triage

# Run the scheduler in a terminal
$ ws loop tick --watch
```

### Run the scheduler every minute with launchd

Save as `~/Library/LaunchAgents/com.github.bobcob7.ws-loop.plist`, with
the path `which ws` prints and a `PATH` that finds `claude` and `git`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.github.bobcob7.ws-loop</string>
  <key>ProgramArguments</key>
  <array><string>/Users/me/go/bin/ws</string><string>loop</string><string>tick</string><string>--quiet</string></array>
  <key>EnvironmentVariables</key>
  <dict><key>PATH</key><string>/Users/me/.local/bin:/opt/homebrew/bin:/usr/bin:/bin</string></dict>
  <key>StartInterval</key><integer>60</integer>
  <key>StandardErrorPath</key><string>/tmp/ws-loop.log</string>
</dict>
</plist>
```

```console
$ launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.github.bobcob7.ws-loop.plist
```

## Errors

See also [`ws claude`'s errors](ws-claude.md#errors), which a run that
can't start reports.

| Situation | Error code | Exit |
| --------- | ---------- | ---- |
| Bad name, period, prompt, `--keep`, `--script-timeout`, or `--var`; a prompt template that doesn't parse or uses a missing value | `loop_invalid` | 2 |
| `--script-timeout` without `--script` | `usage` | 2 |
| A spec that can't be parsed | `spec_invalid` | 2 |
| `--json` with `tick --watch` | `usage` | 2 |
| No loop has that name | `loop_not_found` | 3 |
| `add` with a name already used | `loop_exists` | 4 |
| `run`: the run couldn't start | `ws claude`'s code | 1–4 |
| `run`: the script exited non-zero | `loop_skipped` | 4 |
| `run` or `tick`: the script timed out or couldn't start | `loop_script_failed` | 1 |
| `run` or `tick`: the prompt couldn't be rendered | `loop_invalid` | 2 |
| `tick`: a run couldn't start; the others still start | `ws claude`'s code, in its item | 1–4 |

## See also

[`ws claude`](ws-claude.md), [`ws continue`](ws-continue.md),
[`ws rm`](ws-rm.md), [0048](../adrs/0048-agent-loop-scheduler.md),
[0049](../adrs/0049-loop-scripts-and-variables.md)
