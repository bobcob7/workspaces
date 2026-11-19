package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/runner"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// runnerToml is a config.toml with a ws runner: model alias qwen9b on
// endpoint gpu, and a second endpoint, cpu.
const runnerToml = `[runner]
model = "qwen9b"

[endpoints.gpu]
url = "http://gpu-a.inference.svc:1234"

[endpoints.cpu]
url = "http://cpu.example:1234"

[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 65536
`

// runnerEnv is a test App whose config is toml, whose workspaces are created
// as fix-login-d8efb24f, and whose daemon is daemon. Its daemon starts a
// runner by writing the status a runner writes first.
type runnerEnv struct {
	*mockedApp
	daemon *runnerDaemonMock
}

func newRunnerEnv(t *testing.T, toml string) *runnerEnv {
	t.Helper()
	e := newMockedApp()
	e.app.paths = config.NewPaths(t.TempDir())
	e.app.config = loadRunnerConfig(t, toml)
	paths, cfg := e.app.paths, e.app.config
	e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) { return paths, cfg, nil }}
	lockPath := filepath.Join(t.TempDir(), "lock")
	e.store.CreateFunc = func(_ context.Context, opts workspace.CreateOptions) (workspace.Workspace, *lockfile.Lock, error) {
		lock, ok, err := lockfile.TryExclusiveLock(lockPath)
		require.NoError(t, err)
		require.True(t, ok)
		return workspace.Workspace{ID: "fix-login-d8efb24f", Path: "/cfg/workspaces/fix-login-d8efb24f", Parent: opts.Parent}, lock, nil
	}
	e.store.RecordRunnerSessionFunc = func(string, string) error { return nil }
	e.store.RemoveFunc = func(string, workspace.RemoveOptions) error { return nil }
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{ID: "plan"}, nil }
	state := e.app.paths.State
	d := &runnerDaemonMock{
		PreflightFunc: func(context.Context, daemon.PreflightRequest) (daemon.PreflightResponse, error) {
			return daemon.PreflightResponse{}, nil
		},
		StartRunnerFunc: func(_ context.Context, id string, _ bool) (int, error) {
			if err := runner.FilesFor(state, id).WriteStatus(&runner.Status{}); err != nil {
				return 0, err
			}
			return 4242, nil
		},
		StopRunnerFunc: func(context.Context, string) (bool, error) { return false, nil },
	}
	e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return d, nil }
	e.app.runnerWait = 2 * time.Second
	e.app.runnerStopWait = 2 * time.Second
	e.app.now = testNow
	return &runnerEnv{mockedApp: e, daemon: d}
}

// loadRunnerConfig reads toml as config.toml.
func loadRunnerConfig(t *testing.T, toml string) config.Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(toml), 0o600))
	cfg, err := config.ReadFile(path)
	require.NoError(t, err)
	return cfg
}

// readRun reads workspace id's run.toml.
func readRun(t *testing.T, e *runnerEnv, id string) runner.Run {
	t.Helper()
	run, err := runner.FilesFor(e.app.paths.State, id).ReadRun()
	require.NoError(t, err)
	return run
}

