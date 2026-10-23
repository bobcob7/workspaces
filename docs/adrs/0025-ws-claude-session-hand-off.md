# 0025. `ws claude` keeps creation's lock until the session is recorded, lets `claude --bg` finish, and becomes `claude attach` for an idle session on a terminal

- Status: Accepted (2026-10-19); orphaned sessions in a one-repo workspace's repo amended by [0028](0028-single-repo-session-directory.md) (proposed)
- Date: 2026-10-19

## Context

### Problem

[0014](0014-command-set.md) and [0015](0015-workspace-lifetime-follows-session.md)
decide what `ws claude` does: create the workspace, run `claude --bg` in it,
record the session's id in `state/<id>/session`, print the ids, and remove
the workspace if the session can't start; with no prompt, attach the
terminal to the session instead of printing.
[0022](0022-workspace-record-before-rename.md) has creation hold
`state/<id>/lock` and [0023](0023-shell-session-keeps-creation-lock.md)
hands that lock to `ws shell`'s shell, and says the removal pass must treat
a workspace that is unlocked, has no session record, and is over a minute
old as abandoned. None of them says:

- what the lock does between creation and the agent's session record;
- what happens when `ws` is interrupted, or its terminal closes, while
  `claude --bg` runs;
- what the session is named;
- how `ws` knows there was "no prompt", given that the arguments after `--`
  may hold options such as `--model` and no prompt;
- whether to attach when there is no terminal, or with `--json`;
- how `ws` attaches, and what its exit status is then.

### What a solution needs

1. **A workspace never looks abandoned while it's being handed to its
   agent**, and an agent workspace is recorded as one.
2. **Nothing is left behind** when the session can't start: no workspace,
   and no session pointing at a removed directory.
3. **Attaching behaves like `claude attach`**: its keys, its signals, its
   exit.
4. **Callers that aren't people get a result**, never a terminal user
   interface: agents, scripts, `--json`.
5. **Simple**, with as little of Claude Code's human-oriented text parsed
   as possible ([0024](0024-claude-code-client.md)).

### Relevant facts

- **A session record needs the id**, and the id is known only once
  `claude --bg` has printed it ([0024](0024-claude-code-client.md)).
  `claude --bg` with no prompt took 0.43 to 0.61 s[^bench-pgrp].
- **The lock isn't seen by the session.** A lock passed to `claude --bg`
  was free as soon as it returned; the session runs under a long-lived
  service ([0015](0015-workspace-lifetime-follows-session.md), footnote
  `bench-bg-lock`).
- **An interrupted `claude --bg` can still leave a session.** Sent SIGINT,
  SIGTERM, or SIGKILL 0.1 s after it started, no session was left; at
  0.3 s, each left a session and was still reported as killed by the
  signal; after SIGINT at 0.2 s a session was left although nothing had
  been printed[^bench-signal].
- **Closing the terminal sends SIGHUP**, which "is sent when a program
  loses its controlling terminal" and by default "causes the program to
  exit"; a SIGQUIT (Ctrl-\\) makes it "exit with a stack dump", and
  `Notify` "disables the default behavior" for the signals it is given
  until it is stopped[^go-signal]. A build of `ws` that didn't catch
  SIGHUP, sent it while `claude --bg` ran, was killed by it with no
  output and no session record, and the session had started; a build
  that caught it finished the hand-off and printed the result in each of
  3 runs[^bench-hup].
- **The terminal signals its foreground process group**: Ctrl-C "Generates
  a SIGINT signal which is sent to all processes in the foreground process
  group"[^termios]. A child started in a process group of its own isn't in
  it[^go-setpgid].
- **`claude --bg` in its own process group** under a pseudo-terminal
  finished normally, exit 0 with its usual output, including when SIGINT
  was sent to the foreground group 0.15 s after it started[^bench-pgrp].
- **The backgrounded line says whether there was a prompt.** Without a
  prompt, with or without other options such as `--model`, it read
  `backgrounded · <id> · <name> (idle — send a prompt to start)` in every
  run[^bench-idle]. With a prompt, Claude Code 2.1.302 printed
  `backgrounded · <id> · <name>` with nothing after the name
  ([0014](0014-command-set.md), footnote `bench-bg-start`).
