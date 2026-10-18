# 0006. Remove workspaces a grace period after their last session ends

- Status: Accepted
- Date: 2026-10-17

## Context

### Problem

A workspace is used through **sessions**. `ws init` either drops the user
into a shell in the workspace, or starts a background agent (e.g. `claude`)
there. When the last session ends, the workspace may be needed again soon,
for example to resume the agent's conversation, or it may never be needed
again.

When should a workspace be removed, and what decides that?

### What a solution needs

1. **Never remove a workspace while a session is running in it.**
2. **Remove workspaces automatically** once nothing has used them for a
   while, so they don't pile up. A cargo-sized workspace is about 26 MB on
   top of the shared cache[^bench-disk].
3. **Leave a window to resume.** The workspace keeps its path until it's
   removed.
4. **Don't depend on git.** Some workspaces have no repos.
5. **Discarding work is allowed, but never silent.** Sometimes losing
   unpushed work is the intent.
6. **Survive crashes and reboots** without leaking workspaces or deleting
   live ones.

### Relevant facts

**Detecting a running session.** In a Go prototype, `ws` took a shared
`flock` on a lock file in the workspace and passed it to the session process
it launched[^bench-lock]:

| Moment                                                  | Lock check |
| ------------------------------------------------------- | ---------- |
| After the launcher exited                               | in use     |
| Session's foreground command done, a background child still running | in use |
| After the last process holding the lock exited          | idle       |
| After the session was killed with SIGKILL               | idle       |

The lock covers every process that inherits it and doesn't depend on PIDs,
which get reused. A process that closes inherited file descriptors stops
counting. Launching real session programs with the lock as fd 3 showed which
ones keep it[^bench-sessions]:

| Session program                                   | Holds the lock while running | Commands it runs hold it |
| ------------------------------------------------- | ---------------------------- | ------------------------ |
| `zsh -i`, `bash -i`, `sh -i`, `dash -i`           | yes                          | yes                      |
| `claude` (interactive) and `claude -p`            | yes                          | no                       |

Claude Code runs its Bash tool commands through the user's shell (`zsh -c`) without passing the
descriptor on. Those commands run while `claude` holds the lock, but one it
leaves running after it exits, such as a dev server, doesn't hold it.

**Resuming.**

- `claude --continue` resumes "the most recent conversation in the current
  directory"[^claude-help].
- Claude Code keeps session history per directory path, in
  `~/.claude/projects/<encoded path>`[^bench-claude-dirs].
- Resuming therefore needs the workspace at its original path.

**Last use without a session.** Someone may `cd` into a workspace without
going through `ws`. Editing a file changes its mtime. Commits change
`.git/logs/HEAD`[^bench-activity]. Finding the newest mtime is a full walk of
the workspace, and the cost grows quickly with the number of
workspaces[^bench-pass]:

| Workspaces | Lock check and walk of each | Lock check, walk only those past `idle-after` |
| ---------- | --------------------------- | --------------------------------------------- |
| 20         | 0.43 s                      |                                               |
| 50         | 1.6 s                       |                                               |
| 100        | 3.4 s                       |                                               |
| 200        | 6.5 s                       | 4 ms (none due), 82 ms (5 due)                |

**OS temp cleaners.** macOS and systemd delete temp files one at a time by
age[^dirhelper][^tmpfiles]. They know nothing about sessions.

## Decision

We recommend **lazy removal driven by session tracking**:

- **Session tracking.**
  - Every session `ws` starts holds a shared lock on
    `<workspace>/.ws/lock`.
  - For an interactive shell, the foreground `ws` waits for the shell to
    exit.
  - For a background agent, a small detached `ws` supervisor starts the
    agent, holds the lock, waits for it to exit, and then exits too.
  - Whichever process waited records the session's command, PID, start and
    end times, and exit code in `.ws/sessions`.
- **Idle.** A workspace is idle when its lock is free **and** its last
  activity is older than the grace period (`cleanup.grace`, default `24h`).
  Last activity is the latest of:
  - the last session's end time
  - the newest file mtime in the workspace (repo `.git/` directories are
    skipped, except `index` and `logs/HEAD`)
