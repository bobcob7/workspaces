package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/procgroup"
	"github.com/bobcob7/workspaces/internal/runner"
)

// writeScript writes an executable shell script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ws")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700))
	return path
}

// waitFor polls until ok reports true or the test fails after a few seconds.
func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	require.Eventually(t, ok, 5*time.Second, 10*time.Millisecond)
}

func TestOSStarter_Start(t *testing.T) {
	t.Parallel()
	t.Run("runs the process in its own session with the environment, and logs stderr", func(t *testing.T) {
		dir := t.TempDir()
		logPath := filepath.Join(dir, "runner.log")
		script := writeScript(t, "echo \"$WS_WORKSPACE\" >&2\necho $$ > pid\nexec sleep 30\n")
		pid, err := OSStarter{}.Start(script, []string{"runner", "run"}, dir, logPath, []string{"WS_WORKSPACE=ws-9"})
		require.NoError(t, err)
		t.Cleanup(func() { _ = OSStarter{}.Signal(pid, syscall.SIGKILL) })
		waitFor(t, func() bool {
			data, readErr := os.ReadFile(filepath.Join(dir, "pid"))
			return readErr == nil && len(data) > 0
		})
		pidText, err := os.ReadFile(filepath.Join(dir, "pid"))
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(pid)+"\n", string(pidText))
		logged, err := os.ReadFile(logPath)
		require.NoError(t, err)
		assert.Contains(t, string(logged), "ws-9")
	})
	t.Run("a missing executable is an error", func(t *testing.T) {
		dir := t.TempDir()
		_, err := OSStarter{}.Start(filepath.Join(dir, "missing"), nil, dir, filepath.Join(dir, "log"), nil)
		require.Error(t, err)
	})
	t.Run("a log that can't be opened is an error", func(t *testing.T) {
		dir := t.TempDir()
		_, err := OSStarter{}.Start("/bin/true", nil, dir, filepath.Join(dir, "missing", "log"), nil)
		require.Error(t, err)
	})
}

func TestOSStarter_Signal(t *testing.T) {
	t.Parallel()
	t.Run("interrupts the process group", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "sleep", "30")
		cmd.SysProcAttr = procgroup.Detached()
		require.NoError(t, cmd.Start())
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		require.NoError(t, OSStarter{}.Signal(cmd.Process.Pid, syscall.SIGTERM))
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the process survived SIGTERM")
		}
	})
	t.Run("a group that doesn't exist is an error", func(t *testing.T) {
		require.Error(t, OSStarter{}.Signal(1<<30, syscall.SIGTERM))
	})
}

func TestSupervisor_StartWithProcesses(t *testing.T) {
	t.Parallel()
	t.Run("a real runner process that writes its status is started and reported", func(t *testing.T) {
		state := t.TempDir()
		files := runner.FilesFor(state, "ws-1")
		require.NoError(t, files.WriteRun(&runner.Run{Version: 1, Session: "rn-1", CreatedAt: time.Now().UTC()}))
		script := writeScript(t, "printf '{\"version\":1,\"session\":\"rn-1\",\"pid\":%s}\\n' \"$$\" > status.json\nexec sleep 30\n")
		sup := NewSupervisor(SupervisorConfig{
			StateDir:   state,
			ConfigDir:  t.TempDir(),
			Exe:        script,
			BaseEnv:    os.Environ(),
			LoadConfig: func() (config.Config, error) { return config.Config{}, nil },
			StartWait:  5 * time.Second,
		}, proc.Reader{}, OSStarter{}, discardLog())
		pid, err := sup.Start(t.Context(), "ws-1", false)
		require.NoError(t, err)
		t.Cleanup(func() { _ = OSStarter{}.Signal(pid, syscall.SIGKILL) })
		st, err := files.ReadStatus()
		require.NoError(t, err)
		assert.Equal(t, pid, st.PID)
		assert.Equal(t, "rn-1", st.Session)
	})
}
