// Package ptyproc runs a process on a new pseudo-terminal.
package ptyproc

import "errors"

// ErrUnsupported is returned on systems without the pty ioctls.
var ErrUnsupported = errors.New("pseudo-terminals aren't supported on this system")

// Spec is what to run.
type Spec struct {
	Path       string   // absolute program path
	Args       []string // argv[1:]
	Dir        string
	Env        []string // the whole environment; nil or empty gives an empty one (on Windows, only SYSTEMROOT)
	Cols, Rows int
}
