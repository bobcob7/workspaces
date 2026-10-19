# 0015. A workspace lives exactly as long as its one session

- Status: Accepted (2026-10-18); session reading amended by [0024](0024-claude-code-client.md) (2026-10-19)
- Date: 2026-10-18
- Supersedes: [0013](0013-cleanup-ignores-git-state.md)

## Context

### Problem

[0013](0013-cleanup-ignores-git-state.md) removes a workspace a grace period
after its last session ends, tracking sessions with a lock that `ws` passes
to the session's process. Since then the use cases have narrowed to four:

1. A person starts a Claude Code agent in a new workspace.
2. An agent starts another Claude Code agent in a new workspace.
3. A person opens a shell in a new workspace.
4. After a reboot or a crash of Claude Code's background service, every
   agent comes back.

The maintainer wants a finished agent's workspace kept until its session is
deleted, so a person can review the conversation next to the code it
changed. A grace period removes it on a timer instead. Claude Code's own
background sessions (`claude --bg`) are the natural way to run agents:
`claude agents` lists them, and they can be attached to, read, stopped, and
restarted.

When should a workspace be removed, and how does `ws` know?

### What a solution needs

1. **Never remove a workspace whose session still exists**, running or
   finished.
2. **Remove it once its session is gone**, without a separate cleanup step.
3. **Agents show up in `claude agents`** and can be managed with Claude
   Code's own commands.
4. **Agents can be restored** after a reboot or a background-service crash.
5. **Don't depend on git state** ([0013](0013-cleanup-ignores-git-state.md)
   still holds: dirty workspaces are normal).

### Relevant facts

**Claude Code background sessions.**

- `claude --bg` "Start[s] the session in the background and return[s]
  immediately. Prints the id that `claude attach`, `logs`, `stop` and `rm`
  take; `claude agents` lists them"[^claude-help].
- `claude agents --json` prints "active sessions (interactive and
  background) as a JSON array"; `--all` "also include[s] completed
  background sessions"; `--cwd <path>` shows "only background sessions
  started under <path>"[^claude-agents-help]. Each entry has `id`, `cwd`,
  `state` (`working`, `blocked`, `done`), and a `pid` while a process is
  alive. Listing every session took 0.11 s[^bench-bg-list].
- `claude rm` removes the session from the list; "the removal survives
  supervisor restarts", and the transcript stays available through
  `claude --resume`[^agent-view-rm]. Each session has a directory
  `~/.claude/jobs/<id>`[^agent-view-shell].
- After a reboot, sessions "don't resume on their own". Replying, attaching,
  or `claude respawn <id>` restarts one "from its saved
  conversation"[^agent-view-reboot]. If the background service itself stops,
  each session "waits about a minute for a new supervisor to reconnect to
  it, and stops if none does"[^agent-view-reboot].

**How they behave in a workspace** (Claude Code 2.1.302):

| Check | Result |
| ----- | ------ |
| Session lock passed by `ws` as fd 3[^bench-bg-lock] | free as soon as `claude --bg` returned; the session runs under a long-lived service process, not under the caller |
| Environment variables set by the caller[^bench-bg-env] | not seen by the session; its working directory is |
| `--session-id` with `--bg`[^bench-bg-id] | ignored with a warning; the id is printed, and `claude agents --json --cwd <workspace>` finds it |
| Untrusted directory[^bench-bg-trust] | refused, exit 1. A new directory below a trusted one starts normally |
| Edit to a repo inside the workspace[^bench-bg-edit] | Claude tries to create its own worktree, fails because the workspace root isn't a git repo, and edits in place. With `--settings '{"worktree":{"bgIsolation":"none"}}'` it doesn't try |
| `claude stop`, `respawn`, then `rm`[^bench-bg-rm] | `done` with no `pid`; running again with its conversation; gone from `--all` and from `~/.claude/jobs/`, while the workspace directory stays on disk |
| `--cwd` filter[^bench-bg-id] | matched the workspace, not a sibling directory whose name starts the same |

**Claude Code's worktree hooks.** A `WorktreeCreate` hook can replace how
Claude Code creates a background session's working copy, and
`WorktreeRemove` runs when the session is deleted. But for a hook-made
worktree "that still contains files", `claude rm` "keeps the session and
worktree instead"[^hooks-worktree].

**Shells.** A shell holds the lock `ws` passes it, and so do the commands it
runs; the lock is freed when the shell exits or is killed[^bench-sessions].

## Decision

We will give each workspace **exactly one session**, started when the
workspace is created, and remove the workspace when that session is gone.

