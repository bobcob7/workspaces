# Tasks: ConPTY terminals and `agent.send` on Windows

Implements [design.md](design.md) (D1-D10; ADR 0085 once T6 lands). Every
task must first read [constraints.md](constraints.md): it lists what this
pod can't run (no Windows, no wine: Windows tests are compile-checked here
and run only in CI's `windows` job on `windows-latest`), the pinned values a
task can break, and the rules for every task (verbatim unix moves, x/sys
only, no blank lines in function bodies, commit and push after each file, no
`Co-Authored-By` or any Claude/AI attribution, `go test -p 2`, no `pgrep -f`
loops, no background job left before `ws machine result`, `## Not run here`).

Base: `feat/windows-conpty`. **No `go.mod` change.** The code skeletons
below were compiled, vetted and linted for `GOOS=windows` amd64 and arm64 by
the designer (constraints.md, "Proved here"); paste them, then fix only what
the verify commands flag. They are not proven on Windows: CI is.

## Order

```
T1 ─┬─ T2 ─────┐
    ├─ T3      ├─ T5
    ├─ T4 ─────┘
T6 (docs) ∥ all
```

- **T1** first (small refactor; every later task builds on its API).
- **T2 ∥ T3 ∥ T4** after T1: disjoint files (`internal/ptyproc` /
  `internal/claude` / `internal/cli` + `internal/envagent`). T3's and T4's
  Windows tests only pass in CI once T2 is merged too; that's expected.
- **T5** (CI, Makefile) after T2, T3, T4: it names their tests.
- **T6** (docs, ADR 0085) is `∥` with every task (only `.md` files).

Common verify block (each task runs it, plus its own lines):

```bash
test -z "$(go tool golangci-lint fmt --diff ./...)"
L=$(GOOS= GOARCH= go tool -n golangci-lint); for a in amd64 arm64; do test -z "$(GOOS=windows GOARCH=$a "$L" fmt --diff ./...)" || exit 1; done
go build ./... && GOOS=darwin go build ./... && GOOS=freebsd go build ./internal/ptyproc ./internal/envagent ./internal/claude
for a in amd64 arm64; do GOOS=windows GOARCH=$a go build ./... || exit 1; done
go test -count=1 -run '^TestRepository$' ./internal/blanklines/
git diff --exit-code origin/feat/windows-conpty -- go.mod go.sum
```

---

## T1. Split `ptyproc.Proc` per OS (`small`)

Depends on: none. Constraints rows: "Code today" `ptyproc.go`,
`ptyproc_other.go`; pinned `ptyproc_test.go`, `moq_test.go`.

### Files

- `internal/ptyproc/ptyproc.go`: keep only the package doc, `ErrUnsupported`
  and `Spec`; import only `errors`.
- `internal/ptyproc/proc_unix.go` (new, `//go:build linux || darwin`): the
  `killGrace` const and everything from `// Proc is a running process` to
  the end of today's `ptyproc.go` (`Proc`, `Read`, `Write`, `Resize`,
  `Close`, `Wait`, `shutdown`, `reaped`), **byte for byte**, with the imports
  it needs (`errors`, `io`, `os`, `os/exec`, `sync`, `syscall`, `time`).
- `internal/ptyproc/ptyproc_other.go`: tag stays `!linux && !darwin` (T2
  narrows it); replace the file with:

```go
//go:build !linux && !darwin

package ptyproc

// Supported reports whether this system has pseudo-terminals.
func Supported() bool { return false }

// Start is unsupported on this system.
func Start(Spec) (*Proc, error) {
	return nil, ErrUnsupported
}

// Proc is never created on this system; its methods exist so callers build.
type Proc struct{}

// Read fails: there is no process.
func (*Proc) Read([]byte) (int, error) { return 0, ErrUnsupported }

// Write fails: there is no process.
func (*Proc) Write([]byte) (int, error) { return 0, ErrUnsupported }

// Resize fails: there is no terminal.
func (*Proc) Resize(int, int) error { return ErrUnsupported }

// Close does nothing.
func (*Proc) Close() error { return nil }

// Wait returns nil: there is no exit code.
func (*Proc) Wait() *int { return nil }
```

(no `//nolint:gocritic` on `Start`: the parameter is unnamed, so
`nolintlint` reports the directive as unused on Windows; `setWinsize` goes:
nothing calls it here.)

### Acceptance

- The move is verbatim:
  `diff <(git show origin/feat/windows-conpty:internal/ptyproc/ptyproc.go | sed -n '/^\/\/ Proc is a running process/,$p') <(sed -n '/^\/\/ Proc is a running process/,$p' internal/ptyproc/proc_unix.go)`
  prints nothing.
- Unix tests unchanged and green; Windows and FreeBSD still build (Windows
  uses the stub until T2); `envagent` unchanged.

### Verify

```bash
go test -p 2 -count=1 ./internal/ptyproc ./internal/envagent
go tool golangci-lint run ./internal/ptyproc/...
for a in amd64 arm64; do GOOS=windows GOARCH=$a go vet ./internal/ptyproc ./internal/envagent ./internal/claude || exit 1; done
L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./internal/ptyproc/... ./internal/envagent/...
```

Not run here: nothing new (Windows behaviour is the unchanged refusal).

---

## T2. ConPTY in `ptyproc` and its Windows tests (the hard one)

Depends on: T1. Parallel with T3, T4, T6. Constraints rows: "Code today"
`ptyproc_other.go`, `supported_test.go`, `terminals.go:525,655`; pinned
`go.mod` (x/sys list), `.golangci.yml`. Design: D2-D6.

### Files

- `internal/ptyproc/ptyproc_windows.go` (new): the skeleton below.
- `internal/ptyproc/ptyproc_windows_test.go` (new): the skeleton below.
- `internal/ptyproc/ptyproc_other.go`: first line becomes
  `//go:build !linux && !darwin && !windows`.
- `internal/ptyproc/supported_test.go`: `want := runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows"`,
  message "pseudo-terminals exist on linux, darwin and windows (ConPTY)".
- `internal/ptyproc/ptyproc.go`: `Spec.Env`'s comment becomes
  `// the whole environment; nil or empty gives an empty one (on Windows, only SYSTEMROOT)`.

### API call sequence and closing order (what the skeleton does)

Start: `CreatePipe` ×2 → `CreatePseudoConsole(size, inRead, outWrite, 0, &hpc)`
→ **close `inRead` and `outWrite` at once** (else no EOF ever) →
`NewProcThreadAttributeList(1)` + `Update(PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, <the HPCON value>, sizeof)`
→ `StartupInfoEx{Cb, Flags: STARTF_USESTDHANDLES, null std handles}` →
`CreateProcess(app, ComposeCommandLine, inherit=false, EXTENDED_STARTUPINFO_PRESENT|CREATE_UNICODE_ENVIRONMENT, envBlock, dir)`
→ `attrs.Delete()` → `os.FindProcess(pid)` while `pi.Process` is open →
close `pi.Thread`, `pi.Process` → `go p.wait()`.

Error paths: every handle created so far is closed before returning; after
`CreatePseudoConsole` succeeded, `ClosePseudoConsole` too; a
`*windows.DLLError` from `CreatePseudoConsole` (Windows < 1809) is wrapped
as `ErrUnsupported`.

wait: `process.Wait()` → set `exit` → `close(done)` → `closeConsole()`
(EOF for readers after the last frame).

Close (once): `close(closed)` → start drain → `go closeConsole()` → wait
`killGrace` for `done`, else `process.Kill()` → wait ≤ `closeWait` for
`done` → wait ≤ `closeWait` for `consoleOK`, else close the files in the
background and return `errConsoleStuck` → close both files.

