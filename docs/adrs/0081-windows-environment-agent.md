# 0081. `ws env agent run` builds and runs natively on Windows: per-OS files for processes, locks, key ACLs and the daemon socket; terminals and `agent.send` refuse with `platform_unsupported` until ConPTY

- Status: Proposed
- Date: 2026-11-24

## Context

### Problem

[0076](0076-environment-platforms-and-phasing.md) shipped the environment
agent for Linux and macOS and left Windows to a spike, saying that until
then `ws env agent run` "exits with `unsupported`". The user's Windows rig is
half the reason for remote environments ([0072](0072-remote-environments.md)).
What has to change for `ws env agent run` to build and run on Windows, which
parts can we show are right without a Windows machine, and which must
refuse?

### What a solution needs

1. **Unix behaviour unchanged**: Linux and macOS keep every test and every
   code path; Windows code lives beside it, not instead of it.
2. **Honest scope**: what is compiled, what is tested on Windows in CI, and
   what nobody has run, are stated separately.
3. **The agent's core works**: enrollment and its key, the MQTT connection,
   `workspace.create/list/rm` through git, starting Claude Code, reviews and
   loop ticks.
4. **What can't work refuses clearly**, with a code and a pointer here, not
   an `internal` error.
5. **No new dependencies** if the standard library and `golang.org/x/sys`
   cover it.

### Relevant facts

- `GOOS=windows go build ./...` fails in 7 packages (`backup`, `gate`,
  `lockfile`, `claude`, `mcp`, `daemon`, `cli`) and `go vet` in 6 test
  files; all from `Setpgid`/`Setsid`/`Setctty`, `syscall.Kill`,
  `syscall.SIGWINCH`, `syscall.Getpgrp`, `syscall.Stat_t`, `unix.Flock` and a
  missing `peerUID`[^build]. The list is in
  [constraints.md](../design/env-windows/constraints.md).
- The `unsupported` exit 0076 describes was never built[^grep].
- `golang.org/x/sys` v0.50.0 is already a direct dependency and its
  `windows` package has `LockFileEx`, `SetNamedSecurityInfo`,
  `CreateToolhelp32Snapshot`, `TerminateProcess` and
  `CreatePseudoConsole`[^xsys].
- `os/exec` refuses `ExtraFiles` on Windows[^extrafiles]; a `LockFileEx`
  lock belongs to the handle that took it, and an inherited handle doesn't
  grant the child the lock[^lockfileex].
- Go's `net` package supports `AF_UNIX` on Windows 10 1803 and later;
  there is no `SO_PEERCRED`[^afunix].
- `os.UserConfigDir()` on Windows is `%AppData%` (Roaming); `os.UserCacheDir()`
  is `%LocalAppData%`[^userdir].
- Git for Windows ships `sh.exe` (`<Git>\bin\sh.exe`, `<Git>\usr\bin\sh.exe`)
  and needs `core.longpaths=true` for paths over 260 characters[^gitwin].
- Whether `claude --bg`, `claude attach` and `claude daemon run` work on
  native Windows is still **not known**: nothing here was run on Windows.

## Decision

