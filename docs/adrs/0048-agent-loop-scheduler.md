# 0048. `ws loop` starts an agent in a new workspace every period, from a record in `CONFIG_DIR/loops/`, when `ws loop tick` runs from launchd or the desktop app

- Status: Proposed; gate scripts, variables, prompt templates, and record version 2 added by [0049](0049-loop-scripts-and-variables.md) (proposed)
- Date: 2026-11-05

## Context

### Problem

Some agent work repeats: bump dependencies every day, triage new issues
every hour, check a deploy every 15 minutes. Today a person runs
`ws claude --name … -- "<prompt>"` by hand each time. How should a user
give `ws` a named prompt and a period, so that `ws` starts that agent in
a new workspace every period, and pause, resume, delete, and inspect it
from the terminal and from the desktop app? And what process does the
starting, given that a Mac sleeps and dark-wakes
([0047](0047-ws-continue-after-usage-limit.md)) and the desktop app isn't
always open?

### What a solution needs

1. **Runs without anyone watching**, including when the desktop app is
   closed, and survives reboots and `ws` upgrades.
2. **Never starts an agent that can't work**: not during a dark wake, and
   not a second copy while the previous run is still working.
3. **Bounded**: a Mac that slept through ten periods starts one run, not
   ten, and old runs' workspaces don't pile up forever.
4. **Safe to run from several places at once** (launchd and the desktop
   app) without starting a run twice.
5. **The same machinery as `ws claude`**: the same workspace creation,
   trust, hand-off, and lifetime ([0015](0015-workspace-lifetime-follows-session.md),
   [0025](0025-ws-claude-session-hand-off.md),
   [0028](0028-single-repo-session-directory.md)), the same output
   contract ([0016](0016-output-contract.md)), and the desktop app
   driving `ws --json` ([0029](0029-desktop-app.md)).

### Relevant facts

- **launchd.** A user agent with `StartInterval` "causes the job to be
  started every N seconds. If the system is asleep during the time of the
  next scheduled interval firing, that interval will be missed … If the
  job is running during an interval firing, that interval firing will
  likewise be missed"[^launchd]. `ws continue` already documents a
  launchd agent with `StartInterval` ([ws continue](../tools/ws-continue.md)).
  `StartCalendarInterval` takes cron-like calendar fields, one plist per
  schedule[^launchd].
