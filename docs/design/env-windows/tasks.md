# Tasks: the environment agent on Windows

Implements [ADR 0081](../../adrs/0081-windows-environment-agent.md). Every
task must first read [constraints.md](constraints.md): it lists what this
pod can't run (no Windows, no wine: Windows tests are compile-checked here
and run only in the `windows-latest` CI job), the pinned values a task can
break, and the rules for every task (verbatim unix moves, x/sys/windows
only, no blank lines in function bodies, no `Co-Authored-By` trailer).

Base: `feat/env-windows`. `golang.org/x/sys v0.50.0` is already in
`go.mod` and has every Windows API used here: **no `go.mod` change**;
`go mod tidy` must leave `go.mod` and `go.sum` untouched.

## Order

```
T1 ─┬─────────────┐
T2 ─┤ (T1 ∥ T2)   ├─ T3 ─┬─ T4 ─┐
    │             │      ├─ T5 ─┼─ T7
    │             │      └─ T6 ─┘
```

- T1 ∥ T2 (disjoint files). T3 needs both: it makes `GOOS=windows go
  build ./...`, `vet` and every `go test -c` green.
- T4 ∥ T5 ∥ T6 after T3 (disjoint files; T6 touches `internal/cli`
  files that T4 and T5 don't).
- T7 last: CI, Makefile, release, Windows-only tests, docs.

Common verify block (each task runs it, plus its own lines):

```bash
test -z "$(go tool golangci-lint fmt --diff ./...)"   # gofumpt via golangci-lint; gofumpt isn't on PATH
for a in amd64 arm64; do GOOS=windows GOARCH=$a go tool golangci-lint fmt --diff ./... ; done  # also formats _windows.go files
go build ./... && GOOS=darwin go build ./...
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
git diff --exit-code origin/feat/env-windows -- go.mod go.sum
```

---

## T1. `internal/procgroup` and its callers

Depends on: none. Parallel with T2. Constraints rows: "What fails today"
rounds 1-2 for `backup`, `gate`, `mcp`, `claude`, `daemon`
(`process.go`, `autostart.go`); test rows for `backup`, `claude`, `mcp`.

### Files

New:

- `internal/procgroup/procgroup.go`: package doc only
  (`// Package procgroup starts processes in groups of their own and kills them (ADR 0081).`).
- `internal/procgroup/procgroup_unix.go` (`//go:build unix`)
- `internal/procgroup/procgroup_windows.go`
- `internal/procgroup/procgroup_unix_test.go` (`//go:build unix`)

Changed (replace only the lines named; nothing else moves):

| File | Line today | Becomes |
| --- | --- | --- |
| `internal/backup/backup.go:33` | `&syscall.SysProcAttr{Setpgid: true}` | `procgroup.OwnGroup()` |
| `internal/gate/gate.go:64-65` | `Setpgid` attrs, `syscall.Kill(-pid, SIGKILL)` | `cmd.SysProcAttr = procgroup.OwnGroup()`; `cmd.Cancel = func() error { return procgroup.KillTree(cmd.Process.Pid) }` (leave `/bin/sh` alone: T4) |
| `internal/mcp/client.go:252-253` | same pair | same replacement |
| `internal/claude/client.go:79`, `start.go:73` | `&syscall.SysProcAttr{Setpgid: true}` | `procgroup.OwnGroup()` |
| `internal/claude/daemon.go:71` | `{Setsid: true}` | `procgroup.Detached()` |
| `internal/claude/send.go:71` | `{Setsid: true, Setctty: true}` | `attachProcAttr()`, a new func in `pty_unix.go` returning `&syscall.SysProcAttr{Setsid: true, Setctty: true}` and in `pty_other.go` returning `nil` (on Windows `openPTY` fails before it's used) |
| `internal/daemon/autostart.go:86`, `process.go:25` | `{Setsid: true}` | `procgroup.Detached()` |
| `internal/daemon/process.go:35-40` | `syscall.Kill(-pgid, sig)` | `procgroup.Signal(pgid, sig)`, keep the error wrap text |
| `internal/backup/backup_test.go:51` | asserts `got.SysProcAttr.Setpgid` | `assert.Equal(t, procgroup.OwnGroup(), got.SysProcAttr)` |
| `internal/claude/{daemon,session,start}_test.go` | `syscall.Getpgrp()` in "runs in a process group of its own" subtests | add `//go:build unix` as the first line of each of these three files (all their tests run `#!/bin/sh` fakes, which can't run on Windows) |
| `internal/mcp/client_test.go:160` | `syscall.Kill` | add `//go:build unix` to the file if every test in it uses a fake stdio server; otherwise move `TestServer_Close` to `client_unix_test.go` |

Remove `"syscall"` imports that become unused. If tagging a test file `unix` leaves a helper (`fakeClaude`, `argsFile`, …) undefined for another untagged test file, `GOOS=windows go vet ./internal/<pkg>` says so: move that helper into the untagged file that uses it rather than tagging more files.

### Signatures

```go
// procgroup_unix.go
func OwnGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }
func Detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
func KillTree(pid int) error         { return syscall.Kill(-pid, syscall.SIGKILL) }
func Signal(pgid int, sig syscall.Signal) error { return syscall.Kill(-pgid, sig) }

// procgroup_windows.go
func OwnGroup() *syscall.SysProcAttr // CreationFlags: windows.CREATE_NEW_PROCESS_GROUP
func Detached() *syscall.SysProcAttr // CREATE_NEW_PROCESS_GROUP | DETACHED_PROCESS
func KillTree(pid int) error
func Signal(pgid int, _ syscall.Signal) error // KillTree(pgid)
func descendants(root uint32, entries []windows.ProcessEntry32, created func(pid uint32) (windows.Filetime, bool)) []uint32
```

Windows `KillTree`: take `windows.CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0)`,
read all entries (`Process32First`/`Process32Next` until
`ERROR_NO_MORE_FILES`), compute `descendants(pid, …)` (breadth-first over
`ParentProcessID`; a child is kept only if its creation time,
`GetProcessTimes` via `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION)`, is
not before its parent's), then for the root and each descendant
`OpenProcess(PROCESS_TERMINATE)` + `TerminateProcess(h, 1)` + `CloseHandle`.
An already-exited process (`ERROR_INVALID_PARAMETER` from `OpenProcess`) is
not an error; return the first other error wrapped with the pid.
`descendants` is pure so T7 can test it with a fake table.

### Acceptance

- Unix behaviour byte-identical: same attrs, same signals.
- `GOOS=windows go build` passes for `./internal/procgroup ./internal/backup
  ./internal/gate ./internal/mcp ./internal/claude` (daemon still fails on
  `server.go` until T3: expected).
- Unix test `TestOwnGroup`/`TestKillTree` in `procgroup_unix_test.go`: start
  `sh -c 'sleep 30 & sleep 30'` with `OwnGroup()`, `KillTree`, and the
  process exits by signal within 5 s.

### Verify

```bash
go test -p 2 ./internal/procgroup ./internal/backup ./internal/gate ./internal/mcp ./internal/claude ./internal/daemon
for p in procgroup backup gate mcp claude; do GOOS=windows go build ./internal/$p || exit 1; done
for p in procgroup backup gate mcp claude; do GOOS=windows go vet ./internal/$p || exit 1; done
for a in amd64 arm64; do GOOS=windows GOARCH=$a go test -c -o /dev/null ./internal/procgroup || exit 1; done
make lint
```

---

## T2. `internal/lockfile` on Windows (`small`)

Depends on: none. Parallel with T1. Constraints rows: round 1
`internal/lockfile`; D2.

### Files

- `internal/lockfile/lockfile.go`: keep the API, doc comments and
  `acquire`/`open` structure; replace the `unix.*` calls with the helpers
  below; drop the `x/sys/unix` import.
- `internal/lockfile/lockfile_unix.go` (`//go:build unix`): the `flock`
  calls, moved verbatim.
- `internal/lockfile/lockfile_windows.go`: `LockFileEx`.

### Signatures

```go
type mode int
const (
	shared mode = iota
	exclusive
)
// tryLock takes f's lock without waiting. held is true, with a nil error,
// when another holder has it.
func tryLock(f *os.File, m mode) (held bool, err error)
```

Unix: `unix.Flock(int(f.Fd()), LOCK_SH or LOCK_EX | LOCK_NB)`;
`EWOULDBLOCK` → `held=true`. Windows: `windows.LockFileEx(windows.Handle(f.Fd()),
flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})` with
`flags = LOCKFILE_FAIL_IMMEDIATELY` (+ `LOCKFILE_EXCLUSIVE_LOCK` for
exclusive); `ERROR_LOCK_VIOLATION` (and `ERROR_IO_PENDING`, defensively)
→ `held=true`. Unlock stays "close the file" (Windows releases locks when
the handle closes). `HeldExclusively` keeps probing with a shared lock and
closes the probe handle.

`Lock.File()`'s doc gains: "On Windows a child given the file doesn't hold
the lock (ADR 0081)."

### Acceptance

- `go test -p 2 ./internal/lockfile` unchanged and green.
- `GOOS=windows go build ./internal/lockfile` and `go vet` pass; test binary
  compiles for amd64 and arm64.

### Verify

```bash
go test -p 2 -count=1 ./internal/lockfile ./internal/workspace ./internal/cache
GOOS=windows go vet ./internal/lockfile
for a in amd64 arm64; do GOOS=windows GOARCH=$a go test -c -o /dev/null ./internal/lockfile || exit 1; done
GOOS=windows go tool golangci-lint run ./internal/lockfile/...
```

---

## T3. `internal/privfile`, the daemon socket, `SIGWINCH`: Windows builds green

Depends on: T1, T2 (merged). Constraints rows: round 2 `daemon/server.go`,
round 3 `cli/remote_terminal.go`, test rows for `cli`; "Behaviour" rows
for `daemon/paths.go` and `server.go`; D3, D4.

### Files

New:

- `internal/privfile/privfile.go` (doc + `ErrNotPrivate`),
  `privfile_unix.go` (`//go:build unix`), `privfile_windows.go`,
  `privfile_unix_test.go`.
- `internal/daemon/peercred_windows.go`, `internal/daemon/socket_unix.go`
  (`//go:build unix`), `internal/daemon/socket_windows.go`.
- `internal/cli/remote_terminal_unix.go` (`//go:build unix`),
  `internal/cli/remote_terminal_windows.go`.

Changed:

- `internal/daemon/server.go`: `prepareSocketDir` and `ownerUID` move to
  `socket_unix.go` verbatim; `server.go` no longer imports `syscall`.
- `internal/daemon/paths.go`: `socketPath` calls a new
  `socketBaseDir(getenv, uid) string` for the non-`XDG_RUNTIME_DIR` case;
  unix version (in `socket_unix.go`) is today's `${TMPDIR:-/tmp}/ws-<uid>`;
  Windows is `filepath.Join(os.TempDir(), "ws")`.
- `internal/cli/remote_terminal.go:118,221,245`: replace
  `syscall.SIGWINCH` with `terminalSignals()` and `isResize(sig)`.
- Test files that only compile on unix, each gets `//go:build unix` as its
  first line: `internal/cli/remote_terminal_test.go`,
  `internal/cli/claude_test.go`, `internal/cli/runner_control_test.go`. If
  that leaves a helper undefined for another untagged test file
  (`GOOS=windows go vet ./internal/cli` tells you), move that helper into
  the untagged file that uses it instead of tagging more.

### Signatures

```go
// privfile
var ErrNotPrivate = errors.New("not private to the current user")
func RestrictDir(dir string) error   // unix: nil. windows: protected DACL, current user only, OI|CI inheritance
func RestrictFile(path string) error // unix: nil. windows: protected DACL, current user only, no inheritance flags
func CheckPrivate(path string) error // unix: nil. windows: owner == current user and every ACE's SID == current user, else ErrNotPrivate (wrapped with the path)

// privfile_windows.go helpers
func currentUser() (*windows.SID, error) // GetCurrentProcessToken().GetTokenUser()
func privateACL(user *windows.SID, inherit bool) (*windows.ACL, error) // ACLFromEntries with one GRANT_ACCESS GENERIC_ALL TRUSTEE_IS_SID entry
// set with SetNamedSecurityInfo(path, SE_FILE_OBJECT,
//   DACL_SECURITY_INFORMATION|PROTECTED_DACL_SECURITY_INFORMATION|OWNER_SECURITY_INFORMATION, user, nil, acl, nil)

// daemon/socket_windows.go
func prepareSocketDir(dir string) error // MkdirAll(0o700), privfile.RestrictDir, privfile.CheckPrivate; error text: "socket directory %s must be private to you: %w"
func socketBaseDir(getenv func(string) string, uid int) string
// daemon/peercred_windows.go
func peerUID(net.Conn) (int, error) { return os.Getuid(), nil } // the directory's DACL is the check (ADR 0081 D4)

// cli/remote_terminal_{unix,windows}.go
func terminalSignals() []os.Signal // unix: SIGWINCH, SIGTERM, SIGHUP, SIGINT (today's order). windows: os.Interrupt, syscall.SIGTERM
func isResize(sig os.Signal) bool  // unix: sig == syscall.SIGWINCH. windows: false
```

Never call `signal.Notify` with an empty list (it means every signal):
`terminalSignals()` is never empty.

### Acceptance

- **`GOOS=windows GOARCH=amd64 go build ./...` and `GOARCH=arm64` pass.**
- **`GOOS=windows go vet ./...` passes** (amd64 and arm64).
- Every package's test binary compiles for Windows (loop below).
- Unix: `go test -p 2 ./internal/daemon ./internal/cli ./internal/privfile`
  green; `internal/daemon/paths_test.go` unchanged.

### Verify

```bash
for a in amd64 arm64; do GOOS=windows GOARCH=$a go build ./... && GOOS=windows GOARCH=$a go vet ./... || exit 1; done
mkdir -p /tmp/$USER-wt && for a in amd64 arm64; do GOOS=windows GOARCH=$a go test -c -o /tmp/$USER-wt/ ./... >/dev/null || exit 1; done
go test -p 2 ./internal/daemon ./internal/privfile ./internal/cli
GOOS=windows go tool golangci-lint run ./internal/privfile/... ./internal/daemon/... ./internal/cli/...
make lint
```

(`go test -c -o <dir>/` with several packages writes `<pkg>.test.exe`
files there; checked with Go 1.27.3.)

---

## T4. Shells: loop gates with Git for Windows' `sh`, `ws shell` with `%COMSPEC%` (`small`)

Depends on: T3. Parallel with T5, T6. Constraints rows: "Behaviour"
`shell.go:79`, `shell.go:19,101`, `gate.go:61`; the Makefile ADR 0049 grep;
D6.

### Files

- `internal/gate/gate.go`: `Run` builds its command with
  `scriptCommand(tctx, req.Script)`; add `ErrNoShell`.
- `internal/gate/gate_unix.go` (`//go:build unix`):
  `exec.CommandContext(ctx, "/bin/sh", "-c", script)`, moved verbatim with
  its `//nolint:gosec` comment (the Makefile grep allows `/bin/sh` only in
  `internal/gate/`).
- `internal/gate/gate_windows.go`.
- `internal/shell/shell.go`: `program()` and the `ExtraFiles` line move
  behind two helpers.
- `internal/shell/shell_unix.go` (`//go:build unix`),
  `internal/shell/shell_windows.go`.

### Signatures

```go
// gate.go
var ErrNoShell = errors.New("loop scripts need a POSIX sh: put sh on PATH or install Git for Windows (ADR 0081)")
// gate_unix.go
func scriptCommand(ctx context.Context, script string) (*exec.Cmd, error)
// gate_windows.go
func scriptCommand(ctx context.Context, script string) (*exec.Cmd, error) // findSh(exec.LookPath, os.Stat)
func findSh(lookPath func(string) (string, error), stat func(string) (os.FileInfo, error)) (string, error)
// order: lookPath("sh"); else g := lookPath("git"), then
// filepath.Join(filepath.Dir(g), "..", "bin", "sh.exe"), then "..", "usr", "bin", "sh.exe";
// first that stat finds; else ErrNoShell.

// shell_unix.go / shell_windows.go
func (r *Runner) program() (path string, args []string) // unix: $SHELL or /bin/sh, ["-i"]. windows: %COMSPEC% or "cmd.exe", nil
func inheritLock(cmd *exec.Cmd, lock *os.File)          // unix: cmd.ExtraFiles = []*os.File{lock}. windows: no-op
```

Don't use `exec.Command(…"git"…)`: only `exec.LookPath("git")` (Makefile
grep, ADR 0003). On Windows `r.notify` keeps `SIGINT, SIGQUIT, SIGHUP,
SIGTERM` (they exist in `syscall` on Windows and compile; only Ctrl-C
arrives). `shell.go`'s doc for `Run` gains one line naming the Windows
program.

### Acceptance

- Unix behaviour unchanged; `go test -p 2 ./internal/gate ./internal/shell`
  green.
- Windows build, vet and test-compile of both packages pass.
- `findSh` is pure (injected `lookPath`, `stat`) so T7 can test it.

### Verify

```bash
go test -p 2 ./internal/gate ./internal/shell ./internal/loop
for p in gate shell; do GOOS=windows go vet ./internal/$p && GOOS=windows go test -c -o /dev/null ./internal/$p || exit 1; done
GOOS=windows go tool golangci-lint run ./internal/gate/... ./internal/shell/...
make lint
```

---

## T5. Paths: config dir, long paths, home, case-insensitive roots, directory sync

Depends on: T3. Parallel with T4, T6. Constraints rows: "Behaviour"
`config/loader.go:51`, `git/runner.go`, the `HOME` row, `daemon.go:89`,
`envagent/ops.go:297-308`, `artifact/files.go:59-67`,
`machine/role/set.go:190`; pinned rows for `config/loader_test.go` and
`git` argv tests; D7.

### Files and changes

| File | Change |
| --- | --- |
| `internal/config/basedir_unix.go` (`//go:build unix`) | `func UserBaseDir() (string, error) { return os.UserConfigDir() }` |
| `internal/config/basedir_windows.go` | `func UserBaseDir() (string, error) { return os.UserCacheDir() }` (`%LocalAppData%`) |
| `cmd/ws/main.go:26` | `config.NewLoader(os.Getenv, config.UserBaseDir)`; `loader.go:51`'s comment says "os.UserConfigDir() (%LocalAppData% on Windows)" |
| `internal/git/runner.go:88-92` | `full := append(platformArgs(), …)` before `-C dir` |
| `internal/git/args_unix.go` / `args_windows.go` | `func platformArgs() []string`: unix `nil`; windows `[]string{"-c", "core.longpaths=true"}` |
| `internal/claude/home_unix.go` / `home_windows.go` | `func userHome(environ []string) string`: unix `lookup(environ, "HOME")`; windows `HOME`, else `USERPROFILE` |
| `internal/claude/client.go:56`, `trust.go:42`, `transcript.go:251`, `daemon.go:87` | `lookup(environ, "HOME")` → `userHome(environ)` |
| `internal/claude/daemon.go:89` | fallback `"/"` → `rootDir()`: unix `"/"`, windows `os.Getenv("SystemDrive") + "\\"` (in `home_*.go`) |
| `internal/mcp/defs.go:58` and new `internal/mcp/home_unix.go` / `home_windows.go` | same `userHome` rule (duplicate the 5 lines; packages don't share it today) |
| `internal/envagent/ops.go:307` | `underRoot` calls `hasPathPrefix(clean, filepath.Clean(root)+sep)` |
| `internal/envagent/path_unix.go` / `path_windows.go` | `func hasPathPrefix(s, prefix string) bool`: unix `strings.HasPrefix`; windows `len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)` |
| `internal/artifact/files.go:59-67` | `syncDirectory` → unix file keeps it verbatim (`files_unix.go`); `files_windows.go`: `func syncDirectory(string) error { return nil }` with a one-line comment why |
| `internal/machine/role/set.go:190` | check whether `broken` holds OS paths (read where it's filled). If yes: `filepath.Base(file) == name+".md"`; if they are always `/`-joined labels, leave it and say so in the report |

### Acceptance

- Unix: no test changes needed; `go test -p 2` of each touched package green.
- Windows build, vet, test-compile of every touched package.
- `git` argv tests on unix unchanged (they see `nil` prefix).

### Verify

```bash
go test -p 2 ./internal/config ./internal/git ./internal/claude ./internal/mcp ./internal/envagent ./internal/artifact ./internal/machine/... ./cmd/ws
for p in ./internal/config ./internal/git ./internal/claude ./internal/mcp ./internal/envagent ./internal/artifact ./cmd/ws; do GOOS=windows go vet $p && GOOS=windows go test -c -o /dev/null $p || exit 1; done
GOOS=windows go tool golangci-lint run ./internal/config/... ./internal/git/... ./internal/claude/... ./internal/mcp/... ./internal/envagent/... ./internal/artifact/...
make lint
```

---

## T6. Key ACLs and `platform_unsupported`

Depends on: T3. Parallel with T4, T5 (T5 touches `envagent/ops.go` and new
`envagent/path_*.go`; T6 touches `envagent/files.go` and
`envagent/terminals.go`: disjoint). Constraints rows: "Behaviour" key-file
row, `terminals.go:229`, `send.go:63`; pinned rows for error codes, guides,
ADR 0016, protocol; D3, D5.

### Files and changes

| File | Change |
| --- | --- |
| `internal/envagent/files.go` | in `Save`, after `os.Chmod(f.dir, dirMode)`: `privfile.RestrictDir(f.dir)`; in `writeFileAtomic`, after the `os.Rename`: `privfile.RestrictFile(path)` (the temp file already inherits the directory's DACL; this is belt and braces). Errors wrapped `"protecting %s: %w"` |
| `internal/environment/files.go` | same in its `writeFileAtomic` |
| `internal/environment/authority.go:131-136` | `privfile.RestrictDir(a.dir)` after the `Chmod` |
| `internal/envagent/terminals.go:229-233` | if `errors.Is(err, ptyproc.ErrUnsupported)` return `&envproto.ReplyError{Code: "platform_unsupported", Message: "terminals aren't supported on " + runtime.GOOS + " agents yet (ADR 0081)"}` |
| `internal/cli/env_agent_ops.go` (the `agent.send` handler) | an error that `errors.Is(err, errors.ErrUnsupported)` becomes `newError(codePlatformUnsupported, …)` with "sending to an agent isn't supported on <goos> environments yet (ADR 0081)" |
| `internal/cli/errors.go` | `codePlatformUnsupported errorCode = "platform_unsupported" // exit 1`, added to the `exitFailed` case and to `known()` |
| `internal/cli/errors_test.go` | add it to the exit-1 subtest |
| `internal/cli/guides/ws.md` | add `` `platform_unsupported` `` to the row `\| 1 ` |
| `internal/cli/guides/ws-attach.md`, `ws-shell.md` | one exit-table row / sentence: "The environment runs Windows: terminals aren't supported yet \| `platform_unsupported` \| 1" |
| `docs/adrs/0016-output-contract.md` | `Status:` gains "; `platform_unsupported` added by [0081](0081-windows-environment-agent.md)" |
| `docs/tools/ws-env.md` `## Exit codes` | add the code |

Do **not** change the protocol number or any `OpSpec.Since`.

### Tests (unix, run here)

- `internal/envagent/terminals_test.go`: a fake `ptyStarter` returning
  `ptyproc.ErrUnsupported` → reply code `platform_unsupported`.
- `internal/cli/env_agent_ops_test.go` (or the existing test of the send
  handler): a send that fails with `errors.ErrUnsupported` → code
  `platform_unsupported`.
- `TestGuideTopics` and `TestErrorCode_exitCode` pass.

### Verify

```bash
go test -p 2 ./internal/envagent ./internal/environment ./internal/cli
for p in ./internal/envagent ./internal/environment ./internal/cli; do GOOS=windows go vet $p && GOOS=windows go test -c -o /dev/null $p || exit 1; done
GOOS=windows go tool golangci-lint run ./internal/envagent/... ./internal/environment/... ./internal/cli/...
make lint
```

---

## T7. Windows tests, CI, release, docs

Depends on: T4, T5, T6. Not parallel. Constraints: "Environment limits"
(these tests are compiled here and run only in CI), pinned rows for
`Makefile dist`, `release.yml`, `.golangci.yml`; D9.

Split point: part A (tests) can be recorded PASS alone; part B (CI and
docs) then runs on the same branch.

### Part A: Windows-only unit tests

Each file is named after its source file and so build-tagged by name.
`require` for preconditions, `assert` for checks, `t.Context()`,
`t.Parallel()` only where it touches the file system or processes.

| File | Tests |
| --- | --- |
| `internal/lockfile/lockfile_windows_test.go` | `TestTryLock`: exclusive then a second handle's exclusive is `held`; shared + shared both succeed; exclusive while shared is `held`; closing the first handle frees it. `HeldExclusively` true/false |
| `internal/privfile/privfile_windows_test.go` | `TestRestrictDir`: after it, `GetNamedSecurityInfo` shows a protected DACL with one ACE for the current user; a file created inside afterwards passes `CheckPrivate`. `TestCheckPrivate`: a fresh `t.TempDir()` subdir (inherits Users/Administrators) fails with `ErrNotPrivate` |
| `internal/procgroup/procgroup_windows_test.go` | `TestDescendants` (pure): a fake table with a chain, a sibling, and a reused pid (creation time before its parent's) → only the real chain. `TestKillTree`: start `cmd.exe /C ping -n 30 127.0.0.1` with `OwnGroup()`, `KillTree`, `Wait` returns within 5 s |
| `internal/daemon/socket_windows_test.go` | `TestSocketBaseDir` is under `os.TempDir()`; `TestPrepareSocketDir` creates a private dir and passes `CheckPrivate` |
| `internal/envagent/path_windows_test.go` | `TestHasPathPrefix`: `C:\ws\a` under `c:\WS\` true; `C:\wsx` under `C:\ws\` false; `D:\ws\a` under `C:\ws\` false |
| `internal/gate/gate_windows_test.go` | `TestFindSh`: `sh` on PATH wins; else `<git>\..\bin\sh.exe`; else `usr\bin`; else `ErrNoShell` (all with fakes) |
| `internal/config/basedir_windows_test.go` | `TestUserBaseDir` equals `os.Getenv("LOCALAPPDATA")` |

Verify (here; these tests can't run here):

```bash
for a in amd64 arm64; do for p in lockfile privfile procgroup daemon envagent gate config; do GOOS=windows GOARCH=$a go test -c -o /dev/null ./internal/$p || exit 1; done; done
GOOS=windows go vet ./internal/...
GOOS=windows go tool golangci-lint run ./internal/...
```

### Part B: CI, Makefile, release, docs

| File | Change |
| --- | --- |
| `.github/workflows/ci.yml` | job `windows-cross` (ubuntu-latest, matrix `arch: [amd64, arm64]`): `GOOS=windows GOARCH=$arch go build ./...`, `go vet ./...`, `go test -c -o "$RUNNER_TEMP/wt/" ./...`; and on amd64 only `GOOS=windows go tool golangci-lint run ./...` |
| `.github/workflows/ci.yml` | job `windows` (`runs-on: windows-latest`, `defaults.run.shell: bash`): `go build ./...`, `go vet ./...`, `go test -p 2 $WINDOWS_TESTS` (required), then `go test -p 2 ./... ` with `continue-on-error: true` (informational; its failures decide what joins the list) |
| `Makefile` | `WINDOWS_TESTS := ./internal/lockfile ./internal/privfile ./internal/procgroup ./internal/envproto ./internal/redact ./internal/power ./internal/llm ./internal/telemetry` plus `-run` filtered runs as a second line: `go test -p 2 -run 'TestSocketBaseDir\|TestPrepareSocketDir' ./internal/daemon`, `-run TestHasPathPrefix ./internal/envagent`, `-run TestFindSh ./internal/gate`, `-run TestUserBaseDir ./internal/config`; a `test-windows` target running both. CI calls `make test-windows` (make exists on windows-latest's Git Bash) |
| `Makefile` | `PLATFORMS += windows/amd64 windows/arm64`; in `dist`, for `os=windows` build to `dist/ws.exe`, then `cd dist && zip -q ws-windows-$$arch.zip ws.exe && rm ws.exe`; the static-link check stays linux-only; `SHA256SUMS` still from `ws-*` |
| `.github/workflows/release.yml` | nothing if `dist/*` already uploads the zips (check); add a comment naming the two zips |
| `.woodpecker/ci.yaml` | a `windows-cross` step (`golang:1.27`): build and vet for both arches |
| `docs/tools/ws-env.md` `## Running the agent` | "Windows (ADR 0081, not yet run on a real machine)": Task Scheduler at log on (`schtasks /Create /SC ONLOGON /TN ws-env-agent /TR "\"%LocalAppData%\Programs\ws\ws.exe\" env agent run"`), Git for Windows required, `platform_unsupported` for terminals and send, WSL2 as the alternative |
| `README.md` roadmap | the existing unchecked "Windows environment agent" line (added with the ADR) gains "built and tested in CI; not yet run on a Windows machine with Claude Code" |
| `docs/design/env-windows/constraints.md` | update rows that changed (e.g. "What fails today" becomes history; note the CI jobs) |

### Acceptance

- `make dist VERSION=test` here produces `dist/ws-windows-amd64.zip` and
  `dist/ws-windows-arm64.zip`, each containing only `ws.exe`, both in
  `SHA256SUMS`; the four unix binaries unchanged.
- `actionlint` if available, else `python3 -c 'import yaml,sys;yaml.safe_load(open(sys.argv[1]))' .github/workflows/ci.yml`.
- The `windows` and `windows-cross` jobs are **only proven on the PR**:
  the orchestrator must read their result (and the informational step's
  failures) before calling the work done.

### Verify

```bash
make dist VERSION=test && unzip -l dist/ws-windows-amd64.zip && grep windows dist/SHA256SUMS
python3 -c 'import yaml,sys;[yaml.safe_load(open(f)) for f in sys.argv[1:]]' .github/workflows/ci.yml .github/workflows/release.yml .woodpecker/ci.yaml
make lint && go test -p 2 ./...
```

## Not in these tasks (follow-ups, ADR 0081)

- ConPTY (`internal/ptyproc/ptyproc_windows.go`): `CreatePseudoConsole`,
  `CreateProcess` with `STARTUPINFOEX` and
  `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`, `ResizePseudoConsole`; then
  `claude.Send` on Windows and the terminal environment allow-list's
  Windows variables (case-insensitive). Its own task once `claude attach`
  is known to work on Windows.
- Named-pipe daemon transport; DPAPI for the key; a controller on Windows
  (`rawterm_windows.go`, console resize events).
