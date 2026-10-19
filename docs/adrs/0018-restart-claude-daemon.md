# 0018. `ws restart` swaps Claude Code's background service and respawns each session once it's idle

- Status: Accepted (2026-10-18); "working" amended by [0024](0024-claude-code-client.md) (2026-10-19)
- Date: 2026-10-18

## Context

### Problem

Running agents ([0015](0015-workspace-lifetime-follows-session.md)) need
things that change while they run:

- **Environment variables.** A token such as `GH_TOKEN` expires and is
  replaced in `~/.zshrc`.
- **MCP servers** added to or changed in Claude Code's config.
- **A new Claude Code version.**

For a foreground `claude`, the maintainer edits the source (for example
`~/.zshrc`), quits, and resumes the conversation from a new shell. A
background session has no shell of its own to restart from.

How does a person bring every running agent up to date, without losing
their conversations or interrupting their work?

### What a solution needs

1. **Picks up all three**: new environment, new MCP config, new Claude
   Code binary.
2. **Doesn't interrupt work in progress**, and keeps conversations.
3. **One command**, with the environment taken from the shell it's run in,
   as with a foreground `claude`.
4. **Touches nothing but Claude Code's background sessions**, and brings
   back every one that was running.

### Relevant facts

**Where a background session's environment comes from.** Claude Code's
background service (its "daemon") starts on demand: "the daemon runs on
demand and exits when the last client disconnects"[^daemon-help].
`claude daemon status` names the process that started it; on the test
machine that was a `claude` in another project, 19.7 hours
earlier[^bench-daemon-env]. Sessions run as processes the daemon starts,
some of them in advance[^bench-bg-lock]:

| Check[^bench-daemon-env] | Variable set only in the launching shell |
| ------------------------ | ---------------------------------------- |
| Session started with `claude --bg` under the running daemon | absent from the session's process |
| Claude Code's shell snapshot for the Bash tool | exports only `PATH` |

**Stopping the daemon** (`claude daemon stop --any`)[^daemon-help]:

| | Without `--keep-workers` | With `--keep-workers` |
| - | ------------------------ | --------------------- |
| Running sessions[^bench-daemon-restart][^bench-keep-workers] | stopped: "terminated 3 background sessions" | keep running, same processes |
| A session in the middle of a 100-second command[^bench-keep-workers] | (not tested) | finished the command; its output showed the old environment |
| Interactive `claude` sessions[^bench-daemon-restart] | unaffected | unaffected |

With `--keep-workers`, each session "waits about a minute for a new
supervisor to reconnect to it, and stops if none does"[^agent-view-supervisor].

**Starting a new daemon**[^bench-daemon-restart][^bench-keep-workers][^bench-daemon-run]:

| Command | Starts a daemon? | Its environment |
| ------- | ---------------- | --------------- |
| `claude agents --json` | no; status then warned "supervisor not running but 3 workers in roster" | |
| `claude respawn <id>` | yes, "Starting background service…" | the shell that ran `claude respawn` |
| `claude daemon run`, detached | yes; status shows origin "foreground" | the shell that started it |

Either new daemon re-adopted the kept sessions ("3 running") without
restarting them.

**Respawning a session.** `claude respawn` restarts a session "running or
stopped, for example to pick up an updated Claude Code binary. A session
that has a saved conversation resumes it"[^agent-view-shell].

- The respawned process had the new daemon's environment, and so did its
  Bash commands[^bench-daemon-restart].
- A session that ran Claude Code 2.1.237 came back on 2.1.302, the
  installed version[^bench-daemon-restart].
- Respawning kills the session's process: the daemon logged
  "bg settled 8fff6217 (killed)" and then gave the session a new
  process[^bench-respawn-kill].

**Other ways to change environment variables** were tested on a running
session[^bench-live-env]. A `CLAUDE_CODE_SHELL_PREFIX` wrapper that sources
a file before every Bash command, and `env` in `.claude/settings.local.json`
in the session's directory, both took effect on the next command. `env` in
a `--settings` file and `CLAUDE_ENV_FILE` didn't.

## Decision

We will add **`ws restart`**, which:

1. Lists background sessions with `claude agents --json --all` and records
   every one that has a process, whether `ws` started it or not.
2. Runs `claude daemon stop --any --keep-workers`. The sessions keep
   running.
3. Starts a new daemon at once with a detached `claude daemon run`, so it
   inherits `ws`'s environment. It checks with `claude daemon status` that
   the daemon is its own; if another `claude` got there first, it warns that
   the environment may be stale and suggests running `ws restart` again.
