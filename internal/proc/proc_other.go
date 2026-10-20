//go:build !darwin && !linux

package proc

import (
	"errors"
	"time"
)

func startTime(int) (time.Time, error) {
	return time.Time{}, errors.ErrUnsupported
}
