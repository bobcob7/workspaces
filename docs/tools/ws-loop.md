# ws loop

Start a Claude Code agent in a new workspace every period, with the same
prompt.

## Synopsis

```
ws loop add <NAME> --every <PERIOD> --prompt <TEXT> [OPTIONS] [<SPEC>...]
ws loop edit <NAME> [OPTIONS] [<SPEC>...]
ws loop list
ws loop pause <NAME>
ws loop resume <NAME>
ws loop rm <NAME>
ws loop run [--force] <NAME>
ws loop tick [--dry-run] [--watch]
ws loop history [--json] [--limit <N>] [--run <SEQ> | --clear] <NAME>
ws loop memory list  [--json] <NAME>
ws loop memory get   [--json] <NAME> <KEY>
ws loop memory set   [--json] [--expect-rev <N>] [--file <PATH> | --file -] <NAME> <KEY> [<VALUE>]
ws loop memory rm    [--json] [--expect-rev <N>] <NAME> <KEY>
ws loop memory clear [--json] <NAME>
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
with stdin empty and `ws`'s environment plus `WS_LOOP_NAME=<NAME>`,
`WS_LOOP_HISTORY=<path of history.json>` and `WS_LOOP_MEMORY=<path of
memory.json>` (both absolute), and `WS_LOOP_VAR_<KEY>=<VALUE>` for each
variable. At the timeout its whole
process group is killed. Stdout and stderr are each kept up to 64 KiB
(the rest is dropped, with a warning), invalid UTF-8 becomes `�`, and
trailing newlines are trimmed. They're used only in the prompt; the
record keeps only the outcome, in `last_gate`.

**Prompt templates.** A loop added with this `ws` has a prompt that is a
Go [`text/template`](https://pkg.go.dev/text/template):

| Value | What it is |
| ----- | ---------- |
| `{{.Name}}` | The loop's name |
| `{{.Title}}` | The loop's title, or its name when it has none |
| `{{.Vars.KEY}}` | The value of `--var KEY=…` |
| `{{.Stdout}}` | The script's stdout; only in a loop with `--script` |
| `{{.Stderr}}` | The script's stderr; only in a loop with `--script` |
| `{{memory "KEY"}}` | The value of the loop's memory key `KEY`, or empty when it has none; see [`ws loop memory`](#ws-loop-memory) |

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
without starting a run twice. `ws loop history --clear` holds it too;
`ws loop memory` takes only its own lock.

**History and memory.** Each loop keeps its runs in
`CONFIG_DIR/state/.loops/<NAME>/history.json`: the newest 200, with the
reports of the newest 50 that have one. Its runs share a memory,
`memory.json` in the same directory: up to 100 keys, 64 KiB per value,
and 256 KiB in all. `ws loop rm` deletes both. See
[`ws loop history`](#ws-loop-history) and [`ws loop memory`](#ws-loop-memory).

### ws loop add

Adds a loop. Specs are checked now and resolved at each run, so a run
fetches its repos as `ws claude` does. Without specs, each run gets an
empty workspace.

| Option | Description |
| ------ | ----------- |
| `--title <TITLE>` | A display name, such as `Nightly deps bump`: up to 80 characters, no control characters, trimmed of spaces. The name stays the ID; unset, the name is shown |
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

To change a loop, use `ws loop edit`.

### ws loop edit

Changes a loop in place ([0052](../adrs/0052-loop-edit-and-structured-forms.md)).
Only what's given changes; the loop's runs, `created_at`, and paused
state are kept. The edited loop is checked exactly as `add` checks a new
one (period, prompt template, keep, script timeout, variables, specs),
under the loops lock, and an edit that fails changes nothing. Giving no
option and no spec is a usage error.

| Option | Description |
| ------ | ----------- |
| `--title <TITLE>` | A new display name, as for `add`; `--title ""` clears it |
| `--every <PERIOD>` | A new period, as for `add` |
| `--prompt <TEXT>` | A new prompt, as for `add` |
| `--keep <N>` | How many runs' workspaces to keep, 1–50 |
| `--model <M>` | Passed to `claude --model`; `--model ""` clears it |
| `--permission-mode <P>` | Passed to `claude --permission-mode`; `""` clears it |
| `--script <TEXT>` | A new gate script; blank is the same as `--no-script` |
| `--script-timeout <DUR>` | As for `add`. Needs a script: the loop's, or `--script` |
| `--var <KEY>=<VALUE>` | Replaces **all** the loop's variables with the ones given; repeat for more, as for `add` |
| `--no-script` | Remove the script, its timeout, and `last_gate` |
| `--no-vars` | Remove every variable |
| `--no-specs` | Remove every spec, so runs get an empty workspace |
| `<SPEC>...` | Replace the loop's specs |

- **The name can't be changed**: it names the record and every run's
  workspace. To rename, `rm` the loop and `add` it again. The title can be
  changed with `--title`.
- **Pausing** is `pause` and `resume`, not an edit.
- **Schedule.** When the period's length changes, `next_run_at` becomes
  the last run's start (or `created_at`, if it never ran) plus the new
  period; if that isn't after now, it's now plus the new period. So an
  edit never starts a run by itself. A period of the same length written
  differently (`60m` for `1h`) leaves `next_run_at` as it is. `every` is
  saved as `add` saves it (`90m` becomes `1h30m`).
- **Script.** Setting a script on a loop that had none gives it the
  default `1m` timeout, unless `--script-timeout` is given; changing the
  script keeps its timeout. Changing or removing the script clears
  `last_gate`.
- **Old records.** A loop added before scripts existed (record version 1)
  stays version 1, with a literal prompt, unless the edit gives
  `--prompt`, `--script`, `--no-script`, `--var`, or `--no-vars`. Then it
  becomes version 2 and its prompt is checked as a template: write a
  literal `{{` as `{{"{{"}}`.
- `--no-script` with `--script` or `--script-timeout`, `--no-vars` with
  `--var`, and `--no-specs` with specs are usage errors.

### ws loop list

Lists every loop: its title and then its name in parentheses when it has a
title, else its name. A loop file that can't be read, or is from a
newer `ws`, is a warning and isn't listed.

### ws loop pause, ws loop resume

`pause` stops a loop from running; a run already started keeps going.
`resume` starts it again; if its `next_run_at` passed while it was paused,
the next run is one period after now. Pausing a paused loop, or resuming
a running one, changes nothing.

### ws loop rm

Deletes the loop, with its run history, saved reports, and memory
(`state/.loops/<NAME>/`). Its runs' workspaces stay until they're removed
as any workspace is.

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

### ws loop history

Shows a loop's runs, newest first, from `history.json`. Each run is
recorded when its attempt starts, is skipped, or fails to start, and
`tick` settles it once its agent has finished, so the newest run can
lag the agent by one tick.

| Option | Description |
| ------ | ----------- |
| `--clear` | Delete the loop's run history and its saved reports |
| `--limit <N>` | How many runs to show, 1–200; default 20 |
| `--run <SEQ>` | Show one run, with its report |

`--run` can't be combined with `--clear` or `--limit`. Each run has a
`seq` number, which `--run` takes.

| Status | Meaning |
| ------ | ------- |
| `running` | Its agent is working or starting |
| `done` | Its agent finished without a verdict |
| `succeeded` | Its verdict's outcome is `pass` |
| `failed` | Its verdict's outcome is `fail` |
| `start_failed` | It couldn't start; `error` says why |
| `skipped` | Its gate script exited non-zero; `repeats` counts the skips, and `started_at` and `finished_at` are the first and latest |
| `gone` | Its workspace was removed while it was running |

Text output, one line per run:

```console
$ ws loop history deps-bump
42  succeeded     2026-11-10T08:00:00Z  14m30s  loop-deps-bump-1a2b3c4d  PASS
41  skipped ×12   2026-11-09T20:00:00Z  -       -                        exit 1: no outdated modules
40  start_failed  2026-11-09T08:00:00Z  -       -                        claude_missing: …
```

With `--run`, text output prints the run's line, a blank line, then the
report.

### ws loop memory

A loop's memory is a set of key–value pairs that all its runs share. A
run reads a key with `{{memory "KEY"}}` in its prompt, and changes it
with [`ws machine memory`](ws-machine.md#ws-machine-memory). People and
gate scripts use this command.

A key is 1–64 characters of `A-Za-z0-9_`, not starting with a digit. The
limits are in [state](../state.md#records).

| Subcommand | Description |
| ---------- | ----------- |
| `list` | List the loop's memory |
| `get <KEY>` | Print one memory value |
| `set <KEY> [<VALUE>]` | Set one memory value |
| `rm <KEY>` | Remove one memory value |
| `clear` | Remove all of the loop's memory |

| Option | Description |
| ------ | ----------- |
| `--expect-rev <N>` | Fail with `memory_conflict` unless the key's revision is N; `0` means the key must not exist. Default: don't check |
| `--file <PATH>` | Read the value from this file; `-` reads stdin. `set` needs exactly one of `<VALUE>` and `--file` |

`set` bumps the key's `rev`. Without `--expect-rev`, the last writer wins.

Text output: `list` prints `KEY  rev N  <updated_at>  <first line of the
value, cut to 60 characters>`; `get` prints the value exactly; `set`
prints `KEY  rev N`; `rm` prints `KEY removed`; `clear` prints `N keys
removed`.

## Output

Every subcommand accepts `--json`, except `tick --watch`.

A loop (`add`, `edit`, `pause`, `resume`, `rm`, and each item of `list`):

```json
{
  "name": "deps-bump",
  "title": "Dependency bump",
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

- `add`, `edit`, `pause`, `resume`, and `rm` print the loop's fields at
  the top level, plus `"warnings": []`. `edit` prints the loop as saved;
  `rm` prints the loop as it was.
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

`history`, a list:

```json
{"loop": "deps-bump", "runs": [RunJSON…], "total": 57, "warnings": []}
```

`RunJSON`:

```json
{
  "seq": 42,
  "status": "succeeded",
  "trigger": "tick",
  "started_at": "2026-11-10T08:00:00Z",
  "finished_at": "2026-11-10T08:14:30Z",
  "duration_seconds": 870,
  "workspace": "loop-deps-bump-1a2b3c4d",
  "workspace_exists": true,
  "session": "e8cdf5d8",
  "error": null,
  "gate": {"at": "2026-11-10T07:59:58Z", "outcome": "passed", "exit_code": 0, "message": null},
  "repeats": 1,
  "verdict": {"verdict": "PASS", "outcome": "pass", "recorded_at": "2026-11-10T08:14:30Z", "report": "/…/state/.loops/deps-bump/reports/42.md"}
}
```

- `duration_seconds` is the whole seconds from `started_at` to
  `finished_at`, for `done`, `succeeded`, `failed`, and `gone` runs that
  have a `finished_at`; else `null`.
- `workspace` and `session` are `null` when empty. `workspace_exists` is
  true when there is a workspace and it isn't closed.
- `error` is `{"code", "message"}` when the run has an error code, else
  `null`. `gate` is the `last_gate` object, or `null`.
- `verdict.report` is the absolute path of `reports/<seq>.md` when that
  file exists, else `null`.

`history --run <SEQ>`:

```json
{"loop": "deps-bump", "run": RunJSON, "report_text": "# Bumped 3 modules\n…", "warnings": []}
```

`report_text` is `null` when the run has no report.

`history --clear`:

```json
{"loop": "deps-bump", "removed": 57, "warnings": []}
```

`memory list`:

```json
{"loop": "deps-bump", "entries": [MemoryEntryJSON…], "bytes": 1234, "warnings": []}
```

`memory get` and `memory set`:

```json
{"loop": "deps-bump", "entry": MemoryEntryJSON, "warnings": []}
```

`memory rm`:

```json
{"loop": "deps-bump", "key": "seen", "removed": true, "warnings": []}
```

`memory clear`:

```json
{"loop": "deps-bump", "removed": 4, "warnings": []}
```

`MemoryEntryJSON` (entries are sorted by key):

```json
{"key": "seen", "value": "#412 #415", "rev": 3, "updated_at": "2026-11-10T08:14:00Z", "updated_by": "loop-deps-bump-1a2b3c4d"}
```

`updated_by` is `null` when a person or a gate script wrote the value.

## Examples

```console
# Every morning-ish: bump dependencies in buff-bear
$ ws loop add deps-bump --every 24h --permission-mode acceptEdits \
    --prompt "Update the Go and npm dependencies, run the tests, and open a PR" buff-bear

# Try it now instead of waiting a day
$ ws loop run deps-bump

# Give it a display name, shown in `ws loop list`
$ ws loop edit deps-bump --title "Dependency bump"

# Every 15 minutes, in an empty workspace, keeping only the last 2 runs
$ ws loop add deploy-watch --every 15m --keep 2 --prompt "Check https://status.example.com and summarize anything new"

# Check every 30 minutes instead, keep 4 runs, and drop the script
$ ws loop edit deploy-watch --every 30m --keep 4 --no-script

# Point it at different repos
$ ws loop edit deps-bump buff-bear api

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

# The last 5 runs of deps-bump, then one of them with its report
$ ws loop history --limit 5 deps-bump
$ ws loop history --run 42 deps-bump

# Every hour, triage the issues not yet seen; the prompt reads and extends the memory
$ ws loop add new-issues --every 1h --prompt 'Triage the open issues in buff-bear that are not in {{memory "seen"}}. Then run `ws machine memory set seen ...` with every number seen so far, and finish with `ws machine result PASS --file ../report.md`.' buff-bear

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
| Bad name, title, period, prompt, `--keep`, `--script-timeout`, or `--var`; a prompt template that doesn't parse or uses a missing value | `loop_invalid` | 2 |
| `--script-timeout` without `--script` (`add`) | `usage` | 2 |
| `edit` with nothing to change; `--no-script` with `--script` or `--script-timeout`; `--no-vars` with `--var`; `--no-specs` with specs | `usage` | 2 |
| `edit`: `--script-timeout` on a loop left without a script | `loop_invalid` | 2 |
| A spec that can't be parsed | `spec_invalid` | 2 |
| `--json` with `tick --watch` | `usage` | 2 |
| No loop has that name | `loop_not_found` | 3 |
| `add` with a name already used | `loop_exists` | 4 |
| `run`: the run couldn't start | `ws claude`'s code | 1–4 |
| `run`: the script exited non-zero | `loop_skipped` | 4 |
| `run` or `tick`: the script timed out or couldn't start | `loop_script_failed` | 1 |
| `run` or `tick`: the prompt couldn't be rendered | `loop_invalid` | 2 |
| `tick`: a run couldn't start; the others still start | `ws claude`'s code, in its item | 1–4 |
| `history`: `--run` with `--clear` or `--limit`, a `--limit` outside 1–200, or a `--run` below 1 | `usage` | 2 |
| `history`: `--run` with a seq that isn't in the history | `loop_not_found` | 3 |
| `memory set`: neither or both of `<VALUE>` and `--file` | `usage` | 2 |
| `memory`: a key that isn't valid | `memory_invalid` | 2 |
| `memory get` or `rm`: no such key | `memory_not_found` | 3 |
| `memory set` or `rm`: `--expect-rev` didn't match | `memory_conflict` | 4 |
| `memory set`: a value over 64 KiB, over 100 keys, or over 256 KiB in all | `memory_full` | 4 |
| `history` or `memory`: a data file from a newer `ws` | `loop_invalid` | 2 |

## See also

[`ws claude`](ws-claude.md), [`ws continue`](ws-continue.md),
[`ws rm`](ws-rm.md), [0048](../adrs/0048-agent-loop-scheduler.md),
[0049](../adrs/0049-loop-scripts-and-variables.md)
