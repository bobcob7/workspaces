# Constraints for `env-windows`: the environment agent on Windows

What the code does on `GOOS=windows` today, what a task breaks if it
forgets something, and what this pod can and can't check. Measured
2026-11-24 on `feat/env-windows` at `8b6cce7` with Go 1.27.3
linux/amd64. Every task in [tasks.md](tasks.md) names the rows here that it
touches. The decisions are in
[ADR 0081](../../adrs/0081-windows-environment-agent.md).

## Environment limits (read first)

This pod is linux/amd64 (`Linux 6.18 talos`). There is no Windows host, no
`wine` (`which wine` prints nothing), and no Windows VM. So:

| Check | Here? | How |
| --- | --- | --- |
| Compile for Windows | yes, cross | `GOOS=windows GOARCH=amd64 go build ./...`, same with `GOARCH=arm64` |
| Vet for Windows | yes, cross | `GOOS=windows go vet ./...` (vet also type-checks `_test.go` files) |
| Compile test binaries for Windows | yes, cross | `GOOS=windows go test -c -o /dev/null ./internal/<pkg>` (one package per call; `-o` with several packages needs a directory) |
| Lint the Windows files | yes, cross | `GOOS=windows go tool golangci-lint run ./...` |
| Run any test on Windows | **no** | only the `windows-latest` CI job runs them; a task's Windows tests are first run on its PR |
| Unix behaviour unchanged | yes | `go test -p 2 ./internal/<pkg>` on linux |
| macOS still compiles | yes, cross | `GOOS=darwin go build ./...` |

**Cannot be verified at all, here or in CI** (`windows-latest` is a
Server image, one user, no Claude Code sign-in):

