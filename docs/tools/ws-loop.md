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
ws loop run <NAME>
ws loop tick [--dry-run] [--watch]
```

## Description

A **loop** is a named prompt with a period. Every period, `ws` starts a
new agent for it, exactly as

```
ws claude --name loop-<NAME> --parent none [<SPEC>...] -- [--model <M>] [--permission-mode <P>] <PROMPT>
```

would, so each run is an ordinary workspace (`loop-<NAME>-<8 hex>`) that
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

A name is 1–40 characters of `a-z`, `0-9`, and `-`, starting with a
letter or digit.

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

### ws loop tick

One pass: starts every due loop, records each run, and prunes old runs.
Exits 0 when nothing was due.

| Option | Description |
| ------ | ----------- |
| `--dry-run` | Report what would start; start, write, and remove nothing |
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

`run`:

```json
{"name": "deps-bump", "workspace": "loop-deps-bump-1a2b3c4d", "session": "e8cdf5d8", "path": "/…/workspaces/loop-deps-bump-1a2b3c4d", "warnings": []}
```

`tick`:

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

| `action` | Meaning |
| -------- | ------- |
| `started` | A run started; `workspace` and `session` are the new run's |
| `would_start` | `--dry-run`: it's due and would start |
| `waiting` | Not due yet |
| `paused` | Paused |
| `busy` | Due, but its last run is still working |
| `asleep` | Due, but the Mac isn't fully awake |
| `failed` | Due, but the run couldn't start; `error` says why, and `next_run_at` still moves on |

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
| Bad name, period, prompt, or `--keep` | `loop_invalid` | 2 |
| A spec that can't be parsed | `spec_invalid` | 2 |
| `--json` with `tick --watch` | `usage` | 2 |
| No loop has that name | `loop_not_found` | 3 |
| `add` with a name already used | `loop_exists` | 4 |
| `run`: the run couldn't start | `ws claude`'s code | 1–4 |
| `tick`: a run couldn't start; the others still start | `ws claude`'s code, in its item | 1–4 |

## See also

[`ws claude`](ws-claude.md), [`ws continue`](ws-continue.md),
[`ws rm`](ws-rm.md), [0048](../adrs/0048-agent-loop-scheduler.md)
