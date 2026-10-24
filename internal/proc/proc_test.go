package proc

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReader_StartTime(t *testing.T) {
	t.Parallel()
	t.Run("this process started before now", func(t *testing.T) {
		t.Parallel()
		started, err := Reader{}.StartTime(os.Getpid())
		require.NoError(t, err)
		assert.False(t, started.After(time.Now()))
		assert.True(t, started.After(time.Now().Add(-24*time.Hour)))
	})
	t.Run("a child didn't start before its parent", func(t *testing.T) {
		t.Parallel()
		child := exec.CommandContext(t.Context(), "/bin/cat")
		stdin, err := child.StdinPipe()
		require.NoError(t, err)
		require.NoError(t, child.Start())
		defer func() {
			_ = stdin.Close()
			_ = child.Wait()
		}()
		parent, err := Reader{}.StartTime(os.Getpid())
		require.NoError(t, err)
		got, err := Reader{}.StartTime(child.Process.Pid)
		require.NoError(t, err)
		assert.False(t, got.Before(parent), "Linux counts start times in 10ms ticks, so they can be equal")
	})
	t.Run("an invalid pid", func(t *testing.T) {
		t.Parallel()
		_, err := Reader{}.StartTime(0)
		assert.ErrorIs(t, err, errBadPID)
	})
	t.Run("no such process", func(t *testing.T) {
		t.Parallel()
		_, err := Reader{}.StartTime(99999999)
		assert.Error(t, err)
	})
}
