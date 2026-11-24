//go:build unix

package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes f's lock without waiting. held is true, with a nil error,
// when another holder has it.
func tryLock(f *os.File, m mode) (held bool, err error) {
	how := unix.LOCK_SH
	if m == exclusive {
		how = unix.LOCK_EX
	}
	err = unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
	if err == nil {
		return false, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}
