# Design: ConPTY terminals and `agent.send` on Windows

Follow-up to [ADR 0081](../../adrs/0081-windows-environment-agent.md) D5,
which shipped the Windows environment agent with `terminal.open` (attach and
shell) and `agent.send` refusing with `platform_unsupported`. This design
builds them on ConPTY. The decision record is **ADR 0085** (written by T6
from the "Decisions" section below). The work is split in
[tasks.md](tasks.md); what this pod can and can't check is in
[constraints.md](constraints.md).

Written 2026-11-27 on `feat/windows-conpty` at `566af23`, Go 1.27.3,
`golang.org/x/sys v0.50.0`. Every Win32 call named here was checked in the
module cache, and a prototype of `ptyproc_windows.go`, its test file and
`claude.Send`'s split was compiled, vetted and linted for `GOOS=windows`
(amd64 and arm64) in a scratch worktree; the prototypes are the skeletons in
tasks.md. Nothing was run on Windows.

## Decisions

**D1. One `ptyproc.Proc` API on every OS.** `Proc` keeps its method set
(`Read`, `Write`, `Resize`, `Close`, `Wait() *int`), which is
`envagent.ptyProcess`. The unix struct and methods move verbatim from
`ptyproc.go` to `proc_unix.go` (`linux || darwin`); `ptyproc.go` keeps the
package doc, `ErrUnsupported`, `killGrace` and `Spec`. `ptyproc_windows.go`
has its own `Proc`. `ptyproc_other.go` (`!linux && !darwin && !windows`) has
a method-only stub `Proc` and `Start` returning `ErrUnsupported`. No
interface type is added to `ptyproc`: callers already use `envagent`'s.

**D2. Start.** `CreatePipe` twice (console input, console output);
`CreatePseudoConsole(size, inRead, outWrite, 0, &hpc)`; close our copies of
`inRead` and `outWrite` at once (the console duplicated them; if ours stay
open the output pipe never reaches EOF); a one-entry
`ProcThreadAttributeList` with `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` whose
value is the HPCON itself; `CreateProcess(app=Spec.Path, cmdline=ComposeCommandLine([Path]+Args), inherit=false,
EXTENDED_STARTUPINFO_PRESENT|CREATE_UNICODE_ENVIRONMENT, envBlock, dir)`
with `STARTF_USESTDHANDLES` and null standard handles, so a child of a
parent whose stdio is redirected (CI, a service, Task Scheduler) writes to
the console and not to the parent's handles. Then `os.FindProcess(pid)`
(while `pi.Process` is still open, so the pid can't be reused), and both
`pi` handles are closed. `os.Process` gives `Wait` (exit code from
`GetExitCodeProcess`) and `Kill` (`TerminateProcess`), handle-safe.
Flags `0`: no `PSEUDOCONSOLE_INHERIT_CURSOR`, so the console never sends a
cursor-position request that someone must answer.

The environment block is `Spec.Env`, deduplicated (last wins, keys compared
case-insensitively, like `os/exec`) and with `SYSTEMROOT` added from the
agent's own environment when missing (as `os/exec` does: many programs,
Winsock among them, fail without it). So "nil or empty `Env` gives an empty
environment" becomes "gives only `SYSTEMROOT`" on Windows; `Spec`'s doc
says so. `Spec.Path` stays absolute; a `.cmd`/`.bat` path works because
`CreateProcess` runs batch files through `cmd.exe` itself (the args are
`attach <validated id>` or none, so cmd's escaping rules don't matter).

**D3. EOF and exit.** On a pseudoconsole the output pipe reaches EOF only
after `ClosePseudoConsole`, not when the process exits. So `Proc`'s wait
goroutine, after `process.Wait()` returns, records the exit code, closes
`done` (so `Wait()` returns at once), and then calls `ClosePseudoConsole`.
The console draws its last frame and closes its end, and `Read` returns the
rest of the output and then `io.EOF` (Go maps `ERROR_BROKEN_PIPE` on a pipe
handle to `io.EOF`; checked in `internal/poll/fd_windows.go`). This is what
`envagent`'s `terminal.read` loop needs to send `exited`.

