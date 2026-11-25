//go:build !darwin && !linux

package claude

import (
	"errors"
	"os"
	"syscall"
)

// attachProcAttr has no pty to attach to here: openPTY fails first.
func attachProcAttr() *syscall.SysProcAttr {
	return nil
}

func openPTY() (control, tty *os.File, err error) {
	return nil, nil, errors.ErrUnsupported
}
