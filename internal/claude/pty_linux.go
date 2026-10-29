package claude

import (
	"fmt"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal and returns its controlling side and
// the terminal a child runs on, with /dev/ptmx and its ioctls.
func openPTY() (control, tty *os.File, err error) {
	control, err = os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var name string
	if err := ptyControl(control, func(fd int) error {
		n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
		if err != nil {
			return fmt.Errorf("naming the terminal: %w", err)
		}
		if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
			return fmt.Errorf("unlocking the terminal: %w", err)
		}
		name = "/dev/pts/" + strconv.Itoa(n)
		return nil
	}); err != nil {
		control.Close()
		return nil, nil, err
	}
	return openTTY(control, name)
}
