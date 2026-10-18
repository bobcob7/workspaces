# 0013. Remove workspaces a grace period after their last session ends, whatever their git state

- Status: Superseded by [0015](0015-workspace-lifetime-follows-session.md) (2026-10-18)
- Date: 2026-10-17
- Supersedes: [0006](0006-session-based-cleanup.md)

## Context

### Problem

[0006](0006-session-based-cleanup.md) decided to remove a workspace a grace
period after its last session ends, but to skip automatic removal when its
git repos have unsaved work. Two things have changed since:

- **Dirty workspaces are normal.** The maintainer often uses a workspace to
  investigate something or build a proof of concept, and the result ends up
  outside the workspace: in notes, an issue, or another repo. Their repos
  are routinely left with uncommitted changes and unpushed commits that
  nobody wants. Under 0006 each of these would linger until removed by hand.
- **0006's activity scan counts `ws`'s own files.** It skips only repo
  `.git/` internals, so writing `.ws/idle-after` after a scan would look
  like fresh activity, and the workspace would never become idle.

When should a workspace be removed, and should its git state matter?

### What a solution needs

1. **Never remove a workspace while a session is running in it.**
2. **Remove workspaces automatically** once nothing has used them for a
   while, so they don't pile up. A cargo-sized workspace is about 26 MB on
   top of the shared cache[^bench-disk].
3. **Leave a window to resume.** The workspace keeps its path until it's
   removed.
4. **Don't depend on git.** Some workspaces have no repos, and a dirty repo
   is not a reason to keep one.
5. **Survive crashes and reboots** without leaking workspaces or deleting
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

Launching real session programs with the lock as fd 3[^bench-sessions]:

| Session program                                   | Holds the lock while running | Commands it runs hold it |
| ------------------------------------------------- | ---------------------------- | ------------------------ |
| `zsh -i`, `bash -i`, `sh -i`, `dash -i`           | yes                          | yes                      |
| `claude` (interactive) and `claude -p`            | yes                          | no                       |

Claude Code runs its Bash tool commands through the user's shell
(`zsh -c`) without passing the descriptor on. A command it leaves running
after it exits, such as a dev server, doesn't hold the lock.

**Resuming.** `claude --continue` resumes "the most recent conversation in
the current directory"[^claude-help], and Claude Code keeps history per
directory path[^bench-claude-dirs], so resuming needs the original path.

**Last use without a session.** Editing a file changes its mtime, and
commits change `.git/logs/HEAD`[^bench-activity]. Finding the newest mtime
means walking the workspace, which gets slow across many
workspaces[^bench-pass]:

| Workspaces | Lock check and walk of each | Lock check, walk only those past `idle-after` |
| ---------- | --------------------------- | --------------------------------------------- |
| 20         | 0.43 s                      |                                               |
| 50         | 1.6 s                       |                                               |
| 100        | 3.4 s                       |                                               |
| 200        | 6.5 s                       | 4 ms (none due), 82 ms (5 due)                |

**Unsaved-work detection** was proposed in
[0010](0010-unsaved-work-detection.md) and rejected for the reason above.

## Decision

We will remove workspaces lazily, a grace period after their last session,
whatever state their repos are in. Compared with 0006, the unsaved-work rule
is gone, and `ws`'s state moves out of the workspace into
`$CONFIG_DIR/state/<id>/` ([0012](0012-workspace-layout.md)). Everything else
is unchanged.

- **Session tracking.** Every session `ws` starts holds a shared lock on
  `state/<id>/lock` ([0012](0012-workspace-layout.md)). For an
  interactive shell, the foreground `ws` waits for it to exit. For a
  background agent, a small detached `ws` supervisor waits. Whichever process
  waited records the session's command, PID, start and end times, and exit
  code in `state/<id>/sessions`.
- **Idle.** A workspace is idle when its lock is free **and** its last
  activity is older than `cleanup.grace` (default `24h`). Last activity is
  the latest of the last session's end time and the newest file mtime in
  the workspace. The scan skips repo `.git/` directories
  except `index` and `logs/HEAD`.
- **`idle-after`.** `state/<id>/idle-after` holds the earliest time the workspace
  could become idle: session end plus the grace period, or newest activity
  plus the grace period after a scan. The removal pass doesn't scan a
  workspace whose `idle-after` is in the future. A missing file means "due".
  Nothing is removed without a fresh scan, so a stale `idle-after` can only
  delay removal.
- **When removal runs.** Any `ws` command first removes idle workspaces, at
  most once per minute. `ws cleanup --idle` does the same on demand.
- **Git state is ignored.** Removal deletes the directory. `ws cleanup <id>`
  removes a workspace immediately, unless a session is running in it.
- **Crashes and reboots.** The kernel releases the lock when a session
  dies[^bench-lock], and idleness falls back to file mtimes.

### Why this option

1. **Never remove a live workspace.** The lock is checked at removal time.
   Its blind spot is a process that doesn't hold it, such as something
   Claude Code left running. The mtime rule covers that only while the
   process writes files.
2. **Automatic removal**, lazily, on the next `ws` run. The pass costs a few
   milliseconds plus a scan for each workspace that is due.
3. **Resume window.** The path stays fixed for the grace period.
4. **No git dependency at all**, and PoC workspaces go away on schedule.
5. **Crashes and reboots.** The lock frees itself and mtimes take over.

The cost is that work left only in a workspace is lost one grace period after
its last session, with no warning. Pushing, or copying the result out, is
the user's job before then.

