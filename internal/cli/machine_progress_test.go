package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newProgressApp is newMachineApp, with the config loader handing out the
// temp state dir, since the commands load config themselves.
func newProgressApp(t *testing.T) *mockedApp {
	t.Helper()
	e := newMachineApp(t)
	e.app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) { return e.app.paths, config.Config{}, nil },
	}
	return e
}

// assertStoredProgress checks the caller's stored record, ignoring its
// version and time.
func assertStoredProgress(t *testing.T, e *mockedApp, want *machine.Progress) {
	t.Helper()
	got, err := machine.NewStore(e.app.paths.State).ReadProgress("boss", "plan")
	require.NoError(t, err)
	require.NotNil(t, got)
	got.Version, got.UpdatedAt = 0, time.Time{}
	assert.Equal(t, *want, *got)
}

func TestApp_machineProgress(t *testing.T) {
	t.Run("set records the caller's task and prints the tree", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		assert.Equal(t, "plan", out["id"])
		own := jsonObject(t, out["progress"])
		assert.Equal(t, "build", own["name"])
		assert.InDelta(t, 4, own["total"], 0)
		assert.InDelta(t, 0, own["done"], 0)
	})
	t.Run("step adds to done and keeps the name", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4"))
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "step", "--by", "2", "--note", "tests"), e.stderr.String())
		own := jsonObject(t, jsonObjectOf(t, e.stdout.String())["progress"])
		assert.InDelta(t, 2, own["done"], 0)
		assert.Equal(t, "build", own["name"])
		assert.Equal(t, "tests", own["note"])
	})
	t.Run("step past total is progress_invalid", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--total", "1"))
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "progress", "step", "--by", "2"))
		assert.Contains(t, e.stderr.String(), "progress_invalid")
	})
	t.Run("set replaces a record that is invalid, so an agent can recover", func(t *testing.T) {
		e := newProgressApp(t)
		store := machine.NewStore(e.app.paths.State)
		require.NoError(t, store.WriteProgress("boss", "plan", &machine.Progress{Total: 1}))
		path := filepath.Join(store.Dir("boss", "plan"), "progress.toml")
		require.NoError(t, os.WriteFile(path, []byte("version = 1\ntotal = -3\n"), 0o600))
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--total", "2"), e.stderr.String())
		p, err := store.ReadProgress("boss", "plan")
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.Equal(t, 2, p.Total)
	})
	t.Run("set --total below done is progress_invalid and leaves the record alone", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4", "--done", "3"))
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "progress", "set", "--total", "2"))
		assert.Contains(t, e.stderr.String(), "progress_invalid")
		assertStoredProgress(t, e, &machine.Progress{Name: "build", Total: 4, Done: 3})
	})
	t.Run("set --done above total is progress_invalid and leaves the record alone", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4", "--done", "3"))
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "progress", "set", "--done", "5"))
		assert.Contains(t, e.stderr.String(), "progress_invalid")
		assertStoredProgress(t, e, &machine.Progress{Name: "build", Total: 4, Done: 3})
	})
	t.Run("set --name empty clears the name and keeps the other fields", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4", "--done", "1", "--note", "tests"))
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", ""), e.stderr.String())
		assertStoredProgress(t, e, &machine.Progress{Total: 4, Done: 1, Note: "tests"})
	})
	t.Run("set --note empty clears the note and keeps the other fields", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "4", "--done", "1", "--note", "tests"))
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--note", ""), e.stderr.String())
		assertStoredProgress(t, e, &machine.Progress{Name: "build", Total: 4, Done: 1})
	})
	t.Run("set with no flag is usage", func(t *testing.T) {
		e := newProgressApp(t)
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "progress", "set"))
	})
	t.Run("step by zero is usage", func(t *testing.T) {
		e := newProgressApp(t)
		assert.Equal(t, exitUsage, runMachine(t, e, "machine", "progress", "step", "--by", "0"))
	})
	t.Run("show nests a child's progress under the caller's", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "build", "--total", "2", "--done", "1"))
		tree := machineTree()
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return tree[2], nil }
		e.app.getwd = func() (string, error) { return "/cfg/workspaces/a/x", nil }
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--name", "impl", "--total", "3", "--done", "3"))
		e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return tree[1], nil }
		e.app.getwd = func() (string, error) { return "/cfg/workspaces/plan/x", nil }
		e.stdout.Reset()
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "show"), e.stderr.String())
		root := jsonObject(t, jsonObjectOf(t, e.stdout.String())["tree"])
		rollup := jsonObject(t, root["rollup"])
		assert.InDelta(t, 5, rollup["total"], 0)
		assert.InDelta(t, 4, rollup["done"], 0)
		assert.InDelta(t, 0.8, rollup["fraction"], 0.0001)
		children, ok := root["children"].([]any)
		require.True(t, ok)
		require.NotEmpty(t, children)
		child := jsonObject(t, children[0])
		assert.Equal(t, "a", child["id"])
		assert.Equal(t, "impl", jsonObject(t, child["progress"])["name"])
	})
	t.Run("show with nothing reported has a null fraction", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "show"), e.stderr.String())
		root := jsonObject(t, jsonObjectOf(t, e.stdout.String())["tree"])
		assert.Nil(t, jsonObject(t, root["rollup"])["fraction"])
	})
}

