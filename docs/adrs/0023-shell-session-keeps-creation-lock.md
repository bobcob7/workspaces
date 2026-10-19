# 0023. `ws shell` keeps creation's lock for the shell, and waits out the terminal's signals

- Status: Accepted (2026-10-19)
- Date: 2026-10-19

## Context

### Problem

[0015](0015-workspace-lifetime-follows-session.md) decides that `ws shell`
runs `$SHELL -i` in a new workspace "holding the session lock", deletes the
workspace when the shell exits, and treats a shell session as gone once its
lock is free. [0022](0022-workspace-record-before-rename.md) has creation
hold `state/<id>/lock` exclusively while it runs. Neither says how the lock
gets from creation to the shell, or what `ws` does while the shell runs:
with the terminal's signals, with the shell's exit status, or when removal
fails.

How does the session take over the lock, and how does `ws` wait for the
shell?

### What a solution needs

1. **A live workspace never looks abandoned**: no moment between creation
   and the shell's exit when its lock is free.
2. **The workspace is removed when the shell exits**, however it ends,
   including when the terminal is closed.
3. **A running shell keeps its workspace** even if `ws` is killed.
4. **The terminal works as it does for any shell**: Ctrl-C and Ctrl-Z reach
   the shell and its commands, not `ws`.
5. **`ws` exits with the shell's status** ([0016](0016-output-contract.md)).

### Relevant facts

- **A flock belongs to the open file.** "Locks are on files, not file
  descriptors. That is, file descriptors duplicated through dup(2) or
  fork(2) do not result in multiple instances of a lock, but rather multiple
  references to a single lock"[^flock].
- **Go opens files close-on-exec**, so a child process inherits a
  descriptor only when it's passed explicitly[^go-cloexec]; a file passed
  in the command's extra files list as entry `i` "becomes file descriptor
  3+i"[^go-extrafiles].
- **The terminal signals a process group.** Ctrl-C "Generates a SIGINT
  signal which is sent to all processes in the foreground process
  group"[^termios]; Ctrl-\\ does the same with SIGQUIT.
- **Interactive shells take their own process group.** With a prototype of
  `ws shell` under a pseudo-terminal, `zsh -i`, `bash -i`, `sh -i`, and
  `dash -i` each ran in a process group of their own, not `ws`'s, and the
  terminal's foreground group was the shell's job, not `ws`. Ctrl-C at the
  prompt and during `sleep 5` left `ws` running, and `exit 3` made `ws`
  exit 3[^bench-pty]. Ctrl-Z during `sleep 30` in zsh and bash stopped the
  `sleep`, not `ws`[^bench-jobs].
- **A shell without job control shares the group.** A non-interactive
  script standing in for the shell ran in `ws`'s process group; Ctrl-C
  killed it (status 130) and left `ws` running, which removed the
  workspace[^bench-jobs].
- **An ignored signal stays ignored across `exec`; a caught one doesn't.**
  "Signals set to be ignored in the calling process are set to be ignored
  in the new process. Signals which are set to be caught in the calling
  process image are set to default action in the new process
  image"[^execve]. bash's commands start with "the values inherited by the
  shell from its parent"[^bash-signals].
- **Shells and their commands hold a lock passed as fd 3.** In the same
  runs, `lsof` showed the lock file open on fd 3 in the shell and in a
  `sleep` it ran in the background, and a lock check reported it in
  use[^bench-pty].
- **Interactive shells ignore SIGTERM and exit on SIGHUP.** "When bash is
  interactive, in the absence of any traps, it ignores SIGTERM"; "The shell
  exits by default upon receipt of a SIGHUP"[^bash-signals]. Measured with
  the prototype passing SIGTERM and SIGHUP on unchanged: after SIGTERM,
  zsh and bash kept running; after SIGHUP, both exited (bash with status
  129, zsh with 1) and the workspace was removed. Closing the terminal
  (killing the process that owned the pseudo-terminal) also ended the shell
  and removed the workspace[^bench-signals].
- **A shell outlives a killed `ws`.** After SIGKILL of `ws` with the
  terminal still open, zsh and bash kept running and the lock stayed in
  use. Once the shell exited, the lock was free and the workspace and its
  state were still on disk[^bench-kill].
- **Exit statuses of killed commands.** bash uses "the value of 128+N as
  the exit status" for a command killed by signal N[^bash-exit]; zsh
  reports "128 plus the signal number"[^zsh-exit].

