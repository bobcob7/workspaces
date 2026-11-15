package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/runner"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// withRunnerConfig makes app load runnerToml as its config.toml.
func withRunnerConfig(t *testing.T, app *App) {
	t.Helper()
	cfg := loadRunnerConfig(t, runnerToml)
	app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
		return testPaths(), cfg, nil
	}}
}

func TestApp_loopAddRunner(t *testing.T) {
	t.Run("a ws loop prints its runner, model alias, and endpoint", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		withRunnerConfig(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--runner", "ws", "--endpoint", "cpu", "--model", "qwen9b", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "ws", out["runner"])
		assert.Equal(t, "cpu", out["endpoint"])
		assert.Equal(t, "qwen9b", out["model"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.RunnerWS, stored.Runner)
		assert.Equal(t, loop.FormatVersion, stored.Version)
	})

	t.Run("a claude loop prints runner claude and a null endpoint", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "claude", out["runner"])
		assert.Nil(t, out["endpoint"])
	})

	t.Run("a model id with a slash is a raw id, not an alias", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		withLoopStore(t, app)
		withRunnerConfig(t, app)
		code, out := runLoop(t, app, stdout, "add", "deps", "--every", "1h", "--prompt", "hi", "--runner", "ws", "--model", "qwen/qwen3.5-9b", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "qwen/qwen3.5-9b", out["model"])
	})

	t.Run("refuses what config.toml doesn't define, and what doesn't fit the runner", func(t *testing.T) {
		tests := map[string][]string{
			"model alias not in config": {"--runner", "ws", "--model", "qwen9"},
			"endpoint not in config":    {"--runner", "ws", "--endpoint", "nope"},
			"endpoint with claude":      {"--endpoint", "cpu"},
			"permission mode with ws":   {"--runner", "ws", "--permission-mode", "acceptEdits"},
			"unknown runner":            {"--runner", "x"},
		}
		for name, flags := range tests {
			t.Run(name, func(t *testing.T) {
				app, stdout, _ := newTestApp()
				store := withLoopStore(t, app)
				withRunnerConfig(t, app)
				args := append([]string{"add", "deps", "--every", "1h", "--prompt", "hi"}, flags...)
				code, _ := runLoop(t, app, stdout, args...)
				assert.Equal(t, exitUsage, code)
				_, err := store.Get("deps")
				require.ErrorIs(t, err, loop.ErrNotFound)
			})
		}
	})
}

func TestApp_loopEditRunner(t *testing.T) {
	t.Run("--runner ws makes a version 3 loop version 4, and --runner claude clears it", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		withRunnerConfig(t, app)
		l := loop.Loop{Version: loop.Version3, Name: "deps", Every: "1h", Prompt: "hi", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, store.Save(&l))
		code, out := runLoop(t, app, stdout, "edit", "deps", "--runner", "ws", "--endpoint", "gpu", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "ws", out["runner"])
		assert.Equal(t, "gpu", out["endpoint"])
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.FormatVersion, stored.Version)

		code, out = runLoop(t, app, stdout, "edit", "deps", "--runner", "claude", "--json")
		require.Equal(t, exitOK, code)
		assert.Equal(t, "claude", out["runner"])
		assert.Nil(t, out["endpoint"])
	})

	t.Run("an endpoint that config.toml doesn't define changes nothing", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		store := withLoopStore(t, app)
		withRunnerConfig(t, app)
		l := loop.Loop{Version: loop.Version3, Name: "deps", Every: "1h", Prompt: "hi", Keep: 5, CreatedAt: testNow(), NextRunAt: testNow().Add(time.Hour)}
		require.NoError(t, store.Save(&l))
		code, _ := runLoop(t, app, stdout, "edit", "deps", "--runner", "ws", "--endpoint", "nope")
		assert.Equal(t, exitUsage, code)
		stored, err := store.Get("deps")
		require.NoError(t, err)
		assert.Equal(t, loop.Version3, stored.Version)
		assert.Empty(t, stored.Runner)
		assert.Empty(t, stored.Endpoint)
	})
}

