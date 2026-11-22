package cli

import (
	"testing"

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
