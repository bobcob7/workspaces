# win-attach: attaching to an agent on a Windows environment

Bug report (2026-11-25): on a Windows environment agent (ADR 0081), opening
an agent from the desktop app shows
`ws: claude isn't installed: executable file not found in %PATH%` then
`[exited with status 1]`.

## Root cause (orchestrator's analysis)

The desktop runs `ws attach --env=<env> -- <workspace>` (desktop/src-tauri/src/pty.rs).
The Windows agent answers `terminal.open` in `internal/envagent/terminals.go`
`(*terminals).start`, which calls `commands.TerminalCommand` **before**
`starter.Start`. `TerminalCommand` is `(*agentOperations).attachCommand`
(`internal/cli/env_agent_terminal.go`). It found the agent's Claude session
(so `claude --bg` did start the agent), then `lookPathIn(terminalEnv(environ))`
failed, giving `claude_missing` with exec.ErrNotFound's Windows text. On
Windows `lookPathIn` can never succeed:

1. Windows' environment spells the key `Path`; `terminalEnvAllowed` only keeps
   `PATH` (case-sensitive), so the child env has no search path at all.
2. It looks for `claude` with no extension; native Claude Code is `claude.exe`
   (npm installs `claude.cmd`), so PATHEXT is never tried.
3. It requires `Perm()&0o111 != 0`; Go on Windows reports 0666/0444, never x bits.

Even with a correct lookup, ADR 0081 D5 says terminals refuse on Windows
with `platform_unsupported` (ptyproc_other.go returns ErrUnsupported). That
refusal was unreachable for `attach` because the claude lookup ran first.
So the user saw a false "claude isn't installed" instead of the documented
refusal.

## T1 (only task)

Branch: `feat/win-attach-t1` from `feat/win-attach`. Commit prefix `fix:`.
No `Co-Authored-By` trailer (project rule; it overrides the role prompt).

### Part A: refuse terminals before building the command

- `internal/ptyproc/ptyproc_unix.go` (`linux || darwin`): add
  `// Supported reports whether this system has pseudo-terminals.`
  `func Supported() bool { return true }`.
- `internal/ptyproc/ptyproc_other.go`: `func Supported() bool { return false }`.
- `internal/envagent/terminals.go`: add a field `supported func() bool` to
  `terminals`, set to `ptyproc.Supported` in `newTerminals`. At the top of
  `(*terminals).start`, before `m.commands.TerminalCommand`, return the same
  `platform_unsupported` ReplyError the post-Start branch builds (extract a
  small helper `errPlatformUnsupported()` returning `*envproto.ReplyError`
  with message `"terminals aren't supported on " + runtime.GOOS + " agents yet (ADR 0081)"`,
  used in both places). Keep the existing post-Start mapping.
- Test in `internal/envagent/terminals_test.go`, `TestTerminals_open`, new
  subtest "an unsupported platform refuses before building the command":
  `h.m.supported = func() bool { return false }`, `h.m.commands` a
  `terminalCommandsMock` whose func calls `t.Error` (must not be called);
  open returns ReplyError code `platform_unsupported`; then the slot is given
  back (restore supported and `h.open()` succeeds `envproto.MaxTerminals` times
  is not needed; one `h.open()` after `MaxTerminals+1` refusals, like the
  sibling subtests). Also check `newTerminals(...)` sets `supported`
  (non-nil) — cover it however the existing harness allows.

### Part B: make lookPathIn and the terminal env correct on Windows

Move the OS-specific pieces of `internal/cli/env_agent_terminal.go` into two
new files; keep the shared logic in env_agent_terminal.go.

`internal/cli/env_agent_terminal_unix.go` (`//go:build !windows`):
- `terminalEnvAllowed` moved here verbatim (same doc comment).
- `func isPathKey(key string) bool { return key == "PATH" }`
- `func executableNames(program string, _ []string) []string { return []string{program} }`
- `func isExecutable(info fs.FileInfo) bool { return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 }`