func TestApp_loopTickRunner(t *testing.T) {
	t.Run("starts a due ws loop on the runner, with no parent and the loop's endpoint", func(t *testing.T) {
		e, re := newRunnerTickEnv(t)
		e.addDueRunnerLoop(t)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, exitOK, code, e.stderr.String())
		items := listedLoops(t, out)
		require.Len(t, items, 1)
		assert.Equal(t, "started", items[0]["action"])
		session, ok := items[0]["session"].(string)
		require.True(t, ok)
		assert.True(t, strings.HasPrefix(session, runner.SessionPrefix))
		assert.Empty(t, e.claude.StartCalls())
		require.Len(t, re.daemon.StartRunnerCalls(), 1)
		run := readRun(t, re, "fix-login-d8efb24f")
		assert.Empty(t, run.Parent)
		assert.Equal(t, "gpu", run.EndpointFlag)
		assert.Equal(t, "qwen9b", run.ModelAlias)
		rec := e.loopRecord(t)
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, "fix-login-d8efb24f", rec.Runs[0].Workspace)
	})

	t.Run("a preflight failure is the run's error, and no Claude run starts", func(t *testing.T) {
		e, re := newRunnerTickEnv(t)
		re.app.dialDaemon = func(context.Context) (runnerDaemon, error) {
			return nil, newError(codeDaemonUnavailable, errTest)
		}
		e.addDueRunnerLoop(t)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, exitFailed, code, e.stderr.String())
		items := listedLoops(t, out)
		require.Len(t, items, 1)
		assert.Equal(t, "failed", items[0]["action"])
		assert.Nil(t, items[0]["workspace"])
		errJSON, ok := items[0]["error"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, string(codeDaemonUnavailable), errJSON["code"])
		assert.Empty(t, e.claude.StartCalls())
		rec := e.loopRecord(t)
		require.Len(t, rec.Runs, 1)
		assert.Equal(t, string(codeDaemonUnavailable), rec.Runs[0].ErrorCode)
		assert.True(t, rec.NextRunAt.After(testNow()))
	})

	t.Run("a claude loop starts as before, with a runner config and a failing daemon", func(t *testing.T) {
		e, re := newRunnerTickEnv(t)
		dialed := false
		re.app.dialDaemon = func(context.Context) (runnerDaemon, error) {
			dialed = true
			return nil, newError(codeDaemonUnavailable, errTest)
		}
		e.addDueLoop(t)
		code, out := runLoop(t, e.app, e.stdout, "tick", "--json")
		require.Equal(t, exitOK, code, e.stderr.String())
		items := listedLoops(t, out)
		require.Len(t, items, 1)
		assert.Equal(t, "started", items[0]["action"])
		assert.Len(t, e.claude.StartCalls(), 1)
		assert.False(t, dialed)
	})
}

// newRunnerTickEnv is a tick env whose config is runnerToml, with a ws runner
// whose daemon starts runners as newRunnerEnv's does. Both envs share one App.
func newRunnerTickEnv(t *testing.T) (*tickEnv, *runnerEnv) {
	t.Helper()
	re := newRunnerEnv(t, runnerToml)
	e := &tickEnv{mockedApp: re.mockedApp}
	e.loops = withLoopStore(t, e.app)
	e.data = withLoopData(t, e.app)
	e.store.RecordSessionFunc = func(string, workspace.SessionKind, string) error { return nil }
	e.claude = &claudeClientMock{
		CheckFunc: func(context.Context) error { return nil },
		StartFunc: func(context.Context, claude.StartOptions) (claude.Started, error) {
			return claude.Started{ID: "e8cdf5d8"}, nil
		},
	}
	e.app.claude = e.claude
	e.app.awake = fixedAwake(true)
	e.app.loopPoll = time.Millisecond
	return e, re
}

// addDueRunnerLoop adds loop deps, a ws loop on endpoint gpu with model
// alias qwen9b, due a minute ago.
func (e *tickEnv) addDueRunnerLoop(t *testing.T) {
	t.Helper()
	l := loop.Loop{
		Name:      "deps",
		Every:     "1h",
		Prompt:    "Do it",
		Keep:      5,
		Runner:    loop.RunnerWS,
		Endpoint:  "gpu",
		Model:     "qwen9b",
		CreatedAt: testNow().Add(-2 * time.Hour),
		NextRunAt: testNow().Add(-time.Minute),
	}
	require.NoError(t, e.loops.Create(&l))
}
