package proc

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// errNoProcess means sysctl returned a different process, or none.
var errNoProcess = errors.New("no such process")

// startTime reads the process's kinfo_proc with sysctl kern.proc.pid.
func startTime(pid int) (time.Time, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading process %d: %w", pid, err)
	}
	if int(kp.Proc.P_pid) != pid {
		return time.Time{}, fmt.Errorf("%w: %d", errNoProcess, pid)
	}
	tv := kp.Proc.P_starttime
	return time.Unix(tv.Sec, int64(tv.Usec)*int64(time.Microsecond)), nil
}
