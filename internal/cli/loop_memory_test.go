package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/loop"
)

// newMemoryEnv is an App with a loop named deps, and its memory in a temp dir.
func newMemoryEnv(t *testing.T) (*App, *bytes.Buffer, *loop.DataStore) {
	t.Helper()
	app, stdout, _ := newTestApp()
	store := withLoopStore(t, app)
	data := withLoopData(t, app)
	l := loop.Loop{Version: loop.FormatVersion, Name: "deps", Every: "1h", Prompt: "Do it", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
	require.NoError(t, store.Create(&l))
	return app, stdout, data
}

// runMemory runs `ws loop memory args...` and returns its exit code. Its
// output is left in stdout, since memory's text output isn't JSON.
func runMemory(t *testing.T, app *App, stdout *bytes.Buffer, args ...string) int {
	t.Helper()
	stdout.Reset()
	return app.Run(t.Context(), append([]string{"loop", "memory"}, args...))
}

func TestApp_loopMemory(t *testing.T) {
	t.Run("set prints the revision and get prints the value exactly", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "seen", "#412"))
		assert.Equal(t, "seen  rev 1\n", stdout.String())
		require.Equal(t, exitOK, runMemory(t, app, stdout, "get", "deps", "seen"))
		assert.Equal(t, "#412", stdout.String())
	})
	t.Run("list in JSON has the bytes and a null updated_by for a person's set", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "seen", "#412"))
		require.Equal(t, exitOK, runMemory(t, app, stdout, "--json", "list", "deps"))
		out := jsonObjectOf(t, stdout.String())
		assert.Equal(t, "deps", out["loop"])
		assert.EqualValues(t, 4, out["bytes"])
		entries, ok := out["entries"].([]any)
		require.True(t, ok)
		require.Len(t, entries, 1)
		entry := jsonObject(t, entries[0])
		assert.Equal(t, "seen", entry["key"])
		assert.Equal(t, "#412", entry["value"])
		assert.Nil(t, entry["updated_by"])
	})
	t.Run("set with --expect-rev 0 on an existing key is memory_conflict", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "seen", "#412"))
		assert.Equal(t, exitRefused, runMemory(t, app, stdout, "set", "--expect-rev", "0", "deps", "seen", "#415"))
	})
	t.Run("set with a value and --file is usage", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitUsage, runMemory(t, app, stdout, "set", "--file", "-", "deps", "seen", "x"))
	})
	t.Run("set with neither a value nor --file is usage", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitUsage, runMemory(t, app, stdout, "set", "deps", "seen"))
	})
	t.Run("set reads the value from stdin with --file -", func(t *testing.T) {
		app, stdout, data := newMemoryEnv(t)
		app.stdin = strings.NewReader("from stdin")
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "--file", "-", "deps", "seen"))
		mem, err := data.ReadMemory("deps")
		require.NoError(t, err)
		assert.Equal(t, "from stdin", mem.Entries["seen"].Value)
	})
	t.Run("a value over 64 KiB is memory_full", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		big := strings.Repeat("x", loop.MaxMemoryValue+1)
		assert.Equal(t, exitRefused, runMemory(t, app, stdout, "set", "deps", "big", big))
	})
	t.Run("a key that starts with a digit is memory_invalid", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitUsage, runMemory(t, app, stdout, "set", "deps", "1x", "v"))
	})
	t.Run("rm of a missing key is memory_not_found", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitNotFound, runMemory(t, app, stdout, "rm", "deps", "seen"))
	})
	t.Run("get of a missing key is memory_not_found", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitNotFound, runMemory(t, app, stdout, "get", "deps", "seen"))
	})
	t.Run("an unknown loop is loop_not_found", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		assert.Equal(t, exitNotFound, runMemory(t, app, stdout, "list", "nope"))
	})
	t.Run("clear removes every key and reports how many", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "a", "1"))
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "b", "2"))
		require.Equal(t, exitOK, runMemory(t, app, stdout, "clear", "deps"))
		assert.Equal(t, "2 keys removed\n", stdout.String())
		require.Equal(t, exitOK, runMemory(t, app, stdout, "--json", "list", "deps"))
		assert.Empty(t, jsonObjectOf(t, stdout.String())["entries"])
	})
	t.Run("rm in JSON reports removed", func(t *testing.T) {
		app, stdout, _ := newMemoryEnv(t)
		require.Equal(t, exitOK, runMemory(t, app, stdout, "set", "deps", "seen", "#412"))
		require.Equal(t, exitOK, runMemory(t, app, stdout, "--json", "rm", "deps", "seen"))
		assert.JSONEq(t, `{"loop":"deps","key":"seen","removed":true,"warnings":[]}`, stdout.String())
	})
}

// jsonObjectOf is the JSON object in s, or the test fails.
func jsonObjectOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &out), s)
	return out
}
