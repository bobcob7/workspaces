package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes f's lock without waiting. held is true, with a nil error,
// when another holder has it.
func tryLock(f *os.File, m mode) (held bool, err error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if m == exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
	if err == nil {
		return false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return true, nil
	}
	return false, err
}
