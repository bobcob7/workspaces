package runner

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

const testToken = "sekrit-token-123"

// modelServer is an OpenAI-compatible endpoint that answers each chat
// request with reply, which gets the 1-based request number.
type modelServer struct {
	mu     sync.Mutex
	bodies []string
	auth   []string
	URL    string
}

func newModelServer(t *testing.T, reply func(n int, w http.ResponseWriter)) *modelServer {
	t.Helper()
	m := &modelServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.bodies = append(m.bodies, string(body))
		m.auth = append(m.auth, r.Header.Get("Authorization"))
		n := len(m.bodies)
		m.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		reply(n, w)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	m.URL = ts.URL
	return m
}

// requests is how many chat requests the server got.
func (m *modelServer) requests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.bodies)
}

// lastBody is the last chat request's body.
func (m *modelServer) lastBody() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.bodies) == 0 {
		return ""
	}
	return m.bodies[len(m.bodies)-1]
}

// script answers request n with replies[n-1], the last one repeating.
func script(replies ...string) func(int, http.ResponseWriter) {
	return func(n int, w http.ResponseWriter) {
		_, _ = io.WriteString(w, replies[min(n, len(replies))-1])
	}
}

// toolSSE is a streamed answer that calls one tool, in the fake-ollama
// fixtures' shape (stream-tools.response.txt).
func toolSSE(id, name, args string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1759580000,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"id":%q,"index":0,"type":"function","function":{"name":%q,"arguments":%q}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1759580000,"model":"qwen/qwen3.5-9b","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1759580000,"model":"qwen/qwen3.5-9b","choices":[],"usage":{"prompt_tokens":182,"completion_tokens":22,"total_tokens":204}}

data: [DONE]

`, id, name, args)
}

// recordPass is the answer that ends a run with PASS.
var recordPass = toolSSE("call_pass", "record_result", `{"verdict":"PASS","report":"all good"}`)

// newRunFixture writes a run that talks to url, with hello.txt in its
// workspace, and returns its files and workspace directory.
func newRunFixture(t *testing.T, url string, mutate func(*Run)) (Files, string) {
	t.Helper()
	state := t.TempDir()
	workspace := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "hello.txt"), []byte("hello from ws\n"), fileMode))
	files := FilesFor(state, "ws-1")
	run := sampleRun()
	run.Endpoint = "gpu"
	run.URL = url
	run.EndpointHistory = []EndpointEntry{{Since: run.CreatedAt, Reason: "spawn", Endpoint: "gpu", URL: url, API: "openai", Model: run.Model, ContextTokens: run.ContextTokens}}
	run.MaxSteps = 6
	run.Timeout = 2 * time.Minute
	run.MaxTokens = 100000
	run.Prompt = "Check hello.txt and report."
	run.Parent = ""
	run.Branch = ""
	run.MCPServers = nil
	run.MCPTools = []string{"*"}
	run.MCPDeny = nil
	run.MCPUpfront = 8
	run.UserInstructions = false
	if mutate != nil {
		mutate(&run)
	}
	require.NoError(t, files.WriteRun(&run))
	return files, workspace
}

// testInput is the input for a run in files and workspace, with the token in
// the environment and retries that don't wait.
func testInput(t *testing.T, files Files, workspace string, resume bool) *Input {
	t.Helper()
	return &Input{
		Files:     files,
		Resume:    resume,
		Workspace: workspace,
		ConfigDir: t.TempDir(),
		Environ: []string{
			"PATH=" + os.Getenv("PATH"),
			"WS_ENDPOINT_TOKEN_GPU=" + testToken,
			"WS_RUNNER_SECRET_NAMES=DB_PASSWORD",
			"DB_PASSWORD=hunter2hunter2",
		},
		Store: &resultStoreMock{},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func readTranscript(t *testing.T, files Files) string {
	t.Helper()
	data, err := os.ReadFile(files.Transcript())
	require.NoError(t, err)
	return string(data)
}

func TestExecute(t *testing.T) {
	t.Parallel()
	t.Run("a run with no run.toml is an error", func(t *testing.T) {
		t.Parallel()
		files := FilesFor(t.TempDir(), "ws-1")
		err := Execute(t.Context(), testInput(t, files, t.TempDir(), false))
		require.Error(t, err)
	})
	t.Run("a happy path records PASS and writes usage and status", func(t *testing.T) {
		t.Parallel()
		model := newModelServer(t, script(
			toolSSE("c1", "read_file", `{"path":"hello.txt"}`),
			toolSSE("c2", "list_files", `{"path":"."}`),
			recordPass,
		))
		files, workspace := newRunFixture(t, model.URL, nil)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		assert.Equal(t, "exited", st.State)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitDone, *st.Exit)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "PASS", *st.Verdict)
		assert.Equal(t, 3, model.requests())
		report, err := os.ReadFile(files.path(reportFile))
		require.NoError(t, err)
		assert.Equal(t, "all good", string(report))
		data, err := os.ReadFile(files.path(usageFile))
		require.NoError(t, err)
		var usage Usage
		require.NoError(t, json.Unmarshal(data, &usage))
		assert.Equal(t, agent.ExitDone, usage.Exit)
		assert.Equal(t, 3, usage.Usage.Requests)
		assert.Equal(t, "gpu", usage.Endpoint)
		assert.Equal(t, model.URL, usage.URL)
		assert.Equal(t, testTraceID, usage.TraceID)
	})
	t.Run("replays the fake-ollama stream fixture, whose unknown tool is an invalid call", func(t *testing.T) {
		t.Parallel()
		fixture, err := os.ReadFile(filepath.Join("..", "fakeollama", "testdata", "stream-tools.response.txt"))
		require.NoError(t, err)
		model := newModelServer(t, script(string(fixture), recordPass))
		files, workspace := newRunFixture(t, model.URL, nil)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		assert.Contains(t, readTranscript(t, files), `"kind":"invalid_call"`)
		assert.Contains(t, readTranscript(t, files), `"name":"lookup_order_status"`)
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "PASS", *st.Verdict)
	})
	t.Run("the token reaches the endpoint but never the transcript or the log", func(t *testing.T) {
		t.Parallel()
		model := newModelServer(t, script(recordPass))
		files, workspace := newRunFixture(t, model.URL, nil)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		assert.Equal(t, "Bearer "+testToken, model.auth[0])
		assert.NotContains(t, readTranscript(t, files), testToken)
		logData, err := os.ReadFile(files.Log())
		require.NoError(t, err)
		assert.NotContains(t, string(logData), testToken)
	})
	t.Run("an endpoint that is always down ends endpoint_down with a FAIL report", func(t *testing.T) {
		t.Parallel()
		model := newModelServer(t, func(_ int, w http.ResponseWriter) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		})
		files, workspace := newRunFixture(t, model.URL, nil)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitEndpointDown, *st.Exit)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "FAIL", *st.Verdict)
		report, err := os.ReadFile(files.path(reportFile))
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(report), "ws runner: endpoint_down\n"))
	})
	t.Run("a config.toml that can't be read ends the run internal, before any request", func(t *testing.T) {
		t.Parallel()
		model := newModelServer(t, script(recordPass))
		files, workspace := newRunFixture(t, model.URL, nil)
		in := testInput(t, files, workspace, false)
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte("[runner"), fileMode))
		require.NoError(t, Execute(t.Context(), in))

		assert.Zero(t, model.requests())
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitInternal, *st.Exit)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "FAIL", *st.Verdict)
	})
	t.Run("a stopped run resumes from its transcript on the endpoint it ended on", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		first := newModelServer(t, func(n int, w http.ResponseWriter) {
			if n == 1 {
				_, _ = io.WriteString(w, toolSSE("c1", "read_file", `{"path":"hello.txt"}`))
				return
			}
			cancel()
			http.Error(w, "stop", http.StatusServiceUnavailable)
		})
		files, workspace := newRunFixture(t, first.URL, nil)
		require.NoError(t, Execute(ctx, testInput(t, files, workspace, false)))
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitStopped, *st.Exit)
		assert.Nil(t, st.Verdict)
		second := newModelServer(t, script(recordPass))
		require.NoError(t, files.AppendEndpoint(EndpointEntry{Reason: "retry", Endpoint: "gpu", URL: second.URL, API: "openai", Model: "qwen/qwen3.5-9b"}))
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, true)))
		assert.Contains(t, second.lastBody(), `"role":"tool"`)
		assert.Equal(t, 1, second.requests())
		assert.Contains(t, readTranscript(t, files), `"kind":"restart"`)
		st, err = files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "PASS", *st.Verdict)
	})
	t.Run("without OTEL variables nothing is printed as a span", func(t *testing.T) {
		t.Parallel()
		model := newModelServer(t, script(recordPass))
		files, workspace := newRunFixture(t, model.URL, nil)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		logData, err := os.ReadFile(files.Log())
		require.NoError(t, err)
		assert.NotContains(t, string(logData), `"Name": "`)
	})
	t.Run("an MCP tool call counts toward its server and its start is traced", func(t *testing.T) {
		t.Parallel()
		server := serveMetricsServer(t)
		model := newModelServer(t, script(
			toolSSE("c1", "mcp__metrics__ping", `{}`),
			recordPass,
		))
		files, workspace := newRunFixture(t, model.URL, func(run *Run) {
			run.MCPServers = []string{"metrics"}
		})
		writeMCPJSON(t, workspace, server)
		require.NoError(t, Execute(t.Context(), testInput(t, files, workspace, false)))
		data, err := os.ReadFile(files.path(usageFile))
		require.NoError(t, err)
		var usage Usage
		require.NoError(t, json.Unmarshal(data, &usage))
		assert.Equal(t, 1, usage.MCPCalls["metrics"])
		transcript := readTranscript(t, files)
		assert.Contains(t, transcript, `"kind":"mcp_start"`)
		assert.Contains(t, transcript, `"ok":true`)
		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.Len(t, st.MCP, 1)
		assert.Equal(t, 1, st.MCP[0].Loaded)
	})
}

