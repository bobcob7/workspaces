//go:build linux || darwin

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

// killGrace is how long Close waits after SIGHUP before it sends SIGKILL.
const killGrace = 2 * time.Second

// Proc is a running process and its terminal's controlling side.
type Proc struct {
	control   *os.File
	cmd       *exec.Cmd
	done      chan struct{}
	exit      *int
	signal    func(pid int, sig syscall.Signal) error
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

// Write sends b to the process's input. It succeeds after the process has
// exited, until Close; after Close it returns an error.
func (p *Proc) Write(b []byte) (int, error) {
	return p.control.Write(b)
}

// Resize sets the terminal's size; the kernel sends the process SIGWINCH.
// It succeeds after the process has exited, until Close; after Close it
// returns an error.
func (p *Proc) Resize(cols, rows int) error {
	return setWinsize(p.control, cols, rows)
}

// Close sends SIGHUP to the process group, then SIGKILL after 2 s if it
// still runs, and closes the pty. It is safe to call more than once.
// Once the leader has been reaped, Close sends no signal at all, because its
// pid, and with it the process group id, may have been reused by another
// process. Background members the leader left in the group are not signalled.
func (p *Proc) Close() error {
	p.closeOnce.Do(func() { p.closeErr = p.shutdown() })
	return p.closeErr
}

// Wait waits for the process and returns its exit code, nil when a signal ended it.
func (p *Proc) Wait() *int {
	<-p.done
	return p.exit
}

func (p *Proc) shutdown() error {
	if !p.reaped() {
		pid := p.cmd.Process.Pid
		_ = p.signal(pid, syscall.SIGHUP)
		select {
		case <-p.done:
		case <-time.After(killGrace):
			if !p.reaped() {
				_ = p.signal(pid, syscall.SIGKILL)
			}
		}
	}
	return p.control.Close()
}

// reaped reports whether the process has been waited for, after which its
// pid may belong to another process and must not be signalled.
func (p *Proc) reaped() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}