4. Respawns every recorded session that isn't `working`. Each one that is
   `working` is respawned as soon as it stops working; `ws` waits for them,
   checking every few seconds, and shows which are left.
5. Prints each session and whether it was restarted; `--json` reports it per
   [0016](0016-output-contract.md).

Options:

- `--no-wait` respawns only the sessions that aren't `working`, then exits.
  The others keep the old environment until a later `ws restart`.
- `--force` respawns `working` sessions at once, interrupting their current
  turn.

A session whose process started before the current daemon still has the old
environment. `ws list` marks it `stale`, so an interrupted `ws restart` can
simply be run again.

Run it from a shell that already has the change: a new terminal, or after
`source ~/.zshrc`. Sessions that weren't running stay stopped;
[`ws restore`](0014-command-set.md) is the way to bring those back.
Interactive `claude` sessions aren't affected.

### Why this option

1. **All three changes.** A new daemon from the current shell gives fresh
   environment variables, and respawned sessions are new processes on the
   installed binary.
2. **No interrupted work.** With `--keep-workers`, a session in the middle
   of a command finished it; respawning only idle sessions means none loses
   a turn. `--force` is there for when waiting isn't worth it.
3. **One command**, matching the foreground habit: change the source, then
   restart from a fresh shell.
4. **Only background sessions**, and every running one comes back, including
   ones `ws` didn't start, since they all share the daemon.

The weak points: `ws restart` may wait a long time for a busy agent
(`--no-wait` and `stale` cover that); every background session on the
machine is restarted, not only `ws`'s; and if another `claude` starts a
daemon between the stop and `ws`'s `claude daemon run`, that process's
environment wins, which `ws` can detect but not prevent.

## Alternatives

### Stop the daemon without `--keep-workers`

**Pros**

- One step fewer: the first `claude respawn` starts the new
  daemon[^bench-daemon-restart].

**Cons**

- Every running session stops at once, so busy agents lose their current
  turn and need a "continue"[^agent-view-reboot].

### A shell-prefix wrapper that reloads a file before every command

`ws claude` sets `CLAUDE_CODE_SHELL_PREFIX` to a script that sources
`$CONFIG_DIR/env.sh` before each Bash command.

**Pros**

- Live: the next command sees the new value, with no restart at
  all[^bench-live-env].
- `env.sh` can fetch a token itself, e.g. `GH_TOKEN="$(gh auth token)"`.

**Cons**

- Environment only: no new MCP servers or Claude Code version.
- Replaces any `CLAUDE_CODE_SHELL_PREFIX` the user already has.

It can be added later for tokens that change often.

### `env` in `.claude/settings.local.json` inside each workspace

**Pros**

- Claude Code reloads it live[^bench-live-env].

**Cons**

- `ws` would write inside the workspace, against
  [0012](0012-workspace-layout.md), and keep secrets in plain text in each.
- Environment only.

### `claude respawn` alone, without replacing the daemon

**Pros**

- Nothing but the respawned sessions is touched.

**Cons**

- Respawned processes come from the old daemon, with its old
  environment[^bench-daemon-env].

## Consequences

- Adds `ws restart` to [0014](0014-command-set.md)'s command set; on
  acceptance, 0014's status line notes it.
- [0016](0016-output-contract.md) lists the error code `sessions_busy`
  (exit 4) for an earlier draft of this ADR that refused to restart busy
  sessions. This decision doesn't use it.
- `ws restart` affects background sessions outside `ws`. Its output and
  `ws llm restart` say so, and the agent guide tells agents never to run it
  unless told to.
- **Unverified**, to be checked when `ws restart` is built:
  - that a changed MCP config is picked up after a respawn. A respawned
    session is a new process, which is when MCP servers start;
  - what respawning a `blocked` session does to a pending permission
    prompt;
  - whether a daemon started with `claude daemon run` exits when idle, as
    an on-demand one does.
- Conformance: tests with a fake `claude` on `PATH` that `ws restart` stops
  the daemon with `--keep-workers`, respawns non-`working` sessions at once
  and `working` ones only after they stop working, respawns nothing that
  had no process, and warns when `claude daemon status` names a different
  daemon.

## References

