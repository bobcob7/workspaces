# 0024. Read Claude Code sessions by `kind`, `pid`, `status`, and exit status, through one client that requires Claude Code 2.1.302

- Status: Accepted (2026-10-19); `claude attach` and `--bg` process group amended by [0025](0025-ws-claude-session-hand-off.md) (2026-10-19)
- Date: 2026-10-19

## Context

### Problem

[0015](0015-workspace-lifetime-follows-session.md) and
[0018](0018-restart-claude-daemon.md) build `ws claude`, workspace removal,
`ws rm`, `ws restore`, `ws restart`, and `ws list` on Claude Code's CLI:
`claude --bg`, `claude agents --json`, `stop`, `rm`, `respawn`, and
`daemon`. Their facts were measured on Claude Code 2.1.302. The installed
version is now 2.1.303, and a component that runs these commands has to
decide how to read their output: which entries are sessions, when one is
running, working, or still starting, how a new session's id is found, where
its files are, and how failures are told apart. `ws claude` also passes the
user's own options to `claude --bg` after its own.

How does `ws` read and run Claude Code's CLI, and which Claude Code versions
does it accept?

### What a solution needs

1. **Never mistake a session's state.** A wrong "gone" removes a workspace
   ([0015](0015-workspace-lifetime-follows-session.md)); a wrong "running"
   or "working" skips a restore or a respawn.
2. **Tell failures apart** as [0016](0016-output-contract.md)'s codes need:
   `claude_missing`, `claude_untrusted`, `claude_failed`.
3. **Survive small changes** in Claude Code's output between releases.
4. **Cheap**: no noticeable time added to a `ws` command.

### Relevant facts

**Listing sessions** (Claude Code 2.1.303):

| Check | Result |
| ----- | ------ |
| Entries in `claude agents --json --all`[^bench-list] | background sessions have `kind: "background"`, a short `id`, the full `sessionId`, `name`, `cwd`, `startedAt` (milliseconds since the epoch) and `state`; interactive sessions have `kind: "interactive"` and no `id` or `state` |
| `--cwd <dir>`[^bench-cwd] | also lists interactive sessions in `<dir>`, though the help says "Show only background sessions started under <path>"[^agents-help]; lists sessions in subdirectories of `<dir>`; resolves symlinks in `<dir>` (`/tmp/…` matched a session listed as `/private/tmp/…`) |
| `pid` and `status`[^bench-start] | absent right after `claude --bg` returned; present 3 s later, with `status: "idle"` |
| `state` after `claude stop`[^bench-stop] | `stopped`, with no `pid`. On 2.1.302 it was `done`[^bench-0015-rm] |
| `state` after `claude respawn` of a session with no conversation[^bench-stop] | `working` with `status: "idle"`, unchanged 10 s later. Another user session, not started for this test, also showed `working` with `status: "idle"`[^bench-list] |
| `startedAt`[^bench-stop] | changed when the session got a process and again on respawn; after `claude stop` it showed the value from before the first process started |
| A session left without a process by an earlier service restart[^bench-list] | `state: "blocked"`, no `pid` |
| A new session started without a prompt, 4 s after `claude --bg`[^bench-review] | `state: "blocked"` with a `pid` and `status: "idle"` |

**Configuration directory.** With `CLAUDE_CONFIG_DIR` set, Claude Code
keeps its files under that directory instead of `~/.claude`, and the
evidence is that the jobs directory moves with them[^bench-config-dir].

**Passing `--settings` twice.** `claude` declares `--settings` as a plain
option with one value and no handler that combines repeats[^settings-decl];
the argument parser it uses keeps the last value of such an
option[^commander-repeat]. A user's `--settings` after `ws`'s would
replace `ws`'s rather than add to it. Not tested with a live session.

**Starting a session.** `claude --bg -n <name> --settings <json>` with no
prompt, in a directory below a trusted one, exited 0 in 0.42 s and printed
`backgrounded · <id> · <name> (idle — send a prompt to start)` followed by
four indented hint lines naming `claude agents`, `attach`, `logs`, and
`stop`[^bench-start]. In an untrusted directory it exited 1, printed nothing
on stdout, and printed on stderr "Workspace not trusted. Run `claude` in
<dir> once and accept the trust prompt, then retry."[^bench-trust]

**Stopping, removing, respawning**[^bench-stop]:

| Command | Exit | Output |
| ------- | ---- | ------ |
| `claude stop <id>` on a running session | 0 | `stopped <id>` |
| `claude stop <id>` on a stopped session | 0 | `stopped <id>` |
| `claude respawn <id>` | 0 | `respawned <id>` |
| `claude rm <id>` on a running session | 0 | `removed <id>`; the process and `~/.claude/jobs/<id>` were gone, the directory stayed |
| `stop`, `rm`, or `respawn` with an unknown id | 1 | stderr `No job matching '<id>'`, followed for `stop` by ". Run 'claude agents' to list running sessions." |

**The CLI's surface.** `claude --help` doesn't list `daemon`; `claude daemon
--help` documents `run`, `status`, and `stop` with `--any` and
`--keep-workers`[^daemon-help]. `claude respawn` now also takes
`--all`[^respawn-help]. `claude daemon status` prints unindented
`pid:`, `version:`, `uptime:` (as `56504s`), and `origin:` lines, and
indented detail below `bg sessions:`[^bench-status]. With no service
running it prints `not running` as its first line and exits
1[^bench-not-running]. The service Claude Code starts on demand runs in its
own session with standard input and output on `/dev/null`, standard error
in a log file, and the home directory as its working
directory[^bench-daemon-proc].

**Versions.** This machine also has Claude Code 2.1.84 installed by
Homebrew, behind 2.1.303 on `PATH`. In 2.1.84, `claude --help` has no
`--bg`, `claude agents` is "List configured agents", and `claude respawn
--help` prints the top-level help, as for an unknown command[^bench-old].
`claude --version` prints `2.1.303 (Claude Code)` and took 0.00 to 0.02 s;
`claude agents --json --all` took 0.10 to 0.13 s[^bench-time].

## Decision

We will run Claude Code's CLI from **one component, the Claude Code
client**, as git runs only through the git runner
([0003](0003-use-git-cli.md)), and the client will:

1. **Check the version once per `ws` process** with `claude --version`, and
   refuse anything older than **2.1.302**, the oldest version these
   behaviours were measured on, naming the `claude` found on `PATH` in the
   message. A pre-release suffix is ignored. A failure is `claude_failed`,
   like git's version check; `claude` missing from `PATH` is
   `claude_missing`.
2. **Count only `kind: "background"` entries** as sessions, identified by
   their short `id`. Interactive entries are ignored, with or without
   `--cwd`.
3. **Read a session from `pid`, `status`, and `startedAt`, never from
   `state`:**
   - it **has a process** when it has a `pid`;
   - it is **working**, in the middle of a turn, when it has a `pid` and
     its `status` is `busy`. A session with no `pid` is never working;
   - it is **starting** when it has no `pid` and started less than about
     10 s ago. `ws restore` doesn't respawn it, and `ws list` shows it as
     `starting`;
   - otherwise, with no `pid`, it is **stopped**.

   `state` is passed through as Claude Code reports it, for display only,
   and callers accept values they don't know, as
   [0016](0016-output-contract.md) asks of `ws`'s own consumers.
4. **Take a new session's id from `claude --bg`'s `backgrounded · <id> · …`
   line**, skipping any lines before it. If there is no such line, fall back to
   [0015](0015-workspace-lifetime-follows-session.md)'s lookup,
   `claude agents --json --all --cwd <workspace>`, keeping only the session
   whose `cwd` is the workspace itself, symlinks resolved, and failing
   unless there is exactly one.
5. **Decide success by exit status.** What `stop`, `rm`, and `respawn`
   print isn't checked. A failure is told apart by the start of stderr:
   "Workspace not trusted." is `claude_untrusted`; "No job matching " means
   the session doesn't exist, which callers such as `ws rm` may treat as
   already removed; anything else is `claude_failed`, with stderr in the
   error's details.
6. **Run claude with no stdin**, so a prompt can't be read from `ws`'s
   input, and with `ws`'s environment unchanged.
7. Look for a session's directory in `$CLAUDE_CONFIG_DIR/jobs/<id>` when
   `CLAUDE_CONFIG_DIR` is set and not empty, and in
   `$HOME/.claude/jobs/<id>` otherwise, both from that environment.
8. **Start a background service the way Claude Code starts its own**: in
   its own session, with standard input, output, and error on `/dev/null`,
   and the home directory as its working directory, so it holds nothing of
   `ws`'s, such as a workspace that is later removed.