- **Dark wakes.** `ws` already tells a full wake from a dark wake with
  `power.Reader.Awake` (`ioreg`'s `System Capabilities`), and a turn
  started in a dark wake stops about 45 s later
  ([0047](0047-ws-continue-after-usage-limit.md)).
- **Timers and sleep.** A timer in a long-lived Go process may fire late
  after the Mac sleeps; `ws continue` avoids depending on one by reading
  the wall clock on each pass ([0047](0047-ws-continue-after-usage-limit.md)).
- **Cost of a pass.** `ws list --json`, which runs the removal pass and
  lists Claude Code's sessions, took 0.40–0.50 s, median 0.45 s[^bench-list].
- **Workspaces outlive their agent.** An agent workspace stays until its
  session is deleted ([0015](0015-workspace-lifetime-follows-session.md)),
  so a loop every 15 minutes leaves 96 workspaces a day unless something
  removes them.
- **Claude Code's own scheduling.** `/loop` and the session cron tools
  repeat a prompt inside one running session; scheduled "routines" run
  in Anthropic's cloud[^claude-sched]. Neither creates a local `ws`
  workspace from the cache.
- **The desktop app** keeps its own settings in `desktop.json`
  ([0034](0034-desktop-state-file.md)) and already runs a background task
  on a timer in its Rust backend, the review checker
  ([0035](0035-desktop-review-checker.md)).

## Decision

We will add **`ws loop`**, for people and the desktop app:

```
ws loop add <NAME> --every <PERIOD> --prompt <TEXT> [--keep <N>] [--model <M>] [--permission-mode <P>] [--paused] [<SPEC>...]
ws loop list
ws loop pause <NAME>
ws loop resume <NAME>
ws loop rm <NAME>
ws loop run <NAME>
ws loop tick [--dry-run] [--watch]
```

1. **A loop is a record**, `CONFIG_DIR/loops/<NAME>.toml`, version 1:
   `name`, `every`, `prompt`, `specs`, `model`, `permission_mode`,
   `keep`, `paused`, `created_at`, `next_run_at`, and `[[runs]]` (each
   `started_at`, `workspace`, `session`, `error_code`, `error_message`;
   newest last, at most 20). It is written through a temporary file
   renamed into place. A record of a newer version, or one that doesn't
   parse, is skipped with a warning, and never rewritten
   ([0027](0027-respawn-lock-and-record-versions.md)).
2. **Names and periods.** A name is 1–40 characters of `a-z`, `0-9`,
   and `-`, starting with a letter or digit. A period is a Go duration of
   whole minutes from `5m` to `168h` (`15m`, `1h30m`, `24h`), printed
   without zero units (`24h`, not `24h0m0s`). A prompt can't be empty.
   `keep` is 1–50, default 5. Specs are checked as specs when the loop is
   added and resolved only when a run starts. Breaking any rule is
   `loop_invalid`.
3. **Schedule.** `add` sets `next_run_at` to one period after now;
   `ws loop run` starts a run at once. A loop is **due** when it isn't
   paused and `next_run_at` has passed. After a run starts, or fails to,
   `next_run_at` moves forward one period; if that is still in the past,
   it becomes one period after now, so missed periods are skipped, not
   made up. `pause` keeps `next_run_at`; `resume` sets it to one period
   after now if it has passed.
4. **`ws loop tick` is the scheduler**: one pass over every loop, which
   starts each due one. A run is `ws claude` with no terminal:
   `--name loop-<NAME>`, no parent, the loop's specs, and after `--` the
   `--model` and `--permission-mode` if set, then the prompt. A due loop
   is left for a later pass, with `next_run_at` unchanged, when:
   - **`asleep`**: on macOS, the Mac isn't fully awake, read as
     `ws continue` reads it; if that read fails, nothing starts;
   - **`busy`**: its newest run's session is `working` or `starting`.
5. **Retention.** After a run starts, the loop's older runs beyond the
   newest `keep` whose workspace still exists, and whose session isn't
   `working` or `starting`, are removed as `ws rm` removes them. Removing
   a loop leaves its runs' workspaces alone.
6. **One pass at a time.** Every `ws loop` command that writes a record,
   and the whole of `tick` and `run`, hold `CONFIG_DIR/loops/.lock`
   exclusively. A second `tick` waits, then finds the loop no longer due.
   `--dry-run` takes the lock shared and writes nothing.
7. **Who runs `tick`.** Nothing in `ws` runs in the background. The
   command's page gives a launchd agent that runs `ws loop tick --quiet`
   every 60 s, and `--watch` runs a pass every minute in a terminal. The
   desktop app runs `ws loop tick --json` every 60 s while it is open,
   from a task in its Rust backend, and gets a **Loops** section in its
   rail to add, list, pause, resume, run, and delete loops through
   `ws loop … --json`.
8. **Output.** A loop prints as `{"name", "every", "prompt", "specs",
   "model", "permission_mode", "keep", "paused", "created_at",
   "next_run_at", "last_run"}`; `ws loop list --json` prints `{"loops":
   [...], "warnings"}`, and `tick --json` prints `{"loops": [{"name",
   "action", "workspace", "session", "next_run_at", "error"}],
   "warnings"}`, with `action` one of `started`, `would_start`,
   `waiting`, `paused`, `busy`, `asleep`, or `failed`.
   [`docs/tools/ws-loop.md`](../tools/ws-loop.md) has every shape.
9. **Error codes** ([0016](0016-output-contract.md)): `loop_invalid`
   (exit 2), `loop_not_found` (exit 3), and `loop_exists` (exit 4). A
   run that can't start fails its item with `ws claude`'s code.

### Why this option

1. **Unattended.** launchd starts `tick` whether or not anyone is logged
   in to the app, after reboots and upgrades, and a missed firing while
   asleep is harmless because the next one reads the wall clock. The
   desktop app's own tick means a user who never sets up launchd still
   gets loops while the app is open.
2. **Never wasted.** The dark-wake check is the one `ws continue` relies
   on. The busy check means a slow run delays the next one rather than
   running beside it, which could fight over the same repos and branches.
3. **Bounded.** Skipping missed periods, and pruning to `keep`
   workspaces, bound both bursts and disk use. Only finished runs are
   pruned, so nothing working is deleted.
4. **Concurrency.** One lock covers the records and the starts, so
   launchd and the app can both tick. A pass with nothing due costs about
   what `ws list` does[^bench-list], which is cheap once a minute.
5. **Same machinery.** A run goes through `ws claude`'s own creation and
   hand-off code, so trust, the lock, `claude --bg`, cleanup on failure,
   and lifetime are unchanged; a run is an ordinary workspace that
   `ws list`, `ws rm`, `ws continue`, and the desktop app already show.
   The record is TOML and lives with `ws`'s other records; what crosses
   the boundary is JSON.

Its weak points: a run starts up to a minute late, and later still if
the Mac was asleep. Without launchd or the open app nothing runs, and
`ws` can't tell the user so. A run that hits a usage limit stays `idle`,
not `busy`, so the next run starts anyway (`ws continue` carries the old
one on). The records sit outside `state/`, so `ws cache prune` and the
removal pass don't know about them.

## Alternatives

### Schedule in the desktop app

The app keeps loops in `desktop.json` and starts `ws claude` on its timer.

**Pros**

- No new `ws` command or record; the app already has a timer
  ([0035](0035-desktop-review-checker.md)).

**Cons**

- Nothing runs when the app is closed, which fails criterion 1.
- Loops can't be managed from the terminal, and the scheduling logic
  would live in Rust, outside the CLI the app is meant to drive
  ([0029](0029-desktop-app.md)).

### A long-lived `ws loop daemon`

One process sleeps until the next due loop.

**Pros**

- Runs start on time while the Mac is awake.

**Cons**

- Its timer may fire late after a sleep, and it would still need the
  dark-wake check ([0047](0047-ws-continue-after-usage-limit.md)).
- Something must start and supervise it, which is launchd's job anyway;
  a `tick` that exits after each pass can't leak or hang between runs.

### One launchd job per loop

`ws loop add` writes a plist with `StartCalendarInterval` and loads it.

**Pros**

- launchd does the scheduling, including runs missed during sleep[^launchd].

**Cons**

- `ws` would write into `~/Library/LaunchAgents` and run `launchctl`,
  outside `CONFIG_DIR` ([0011](0011-config-dir.md)), and only on macOS.
- Calendar fields express "at 09:00", not "every 15 minutes from now",
  and coalesced missed firings would still need the dark-wake and busy
  checks.

### Claude Code's scheduling

Use `/loop`, session cron, or cloud routines.

**Pros**

- No code in `ws`.

**Cons**

- They repeat a prompt in one session, or run in the cloud[^claude-sched];
  none starts a fresh local workspace from the cache, which is what
  `ws` is for.

## Consequences

- New package `internal/loop` (records, validation, schedule, store) and
  `CONFIG_DIR/loops/` in [config](../config.md) and
  [Workspace state](../state.md). `config.Paths` gains `Loops`.
- [0014](0014-command-set.md)'s status line notes `ws loop`;
  [0016](0016-output-contract.md)'s notes the three codes;
  [0031](0031-desktop-sections-rail.md)'s notes the Loops section.
- `ws loop` is a people command: its guides say agents don't run it, and
  `ws machine` doesn't wrap it.
- Conformance: unit tests in `internal/loop` pin validation, `Due`, and
  `Advance`; tests in `internal/cli` pin `tick`'s actions with mocked
  stores, Claude Code, and power reader; the guide coverage tests pin
  the guides; the desktop app's tests drive the Loops section with a
  fake API, and its Rust tests use a fake `ws`.
- **Unverified:** that launchd's 60 s `StartInterval` and the app's tick
  never start the same run twice (expected, from the lock; checked by a
  test that runs two ticks against one store); and that a run started
  right after a full wake keeps working when the Mac sleeps again (as
  with `ws continue`, a turn interrupted by sleep is `idle` afterwards
  and isn't retried by `ws loop`).

## References

[^launchd]: launchd.plist(5), macOS 26.6.2 (25G83), read 2026-11-05. `StartInterval`: "This optional key causes the job to be started every N seconds. If the system is asleep during the time of the next scheduled interval firing, that interval will be missed due to shortcomings in kqueue(3). If the job is running during an interval firing, that interval firing will likewise be missed." `StartCalendarInterval`: "The semantics are similar to crontab(5) in how firing dates are specified … Unlike cron which skips job invocations when the computer is asleep, launchd will start the job the next time the computer wakes up."
[^bench-list]: Measured 2026-11-05 on an Apple M4, macOS 26.6.2, `ws` 314a354, Claude Code 2.1.318: `/usr/bin/time -p ws list --json >/dev/null`, 3 runs, real 0.45 s, 0.50 s, 0.40 s. It includes the removal pass and `claude agents`; the number of workspaces on the machine skews it.
[^claude-sched]: Observed 2026-11-05 in Claude Code 2.1.318's built-in skills and tools: `/loop` "Run a prompt or slash command on a recurring interval" inside the current session; `CronCreate` schedules prompts for the running session; `/schedule` creates "scheduled cloud agents (routines) that execute on a cron schedule". (secondary: tool descriptions, not documentation)