// TestExecute_consoleSpans runs a run in a child process with the console span
// exporter, which writes to the process's stdout as it was at start, and
// checks the spans it prints.
func TestExecute_consoleSpans(t *testing.T) {
	if os.Getenv(helperEnv) == "1" {
		runConsoleHelper(t)
		return
	}
	model := newModelServer(t, script(
		toolSSE("c1", "read_file", `{"path":"hello.txt"}`),
		toolSSE("c2", "list_files", `{"path":"."}`),
		recordPass,
	))
	files, workspace := newRunFixture(t, model.URL, nil)
	state := filepath.Dir(filepath.Dir(files.Dir))
	out := runConsoleChild(t, state, workspace, false)
	spans := out
	assert.Equal(t, 1, strings.Count(spans, `"Name":"invoke_agent implementer"`))
	assert.Equal(t, 3, strings.Count(spans, `"Name":"chat `))
	assert.Equal(t, 3, strings.Count(spans, `"Name":"execute_tool `))
	assert.Equal(t, strings.Count(spans, `"TraceID":"`), strings.Count(spans, `"TraceID":"`+testTraceID+`"`))
	assert.NotContains(t, spans, "Check hello.txt and report.")
}

// TestExecute_consoleResume stops a run after its first step and resumes it in
// a second child process: the resume adds a second invoke_agent span in the
// same trace.
func TestExecute_consoleResume(t *testing.T) {
	if os.Getenv(helperEnv) == "1" {
		runConsoleHelper(t)
		return
	}
	var files Files
	var workspace string
	first := newModelServer(t, func(n int, w http.ResponseWriter) {
		if n != 1 {
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		if err := files.RequestStop(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, toolSSE("c1", "read_file", `{"path":"hello.txt"}`))
	})
	files, workspace = newRunFixture(t, first.URL, nil)
	state := filepath.Dir(filepath.Dir(files.Dir))
	firstSpans := runConsoleChild(t, state, workspace, false)
	assert.Equal(t, 1, strings.Count(firstSpans, `"Name":"invoke_agent implementer"`))
	stopped, err := files.ReadStatus()
	require.NoError(t, err)
	require.NotNil(t, stopped.Exit)
	assert.Equal(t, agent.ExitStopped, *stopped.Exit)
	assert.Nil(t, stopped.Verdict)
	require.NoError(t, os.Remove(files.path(stopFile)))

	second := newModelServer(t, script(recordPass))
	require.NoError(t, files.AppendEndpoint(EndpointEntry{Reason: "resume", Endpoint: "gpu", URL: second.URL, API: "openai", Model: "qwen/qwen3.5-9b"}))
	resumeSpans := runConsoleChild(t, state, workspace, true)
	assert.Equal(t, 1, strings.Count(resumeSpans, `"Name":"invoke_agent implementer"`))
	assert.Equal(t, strings.Count(resumeSpans, `"TraceID":"`), strings.Count(resumeSpans, `"TraceID":"`+testTraceID+`"`))
}

// runConsoleChild runs Execute in a child process of this test binary, with the
// console span exporter, and returns the spans it printed.
func runConsoleChild(t *testing.T, state, workspace string, resume bool) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^"+t.Name()+"$")
	cmd.Env = append(os.Environ(), helperEnv+"=1", "OTEL_TRACES_EXPORTER=console",
		"WS_RUNNER_TEST_STATE="+state, "WS_RUNNER_TEST_WORKSPACE="+workspace,
		fmt.Sprintf("WS_RUNNER_TEST_RESUME=%t", resume))
	out, err := cmd.Output()
	require.NoError(t, err)
	return string(out)
}

