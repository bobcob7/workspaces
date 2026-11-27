# 0085. Each workspace's Claude Code session gets its own config directory, seeded from the user's

- Status: Proposed
- Date: 2026-11-27

## Context

### Problem

Every Claude Code session `ws` starts shares one Claude Code config
directory, `~/.claude` (or `$CLAUDE_CONFIG_DIR`), and one global config
file, `~/.claude.json`. Dozens of agents run at once, each reading and
rewriting the same files. How should `ws` keep concurrent agents from
interfering through that shared, mutable state, without breaking sign-in,
the sessions `ws` lists and supervises, or the desktop's transcript view?

### What a solution needs

1. **Isolation by default**: one agent's writes to Claude Code's mutable
   state (global config, settings, history, caches, locks) don't reach
   another agent.
2. **Nothing `ws` does today breaks**: `ws list`, `wait`, `rm`, `stop`,
   `attach`, `logs`, `restart`, the removal pass, `ws machine`, the
   desktop's conversation view and citation scan, and the environment
   agent.
3. **Sign-in keeps working**, including a token given only in the
   environment (`CLAUDE_CODE_OAUTH_TOKEN`), and never falls over silently.
4. **An explicit opt-out** per machine, per run and per command.
5. **Cleanup** with the workspace, on every removal path.
6. **Works the same on a remote environment** (ADRs 0072, 0073).

### Relevant facts

- Before this change, `ws` started every session with `claude --bg` in
  one place, `App.startAgent` (`internal/cli/claude.go`), used by `ws
  claude`, `ws machine spawn`, loop ticks and `ws review start`. Its
  environment was `ws`'s own (the `claude.New(log, "claude", environ)` call
  in `newApp`, `internal/cli/app.go`).
- `ws` reads Claude Code's directories from the environment:
  `$CLAUDE_CONFIG_DIR/{jobs,projects,.claude.json}`, else under `$HOME`
  (`configDir` in `internal/claude/client.go`, the transcript path in
  `transcript.go`, `configFile` in `trust.go`). It writes `.claude.json` itself, under Claude Code's
  lock, to trust a one-repo workspace's repo (ADR 0028).
- Claude Code's background daemon and `claude agents` are per config
  directory: another `CLAUDE_CONFIG_DIR` has its own daemon socket
  directory and lists only its own sessions[^daemon].
- A daemon costs about 170 MiB and exits once its sessions are gone; one
  `claude agents --json` takes 0.31-0.57 s[^daemon].
- Sessions don't inherit the daemon's `CLAUDE_CODE_OAUTH_TOKEN`[^token]. So a
  `claude --bg` with a new config directory, run by `ws` inside an agent,
  starts a daemon with no token unless `ws` supplies one.
- The shared directory already shows contention: 616 leftover
  `policy-limits.json.stamp.json.tmp.*` files and a 264 KiB `.claude.json`
  with 78 project entries that sessions read and rewrite[^contention].
- Transcripts live in `projects/<cwd with / as ->/`, so two workspaces never
  share a transcript directory; the desktop finds them under the config
  directory's `projects/` (`desktop/src-tauri/src/transcript.rs:86-104`)
  and job state under `jobs/` (`desktop/src-tauri/src/jobs.rs:47-55`).
- The daemon leaves job directories it doesn't own alone: 16 `jobs/`
  entries for 13 live workers[^contention].
- `ws`'s own `CONFIG_DIR` must stay shared: `ws machine` finds its caller
  by the workspace under `CONFIG_DIR/workspaces/`, and trees, results and
  the controller all live there (ADRs 0044, 0046).

## Decision

We will give each workspace's Claude Code session its own config
directory, an **overlay** at `CONFIG_DIR/state/<id>/claude/`, by default,
with `[claude] config_dir` in config.toml, `WS_CLAUDE_CONFIG_DIR` and
`--claude-config-dir` to choose.

**Modes.** `auto` (the default), `workspace` and `shared`:

