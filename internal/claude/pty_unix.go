//go:build darwin || linux

package claude

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// attachProcAttr starts claude attach with the pty as its controlling terminal.
func attachProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true, Setctty: true}
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

// unixAttach is claude attach on a pty: the process and the pty's controlling side.
type unixAttach struct {
	*os.File
	cmd *exec.Cmd
}

// startAttach runs claude attach id on a new pty.
func (c *Client) startAttach(ctx context.Context, id string) (attachTerm, error) {
	control, tty, err := openPTY()
	if err != nil {
		return nil, fmt.Errorf("opening a terminal for claude attach %s: %w", id, err)
	}
	//nolint:gosec // G204: running claude with arguments is this package's purpose.
	cmd := exec.CommandContext(ctx, c.path, "attach", id)
	cmd.Env = append(append([]string{}, c.environ...), "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = tty, tty, tty
	cmd.SysProcAttr = attachProcAttr()
	err = cmd.Start()
	tty.Close()
	if err != nil {
		control.Close()
		if notInstalled(err) {
			return nil, fmt.Errorf("%w: %w", ErrNotInstalled, err)
		}
		return nil, fmt.Errorf("running claude attach %s: %w", id, err)
	}
	return &unixAttach{File: control, cmd: cmd}, nil
}

// hangUp closes the terminal, which detaches claude attach, and waits for
// it to exit, killing it if it takes longer than the hang-up timing.
func (a *unixAttach) hangUp(ctx context.Context, log *slog.Logger, grace time.Duration) {
	a.Close()
	done := make(chan struct{})
	go func() { _ = a.cmd.Wait(); close(done) }()
	select {
	case <-done:
		return
	case <-time.After(grace):
	}
	log.DebugContext(ctx, "claude attach didn't exit on hang-up; killing it")
	_ = a.cmd.Process.Kill()
	<-done
}
