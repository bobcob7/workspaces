package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestApp_newRunnerCmd(t *testing.T) {
	t.Run("is hidden and has a run operation", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newRunnerCmd()
		assert.True(t, cmd.Hidden)
		var names []string
		for _, sub := range cmd.Commands() {
			names = append(names, sub.Name())
		}
		assert.ElementsMatch(t, []string{"run"}, names)
	})
	t.Run("without an operation prints its help", func(t *testing.T) {
		app, stdout, _ := newTestApp()
		cmd := app.newRunnerCmd()
		cmd.SetOut(stdout)
		require.NoError(t, cmd.RunE(cmd, nil))
		assert.Contains(t, stdout.String(), "run")
	})
}

func TestApp_newRunnerRunCmd(t *testing.T) {
	t.Run("needs exactly one workspace", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newRunnerRunCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
	t.Run("has a resume flag", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newRunnerRunCmd()
		flag := cmd.Flags().Lookup("resume")
		require.NotNil(t, flag)
		assert.Equal(t, "false", flag.DefValue)
	})
}

func TestApp_runRunner(t *testing.T) {
	t.Run("an unknown workspace is an error", func(t *testing.T) {
		app, _, _ := newRunnerTestApp(t, t.TempDir())
		app.workspaces = &workspaceStoreMock{
			FindFunc: func(string) (workspace.Workspace, error) { return workspace.Workspace{}, errors.New("no workspace") },
		}
		err := app.runRunner(t.Context(), "ws-1", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "finding workspace ws-1")
	})
	t.Run("runs the session to its verdict", func(t *testing.T) {
		root := t.TempDir()
		state := filepath.Join(root, "state")
		model := runnerModel(t)
		app, _, _ := newRunnerTestApp(t, root)
		workspaceDir := t.TempDir()
		app.workspaces = &workspaceStoreMock{
			FindFunc: func(string) (workspace.Workspace, error) {
				return workspace.Workspace{ID: "ws-1", Path: workspaceDir}, nil
			},
		}
		writeRunnerFiles(t, state, model)
		require.NoError(t, app.runRunner(t.Context(), "ws-1", false))
		data, err := os.ReadFile(filepath.Join(state, "ws-1", "runner", "status.json"))
		require.NoError(t, err)
		assert.Contains(t, string(data), `"verdict":"PASS"`)
		assert.Contains(t, string(data), `"exit":"done"`)
	})
	t.Run("a run.toml that can't be read is an internal error", func(t *testing.T) {
		app, _, _ := newRunnerTestApp(t, t.TempDir())
		app.workspaces = &workspaceStoreMock{
			FindFunc: func(string) (workspace.Workspace, error) {
				return workspace.Workspace{ID: "ws-1", Path: t.TempDir()}, nil
			},
		}
		err := app.runRunner(t.Context(), "ws-1", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "reading run.toml")
	})
}

func TestRunnerRecords_Lineage(t *testing.T) {
	t.Run("is the root and the ancestors of a child, as ws machine result computes them", func(t *testing.T) {
		store := &workspaceStoreMock{
			ListFunc: func() ([]workspace.Workspace, error) {
				return []workspace.Workspace{
					{ID: "root-1"},
					{ID: "mid-2", Parent: "root-1"},
					{ID: "child-3", Parent: "mid-2"},
				}, nil
			},
		}
		records := runnerRecords{Store: machine.NewStore(t.TempDir()), workspaces: store}
		root, ancestors, err := records.Lineage("child-3")
		require.NoError(t, err)
		assert.Equal(t, "root-1", root)
		assert.Equal(t, []string{"mid-2", "root-1"}, ancestors)
	})
	t.Run("a list failure is an error", func(t *testing.T) {
		store := &workspaceStoreMock{
			ListFunc: func() ([]workspace.Workspace, error) { return nil, errors.New("disk gone") },
		}
		_, _, err := runnerRecords{Store: machine.NewStore(t.TempDir()), workspaces: store}.Lineage("child-3")
		require.Error(t, err)
	})
}

// newRunnerTestApp is an App whose config lives under root, with the
// endpoint token in its environment.
func newRunnerTestApp(t *testing.T, root string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	app, stdout, stderr := newTestApp()
	paths := config.NewPaths(root)
	app.configs = &configLoaderMock{
		LoadFunc: func(string) (config.Paths, config.Config, error) { return paths, config.Config{}, nil },
	}
	app.newStores = func(config.Paths, config.Config) stores {
		return stores{workspaces: stubWorkspaces()}
	}
	app.paths = paths
	app.environ = []string{"WS_ENDPOINT_TOKEN_GPU=cli-token-123"}
	app.workspaces = stubWorkspaces()
	return app, stdout, stderr
}

// runnerModel answers every chat request with one record_result PASS.
func runnerModel(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_pass","index":0,"type":"function","function":{"name":"record_result","arguments":"{\"verdict\":\"PASS\",\"report\":\"done\"}"}}]},"finish_reason":null}]}

data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

// writeRunnerFiles writes a run that talks to model, in state's ws-1.
func writeRunnerFiles(t *testing.T, state string, model *httptest.Server) {
	t.Helper()
	created := time.Date(2026, 11, 13, 17, 45, 58, 0, time.UTC)
	toml := fmt.Sprintf(`version = 1
session = "rn-3f2a9c1e"
model_alias = "qwen9b"
model = "qwen/qwen3.5-9b"
endpoint = "gpu"
url = %q
api = "openai"
context_tokens = 65536
max_output = 4096
temperature = 0.2
reasoning = "none"
request_timeout = "10m"
idle_timeout = "90s"
role = "implementer"
parent = ""
pass = ["PASS"]
fail = ["FAIL"]
max_steps = 4
timeout = "2m"
max_tokens = 100000
branch = ""
mcp_servers = []
mcp_tools = ["*"]
mcp_deny = []
mcp_upfront = 8
user_instructions = false
prompt = "Say done."
trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"
created_at = %s
endpoint_alias = ""
endpoint_flag = ""

[[endpoint_history]]
since = %s
step = 0
reason = "spawn"
endpoint = "gpu"
url = %q
api = "openai"
model = "qwen/qwen3.5-9b"
context_tokens = 65536
`, model.URL, created.Format(time.RFC3339), created.Format(time.RFC3339), model.URL)
	dir := filepath.Join(state, "ws-1", "runner")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run.toml"), []byte(toml), 0o600))
}
