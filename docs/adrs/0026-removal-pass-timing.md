# 0026. The removal pass runs before every command that reads `CONFIG_DIR`, at most once a minute, except in `ws list`, which runs it every time

- Status: Accepted (2026-10-19)
- Date: 2026-10-19

## Context

### Problem

[0015](0015-workspace-lifetime-follows-session.md) decides that "any `ws`
command first removes workspaces whose session is gone, at most once per
minute", and [0012](0012-workspace-layout.md) and
[0022](0022-workspace-record-before-rename.md) that any `ws` command removes
leftover state. The command pages add two expectations of their own:
[`ws list`](../tools/ws-list.md) says workspaces whose session is gone "are
removed before listing, so they don't appear", and
[`ws rm`](../tools/ws-rm.md) removes the workspaces named on its command
line, one of which the pass at its start may already have removed.

Which commands run the pass, how is "once a minute" kept, does `ws list`
keep to it, and what does `ws rm` report for a workspace the pass removed
just before? Building the pass also raised what `ws list` shows when a
removal fails, what `ws rm` does with a workspace still being handed to
its session or whose recorded session is somewhere else, and how `ws list`
tells that an agent is stale
([0018](0018-restart-claude-daemon.md)).

### What a solution needs

1. **Never remove a workspace whose session still exists**
   ([0015](0015-workspace-lifetime-follows-session.md)).
2. **`ws list` agrees with the pass**: it doesn't show a workspace whose
   session is gone.
3. **Cheap** for commands that don't otherwise run `claude`.
4. **`ws rm`'s result doesn't depend on timing**: the same command line
   gives the same result and exit code whether or not the pass was due.
5. **Simple**: one rule, with as few exceptions as possible.
6. **`ws rm` touches only what is the workspace's own**: never a Claude
   Code session in another directory.

### Relevant facts

- **What a pass costs.** Judging an agent workspace needs `claude agents
  --json --all`, which took 0.110 to 0.132 s; `claude --version`, run once
  per process before any other `claude` command
  ([0024](0024-claude-code-client.md)), took 0.007 to 0.013 s. With one
  agent workspace, `ws cache list` took a median 0.147 s when the pass ran
  and 0.021 s when it didn't[^bench-pass].
- **`ws list` lists sessions anyway.** It shows each agent's state from
  `claude agents --json --all` ([`ws list`](../tools/ws-list.md)); with
  one agent workspace it took a median 0.133 s[^bench-pass].
- **`ws llm`** prints text "embedded in the binary"
  ([0014](0014-command-set.md)); the other commands read config, cache, or
  workspaces under `CONFIG_DIR`.
- **A session is recorded after Claude Code accepts it.** `ws claude`
  writes the workspace's session record once `claude --bg` has returned
  the session's id ([0025](0025-ws-claude-session-hand-off.md)). Run
  right after `claude --bg` returned, `claude agents --json --all` listed
  the new session[^bench-start].
- **Pre-spawned processes.** Claude Code's background service keeps a
  spare process ready, and a new session takes it over: one session's
  process started 8 minutes before the `startedAt` that
  `claude agents --json` reported for it[^bench-spare].
- **Start times from the operating system.** `claude daemon status` gives
  the service's pid, and its uptime in whole seconds
  ([0024](0024-claude-code-client.md)). The operating system records each
  process's start to the microsecond on macOS; a session's process started
  0.3 s after the service's, closer than the uptime can
  tell[^bench-proc-start]. On Linux, `/proc/<pid>/stat` gives a process's
  start in clock ticks since boot[^proc-stat].
- **Directories as Claude Code reports them.** `claude agents --json
  --all` reports a session's working directory with symlinks resolved,
  even for a session started from a path through a
  symlink[^bench-symlink].
- **A hand-off holds the lock.** `ws claude` holds the workspace's lock
  from creation until the session is recorded
  ([0025](0025-ws-claude-session-hand-off.md)), which took 0.4 to 0.6 s.
- **A record names a session only by id.** `ws rm` passes the recorded id
  to `claude stop` and `claude rm`, which act on whichever session has
  that id, wherever it runs.
- **A natural sequence crosses the pass.** After `claude rm <id>`, the
  workspace is still on disk until a pass runs
  ([0015](0015-workspace-lifetime-follows-session.md)). If the next command
  is `ws rm <id>` and the pass is due, the pass removes the workspace before
  `ws rm` looks for it.

## Decision

We will:

1. **Clean up leftover state** ([0022](0022-workspace-record-before-rename.md))
   at the start of every command that reads `CONFIG_DIR`, every time: all
   commands except `ws llm`, `--help`, and `--version`.