- **Agent workspaces** (`ws claude`, [0014](0014-command-set.md)). `ws`
  creates the workspace, then runs `claude --bg -n <workspace name>
  --settings '{"worktree":{"bgIsolation":"none"}}' [args]` in it. If that
  exits 0, `ws` finds the session with `claude agents --json --cwd
  <workspace>` and records its id in `state/<id>/session`. If it fails, `ws`
  deletes the workspace and exits non-zero.
- **Shell workspaces** (`ws shell`). `ws` creates the workspace and runs
  `$SHELL -i` in it, holding the session lock. When the shell exits, for any
  reason or exit code, `ws` deletes the workspace.
- **When a session is gone.** An agent session is gone when it no longer
  appears in `claude agents --json --all` **and** `~/.claude/jobs/<id>` no
  longer exists. A shell session is gone when its lock is free.
- **When removal runs.** Any `ws` command first removes workspaces whose
  session is gone, at most once per minute. This catches `claude rm` and
  shells whose `ws` was killed (a closed terminal, a reboot). If
  `claude agents` fails, agent workspaces are left alone.
- **`ws rm <ws>`** removes one now: for an agent, `claude stop` and
  `claude rm`, then the directory; for a shell, refused while it runs.
- **`ws restore`** runs `claude respawn <id>` for each agent workspace whose
  session has no process. Shell workspaces aren't restored; a shell killed
  by a reboot loses its workspace on the next `ws` command.
- **Git state is ignored**, as in 0013.
- **Trust.** `ws` doesn't edit Claude Code's settings. If Claude refuses
  the directory as untrusted, `ws` deletes the workspace and says to run
  `claude` once in `$CONFIG_DIR/workspaces` and accept the prompt.

### Why this option

1. **Never too early.** A finished agent's workspace stays as long as its
   session is listed, so its conversation can be read against its code. Two
   independent signals, the listing and the jobs directory, must both say
   the session is gone.
2. **Removal follows the session** with no timer and no cleanup command.
   It is lazy: after `claude rm`, the directory goes on the next `ws` run.
3. **Claude Code manages agents.** `claude agents`, `attach`, `logs`, and
   `stop` work on them unchanged.
4. **Restore** is one command, built on `claude respawn`.
5. **No git dependency.**

The weak points: `ws` now depends on Claude Code's background-session
interface, including the shape of `claude agents --json`. A workspace whose
agent is never deleted is never removed. And a shell workspace goes away
when its shell exits, so anything worth keeping must be pushed or copied out
first.

## Alternatives

### Keep 0013's grace period

**Pros**

- Works for any program, not only Claude Code.
- Workspaces can't pile up: everything goes after a day without use.

**Cons**

- Removes a finished agent's workspace while its conversation is still
  listed, so it can't be reviewed against the code.
- Its lock can't see `claude --bg` sessions[^bench-bg-lock], so a running
  agent could be removed.

### `ws`'s own background supervisor (`ws run`, `ws logs`)

**Pros**

- Runs any command, and its lock covers the whole run.

**Cons**

- Its sessions don't appear in `claude agents` and can't be attached to.
- Duplicates logs, stop, and restart, which Claude Code already provides.

### Let Claude Code create workspaces through worktree hooks

**Pros**

- `claude rm` would remove the workspace itself, with no lazy pass.

**Cons**

- `claude rm` keeps a hook-made worktree that still contains
  files[^hooks-worktree], which a workspace always does.
- The hook gets only a name, not repo specs, and the hook setting changes
  how worktrees are made for every repo.

### Remove agent workspaces when the session is `done`

**Pros**

- No dependency on the user deleting sessions.

**Cons**

- `done` sessions are the ones a person wants to review.

## Consequences

- `ws init`, `ws run`, `ws logs`, and `ws cleanup` go away
  ([0014](0014-command-set.md)), and so does `cleanup.grace`.
- [0012](0012-workspace-layout.md)'s state directory changes: `idle-after`
  and `sessions` are replaced by one `session` record (kind, Claude session
  id, start time); `lock` is used only by shell workspaces. The rest of
  0012, including the deletion guard, still applies.
- Removing an agent workspace needs `claude rm`, `ws rm`, or deleting the
  session in agent view.
- A Claude Code session started by hand inside a shell workspace ends with
  the shell's workspace.
- **Unverified:** that a session restarted with `claude respawn` keeps its
  `--settings`; an actual reboot (tested with `claude stop` instead). To be
  checked when `ws restore` is built.
- When this is accepted, 0013's status becomes `Superseded by 0015`.
- Conformance: tests with a fake `claude` on `PATH` check that a workspace
  is kept while its session is listed in any state, removed once it is
  missing from the listing and from `~/.claude/jobs`, and kept when
  `claude agents` fails; that a failed `claude --bg` leaves no workspace;
  and that a shell workspace is removed when the shell exits and, after
  SIGKILL of `ws`, by the next `ws` command.

## References

