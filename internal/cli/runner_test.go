package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/runner"
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
		assert.ElementsMatch(t, []string{"check", "run", "stop"}, names)
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
	t.Run("a workspace with one repo runs the agent in the repo's directory", func(t *testing.T) {
		app, log := runnerAppForRepos(t, "api")
		require.NoError(t, app.runRunner(t.Context(), "ws-1", false))
		assert.Contains(t, log.all(), "instructions from api")
		assert.Contains(t, log.all(), "instructions from the workspace root")
	})
	t.Run("a workspace with two repos runs the agent in the workspace root", func(t *testing.T) {
		app, log := runnerAppForRepos(t, "api", "web")
		require.NoError(t, app.runRunner(t.Context(), "ws-1", false))
		assert.Contains(t, log.all(), "instructions from the workspace root")
		assert.NotContains(t, log.all(), "instructions from api")
		assert.NotContains(t, log.all(), "instructions from web")
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

// runnerAppForRepos is an app whose workspace ws-1 has the repos, each with a
// CLAUDE.md naming it beside the workspace root's. The agent reads the
// instructions in the directory it works in, so the request shows which that was.
func runnerAppForRepos(t *testing.T, repos ...string) (*App, *requestLog) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	model, log := recordingRunnerModel(t)
	app, _, _ := newRunnerTestApp(t, root)
	workspaceDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, "CLAUDE.md"), []byte("instructions from the workspace root"), 0o600))
	ws := workspace.Workspace{ID: "ws-1", Path: workspaceDir}
	for _, name := range repos {
		require.NoError(t, os.MkdirAll(filepath.Join(workspaceDir, name), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(workspaceDir, name, "CLAUDE.md"), []byte("instructions from "+name), 0o600))
		ws.Repos = append(ws.Repos, workspace.Repo{ID: "example.test/o/" + name, Path: name})
	}
	app.workspaces = &workspaceStoreMock{
		FindFunc: func(string) (workspace.Workspace, error) { return ws, nil },
	}
	writeRunnerFiles(t, state, model)
	return app, log
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
	ts, _ := recordingRunnerModel(t)
	return ts
}

// requestLog is the chat request bodies a recordingRunnerModel received.
type requestLog struct {
	mu     sync.Mutex
	bodies []string
}

func (l *requestLog) add(body string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.bodies = append(l.bodies, body)
}

func (l *requestLog) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.bodies, "\n")
}

// recordingRunnerModel is runnerModel that also keeps the request bodies.
func recordingRunnerModel(t *testing.T) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		log.add(string(body))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":"call_pass","index":0,"type":"function","function":{"name":"record_result","arguments":"{\"verdict\":\"PASS\",\"report\":\"done\"}"}}]},"finish_reason":null}]}

data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"qwen/qwen3.5-9b","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}

data: [DONE]