- **`claude attach`** "Open[s] the background session in this terminal. ←
  returns to agent view, Ctrl+Z drops back to your shell. The session keeps
  running either way"[^attach-help]. Run under a pseudo-terminal as the
  child of another process, it stayed in that process's group; Ctrl+Z made
  it exit 0; Ctrl-C printed "Press Ctrl-C again to detach (session keeps
  running)" and didn't signal its parent; SIGTERM, SIGHUP, or SIGINT sent to
  it killed it with that signal; the session kept running in every
  case[^bench-attach].
- **Without a terminal, `claude attach` returns at once.** With stdin from
  `/dev/null` it exited 0 in 0.32 to 0.42 s and wrote 209 bytes of terminal
  control sequences to stdout[^bench-attach-notty].
- **Replacing a process keeps its place.** After `execve`, "the new process
  also inherits … process ID … parent process ID … process group ID …
  working directory"; descriptors stay open "except for those for which the
  close-on-exec flag is set"; and "signals which are set to be caught in
  the calling process image are set to default action in the new process
  image"[^execve]. Go exposes it directly[^go-exec].

## Decision

We will hand an agent workspace to its session under creation's lock, let
`claude --bg` finish whatever happens to `ws`, and attach by replacing `ws`
with `claude attach`:

1. **Check first.** `ws claude` refuses `--settings` after `--` (`usage`,
   [0024](0024-claude-code-client.md)) and checks the Claude Code version
   before creating anything, so `claude_missing` and a too-old `claude`
   cost no clone.