[^daemon-help]: Claude Code CLI 2.1.302, `claude daemon --help` (2026-10-18). "Service install is disabled in this version — the daemon runs on demand and exits when the last client disconnects." `run`: "Run the supervisor in the foreground (default when piped)". `stop`: "Shut down the supervisor and terminate background sessions"; `--any`: "also stop a transient (non-service) daemon"; `--keep-workers`: "leave detached sessions running".
[^agent-view-shell]: Claude Code docs, "Agent view," section "Manage sessions from the shell." <https://code.claude.com/docs/en/agent-view> (accessed 2026-10-18). `claude respawn <id>`: "Restart a session, running or stopped, for example to pick up an updated Claude Code binary. A session that has a saved conversation resumes it".
[^agent-view-supervisor]: Same page. When the supervisor stops, "each session's process waits about a minute for a new supervisor to reconnect to it, and stops if none does."
[^agent-view-reboot]: Same page. An interrupted session "restarts from its saved conversation. To pick the interrupted work back up, send it a reply asking it to continue."
[^bench-bg-lock]: See [0015](0015-workspace-lifetime-follows-session.md), footnote `bench-bg-lock`: the session process was `claude bg-spare`, child of `claude bg-pty-host`, child of `claude daemon run`, and had been started 18 h 30 min before the session.
[^bench-daemon-env]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, Claude Code 2.1.302. `claude daemon status`: "uptime: 71033s", "origin: transient — started on-demand by `claude` (pid 13120) in /Users/bobcob7/Dev/go/refinery". `WS_DAEMON_TEST=fresh1 claude --bg '<prompt>'`, then `ps -E -ww -p <session pid>`: no `WS_DAEMON_TEST` (`ps -E` checked first on a shell started with a test variable, which it showed). Newest file in `~/.claude/shell-snapshots/`: one `export` line, for `PATH`; variables exported in `~/.zshrc`, such as `TMUX_CONF`, absent.
[^bench-daemon-restart]: Measured 2026-10-18, same machine. With `WS_DAEMON_TEST=fresh1` exported: `claude daemon stop --any` printed "stopped (terminated 3 background sessions)" in 0.29 s; `claude agents --json --all` then listed the background sessions with no `pid`, and the three interactive `claude` processes were still alive. `claude respawn <test id>`: "Starting background service…", "respawned <id>", 0.79 s; `claude daemon status` then showed "uptime: 3s" and the respawn's pid as origin, and `ps -E` on the session's new process showed `WS_DAEMON_TEST=fresh1`. A new `claude --bg` started with `env -u WS_DAEMON_TEST` and asked to run `printenv WS_DAEMON_TEST` wrote `fresh1`. An existing session whose process tree ran from `versions/2.1.237` before the stop ran from `versions/2.1.302` after `claude respawn`.
[^bench-keep-workers]: Measured 2026-10-18, same machine. Under a daemon with `WS_DAEMON_TEST=fresh1`: one session running `sleep 100; echo "turn-finished WS_DAEMON_TEST=$WS_DAEMON_TEST" >> out.txt` (status `busy`) and one idle. With `WS_DAEMON_TEST=fresh2` exported, `claude daemon stop --any --keep-workers` printed "stopped"; both session processes and a third, unrelated session's process were still alive. `claude agents --json --all` listed them with their old pids, and `claude daemon status` then said "not running" and "supervisor not running but 3 workers in roster — running `claude agents` restarts the daemon and re-adopts still-running sessions". `claude respawn <idle id>` 18 s after the stop: "Starting background service…"; status "3 running (control.sock)"; the busy session kept its pid and `fresh1`, and the respawned one had `fresh2`. The busy session's file then read `turn-finished WS_DAEMON_TEST=fresh1`. Respawning it after it went idle gave its new process `fresh2`.
[^bench-daemon-run]: Measured 2026-10-18, same machine. With `WS_DAEMON_TEST=fresh3` exported: `claude daemon stop --any --keep-workers`, then `nohup claude daemon run &`. 4 s later `claude daemon status` showed that pid, "origin: foreground", and "4 running (control.sock)"; the four session processes kept their pids. `claude respawn <id>` then gave the session's new process `fresh3`.
[^bench-respawn-kill]: Measured 2026-10-18, same machine. `claude respawn` on a session that was `busy` waiting for a background shell task it had started: `~/.claude/daemon.log` recorded "bg settled 8fff6217 (killed)" and, 0.1 s later, "bg claimed-spare 8fff6217 (fleet)". The background shell task outlived the old process and reported its result to the new one.
[^bench-live-env]: Measured 2026-10-18, same machine. Background sessions asked to print variables, `sleep 45`, then print them again, with the source changed during the sleep. `env` in a file passed as `--settings <path>`: unchanged. `CLAUDE_ENV_FILE` set through that file's `env`: unchanged. `env` in `<session dir>/.claude/settings.local.json`: changed. `CLAUDE_CODE_SHELL_PREFIX` pointing at a script that sources a file and then runs the command: changed.