## Decision

We will hand the lock creation took to the session, and have `ws` wait for
the shell without letting the terminal's signals stop it:

1. **One lock, never released early.** Creation returns with
   `state/<id>/lock` still held. `ws` writes the session record
   `state/<id>/session` as TOML, like `workspace.toml`: `kind = "shell"`,
   `claude_session_id = ""`, and `started_at` (RFC 3339, UTC). An agent
   workspace writes the same record with `kind = "claude"` and its Claude
   session id.
2. **The shell gets the lock as fd 3.** `ws` runs `$SHELL -i`, or
   `/bin/sh -i` when `SHELL` is unset or empty, in the workspace, with
   `WS_ID`, `WS_WORKSPACE`, and `PWD` set and the lock's open file as fd 3.
   Nothing else `ws` starts inherits it.
3. **`ws` keeps its own descriptor** until removal has finished, then
   closes it.
4. **Signals.** From just before the shell starts until removal has
   finished, `ws` catches SIGINT, SIGQUIT, SIGHUP, and SIGTERM. While the
   shell runs, it discards SIGINT and SIGQUIT, which the terminal sends the
   shell, and sends the shell SIGHUP when it gets SIGHUP or SIGTERM, so
   `kill <ws pid>` ends the session as closing the terminal does. During
   removal it discards all four. It catches rather than ignores them, so
   the shell and its commands start with the default actions. `ws` never
   kills the shell itself; cancelling the command doesn't stop it.
5. **Exit status.** `ws` exits with the shell's exit code, or 128+N when
   signal N killed it.
6. **Removal** runs when the shell exits, whatever its status, through
   [0012](0012-workspace-layout.md)'s deletion guard. If it fails, `ws`
   prints `ws: couldn't remove workspace <id>: <reason>` on stderr and still
   exits with the shell's status, not 1: past the shell's start, the exit
   status is the shell's (0016). The workspace is left with its lock
   free, for 0015's removal pass.
7. **Before the shell starts**, a failure to write the session record or to
   start the shell removes the workspace and fails with `internal` (exit
   1), per 0016's table.

### Why this option

1. **Never abandoned.** The lock is taken once, before the state directory
   is used, and is held by `ws`, then by `ws` and the shell, until the
   workspace is gone.
2. **Removed on exit.** `ws` waits until the shell exits, for any reason,
   and then removes the workspace. Closing the terminal sends SIGHUP, and
   `kill` sends SIGTERM, which reaches the shell as SIGHUP; either ends the
   shell, and `ws` still removes the workspace[^bench-term]. A signal
   during removal doesn't stop it, since `ws` still catches them; only
   SIGKILL, which can't be caught[^sigaction], or a crash can.
3. **Survives a killed `ws`.** The shell and its commands hold the lock
   through fd 3, so the workspace isn't removed under them; once they exit,
   the free lock tells the next `ws` command the session is gone.
4. **The terminal behaves.** Job-control shells move into their own process
   group, so Ctrl-C reaches their commands and not `ws`. A shell without
   job control shares `ws`'s group, and `ws` discards the keypress there
   too. Because `ws` catches the signals instead of ignoring them, the
   shell's commands still stop on Ctrl-C[^execve].
5. **Exit status** is the shell's, with the shells' own convention for
   signals.

The weak points:

- The shell never sees SIGTERM sent to `ws`, only SIGHUP: a shell that
  traps SIGTERM to clean up doesn't run that trap. And `ws`'s exit status
  after `kill` depends on the shell: 129 with bash, 1 with
  zsh[^bench-term].
- A command the shell leaves running in the background keeps the lock
  after the shell exits. If `ws` is running, it removes the workspace
  anyway; if `ws` was killed, the workspace stays until that command exits.
- Every command run from the shell has an extra open descriptor, fd 3.
- SIGKILL during removal still stops `ws` partway[^sigaction], leaving
  the rest to 0015's removal pass. So does SIGHUP or SIGQUIT in the moment
  between creation and the start of the shell, before `ws` catches them.

## Alternatives

### Release creation's lock, then take a new one for the session

**Pros**

- Creation and the session don't share state; the session could use a
  shared lock, as 0013 did.

**Cons**

- The lock is free between the two. 0015's removal pass treats a free
  shell lock as a gone session, so a concurrent `ws` command in that moment
  could delete the new workspace before its shell starts.