- **Skipping workspaces that can't be idle yet.** `.ws/idle-after` holds the
  earliest time the workspace could become idle. It is written as the
  session's end time plus the grace period when a session ends, and as the
  newest activity found plus the grace period after each walk. The removal
  pass skips a workspace whose `idle-after` is in the future. A missing or
  unreadable file means "due". Nothing is removed without a fresh walk, so a
  stale `idle-after` can only delay removal, never cause it.
- **When removal runs.** Any `ws` command first removes idle workspaces, at
  most once per minute (a timestamp file throttles it). Each pass checks every
  lock but walks only the workspaces that are due. `ws cleanup --idle`
  does the same on demand.
- **Unsaved work.**
  - Automatic removal skips workspaces whose git repos have uncommitted,
    unpushed, or stashed work, and lists them in `ws list` as `idle (unsaved
    work)`.
  - Explicit `ws cleanup <id>` deletes regardless and reports what was
    discarded.
  - Non-git content has no notion of "unsaved", so it is removed with the
    workspace.
- **Crashes and reboots.** If a session dies without its end time being
  recorded, the kernel still releases the lock[^bench-lock]. Idleness then
  falls back to file mtimes.

### Why this option

1. **Never remove a live workspace.** The lock is checked at removal time and
   can't be fooled by PID reuse. Shells and `claude` hold it for their
   lifetime. Its blind spot is a process that doesn't hold the descriptor,
   including anything Claude Code started and left running after it exited.
   The mtime rule partly covers that, as long as the process keeps writing
   files, and the grace period gives a day before it matters.
2. **Automatic removal.** Yes, but lazily: only when `ws` next runs. Someone
   who stops using `ws` entirely keeps their last workspaces. A scheduled job
   (option C) closes that gap later.
3. **Resume window.** The path stays fixed for the grace period, so
   `claude --continue` finds its history.
4. **Not git-dependent.** The lock, session records, and mtimes work for any
   directory. Git only affects the unsaved-work check.
5. **Discarding allowed, never silent.** Explicit cleanup deletes and
   reports. Automatic cleanup doesn't destroy git work nobody asked to lose,
   which is the main judgment call here.
6. **Crashes and reboots.** The lock frees itself and mtimes take over. No
   live workspace is removed, and no workspace is stuck as "running".

## Alternatives

### A. A waiting reaper per workspace

When the session ends, `ws` keeps a process alive for the grace period, then
removes the workspace.

**Pros**

- Removal happens exactly on time.
- No dependence on `ws` being run again.

**Cons**

- One sleeping process per finished workspace for the whole grace period.
- The reaper dies on reboot or logout, leaking the workspace.
- An interactive shell can't block the terminal for the grace period, so
  shell sessions would need a detached reaper anyway.

### B. Lazy removal on each `ws` run (recommended)

See Decision.

**Pros**

- No long-lived processes beyond the sessions themselves.
- Survives reboots.

**Cons**

- Nothing is removed until `ws` runs again.

### C. A scheduled job

launchd or a systemd timer runs `ws cleanup --idle` periodically.

**Pros**

- Removes workspaces even if `ws` isn't used again.

**Cons**

- A separate install step on each platform.

It complements B and can be added later.

### D. Explicit cleanup only

Nothing is automatic. Users run `ws cleanup` with filters.

**Pros**

- Fully predictable.

**Cons**

- Fails need 2: workspaces pile up unless someone remembers.

### E. `$TMPDIR` and the OS temp cleaner

**Pros**

- No code.

**Cons**

- Deletes individual files by age without knowing about
  sessions[^dirhelper][^tmpfiles], so it can remove files from a workspace
  an agent is using.
- Fails needs 1 and 3.

## Consequences

- A workspace lives for its sessions plus the grace period, then goes away
  on the next `ws` run. The exception is one with unsaved git work, which
  stays visible in `ws list` until it's explicitly removed.
- `ws` needs commands for sessions, and for re-entering a workspace that
  still exists (a shell, or `claude --continue`). That extends the command
  set in [0004](0004-single-binary-with-subcommands.md) and needs a
  superseding ADR.
- Restoring a workspace after it has been removed (re-cloning at the
  recorded commits) is out of scope here, and gets its own ADR if wanted.
- With `idle-after`, a pass over 200 workspaces costs a few milliseconds
  plus about 16 ms for each workspace that is due. Without it, a pass would
  add seconds to `ws` commands.