// helperEnv marks the child process that TestExecute_consoleSpans starts.
const helperEnv = "WS_RUNNER_TEST_CONSOLE_HELPER"

// runConsoleHelper is the child's run: the state and workspace the parent
// wrote, with the environment the parent set.
func runConsoleHelper(t *testing.T) {
	t.Helper()
	files := FilesFor(os.Getenv("WS_RUNNER_TEST_STATE"), "ws-1")
	in := &Input{
		Files:     files,
		Resume:    os.Getenv("WS_RUNNER_TEST_RESUME") == "true",
		Workspace: os.Getenv("WS_RUNNER_TEST_WORKSPACE"),
		ConfigDir: t.TempDir(),
		Environ:   os.Environ(),
		Store:     &resultStoreMock{},
		Sleep:     func(context.Context, time.Duration) error { return nil },
	}
	if err := Execute(t.Context(), in); err != nil {
		t.Fatal(err)
	}
}

const testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

// serveMetricsServer serves an MCP server with one read-only tool, ping,
// over streamable HTTP, and returns its URL.
func serveMetricsServer(t *testing.T) string {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "metrics", Version: "1.0.0"}, nil)
	srv.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "answer pong",
		InputSchema: map[string]any{"type": "object"},
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "pong"}}}, nil
	})
	ts := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
	t.Cleanup(ts.Close)
	return ts.URL
}