9. **Refuse a user's `--settings`.** If the options `ws claude` passes on
   to `claude` contain `--settings`, before any `--` of their own, `ws`
   fails with `usage` (exit 2) without running `claude`, and says to put
   the settings in a settings file instead.

### Why this option

1. **State.** `pid` is the one field whose meaning held across every
   measurement, and `status` was `idle` or `busy` whenever there was a
   `pid`; `state` read `stopped` where 0015 recorded `done`, `working` on
   an idle session, and `blocked` on a new, idle one. A new session has no
   `pid` for its first seconds[^bench-start][^bench-review]; 10 s covers
   that with room to spare, so `ws restore` doesn't respawn a session that
   is about to get its process, while a session stopped for longer is
   still restored. Interactive sessions have no `id`, so
   counting them would break the id-based checks. The printed id avoids
   `--cwd`'s subdirectory and interactive matches.
2. **Failures.** The two stderr prefixes are the only ones `ws` acts on;
   everything else keeps Claude Code's own message.
3. **Change.** Only the `backgrounded` line of `--bg`'s output, two stderr
   prefixes, and `daemon status` are text `ws` parses, and the first has a
   fallback. The version
   floor stops a much older `claude`, which may read a subcommand as a
   prompt, from being driven at all.
4. **Cheap.** The version check adds at most 0.02 s, once.
5. **Settings.** Refusing `--settings` keeps the setting that makes the
   agent edit the workspace in place[^settings-decl][^commander-repeat],
   and a settings file reaches the session as well.
6. **Configuration directory.** Following `CLAUDE_CONFIG_DIR` keeps the
   jobs-directory signal pointed where Claude Code writes, so a session isn't
   taken for gone because `ws` looked in the wrong place.

The weak points: the stderr prefixes and the `--bg` line are human-oriented
text that a release could reword, which would turn `claude_untrusted` into
`claude_failed`; the floor refuses releases older than 2.1.302 that might
work; a session that loses its process within 10 s of starting shows as
`starting` rather than `stopped` until the 10 s pass; and a user who wants
per-run settings must write a file.

## Alternatives

### Follow 0015 and 0018 as written

Find a new session with `--cwd`, and read `state` for running or idle.

**Pros**

- Parses JSON only, no human-oriented text.
- No new decision beyond the accepted ADRs.

**Cons**

- `--cwd` also returns interactive sessions and sessions in
  subdirectories[^bench-cwd].
- `state` stayed `working` on an idle, respawned session[^bench-stop], and
  reads `stopped` rather than `done` after `claude stop`.

### Take the id only from `claude --bg`'s output

**Pros**

- One command; no listing after the start.

**Cons**

- A reworded first line would fail every `ws claude`, with no second way to
  find the session.

### Respawn every session without a `pid`

**Pros**

- No time window, and no clock in the rule.

**Cons**

- `ws restore` run right after `ws claude` would respawn a session that
  is still getting its first process[^bench-start].

### Merge or pass through the user's `--settings`

**Pros**

- The user can pass settings for a single run.

**Cons**

- Passed through, theirs replaces `ws`'s[^commander-repeat] and the agent
  tries to create its own worktree again
  ([0015](0015-workspace-lifetime-follows-session.md)).
- Merging means reading the user's file or JSON and writing a combined one,
  for a case a settings file already covers.

### Always look in `~/.claude/jobs`

**Pros**

- One place, as 0015 describes it.

**Cons**

- Wrong when `CLAUDE_CONFIG_DIR` is set[^bench-config-dir], which would
  leave one of the two "gone" signals reading "gone" for every session.

### No version floor

**Pros**

- No extra command, and no refusal of an older release that happens to
  work.

**Cons**

- Claude Code 2.1.84 has no `--bg`, gives `agents` another meaning, and
  doesn't know `respawn`[^bench-old], so the first sign of a stale
  `claude` on `PATH` would be a confusing failure partway through a command.

## Consequences

- `ws` depends on two stderr prefixes, the `--bg` `backgrounded` line, and
  `daemon status`'s `pid:` and `not running` lines. Unit tests
  with a fake `claude` pin each one; when Claude Code changes them, those
  tests and this ADR are updated together.
- `ws restart` ([0018](0018-restart-claude-daemon.md)) waits for sessions
  that are working in the sense above: `status` `busy`.
- `ws list` shows an agent as `working`, `idle`, `starting`, or `stopped`
  by the rules above, instead of Claude Code's `state` values `blocked` and
  `done`.