## Alternatives

The timing alternatives (a reaper process per workspace, a scheduled job,
explicit cleanup only, the OS temp cleaner) were weighed in 0006, and
nothing here changes that comparison. A scheduled job can still be added
later to run `ws cleanup --idle`.

### Keep 0006's rule: skip automatic removal when work is unsaved

**Pros**

- Commits an agent forgot to push survive.

**Cons**

- Investigation and PoC workspaces, which are routinely dirty, pile up until
  removed by hand.
- Adds git checks to cleanup ([0010](0010-unsaved-work-detection.md)).

### Remove anyway, but report what was discarded

**Pros**

- Nothing lingers, and losses are recorded.

**Cons**

- For dirty-by-design workspaces the report is noise, printed during some
  unrelated later command.
- Still needs the git checks.

### A longer grace period for dirty workspaces

**Pros**

- More time to rescue forgotten work, without keeping it forever.

**Cons**

- Still needs the git checks, and PoC workspaces still linger longer.
- Two durations to explain.

## Consequences

- A workspace lives for its sessions plus the grace period, then goes away
  on the next `ws` run, whatever it contains.
- `ws list` has no clean/dirty column, and `ws cleanup` has no `--force` or
  "skipped" results.
- `ws` needs commands for starting sessions and re-entering a workspace
  that still exists. That extends [0004](0004-single-binary-with-subcommands.md)
  and needs a superseding ADR.
- Restoring a workspace after removal is out of scope.
- Processes Claude Code leaves running don't keep a workspace alive. If that
  matters, the supervisor can wait for the session's whole process group.
- When this is accepted, 0006's status becomes `Superseded by 0013`.
- Conformance: tests check that a locked workspace is never removed, even
  with an old mtime; that a workspace is removed after the grace period
  once its lock is free, including one with uncommitted changes and
  unpushed commits; that a recent edit keeps it, even when `idle-after` is
  in the past; that updating `idle-after` doesn't count as activity; that a
  workspace with a future `idle-after` isn't scanned; and that killing a
  session with SIGKILL makes its workspace idle.

## References

[^bench-disk]: Measured 2026-10-17 on an Apple M4, macOS 26.6.2, git 2.50.1. 20 hard-linked clones of a rust-lang/cargo bare cache: `du -sk` over the cache and all 20 together was 619,516 KB, against 88,652 KB for the cache alone. That is about 26.5 MB per workspace.
[^bench-lock]: Measured 2026-10-17, same machine, Go 1.27.1. A launcher takes `syscall.Flock(LOCK_SH|LOCK_NB)` on a lock file, starts `sh -c <cmd>` with the file in `cmd.ExtraFiles` and `Setsid: true`, then exits. A separate checker tries `Flock(LOCK_EX|LOCK_NB)`. With `sleep 3 & sleep 1.5; wait`: "IN USE" right after the launcher exited and at about 2 s, then "idle" at about 3.5 s. With `sleep 30`, after `kill -9` on the session: "idle" within 0.2 s.
[^claude-help]: Claude Code CLI, `claude --help` (local install, 2026-10-17). `-c, --continue`: "Continue the most recent conversation in the current directory"; `--resume <session-id>` continues a given session.
[^bench-claude-dirs]: Observed 2026-10-17: `ls ~/.claude/projects` lists one directory per working directory, named after the path with `/` replaced by `-` (e.g. `-private-tmp-playground-20261002-fcf1ddf4`).
[^bench-activity]: Measured 2026-10-17, same machine. In a one-commit repo with timestamps backdated using `touch -t`: editing a file changed only that file's mtime. A commit changed `.git/logs/HEAD`. `git status` changed nothing reliably.
[^bench-sessions]: Measured 2026-10-17, same machine, Go 1.27.1, zsh 5.9, bash 3.2.57, Claude Code 2.1.302. A Go launcher takes `unix.Flock(LOCK_SH|LOCK_NB)` on a lock file, starts the program with the file in `cmd.ExtraFiles` (fd 3), closes its own copy, and waits. Shells ran interactively under `script -q /dev/null` with the user's own rc files, fed `sleep 4` and then `exit`. While at the prompt and while `sleep` ran, the checker (`Flock(LOCK_EX|LOCK_NB)`) reported "IN USE", and `lsof -p <pid> -d 3` showed the lock file open in the shell and in `sleep`. After `exit` it reported "idle". `claude -p`, and interactive `claude` under `script` (folder-trust prompt accepted), were each told to run `sleep 20` or `sleep 30` with the Bash tool. Both reported "IN USE" with the lock open on fd 3 in `claude`. In both, the `sleep` (child of `/bin/zsh -c`, child of `claude`) had no lock file open on any descriptor. After `claude` exited or got SIGTERM: "idle".
[^bench-pass]: Measured 2026-10-17, same machine, Go 1.27.1. Workspaces were APFS clones (`cp -c`) of 20 hard-linked rust-lang/cargo checkouts (about 2,950 files each outside `.git/`). Each pass ran one goroutine per workspace, limited to 10 (the core count): `Flock(LOCK_EX|LOCK_NB)` on `.ws/lock`, then, if free, `filepath.WalkDir` skipping `.git/` except for statting `index` and `logs/HEAD`. Medians of 5 warm runs (6 run, first dropped). The `idle-after` variant read `.ws/idle-after` (RFC 3339) and skipped the walk when it was in the future.