2. **Run the removal pass** at the start of the same commands, **at most
   once a minute**. The pass keeps its time as the modification time of a
   hidden file, `state/.removal-pass`, set when a pass starts. A missing
   file, or a time in the future after the clock went back, counts as due;
   with no `state/` directory there are no workspaces, and nothing runs.
3. **`ws list` runs the pass every time**, as part of listing, and sets the
   time.
4. **`ws rm` runs the pass first, like any command.** A WORKSPACE argument
   that names, by id, path, or Claude session id, a workspace this pass
   removed is reported as removed (`"removed": true`, no error), and no
   `claude` command is run for it.
5. **The pass reads every session record before it lists Claude Code's
   sessions**, and lists them, at most once, only when an agent or
   abandoned workspace needs it.
6. **A workspace the pass fails to remove stays listed** by `ws list`,
   with the failure in `warnings`, whether its session was gone or it was
   abandoned.
7. **`ws rm` refuses a workspace still being handed to its session**: one
   with no session record whose lock is held fails with the new error code
   `workspace_busy`, exit 4.
8. **`ws rm` checks the session's directory first.** Before `claude stop`
   and `claude rm`, it lists Claude Code's sessions. If the recorded
   session is listed with a working directory other than the workspace's,
   resolved as the deletion guard resolves it
   ([0012](0012-workspace-layout.md)), it touches neither the session nor
   the workspace and fails that item with the new error code
   `session_mismatch`, exit 4, naming both directories. A session that
   isn't listed is already gone, as before. `--dry-run` makes the same
   check.
9. **`stale` compares process start times.** `ws list` asks the operating
   system when each agent's process and the service's process started,
   the service's pid coming from `claude daemon status`, and marks the
   agent stale when its process started first. If no service runs, or
   either start time can't be read, `stale` is `null`.

### Why this option

1. **Never too early.** Reading the records first means any session
   recorded by then was already accepted by Claude Code, so the listing
   that follows includes it[^bench-start]; a listing taken first could miss
   a session recorded a moment later, leaving only the jobs-directory check
   ([0015](0015-workspace-lifetime-follows-session.md)) between it and
   removal.
2. **`ws list` agrees with the pass** because it is the pass, plus
   printing; running it every time costs little, since `ws list` lists
   sessions anyway[^bench-pass].
3. **Cheap.** Other commands pay the pass's 0.13 s at most once a
   minute[^bench-pass], and only when an agent or abandoned workspace
   exists; shell workspaces need only their lock.
4. **`ws rm` doesn't depend on timing.** Whether the pass removed the
   workspace or `ws rm` did, the result and the exit code are the same.
5. **Simple.** One rule for every command, one exception (`ws list`), and
   the minute kept in a file anyone can inspect.
6. **Only the workspace's own.** A recorded id that Claude Code lists in
   another directory, after a hand edit or a reused id, is refused before
   anything runs; a live check refused it and left both the session and the
   workspace[^bench-rm-mismatch].
7. **What's on disk is shown.** A workspace whose removal failed is still
   on disk, and `ws rm` and its path still find it, so `ws list` shows it,
   as it already showed an abandoned one.
8. **The right error codes.** A hand-off in progress and a session in
   another directory are refusals that protect something, which is what
   exit 4 means ([0016](0016-output-contract.md)); no existing code says
   either (`shell_running` names a shell, and `claude_failed` a `claude`
   command that failed, while here none ran). 0016 lets codes be added
   under an existing exit code.
9. **Stale means what [0018](0018-restart-claude-daemon.md) says**: a
   process that started before the current service. Comparing the
   processes themselves isn't fooled by pre-spawned processes, nor by
   the uptime's whole seconds[^bench-spare][^bench-proc-start], and needs
   no extra program.

The weak points: two commands started in the same instant can both run the
pass. Each removal still goes through the deletion guard
([0012](0012-workspace-layout.md)), a workspace the other pass already
removed, or is removing, counts as removed, and checking whether a lock is
held doesn't make it look held to another check, so the cost is a second
listing, not a wrong removal or a warning[^bench-concurrent]. `ws rm --dry-run` can still remove workspaces through the pass at
its start, as any command can; those are reported as removed, not as
"would remove". A pass that fails to set its time runs again on the
next command, with a warning. `ws rm` of an agent workspace now runs
`claude agents` once more, about 0.11 s[^bench-pass]. Reading start times
needs code for each operating system: macOS and Linux are covered, and
elsewhere `stale` is always `null`.

## Alternatives

### `ws list` keeps to the minute and hides what the pass would remove

**Pros**

- One rule for every command, as 0015 states it.

**Cons**

- `ws list` still runs `claude agents` every time, so nothing is saved, and
  it hides workspaces that are still on disk, which `ws rm` and the
  workspace's path still find.

