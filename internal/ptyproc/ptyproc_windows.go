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
// which kernel32 has on Windows 10 1809 and Server 2019 and later (ADR 0086).
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
