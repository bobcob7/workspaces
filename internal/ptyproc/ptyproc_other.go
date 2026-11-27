//go:build !linux && !darwin

package ptyproc

// Supported reports whether this system has pseudo-terminals.
func Supported() bool { return false }

// Start is unsupported on this system.
func Start(Spec) (*Proc, error) {
	return nil, ErrUnsupported
}

// Proc is never created on this system; its methods exist so callers build.
type Proc struct{}

// Read fails: there is no process.
func (*Proc) Read([]byte) (int, error) { return 0, ErrUnsupported }

// Write fails: there is no process.
func (*Proc) Write([]byte) (int, error) { return 0, ErrUnsupported }

// Resize fails: there is no terminal.
func (*Proc) Resize(int, int) error { return ErrUnsupported }

// Close does nothing.
func (*Proc) Close() error { return nil }

// Wait returns nil: there is no exit code.
func (*Proc) Wait() *int { return nil }