[^claude-help]: Claude Code CLI 2.1.302, `claude --help` (local install, 2026-10-18), `--bg, --background`: "Start the session in the background and return immediately. Prints the id that `claude attach`, `logs`, `stop` and `rm` take; `claude agents` lists them."
[^claude-agents-help]: Claude Code CLI 2.1.302, `claude agents --help` (2026-10-18). `--json`: "Print active sessions (interactive and background) as a JSON array and exit (for scripting; does not require a TTY)". `--all`: "With --json: also include completed background sessions". `--cwd <path>`: "Show only background sessions started under <path>".
[^agent-view-rm]: Claude Code docs, "Agent view," section "What deleting a session removes." <https://code.claude.com/docs/en/agent-view> (accessed 2026-10-18). "Its transcript stays on your machine through `claude --resume`, and the removal survives supervisor restarts."
[^agent-view-shell]: Same page, "Manage sessions from the shell." "each session's ID is its directory name under `~/.claude/jobs/`."
[^agent-view-reboot]: Same page. On reboots: "Shutting down or restarting your machine stops running background sessions." Within 48 hours a session "shows as failed. Attach or reply to it and it restarts from its saved conversation." On the supervisor: "each session's process waits about a minute for a new supervisor to reconnect to it, and stops if none does." `claude respawn <id>`: "Restart a session, running or stopped … A session that has a saved conversation resumes it."
[^hooks-worktree]: Claude Code docs, "Hooks reference," section "WorktreeRemove." <https://code.claude.com/docs/en/hooks> (accessed 2026-10-18). "On that route, the hook runs for a worktree that still contains files only when you confirm the delete in agent view; for such a worktree, `claude rm` keeps the session and worktree instead."
[^bench-bg-list]: Measured 2026-10-18 on an Apple M4, macOS 26.6.2, Claude Code 2.1.302. `claude agents --json --all` with 5 sessions listed, 6 runs under `/usr/bin/time -p`: 0.11 s each.
[^bench-bg-lock]: Measured 2026-10-18, same machine. The Go launcher from [^bench-sessions] took a shared `flock` and started `claude --bg -n ws-bgtest '<prompt>'` with the lock file as fd 3. Right after the launcher returned (0.32 s), the checker (`Flock(LOCK_EX|LOCK_NB)`) reported "idle" while the session was `working`. `ps` showed the session as `claude bg-spare`, child of `claude bg-pty-host`, child of `claude daemon run` (PPID 1); the session process had been started 18 h 30 min earlier.
[^bench-bg-env]: Measured 2026-10-18, same machine. `WS_TEST=xyz123 claude --bg '<prompt>'`, asked to run `printenv WS_TEST > envout.txt; pwd >> envout.txt`: the file held only the starting directory.
[^bench-bg-id]: Measured 2026-10-18, same machine. `claude --bg --session-id <uuid>` printed "warning: --bg manages the session id; ignoring --session-id" and a different id. Its first output line was `backgrounded · 5d8d652c · ws-bgsid`. With sessions only in `<dir>/wsiso2`, `claude agents --json --all --cwd <dir>/wsiso2` returned that session and `--cwd <dir>/wsiso2-sibling` returned none.
[^bench-bg-trust]: Measured 2026-10-18, same machine. In a new, untrusted directory, `claude --bg` printed "Workspace not trusted. Run `claude` in <dir> once and accept the trust prompt, then retry." and exited 1. In a new subdirectory of a directory whose trust prompt had been accepted, it started.
[^bench-bg-edit]: Measured 2026-10-18, same machine. Directory (not a git repo) holding a one-commit repo `r`. `claude --bg` asked to append a line to `r/a.txt`: `claude logs` showed "Creating worktree … Error: Cannot create a worktree: not in a git repository and no WorktreeCreate hooks are configured", then the edit in `r/a.txt` itself, and `git worktree list` showed only `r`. Repeated with `--settings '{"worktree":{"bgIsolation":"none"}}'`: the edit happened with no worktree attempt in the log.
[^bench-bg-rm]: Measured 2026-10-18, same machine. After `claude stop <id>`, `claude agents --json --all` listed the session with `state: done` and no `pid`. `claude respawn <id>` gave it a new `pid`, and `claude logs` showed the earlier conversation. `claude rm <id>` printed "removed <id>"; the id was then absent from `claude agents --json --all`, `~/.claude/jobs/<id>` no longer existed, and the session's directory was still on disk.
[^bench-sessions]: See [0013](0013-cleanup-ignores-git-state.md), footnote `bench-sessions`: `zsh`, `bash`, `sh`, and `dash` run interactively held the lock passed as fd 3, as did the commands they ran, and released it on exit; after SIGKILL the lock was free within 0.2 s (footnote `bench-lock`).