**D4. Close never hangs.** `Close` (once; later calls return the first
result):

1. marks the `Proc` closed (`Resize` now fails);
2. starts a drain (`io.Copy(io.Discard, output)`), because on Windows
   before 11 24H2 `ClosePseudoConsole` waits for the console to write its
   last frame and blocks on a full pipe nobody reads;
3. closes the console in a goroutine: every program attached to it gets
   `CTRL_CLOSE_EVENT` and is ended, the analogue of the unix hang-up
   (SIGHUP to the session);
4. waits `killGrace` (2 s) for the process, then `process.Kill()`;
5. waits up to `closeWait` (5 s) for the process, then up to `closeWait`
   for `ClosePseudoConsole` to return;
6. closes both pipe files (Go's pipe `Close` calls `CancelIoEx`, which
   ends a blocked `Read`). If the console never closed, it closes them in
   the background and returns `errConsoleStuck`.

Worst case about 12 s, never forever. `Resize` holds a mutex around
`ResizePseudoConsole` and is a no-op once the console is closed (after
exit, before `Close`), so it never touches a freed HPCON; after `Close` it
returns `os.ErrClosed`. `Write` after the process exited may fail (the
console is gone); `envagent` already treats a write error as `exited`.

**D5. No job object.** A job with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`
would also kill processes a terminal started on purpose to outlive it:
`claude --bg` starting Claude Code's background service, or `ws` starting
its daemon (`procgroup.Detached()`), from a remote `ws shell`. Node
(libuv) doesn't pass `CREATE_BREAKAWAY_FROM_JOB` for detached children, so
they can't leave the job. Closing the console already ends every program
attached to it, and detached ones survive, as `setsid` daemons survive a
unix hang-up. The root process is `TerminateProcess`d after the grace
period. ADR 0081 rejected job objects for `procgroup` for a related reason
(assignment races). The Windows test proves that a console-attached child
(`ping.exe` under `cmd.exe`) ends on `Close`.

**D6. `Supported()`** is true when kernel32 exports `CreatePseudoConsole`
(Windows 10 1809 / Server 2019 and later): `windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find() == nil`.
`Start` wraps a `*windows.DLLError` as `ErrUnsupported`. So
`platform_unsupported` stays for systems without pseudo-terminals: any OS
other than linux, darwin and windows (FreeBSD, …), and Windows older than
1809. The refusal messages lose "yet" and cite ADR 0085.

**D7. `claude.Send` on a terminal abstraction.** `send.go` gets

```go
type attachTerm interface {
	io.ReadWriter
	hangUp(ctx context.Context, log *slog.Logger, grace time.Duration)
}
func (c *Client) startAttach(ctx context.Context, id string) (attachTerm, error) // per OS
```

`typeMessage` and `watchPrompt` take `io.ReadWriter`/`io.Reader` instead of
`*os.File`. Unix (`pty_unix.go`): today's `openPTY` + `exec.CommandContext`
+ `Setsid/Setctty` code and `hangUp`, moved verbatim into `startAttach` and
`(*unixAttach).hangUp`. Windows (`pty_windows.go`): `exec.LookPath(c.path)`
(the same lookup `exec.Command` does; the `make lint` grep only matches
`exec.Command…"claude"`), then `ptyproc.Start` 120×40, and `hangUp` =
`Proc.Close()` + `Wait()`. Other systems (`pty_other.go`): `startAttach`
returns `errors.ErrUnsupported`, which `agentSend` already maps to
`platform_unsupported`. `internal/claude` may import `internal/ptyproc`:
`ptyproc` imports only the standard library and `x/sys`, so there is no
cycle.

**D8. Terminal environment and shell.** The Windows allow-list
(`env_agent_terminal_windows.go`, case-insensitive since c4c9ffe) already
has the system variables; it gains `ALLUSERSPROFILE`, `PUBLIC`,
`COMMONPROGRAMFILES(X86)`, `COMMONPROGRAMW6432`, `PROCESSOR_IDENTIFIER`,
`PROCESSOR_LEVEL`, `PROCESSOR_REVISION` and `PSMODULEPATH`. `claude` is
found by `lookPathIn` with `PATHEXT` (c4c9ffe). **New:** a shell terminal
needs a home directory for its working directory, and `homeOf` reads only
`HOME`, which Windows usually lacks, so every `ws shell --env <windows>`
would fail with "HOME is not set". `homeOf` moves to the per-OS files; on
Windows it is `HOME`, else `USERPROFILE` (case-insensitive keys). The shell
itself is not the agent's choice: a shell terminal runs `ws shell`, whose
Windows runner already starts `%COMSPEC%`, else `cmd.exe`
(`internal/shell/shell_windows.go`), the same as a local `ws shell` on
Windows. We keep that rather than prefer PowerShell: `COMSPEC` is always
set and in the allow-list, the behaviour matches local `ws shell`, and a
user can run `pwsh` from it. There is no shell setting in `config.toml`.

**D9. Controller on Windows: out of scope.** `ws attach` and
`ws shell --env` *from* a Windows controller still need a raw console and
resize events (`internal/cli/rawterm_other.go`); not trivial, so not here.
The test flow uses a Linux or macOS controller and a Windows environment.

**D10. ADR: a new ADR 0085, not an amendment section in 0081.** The repo's
rule (`docs/adrs/README.md`, Lifecycle) is that a decision is changed by a
new ADR that says what it supersedes. ConPTY is its own decision with its
own rejected alternatives (job objects, a third-party pty library,
`STARTF_USESTDHANDLES` or not), and 0081 is already merged. 0081's status
line gains "D5 superseded by 0085"; 0076's "Amendments (ADR 0081, Windows)"
bullet gains one sentence. No error code is added or removed, so ADR 0016
and the `errorCode` tables don't change, and the protocol number doesn't
either.

## Rejected

- **Job object with `KILL_ON_JOB_CLOSE`:** kills detached daemons started
  from a terminal (D5).
- **A third-party ConPTY package** (`github.com/UserExistsError/conpty`,
  `charmbracelet/x/conpty`, `creack/pty` v2 betas): a new dependency for
  about 200 lines over `x/sys`, which already has every call; and their
  close paths don't meet D4.
- **Overlapped (named) pipes for the console:** cancellable reads, but
  `CreatePipe`'s synchronous pipes plus Go's `CancelIoEx` on `Close` are
  enough, and `os.NewFile` gives `io.EOF` mapping for free.
- **PowerShell as the shell:** see D8.

## Risks and what only CI or a person can prove

| Risk | Mitigation | Proven by |
| --- | --- | --- |
| Output goes to the parent's redirected stdout | `STARTF_USESTDHANDLES`, null handles (node-pty and wezterm do the same; wezterm uses `INVALID_HANDLE_VALUE`) | CI test "output, exit code 0 and io.EOF" (go test's stdout is a pipe) |
| A short-lived program's output is lost on older ConPTY | read concurrently from the start; console closed only after exit | the same CI test, on windows-latest (Server 2022) |
| `ClosePseudoConsole` blocks | drain + bounded waits (D4) | CI "an interactive cmd nobody reads" |
| HPCON passed as `unsafe.Pointer` | `*(*unsafe.Pointer)(unsafe.Pointer(&hpc))` passes vet; the value is a kernel32 heap address outside Go's heap, so the GC ignores it | CI (any Start) |
| `claude attach` on native Windows | none possible here | a person, with the smoke steps below |
| `ws shell` (Windows lock semantics, ADR 0081) inside a pseudoconsole | none | a person |
| Windows < 1809 | `Supported()` false → `platform_unsupported` | not testable (CI has Server 2022) |

## Getting a `ws.exe` for the branch

- **Every pull request and every push to main** (T5): CI job `windows-exe` builds `ws.exe` for
  windows/amd64 and windows/arm64 with `make windows-exe` (the `dist`
  ldflags: `-s -w -X main.version=$(git describe --tags --always --dirty)`,
  `-trimpath`, `CGO_ENABLED=0`) and uploads each with
  `actions/upload-artifact` as **`ws-windows-amd64`** and
  **`ws-windows-arm64`** (14-day retention). Download: the PR's Checks tab →
  CI → Summary → Artifacts, or
  `gh run download <run-id> -n ws-windows-amd64`.
- **Releases** (unchanged): `release.yml` runs `make dist`, whose
  `PLATFORMS` include windows/amd64 and arm64; each `ws.exe` is zipped to
  `dist/ws-windows-<arch>.zip`, summed into `SHA256SUMS`, and attached by
  `gh release create … dist/*`.
- **By hand, from any OS:**

  ```bash
  CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
    -ldflags "-s -w -X main.version=$(git describe --tags --always --dirty)" \
    -o ws.exe ./cmd/ws
  # or: make windows-exe WINARCH=amd64   -> dist/windows-amd64/ws.exe
  ```

## How to test on Windows (smoke steps for the PR body)

Needs: a Windows 10 1809+ / 11 machine (x64 or arm64) with Git for Windows
and Claude Code installed and signed in (`claude --version` works in
`cmd.exe`), and a Linux or macOS controller running this branch's `ws`
with its daemon (`ws daemon run`, or started on demand).

1. **Install the branch build** on Windows: download artifact
   `ws-windows-amd64` (or `-arm64`), put `ws.exe` in
   `%LocalAppData%\Programs\ws\`, and check
   `"%LocalAppData%\Programs\ws\ws.exe" --version` prints the branch's
   `git describe`.
2. **Enroll:** on the controller `ws env add win`; on Windows
   `ws env agent enroll <code>` then `ws env agent run` (leave it running in
   its own console). Controller: `ws env list --json` shows `win` with
   `"status": "online"`, `hello.os` `"windows"` and `hello.ws_version` the
   branch's version.
3. **Shell terminal:** controller `ws shell --env win <repo>`.
   **Pass:** a `cmd.exe` prompt in the new workspace's directory, within a
   few seconds; `cd` prints a path under the agent's `workspaces\`;
   `echo %USERPROFILE% %TERM%` prints the profile path and
   `xterm-256color`; `set` shows **no** `CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`
   or other secrets; resizing the controller's terminal window and running
   `mode con` shows the new column count; `exit 3` ends `ws shell` with exit
   code 3 (`echo $?` on the controller).
4. **Close while running:** `ws shell --env win <repo>`, run
   `ping -n 100 127.0.0.1`, close the controller's terminal window (or kill
   its `ws`). **Pass:** on Windows, within ~10 s, Task Manager shows no
   `PING.EXE` or `cmd.exe` left from it; `ws env agent run` keeps running
   and logs no error other than the terminal closing.
5. **Attach terminal:** `ws claude --env win <repo> -- "Say hello and wait"`,
   then `ws attach --env win <workspace id>`. **Pass:** Claude Code's TUI
   draws with colours and the `❯` prompt; typing a message gets a reply;
   resizing redraws; detaching (Claude Code's detach key, or closing the
   window) leaves the session running: `ws list --env win` still shows the
   agent, and a second `ws attach` reconnects.
6. **Limits:** with 8 terminals open a 9th gets `terminal_limit` (exit 4),
   as on Linux.
7. **`agent.send`:** no `ws` command calls it today (checked:
   `grep -rn OpAgentSend internal/cli` finds only tests), so it is proven
   only by CI's `TestClient_Send_windows` with a fake `claude`; nothing to
   do by hand.

Fail looks like: `platform_unsupported` (the build is old or `Supported()`
is false), a blank screen that never draws (output going to the agent's own
console: `STARTF_USESTDHANDLES`), a terminal that never closes after `exit`
(EOF not reached: D3), or `ws env agent run` hanging after a close (D4).
Report the agent's log and the controller's `-v` output.

## Out of scope

A Windows controller (D9); a named-pipe daemon transport and DPAPI (ADR
0081 follow-ups); running these tests on Windows arm64 (no hosted runner in
the workflow; arm64 is cross-compiled and vetted only).
