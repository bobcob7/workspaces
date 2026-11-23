//go:build linux || darwin

package ptyproc

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// Start opens a pty sized Cols×Rows, starts the process as the session
// leader with the pty as its controlling terminal, and returns once it runs.
//
//nolint:gocritic // hugeParam: Spec is passed by value, as the package API states.
func Start(spec Spec) (*Proc, error) {
	control, tty, err := openPTY()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(context.Background(), spec.Path, spec.Args...) //nolint:gosec // the path and args come from TerminalCommand, built from validated fields.
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		control.Close()
		tty.Close()
		return nil, fmt.Errorf("starting %s: %w", spec.Path, err)
	}
	tty.Close()
	if err := setWinsize(control, spec.Cols, spec.Rows); err != nil {
		control.Close()
		return nil, err
	}
	p := &Proc{control: control, cmd: cmd, done: make(chan struct{})}
	go p.wait()
	return p, nil
}

// setWinsize sets the size of the terminal control is the controlling side of.
func setWinsize(control *os.File, cols, rows int) error {
	size := &unix.Winsize{Row: winsizeField(rows), Col: winsizeField(cols)}
	return ptyControl(control, func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, size) })
}

// winsizeField clamps a terminal dimension to the range a winsize holds.
func winsizeField(n int) uint16 {
	if n < 0 {
		return 0
	}
	if n > math.MaxUint16 {
		return math.MaxUint16
	}
	return uint16(n)
}

// signalGroup sends sig to the process group led by pid.
func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

// ptyControl runs fn on control's descriptor without taking it out of
// non-blocking mode, so closing control still ends a Read in progress.
func ptyControl(control *os.File, fn func(fd int) error) error {
	raw, err := control.SyscallConn()
	if err != nil {
		return err
	}
	var fnErr error
	if err := raw.Control(func(fd uintptr) { fnErr = fn(int(fd)) }); err != nil {
		return err
	}
	return fnErr
}

// openTTY opens the terminal named name.
func openTTY(control *os.File, name string) (*os.File, *os.File, error) {
	tty, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		control.Close()
		return nil, nil, fmt.Errorf("opening the terminal %s: %w", name, err)
	}
	return control, tty, nil
}