We will port the agent natively (0076's outcome (a)) as far as it can be
shown correct by cross-compiling, vetting and an allow-list of tests on a `windows-latest` CI job, and
make the parts that need a pseudo-terminal refuse.

**D1. Processes: `internal/procgroup`.** One package owns process groups,
with `procgroup_unix.go` (today's code, moved verbatim) and
`procgroup_windows.go`:

| Function | Unix | Windows |
| --- | --- | --- |
| `OwnGroup() *syscall.SysProcAttr` | `Setpgid: true` | `CreationFlags: CREATE_NEW_PROCESS_GROUP` (Ctrl-C at ws's console doesn't reach it) |
| `Detached() *syscall.SysProcAttr` | `Setsid: true` | `CREATE_NEW_PROCESS_GROUP \| DETACHED_PROCESS` (no console; outlives ws) |
| `KillTree(pid int) error` | `kill(-pid, SIGKILL)` | `TerminateProcess` on `pid` and every descendant found in a Toolhelp32 snapshot, skipping any whose creation time is before its parent's (a reused pid) |
| `Signal(pgid int, sig syscall.Signal) error` | `kill(-pgid, sig)` | `KillTree(pgid)` for any signal: there is no graceful stop between processes |

`gate`, `mcp`, `backup`, `claude` and `daemon` call these instead of
`syscall`. A Job Object would catch orphaned grandchildren, but a process
must be assigned to it after `Start`, and `mcp`'s `CommandTransport`
starts the command itself, so there is no point to assign it; and
`taskkill /T` walks the same parent links through an external program.
Orphans whose parent already exited survive `KillTree`: recorded below.

**D2. Locks.** `lockfile_windows.go` takes `LockFileEx` on the whole file
(`LOCKFILE_FAIL_IMMEDIATELY`, plus `LOCKFILE_EXCLUSIVE_LOCK` for exclusive)
and polls every 50 ms as the unix code does; `ERROR_LOCK_VIOLATION` means
held. Windows locks are mandatory, which is harmless here because every
lock file is only ever locked, never read. `Lock.File()` stays, but
`internal/shell` doesn't hand it to the shell on Windows (no `ExtraFiles`):
`ws shell` holds the lock while it waits for the shell, and jobs the shell
leaves running after it exits don't (ADR [0023](0023-shell-session-keeps-creation-lock.md)'s
guarantee is weaker on Windows).

**D3. Private files: `internal/privfile`.** `RestrictDir(dir)` and
`RestrictFile(path)` are no-ops on unix (the callers' `0700`/`0600` stay
as they are) and on Windows set a **protected** DACL with one entry: the
current user's SID (from the process token), full control, inherited by
files and subdirectories. `CheckPrivate(path)` reports an error unless the
owner is the current user and the DACL grants nobody else access. The
agent's identity directory (`CONFIG_DIR/environment/`), its key and every
file `writeFileAtomic` writes there, the controller's CA directory, and the
daemon's socket directory use them. We chose x/sys/windows over `icacls`
(a subprocess with localized output and no error codes) and over DPAPI
(the same user can decrypt it, so it adds only protection from offline
disk reads, at the cost of a key format `tls.LoadX509KeyPair` can't read;
a follow-up if wanted).

**D4. The daemon socket stays AF_UNIX.** On Windows it lives in
`os.TempDir()\ws\<hash>.sock` (`%LocalAppData%\Temp`, per user) unless
`XDG_RUNTIME_DIR` is set; its directory gets `privfile.RestrictDir` and
`prepareSocketDir` checks `privfile.CheckPrivate` instead of the uid and
mode. `peerUID` on Windows returns the listener's own uid, so the DACL on
the directory is the only check, which we can't test with a second user. A
named pipe with a DACL is the better long-term design but needs a new
dependency (`go-winio`) or a hand-written listener; it's a follow-up.

**D5. Terminals refuse.** On Windows, `terminal.open` (both `attach` and
`shell`) and `agent.send` (which types into `claude attach` in a pty) answer
the new error code **`platform_unsupported`** (exit 1), with a message
naming the operation and this ADR. `ptyproc_other.go` and
`claude/pty_other.go` already return `ErrUnsupported`; the agent maps it.
ConPTY (`CreatePseudoConsole` plus `CreateProcess` with
`PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`, which `os/exec` can't pass) is
about 300 lines of Win32 that nothing here can run, and its only callers
are `claude attach` and `ws shell`, whose Windows behaviour is unknown. It
is a follow-up with its own task once `claude attach` is known to work. One
code, not `terminal_unsupported`, because `agent.send` refuses for the same
reason. The protocol number doesn't change: controllers that predate this
show the code as an unknown remote code (exit 1).

**D6. Shells.** Loop gate scripts ([0049](0049-loop-scripts-and-variables.md))
are POSIX shell; on Windows `internal/gate` runs them with `sh` from `PATH`,
else `<dir of git.exe>\..\bin\sh.exe`, else `..\usr\bin\sh.exe` (Git for
Windows, which [0003](0003-use-git-cli.md) already requires), and fails
with a clear error naming Git for Windows if none exists. `cmd.exe` would
silently change what every existing script means. `ws shell` on Windows
runs `%COMSPEC%` (default `cmd.exe`) with no `-i`, ignoring `SHELL`
(Git Bash sets it to an MSYS path).

**D7. Paths.**

- `CONFIG_DIR` defaults to `%LocalAppData%\workspaces` on Windows (not
  Roaming); `WS_CONFIG_DIR` and `--config-dir` still win. Unix is unchanged.
- `internal/git` passes `-c core.longpaths=true` on Windows.
- Claude Code's directories fall back from `HOME` to `USERPROFILE` on
  Windows (assumed `%USERPROFILE%\.claude`; unverified).
- `envagent`'s `underRoot` compares case-insensitively on Windows
  (`strings.EqualFold` on the cleaned prefix, then a separator).
- `artifact`'s directory sync is a no-op on Windows (NTFS journals the
  rename; `FlushFileBuffers` on a directory handle isn't allowed).

**D8. `ws env agent run` has no blanket refusal.** It runs on Windows;
the parts in D5 refuse one by one. Running it as a service is documented as
a per-user Task Scheduler task "at log on" (Claude Code's sign-in is per
user, so a `LocalSystem` service would find `claude` signed out), marked
unverified. WSL2 stays documented as the alternative that runs the Linux
agent unchanged.

**D9. CI and release.** A `windows-cross` job on ubuntu cross-builds,
vets, lints and compiles every test binary for `windows/amd64` and
`windows/arm64`; a `windows` job on `windows-latest` runs `go build ./...`,
`go vet ./...`, and `go test` for an **explicit allow-list** of packages,
then the whole suite as an informational step (`continue-on-error`) whose
failures decide what joins the list. `make dist` adds
`windows/amd64` and `windows/arm64` as `ws-windows-<arch>.zip` holding
`ws.exe`.

### Why this option

1. Criterion 1: every unix change is a verbatim move into a `_unix.go`
   file; `go test` on linux and the macOS CI legs are unchanged.
2. Criterion 2: [constraints.md](../design/env-windows/constraints.md)
   lists what is cross-checked, what CI runs on Windows, and what nobody
   ran. The roadmap entry stays unchecked until a person runs the agent on
   Windows.
3. Criterion 3: enrollment, the key (D3), MQTT (pure Go), git, Claude
   Code's start (D1), reviews and loops (D6) all compile and have a Windows
   path; only Claude Code itself is unknown.
4. Criterion 4: D5's one code.
5. Criterion 5: only `golang.org/x/sys/windows`, already in `go.mod`.

Where it's weak: if `claude --bg` doesn't work natively, the agent connects
but can't start an agent, and WSL2 becomes the answer anyway; `KillTree`
misses orphans; the socket's only guard is a DACL nobody tested with a
second user; no terminals.

## Alternatives

### WSL2 only

The rig runs the Linux agent inside WSL2 and counts as a Linux environment.

**Pros:** no port; known to work wherever Linux works.
**Cons:** the user's tools, repos and GPU setup live on the Windows side;
paths and line endings differ; WSL2 must be running. Kept as documented
fallback.

### ConPTY now

**Pros:** `ws attach` and `ws shell --env` on Windows.
**Cons:** the largest, least verifiable part, for callers whose Windows
behaviour is unknown. Deferred (D5).

### Named pipe for the daemon

**Pros:** the native IPC with a per-pipe DACL and client identity
(`GetNamedPipeClientProcessId`).
**Cons:** a new dependency or a hand-written listener; the daemon's HTTP
server already works over `net.Listener` on AF_UNIX. Deferred (D4).

### Job Objects for process trees

**Pros:** catch every descendant, orphans included.
**Cons:** needs a hook after `Start` that `exec.Cmd` and `mcp`'s transport
don't give, and assignment races the child's own children (D1).

## Consequences

- New packages `internal/procgroup` and `internal/privfile`; new
  `_windows.go` files in `lockfile`, `daemon`, `shell`, `gate`, `cli`,
  `config`, `git`, `claude`, `envagent`, `artifact`.
- New error code `platform_unsupported` (exit 1), in
  [0016](0016-output-contract.md)'s list.
- `make dist` and releases gain two `.zip` files.
- Conformance: the `windows-cross` and `windows` CI jobs; the Windows tests
  of `lockfile`, `privfile`, `procgroup`, `daemon` paths, `envagent`
  `underRoot` and `gate`'s `sh` lookup run on `windows-latest`.
- Amends [0076](0076-environment-platforms-and-phasing.md): its "Until
  then" sentence is replaced by D8, and its spike's question 2 is answered
  by [constraints.md](../design/env-windows/constraints.md).
- **Unverified:** Claude Code on native Windows (0076's question 1);
  ConPTY; a Task Scheduler or Service run; the DACLs against a second user;
  AF_UNIX's access check; long paths without the system policy;
  case-insensitive name clashes; `os.Rename` over a file another process
  holds open.
- **Follow-ups:** ConPTY terminals and `agent.send`; a named-pipe daemon
  transport; DPAPI for the key; the terminal environment allow-list's
  Windows variables (`SystemRoot`, `USERPROFILE`, `APPDATA`, `COMSPEC`,
  `PATHEXT`, …, compared case-insensitively) once terminals exist; a
  controller on Windows (`ws attach` there needs a raw console and resize
  events in place of `SIGWINCH`).

## References

[^build]: Measured 2026-11-24 in the `env-windows-design` workspace (linux/amd64, Go 1.27.3) at `8b6cce7`: `GOOS=windows GOARCH=amd64 go build -gcflags=-e ./...`, then the same after stubbing each failing package in a scratch copy, until green; then `GOOS=windows go vet ./...` (amd64 and arm64) and `GOOS=windows go test -c -o /dev/null -gcflags=-e` for every package.
[^grep]: `grep -rn unsupported internal/cli` at `8b6cce7` finds no check in `runEnvAgentRun` (`internal/cli/env_agent.go:136`).
[^xsys]: Checked 2026-11-24 by grepping `$(go env GOMODCACHE)/golang.org/x/sys@v0.50.0/windows/*.go` for each function.
[^extrafiles]: Go, `os/exec` package documentation, `Cmd.ExtraFiles`: "ExtraFiles specifies additional open files to be inherited by the new process. … It does not include standard input, standard output, or standard error. If non-nil, entry i becomes file descriptor 3+i. ExtraFiles is not supported on Windows." <https://pkg.go.dev/os/exec#Cmd> (accessed 2026-11-24).
[^lockfileex]: Microsoft, "LockFileEx function." <https://learn.microsoft.com/windows/win32/api/fileapi/nf-fileapi-lockfileex> (accessed 2026-11-24): "If the locking process opens the file a second time, it cannot access the specified region through this second handle until it unlocks the region." and, on `LockFile`, "If a process terminates with a portion of a file locked or closes a file that has outstanding locks, the locks are unlocked by the operating system."
[^afunix]: Microsoft, "AF_UNIX comes to Windows," Windows Command Line blog, 2017-12-19 <https://devblogs.microsoft.com/commandline/af_unix-comes-to-windows/> (accessed 2026-11-24); Go's `net` package listens on `unix` on Windows since Go 1.12.
[^userdir]: Go, `os.UserConfigDir` and `os.UserCacheDir` documentation: "On Windows, it returns %AppData%" and "On Windows, it returns %LocalAppData%." <https://pkg.go.dev/os#UserConfigDir> (accessed 2026-11-24).
[^gitwin]: Git for Windows, `core.longpaths` in its FAQ; `sh.exe` paths as installed by the Git for Windows installer. Not checked on a Windows machine for this ADR.