// writeMCPJSON writes the workspace's .mcp.json with one HTTP server at url.
func writeMCPJSON(t *testing.T, workspace, url string) {
	t.Helper()
	data := fmt.Sprintf(`{"mcpServers":{"metrics":{"type":"http","url":%q}}}`, url)
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".mcp.json"), []byte(data), fileMode))
}

func TestDropUnanswered(t *testing.T) {
	t.Parallel()
	call := llm.ToolCall{ID: "c1", Name: "read_file"}
	assistant := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call}}
	result := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "hi"}
	t.Run("keeps a history that ends answered", func(t *testing.T) {
		history := []llm.Message{{Role: llm.RoleUser, Content: "task"}, assistant, result}
		assert.Equal(t, history, dropUnanswered(history))
	})
	t.Run("cuts the turn whose calls were never answered", func(t *testing.T) {
		history := []llm.Message{{Role: llm.RoleUser, Content: "task"}, assistant}
		assert.Equal(t, history[:1], dropUnanswered(history))
	})
	t.Run("keeps a history with no tool calls", func(t *testing.T) {
		history := []llm.Message{{Role: llm.RoleUser, Content: "task"}}
		assert.Equal(t, history, dropUnanswered(history))
	})
}

func TestSession_saveStatus(t *testing.T) {
	t.Parallel()
	t.Run("keeps a verdict the run recorded on disk", func(t *testing.T) {
		t.Parallel()
		files := FilesFor(t.TempDir(), "ws-1")
		verdict := "PASS"
		require.NoError(t, files.WriteStatus(&Status{Verdict: &verdict}))
		s := &session{in: Input{Files: files}}
		require.NoError(t, s.saveStatus("working"))
		got, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, got.Verdict)
		assert.Equal(t, "PASS", *got.Verdict)
		assert.Equal(t, "working", got.State)
	})
}

func TestResumePrior(t *testing.T) {
	t.Parallel()
	t.Run("pads the usage to the history and ends on the current endpoint", func(t *testing.T) {
		run := sampleRun()
		got := resumePrior([]agent.EndpointUsage{{Endpoint: "gpu"}}, &run)
		require.Len(t, got, 2)
		assert.Equal(t, "gpu-b", got[1].Endpoint)
	})
	t.Run("without usage starts from the current endpoint", func(t *testing.T) {
		run := sampleRun()
		run.EndpointHistory = nil
		got := resumePrior(nil, &run)
		require.Len(t, got, 1)
		assert.Equal(t, "gpu", got[0].Endpoint)
	})
	t.Run("keeps usage that already ends on the current endpoint", func(t *testing.T) {
		run := sampleRun()
		prior := []agent.EndpointUsage{{Endpoint: "gpu"}, {Endpoint: "gpu-b"}}
		assert.Equal(t, prior, resumePrior(prior, &run))
	})
}

