package cli

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newMachineMemoryEnv is newMachineApp, with a loop named deps, and a run mark
// on boss, the root of plan, so plan's runs belong to deps.
func newMachineMemoryEnv(t *testing.T) (*mockedApp, *loop.DataStore) {
	t.Helper()
	e := newMachineApp(t)
	store := withLoopStore(t, e.app)
	data := withLoopData(t, e.app)
	l := loop.Loop{Version: loop.FormatVersion, Name: "deps", Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
	require.NoError(t, store.Create(&l))
	require.NoError(t, data.WriteRunMark("boss", loop.RunMark{Loop: "deps", StartedAt: testNow()}))
	return e, data
}

func TestApp_machineMemory(t *testing.T) {
	t.Run("set writes the loop's memory, updated by the caller", func(t *testing.T) {
		e, data := newMachineMemoryEnv(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "#412"), e.stderr.String())
		assert.Contains(t, e.stdout.String(), `"loop":"deps"`)
		mem, err := data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "#412", mem.Entries["seen"].Value)
		assert.Equal(t, "plan", mem.Entries["seen"].UpdatedBy)
	})
	t.Run("a child of the run shares its loop's memory", func(t *testing.T) {
		e, data := newMachineMemoryEnv(t)
		tree := machineTree()
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return tree[4], nil }
		e.app.getwd = func() (string, error) { return "/cfg/workspaces/a1", nil }
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "x"), e.stderr.String())
		mem, err := data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "a1", mem.Entries["seen"].UpdatedBy)
	})
	t.Run("get prints the value in JSON", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "#412"), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "get", "seen"), e.stderr.String())
		entry := jsonObject(t, jsonObjectOf(t, e.stdout.String())["entry"])
		assert.Equal(t, "#412", entry["value"])
		assert.Equal(t, "plan", entry["updated_by"])
	})
	t.Run("get of a missing key is memory_not_found", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		assert.Equal(t, exitNotFound, runMachine(t, e, "machine", "memory", "get", "seen"))
	})
	t.Run("list shows the loop's keys", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "#412"), e.stderr.String())
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "list"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, "deps", out["loop"])
		assert.Len(t, out["entries"], 1)
	})
	t.Run("rm removes a key", func(t *testing.T) {
		e, data := newMachineMemoryEnv(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "#412"), e.stderr.String())
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "rm", "seen"), e.stderr.String())
		mem, err := data.ReadMemory("deps")
		require.NoError(t, err)
		assert.NotContains(t, mem.Entries, "seen")
	})
	t.Run("set with --expect-rev 0 on an existing key is memory_conflict", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "memory", "set", "seen", "#412"), e.stderr.String())
		assert.Equal(t, exitRefused, runMachine(t, e, "machine", "memory", "set", "--expect-rev", "0", "seen", "#415"))
	})
	t.Run("a workspace that no loop run started is not_loop_run", func(t *testing.T) {
		e := newMachineApp(t)
		withLoopStore(t, e.app)
		assert.Equal(t, exitRefused, runMachine(t, e, "machine", "memory", "list"))
	})
	t.Run("a run mark for a removed loop is not_loop_run", func(t *testing.T) {
		e := newMachineApp(t)
		data := withLoopData(t, e.app)
		withLoopStore(t, e.app)
		require.NoError(t, data.WriteRunMark("boss", loop.RunMark{Loop: "gone", StartedAt: testNow()}))
		assert.Equal(t, exitRefused, runMachine(t, e, "machine", "memory", "list"))
	})
	t.Run("outside a workspace is outside_workspace", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		e.outside()
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "memory", "list"))
	})
	t.Run("set with neither a value nor --file is usage", func(t *testing.T) {
		e, _ := newMachineMemoryEnv(t)
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "memory", "set", "seen"))
	})
}