| Mode | What `ws` does |
| ---- | -------------- |
| `auto` | Seed the overlay and check sign-in in it. Signed in: start the session with `CLAUDE_CONFIG_DIR=<overlay>`. Not signed in: delete the overlay, start in the shared directory as today, and add the warning `Claude Code isn't signed in for a separate config directory (<reason>); the agent shares <base>` to the command's `warnings` |
| `workspace` | The same, but not signed in fails with `claude_signed_out` (exit 1) and removes the workspace, as a failed start does |
| `shared` | No overlay; today's behaviour |

The mode is the first set of: the command's `--claude-config-dir <mode>`
flag (on `ws claude` and `ws machine spawn`; refused with `usage` for
`--runner ws` and with `--env`, where the remote's own setting applies),
then `WS_CLAUDE_CONFIG_DIR`, then `[claude] config_dir`, then `auto`. Loop
ticks and `ws review start` use the environment variable and the key. A
bad value is `config_invalid` (key), or `usage` (flag, variable).

**The base directory** is what the overlay is seeded from:
`$CLAUDE_CONFIG_DIR`, else `$HOME/.claude`. When `$CLAUDE_CONFIG_DIR` is
itself an overlay (it holds the marker file `.ws-overlay`, whose one line
is the base's absolute path), the base is the marker's path. The agent can
write the marker, so a path that isn't absolute and cleaned is ignored and
the directory counts as no overlay. So an agent's
own `ws machine spawn` seeds its child from the user's directory, not from
its parent's overlay. `ws`'s Claude Code client always works against the
base, never an overlay it was started in.

**The overlay** is made fresh at start and holds:

| Entry | How | Why |
| ----- | --- | --- |
| `.ws-overlay` | written, `0600` | marks an overlay and names its base |
| `.claude.json` | a copy of the base's with only `userID`, `oauthAccount`, `hasCompletedOnboarding`, `lastOnboardingVersion`, `installMethod`, `autoUpdates`, `mcpServers`, `theme`, and the `projects` entries of the workspace root and its parents whose `hasTrustDialogAccepted` is true; `0600` | sign-in identity, onboarding, user MCP servers and trust, without the other projects; every write after that is private |
| `settings.json`, `CLAUDE.md`, `keybindings.json` | copied | user instructions and settings apply; an agent that edits them changes only its own copy |
| `.credentials.json`, `hooks/`, `agents/`, `commands/`, `skills/`, `plugins/`, `output-styles/` | symlinked to the base, when the base has them | the user's sign-in and add-ons; large or shared on purpose |
| `projects/`, `jobs/` | symlinked to the base | transcripts and job state stay where the desktop, citations, `ws` and `claude logs` look today; their entries are per working directory and per session, so agents don't collide |
| everything else (`history.jsonl`, `todos/`, `shell-snapshots/`, `file-history/`, `statsig/`, caches, `policy-limits*`, `backups/`, daemon files) | not seeded; Claude Code creates them in the overlay | private per agent |

A base entry that doesn't exist is skipped. Seeding never writes to the
base.

**Sign-in check.** `claude auth status` with `CLAUDE_CONFIG_DIR=<overlay>`
and the start environment below; signed in when its JSON has
`"loggedIn": true`. It doesn't start a daemon[^token].

**The start environment** of an overlay session is `ws`'s environment
plus `CLAUDE_CONFIG_DIR=<overlay>`, `WS_CONFIG_DIR=<CONFIG_DIR>`, and
`CLAUDE_CODE_OAUTH_TOKEN` and `ANTHROPIC_API_KEY` from `secrets.env` when
set there and not in the environment. `WS_CONFIG_DIR` is passed in shared
mode too (audit bug 11). Because the overlay's daemon is started by this
`claude --bg`, these reach the session.

**Sessions in several daemons.** `ws`'s Claude Code client becomes a pool
(`internal/claude/pool.go`) behind the same `claudeClient` interface:

- `Agents` lists the base and every overlay under `CONFIG_DIR/state/*/claude/`
  concurrently (at most 8 at once), merges them, and remembers each
  session's directory.
- `Start` uses the overlay in `StartOptions.ConfigDir`; `Attach`, `Stop`,
  `Remove`, `Respawn`, `Send` and `Logs` use the remembered directory of the
  session id, listing again once on a miss, and the base when it's still
  unknown. A route to an overlay that no longer exists is dropped.
- `Trust` and `Untrust` write the `.claude.json` of the overlay whose
  workspace holds the directory, else the base's. The overlays are found by
  listing `state/*/claude/`, so `Store.Remove` untrusts before it deletes
  `state/<id>/`; after that `Untrust` would reach the base.
- `StopDaemon` stops the base's daemon and each overlay's; `RunDaemon` and
  `DaemonStatus` act on the base (`ws restart`, ADR 0018). Overlay daemons
  start again on `claude respawn`.
- `JobExists`, `UsageLimit`, `Model` and `Recorded` read `jobs/` and
  `projects/`, which are the base's through the symlinks, so they don't
  change.

**Cleanup.** The overlay is inside `state/<id>/`, which every removal path
deletes (`Store.Remove`, the failed-create cleanup and the leftovers pass
in `internal/workspace`). `os.RemoveAll` unlinks the symlinks without following
them, so the base's `projects/`, `plugins/` and the rest stay. The removal
pass stops and removes the session through the pool before deleting the
state, so the right daemon is asked. The overlay's daemon exits by itself
once its session is gone[^daemon].

**Remote environments.** The agent (`ws env agent run`) runs the same
`startAgent` with the remote's `CONFIG_DIR`, environment and
`secrets.env` (ADR 0073), so sessions there get overlays under the
remote's `state/` by the remote's own `[claude] config_dir`. The hello's
`claude: present | signed_out` probe keeps using the base. The controller
neither sends nor stores the mode: `--claude-config-dir` with `--env` is
refused, and `config.set` doesn't allow the key.

**What stays shared, on purpose.** `ws`'s `CONFIG_DIR` (trees, results,
progress, artifacts, roles, loop memory: each already keyed per workspace,
per loop with `--expect-rev`, or a store meant to be shared), and the
symlinked entries above. Those are links, not copies, and an agent runs
as the user: a write through `hooks/`, `skills/`, `agents/`, `commands/`,
`plugins/` or `.credentials.json` changes the user's own files, and so
every later session, overlay or not. The overlay isolates Claude Code's
mutable state and settings, not these entries; it is not a sandbox. Copying
them instead would stop the write-through but go stale when the user
changes them, and plugins and credentials need links anyway.

### Why this option

1. Isolation: every file Claude Code rewrites at the top level of its
   config directory becomes private to the workspace; the shared entries
   are add-ons, sign-in, and directories keyed per session or per working
   directory. Weak spot: `plugins/` stays shared, so `/plugin install` in
   one agent installs for all; that matches "the user's add-ons".
2. Nothing breaks: callers keep the `claudeClient` interface; transcripts
   and jobs stay at their paths, so the desktop needs no change. Weak spot:
   `ws list` now runs one `claude agents` per live overlay, about 0.4 s of
   CPU each, in parallel; with 20 agents a `ws list` takes about 1.5 s
   instead of 0.5 s. Each running agent also has its own 170 MiB daemon.
3. Sign-in: checked before the session starts; `auto` never leaves an agent
   signed out, it falls back with a warning. Weak spot: where the token is
   only in the shared daemon's environment (the ws-online pod), children
   an agent spawns fall back to shared until the token is put in
   `secrets.env`; docs/config.md says so.
4. Opt-out: key, variable and flag.
5. Cleanup: free, through `state/<id>/`.
6. Remote: the same code runs there.

## Alternatives

### Keep one shared config directory (status quo)

**Pros:** no cost; one daemon. **Cons:** the contention measured above
stays, and an agent's change to settings or `.claude.json` reaches every
other agent. Rejected: it's the problem.

### A full private copy of `~/.claude` per workspace

**Pros:** total isolation. **Cons:** copies `projects/` (every transcript;
hundreds of MiB) and `plugins/`; moves transcripts out of the desktop's
reach, so the conversation view, citation scan and `UsageLimit` need to
learn per-session directories, and a transcript is lost when its workspace
is removed. Rejected for the cost and the breakage.

### One overlay per workspace tree (shared by a root and its children)

**Pros:** fewer daemons. **Cons:** children of one tree, which run at the
same time and do the most interfering, still share. Rejected: the
requirement is per agent.

### Only private `HOME` or `TMPDIR`

**Pros:** cheap. **Cons:** `HOME` moves `~/.claude` too (same as an
overlay, but also hides `~/.gitconfig`, `~/.ssh`, `gh` sign-in); `TMPDIR`
doesn't touch the config directory. Rejected.

## Consequences

- New `internal/claude/overlay.go` (seed, base, sign-in check) and
  `internal/claude/pool.go`; `StartOptions` gains `ConfigDir` and `Env`.
- New config key `claude.config_dir` (listed by `ws config list` only when
  config.toml has a `[claude]` table, like the runner's keys, so the
  desktop's Settings fixtures don't change); new variable
  `WS_CLAUDE_CONFIG_DIR`; new flags; new error code `claude_signed_out`.
- `ws claude` and `ws machine spawn` JSON is unchanged except for the
  fallback warning in `warnings`.
- Memory: one Claude daemon per running agent (about 170 MiB each). On an
  8 GiB machine with many agents, `shared` may be the better setting.
- Conformance: `internal/claude/overlay_test.go` checks the overlay's
  entries (copy, link, skipped) and that the base is never written;
  `pool_test.go` checks routing with a fake `claude` that records its
  `CLAUDE_CONFIG_DIR`; `internal/cli` tests check the mode precedence, the
  fallback warning and `claude_signed_out`.
- Follow-up, not done: `claudeStart` and `overlayStart` in `internal/cli`
  mix the mode decision with its effects (seed, sign-in probe, removal,
  warnings). Moving the effects into one `claude.Prepare(mode)` would make
  the fallback rules testable without `App` and give new spawn paths one
  entry point.
- **Unverified here** (no signed-in session can be started in the test
  pod): macOS, where credentials are in the Keychain and Claude Code may
  key them by config directory (the sign-in check then fails and `auto`
  falls back); and how Claude Code rewrites `.credentials.json` on a token
  refresh (by rename, the overlay would keep a private refreshed copy and
  the base a token that may already be rotated). The first run with a
  signed-in Claude Code should check both, and `ws list` on 20 agents.

## References

[^daemon]: Measured 2026-11-27 in the ws-online pod (linux/amd64), Claude Code 2.1.332: `claude daemon status` (default config: sock dir `/tmp/cc-daemon-1000/3bfeb895`, 13 workers; `CLAUDE_CONFIG_DIR=<empty dir>`: `not running`, sock dir `.../f68de010`); `claude agents --json` (13 sessions vs `[]`, also with `jobs/` symlinked to the default); `ps -o rss` of `claude daemon run`: 174 584 KiB; `claude agents --json` timed 3 times each: 0.48-0.57 s default, 0.31-0.40 s empty. Details in `docs/design/wsenv/constraints.md`.
[^token]: Same machine and date: `/proc/<daemon pid>/environ` holds `CLAUDE_CODE_OAUTH_TOKEN`; this session's environment doesn't; `claude auth status` in the session reports `"loggedIn": false, "authMethod": "none"`, and with `CLAUDE_CONFIG_DIR` set reports that directory as `configDirectory` without starting a daemon.
[^contention]: Same machine and date: `ls ~/.claude | grep -c 'tmp\.'` = 616; `~/.claude.json` 264 KiB with 78 `projects` entries; `~/.claude/projects/` holds 1602 transcript directories; `ls ~/.claude/jobs | wc -l` = 16 while `claude daemon status` lists 13 workers.