`internal/cli/env_agent_terminal_windows.go` (`//go:build windows`):
- `terminalEnvAllowed(key)`: case-insensitive (`strings.EqualFold`) match
  against: PATH, PATHEXT, SYSTEMROOT, SYSTEMDRIVE, WINDIR, COMSPEC,
  USERPROFILE, HOMEDRIVE, HOMEPATH, HOME, APPDATA, LOCALAPPDATA, PROGRAMDATA,
  PROGRAMFILES, PROGRAMFILES(X86), PROGRAMW6432, COMMONPROGRAMFILES,
  TEMP, TMP, USERNAME, USERDOMAIN, COMPUTERNAME, NUMBER_OF_PROCESSORS,
  PROCESSOR_ARCHITECTURE, OS, LANG. Nothing else (no tokens).
  Doc comment: why (ADR 0081 follow-up list; Windows needs SystemRoot etc.).
- `isPathKey(key)`: `strings.EqualFold(key, "PATH")`.
- `executableNames(program string, env []string) []string`: the extensions
  are the last PATHEXT in env (key matched case-insensitively), split on
  `;`, empty entries dropped; default `.COM;.EXE;.BAT;.CMD` when unset or
  empty. If `filepath.Ext(program)` equals one of them (EqualFold) return
  `[]string{program}`; else `program+ext` for each ext, in order.
- `isExecutable(info)`: `info.Mode().IsRegular()`.

`env_agent_terminal.go` `lookPathIn(env)`:
- pick the last `KEY=value` whose key satisfies `isPathKey` (use
  `strings.Cut(kv, "=")`, not CutPrefix).
- for each absolute dir in `filepath.SplitList`, for each name in
  `executableNames("claude", env)`, `os.Stat(filepath.Join(dir, name))`, return
  the first with `isExecutable`. Else `exec.ErrNotFound`. Keep the doc comment
  accurate. `homeOf` is unchanged (shells refuse on Windows anyway).
- `terminalEnv` unchanged except it uses the per-OS `terminalEnvAllowed`.

Tests:
- Existing unix tests in `internal/cli/env_agent_terminal_test.go` must still
  pass unchanged, except `TestTerminalEnvAllowed` and the lookPathIn subtests
  must be moved to a `//go:build !windows` test file
  `env_agent_terminal_unix_test.go` **only if** they fail to compile or run
  on Windows; check with `GOOS=windows go vet ./internal/cli`. Prefer leaving
  them; if moved, move verbatim.
- New `internal/cli/env_agent_terminal_windows_test.go` (`//go:build windows`),
  functions `TestTerminalEnvAllowed_windows`, `TestExecutableNames`,
  `TestLookPathIn_windows`: `Path=` (mixed case) is kept and `path`
  allowed, `CLAUDE_CODE_OAUTH_TOKEN`/`GH_TOKEN` blocked; PATHEXT default and
  custom (`.EXE;;.CMD` -> claude.EXE, claude.CMD); `claude.exe` given keeps
  its name; lookPathIn finds `claude.cmd` in a t.TempDir() given
  `Path=<dir>` and `PATHEXT=.EXE;.CMD`, prefers `.exe` when both exist,
  skips a relative dir, and fails with exec.ErrNotFound when only an
  extensionless `claude` exists. These only run on windows-latest CI.
- `Makefile` `test-windows`: add the line
  `go test -p 2 -run 'TestTerminalEnvAllowed_windows|TestExecutableNames|TestLookPathIn_windows' ./internal/cli || rc=1; \`
  next to the other `-run` lines.

### Verify (all must pass; list any you can't run under `## Not run here`)

```
gofmt -l internal/                     # prints nothing
go build ./... && GOOS=windows GOARCH=amd64 go build ./... && GOOS=windows GOARCH=arm64 go build ./... && GOOS=darwin go build ./...
GOOS=windows go vet ./internal/cli ./internal/envagent ./internal/ptyproc
GOOS=windows go test -c -o /dev/null ./internal/cli
GOOS=windows go test -c -o /dev/null ./internal/envagent
go test -p 2 -race ./internal/cli -run 'TerminalCommand|TerminalEnv|Within|LookPath'
go test -p 2 -race ./internal/envagent ./internal/ptyproc
L=$(GOOS= GOARCH= go tool -n golangci-lint); "$L" run ./internal/cli/... ./internal/envagent/... ./internal/ptyproc/...; GOOS=windows "$L" run ./internal/cli/... ./internal/envagent/... ./internal/ptyproc/...
```

Mutation check (report survivors): flip `isPathKey` on Windows to
case-sensitive; drop the `supported` early return; drop the `.exe` from the
default PATHEXT. The first and third can only be judged by compiling
(Windows tests don't run here) — say so.
