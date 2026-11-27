# 0085. `terminal.open` and `agent.send` run on ConPTY on Windows: one `ptyproc.Proc` API, the console closed on exit and on Close, no job object; `platform_unsupported` only without pseudo-terminals

- Status: Proposed
- Date: 2026-11-27

## Context

### Problem

[0081](0081-windows-environment-agent.md) D5 refused `terminal.open` (attach
and shell) and `agent.send` on Windows with `platform_unsupported`. Can the
Windows agent offer the same terminals and sends as Linux and macOS, with
the same API and the same close behaviour, and keep the agent's own process
tree free of leftovers, and can we show that in CI?

### What a solution needs

1. **Same API and behaviour as unix:** `envagent` and `claude.Send` call one
   `ptyproc.Proc` on every OS.
2. **Close never hangs:** the agent's `Close` returns in bounded time even
   when the console or a program in it is stuck.
3. **Nothing outlives a hang-up that wouldn't on unix:** programs attached to
   the console end with it; programs a terminal started on purpose to
   outlive it (a daemon) keep running.
4. **Provable in CI:** a Windows test in the `windows` job, not a claim.
5. **No new dependency:** `golang.org/x/sys` already has the calls.

### Relevant facts

- A pseudoconsole needs `CreatePseudoConsole`, a `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`
  attribute, and `ClosePseudoConsole` to end its output; the caller must
  close its own copies of the pipe ends it passed in[^cps][^close].
- `ClosePseudoConsole` returns only after the console has written its last
  frame, so a pipe nobody reads can block it, on Windows before 11 24H2[^close].
- `CreateProcess` with `STARTF_USESTDHANDLES` and null standard handles
  keeps the child's output on the console, not on the parent's redirected
  stdio[^cpw].
- A pipe read that ends with `ERROR_BROKEN_PIPE` returns `io.EOF` in Go, and a
  pipe `Close` cancels a blocked read with `CancelIoEx`[^poll].
- A job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` ends the whole job
  when its last handle closes; children started with
  `CREATE_BREAKAWAY_FROM_JOB` can leave it, and libuv does not pass that flag
  for detached children[^libuv].
- Measured 2026-11-27 on this pod (linux/amd64): `GOOS=windows` cross-build,
  vet and lint pass for the prototype in
  [design.md](../design/windows-conpty/design.md). No Windows host was
  available, so no Windows test has run.

## Decision

We will build ConPTY in `internal/ptyproc` and use it for terminals and
`agent.send` on Windows 10 1809 and later:

- **One `Proc`.** The unix struct moves verbatim to `proc_unix.go`; Windows
  has its own `Proc` with the same methods (`Read`, `Write`, `Resize`,
  `Close`, `Wait() *int`). No interface is added to `ptyproc`.
- **Start.** Two pipes, `CreatePseudoConsole` at the requested size, our
  copies of the console's pipe ends closed at once, `CreateProcess` with
  `STARTF_USESTDHANDLES` and null standard handles, and the environment block
  from `Spec.Env` plus `SYSTEMROOT`.
- **EOF on exit.** After the process exits, `Wait` returns and the console is
  closed, so `Read` drains the output and then returns `io.EOF`.
- **Close.** Drain the output, close the console (programs attached to it
  get `CTRL_CLOSE_EVENT`), kill the root process after 2 s, and close the
  pipes. Each wait is bounded (5 s), so the worst case is about 12 s. If the
  console never closes, the pipes are closed in the background and
  `errConsoleStuck` is returned.
- **No job object.** Closing the console ends programs attached to it;
  detached daemons started from a terminal survive, as they do on unix.
- **`Supported()`** is true when kernel32 exports `CreatePseudoConsole`.
  `platform_unsupported` remains for systems without pseudo-terminals.

**Why this option.** It meets criteria 1, 2 and 5 with the calls `x/sys`
already has. Criterion 3 holds for attached programs and for detached
daemons, as on unix. It is weakest where the Windows API is least
documented: the drain-and-bounded-close path (criterion 2) is only proven by
the CI test "an interactive cmd nobody reads". Criterion 4 is met only in CI.

## Alternatives

### Job object with `KILL_ON_JOB_CLOSE`

What it is: put the console's programs in a job and close the job on Close.

**Pros**

- Ends every process in the job, including grandchildren, on Close.

**Cons**

- Kills daemons started from a terminal (`claude --bg`, `ws`'s own daemon)
  unless they break away, and libuv does not break away for detached
  children[^libuv].
- 0081 rejected job objects for `procgroup` for a related reason.

### A third-party ConPTY package

What it is: `github.com/UserExistsError/conpty` or `charmbracelet/x/conpty`.

**Pros**

- Ready-made Windows pseudo-terminal code.

**Cons**

- A new module for about 200 lines over `x/sys`, which has every call.
- Their close paths do not meet criterion 2 (unverified; to check when the
  package is read in detail).

### Overlapped (named) pipes for the console

What it is: cancellable reads on named pipes.

**Pros**

- Reads can be cancelled without `CancelIoEx`.

**Cons**

- More code. `CreatePipe`'s synchronous pipes with Go's `CancelIoEx` on
  `Close` are enough, and `os.NewFile` gives `io.EOF` for free.

## Consequences

- Supersedes 0081 D5. Amends 0076's Windows amendment: terminals and
  `agent.send` now run on Windows where pseudo-terminals exist.
- No new error code: `platform_unsupported` stays for systems without
  pseudo-terminals ([0016](0016-output-contract.md) unchanged). No protocol change.
- A new CI job, `windows-exe`, builds `ws.exe` for each PR. The Windows tests
  in `ptyproc`, `claude`, `envagent` and `cli` run in the existing `windows`
  job.
- Conformance: the CI tests named in design.md "Risks"; the smoke steps in
  design.md "How to test on Windows" are for a person, and are not done.
- **Unverified:** the pseudoconsole's behaviour on Windows 11 24H2's changed
  `ClosePseudoConsole`; Windows older than 1809 (`Supported()` false, not
  testable on Server 2022); `claude attach` on native Windows (ADR 0076
  question 1); `ws shell` inside a pseudoconsole on a real profile; arm64 at
  run time (cross-built and vetted only).
- Follow-up: a Windows controller for `ws attach` and `ws shell --env`
  (design.md D9).

## References

[^cps]: Microsoft, "Creating a Pseudoconsole session." <https://learn.microsoft.com/windows/console/creating-a-pseudoconsole-session> (accessed 2026-11-27).
[^close]: Microsoft, "ClosePseudoConsole function." <https://learn.microsoft.com/windows/console/closepseudoconsole> (accessed 2026-11-27).
[^cpw]: Microsoft, "CreateProcessW function," `STARTF_USESTDHANDLES`. <https://learn.microsoft.com/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw> (accessed 2026-11-27).
[^libuv]: libuv, `src/win/process.c`, handling of `CREATE_BREAKAWAY_FROM_JOB` for detached children (accessed 2026-11-27).
[^poll]: Go standard library, `internal/poll/fd_windows.go`: `ERROR_BROKEN_PIPE` mapped to `io.EOF`; pipe `Close` calls `CancelIoEx` (accessed 2026-11-27).