func TestApp_startRunnerAgent(t *testing.T) {
	t.Run("starts the runner and writes its run", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		got, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Parent: "plan", Prompt: "fix it", Vars: map[string]string{"BRANCH": "feat/x"}})
		require.NoError(t, err)
		assert.Equal(t, "fix-login-d8efb24f", got.Workspace.ID)
		assert.True(t, strings.HasPrefix(got.Session, runner.SessionPrefix))
		run := readRun(t, e, "fix-login-d8efb24f")
		assert.Equal(t, got.Session, run.Session)
		assert.Equal(t, "qwen9b", run.ModelAlias)
		assert.Equal(t, "qwen/qwen3.5-9b", run.Model)
		assert.Equal(t, "gpu", run.Endpoint)
		assert.Empty(t, run.EndpointAlias)
		assert.Empty(t, run.EndpointFlag)
		assert.Equal(t, "http://gpu-a.inference.svc:1234", run.URL)
		assert.Equal(t, 65536, run.ContextTokens)
		assert.Equal(t, "fix it", run.Prompt)
		assert.Equal(t, "plan", run.Parent)
		assert.Empty(t, run.Role)
		assert.Equal(t, []string{"PASS"}, run.Pass)
		assert.Equal(t, []string{"FAIL"}, run.Fail)
		assert.Equal(t, "feat/x", run.Branch)
		assert.NotEmpty(t, run.TraceID)
		require.Len(t, run.EndpointHistory, 1)
		assert.Equal(t, "spawn", run.EndpointHistory[0].Reason)
		assert.Equal(t, 65536, run.EndpointHistory[0].ContextTokens)
		require.Len(t, e.store.RecordRunnerSessionCalls(), 1)
		assert.Equal(t, got.Session, e.store.RecordRunnerSessionCalls()[0].RunnerSessionID)
		require.Len(t, e.daemon.StartRunnerCalls(), 1)
		assert.False(t, e.daemon.StartRunnerCalls()[0].Resume)
	})
	t.Run("a raw model id keeps its text, on the endpoint of the alias the run would use", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		got, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Model: "qwen3.5:9b", Prompt: "fix it"})
		require.NoError(t, err)
		run := readRun(t, e, got.Workspace.ID)
		assert.Equal(t, "qwen3.5:9b", run.Model)
		assert.Empty(t, run.ModelAlias)
		assert.Equal(t, "qwen9b", run.EndpointAlias)
		assert.Equal(t, "gpu", run.Endpoint)
		assert.Contains(t, strings.Join(got.Warnings, "\n"), "isn't a model alias")
	})
	t.Run("--endpoint sets the endpoint flag and its url", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		got, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Endpoint: "cpu", Prompt: "fix it"})
		require.NoError(t, err)
		run := readRun(t, e, got.Workspace.ID)
		assert.Equal(t, "cpu", run.EndpointFlag)
		assert.Equal(t, "cpu", run.Endpoint)
		assert.Equal(t, "http://cpu.example:1234", run.URL)
	})
	t.Run("a preflight window smaller than the configured one is the run's window, and the spawn's", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.PreflightFunc = func(context.Context, daemon.PreflightRequest) (daemon.PreflightResponse, error) {
			return daemon.PreflightResponse{ContextTokens: 32768, Warnings: []string{"the model's context is 32768"}}, nil
		}
		got, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		require.NoError(t, err)
		run := readRun(t, e, got.Workspace.ID)
		assert.Equal(t, 32768, run.ContextTokens)
		require.Len(t, run.EndpointHistory, 1)
		assert.Equal(t, 32768, run.EndpointHistory[0].ContextTokens)
		assert.Equal(t, 32768, got.Resolved.ContextTokens)
		assert.Equal(t, 32768, runnerSpawnJSON("", &got, &claudeResult{}).WSRunner.ContextTokens)
		assert.Contains(t, got.Warnings, "the model's context is 32768")
	})
	t.Run("no model anywhere is config_invalid, and nothing is created", func(t *testing.T) {
		e := newRunnerEnv(t, "[endpoints.gpu]\nurl = \"http://gpu-a.inference.svc:1234\"\n")
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		requireCode(t, codeConfigInvalid, err)
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("an unknown endpoint is endpoint_unknown, with the endpoints there are", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Endpoint: "nope", Prompt: "fix it"})
		requireCode(t, codeEndpointUnknown, err)
		ce, _ := errors.AsType[*cliError](err)
		assert.Equal(t, []string{"cpu", "gpu"}, ce.details["endpoints"])
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a failed check is returned with the daemon's code and nothing is created", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.PreflightFunc = func(context.Context, daemon.PreflightRequest) (daemon.PreflightResponse, error) {
			return daemon.PreflightResponse{}, &daemon.RemoteError{Code: "model_not_served", Message: "not served", Details: map[string]any{"model": "qwen/qwen3.5-9b"}}
		}
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		requireCode(t, codeModelNotServed, err)
		assert.Empty(t, e.store.CreateCalls())
		assert.Empty(t, e.daemon.StartRunnerCalls())
	})
	t.Run("without a daemon nothing is created", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.dialDaemon = func(context.Context) (runnerDaemon, error) { return nil, newError(codeDaemonUnavailable, errTest) }
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		requireCode(t, codeDaemonUnavailable, err)
		assert.Empty(t, e.store.CreateCalls())
	})
	t.Run("a runner that writes no status in time is runner_failed, and its workspace is removed", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.app.runnerWait = 50 * time.Millisecond
		e.daemon.StartRunnerFunc = func(context.Context, string, bool) (int, error) { return 4242, nil }
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		requireCode(t, codeRunnerFailed, err)
		ce, _ := errors.AsType[*cliError](err)
		assert.Equal(t, runner.FilesFor(e.app.paths.State, "fix-login-d8efb24f").Log(), ce.details["log"])
		assert.Len(t, e.daemon.StopRunnerCalls(), 1)
		assert.Equal(t, []string{"fix-login-d8efb24f"}, removedWorkspaces(e))
	})
	t.Run("a runner the daemon won't start is runner_failed", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.daemon.StartRunnerFunc = func(context.Context, string, bool) (int, error) {
			return 0, &daemon.RemoteError{Code: "runner_failed", Message: "runner didn't start"}
		}
		_, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Prompt: "fix it"})
		requireCode(t, codeRunnerFailed, err)
		assert.Equal(t, []string{"fix-login-d8efb24f"}, removedWorkspaces(e))
	})
	t.Run("a role renders its prompt, and its run keeps the role's verdicts", func(t *testing.T) {
		e := newRunnerEnv(t, runnerToml)
		e.store.ListFunc = func() ([]workspace.Workspace, error) {
			return []workspace.Workspace{{ID: "fix-login-d8efb24f", Path: "/cfg/workspaces/fix-login-d8efb24f"}}, nil
		}
		vars := map[string]string{"TASK": "T1", "TASKS": "docs/tasks.md", "BASE": "feat/x", "BRANCH": "feat/x-t1"}
		got, err := e.app.startRunnerAgent(t.Context(), runnerStart{Name: "Fix Login", Role: "implementer", Vars: vars, Prompt: "extra"})
		require.NoError(t, err)
		run := readRun(t, e, got.Workspace.ID)
		assert.Equal(t, "implementer", run.Role)
		assert.Equal(t, []string{"PASS"}, run.Pass)
		assert.Equal(t, []string{"FAIL"}, run.Fail)
		assert.Contains(t, run.Prompt, "T1")
		assert.Equal(t, "feat/x-t1", run.Branch)
		spawn := filepath.Join(machine.NewStore(e.app.paths.State).Dir("fix-login-d8efb24f", "fix-login-d8efb24f"), "spawn.toml")
		assert.FileExists(t, spawn)
		assert.Empty(t, e.app.warned)
	})
}

// removedWorkspaces is the ids the store removed, in order.
func removedWorkspaces(e *runnerEnv) []string {
	var ids []string
	for _, call := range e.store.RemoveCalls() {
		ids = append(ids, call.ID)
	}
	return ids
}

func TestLogTail(t *testing.T) {
	t.Run("keeps the last bytes of the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runner.log")
		require.NoError(t, os.WriteFile(path, []byte("abcdef"), 0o600))
		assert.Equal(t, "def", logTail(path, 3))
	})
	t.Run("a file that can't be read is empty", func(t *testing.T) {
		assert.Empty(t, logTail(filepath.Join(t.TempDir(), "missing"), 3))
	})
}