### No minute: every command runs the pass

**Pros**

- No timestamp file; removal is never delayed.

**Cons**

- Commands that don't use Claude Code take 7 times as long: 0.147 s
  instead of 0.021 s for `ws cache list`[^bench-pass].

### Serialize passes with a lock on the timestamp file

A command takes the timestamp file's lock without waiting before it
checks the time, and skips the pass if another command holds it; `ws list`
waits for it.

**Pros**

- At most one pass at a time, so "once a minute" holds exactly and
  `claude agents` runs once even for commands started together.

**Cons**

- A pass stuck in a slow `claude` command holds every `ws list` behind it.
- More to build and test, for a race whose only cost is a second
  listing[^bench-concurrent].

### `ws rm` runs the pass after its own removals

**Pros**

- Arguments are resolved against the workspaces as they were, with no
  special case for ones the pass removed.

**Cons**

- Departs from 0015's "first", for one command.
- A workspace whose session is gone still costs `claude stop` and
  `claude rm`, each answered "No job matching"
  ([0024](0024-claude-code-client.md)).

### Hide a workspace the pass failed to remove

**Pros**

- `ws list` shows only workspaces whose session exists.

**Cons**

- It hides a workspace still on disk, as the first alternative does.

### `ws rm` waits for a hand-off instead of refusing

**Pros**

- No new error code; the command succeeds once the hand-off finishes.

**Cons**

- A `claude --bg` that hangs holds `ws rm` with it
  ([0025](0025-ws-claude-session-hand-off.md)).

### `ws rm` trusts the recorded id, or reports a mismatch as `claude_failed`

**Pros**

- No extra `claude agents`; or no new error code.

**Cons**

- Trusting the id can stop and remove a session in another directory.
- `claude_failed` (exit 1) says a `claude` command failed, when none ran
  and `ws` refused to protect a session.

### Stale from `startedAt` and the service's uptime

**Pros**

- Everything comes from `claude`; no code for each operating system.

**Cons**

- `startedAt` can be minutes after the process started, so a
  pre-spawned process from before a restart would look
  current[^bench-spare]; and the uptime's whole seconds can't order a
  session started within a second of the service[^bench-proc-start].

### Stale from `ps -o lstart=`

**Pros**

- One command on both operating systems.

**Cons**

- Another process for every agent, and text to parse; `lstart` has whole
  seconds only[^bench-proc-start].

### `ws rm` reports a workspace the pass removed as not found

**Pros**

- No special case: an argument either names a workspace or doesn't.

**Cons**

- `claude rm <id>` followed by `ws rm <id>` exits 3 with
  `workspace_not_found` when the pass was due and 0 when it wasn't.

## Consequences

- 0012's status line notes this amendment: `state/` also holds the hidden
  `.removal-pass` file, which is never a workspace's state.
- 0015's status line notes this amendment: `ws list` runs the removal pass
  every time; other commands that read `CONFIG_DIR` run it at most once a
  minute, timed by `state/.removal-pass`; `ws llm` doesn't run it; `ws rm`
  reports a workspace that pass removed as removed; a workspace the pass
  fails to remove stays listed with a warning; and `ws rm` refuses a
  workspace still being handed over, and checks that the recorded session
  runs in the workspace before stopping and removing it.
- 0016's status line notes this amendment: the error codes
  `workspace_busy` and `session_mismatch` are added under exit 4.
- 0018's status line notes this amendment: `stale` compares the start
  times of the session's process and the service's process, read from the
  operating system, with the service's pid from `claude daemon status`.
- The `ws list`, `ws rm`, and command reference pages say which commands
  run the pass, how `ws rm` reports it, the new refusals, and how `stale`
  is decided.
- Conformance: unit tests with an injected clock check that the pass runs
  when due, not within a minute of the last, and after the clock went
  back; that leftover cleanup runs either way; that `ws list` runs the pass
  when it isn't due; that `ws rm` reports a workspace the pass removed, by
  id, path, or session id, as removed without running `claude`; that every
  session record is read before sessions are listed; that sessions
  aren't listed when no workspace needs them; that a workspace whose
  removal failed is listed; that `ws rm` refuses a hand-off and a session
  listed in another directory, touching nothing, and treats an unlisted
  session as gone; and, with an injected reader of start times, that
  `stale` is `true` only for a process that started before the service's,
  and `null` when no service runs or either start time can't be read.
- **Unverified:** measured on macOS only; the Linux start-time reading is
  unit-tested on its parsing, not run on Linux.

## References

