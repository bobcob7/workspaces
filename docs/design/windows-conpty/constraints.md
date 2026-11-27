# Constraints for `windows-conpty`: ConPTY terminals and `agent.send` on Windows

What the code does today, what a task breaks if it forgets something, and
what this pod can and can't check. Measured 2026-11-27 on
`feat/windows-conpty` at `566af23` (same tree as `main`) with Go 1.27.3
linux/amd64. Every task in [tasks.md](tasks.md) names the rows here that it
touches. The decisions are in [design.md](design.md) (D1-D10) and, once T6
lands, ADR 0086.

## Environment limits (read first)

This pod is linux/amd64 (`Linux 6.18 talos`, 48 CPUs, 128 GB). There is no
Windows host, no `wine` (`which wine` prints nothing), no Windows VM, no
`actionlint`, and Python has no `yaml` module. So:

| Check | Here? | How |
| --- | --- | --- |
| Compile for Windows | yes, cross | `GOOS=windows GOARCH=amd64 go build ./...`, same with `GOARCH=arm64` |
| Vet for Windows | yes, cross | `GOOS=windows go vet ./...` (vet also type-checks `_test.go` files) |
| Compile Windows test binaries | yes, cross | `GOOS=windows GOARCH=<arch> go test -c -o /dev/null ./internal/<pkg>` (one package per call) |
| Lint the Windows files | yes, cross | `L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./internal/<pkg>/...` (`go tool` would otherwise build the linter for Windows) |
| Format the Windows files | yes, cross | `L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" fmt --diff ./...` |
| Run **any** Windows test | **no** | only CI's `windows` job (`windows-latest`, Server 2022, ConPTY available) runs them; a task's Windows tests first run on the PR |
| Unix behaviour unchanged | yes | `go test -p 2 ./internal/<pkg>` |
| macOS, FreeBSD still compile | yes, cross | `GOOS=darwin go build ./...`; `GOOS=freebsd go build ./internal/ptyproc ./internal/envagent ./internal/claude` (the whole repo doesn't build for FreeBSD; these three do today) |
| YAML parses | yes | the `_yamlcheck` snippet in tasks.md T5 (uses `go.yaml.in/yaml/v3`, already in `go.mod`) |
| Run `ws.exe`, `claude attach`, a remote shell | **no** | a person, with design.md's smoke steps |

**Proved here on 2026-11-27** (all passed, times on this pod):

```bash
for a in amd64 arm64; do GOOS=windows GOARCH=$a go build ./... && GOOS=windows GOARCH=$a go vet ./...; done
GOOS=darwin go build ./...
GOOS=freebsd go build ./internal/ptyproc ./internal/envagent ./internal/claude
GOOS=windows GOARCH=amd64 go test -c -o "$CLAUDE_JOB_DIR/tmp/wt/" ./...      # 66 test binaries
L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./...     # 0 issues
make lint                                                                     # 40 s, passes
go test -p 2 -count=1 ./internal/ptyproc ./internal/claude ./internal/envagent ./internal/cli   # 51 s, passes
```

A prototype of T1-T3 (the skeletons in tasks.md) was then built in a
scratch worktree: unix tests of `ptyproc`, `claude`, `envagent` pass; for
`GOOS=windows` amd64 and arm64, `build`, `vet`, `go test -c` and
golangci-lint pass with 0 issues; FreeBSD and darwin build.

**Cannot be verified here or in CI** (CI's runner is a Server image, one
user, no Claude Code sign-in):

- `claude attach` (and `claude --bg`) on native Windows (ADR 0076 question 1);
- `ws shell` inside a pseudoconsole on a real profile, and the remote
  shell flow end to end (needs an enrolled agent and a controller);
- ConPTY on Windows older than 1809, and on Windows 11 24H2's changed
  `ClosePseudoConsole` (CI is Server 2022);
- Windows on arm64 (cross-compiled and vetted only).

## Code today

| Where | What it does | Change | Task |
| --- | --- | --- | --- |
| `internal/ptyproc/ptyproc.go` | package doc, `ErrUnsupported`, `killGrace`, `Spec`, **and** the unix `Proc` (`*os.File`, `*exec.Cmd`, `syscall.Signal`) with `Read` (EIO→EOF), `Write`, `Resize`, `Close` (SIGHUP, SIGKILL after 2 s), `Wait`, `shutdown`, `reaped` | `Proc` and its methods move verbatim to `proc_unix.go` | T1 |
| `internal/ptyproc/ptyproc_other.go` (`!linux && !darwin`) | `Supported() false`, `Start` → `ErrUnsupported`, `setWinsize` stub | T1: a stub `Proc`; T2: tag gains `&& !windows` | T1, T2 |
| `internal/ptyproc/supported_test.go` (no tag) | wants `Supported()` only on linux, darwin | add windows | T2 |
| `internal/ptyproc/ptyproc_test.go` (`linux \|\| darwin`) | unix tests; `TestCloseAfterExit` sets `p.signal` | unchanged: the unix struct keeps its fields | none |
| `internal/envagent/interfaces.go` | `ptyProcess`: `io.ReadWriter`, `Resize`, `Close`, `Wait() *int`; `ptyprocStarter` returns `*ptyproc.Proc` as it | unchanged | none |
| `internal/envagent/terminals.go:223-247` | `errPlatformUnsupported` "terminals aren't supported on <goos> agents yet (ADR 0081)"; refuses when `!m.supported()` or `Start` returns `ErrUnsupported` | message only: "terminals aren't supported on <goos> agents: no pseudo-terminals (ADR 0086)" | T4 |
| `internal/envagent/terminals.go:525,655` | `read` loop ends on any `Read` error → `exited`; `finish` does `Close`, `Wait`, then `<-readDone` | needs `Read` to return after the process exits **and** after `Close` (D3, D4) | T2 |
| `internal/claude/send.go` | `openPTY()` → `*os.File`s, `exec.CommandContext` with `attachProcAttr()`, `typeMessage(… *os.File …)`, `watchPrompt(*os.File)`, `Client.hangUp(cmd, control)` | split per D7 | T3 |
| `internal/claude/pty_unix.go` (`darwin \|\| linux`) | `attachProcAttr`, `ptyRows`/`ptyCols`, `ptyControl`, `openTTY` | gains `startAttach`/`unixAttach.hangUp` (moved code); loses `ptyRows`/`ptyCols` to `send.go` | T3 |
| `internal/claude/pty_other.go` (`!darwin && !linux`) | `attachProcAttr` nil, `openPTY` → `errors.ErrUnsupported` | tag gains `&& !windows`; becomes `startAttach` → `errors.ErrUnsupported` | T3 |
| `internal/claude/send_test.go` (no tag) | `#!/bin/sh` fakes; compiles on Windows, never run there | unchanged | none |
| `internal/cli/env_agent_sessions.go:179` | `errors.ErrUnsupported` → `platform_unsupported` "sending to an agent isn't supported on <goos> environments yet (ADR 0081)" | message only | T4 |
| `internal/cli/env_agent_terminal_windows.go` | case-insensitive allow-list (PATH, PATHEXT, SYSTEMROOT, SYSTEMDRIVE, WINDIR, COMSPEC, USERPROFILE, HOMEDRIVE, HOMEPATH, HOME, APPDATA, LOCALAPPDATA, PROGRAMDATA, PROGRAMFILES, PROGRAMFILES(X86), PROGRAMW6432, COMMONPROGRAMFILES, TEMP, TMP, USERNAME, USERDOMAIN, COMPUTERNAME, NUMBER_OF_PROCESSORS, PROCESSOR_ARCHITECTURE, OS, LANG), `isPathKey`, `executableNames` (PATHEXT), `isExecutable` | add 8 names (D8); add `homeOf` | T4 |
| `internal/cli/env_agent_terminal.go:107-135` | `shellCommand` refuses when `homeOf(env)` (only `HOME=`) is empty; `homeOf` defined here | `homeOf` moves to `_unix.go` (verbatim) and `_windows.go` (HOME, else USERPROFILE) | T4 |
| `internal/shell/shell_windows.go` | `ws shell` runs `%COMSPEC%`, else `cmd.exe` | unchanged (D8) | none |
| `internal/cli/rawterm_other.go` | a Windows **controller** can't attach | unchanged (D9) | none |

## Pinned values and tests a task can break

| Where | What is pinned | Breaks when |
| --- | --- | --- |
| `Makefile` `lint`, ADR 0024 grep | `exec.Command(Context)?(…"claude"…)` only in `internal/claude/` | fine as long as the Windows lookup is `exec.LookPath(c.path)` inside `internal/claude` |
| `Makefile` `lint`, `internal/blanklines` | no blank lines inside function bodies in **every** `.go` file, `_windows.go` and `_test.go` included | a skeleton is pasted with a blank line in a body |
| `.golangci.yml` | runs only for the host GOOS; `forbidigo` bans `os.Exit`, `os.Stdin`, `fmt.Print*`; `errcheck`; `govet` `shadow`; `unparam`; `gochecknoglobals`; `funlen`/`cyclop` | the Windows files aren't linted unless you run the cross lint above; a `TestMain` needs `//nolint:forbidigo // <reason>` (pattern: `internal/mqttbroker/conformance_test.go:409-411`) |
| `internal/envagent/moq_test.go` | generated from `interfaces.go` | **don't** change `ptyProcess`/`ptyStarter`; nothing here needs it |
| `internal/envagent/terminals_test.go:772-800` | asserts `platform_unsupported` **code** for `ErrUnsupported` and `supported()==false` | the code must stay; only messages change |
| `internal/cli/env_agent_sessions_test.go:268-275` | `errors.ErrUnsupported` from `Send` → `platform_unsupported` | `pty_other.go` must keep returning an error that `errors.Is(…, errors.ErrUnsupported)` |
| `internal/cli/errors.go`, `TestGuideTopics`, `TestErrorCode_exitCode`, ADR 0016 | the code list | **no** code is added or removed: don't touch them |
| `internal/envproto` protocol number, `OpSpec.Since` | the hello's protocol | **don't** bump: no new operation |
| `Makefile` `test-windows`, `WINDOWS_TESTS` | the Windows test set CI runs | T5 adds `./internal/ptyproc`, `-run '…_windows'` lines for `claude`, `envagent`, `cli` |
| `.github/workflows/ci.yml` jobs `windows-cross`, `windows` | build/vet/test -c/lint from Linux; `make test-windows` on `windows-latest` | T5 adds a `windows-exe` job; don't rename the existing jobs |
| `Makefile` `dist`, `release.yml` | `ws-windows-<arch>.zip` in `dist/`, in `SHA256SUMS` | T5's `windows-exe` must write under `dist/windows-<arch>/`, not `dist/ws-*`, and is a separate target |
| `go.mod` | `golang.org/x/sys v0.50.0` has `CreatePseudoConsole`, `ResizePseudoConsole`, `ClosePseudoConsole`, `CreatePipe`, `NewProcThreadAttributeList`/`Update`/`List`/`Delete`, `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE`, `StartupInfoEx`, `STARTF_USESTDHANDLES`, `EXTENDED_STARTUPINFO_PRESENT`, `CREATE_UNICODE_ENVIRONMENT`, `CreateProcess`, `ComposeCommandLine`, `EscapeArg`, `Coord`, `CreateToolhelp32Snapshot`, `Process32First/Next`, `OpenProcess`, `WaitForSingleObject`, `NewLazySystemDLL`, `DLLError` (checked in the module cache). It has **no** helper that builds an environment block from strings (`CreateEnvBlock` is the user-token one): T2 writes `envBlock` | **no** `go.mod`/`go.sum` change; `git diff --exit-code origin/feat/windows-conpty -- go.mod go.sum` |
| `docs/adrs/README.md` index | one row per ADR, numbers never reused | 0086 is the next free number (0084 is the last) |

## Rules for every task

- Read this file and [design.md](design.md) first.
- Unix behaviour must not change: move unix code **verbatim**; keep unix
  tests passing unchanged.
- `golang.org/x/sys/windows` only; no new module, no `go.mod` change, no
  shelling out to PowerShell, `taskkill` or `icacls`.
- No blank lines inside function bodies; gofumpt via `go tool golangci-lint fmt`.
- One test file per source file, build-tagged by its name
  (`ptyproc_windows.go` → `ptyproc_windows_test.go`).
- `go test -p 2`; heavy checks one at a time.
- Commit and push after **each file**. Commit messages: imperative,
  conventional prefix (`feat:`, `test:`, `docs:`, `ci:`, `refactor:`), and
  **never** a `Co-Authored-By` trailer or any Claude/AI attribution (this
  project's rule overrides any role prompt that asks for one).
- Never wait with a loop on `pgrep -f <pattern>` (it matches itself); run
  long jobs as background commands and wait for their notice. Before
  `ws machine result`, make sure no background job of yours still runs.
- In the report, list under `## Not run here` every check only CI's
  Windows job (or a person) can run, and say which files are only
  compile-checked. Record `PASS` when everything runnable here passed.