### Pass SIGTERM on to the shell unchanged

**Pros**

- The shell gets the signal `ws` got, and a shell with a SIGTERM trap runs
  it.

**Cons**

- An interactive shell ignores SIGTERM[^bash-signals]; with the signal
  passed on unchanged, zsh and bash kept running after `kill` on
  `ws`[^bench-signals], so the usual way to stop a process does nothing.

### Hold the lock in `ws` only, not in the shell

**Pros**

- Background commands left by the shell don't keep the lock, and they don't
  see an extra fd 3.

**Cons**

- A shell outlives a killed `ws`[^bench-kill]. With `ws` holding the only
  descriptor, the lock would be free, and the next `ws` command would
  delete the workspace while the shell is still using it.

### Replace `ws` with the shell (`exec`)

**Pros**

- No signal handling at all, and the exit status is the shell's by
  construction.

**Cons**

- Nothing is left to remove the workspace when the shell exits; it would
  wait for the next `ws` command, against 0015's "When the shell exits …
  `ws` deletes the workspace".

### Stop the shell when the command is cancelled

Tie the shell to `ws`'s cancellation, as `ws` does for git, so that Ctrl-C
or SIGTERM to `ws` kills it.

**Pros**

- The same lifetime rule as every other child process `ws` starts.

**Cons**

- A shell without job control shares `ws`'s process group[^bench-jobs],
  so Ctrl-C meant for a command in that shell would kill the shell too,
  ending the session.
- Cancellation kills the shell outright by default[^go-cancel], so it
  can't pass SIGHUP on to its jobs before exiting, as bash does when it
  gets SIGHUP itself[^bash-signals].

## Consequences

- 0016's status line notes this amendment.
- 0016's "the shell's exit code" now includes 128+N for a shell killed by
  a signal, and `ws shell` can fail with `internal` before the shell
  starts.
- Workspaces left by a killed `ws` depend on 0015's removal pass, which
  needs the session record to tell a shell workspace from an agent one.
- The session record is written after the workspace directory is moved
  into place, so a `ws` killed in between leaves a complete workspace with
  no session record and a free lock. The removal pass must treat a
  workspace that is unlocked, has no session record, and is over a minute
  old as abandoned; the minute keeps it away from a creation that is still
  between the move and the record, as with 0022's leftovers.
- Conformance: tests check that the lock is held while the session is
  recorded, while the shell runs, and during removal, and is free
  afterwards; that the shell gets it as fd 3 and a command it starts in the
  background keeps it; that SIGINT isn't passed on, SIGHUP is, and SIGTERM
  reaches the shell as SIGHUP; that a signal during removal doesn't stop
  it; that a
  shell killed by signal N gives 128+N; that a failed removal keeps the
  shell's status; and, end to end with a fake shell and local upstreams,
  that the workspace exists with its lock held while the shell runs and is
  gone afterwards.
- **Unverified:** the measurements were on macOS only. To be repeated on
  Linux when Linux binaries are built.

## References