func TestApp_machineProgressWithResults(t *testing.T) {
	t.Run("a passing result finishes the declared steps", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--total", "4", "--done", "1"))
		require.NoError(t, e.app.runMachineResult("pass", ""))
		p, err := machine.NewStore(e.app.paths.State).ReadProgress("boss", "plan")
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.Equal(t, 4, p.Done)
	})
	t.Run("a failing result leaves the steps where they were", func(t *testing.T) {
		e := newProgressApp(t)
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "set", "--total", "4", "--done", "1"))
		require.NoError(t, e.app.runMachineResult("fail", ""))
		p, err := machine.NewStore(e.app.paths.State).ReadProgress("boss", "plan")
		require.NoError(t, err)
		require.NotNil(t, p)
		assert.Equal(t, 1, p.Done)
	})
	t.Run("a passing result with no progress records none", func(t *testing.T) {
		e := newProgressApp(t)
		require.NoError(t, e.app.runMachineResult("pass", ""))
		p, err := machine.NewStore(e.app.paths.State).ReadProgress("boss", "plan")
		require.NoError(t, err)
		assert.Nil(t, p)
	})
	t.Run("an unreadable progress file is a warning, and the result is still recorded", func(t *testing.T) {
		e := newProgressApp(t)
		store := machine.NewStore(e.app.paths.State)
		require.NoError(t, store.WriteProgress("boss", "plan", &machine.Progress{Total: 1}))
		path := filepath.Join(store.Dir("boss", "plan"), "progress.toml")
		require.NoError(t, os.WriteFile(path, []byte("version = 1\ntotal = -3\n"), 0o600))
		require.Equal(t, exitOK, runMachine(t, e, "machine", "result", "pass"), e.stderr.String())
		out := jsonObjectOf(t, e.stdout.String())
		warnings, ok := out["warnings"].([]any)
		require.True(t, ok)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "couldn't finish plan's progress")
		rec, err := store.Read("boss", "plan")
		require.NoError(t, err)
		assert.NotNil(t, rec.Result)
	})
	t.Run("show counts a finished child that never reported as one step", func(t *testing.T) {
		e := newProgressApp(t)
		store := machine.NewStore(e.app.paths.State)
		require.NoError(t, store.WriteResult("boss", "a", &machine.Result{Verdict: "PASS", RecordedAt: testNow()}, nil))
		require.Equal(t, exitOK, runMachine(t, e, "machine", "progress", "show"), e.stderr.String())
		rollup := jsonObject(t, jsonObject(t, jsonObjectOf(t, e.stdout.String())["tree"])["rollup"])
		assert.InDelta(t, 1, rollup["total"], 0)
		assert.InDelta(t, 1, rollup["done"], 0)
	})
}