2. **One lock until the record.** Creation returns with `state/<id>/lock`
   held. `ws` keeps holding it while `claude --bg` runs and while it writes
   the session record (`kind = "claude"`, the session's id, `started_at`),
   then releases it. `claude` never gets the lock's descriptor. The session
   is named after the workspace's id, which is made only of lowercase
   letters, digits, and inner hyphens
   ([0012](0012-workspace-layout.md)), not after the `--name` it was made
   from.
3. **`claude --bg` always finishes.** It runs in a process group of its
   own, and cancelling `ws` (Ctrl-C, SIGTERM) doesn't stop it, so `ws`
   always learns whether a session started and its id. `ws` interrupted
   before `claude --bg` starts removes the workspace and starts nothing,
   as an interrupted creation does. From just before `claude --bg` starts
   until the session record is written, `ws` also catches SIGHUP, so
   closing the terminal can't kill it partway; it doesn't catch SIGQUIT,
   which stays the way out of a `claude --bg` that hangs.
4. **On failure, nothing stays.** If `claude --bg` fails, `ws` removes the
   workspace, while still holding the lock, and fails with
   `claude_untrusted`, `claude_failed`, or `claude_missing`. For
   `claude_untrusted`, the message names the workspace root to trust and how
   (run `claude` there once and accept the prompt), and `details.directory`
   holds it. If the session record can't be written, `ws` runs `claude rm`
   on the new session, removes the workspace, and fails with `internal`.
5. **Attach only to an idle session, only for a person.** `ws` attaches
   when the backgrounded line has `(idle` right after the session's name,
   stdin and stdout are both terminals, `--json` wasn't given, and `ws`
   has had neither an interrupt nor a SIGHUP. Otherwise it prints the
   result, as with a prompt. A backgrounded line `ws` can't read, or an id found by 0024's
   fallback lookup, counts as not idle.
6. **Attach by becoming `claude attach`.** After releasing the lock, `ws`
   prints one line on stderr with the workspace id and path (none with
   `--quiet`), then replaces itself with `claude attach <id>`, with its
   own stdin, stdout, stderr, and environment; stdout stays empty. From
   then on its exit
   status is `claude attach`'s. If `claude attach` can't be run, `ws`
   warns, with the command to run, and prints the result, exiting 0.
7. **Lifetime removes orphaned sessions.** When the removal pass removes an
   abandoned workspace (0023's rule), it also runs `claude rm` on every
   session whose working directory is exactly that workspace. This is
   decided here and built with the Lifetime step.

### Why this option

1. **Never abandoned.** The lock is held from creation to the record, so
   the removal pass's rule from 0023 (unlocked, no record, over a minute
   old) never matches a workspace whose `claude --bg` is in flight. After
   the record, the workspace is an agent's, judged by Claude Code's
   listing ([0015](0015-workspace-lifetime-follows-session.md)), and the
   lock has no further use.
2. **Nothing left behind.** With `claude --bg` uninterrupted, a failure is
   always a reported one, and an untrusted directory leaves no
   session[^bench-trust]. Interrupting it instead could leave a session
   whose id `ws` never sees[^bench-signal]. With SIGHUP caught, only
   SIGKILL or SIGQUIT can stop `ws` in the 0.4 to 0.6 s window, and a
   session left by either is removed by decision 7.
3. **Attaching behaves.** Once replaced, the process is `claude attach`,
   in the same process group, with default signal actions[^execve], so
   Ctrl+Z, Ctrl-C, closing the terminal, and its exit status are exactly
   those of `claude attach`[^bench-attach]; a build of `ws` doing this
   attached, and Ctrl+Z left it with status 0 and the session
   running[^bench-ws]. There is nothing left for `ws`
   to do once it attaches: the session record is written and the lock is
   released. The line on stderr leaves the workspace's path on screen for
   after Ctrl+Z.
4. **Results for programs.** A caller without a terminal, or with
   `--json`, gets the ids and path, not 209 bytes of control
   sequences[^bench-attach-notty].
5. **Text parsed.** Only the backgrounded line, which 0024 already reads,
   and only a marker right after the name `ws` itself passed. That name is
   the workspace id, which can't contain "(idle", spaces, or a leading
   hyphen, so it is passed and matched exactly. If a release rewords the
   line, `ws` prints instead of attaching, which is safe.

The weak points:

- The prompted line was measured on 2.1.302 only; a new session with a
  prompt costs a model call, and wasn't repeated on 2.1.303.
- If `ws` dies from SIGKILL or SIGQUIT while `claude --bg` runs, the
  workspace is left unlocked with no record, and a session Claude Code
  accepted may be listed in it. Until the Lifetime step exists, that
  session stays listed with a missing working directory until `claude
  rm`; afterwards decision 7 removes it with the workspace, a minute
  later.
- A Ctrl-C during `claude --bg` doesn't stop it; `ws` finishes the hand-off
  and prints the result rather than attaching. A `claude --bg` that hangs
  holds `ws` until it ends: repeated Ctrl-C, SIGTERM, and SIGHUP are
  caught, and only Ctrl-\\ (SIGQUIT) or SIGKILL stop `ws`, with the
  consequences above.
- When the backgrounded line can't be read and 0024's fallback lookup
  doesn't find exactly one session, `claude --bg` has succeeded but `ws`
  has no id: it removes the workspace and fails with `claude_failed`. A
  session it couldn't name stays listed until `claude rm`, or, once the
  Lifetime step exists, until the next removal pass applies decision 7 to
  the workspace's directory.
- The session's name in `claude agents` is the workspace id, not the
  `--name` as typed: `--name "Fix Login"` lists as `fix-login`.
- After attaching, `ws` adds nothing: no `--verbose` timing, and a non-zero
  exit status may be `claude attach`'s rather than one from 0016's table,
  as with `ws shell`.

## Alternatives

### Release the lock before `claude --bg`

**Pros**

- Matches 0015's "`lock` is used only by shell workspaces" literally.

**Cons**

- For 0.4 s or more the workspace is unlocked with no record: abandoned by
  0023's rule apart from the minute, which is then the only protection.

### Write the session record before `claude --bg`, with an empty id

**Pros**

- The workspace is marked as an agent's from the start.

**Cons**

- An agent record with no id, unlocked after a killed `ws`, needs a rule
  of its own in the removal pass, and the record is written twice.

### Let cancellation stop `claude --bg`, then look for a session to remove

**Pros**

- Ctrl-C stops `ws` at once, as during creation.

**Cons**

- A session can appear without the id having been printed[^bench-signal],
  so `ws` would have to list sessions in the workspace and hope the
  session is already listed.

### Attach whenever nothing follows `--`

**Pros**

- Decided from `ws`'s own arguments, without reading Claude Code's output.

**Cons**

- `ws claude repo -- --model opus` starts an idle session and doesn't
  attach. Telling options from a prompt in general means knowing which of
  `claude`'s options take values.

### Run `claude attach` as a child and wait, as `ws shell` does

**Pros**

- `ws` stays in control: it can log, and its exit status is its own.

**Cons**

- `ws` must catch the terminal's signals as 0023 does, for nothing to do
  afterwards. If a release made `claude attach` stop itself on Ctrl+Z, `ws`
  would keep waiting while the terminal belongs to a stopped process.

### Name the session after the `--name` as typed

**Pros**

- `claude agents` shows the name the person chose, capitals and spaces
  included.

**Cons**

- The idle marker is matched after a name `ws` doesn't control the form
  of: spaces, "(idle", or a leading hyphen, which `claude` could read as
  an option. The listing and `ws list` show different names.

### Catch SIGQUIT as well as SIGHUP during the hand-off

**Pros**

- No terminal signal can stop `ws` between `claude --bg` and the record.

**Cons**

- A `claude --bg` that hangs could then only be stopped with SIGKILL
  from another terminal.

### Attach regardless of terminal and `--json`

**Pros**

- One rule, as 0014 states it.

**Cons**

- An agent running `ws claude repo` gets control sequences and an exit 0
  with no ids[^bench-attach-notty]; `--json` gets no object, against
  [0016](0016-output-contract.md).

## Consequences

- 0014's status line notes this amendment: `ws claude` attaches only to
  a session Claude Code reports idle, with a terminal on stdin and stdout,
  without `--json`, and not after an interrupt or SIGHUP; it says on
  stderr where the workspace is first; and the session is named after the
  workspace id.
- 0015's status line notes this amendment: an agent workspace also holds
  `lock`, from creation until its session record is written; and removing
  an abandoned workspace also runs `claude rm` on each session whose
  working directory is exactly that workspace.
- 0016's status line notes this amendment: once `ws claude` has attached,
  its exit status is `claude attach`'s, as `ws shell`'s is the shell's.
- 0024's status line notes this amendment: `claude attach` runs on `ws`'s
  terminal by replacing `ws`, the one exception to running claude with no
  stdin; and `claude --bg` runs in a process group of its own and isn't
  stopped by cancellation.
- The Lifetime step skips locked workspaces, and implements decision 7:
  when it removes an abandoned workspace, it first runs `claude rm` on
  each session that `claude agents --json --all` lists with exactly that
  working directory.
- Conformance: unit tests check the lock is held while `claude --bg` runs
  and while the record is written, and is free before attaching; that the
  start isn't stopped by cancellation and runs in its own process group;
  that a failed start or record removes the workspace (and the session);
  that SIGHUP is caught only from the start to the record, survives a real
  SIGHUP, and prevents attaching; the session named after the workspace
  id; the idle marker after the name; each attach condition; the line on
  stderr before attaching; that attaching replaces the process and passes
  on `claude attach`'s exit status; and,
  end to end with a fake `claude` and local upstreams, the record and the
  output, and that an untrusted workspace leaves nothing.
- **Unverified:** the prompted backgrounded line on 2.1.303; behaviour on
  Linux.

## References

[^termios]: termios(4), macOS 26.6.2, Special Characters, INTR: "Generates a SIGINT signal which is sent to all processes in the foreground process group for which the terminal is the controlling terminal."
[^go-setpgid]: Go 1.27.1, `go doc syscall.SysProcAttr`, darwin: "Setpgid sets the process group ID of the child to Pgid, or, if Pgid == 0, to the new child's process ID."
[^go-exec]: Go 1.27.1, `go doc syscall.Exec`: "Exec invokes the execve(2) system call."
[^execve]: execve(2), macOS 26.6.2, DESCRIPTION. "File descriptors open in the calling process image remain open in the new process image, except for those for which the close-on-exec flag is set"; "Signals which are set to be caught in the calling process image are set to default action in the new process image"; "The new process also inherits the following attributes from the calling process: process ID … parent process ID … process group ID … working directory".
[^attach-help]: Claude Code CLI 2.1.303, `claude attach --help` (2026-10-19).
[^go-signal]: Go 1.27.1, `go doc os/signal`: "the SIGHUP signal is sent when a program loses its controlling terminal"; "A SIGHUP, SIGINT, or SIGTERM signal causes the program to exit. A SIGQUIT, SIGILL, SIGTRAP, SIGABRT, SIGSTKFLT, SIGEMT, or SIGSYS signal causes the program to exit with a stack dump"; "Notify disables the default behavior for a given set of asynchronous signals and instead delivers them over one or more registered channels."
[^bench-hup]: Measured 2026-10-19, same setup, with a build of `ws` that does what this ADR decides, `WS_CONFIG_DIR` below a trusted directory, a local upstream reached through `insteadOf`, and stdin from `/dev/null`. A Python parent ran `ws claude --name <name> <spec>` with no prompt, polled `pgrep` for the `claude --bg` command line, and sent `ws` SIGHUP 0.1 s after it appeared. 3 runs: SIGHUP at 0.28, 0.36, and 0.37 s; `ws` exited 0 at 0.56, 0.68, and 0.65 s, printing `<id>  <session>  <path>`; each `state/<id>/session` held `kind = "claude"` and the session id; `claude agents --json --all` listed each session as idle, named after the workspace id. Control, one run, the same build with SIGHUP not caught: SIGHUP at 0.48 s killed `ws` with no output; `state/<id>/` held `lock` and `workspace.toml` but no session record, and the session was listed, idle. The sessions were then removed with `claude stop` and `claude rm`.
[^bench-idle]: Measured 2026-10-19 on an Apple M4, macOS 26.6.2, Claude Code 2.1.303, in new directories below a directory whose trust prompt had been accepted, stdin from `/dev/null`. `claude --bg -n <name> --settings '{"worktree":{"bgIsolation":"none"}}' --model haiku`: exit 0 in 0.45 s, first line `backgrounded · <id> · <name> (idle — send a prompt to start)`. The same command without `--model` printed the same form in each of the 13 further runs, from [^bench-signal] and [^bench-pgrp], that printed a line.
[^bench-signal]: Measured 2026-10-19, same setup. A Python 3.9.6 parent started `claude --bg -n <name> --settings <json>` with default signal actions, sent SIGINT, SIGTERM, or SIGKILL after 0.1, 0.2, or 0.3 s, one run each, and listed `claude agents --json --all --cwd <dir>` 3 s later. 0.1 s: no output, killed by the signal, no session. 0.2 s: no output, killed by the signal; a session for SIGINT only. 0.3 s: the backgrounded line, killed by the signal, and a session, for each signal.
[^bench-pgrp]: Measured 2026-10-19, same setup. Under a pseudo-terminal (Python `pty.fork`), a Python parent that owned the foreground process group started `claude --bg` with `setpgrp` in the child, stdin `/dev/null`, output to pipes. 3 runs: exit 0 in 0.43, 0.50, and 0.59 s with the backgrounded line. 3 runs that sent SIGINT to the parent's process group 0.15 s after the start: the parent got it, and `claude --bg` exited 0 in 0.52 to 0.61 s with the backgrounded line.
[^bench-attach]: Measured 2026-10-19, same setup, on an idle session started without a prompt. Under a pseudo-terminal, a Python parent ran `claude attach <id>` as a child and waited. `ps`-style ids printed by the parent: the child's process group was the parent's, which was the terminal's foreground group (3 runs). Ctrl+Z (`\x1a`) 5 s after the start: the child exited 0 (3 runs). Ctrl-C (`\x03`): the screen showed the quoted text; a second Ctrl-C 0.5 or 1 s later didn't end it within 3 to 4 s (1 run each), and the parent wasn't interrupted. SIGTERM, SIGHUP, or SIGINT sent to the child after 4 s: killed by that signal within 0.02 s (3 runs each). Afterwards `claude agents --json --all --cwd <dir>` listed the session with a `pid` and `status: "idle"`.
[^bench-attach-notty]: Measured 2026-10-19, same setup and session. `claude attach <id> </dev/null >out`: exit 0 in 0.32, 0.38, 0.42, and 0.37 s (4 runs); `out` held 209 bytes: alternate-screen, mouse and keyboard mode sequences and "Attaching…".
[^bench-trust]: See [0024](0024-claude-code-client.md), footnote `bench-trust`: in an untrusted directory `claude --bg` exited 1 and "No session was listed afterwards". Repeated 2026-10-19 with a build of `ws` that does what this ADR decides, `WS_CONFIG_DIR` in an untrusted scratch directory and a local upstream reached through `insteadOf`: `ws claude` exited 1 with `claude_untrusted` in 3.28 s (one run, including the first clone), `workspaces/` and `state/` were empty, and `claude agents --json --all --cwd <scratch>` returned `[]`.
[^bench-ws]: Measured 2026-10-19, same setup, with a build of `ws` that does what this ADR decides, `WS_CONFIG_DIR` below a trusted directory, and a local upstream reached through `insteadOf`; one run each. `ws claude --name "E2E Print" <spec> -- --model haiku </dev/null`: printed `e2e-print  <id>  <path>`, exit 0 in 0.57 s; `state/e2e-print/session` held `kind = "claude"` and the id, and an exclusive `flock` on its lock succeeded. `ws --json claude <spec>@main </dev/null`: one JSON object, exit 0. Under a pseudo-terminal, `ws claude --name "E2E Attach" <spec>`: the screen showed "Attaching…" and the session's name; Ctrl+Z 7 s later ended it with status 0; `claude agents --json --all` listed all three sessions with a `pid` and `status: "idle"`.
