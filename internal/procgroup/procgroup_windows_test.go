package procgroup

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// filetimeOfTicks is the FILETIME holding ticks 100ns intervals.
func filetimeOfTicks(ticks uint64) windows.Filetime {
	return windows.Filetime{LowDateTime: uint32(ticks), HighDateTime: uint32(ticks >> 32)}
}

// creationTimes is a created func answering from a map; a pid not in the map
// can't be inspected.
func creationTimes(times map[uint32]uint64) func(uint32) (windows.Filetime, bool) {
	return func(pid uint32) (windows.Filetime, bool) {
		ticks, ok := times[pid]
		return filetimeOfTicks(ticks), ok
	}
}

func TestDescendants(t *testing.T) {
	t.Parallel()
	t.Run("keeps the real descendants and skips a reused pid", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{
			{ProcessID: 100, ParentProcessID: 1},
			{ProcessID: 200, ParentProcessID: 100},
			{ProcessID: 300, ParentProcessID: 200},
			{ProcessID: 400, ParentProcessID: 1},
			{ProcessID: 500, ParentProcessID: 100},
			{ProcessID: 600, ParentProcessID: 100},
			{ProcessID: 700, ParentProcessID: 500},
		}
		created := creationTimes(map[uint32]uint64{
			100: 10,
			200: 20,
			300: 30,
			400: 40,
			500: 5,
			600: 15,
			700: 50,
		})
		got := descendants(100, entries, created)
		assert.ElementsMatch(t, []uint32{200, 300, 600}, got)
	})
	t.Run("keeps a child that started at the same tick as its parent", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{{ProcessID: 200, ParentProcessID: 100}}
		created := creationTimes(map[uint32]uint64{100: 10, 200: 10})
		assert.Equal(t, []uint32{200}, descendants(100, entries, created))
	})
	t.Run("drops a child that started strictly before its parent", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{{ProcessID: 200, ParentProcessID: 100}}
		created := creationTimes(map[uint32]uint64{100: 10, 200: 9})
		assert.Empty(t, descendants(100, entries, created))
	})
	t.Run("compares the high 32 bits of the creation time", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{{ProcessID: 200, ParentProcessID: 100}}
		created := creationTimes(map[uint32]uint64{100: 1 << 33, 200: 1<<33 - 1})
		assert.Empty(t, descendants(100, entries, created))
	})
	t.Run("keeps no descendants when the root's creation time can't be read", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{{ProcessID: 200, ParentProcessID: 100}}
		created := creationTimes(map[uint32]uint64{200: 20})
		assert.Empty(t, descendants(100, entries, created))
	})
	t.Run("drops a child whose creation time can't be read", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{
			{ProcessID: 200, ParentProcessID: 100},
			{ProcessID: 300, ParentProcessID: 100},
		}
		created := creationTimes(map[uint32]uint64{100: 10, 300: 30})
		assert.Equal(t, []uint32{300}, descendants(100, entries, created))
	})
	t.Run("a cycle of parents ends", func(t *testing.T) {
		t.Parallel()
		entries := []windows.ProcessEntry32{
			{ProcessID: 200, ParentProcessID: 100},
			{ProcessID: 100, ParentProcessID: 200},
		}
		created := creationTimes(map[uint32]uint64{100: 10, 200: 20})
		assert.Equal(t, []uint32{200}, descendants(100, entries, created))
	})
}

// descendantsOf lists the descendants of pid in a fresh process snapshot.
func descendantsOf(pid int) ([]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()
	entries, err := processEntries(snapshot)
	if err != nil {
		return nil, err
	}
	return descendants(uint32(pid), entries, creationTime), nil
}

// exited reports whether pid has ended, waiting up to five seconds for it.
func exited(t *testing.T, pid uint32) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true
	}
	require.NoError(t, err)
	defer func() { _ = windows.CloseHandle(h) }()
	event, err := windows.WaitForSingleObject(h, 5000)
	require.NoError(t, err)
	return event == windows.WAIT_OBJECT_0
}

func TestKillTree(t *testing.T) {
	t.Parallel()
	t.Run("ends the process and the processes it started", func(t *testing.T) {
		t.Parallel()
		cmd := exec.CommandContext(t.Context(), "cmd.exe", "/C", "ping", "-n", "30", "127.0.0.1")
		cmd.SysProcAttr = OwnGroup()
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = KillTree(cmd.Process.Pid) })
		var children []uint32
		require.Eventually(t, func() bool {
			found, err := descendantsOf(cmd.Process.Pid)
			children = found
			return err == nil && len(found) > 0
		}, 5*time.Second, 50*time.Millisecond, "cmd.exe never started a child")
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
		for _, child := range children {
			assert.True(t, exited(t, child), "descendant %d still running after KillTree", child)
		}
	})
}