- whether `claude --bg`, `claude agents`, `claude logs`, `claude attach`,
  `claude stop` and `claude daemon run` exist and behave on native Windows
  (ADR 0076's spike question 1 is still open);
- ConPTY behaviour with `claude attach` (terminals are refused, see D5);
- a real Windows Service or Task Scheduler run of `ws env agent run`;
- that another local user can't open the agent's key or the daemon socket
  (CI has one user; the ACL test only reads the DACL back);
- that Windows' AF_UNIX checks the socket file's DACL on `connect`;
- DPAPI (not used, D4);
- git clones with paths over 260 characters on a machine without the
  `LongPathsEnabled` policy, and case-insensitive name clashes in real repos;
- `os.Rename` over a file another process holds open (sharing violations),
  antivirus holding new files open.

## What fails today (Step 1 evidence)

`GOOS=windows GOARCH=amd64 go build -gcflags=-e ./...` fails in 5 packages
first; after stubbing those (in a scratch copy, not committed), 2 more
appear, then the build is green. `GOARCH=arm64` gives the same set (no
arch-specific files). `make build`'s `cmd/ws` depends on all of them.

| Round | Package | File:line | Error |
| --- | --- | --- | --- |
| 1 | `internal/backup` | `backup.go:33` | `unknown field Setpgid in struct literal of type syscall.SysProcAttr` |
| 1 | `internal/gate` | `gate.go:64` | `unknown field Setpgid` |
| 1 | `internal/gate` | `gate.go:65` | `undefined: syscall.Kill` |
| 1 | `internal/lockfile` | `lockfile.go:23,29,55,59,77,82,107,111` | `undefined: unix.LOCK_EX`, `unix.LOCK_SH`, `unix.LOCK_NB`, `unix.Flock`, `unix.EWOULDBLOCK` (13 errors) |
| 1 | `internal/claude` | `client.go:79`, `start.go:73` | `unknown field Setpgid` |
| 1 | `internal/claude` | `daemon.go:71` | `unknown field Setsid` |
| 1 | `internal/claude` | `send.go:71` | `unknown field Setsid`, `unknown field Setctty` |
| 1 | `internal/mcp` | `client.go:252` | `unknown field Setpgid` |
| 1 | `internal/mcp` | `client.go:253` | `undefined: syscall.Kill` |
| 2 | `internal/daemon` | `autostart.go:86`, `process.go:25` | `unknown field Setsid` |
| 2 | `internal/daemon` | `process.go:36` | `undefined: syscall.Kill` |
| 2 | `internal/daemon` | `server.go:508` | `undefined: syscall.Stat_t` |
| 2 | `internal/daemon` | `server.go:529` | `undefined: peerUID` (only `peercred_{linux,darwin}.go` define it) |
| 3 | `internal/cli` | `remote_terminal.go:118,221,245` | `undefined: syscall.SIGWINCH` |

With those stubbed, `GOOS=windows go vet ./...` (amd64 and arm64) and
`GOOS=windows go test -c` over every package fail only in test files:

| Package | File:line | Error |
| --- | --- | --- |
| `internal/backup` | `backup_test.go:51` | `got.SysProcAttr.Setpgid undefined` |
| `internal/claude` | `daemon_test.go:68,101`, `session_test.go:48`, `start_test.go:116` | `undefined: syscall.Getpgrp` |
| `internal/cli` | `claude_test.go:125` | `undefined: syscall.Kill` |
| `internal/cli` | `remote_terminal_test.go:234,413,428,433,448,459,477` | `undefined: syscall.SIGWINCH` |
| `internal/cli` | `runner_control_test.go:78` | `unknown field Setsid` |
| `internal/mcp` | `client_test.go:160` | `undefined: syscall.Kill` |

Already split per OS and fine on Windows: `internal/proc`
(`proc_other.go`: `errors.ErrUnsupported`), `internal/power`
(`power_other.go`), `internal/ptyproc` (`ptyproc_other.go`:
`ErrUnsupported`), `internal/claude/pty_other.go`, `internal/cli/rawterm_other.go`,
`internal/mqttclient/dialer_other.go` (no `TCP_NOTSENT_LOWAT`).

**The `unsupported` exit of ADR 0076 doesn't exist.** `grep -rn
unsupported internal/cli` finds nothing for `ws env agent run`
(`internal/cli/env_agent.go:136`); it was never built, because Windows
never compiled. Nothing to remove; D7 says what refuses instead.

## Behaviour that compiles but is wrong on Windows

Found by reading, not by running. Each row names the task that fixes it.

| Where | What it does | On Windows | Task |
| --- | --- | --- | --- |
| `internal/shell/shell.go:79` | `cmd.ExtraFiles = []*os.File{sess.Lock}` | `os/exec` returns an error from `Start` when `ExtraFiles` is set on Windows: `ws shell` would never start | T2 |
| `internal/shell/shell.go:19,101` | `$SHELL -i`, else `/bin/sh -i` | no `/bin/sh`; Git Bash's `SHELL=/usr/bin/bash` is an MSYS path `CreateProcess` can't run | T2 |
| `internal/gate/gate.go:61` | `/bin/sh -c <script>` for loop gates | no `/bin/sh`: every loop tick's gate fails | T2 |
| `internal/daemon/paths.go:34` | socket in `${TMPDIR:-/tmp}/ws-<uid>` | `TMPDIR` is unset, `/tmp` is `\tmp` on the current drive, and `os.Getuid()` is `-1` | T3 |
| `internal/daemon/server.go:495-512` | socket dir must be 0700 and owned by `os.Getuid()` | `Stat_t` doesn't exist; mode bits mean nothing; uid is `-1` | T3 |
| `internal/claude/{client.go:56,trust.go:42,transcript.go:251,daemon.go:87}`, `internal/mcp/defs.go:58` | Claude Code's dirs from `HOME` | `HOME` is usually unset; Claude Code uses `%USERPROFILE%\.claude` (assumed, unverified) | T3 |
| `internal/claude/daemon.go:89` | detached daemon's dir falls back to `/` | means the current drive's root | T3 |
| `internal/envagent/files.go:31-32,172,374`, `internal/environment/files.go:37`, `internal/environment/authority.go:131-134,519` | key and CA files 0600, dirs 0700 by `Chmod` | `Chmod` only toggles the read-only bit; the key is as private as the inherited ACL of its folder | T4 |
| `internal/artifact/files.go:59-67` | `syncDirectory` opens a dir and `Sync`s it | `FlushFileBuffers` on a directory handle opened read-only fails with "Access is denied" (expected; unverified here). The error isn't `EINVAL`/`ErrUnsupported`, so every artifact write would fail | T3 |
| `internal/config/loader.go:51`, `cmd/ws/main.go:26` | `CONFIG_DIR` = `os.UserConfigDir()/workspaces` | `%AppData%` is **Roaming**: a repo cache and every workspace would roam with the profile | T3 |
| `internal/git/runner.go:88-94` | `git` with no extra config | checkouts deeper than 260 characters fail unless `core.longpaths=true` | T3 |
| `internal/envagent/ops.go:297-308` | `underRoot`: `strings.HasPrefix` on cleaned paths | case-sensitive; `C:\` vs `c:\` refuses a valid path (fails closed: safe, but wrong) | T3 |
| `internal/envagent/terminals.go:229` | `ptyproc.Start` error returned as-is | the controller sees `internal` for `ptyproc.ErrUnsupported` | T5 |
| `internal/claude/send.go:63` (`agent.send`) | `claude attach` in a pty | `openPTY` returns `errors.ErrUnsupported`; the controller sees `claude_failed`/`internal` | T5 |
| `internal/daemon/process.go:35` | `Signal(pgid, SIGTERM)` to stop a runner | no signals between processes; only kill | T1 |
| `internal/cli/env_agent.go:147` | `signal.NotifyContext(ctx, SIGTERM, os.Interrupt)` | compiles; `SIGTERM` never arrives on Windows, `os.Interrupt` does (Ctrl-C, Ctrl-Break). A Task Scheduler "End" is `TerminateProcess`: no clean disconnect, the broker's last will covers it | none (documented) |
| `internal/workspace/create.go:165` + `internal/cli/shell.go:124` | the workspace lock is inherited by the shell (fd 3, ADR 0015) | a `LockFileEx` lock isn't shared through an inherited handle and ends with the process that took it; `ws shell` holds it while it waits for the shell, but jobs the shell leaves behind don't | T1/T2 (documented) |
| `internal/machine/role/set.go:190` | `HasSuffix(file, "/"+name+".md")` | if `file` is an OS path, a broken user role file isn't matched on Windows; check where `broken` comes from | T3 |

## Pinned values and tests a task can break

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `Makefile` `lint`, the ADR 0049 grep | `exec.Command…"/bin/sh"` only in `internal/gate/` | the Windows gate looks for `sh` elsewhere: keep the lookup in `internal/gate` |
| `Makefile` `lint`, ADR 0003/0024/0039 greps | `exec.Command(…"git"/"claude"/"gh")` only in their packages | a Windows helper runs `git` (e.g. to find `sh.exe` next to it): use `exec.LookPath("git")`, which the grep doesn't match, never `exec.Command("git"…)` outside `internal/git` |
| `Makefile` `lint`, `internal/blanklines` | no blank lines inside function bodies, in **every** `.go` file including `_windows.go` | a new file has one |
| `.golangci.yml` | linters run on the host's GOOS only | Windows files are never linted unless CI runs `GOOS=windows go tool golangci-lint run ./...` (T6 adds it) |
| `internal/cli/errors.go` `errorCode` consts, `exitCode()`, `known()` | every code in all three; `exhaustive` linter | `platform_unsupported` (T5) isn't in all three |
| `internal/cli/llm_test.go` `TestGuideTopics` | every declared code is in `internal/cli/guides/ws.md`'s exit table row | `platform_unsupported` isn't in row `\| 1 ` |
| `internal/cli/errors_test.go` `TestErrorCode_exitCode` | samples per exit code | add `platform_unsupported` to the exit-1 subtest |
| `docs/adrs/0016-output-contract.md` `Status:` | lists the ADR that added each code | append "`platform_unsupported` added by [0081](0081-windows-environment-agent.md)" |
| `internal/envproto/ops.go` `Since`, `envproto.Protocol*` | protocol number in the hello | **don't** bump it: no new operation, only a new error code, which older controllers already handle as unknown (`errorCode.known()`) |
| `internal/daemon/paths_test.go` | socket path for given `getenv`/`uid` | the Windows path rule must live in a `_windows.go` file so the unix test is unchanged |
| `internal/config/loader_test.go` | `userConfigDir` injected by tests | keep `NewLoader(getenv, userConfigDir)`'s signature; change only what `cmd/ws/main.go` passes |
| `internal/git` tests that assert exact argv | `git -C dir …` | the Windows `-c core.longpaths=true` prefix must come from a function that returns `nil` on unix |
| `go.mod` | `golang.org/x/sys v0.50.0` is already a direct requirement | `x/sys/windows` is in the same module: **no** `go.mod` change. It has `LockFileEx`, `UnlockFileEx`, `SetNamedSecurityInfo`, `GetNamedSecurityInfo`, `ACLFromEntries`, `GetCurrentProcessToken`, `CreateToolhelp32Snapshot`, `Process32First/Next`, `OpenProcess`, `TerminateProcess`, `CREATE_NEW_PROCESS_GROUP`, `DETACHED_PROCESS`, `CreatePseudoConsole` (checked in the module cache). `go mod tidy` must leave `go.mod`/`go.sum` unchanged |
| `.github/workflows/release.yml` | `gh release create … dist/*` | the zip names must not collide with the existing `ws-<os>-<arch>` files |
| `Makefile` `dist` | `SHA256SUMS` from `ws-*` | the `.zip` files must be inside `ws-*` to be summed |

## Rules for every task

- Read this file and [ADR 0081](../../adrs/0081-windows-environment-agent.md) first.
- Unix behaviour must not change: move unix code into `_unix.go` files
  **verbatim** (`//go:build unix` or the package's existing
  `linux || darwin` tag), and add `_windows.go` beside it.
- Use `golang.org/x/sys/windows`, never `syscall` constants that only exist
  in x/sys, and never shell out to `icacls`, `taskkill` or PowerShell.
- No blank lines inside function bodies (`internal/blanklines`).
- gofumpt; `go test -p 2`; one test file per source file
  (`lockfile_windows.go` → `lockfile_windows_test.go`, build-tagged by its
  name).
- Commit messages: imperative, conventional prefix, **no Co-Authored-By
  trailer** (project rule).
- Under `## Not run here`, list every check that only the Windows CI job
  can run; record PASS when everything runnable passed.
- Never wait with a loop on `pgrep -f`; end with no background jobs running.