[^flock]: flock(2), macOS 26.6.2 (Command Line Tools SDK), NOTES. "Locks are on files, not file descriptors. That is, file descriptors duplicated through dup(2) or fork(2) do not result in multiple instances of a lock, but rather multiple references to a single lock."
[^go-cloexec]: Go 1.27.1 source, `src/os/file_unix.go`: files are opened with `open(name, flag|syscall.O_CLOEXEC, …)`.
[^go-extrafiles]: Go 1.27.1, `go doc os/exec.Cmd`, field `ExtraFiles`: "ExtraFiles specifies additional open files to be inherited by the new process. It does not include standard input, standard output, or standard error. If non-nil, entry i becomes file descriptor 3+i."
[^go-cancel]: Go 1.27.1, `go doc os/exec.CommandContext`: "The provided context is used to interrupt the process (by calling cmd.Cancel or os.Process.Kill) if the context becomes done before the command completes on its own. CommandContext sets the command's Cancel function to invoke the Kill method on its Process".
[^execve]: execve(2), macOS 26.6.2, DESCRIPTION. "Signals set to be ignored in the calling process are set to be ignored in the new process. Signals which are set to be caught in the calling process image are set to default action in the new process image."
[^sigaction]: sigaction(2), macOS 26.6.2, ERRORS. "[EINVAL] An attempt is made to ignore or supply a handler for SIGKILL or SIGSTOP."
[^termios]: termios(4), macOS 26.6.2, Special Characters, INTR: "Generates a SIGINT signal which is sent to all processes in the foreground process group for which the terminal is the controlling terminal." QUIT: "Generates a SIGQUIT signal which is sent to all processes in the foreground process group".
[^bash-signals]: bash(1), GNU bash 3.2.57, macOS 26.6.2, SIGNALS. "When bash is interactive, in the absence of any traps, it ignores SIGTERM (so that kill 0 does not kill an interactive shell)"; "Non-builtin commands run by bash have signal handlers set to the values inherited by the shell from its parent"; "The shell exits by default upon receipt of a SIGHUP. Before exiting, an interactive shell resends the SIGHUP to all jobs, running or stopped."
[^bash-exit]: bash(1), same version, EXIT STATUS. "When a command terminates on a fatal signal N, bash uses the value of 128+N as the exit status."
[^zsh-exit]: zshmisc(1), zsh 5.9, macOS 26.6.2, SIMPLE COMMANDS. "The value of a simple command is its exit status, or 128 plus the signal number if terminated by a signal."
[^bench-pty]: Measured 2026-10-19 on an Apple M4, macOS 26.6.2, git 2.50.1, Go 1.27.1, with a build of `ws shell` that does what this ADR decides, against a local bare repo reached through a `GIT_CONFIG_GLOBAL` `insteadOf` rule, with an empty `HOME`. Run under `script -q /dev/null`, with input written to the pseudo-terminal: `ps -o pid,ppid,pgid,tpgid,comm -p $$,$PPID`, `lsof -a -p $$ -d 3`, `sleep 3 &` followed by `lsof -a -p $! -d 3`, a separate `Flock(LOCK_EX|LOCK_NB)` check on `state/<id>/lock`, Ctrl-C (`\003`) at the prompt, `sleep 5` interrupted with Ctrl-C, an `echo` after each, and `exit 3`. 3 runs each with `SHELL` set to `/bin/zsh` (zsh 5.9), `/bin/bash` (bash 3.2.57), `/bin/sh`, and `/bin/dash`; every run gave the same result: the shell's process group differed from `ws`'s, fd 3 was the lock in the shell and in `sleep`, the check said "in use", both `echo`s ran, `ws` exited 3, and the workspace and its state were gone afterwards.
[^bench-signals]: Measured 2026-10-19, same setup, with the prototype passing SIGHUP and SIGTERM on to the shell unchanged; zsh and bash, 3 runs each. `kill -TERM`, `kill -HUP`, or killing the `script` process with SIGKILL (which closes the pseudo-terminal) while the shell sat at its prompt, then `exit 4`. SIGTERM: one second later `ws` and the shell were both running and the lock was in use; after `exit 4`, `ws` exited 4 and the workspace was gone. SIGHUP: the shell had exited (bash status 129, zsh 1) and the workspace was gone. Closed terminal: `ws` and the shell had exited and the workspace was gone.
[^bench-term]: Measured 2026-10-19, same setup, with a build of `ws shell` that sends the shell SIGHUP for SIGTERM; zsh and bash, 3 runs each. `kill -TERM` on `ws` while the shell sat at its prompt: one second later `ws` and the shell had exited, `ws` with status 129 under bash and 1 under zsh, and the workspace and its state were gone. `kill -HUP` gave the same results.
[^bench-kill]: Measured 2026-10-19, same setup, zsh and bash, 3 runs each. `script -q /dev/null /bin/sh -c '<ws shell …>; sleep 4'`, so the pseudo-terminal stays open after `ws` dies; `kill -KILL` on `ws` with the shell at its prompt. One second later the shell was running, ran a further command, and the lock was in use; after `exit 4` the lock was free and `workspaces/<id>` and `state/<id>` were still there.
[^bench-jobs]: Measured 2026-10-19, same setup, 3 runs each. With zsh and bash: `sleep 30`, then Ctrl-Z (`\032`); `ps -o stat=` on `ws` showed `S` (not stopped), `jobs` listed the `sleep` as stopped, and after `kill -KILL %1; exit 0`, `ws` exited 0 and the workspace was gone. With `SHELL` set to a `/bin/sh` script that printed `ps -o pgid= -p $$,$PPID` and looped on `sleep 0.1`: both process group ids were equal; Ctrl-C (`\003`) made `ws` exit 130 and the workspace was gone.