- `ws claude`'s page documents the `--settings` refusal.
- Conformance: a lint rule fails the build if any other component starts
  `claude`; unit tests with a fake `claude` cover the version floor, the
  `kind` filter, the id from `--bg` and its fallback, the stderr
  classification, `daemon status` parsing, the working and starting rules
  with an injected clock, the jobs directory with and without
  `CLAUDE_CONFIG_DIR`, and the `--settings` refusal.
- **Unverified:** that a session started under another `CLAUDE_CONFIG_DIR`
  gets its jobs directory there (the evidence is indirect[^bench-config-dir]);
  that `--settings` given twice keeps the last[^commander-repeat]; whether
  `startedAt` is the process's start time in general. To be checked when
  Lifetime and `ws restart` are built.
- 0015's status line notes this amendment: a new
  session's id comes from `claude --bg`'s output, with 0015's lookup as the
  fallback; a session is judged by `pid`, `status`, and `startedAt`, not
  `state`, and `ws restore` skips one that is starting; the jobs directory
  follows `CLAUDE_CONFIG_DIR`; and `ws claude` refuses a user's
  `--settings`.
- 0018's status line notes this amendment: a
  session is working when its `status` is `busy`, not when its `state` is
  `working`.

## References

[^agents-help]: Claude Code CLI 2.1.303, `claude agents --help` (2026-10-19). `--cwd <path>`: "Show only background sessions started under <path>". `--json`: "Print active sessions (interactive and background) as a JSON array and exit".
[^daemon-help]: Claude Code CLI 2.1.303, `claude --help` and `claude daemon --help` (2026-10-19). The first lists `agents`, `attach`, `logs`, `respawn`, `rm`, and `stop|kill` under "Commands", and no `daemon`. The second: "run [json-path]   Run the supervisor in the foreground", "status            Show daemon pid, version, uptime", "stop              Shut down the supervisor and terminate background sessions", "--any           also stop a transient (non-service) daemon", "--keep-workers  leave detached sessions running".
[^respawn-help]: Claude Code CLI 2.1.303, `claude --help` (2026-10-19). "respawn [options] [id]   Restart a background session, or all of them with --all, so it runs the current Claude Code version".
[^bench-list]: Measured 2026-10-19 on an Apple M4, macOS 26.6.2, Claude Code 2.1.303. `claude agents --json --all` listed 5 entries: 2 with `kind: "background"` and an `id`, 3 with `kind: "interactive"`, no `id`, no `state`, and a `status` of `idle` or `busy`. One background entry had no `pid` and `state: "blocked"`; the other had a `pid`, `status: "idle"`, and `state: "working"`. Its `startedAt`, 2026-10-19T00:37:59Z, matched `ps -o lstart` for its process.
[^bench-cwd]: Measured 2026-10-19, same machine. `claude agents --json --all --cwd <repo dir>` returned this machine's interactive session in that directory (`kind: "interactive"`). With one test session in `<trusted>/wsclient-verify`: `--cwd` of that directory and of its parent returned it; `--cwd` of `<trusted>/wsclient-verify-sibling` and of a missing subdirectory returned `[]`; the same path through `/tmp` instead of `/private/tmp` returned it, with `cwd` under `/private/tmp`.
[^bench-start]: Measured 2026-10-19, same machine. In a new directory below one whose trust prompt had been accepted, `claude --bg -n ws-client-verify --settings '{"worktree":{"bgIsolation":"none"}}' </dev/null` exited 0 in 0.42 s (`/usr/bin/time -p`, one run), stderr empty. Immediately after, `claude agents --json --all --cwd <dir>` listed it with `state: "blocked"` and no `pid` or `status`; 3 s later it had a `pid`, `status: "idle"`, and a later `startedAt`.
[^bench-trust]: Measured 2026-10-19, same machine. `claude --bg -n ws-verify-untrusted </dev/null` in a new directory under the session scratch directory, which has no trusted parent: exit 1 in 0.33 s, empty stdout, the quoted stderr. No session was listed afterwards.
[^bench-stop]: Measured 2026-10-19, same machine, on the session from [^bench-start], each command run once with stdin from `/dev/null`. The table records exit status, stdout, and stderr. After `stop`, the listing showed `state: "stopped"`, no `pid`, and the `startedAt` from before its first process. After `respawn`, a new `pid`, a new `startedAt`, `status: "idle"`, `state: "working"`, still so 10 s later. After `rm`, `claude agents --json --all --cwd <dir>` returned `[]`, `ls ~/.claude/jobs/<id>` failed, `ps -p <pid>` found no process, and the directory existed. Then `rm`, `stop`, and `respawn` of the same id, and `stop zz-ws-no-such-session`, each exited 1 with the quoted stderr.
[^bench-0015-rm]: See [0015](0015-workspace-lifetime-follows-session.md), footnote `bench-bg-rm` (Claude Code 2.1.302): "After `claude stop <id>`, `claude agents --json --all` listed the session with `state: done` and no `pid`."
[^bench-status]: Measured 2026-10-19, same machine. `claude daemon status` exited 0 and printed `pid:     49849`, `version: 2.1.303`, `uptime:  56504s`, "origin:  transient — started on-demand by `claude` (pid 49109) in <dir>", `config:` and `log:` lines, then `bg sessions:` with indented lines such as `sock dir:     /tmp/cc-daemon-501/4f1400ee` and `bg workers:   1 running (control.sock), 1 in roster.json`.
[^bench-old]: Measured 2026-10-19, same machine. `/opt/homebrew/bin/claude --version`: `2.1.84 (Claude Code)`. Its `--help` contains no `--bg`; `claude agents --help` reads "List configured agents"; `claude respawn --help` printed the top-level usage, "Usage: claude [options] [command] [prompt]".
[^bench-review]: Measured 2026-10-19, same machine, Claude Code 2.1.303, in a new directory below a trusted one. `claude --bg -n ws-review-verify --settings '{"worktree":{"bgIsolation":"none"}}' </dev/null`: exit 0, 0.42 s, the `backgrounded` line (bytes `c2 b7` for each `·`, no terminal escapes with stdout to a file) and four hint lines; 4 s later `claude agents --json --all --cwd <dir>` listed it with a `pid`, `status: "idle"`, `state: "blocked"`. `claude stop` twice, then `claude rm`: exit 0 with `stopped <id>`, `stopped <id>`, `removed <id>`; `~/.claude/jobs/<id>` was then gone; `stop` and `rm` again exited 1 with the quoted stderr.
[^bench-not-running]: Measured 2026-10-19, same machine, Claude Code 2.1.303. `CLAUDE_CONFIG_DIR=<new empty dir> claude daemon status </dev/null`: stdout began `not running`, then a `bg sessions:` block with `sock dir: /tmp/cc-daemon-501/3352915c` and "control.sock: unreachable"; exit 1. The machine's own service kept running.
[^bench-daemon-proc]: Measured 2026-10-19, same machine. `lsof -p` on the service from [^bench-status]: fd 0 and 1 `/dev/null`, fd 2 a `stderr.log` under `$TMPDIR/cc-daemon-*`, cwd the home directory; `ps` showed PPID 1 and a process group of its own, started as `claude daemon run --json-path … --log-file … --origin transient --spawned-by {…}`.
[^bench-config-dir]: Measured 2026-10-19, same machine. The run in [^bench-not-running] reported `sock dir: /tmp/cc-daemon-501/3352915c`, while the default configuration's service uses `/tmp/cc-daemon-501/4f1400ee`[^bench-status]. In the 2.1.303 binary (`strings`), the jobs directory is `join(<config home>, "jobs")`, where the same config home gives `projects` and `ide`, and elsewhere `projects` is `CLAUDE_CONFIG_DIR ?? join(homedir(), ".claude")`. Not tested by starting a session under another configuration directory.
[^settings-decl]: Claude Code CLI 2.1.303, the installed binary read with `strings` (2026-10-19). The top-level command declares `.option("--settings <file-or-json>","Path to a settings JSON file or a JSON string to load additional settings from")`, with no argument-processing function.
[^commander-repeat]: Commander.js README, "Custom option processing." <https://github.com/tj/commander.js#custom-option-processing> (accessed 2026-10-19). Its example of a repeatable option needs a custom `collect` function that appends each value to the previous ones; without one, each occurrence sets the option's value again, so the last occurrence wins.
[^bench-time]: Measured 2026-10-19, same machine, Claude Code 2.1.303. 6 runs each under `/usr/bin/time -p`: `claude --version` 0.00 to 0.02 s; `claude agents --json --all` with 5 entries 0.10 to 0.13 s, median 0.11 s.