func TestKeepsGitTokens(t *testing.T) {
	t.Parallel()
	t.Run("defaults to keeping them without a config file", func(t *testing.T) {
		keep, err := keepsGitTokens(t.TempDir())
		require.NoError(t, err)
		assert.True(t, keep)
	})
	t.Run("reads keep_git_tokens from config.toml", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[runner]\nkeep_git_tokens = false\n"), fileMode))
		keep, err := keepsGitTokens(dir)
		require.NoError(t, err)
		assert.False(t, keep)
	})
	t.Run("a broken config.toml is an error", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[runner"), fileMode))
		_, err := keepsGitTokens(dir)
		require.Error(t, err)
	})
}

func TestSecretNames(t *testing.T) {
	t.Parallel()
	t.Run("splits the comma list and skips blanks", func(t *testing.T) {
		assert.Equal(t, []string{"A", "B"}, secretNames([]string{"WS_RUNNER_SECRET_NAMES= A, ,B"}))
	})
	t.Run("without the variable there are none", func(t *testing.T) {
		assert.Empty(t, secretNames([]string{"PATH=/bin"}))
	})
}

func TestEnvValue(t *testing.T) {
	t.Parallel()
	t.Run("finds the value of a name", func(t *testing.T) {
		assert.Equal(t, "b=c", envValue([]string{"A=1", "X=b=c"}, "X"))
	})
	t.Run("a missing name is empty", func(t *testing.T) {
		assert.Empty(t, envValue([]string{"A=1"}, "X"))
	})
}

func TestSessionSecretValues(t *testing.T) {
	t.Parallel()
	environ := []string{"WS_ENDPOINT_TOKEN_GPU=tok-12345", "GH_TOKEN=gh-12345", "GITHUB_TOKEN=ghub-12345", "PATH=/bin"}
	t.Run("keep_git_tokens registers the git tokens with the redactor", func(t *testing.T) {
		s := &session{in: Input{Environ: environ}, keepGit: true}
		values := s.secretValues()
		assert.Contains(t, values, "tok-12345")
		assert.Contains(t, values, "gh-12345")
		assert.Contains(t, values, "ghub-12345")
	})
	t.Run("without keep_git_tokens the git tokens are secrets too", func(t *testing.T) {
		s := &session{in: Input{Environ: environ}, keepGit: false}
		values := s.secretValues()
		assert.Contains(t, values, "gh-12345")
		assert.NotContains(t, values, "")
	})
}

func TestRedactWriter(t *testing.T) {
	t.Parallel()
	t.Run("replaces registered values in what it writes", func(t *testing.T) {
		var b strings.Builder
		w := redactWriter{w: &b, r: tools.NewRedactor("tok-12345")}
		n, err := w.Write([]byte("token tok-12345 used"))
		require.NoError(t, err)
		assert.Equal(t, len("token tok-12345 used"), n)
		assert.Equal(t, "token [redacted] used", b.String())
	})
}

func TestExitOf(t *testing.T) {
	t.Parallel()
	t.Run("an interrupted run without a verdict is stopped", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		exit, message := exitOf(ctx, &agent.Outcome{Exit: agent.ExitEndpointDown, Message: "x"}, "")
		assert.Equal(t, agent.ExitStopped, exit)
		assert.Empty(t, message)
	})
	t.Run("an interrupted run with a verdict keeps its exit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		exit, message := exitOf(ctx, &agent.Outcome{Exit: agent.ExitDone, Message: "m"}, "PASS")
		assert.Equal(t, agent.ExitDone, exit)
		assert.Equal(t, "m", message)
	})
	t.Run("a live run keeps its exit", func(t *testing.T) {
		exit, message := exitOf(t.Context(), &agent.Outcome{Exit: agent.ExitMaxSteps, Message: "m"}, "")
		assert.Equal(t, agent.ExitMaxSteps, exit)
		assert.Equal(t, "m", message)
	})
}

func TestVerdictList(t *testing.T) {
	t.Parallel()
	run := sampleRun()
	run.Pass = []string{"PASS", "OK"}
	run.Fail = []string{"FAIL"}
	assert.Equal(t, "PASS or OK or FAIL", verdictList(&run))
}

func TestLastToolCallTurn(t *testing.T) {
	t.Parallel()
	t.Run("the index of the last turn that called tools", func(t *testing.T) {
		history := []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "a"}}},
			{Role: llm.RoleTool, ToolCallID: "a"},
			{Role: llm.RoleAssistant, Content: "text only"},
		}
		assert.Equal(t, 0, lastToolCallTurn(history))
	})
	t.Run("minus one without any", func(t *testing.T) {
		assert.Equal(t, -1, lastToolCallTurn([]llm.Message{{Role: llm.RoleUser}}))
	})
}