`)
	}))
	t.Cleanup(ts.Close)
	return ts, log
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

// serveRunnerDaemon runs an in-process managed daemon with the real
// supervisor for app's config directory, until the test ends. Runners are not
// resumed at start, so nothing is started.
func serveRunnerDaemon(t *testing.T, app *App) {
	t.Helper()
	resume := false
	app.config.Daemon.ResumeOnStart = &resume
	ctx, cancel := context.WithCancel(daemon.WithVersion(t.Context(), app.version))
	t.Cleanup(cancel)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sup := daemon.NewSupervisor(app.supervisorConfig(os.Args[0]), proc.Reader{}, daemon.OSStarter{}, log)
	go func() { _ = daemon.Serve(ctx, app.daemonPaths(), daemon.OriginManaged, sup, log) }()
	require.Eventually(t, func() bool {
		_, err := daemon.Dial(app.daemonPaths()).Status(ctx)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

// writeRunnerConfig writes app's config.toml: one endpoint, gpu, at url, serving
// gemma3:12b with no probe.
func writeRunnerConfig(t *testing.T, app *App, url string) {
	t.Helper()
	body := fmt.Sprintf("[runner]\nmodel = \"gemma\"\n\n[endpoints.gpu]\nurl = %q\nprobe = \"none\"\n\n"+
		"[models.gemma]\nendpoint = \"gpu\"\nid = \"gemma3:12b\"\ncontext_tokens = 32768\n", url)
	require.NoError(t, os.WriteFile(app.paths.ConfigFile, []byte(body), 0o600))
}

// modelsEndpoint is an httptest server whose /v1/models lists gemma3:12b.
func modelsEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"gemma3:12b","object":"model"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// closedEndpoint is an address nothing listens on.
func closedEndpoint(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return "http://" + addr
}

func TestApp_newRunnerCheckCmd(t *testing.T) {
	t.Run("takes at most one endpoint", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newRunnerCheckCmd()
		require.NoError(t, cmd.Args(cmd, nil))
		require.NoError(t, cmd.Args(cmd, []string{"gpu"}))
		requireUsageError(t, cmd.Args(cmd, []string{"gpu", "lab"}))
	})
	t.Run("has a model flag", func(t *testing.T) {
		app, _, _ := newTestApp()
		assert.NotNil(t, app.newRunnerCheckCmd().Flags().Lookup("model"))
	})
}

func TestApp_runRunnerCheck(t *testing.T) {
	t.Run("prints the check object as JSON with --json", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.global.json = true
		writeRunnerConfig(t, app, modelsEndpoint(t))
		serveRunnerDaemon(t, app)
		require.NoError(t, app.runRunnerCheck(t.Context(), []string{"gpu"}, "gemma"))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, "gpu", obj["endpoint"])
		assert.Equal(t, "gemma3:12b", obj["model"])
		assert.Equal(t, []any{"gemma3:12b"}, obj["models"])
	})
	t.Run("prints a line without --json", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		url := modelsEndpoint(t)
		writeRunnerConfig(t, app, url)
		serveRunnerDaemon(t, app)
		require.NoError(t, app.runRunnerCheck(t.Context(), nil, ""))
		assert.Contains(t, stdout.String(), "gpu ("+url+"): gemma3:12b, context 32768 tokens")
	})
	t.Run("an endpoint that doesn't answer keeps its code and url", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		url := closedEndpoint(t)
		writeRunnerConfig(t, app, url)
		serveRunnerDaemon(t, app)
		ce := asCLIError(app.runRunnerCheck(t.Context(), []string{"gpu"}, ""))
		assert.Equal(t, codeEndpointUnreachable, ce.code)
		assert.Equal(t, url, ce.details["url"])
	})
	t.Run("with autostart off and no daemon it is daemon_unavailable", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		app.environ = append(app.environ, "WS_DAEMON_AUTOSTART=0")
		assert.Equal(t, codeDaemonUnavailable, asCLIError(app.runRunnerCheck(t.Context(), nil, "")).code)
	})
}

func TestApp_newRunnerStopCmd(t *testing.T) {
	t.Run("needs exactly one workspace", func(t *testing.T) {
		app, _, _ := newTestApp()
		cmd := app.newRunnerStopCmd()
		requireUsageError(t, cmd.Args(cmd, nil))
	})
}

func TestApp_runRunnerStop(t *testing.T) {
	t.Run("a workspace without a runner session is workspace_not_found", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		assert.Equal(t, codeWorkspaceNotFound, asCLIError(app.runRunnerStop(t.Context(), "ws-1")).code)
	})
	t.Run("an id that isn't one path element is workspace_not_found without reading outside the state", func(t *testing.T) {
		app, _, _ := newDaemonTestApp(t)
		outside := runner.FilesFor(filepath.Dir(app.paths.State), "x")
		require.NoError(t, outside.WriteRun(&runner.Run{Version: 1, Session: "rn-x"}))
		assert.Equal(t, codeWorkspaceNotFound, asCLIError(app.runRunnerStop(t.Context(), "../x")).code)
	})
	t.Run("stops a runner that isn't running and prints its object", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		app.global.json = true
		files := runner.FilesFor(app.paths.State, "ws-1")
		require.NoError(t, files.WriteRun(&runner.Run{Version: 1, Session: "rn-1"}))
		require.NoError(t, files.WriteStatus(&runner.Status{Session: "rn-1", PID: 999999}))
		serveRunnerDaemon(t, app)
		require.NoError(t, app.runRunnerStop(t.Context(), "ws-1"))
		obj := decodeJSONObject(t, stdout)
		assert.Equal(t, "ws-1", obj["id"])
		assert.Equal(t, "rn-1", obj["session"])
		assert.Equal(t, true, obj["stopped"])
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, "stopped", *st.Exit)
	})
	t.Run("a runner that had exited says so", func(t *testing.T) {
		app, stdout, _ := newDaemonTestApp(t)
		files := runner.FilesFor(app.paths.State, "ws-1")
		require.NoError(t, files.WriteRun(&runner.Run{Version: 1, Session: "rn-1"}))
		exit := "stopped"
		require.NoError(t, files.WriteStatus(&runner.Status{Session: "rn-1", PID: 999999, Exit: &exit}))
		serveRunnerDaemon(t, app)
		require.NoError(t, app.runRunnerStop(t.Context(), "ws-1"))
		assert.Contains(t, stdout.String(), "runner rn-1 had already exited")
	})
}

func TestRemoteRunnerError(t *testing.T) {
	t.Parallel()
	t.Run("a plain error is returned as it is", func(t *testing.T) {
		plain := errors.New("plain")
		assert.Equal(t, plain, remoteRunnerError(plain))
	})
	t.Run("a remote error keeps its code and details", func(t *testing.T) {
		ce := asCLIError(remoteRunnerError(&daemon.RemoteError{Code: "model_not_served", Details: map[string]any{"model": "m"}}))
		assert.Equal(t, codeModelNotServed, ce.code)
		assert.Equal(t, "m", ce.details["model"])
		assert.Equal(t, exitNotFound, ce.exitCode())
	})
}

func TestApp_supervisorConfig(t *testing.T) {
	t.Parallel()
	app, _, _ := newDaemonTestApp(t)
	app.config.Daemon.ResumeOnStart = nil
	cfg := app.supervisorConfig("/bin/ws")
	assert.Equal(t, app.paths.State, cfg.StateDir)
	assert.Equal(t, app.paths.Root, cfg.ConfigDir)
	assert.Equal(t, "/bin/ws", cfg.Exe)
	assert.True(t, cfg.ResumeOnStart)
}
