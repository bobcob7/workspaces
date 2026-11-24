package lockfile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock takes f's lock without waiting. held is true, with a nil error,
// when another holder has it, which LOCKFILE_FAIL_IMMEDIATELY reports as
// ERROR_LOCK_VIOLATION. Any other failure, including ERROR_IO_PENDING, is
// returned as an error.
func tryLock(f *os.File, m mode) (held bool, err error) {
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if m == exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	err = windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, ^uint32(0), ^uint32(0), &windows.Overlapped{})
	if err == nil {
		return false, nil
	}
	if busy(err) {
		return true, nil
	}
	return false, err
}

// busy reports whether a failed LockFileEx means another holder has the lock.
func busy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
