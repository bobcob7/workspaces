//go:build !linux && !darwin

package ptyproc

import (
	"os"
	"syscall"
)

// Start is unsupported on this system.
func Start(Spec) (*Proc, error) {
	return nil, ErrUnsupported
}

func setWinsize(*os.File, int, int) error {
	return ErrUnsupported
}

func signalGroup(int, syscall.Signal) error {
	return ErrUnsupported
}