```go
package ptyproc

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// killGrace is how long Close waits after closing the console before it
	// kills the process.
	killGrace = 2 * time.Second
	// closeWait bounds each of Close's waits after the grace period: for the
	// process to end once killed, and for the pseudoconsole to close.
	closeWait = 5 * time.Second
)

// errConsoleStuck is Close's error when ClosePseudoConsole hasn't returned
// within closeWait; the pipes are then closed in the background.
var errConsoleStuck = errors.New("the pseudoconsole didn't close")

// Supported reports whether this system has pseudo-terminals: ConPTY,
// which kernel32 has on Windows 10 1809 and Server 2019 and later (ADR 0085).
func Supported() bool {
	return windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find() == nil
}

// Proc is a running process on a pseudoconsole (ConPTY).
type Proc struct {
	input     *os.File // our end of the console's input pipe
	output    *os.File // our end of the console's output pipe
	process   *os.Process
	done      chan struct{} // closed once the process has exited and exit is set
	exit      *int
	consoleMu sync.Mutex
	console   windows.Handle // the HPCON; 0 once closed
	consoleOK chan struct{}  // closed once ClosePseudoConsole has returned
	closeOnce sync.Once
	closeErr  error
	closed    chan struct{} // closed when Close starts
	drainOnce sync.Once
}

// Start creates a pseudoconsole sized Cols×Rows, starts spec on it, and
// returns once the process runs.
//
//nolint:gocritic // hugeParam: Spec is passed by value, as the package API states.
func Start(spec Spec) (*Proc, error) {
	app, cmdline, env, dir, err := startArgs(spec)
	if err != nil {
		return nil, err
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err = windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, fmt.Errorf("creating the console's input pipe: %w", err)
	}
	if err = windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		closeHandles(inRead, inWrite)
		return nil, fmt.Errorf("creating the console's output pipe: %w", err)
	}
	var console windows.Handle
	err = windows.CreatePseudoConsole(consoleSize(spec.Cols, spec.Rows), inRead, outWrite, 0, &console)
	// The console holds its own copies of its ends of the pipes; ours must go,
	// or the output pipe never reaches EOF.
	closeHandles(inRead, outWrite)
	if err != nil {
		closeHandles(inWrite, outRead)
		if _, ok := errors.AsType[*windows.DLLError](err); ok {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		return nil, fmt.Errorf("creating a pseudoconsole: %w", err)
	}
	process, err := createProcess(console, app, cmdline, env, dir)
	if err != nil {
		windows.ClosePseudoConsole(console)
		closeHandles(inWrite, outRead)
		return nil, fmt.Errorf("starting %s: %w", spec.Path, err)
	}
	p := &Proc{
		input:     os.NewFile(uintptr(inWrite), "conpty-input"),
		output:    os.NewFile(uintptr(outRead), "conpty-output"),
		process:   process,
		done:      make(chan struct{}),
		console:   console,
		consoleOK: make(chan struct{}),
		closed:    make(chan struct{}),
	}
	go p.wait()
	return p, nil
}

// startArgs converts spec to CreateProcess's arguments.
//
//nolint:gocritic // hugeParam: Spec is passed by value, as the package API states.
func startArgs(spec Spec) (app, cmdline *uint16, env []uint16, dir *uint16, err error) {
	app, err = windows.UTF16PtrFromString(spec.Path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	cmdline, err = windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{spec.Path}, spec.Args...)))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	env, err = envBlock(dedupEnv(withSystemRoot(spec.Env, os.Getenv("SYSTEMROOT"))))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if spec.Dir != "" {
		dir, err = windows.UTF16PtrFromString(spec.Dir)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return app, cmdline, env, dir, nil
}

// createProcess starts the program attached to console and returns it.
func createProcess(console windows.Handle, app, cmdline *uint16, env []uint16, dir *uint16) (*os.Process, error) {
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return nil, err
	}
	defer attrs.Delete()
	// The attribute's value is the HPCON itself, not a pointer to it.
	if err = attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&console)), unsafe.Sizeof(console)); err != nil {
		return nil, err
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	// No standard handles: without this flag the child inherits ours, and
	// writes to them instead of to the console when they are redirected.
	si.Flags = windows.STARTF_USESTDHANDLES
	var pi windows.ProcessInformation
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_UNICODE_ENVIRONMENT)
	if err = windows.CreateProcess(app, cmdline, nil, nil, false, flags, &env[0], dir, &si.StartupInfo, &pi); err != nil {
		return nil, err
	}
	defer closeHandles(pi.Thread, pi.Process)
	// The handle in pi keeps the pid from being reused until FindProcess has its own.
	process, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return nil, err
	}
	return process, nil
}

// Read reads the console's output. It returns io.EOF once the process has
// exited and the console has drawn its last frame.
func (p *Proc) Read(b []byte) (int, error) {
	return p.output.Read(b)
}

// Write sends b to the process's input. Once the process has exited the
// console is gone, so Write may fail before Close; after Close it fails.
func (p *Proc) Write(b []byte) (int, error) {
	return p.input.Write(b)
}

// Resize sets the console's size. It succeeds after the process has exited,
// until Close; after Close it returns an error.
func (p *Proc) Resize(cols, rows int) error {
	select {
	case <-p.closed:
		return os.ErrClosed
	default:
	}
	p.consoleMu.Lock()
	defer p.consoleMu.Unlock()
	if p.console == 0 {
		return nil
	}
	return windows.ResizePseudoConsole(p.console, consoleSize(cols, rows))
}

// Close closes the console, which ends the programs attached to it, kills
// the process if it still runs after 2 s, and closes the pipes. It never
// blocks for longer than about 12 s, and is safe to call more than once.
// Programs that detached from the console (DETACHED_PROCESS) keep running,
// as a setsid daemon survives a unix hang-up.
func (p *Proc) Close() error {
	p.closeOnce.Do(func() { p.closeErr = p.shutdown() })
	return p.closeErr
}

// Wait waits for the process and returns its exit code.
func (p *Proc) Wait() *int {
	<-p.done
	return p.exit
}

func (p *Proc) shutdown() error {
	close(p.closed)
	p.drain()
	go p.closeConsole()
	select {
	case <-p.done:
	case <-time.After(killGrace):
		_ = p.process.Kill()
	}
	select {
	case <-p.done:
	case <-time.After(closeWait):
	}
	select {
	case <-p.consoleOK:
	case <-time.After(closeWait):
		go func() { _ = p.closeFiles() }()
		return errConsoleStuck
	}
	return p.closeFiles()
}

// drain discards the console's output from now on, so ClosePseudoConsole,
// which waits for the console to write its last frame, can't block on a
// full pipe when nothing else reads it.
func (p *Proc) drain() {
	p.drainOnce.Do(func() { go func() { _, _ = io.Copy(io.Discard, p.output) }() })
}

func (p *Proc) closeFiles() error {
	return errors.Join(p.input.Close(), p.output.Close())
}

// closeConsole closes the pseudoconsole once; later calls wait for the first.
// Programs still attached are ended, and the output pipe reaches EOF after
// the last frame.
func (p *Proc) closeConsole() {
	p.consoleMu.Lock()
	console := p.console
	p.console = 0
	p.consoleMu.Unlock()
	if console == 0 {
		<-p.consoleOK
		return
	}
	windows.ClosePseudoConsole(console)
	close(p.consoleOK)
}

// wait reaps the process, records its exit code, then closes the console so
// readers see the last output and io.EOF.
func (p *Proc) wait() {
	state, err := p.process.Wait()
	if err == nil {
		code := state.ExitCode()
		p.exit = &code
	}
	close(p.done)
	p.closeConsole()
}

// consoleSize clamps a terminal size to what a COORD holds, at least 1×1.
func consoleSize(cols, rows int) windows.Coord {
	return windows.Coord{X: coordField(cols), Y: coordField(rows)}
}

func coordField(n int) int16 {
	return int16(min(max(n, 1), math.MaxInt16))
}

// withSystemRoot adds SYSTEMROOT=root to env when env has no SYSTEMROOT,
// in any case, as os/exec does: many Windows programs fail without it.
func withSystemRoot(env []string, root string) []string {
	for _, kv := range env {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, "SYSTEMROOT") {
			return env
		}
	}
	if root == "" {
		return env
	}
	return append(append([]string{}, env...), "SYSTEMROOT="+root)
}

// dedupEnv keeps the last "key=value" of each key, comparing keys without
// regard to case as Windows does, in the order of those last entries. A
// leading '=' belongs to the key ("=C:=C:\\ws").
func dedupEnv(env []string) []string {
	last := make(map[string]int, len(env))
	for i, kv := range env {
		last[envKey(kv)] = i
	}
	out := make([]string, 0, len(last))
	for i, kv := range env {
		if last[envKey(kv)] == i {
			out = append(out, kv)
		}
	}
	return out
}

// envKey is kv's key, upper-cased.
func envKey(kv string) string {
	if i := strings.IndexByte(kv[min(1, len(kv)):], '='); i >= 0 {
		return strings.ToUpper(kv[:i+min(1, len(kv))])
	}
	return strings.ToUpper(kv)
}

// envBlock is env as CreateProcess's Unicode environment block: each
// "key=value" followed by a NUL, and one more NUL at the end.
func envBlock(env []string) ([]uint16, error) {
	block := make([]uint16, 0, 64)
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			return nil, fmt.Errorf("environment variable %q holds a NUL", kv)
		}
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil
}

func closeHandles(handles ...windows.Handle) {
	for _, h := range handles {
		_ = windows.CloseHandle(h)
	}
}
```

