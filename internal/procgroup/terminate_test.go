package procgroup

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminateAll(t *testing.T) {
	t.Parallel()
	t.Run("ends every target in order when all succeed", func(t *testing.T) {
		t.Parallel()
		var got []uint32
		err := terminateAll([]uint32{3, 2, 1}, func(pid uint32) error {
			got = append(got, pid)
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, []uint32{3, 2, 1}, got)
	})
	t.Run("an error on one target does not skip the rest", func(t *testing.T) {
		t.Parallel()
		errDenied := errors.New("access denied")
		var got []uint32
		err := terminateAll([]uint32{300, 200, 100}, func(pid uint32) error {
			got = append(got, pid)
			if pid == 200 {
				return errDenied
			}
			return nil
		})
		assert.Equal(t, []uint32{300, 200, 100}, got)
		require.Error(t, err)
		require.ErrorIs(t, err, errDenied)
		require.ErrorContains(t, err, "process 200")
	})
	t.Run("joins every failure", func(t *testing.T) {
		t.Parallel()
		err := terminateAll([]uint32{1, 2}, func(uint32) error {
			return errors.New("failed")
		})
		require.ErrorContains(t, err, "process 1")
		require.ErrorContains(t, err, "process 2")
	})
}

func TestKillOrder(t *testing.T) {
	t.Parallel()
	t.Run("ends the descendants deepest first and the root last", func(t *testing.T) {
		t.Parallel()
		got := killOrder(100, []uint32{200, 300, 600})
		assert.Equal(t, []uint32{600, 300, 200, 100}, got)
	})
	t.Run("a lone root is just the root", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, []uint32{100}, killOrder(100, nil))
	})
}
