//go:build !linux && !darwin

package ptyproc

import "os"

// Supported reports whether this system has pseudo-terminals.
func Supported() bool { return false }

// Start is unsupported on this system.
func Start(Spec) (*Proc, error) {
	return nil, ErrUnsupported
}

func setWinsize(*os.File, int, int) error {
	return ErrUnsupported
}