If CI shows the child's output missing (test "output, exit code 0 and
io.EOF" gets no `hello`) set `si.StdInput`, `si.StdOutput`, `si.StdErr` to
`windows.InvalidHandle` (wezterm's choice) and say so in the report.

### Tests

```go
package ptyproc

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// cmdExe is cmd.exe's absolute path: Spec.Path is never searched for.
func cmdExe(t *testing.T) string {
	t.Helper()
	root := os.Getenv("SystemRoot")
	require.NotEmpty(t, root, "SystemRoot is unset")
	return filepath.Join(root, "System32", "cmd.exe")
}

// startCmd runs cmd.exe with args on a pseudoconsole, closes it when the
// test ends, and fails the test if that Close takes longer than 15 s.
func startCmd(t *testing.T, args ...string) *Proc {
	t.Helper()
	p, err := Start(Spec{Path: cmdExe(t), Args: args, Dir: t.TempDir(), Env: os.Environ(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { closeWithin(t, p, 15*time.Second) })
	return p
}

// closeWithin calls p.Close and fails the test if it takes longer than d.
func closeWithin(t *testing.T, p *Proc, d time.Duration) {
	t.Helper()
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(d):
		t.Errorf("Close did not return within %s", d)
	}
}

// readWait bounds readAll.
const readWait = 15 * time.Second

// readAll reads p to EOF in the background and fails the test after readWait.
func readAll(t *testing.T, p *Proc) string {
	t.Helper()
	type result struct {
		out []byte
		err error
	}
	got := make(chan result, 1)
	go func() {
		out, err := io.ReadAll(p)
		got <- result{out, err}
	}()
	select {
	case r := <-got:
		require.NoError(t, r.err, "ReadAll ends at io.EOF")
		return string(r.out)
	case <-time.After(readWait):
		require.FailNow(t, "the output never reached EOF", "within %s", readWait)
		return ""
	}
}

// readUntil reads p until its output contains want, failing after d.
func readUntil(t *testing.T, p *Proc, want string, d time.Duration) string {
	t.Helper()
	var out bytes.Buffer
	found := make(chan struct{})
	go func() {
		defer close(found)
		buf := make([]byte, 4096)
		for !strings.Contains(out.String(), want) {
			n, err := p.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-found:
	case <-time.After(d):
		_ = p.Close()
		<-found
		require.FailNow(t, "output never contained "+want, "got %q", out.String())
	}
	return out.String()
}

// waitWithin returns p.Wait's code, failing the test after d.
func waitWithin(t *testing.T, p *Proc, d time.Duration) *int {
	t.Helper()
	got := make(chan *int, 1)
	go func() { got <- p.Wait() }()
	select {
	case code := <-got:
		return code
	case <-time.After(d):
		require.FailNow(t, "Wait did not return", "within %s", d)
		return nil
	}
}

func TestStart_windows(t *testing.T) {
	t.Run("output, exit code 0 and io.EOF", func(t *testing.T) {
		p := startCmd(t, "/c", "echo hello")
		assert.Contains(t, readAll(t, p), "hello")
		code := waitWithin(t, p, 10*time.Second)
		require.NotNil(t, code)
		assert.Equal(t, 0, *code)
	})
	t.Run("the exit code is returned", func(t *testing.T) {
		p := startCmd(t, "/c", "exit 7")
		_ = readAll(t, p)
		code := waitWithin(t, p, 10*time.Second)
		require.NotNil(t, code)
		assert.Equal(t, 7, *code)
	})
	t.Run("input reaches an interactive cmd", func(t *testing.T) {
		p := startCmd(t)
		_, err := p.Write([]byte("echo mark^er\r\n"))
		require.NoError(t, err)
		assert.Contains(t, readUntil(t, p, "marker", 15*time.Second), "marker")
	})
	t.Run("resize is seen by the program", func(t *testing.T) {
		p := startCmd(t)
		require.NoError(t, p.Resize(100, 30))
		_, err := p.Write([]byte("mode con\r\n"))
		require.NoError(t, err)
		// ConPTY may draw a run of spaces as a cursor move (ESC [ n C).
		assert.Regexp(t, `Columns:(?:\s|\x1b\[[0-9;]*[A-Za-z])*100\b`, readUntil(t, p, "Code page", 15*time.Second))
	})
	t.Run("a nil environment holds only SYSTEMROOT", func(t *testing.T) {
		t.Setenv("PTYPROC_SECRET", "leaked")
		p, err := Start(Spec{Path: cmdExe(t), Args: []string{"/c", "set"}, Dir: t.TempDir(), Cols: 200, Rows: 24})
		require.NoError(t, err)
		t.Cleanup(func() { closeWithin(t, p, 15*time.Second) })
		out := readAll(t, p)
		assert.NotContains(t, out, "PTYPROC_SECRET")
		assert.Contains(t, strings.ToUpper(out), "SYSTEMROOT=")
	})
}

func TestClose_windows(t *testing.T) {
	t.Run("ends a running program within seconds, twice", func(t *testing.T) {
		p := startCmd(t, "/c", "ping -n 30 127.0.0.1")
		readUntil(t, p, "127.0.0.1", 15*time.Second)
		started := time.Now()
		closeWithin(t, p, 15*time.Second)
		assert.Less(t, time.Since(started), 15*time.Second)
		code := waitWithin(t, p, 5*time.Second)
		if code != nil {
			assert.NotZero(t, *code)
		}
		closeWithin(t, p, time.Second)
		_, err := p.Write([]byte("x"))
		require.Error(t, err, "Write after Close fails")
		require.Error(t, p.Resize(90, 30), "Resize after Close fails")
	})
	t.Run("ends the program's own children", func(t *testing.T) {
		p := startCmd(t, "/c", "ping -n 60 127.0.0.1")
		readUntil(t, p, "127.0.0.1", 15*time.Second)
		child := childNamed(t, uint32(p.process.Pid), "PING.EXE")
		defer func() { _ = windows.CloseHandle(child) }()
		closeWithin(t, p, 15*time.Second)
		event, err := windows.WaitForSingleObject(child, 10000)
		require.NoError(t, err)
		assert.Equal(t, uint32(windows.WAIT_OBJECT_0), event, "ping.exe outlived Close")
	})
	t.Run("an interactive cmd nobody reads", func(t *testing.T) {
		p := startCmd(t)
		closeWithin(t, p, 15*time.Second)
		waitWithin(t, p, 5*time.Second)
	})
	t.Run("resize after exit, before Close", func(t *testing.T) {
		p := startCmd(t, "/c", "exit 0")
		_ = readAll(t, p)
		waitWithin(t, p, 10*time.Second)
		assert.NoError(t, p.Resize(90, 30))
	})
}

// childNamed opens the child of parent whose image is name, with SYNCHRONIZE access.
func childNamed(t *testing.T, parent uint32, name string) windows.Handle {
	t.Helper()
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(snap) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if entry.ParentProcessID == parent && strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, entry.ProcessID)
			require.NoError(t, err)
			return h
		}
	}
	require.FailNow(t, "no child "+name+" of the process")
	return 0
}

func TestEnvBlock(t *testing.T) {
	block, err := envBlock([]string{"A=1", "B=2"})
	require.NoError(t, err)
	assert.Equal(t, append(windows.StringToUTF16("A=1"), windows.StringToUTF16("B=2")...), append(block[:0:0], block[:len(block)-1]...))
	empty, err := envBlock(nil)
	require.NoError(t, err)
	assert.Equal(t, []uint16{0, 0}, empty)
	_, err = envBlock([]string{"A=\x00"})
	require.Error(t, err)
}

func TestWithSystemRoot(t *testing.T) {
	assert.Equal(t, []string{"SYSTEMROOT=C:\\Windows"}, withSystemRoot(nil, `C:\Windows`))
	assert.Equal(t, []string{"SystemRoot=D:\\W"}, withSystemRoot([]string{"SystemRoot=D:\\W"}, `C:\Windows`))
}

func TestConsoleSize(t *testing.T) {
	assert.Equal(t, windows.Coord{X: 1, Y: 1}, consoleSize(0, -5))
	assert.Equal(t, windows.Coord{X: 120, Y: 40}, consoleSize(120, 40))
	assert.Equal(t, windows.Coord{X: 32767, Y: 32767}, consoleSize(1<<20, 1<<20))
}

func TestDedupEnv(t *testing.T) {
	assert.Equal(t, []string{"b=2", "Path=C:\\new", "=C:=C:\\ws"}, dedupEnv([]string{"PATH=C:\\old", "b=2", "Path=C:\\new", "=C:=C:\\ws"}))
	assert.Empty(t, dedupEnv(nil))
}
```

`echo mark^er` makes cmd print `marker` while the echoed input line holds
`mark^er`, so the test sees the program's output, not the console's echo.
`readUntil` closes `p` on timeout so its reader goroutine ends.

### Mutations (run each on a scratch copy; report which tests failed)

These can only be *run* in CI. Implementer: apply each mutation, confirm the
package still compiles for Windows (so the test would run), revert, and list
in the report which test each is meant to fail. The orchestrator or a
reviewer may push one mutation to a throwaway branch to see CI fail.

| # | Mutation | Must fail |
| --- | --- | --- |
| M1 | delete `closeHandles(inRead, outWrite)` after `CreatePseudoConsole` | "output, exit code 0 and io.EOF" (no EOF, times out) |
| M2 | delete `p.closeConsole()` in `wait` | the same, and "the exit code is returned" |
| M3 | `Resize` returns `nil` without calling `ResizePseudoConsole` | "resize is seen by the program" |
| M4 | delete `p.drain()` in `shutdown` | "an interactive cmd nobody reads" may hang on older Windows: expect a pass on Server 2022; report it |
| M5 | `coordField` returns `int16(n)` | `TestConsoleSize` |
| M6 | `withSystemRoot` returns `env` unchanged | "a nil environment holds only SYSTEMROOT", `TestWithSystemRoot` |
| M7 | `dedupEnv` returns `env` | `TestDedupEnv` |
| M8 | delete `si.Flags = windows.STARTF_USESTDHANDLES` | "output, exit code 0 and io.EOF" (output goes to go test's own stdout); if it passes, report it |

### Acceptance

- Windows: `Supported()` true on windows-latest; every test above passes in
  CI's `windows` job (T5 wires it); no test can hang (each wait has a bound).
- Unix files untouched by this task; unix tests green.

### Verify

```bash
go test -p 2 -count=1 ./internal/ptyproc ./internal/envagent
for a in amd64 arm64; do GOOS=windows GOARCH=$a go vet ./internal/ptyproc ./internal/envagent && GOOS=windows GOARCH=$a go test -c -o /dev/null ./internal/ptyproc || exit 1; done
L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./internal/ptyproc/... ./internal/envagent/...
go tool golangci-lint run ./internal/ptyproc/...
```

Not run here: every test in `ptyproc_windows_test.go` and
`supported_test.go` on Windows (CI only); `ptyproc_windows.go` is
compile-checked only.

---

## T3. `claude.Send` over a terminal abstraction, ConPTY on Windows

Depends on: T1 (compiles against the T1 stub; its Windows test passes only
with T2). Parallel with T2, T4, T6. Constraints rows: "Code today"
`send.go`, `pty_unix.go`, `pty_other.go`, `send_test.go`; pinned ADR 0024
grep, `forbidigo`, `env_agent_sessions_test.go:268-275`. Design: D7.

### Files and changes

- `internal/claude/send.go`: apply this diff (it removes the `os` and
  `os/exec` imports, adds `io`; `ptyRows`/`ptyCols` move here from
  `pty_unix.go`; `Client.hangUp` moves to `pty_unix.go` as
  `(*unixAttach).hangUp`):

```diff
--- a/internal/claude/send.go
+++ b/internal/claude/send.go
@@ -5,9 +5,8 @@ import (
 	"context"
 	"errors"
 	"fmt"
+	"io"
 	"log/slog"
-	"os"
-	"os/exec"
 	"time"
 )
 
@@ -15,6 +14,22 @@ import (
 // its transcript in time (ADR 0047).
 var ErrNotDelivered = errors.New("Claude Code didn't record the message") //nolint:staticcheck // ST1005: "Claude Code" is a name.
 
+// ptyRows and ptyCols are the terminal's size: room for Claude Code to
+// draw its prompt.
+const (
+	ptyRows = 40
+	ptyCols = 120
+)
+
+// attachTerm is claude attach running on a terminal: Read is what it draws,
+// Write types into it.
+type attachTerm interface {
+	io.ReadWriter
+	// hangUp closes the terminal, which detaches claude attach, and waits
+	// for it to exit, killing it if it takes longer than grace.
+	hangUp(ctx context.Context, log *slog.Logger, grace time.Duration)
+}
+
 // promptGlyph is what Claude Code draws at its prompt; once it's on the
 // terminal, typed text reaches the prompt (ADR 0047).
 const promptGlyph = "❯"
@@ -59,32 +74,18 @@ func (c *Client) Send(ctx context.Context, s *Session, message string) error {
 	if err := c.ensureVersion(ctx); err != nil {
 		return err
 	}
-	control, tty, err := openPTY()
-	if err != nil {
-		return fmt.Errorf("opening a terminal for claude attach %s: %w", s.ID, err)
-	}
-	//nolint:gosec // G204: running claude with arguments is this package's purpose.
-	cmd := exec.CommandContext(ctx, c.path, "attach", s.ID)
-	cmd.Env = append(append([]string{}, c.environ...), "TERM=xterm-256color")
-	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
-	cmd.SysProcAttr = attachProcAttr()
-	err = cmd.Start()
-	tty.Close()
+	term, err := c.startAttach(ctx, s.ID)
 	if err != nil {
-		control.Close()
-		if notInstalled(err) {
-			return fmt.Errorf("%w: %w", ErrNotInstalled, err)
-		}
-		return fmt.Errorf("running claude attach %s: %w", s.ID, err)
+		return err
 	}
 	c.log.DebugContext(ctx, "claude", slog.Any("args", []string{"attach", s.ID}), slog.String("via", "pty"))
-	defer c.hangUp(ctx, cmd, control)
-	return c.typeMessage(ctx, s, control, message)
+	defer term.hangUp(ctx, c.log, c.sendTimings.hangUp)
+	return c.typeMessage(ctx, s, term, message)
 }
 
 // typeMessage waits for the prompt on control, types message and Enter,
 // and waits for s's transcript to record it.
-func (c *Client) typeMessage(ctx context.Context, s *Session, control *os.File, message string) error {
+func (c *Client) typeMessage(ctx context.Context, s *Session, control io.ReadWriter, message string) error {
 	t := c.sendTimings
 	select {
 	case <-watchPrompt(control):
@@ -98,7 +99,7 @@ func (c *Client) typeMessage(ctx context.Context, s *Session, control *os.File,
 		if err := pause(ctx, t.settle); err != nil {
 			return err
 		}
-		if _, err := control.WriteString(step); err != nil {
+		if _, err := io.WriteString(control, step); err != nil {
 			return fmt.Errorf("typing into claude attach %s: %w", s.ID, err)
 		}
 	}
@@ -123,7 +124,7 @@ func (c *Client) typeMessage(ctx context.Context, s *Session, control *os.File,
 // watchPrompt reads everything claude attach draws on control, and closes
 // the channel it returns once the prompt glyph has been drawn. It keeps
 // reading, so the terminal never fills, until control is closed.
-func watchPrompt(control *os.File) <-chan struct{} {
+func watchPrompt(control io.Reader) <-chan struct{} {
 	drawn := make(chan struct{})
 	go func() {
 		var seen []byte
@@ -146,22 +147,6 @@ func watchPrompt(control *os.File) <-chan struct{} {
 	return drawn
 }
 
-// hangUp closes the terminal, which detaches claude attach, and waits for
-// it to exit, killing it if it takes longer than the hang-up timing.
-func (c *Client) hangUp(ctx context.Context, cmd *exec.Cmd, control *os.File) {
-	control.Close()
-	done := make(chan struct{})
-	go func() { _ = cmd.Wait(); close(done) }()
-	select {
-	case <-done:
-		return
-	case <-time.After(c.sendTimings.hangUp):
-	}
-	c.log.DebugContext(ctx, "claude attach didn't exit on hang-up; killing it")
-	_ = cmd.Process.Kill()
-	<-done
-}
-
 // pause waits for d, or until ctx is done.
 func pause(ctx context.Context, d time.Duration) error {
 	t := time.NewTimer(d)
```

- `internal/claude/pty_unix.go`: apply this diff (the moved code, verbatim
  apart from `s.ID`→`id`, `return …`→`return nil, …`, and the receiver):

```diff
--- a/internal/claude/pty_unix.go
+++ b/internal/claude/pty_unix.go
@@ -3,9 +3,13 @@
 package claude
 
 import (
+	"context"
 	"fmt"
+	"log/slog"
 	"os"
+	"os/exec"
 	"syscall"
+	"time"
 
 	"golang.org/x/sys/unix"
 )
@@ -15,13 +19,6 @@ func attachProcAttr() *syscall.SysProcAttr {
 	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
 }
 
-// ptyRows and ptyCols are the terminal's size: room for Claude Code to
-// draw its prompt.
-const (
-	ptyRows = 40
-	ptyCols = 120
-)
-
 // ptyControl runs fn on control's descriptor without taking it out of
 // non-blocking mode, so closing control still ends a Read in progress.
 func ptyControl(control *os.File, fn func(fd int) error) error {
@@ -51,3 +48,48 @@ func openTTY(control *os.File, name string) (*os.File, *os.File, error) {
 	}
 	return control, tty, nil
 }
+
+// unixAttach is claude attach on a pty: the process and the pty's controlling side.
+type unixAttach struct {
+	*os.File
+	cmd *exec.Cmd
+}
+
+// startAttach runs claude attach id on a new pty.
+func (c *Client) startAttach(ctx context.Context, id string) (attachTerm, error) {
+	control, tty, err := openPTY()
+	if err != nil {
+		return nil, fmt.Errorf("opening a terminal for claude attach %s: %w", id, err)
+	}
+	//nolint:gosec // G204: running claude with arguments is this package's purpose.
+	cmd := exec.CommandContext(ctx, c.path, "attach", id)
+	cmd.Env = append(append([]string{}, c.environ...), "TERM=xterm-256color")
+	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
+	cmd.SysProcAttr = attachProcAttr()
+	err = cmd.Start()
+	tty.Close()
+	if err != nil {
+		control.Close()
+		if notInstalled(err) {
+			return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
+		}
+		return nil, fmt.Errorf("running claude attach %s: %w", id, err)
+	}
+	return &unixAttach{File: control, cmd: cmd}, nil
+}
+
+// hangUp closes the terminal, which detaches claude attach, and waits for
+// it to exit, killing it if it takes longer than the hang-up timing.
+func (a *unixAttach) hangUp(ctx context.Context, log *slog.Logger, grace time.Duration) {
+	a.Close()
+	done := make(chan struct{})
+	go func() { _ = a.cmd.Wait(); close(done) }()
+	select {
+	case <-done:
+		return
+	case <-time.After(grace):
+	}
+	log.DebugContext(ctx, "claude attach didn't exit on hang-up; killing it")
+	_ = a.cmd.Process.Kill()
+	<-done
+}
```

- `internal/claude/pty_other.go`: tag `//go:build !darwin && !linux && !windows`; whole file:

```go
//go:build !darwin && !linux && !windows

package claude

import (
	"context"
	"errors"
	"fmt"
)

// startAttach fails: this system has no pseudo-terminals.
func (c *Client) startAttach(_ context.Context, id string) (attachTerm, error) {
	return nil, fmt.Errorf("opening a terminal for claude attach %s: %w", id, errors.ErrUnsupported)
}
```

- `internal/claude/pty_windows.go` (new):

```go
package claude

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"time"

	"github.com/bobcob7/workspaces/internal/ptyproc"
)

// windowsAttach is claude attach on a pseudoconsole.
type windowsAttach struct {
	*ptyproc.Proc
}

// startAttach runs claude attach id on a new pseudoconsole (ConPTY, ADR 0085).
func (c *Client) startAttach(_ context.Context, id string) (attachTerm, error) {
	path, err := exec.LookPath(c.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
	}
	proc, err := ptyproc.Start(ptyproc.Spec{
		Path: path,
		Args: []string{"attach", id},
		Env:  append(append([]string{}, c.environ...), "TERM=xterm-256color"),
		Cols: ptyCols,
		Rows: ptyRows,
	})
	if err != nil {
		if notInstalled(err) {
			return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return nil, fmt.Errorf("running claude attach %s: %w", id, err)
	}
	return windowsAttach{Proc: proc}, nil
}

// hangUp closes the pseudoconsole, which ends claude attach (Close kills it
// after its own grace period, 2 s), and waits for it to exit.
func (a windowsAttach) hangUp(ctx context.Context, log *slog.Logger, _ time.Duration) {
	if err := a.Close(); err != nil {
		log.DebugContext(ctx, "claude attach's terminal didn't close cleanly", slog.Any("error", err))
	}
	a.Wait()
}
```

- `internal/claude/pty_windows_test.go` (new). The `#!/bin/sh` fakes can't
  run on Windows, so the test binary acts as `claude` when
  `WS_TEST_FAKE_CLAUDE=1` (a `TestMain`; the package has none today):

```go
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClaudeEnv makes this test binary act as claude (fakeClaudeMain).
const fakeClaudeEnv = "WS_TEST_FAKE_CLAUDE"

// TestMain runs the tests, or, when fakeClaudeEnv is set, acts as claude:
// Windows can't run the #!/bin/sh fakes the other tests use.
func TestMain(m *testing.M) {
	if os.Getenv(fakeClaudeEnv) == "1" {
		os.Exit(fakeClaudeMain(os.Stdin, os.Stdout, os.Args[1:])) //nolint:forbidigo // A test binary run as claude exits with the fake's code.
	}
	os.Exit(m.Run()) //nolint:forbidigo // TestMain exits with the tests' code.
}

// fakeClaudeMain is fakeAttach for Windows: --version prints a supported
// version; attach records its arguments to $ARGS, draws the prompt unless
// $NO_PROMPT is set, reads a line, appends it to $TRANSCRIPT as a user
// entry, and waits to be closed.
func fakeClaudeMain(stdin io.Reader, stdout io.Writer, args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintln(stdout, "2.1.303 (Claude Code)")
		return 0
	}
	if err := os.WriteFile(os.Getenv("ARGS"), []byte(strings.Join(args, "\n")+"\n"), 0o600); err != nil {
		return 2
	}
	if os.Getenv("NO_PROMPT") == "" {
		_, _ = fmt.Fprint(stdout, promptGlyph+" ")
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil {
		return 3
	}
	entry, err := json.Marshal(map[string]any{"type": "user", "timestamp": time.Now().UTC().Format(time.RFC3339), "message": map[string]string{"content": strings.TrimRight(line, "\r\n")}})
	if err != nil {
		return 4
	}
	f, err := os.OpenFile(os.Getenv("TRANSCRIPT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 5
	}
	_, _ = f.Write(append(entry, '\n'))
	_ = f.Close()
	time.Sleep(30 * time.Second)
	return 0
}

// windowsSendClient is a Client whose claude is this test binary acting as
// fakeClaudeMain, the session it sends to, and its recorded arguments.
func windowsSendClient(t *testing.T, extra ...string) (c *Client, s *Session, args func() []string) {
	t.Helper()
	exe, err := os.Executable()
	require.NoError(t, err)
	cfg := t.TempDir()
	s = &Session{ID: "d12c2de1", SessionID: "d12c2de1-edad-4531-93c1-885ee54e3448", Cwd: `C:\w\x`}
	transcript := filepath.Join(cfg, "projects", "C--w-x", s.SessionID+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(transcript), 0o700))
	argsEnv, args := argsFile(t)
	env := append(os.Environ(), argsEnv...)
	env = append(env, fakeClaudeEnv+"=1", "CLAUDE_CONFIG_DIR="+cfg, "TRANSCRIPT="+transcript)
	c = New(discardLogger(), exe, append(env, extra...))
	c.sendTimings = fastTimings
	return c, s, args
}

func TestClient_Send_windows(t *testing.T) {
	t.Run("types the message at the prompt and returns once it's recorded", func(t *testing.T) {
		c, s, args := windowsSendClient(t)
		c.sendTimings.prompt = time.Minute
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		started := time.Now()
		require.NoError(t, c.Send(ctx, s, testMessage))
		assert.Equal(t, []string{"attach", "d12c2de1"}, args())
		assert.Less(t, time.Since(started), 25*time.Second, "hang-up didn't end the fake's 30 s sleep")
	})
	t.Run("a message that isn't recorded is ErrNotDelivered", func(t *testing.T) {
		c, s, _ := windowsSendClient(t, "TRANSCRIPT="+filepath.Join(t.TempDir(), "elsewhere.jsonl"))
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		require.ErrorIs(t, c.Send(ctx, s, testMessage), ErrNotDelivered)
	})
	t.Run("a missing claude is ErrNotInstalled", func(t *testing.T) {
		c := New(discardLogger(), filepath.Join(t.TempDir(), "claude.exe"), os.Environ())
		c.checked = true
		s := &Session{ID: "d12c2de1"}
		require.ErrorIs(t, c.Send(t.Context(), s, testMessage), ErrNotInstalled)
	})
}
```

### Mutations

| # | Mutation | Must fail |
| --- | --- | --- |
| M1 | Windows `hangUp` calls only `a.Wait()`, no `Close` | "types the message…" (`Wait` sits out the fake's 30 s sleep; the 25 s bound fails) |
| M2 | `startAttach` (Windows) returns a plain error when `exec.LookPath` fails | "a missing claude is ErrNotInstalled" |
| M3 | unix: `typeMessage` writes `step` twice | `TestClient_Send` (unix, run here) |

Run M3 here; M1 and M2 compile-check only (CI runs them).

### Acceptance

- Unix `go test ./internal/claude` passes unchanged (all of `send_test.go`).
- `pty_unix.go`'s `startAttach` body equals the removed `Send` lines, with
  only the changes named above (`git diff -M` review).
- `make lint` passes (ADR 0024 grep included).

### Verify

```bash
go test -p 2 -count=1 ./internal/claude ./internal/cli
for a in amd64 arm64; do GOOS=windows GOARCH=$a go vet ./internal/claude && GOOS=windows GOARCH=$a go test -c -o /dev/null ./internal/claude || exit 1; done
L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./internal/claude/...
make lint
```

Not run here: `TestClient_Send_windows` (CI only, and only once T2 is in);
`pty_windows.go` is compile-checked only; `claude attach` on a real Windows
machine (a person, design.md step 5).

---

## T4. Terminal environment, home directory and refusal messages on Windows

Depends on: T1. Parallel with T2, T3, T6. Constraints rows: "Code today"
`env_agent_terminal_windows.go`, `env_agent_terminal.go:107-135`,
`terminals.go:223-247`, `env_agent_sessions.go:179`; pinned
`terminals_test.go:772-800`, error-code tables (unchanged). Design: D6, D8.

### Files and changes

| File | Change |
| --- | --- |
| `internal/cli/env_agent_terminal_windows.go` | append to `windowsTerminalEnv`: `"ALLUSERSPROFILE", "PUBLIC", "COMMONPROGRAMFILES(X86)", "COMMONPROGRAMW6432", "PROCESSOR_IDENTIFIER", "PROCESSOR_LEVEL", "PROCESSOR_REVISION", "PSMODULEPATH"`; add `homeOf` (below) |
| `internal/cli/env_agent_terminal.go` | **remove** `homeOf` (lines 131-139 today) |
| `internal/cli/env_agent_terminal_unix.go` | add today's `homeOf`, verbatim (it needs `"strings"`) |
| `internal/cli/env_agent_terminal_windows_test.go` | `TestTerminalEnvAllowed_windows` gains the 8 names in mixed case (`AllUsersProfile`, `PSModulePath`, `CommonProgramFiles(x86)`…); new `TestHomeOf_windows` (below); `TestAgentOperations_TerminalCommand_windows` gains "shell uses USERPROFILE when HOME is unset" |
| `internal/envagent/terminals.go` | `errPlatformUnsupported`'s doc: "refuses a terminal on a system without pseudo-terminals (ADR 0085: any OS but linux, darwin and Windows 10 1809+)"; message `"terminals aren't supported on " + runtime.GOOS + " agents: no pseudo-terminals (ADR 0085)"` |
| `internal/cli/env_agent_sessions.go` | message `"sending to an agent isn't supported on " + runtime.GOOS + " environments: no pseudo-terminals (ADR 0085)"` |
| `internal/envagent/terminals_windows_test.go` (new) | real-ConPTY tests through the terminal manager (below) |

```go
// homeOf is the home directory in env: HOME, else USERPROFILE (Windows
// rarely sets HOME), either key in any case; "" when neither is set.
func homeOf(env []string) string {
	for _, name := range []string{"HOME", "USERPROFILE"} {
		if home := lastEnv(env, name); home != "" {
			return home
		}
	}
	return ""
}

// lastEnv is the last non-empty value of name in env, matched without regard to case.
func lastEnv(env []string, name string) string {
	value := ""
	for _, kv := range env {
		if key, v, ok := strings.Cut(kv, "="); ok && strings.EqualFold(key, name) && v != "" {
			value = v
		}
	}
	return value
}
```

`TestHomeOf_windows`: `HOME=C:\h` wins over `USERPROFILE=C:\u`;
`UserProfile=C:\u` alone gives `C:\u`; `HOME=` (empty) with
`USERPROFILE=C:\u` gives `C:\u`; neither gives `""`.

Shell-command test (in `TestAgentOperations_TerminalCommand_windows`): with
`ops.app.environ = []string{"Path=C:\\Windows\\System32", "USERPROFILE=" + dir}`
and `envproto.TermShell` args, `TerminalCommand` succeeds and `cmd.Dir == dir`.
Build the ops with `terminalOps(t, claudeSession())` and the args with
`shellArgs()` (both in `env_agent_terminal_test.go`), then overwrite
`ops.app.environ`.

`internal/envagent/terminals_windows_test.go`:

```go
package envagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// conptyHarness is a termHarness whose terminals run cmd.exe with args on a
// real pseudoconsole (ptyproc on Windows).
func conptyHarness(t *testing.T, args ...string) *termHarness {
	t.Helper()
	h := newTermHarness(t)
	h.m.starter = ptyprocStarter{}
	cmdExe := filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	h.m.commands = &terminalCommandsMock{TerminalCommandFunc: func(context.Context, *envproto.TerminalOpenArgs) (TerminalCommand, error) {
		return TerminalCommand{Path: cmdExe, Args: args, Dir: t.TempDir(), Env: os.Environ()}, nil
	}}
	return h
}

// openReal opens a terminal on h and returns its sid.
func openReal(t *testing.T, h *termHarness) string {
	t.Helper()
	raw, err := h.m.open(t.Context(), attachArgs())
	require.NoError(t, err)
	var result envproto.TerminalOpenResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result.SID
}

// output is the data frames' bodies for sid so far, joined.
func output(h *termHarness, sid string) string {
	var out strings.Builder
	for _, f := range h.frames(sid) {
		if f.Kind == envproto.FrameData {
			out.Write(f.Body)
		}
	}
	return out.String()
}

func TestTerminals_conpty(t *testing.T) {
	t.Run("a program that exits sends its output and an exited frame", func(t *testing.T) {
		h := conptyHarness(t, "/c", "echo hello& exit 4")
		sid := openReal(t, h)
		exit := h.exit(sid)
		assert.Equal(t, reasonExited, exit.Reason)
		require.NotNil(t, exit.Code)
		assert.Equal(t, 4, *exit.Code)
		assert.Contains(t, output(h, sid), "hello")
	})
	t.Run("input reaches the program and close ends it", func(t *testing.T) {
		h := conptyHarness(t)
		sid := openReal(t, h)
		h.send(sid, envproto.FrameInput, 1, []byte("echo mark^er\r\n"))
		require.Eventually(t, func() bool { return strings.Contains(output(h, sid), "marker") }, 15*time.Second, 50*time.Millisecond)
		started := time.Now()
		h.send(sid, envproto.FrameClose, 2, nil)
		exit := h.exit(sid)
		assert.Equal(t, reasonClosed, exit.Reason)
		assert.Less(t, time.Since(started), 15*time.Second)
	})
}
```

### Mutations

| # | Mutation | Must fail | Where |
| --- | --- | --- | --- |
| M1 | Windows `homeOf` returns only `HOME` | `TestHomeOf_windows`, the shell-command test | CI |
| M2 | drop `PSMODULEPATH` from the list | `TestTerminalEnvAllowed_windows` | CI |
| M3 | `terminals.go`: `start` stops checking `m.supported()` | `TestTerminals_open` "an unsupported platform refuses before building the command" (unix, here) | here |

### Acceptance

- Unix tests unchanged and green (`internal/cli`, `internal/envagent`).
- No error code, protocol number or guide exit table changes.

### Verify

```bash
go test -p 2 -count=1 ./internal/envagent ./internal/cli
for a in amd64 arm64; do for p in ./internal/envagent ./internal/cli; do GOOS=windows GOARCH=$a go vet $p && GOOS=windows GOARCH=$a go test -c -o /dev/null $p || exit 1; done; done
L=$(GOOS= GOARCH= go tool -n golangci-lint); GOOS=windows "$L" run ./internal/envagent/... ./internal/cli/...
make lint
```

Not run here: `TestTerminalEnvAllowed_windows`, `TestHomeOf_windows`,
`TestAgentOperations_TerminalCommand_windows`, `TestTerminals_conpty`
(CI only; `TestTerminals_conpty` needs T2).

---

## T5. CI: Windows tests and a `ws.exe` artifact on every PR (`small`)

Depends on: T2, T3, T4 (names their tests). Parallel with T6. Constraints
rows: pinned `Makefile test-windows`, `ci.yml`, `dist`/`release.yml`.

### Files and changes

`Makefile`:

- `.PHONY` gains `windows-exe`.
- After `build:` add:

```make
# windows-exe builds ws.exe for one Windows architecture with the release
# flags, for trying a branch on Windows (ADR 0085):
# make windows-exe WINARCH=arm64 -> dist/windows-arm64/ws.exe
WINARCH ?= amd64
windows-exe:
	CGO_ENABLED=0 GOOS=windows GOARCH=$(WINARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o dist/windows-$(WINARCH)/ws.exe ./cmd/ws
```

- `WINDOWS_TESTS` gains `./internal/ptyproc`.
- In `test-windows`, replace the `TestUserHome ./internal/claude` line with
  `-run 'TestUserHome|TestClient_Send_windows' ./internal/claude`, the
  `TestHasPathPrefix ./internal/envagent` line with
  `-run 'TestHasPathPrefix|TestTerminals_conpty' ./internal/envagent`, and
  add `TestHomeOf_windows` to the `internal/cli` alternation
  (`'TestTerminalEnvAllowed_windows|TestExecutableNames|TestLookPathIn_windows|TestAgentOperations_TerminalCommand_windows|TestHomeOf_windows'`).
  Keep `|| rc=1; \` on every line.

`.github/workflows/ci.yml`: add after the `windows` job:

```yaml
  # ADR 0085: a ws.exe for every push and PR, to try the branch on Windows.
  # Download it from the run's Artifacts, or gh run download <id> -n ws-windows-amd64.
  windows-exe:
    strategy:
      matrix:
        arch: [amd64, arm64]
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0 # git describe needs the tags for main.version
      - uses: actions/setup-go@v7
        with:
          go-version: 1.27.x
          check-latest: true
      - run: make windows-exe WINARCH=${{ matrix.arch }}
      - uses: actions/upload-artifact@v4
        with:
          name: ws-windows-${{ matrix.arch }}
          path: dist/windows-${{ matrix.arch }}/ws.exe
          if-no-files-found: error
          retention-days: 14
```

and in the `windows` job, after `make test-windows`, nothing else changes
(the informational full suite stays). `release.yml`: unchanged (it calls
`ci.yml`, so a tag run also uploads the artifacts; the release itself still
ships `dist/*` from `make dist`): add nothing.

### Acceptance

- `make windows-exe` and `make windows-exe WINARCH=arm64` here produce
  `dist/windows-amd64/ws.exe` and `dist/windows-arm64/ws.exe`;
  `file` says `PE32+ executable … x86-64` and `… Aarch64`;
  `make dist VERSION=test` still produces exactly the four unix binaries,
  two zips and `SHA256SUMS` (no `dist/windows-*` dirs: `dist` starts with
  `rm -rf dist`).
- The YAML parses. The CI jobs `windows`, `windows-cross` and `windows-exe`
  are **only proven on the PR**: the orchestrator must read the `windows`
  job's `make test-windows` output (every new test listed as run, none
  skipped as "no tests to run" — check with `go test -v` in the log if
  unsure) and see both artifacts in the run's summary.

### Verify

```bash
make windows-exe && make windows-exe WINARCH=arm64 && file dist/windows-*/ws.exe
make dist VERSION=test && ls dist && grep -c ws- dist/SHA256SUMS
mkdir -p _yamlcheck && cat > _yamlcheck/main.go <<'GO'
package main

import (
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

func main() {
	for _, f := range os.Args[1:] {
		data, err := os.ReadFile(f)
		if err == nil {
			var v any
			err = yaml.Unmarshal(data, &v)
		}
		if err != nil {
			fmt.Println(f, err)
			os.Exit(1)
		}
		fmt.Println(f, "ok")
	}
}
GO
go run ./_yamlcheck .github/workflows/ci.yml .github/workflows/release.yml; rc=$?; rm -rf _yamlcheck dist; test $rc = 0
make -n test-windows | grep -E 'ptyproc|TestClient_Send_windows|TestTerminals_conpty|TestHomeOf_windows'
```

(`_yamlcheck` is a scratch dir: never commit it; `git status` must be clean
of it.)

Not run here: the `windows`, `windows-cross` and `windows-exe` jobs and the
artifact upload (CI only).

---

## T6. ADR 0085 and the docs (`small`, docs only)

Depends on: none (no code). Parallel with T1-T5. Constraints: pinned ADR index row. Design: all of D1-D10,
"Rejected", "Risks", "How to test on Windows".

### Files and changes

| File | Change |
| --- | --- |
| `docs/adrs/0085-windows-conpty-terminals.md` (new, from `docs/adrs/template.md`) | Title: "`terminal.open` and `agent.send` run on ConPTY on Windows: one `ptyproc.Proc` API, the console closed on exit and on Close, no job object; `platform_unsupported` only without pseudo-terminals". `Status: Proposed`, `Date: 2026-11-27`. Context (problem: 0081 D5 refused; criteria: same API and behaviour as unix, Close never hangs, nothing outlives a hang-up that wouldn't on unix, provable in CI; facts: the ConPTY pitfalls with citations, below). Decision: design.md D1-D9 condensed, saying where each is weak. Alternatives: design.md "Rejected". Consequences: supersedes 0081 D5; amends 0076's Windows amendment; no new error code (0016 unchanged); new CI job `windows-exe`; unverified items from design.md "Risks"; follow-up: a Windows controller (D9). Footnote citations (accessed 2026-11-27): Microsoft, "Creating a Pseudoconsole session" <https://learn.microsoft.com/windows/console/creating-a-pseudoconsole-session>; "ClosePseudoConsole function" <https://learn.microsoft.com/windows/console/closepseudoconsole>; "CreateProcessW", `STARTF_USESTDHANDLES` <https://learn.microsoft.com/windows/win32/api/processthreadsapi/nf-processthreadsapi-createprocessw>; libuv `src/win/process.c` on `CREATE_BREAKAWAY_FROM_JOB` for detached children; Go `internal/poll/fd_windows.go` (`ERROR_BROKEN_PIPE`→`io.EOF`, `CancelIoEx` on pipe `Close`). Keep it to two screens |
| `docs/adrs/0081-windows-environment-agent.md` | `- Status: Proposed` → `- Status: Proposed; D5 superseded by [0085](0085-windows-conpty-terminals.md)` (nothing else; ADRs are immutable apart from status lines) |
| `docs/adrs/0076-environment-platforms-and-phasing.md` | in "## Amendments (ADR 0081, Windows)", the first bullet gains: "[0085](0085-windows-conpty-terminals.md) then built terminals and `agent.send` on ConPTY; they refuse only on systems without pseudo-terminals." Status line: append "; Windows terminals by [0085](0085-windows-conpty-terminals.md) (proposed)" |
| `docs/adrs/README.md` | index row for 0085 after 0084 (one-sentence summary as in the title), status `Proposed`; 0081's row: replace "terminals and `agent.send` refuse with `platform_unsupported` until ConPTY" with "terminals and `agent.send` on ConPTY since [0085](0085-windows-conpty-terminals.md)" and its status cell becomes "Proposed; D5 superseded by [0085](0085-windows-conpty-terminals.md)" |
| `internal/cli/guides/ws-attach.md:51`, `docs/tools/ws-attach.md:55` | row text → "The environment's OS has no pseudo-terminals (not Linux, macOS, or Windows 10 1809+)" |
| `internal/cli/guides/ws-shell.md:60`, `docs/tools/ws-shell.md:107` | "(exit 1, when the environment's OS has no pty support, today Windows)" → "(exit 1, when the environment's OS has no pseudo-terminals: not Linux, macOS, or Windows 10 1809+)"; add one sentence: "On a Windows environment the shell is `%COMSPEC%` (normally `cmd.exe`) and starts in the agent user's home (`USERPROFILE` when `HOME` is unset)." Mind the guide's line wrapping |
| `internal/cli/guides/ws-claude.md:141`, `docs/tools/ws-claude.md:223` | "The environment's OS has no pty support (today Windows), so `agent.send` is refused" → "The environment's OS has no pseudo-terminals (not Linux, macOS, or Windows 10 1809+), so `agent.send` is refused" |
| `docs/tools/ws-env.md:183-190, 430` | replace "Terminals and `agent.send` are refused … until ConPTY is built. WSL2 is the alternative…" with "Terminals and `agent.send` run on ConPTY (Windows 10 1809 or later; ADR 0085). `ws attach` and `ws shell --env` work from a Linux or macOS controller; a Windows controller can't attach yet. WSL2 remains an alternative." Drop the "`claude attach` in a terminal, since ConPTY isn't built;" bullet and add "`claude attach` on native Windows in a remote terminal (built on ConPTY, tested in CI only with cmd.exe and a fake claude);". Line 430: "(today Windows)" → "(not Linux, macOS, or Windows 10 1809+)" and "(ADR 0081)" → "(ADR 0081, 0085)". Add a "### Trying a branch build on Windows" subsection with design.md's artifact bullets |
| `README.md:293-298` (roadmap) | the Windows line: "`ws env agent run` built natively for Windows, with terminals and `agent.send` on ConPTY ([0085](docs/adrs/0085-windows-conpty-terminals.md))"; keep "not yet run on a Windows machine with Claude Code" |
| `docs/design/env-windows/tasks.md` "Not in these tasks" | first bullet gains "Done in [windows-conpty](../windows-conpty/tasks.md) (ADR 0085)." |
| `docs/design/env-windows/constraints.md` | in "Cannot be verified at all", the ConPTY bullet → "ConPTY with `claude attach` (built by ADR 0085; CI tests cmd.exe and a fake claude only);" |

`grep -rn "today Windows\|until ConPTY\|ConPTY isn't built" --include=*.md . | grep -v docs/design/windows-conpty`
must print nothing afterwards (ADR 0081's own text excepted: it is immutable;
filter it with `| grep -v 0081-windows`).

### Acceptance

- `go test -count=1 ./internal/cli -run 'TestGuideTopics|TestGuide'` passes
  (guides are embedded; codes unchanged).
- ADR 0085 follows `docs/adrs/README.md` "Writing each section"; every
  claim about Windows behaviour has a citation or says "unverified".

### Verify

```bash
go test -p 2 -count=1 -run 'TestGuide' ./internal/cli
grep -rn "today Windows\|until ConPTY\|ConPTY isn't built" --include=*.md . | grep -v 'docs/design/windows-conpty\|0081-windows' ; test $? = 1
grep -n 0085 docs/adrs/README.md docs/adrs/0081-windows-environment-agent.md docs/adrs/0076-environment-platforms-and-phasing.md
```

Not run here: nothing (docs only).
