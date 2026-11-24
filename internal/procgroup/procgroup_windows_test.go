package procgroup

import (
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func filetimeAt(ticks uint32) windows.Filetime {
	return windows.Filetime{LowDateTime: ticks}
}

func TestDescendants(t *testing.T) {
	t.Run("keeps the real descendants and skips a reused pid", func(t *testing.T) {
		entries := []windows.ProcessEntry32{
			{ProcessID: 100, ParentProcessID: 1},
			{ProcessID: 200, ParentProcessID: 100},
			{ProcessID: 300, ParentProcessID: 200},
			{ProcessID: 400, ParentProcessID: 1},
			{ProcessID: 500, ParentProcessID: 100},
			{ProcessID: 600, ParentProcessID: 100},
			{ProcessID: 700, ParentProcessID: 500},
		}
		times := map[uint32]windows.Filetime{
			100: filetimeAt(10),
			200: filetimeAt(20),
			300: filetimeAt(30),
			400: filetimeAt(40),
			500: filetimeAt(5),
			600: filetimeAt(15),
			700: filetimeAt(50),
		}
		created := func(pid uint32) (windows.Filetime, bool) {
			ft, ok := times[pid]
			return ft, ok
		}
		got := descendants(100, entries, created)
		assert.ElementsMatch(t, []uint32{200, 300, 600}, got)
	})
}

func TestKillTree(t *testing.T) {
	t.Parallel()
	t.Run("ends the process and the processes it started", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "cmd.exe", "/C", "ping", "-n", "30", "127.0.0.1")
		cmd.SysProcAttr = OwnGroup()
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = KillTree(cmd.Process.Pid) })
		require.NoError(t, KillTree(cmd.Process.Pid))
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("process tree still running after KillTree")
		}
	})
}
