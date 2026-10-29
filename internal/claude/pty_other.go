//go:build !darwin && !linux

package claude

import (
	"errors"
	"os"
)

func openPTY() (control, tty *os.File, err error) {
	return nil, nil, errors.ErrUnsupported
}