[^bench-pass]: Measured 2026-10-19 on an Apple M4, macOS 26.6.2, Claude Code 2.1.304, Go 1.27.1, git 2.50.1, with a build of `ws` that does what this ADR decides. `WS_CONFIG_DIR` was a new directory below a directory whose Claude Code trust prompt had been accepted, holding one agent workspace with an idle session started without a prompt. `claude` on `PATH` was a shell script that logged its arguments and ran the installed `claude`, except that it answered `claude daemon status` with "not running" and exit 1 instead of querying the service. Each command was run 6 times from Python 3 with `time.perf_counter`, stdin, stdout, and stderr on `/dev/null`: `claude agents --json --all` through the script, min 0.113, median 0.116, max 0.132 s, and directly, 0.110, 0.112, 0.112 s; `claude --version` directly, 0.007, 0.008, 0.013 s; `ws list`, 0.130, 0.133, 0.140 s; `ws cache list` with `state/.removal-pass` set to 1970 before each run, so the pass was due, 0.145, 0.147, 0.149 s; and with it left as the previous run set it, so the pass wasn't due, 0.020, 0.021, 0.022 s.
[^bench-start]: See [0024](0024-claude-code-client.md), footnote `bench-start` (Claude Code 2.1.303): "Immediately after, `claude agents --json --all --cwd <dir>` listed it with `state: "blocked"` and no `pid` or `status`." Repeated 2026-10-19 with Claude Code 2.1.304, in a new directory below a trusted one: `claude --bg -n <name> --settings '{"worktree":{"bgIsolation":"none"}}' </dev/null` exited 0, and `claude agents --json --all` run right after it listed the session with `state: "blocked"` and no `pid` or `status`.
[^bench-concurrent]: Measured 2026-10-19, same machine, Claude Code 2.1.304, with a build of `ws` that does what this ADR decides, `claude` on `PATH` the same kind of logging script, and a new `WS_CONFIG_DIR` each round holding 40 shell workspaces with free locks, 10 agent workspaces whose sessions weren't listed and had no jobs directory, 5 workspaces with no session record, free locks, and state 5 minutes old, and three to keep: a shell workspace and a workspace with no session record, both with their locks held by another process, and one with an unreadable session record. Two `ws --json list` were started together from Python 3, 10 rounds. Every round, both exited 0 in 0.16 to 0.40 s, the 55 removable workspaces were gone and the three others kept, neither listed any workspace but those three, and neither had a warning but the unreadable record's; the script logged 20 `claude --version` and 20 `claude agents --json --all`. An earlier build, whose lock check took the lock exclusively for a moment and which warned when another pass had already deleted an abandoned workspace's directory, listed 0 to 6 of the removed workspaces per command, and once warned "kept abandoned workspace".
[^bench-spare]: Measured 2026-10-19, same machine, Claude Code 2.1.304. A session started with `claude --bg` and no prompt was listed by `claude agents --json --all` with `pid` 88233 and `startedAt` 1792450263813 (17:51:03.813 local time); `ps -o lstart= -p 88233` printed 17:42:51, and its command line was `claude bg-spare --bg-spare <dir>/spare/<id>.claim.sock`.
[^bench-proc-start]: Measured 2026-10-19, same machine. `claude daemon status` printed `pid: 49096` and `uptime: 1818s`. Read with `sysctl` `kern.proc.pid` for each pid, the service's process started at 17:20:49.241404 and the process of the session it ran, pid 49108, at 17:20:49.542630; `ps -o lstart=` printed 17:20:49 for both. A `ws list` built to this ADR, with `claude daemon status` let through to the service, reported that session's replacement process (started 17:59:26, after the service) as `"stale": false`.
[^proc-stat]: Linux man-pages, proc_pid_stat(5). <https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html> (accessed 2026-10-19). "(22) starttime %llu The time the process started after system boot. ... the value is expressed in clock ticks (divide by sysconf(_SC_CLK_TCK))."
[^bench-symlink]: Measured 2026-10-19, same machine, Claude Code 2.1.304. Run from `/tmp/<dir>` (`/tmp` links to `/private/tmp`), with `PWD` holding that path, `claude --bg` started a session that `claude agents --json --all` listed with `cwd` `/private/tmp/<dir>`.
[^bench-rm-mismatch]: Measured 2026-10-19, same machine, with a build of `ws` that does what this ADR decides, a new `WS_CONFIG_DIR`, and a `claude` script that let only listed ids reach `stop` and `rm`. Workspace `mismatch` was given the session record of a session started in workspace `stale-check`. `ws rm mismatch` exited 4 and printed "the workspace's Claude Code session is in another directory: session 71d1ce51 is in <dir>/stale-check, not in workspace mismatch's directory <dir>/mismatch; neither was touched"; the script logged only `--version` and `agents --json --all`, both directories stayed, and the session kept its pid. `ws rm stale-check` then ran `stop` and `rm` and removed it.
