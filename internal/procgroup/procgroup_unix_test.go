//go:build unix

package procgroup

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnGroup(t *testing.T) {
	t.Parallel()
	t.Run("starts the process as its group leader", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "sleep", "30")
		cmd.SysProcAttr = OwnGroup()
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = KillTree(cmd.Process.Pid) })
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		require.NoError(t, err)
		assert.Equal(t, cmd.Process.Pid, pgid)
	})
}

func TestKillTree(t *testing.T) {
	t.Parallel()
	t.Run("kills the process and its children", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "sh", "-c", "sleep 30 & sleep 30")
		cmd.SysProcAttr = OwnGroup()
		require.NoError(t, cmd.Start())
		require.NoError(t, KillTree(cmd.Process.Pid))
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			var exitErr *exec.ExitError
			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, syscall.SIGKILL, exitErr.Sys().(syscall.WaitStatus).Signal())
		case <-time.After(5 * time.Second):
			t.Fatal("process group still running after KillTree")
		}
	})
}
