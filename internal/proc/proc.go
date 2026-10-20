// Package proc reads when processes started from the operating system,
// without running another program (ADR 0018's stale sessions).
package proc

import (
	"errors"
	"fmt"
	"time"
)

var errBadPID = errors.New("invalid pid")

// Reader reads process start times from the operating system.
type Reader struct{}

// StartTime returns when process pid started.
func (Reader) StartTime(pid int) (time.Time, error) {
	if pid <= 0 {
		return time.Time{}, fmt.Errorf("%w: %d", errBadPID, pid)
	}
	return startTime(pid)
}
