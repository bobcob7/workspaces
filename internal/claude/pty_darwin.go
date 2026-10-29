package claude

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal and returns its controlling side and
// the terminal a child runs on: posix_openpt, grantpt, unlockpt, and
// ptsname as macOS implements them, with ioctls on /dev/ptmx.
func openPTY() (control, tty *os.File, err error) {
	control, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var name string
	if err := ptyControl(control, func(fd int) error {
		if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
			return fmt.Errorf("granting the terminal: %w", err)
		}
		if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
			return fmt.Errorf("unlocking the terminal: %w", err)
		}
		var buf [128]byte
		//nolint:staticcheck // SA1019: x/sys/unix has no wrapper for TIOCPTYGNAME, which writes the terminal's name into buf.
		if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
			return fmt.Errorf("naming the terminal: %w", errno)
		}
		name = unix.ByteSliceToString(buf[:])
		return nil
	}); err != nil {
		control.Close()
		return nil, nil, err
	}
	return openTTY(control, name)
}
