//go:build darwin || linux

package claude

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ptyRows and ptyCols are the terminal's size: room for Claude Code to
// draw its prompt.
const (
	ptyRows = 40
	ptyCols = 120
)

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

// openTTY opens the terminal named name and sets its size.
func openTTY(control *os.File, name string) (*os.File, *os.File, error) {
	tty, err := os.OpenFile(name, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		control.Close()
		return nil, nil, fmt.Errorf("opening the terminal %s: %w", name, err)
	}
	size := &unix.Winsize{Row: ptyRows, Col: ptyCols}
	if err := ptyControl(control, func(fd int) error { return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, size) }); err != nil {
		control.Close()
		tty.Close()
		return nil, nil, fmt.Errorf("sizing the terminal: %w", err)
	}
	return control, tty, nil
}