- Processes Claude Code leaves running after it exits don't keep a workspace
  alive. If that becomes a problem, the session supervisor can wait for the
  session's whole process group instead.
- Conformance: tests check that a locked workspace is never removed, even
  with an old mtime; that a workspace is removed after the grace period once
  its lock is free; that a recent edit keeps it, even when `idle-after` is in the past;
that a workspace with a future `idle-after` isn't walked; that one with unsaved git
  work is skipped automatically but removed by `ws cleanup <id>`; and that
  killing a session with SIGKILL makes its workspace idle.

## References

[^bench-disk]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1. 20 hard-linked clones of a rust-lang/cargo bare cache: `du -sk` over the cache and all 20 together was 619,516 KB, against 88,652 KB for the cache alone. That is about 26.5 MB per workspace.
[^bench-lock]: Measured 2026-10-17, same machine, Go 1.27.1. A launcher takes `syscall.Flock(LOCK_SH|LOCK_NB)` on a lock file, starts `sh -c <cmd>` with the file in `cmd.ExtraFiles` and `Setsid: true`, then exits. A separate checker tries `Flock(LOCK_EX|LOCK_NB)`. With `sleep 3 & sleep 1.5; wait`: "IN USE" right after the launcher exited and at about 2 s, then "idle" at about 3.5 s. With `sleep 30`, after `kill -9` on the session: "idle" within 0.2 s.
[^claude-help]: Claude Code CLI, `claude --help` (local install, 2026-10-17). `-c, --continue`: "Continue the most recent conversation in the current directory"; `--resume <session-id>` continues a given session.
[^bench-claude-dirs]: Observed 2026-10-17: `ls ~/.claude/projects` lists one directory per working directory, named after the path with `/` replaced by `-` (e.g. `-private-tmp-playground-20261002-fcf1ddf4`).
[^bench-activity]: Measured 2026-10-17, same machine. In a one-commit repo with timestamps backdated using `touch -t`: editing a file changed only that file's mtime. A commit changed `.git/logs/HEAD`. `git status` changed nothing reliably.
[^bench-sessions]: Measured 2026-10-17, same machine, Go 1.27.1, zsh 5.9, bash 3.2.57, Claude Code 2.1.302. A Go launcher takes `unix.Flock(LOCK_SH|LOCK_NB)` on a lock file, starts the program with the file in `cmd.ExtraFiles` (fd 3), closes its own copy, and waits. Shells ran interactively under `script -q /dev/null` with the user's own rc files, fed `sleep 4` and then `exit`. While at the prompt and while `sleep` ran, the checker (`Flock(LOCK_EX|LOCK_NB)`) reported "IN USE", and `lsof -p <pid> -d 3` showed the lock file open in the shell and in `sleep`. After `exit` it reported "idle". `claude -p`, and interactive `claude` under `script` (folder-trust prompt accepted), were each told to run `sleep 20` or `sleep 30` with the Bash tool. Both reported "IN USE" with the lock open on fd 3 in `claude`. In both, the `sleep` (child of `/bin/zsh -c`, child of `claude`) had no lock file open on any descriptor. After `claude` exited or got SIGTERM: "idle".
[^bench-pass]: Measured 2026-10-17, same machine, Go 1.27.1. Workspaces were APFS clones (`cp -c`) of 20 hard-linked rust-lang/cargo checkouts (about 2,950 files each outside `.git/`). Each pass ran one goroutine per workspace, limited to 10 (the core count): `Flock(LOCK_EX|LOCK_NB)` on `.ws/lock`, then, if free, `filepath.WalkDir` skipping `.git/` except for statting `index` and `logs/HEAD`. Medians of 5 warm runs (6 run, first dropped). The `idle-after` variant read `.ws/idle-after` (RFC 3339) and skipped the walk when it was in the future.
[^dirhelper]: Read 2026-10-17 on macOS 26.6.2 with `plutil -p /System/Library/LaunchDaemons/com.apple.bsd.dirhelper.plist`: `RunAtLoad => true`, `StartCalendarInterval => {Hour => 3, Minute => 35}`, `CLEAN_FILES_OLDER_THAN_DAYS => "3"`.
[^tmpfiles]: tmpfiles.d(5). <https://www.freedesktop.org/software/systemd/man/latest/tmpfiles.d.html> (accessed 2026-10-17). "If a file or directory is older than the current time minus the age field, it is deleted."
