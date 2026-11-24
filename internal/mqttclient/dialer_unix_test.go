//go:build linux || darwin

package mqttclient

import (
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failingRawConn is a syscall.RawConn whose Control returns controlErr, or
// runs fn when that is nil.
type failingRawConn struct {
	controlErr error
}

func (r failingRawConn) Control(fn func(uintptr)) error {
	if r.controlErr != nil {
		return r.controlErr
	}
	fn(^uintptr(0))
	return nil
}

func (failingRawConn) Read(func(uintptr) bool) error  { return nil }
func (failingRawConn) Write(func(uintptr) bool) error { return nil }

func Test_notSentLowWaterControl_errors(t *testing.T) {
	t.Run("returns the error of RawConn.Control", func(t *testing.T) {
		want := errors.New("control failed")
		err := notSentLowWaterControl(16384)("tcp", "x", failingRawConn{controlErr: want})
		assert.ErrorIs(t, err, want)
	})
	t.Run("returns the setsockopt error", func(t *testing.T) {
		err := notSentLowWaterControl(16384)("tcp", "x", failingRawConn{})
		require.Error(t, err)
		assert.ErrorIs(t, err, syscall.EBADF)
	})
}
