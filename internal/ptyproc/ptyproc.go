// Package ptyproc runs a process on a new pseudo-terminal.
package ptyproc

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ErrUnsupported is returned on systems without the pty ioctls.
var ErrUnsupported = errors.New("pseudo-terminals aren't supported on this system")

// killGrace is how long Close waits after SIGHUP before it sends SIGKILL.
const killGrace = 2 * time.Second

// Spec is what to run.
type Spec struct {
	Path       string   // absolute program path
	Args       []string // argv[1:]
	Dir        string
	Env        []string // the whole environment
	Cols, Rows int
}

// Proc is a running process and its terminal's controlling side.
type Proc struct {
	control   *os.File
	cmd       *exec.Cmd
	done      chan struct{}
	exit      *int
	closeOnce sync.Once
	closeErr  error
}

// Read reads the pty's output and returns io.EOF once the process has exited.
func (p *Proc) Read(b []byte) (int, error) {
	n, err := p.control.Read(b)
	if errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

// Write sends b to the process's input.
func (p *Proc) Write(b []byte) (int, error) {
	return p.control.Write(b)
}

// Resize sets the terminal's size; the kernel sends the process SIGWINCH.
func (p *Proc) Resize(cols, rows int) error {
	return setWinsize(p.control, cols, rows)
}

// Close sends SIGHUP to the process group, then SIGKILL after 2 s if it
// still runs, and closes the pty. It is safe to call more than once.
func (p *Proc) Close() error {
	p.closeOnce.Do(func() { p.closeErr = p.shutdown() })
	return p.closeErr
}

// Wait waits for the process and returns its exit code, nil when a signal ended it.
func (p *Proc) Wait() *int {
	<-p.done
	return p.exit
}

// wait reaps the process and records its exit code.
func (p *Proc) wait() {
	defer close(p.done)
	_ = p.cmd.Wait()
	code := p.cmd.ProcessState.ExitCode()
	if code < 0 {
		return
	}
	p.exit = &code
}

func (p *Proc) shutdown() error {
	pid := p.cmd.Process.Pid
	_ = signalGroup(pid, syscall.SIGHUP)
	select {
	case <-p.done:
	case <-time.After(killGrace):
		_ = signalGroup(pid, syscall.SIGKILL)
	}
	return p.control.Close()
}
