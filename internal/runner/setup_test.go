package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
	mcpclient "github.com/bobcob7/workspaces/internal/mcp"
)

// noCaller is an MCP caller that is never reached: the tests only load tools.
type noCaller struct{}

func (noCaller) Call(context.Context, string, string) (mcpclient.CallResult, error) {
	return mcpclient.CallResult{}, nil
}

// fakeToolset is an agent.Toolset that answers every call with err.
type fakeToolset struct {
	err error
}

func (fakeToolset) Definitions() []llm.Tool { return nil }

func (f fakeToolset) Call(context.Context, string, string) (tools.Result, error) {
	return tools.Result{}, f.err
}

func (fakeToolset) Known(string) bool { return true }

// openTestTranscript opens files' transcript, creating its directory, and
// closes it when the test ends.
func openTestTranscript(t *testing.T, files Files) *agent.Transcript {
	t.Helper()
	require.NoError(t, os.MkdirAll(files.Dir, dirMode))
	tr, err := agent.OpenTranscript(files.Transcript(), tools.NewRedactor())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tr.Close() })
	return tr
}

func TestNewProvider(t *testing.T) {
	t.Parallel()
	t.Run("the openai api is the openai provider", func(t *testing.T) {
		t.Parallel()
		p, err := newProvider(&llm.Endpoint{Name: "gpu", URL: "http://gpu", API: config.APIOpenAI}, slog.New(slog.DiscardHandler))
		require.NoError(t, err)
		assert.NotNil(t, p)
	})
	t.Run("a reserved api is not implemented yet", func(t *testing.T) {
		t.Parallel()
		_, err := newProvider(&llm.Endpoint{Name: "gpu", API: "anthropic"}, slog.New(slog.DiscardHandler))
		assert.ErrorIs(t, err, config.ErrAPINotImplemented)
	})
	t.Run("an unknown api is an error", func(t *testing.T) {
		t.Parallel()
		_, err := newProvider(&llm.Endpoint{Name: "gpu", API: "carrier-pigeon"}, slog.New(slog.DiscardHandler))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown api")
	})
}

func TestServerOfTools(t *testing.T) {
	t.Parallel()
	got := serverOfTools([]mcpclient.ToolInfo{{Name: "mcp__a__x", Server: "a"}, {Name: "mcp__b__y", Server: "b"}})
	assert.Equal(t, map[string]string{"mcp__a__x": "a", "mcp__b__y": "b"}, got)
}

func TestTransportsOf(t *testing.T) {
	t.Parallel()
	got := transportsOf([]mcpclient.ServerDef{{Name: "a", Type: "stdio"}, {Name: "b", Type: "http"}})
	assert.Equal(t, map[string]string{"a": "stdio", "b": "http"}, got)
}

func TestErrorText(t *testing.T) {
	t.Parallel()
	t.Run("nil is empty", func(t *testing.T) {
		assert.Empty(t, errorText(nil))
	})
	t.Run("a message is itself", func(t *testing.T) {
		msg := "boom"
		assert.Equal(t, "boom", errorText(&msg))
	})
}

func TestMCPSet_statuses(t *testing.T) {
	t.Parallel()
	t.Run("copies each server's status with the tools loaded", func(t *testing.T) {
		allowed := []mcpclient.ToolInfo{{Name: "mcp__metrics__ping", Server: "metrics", Tool: "ping"}}
		toolset := agent.NewToolset(nil, allowed, 8, noCaller{}, nil)
		set := &mcpSet{toolset: toolset, status: []mcpclient.Status{{Server: "metrics", State: "connected", Tools: 1}}}
		got := set.statuses()
		require.Len(t, got, 1)
		assert.Equal(t, 1, got[0].Loaded)
		assert.Zero(t, set.status[0].Loaded, "the stored status is not changed")
	})
}

func TestSession_mcpDefs(t *testing.T) {
	t.Parallel()
	t.Run("a run without servers reads no definitions", func(t *testing.T) {
		t.Parallel()
		run := sampleRun()
		run.MCPServers = nil
		s := &session{run: run, in: Input{Workspace: t.TempDir()}}
		defs, err := s.mcpDefs()
		require.NoError(t, err)
		assert.Nil(t, defs)
	})
	t.Run("an unknown server is left out, not an error", func(t *testing.T) {
		t.Parallel()
		run := sampleRun()
		run.MCPServers = []string{"nowhere"}
		s := &session{run: run, in: Input{Workspace: t.TempDir(), Environ: []string{"HOME=" + t.TempDir()}}, log: slog.New(slog.DiscardHandler)}
		defs, err := s.mcpDefs()
		require.NoError(t, err)
		assert.Empty(t, defs)
	})
}

func TestLoggedToolset_Call(t *testing.T) {
	t.Parallel()
	t.Run("counts an MCP call and logs its Go error", func(t *testing.T) {
		t.Parallel()
		files := FilesFor(t.TempDir(), "ws-1")
		tr := openTestTranscript(t, files)
		s := &session{
			in:  Input{Files: files},
			tr:  tr,
			mcp: &mcpSet{serverOf: map[string]string{"mcp__metrics__ping": "metrics"}, calls: map[string]int{}},
		}
		ts := &loggedToolset{Toolset: fakeToolset{err: errors.New("connection reset")}, s: s}
		_, err := ts.Call(t.Context(), "mcp__metrics__ping", "{}")
		require.Error(t, err)
		assert.Equal(t, 1, s.mcp.calls["metrics"])
		data, err := os.ReadFile(files.Transcript())
		require.NoError(t, err)
		assert.Contains(t, string(data), `"kind":"mcp_error"`)
		assert.Contains(t, string(data), `"tool":"mcp__metrics__ping"`)
	})
	t.Run("a built-in call is not counted or logged", func(t *testing.T) {
		t.Parallel()
		files := FilesFor(t.TempDir(), "ws-1")
		tr := openTestTranscript(t, files)
		s := &session{in: Input{Files: files}, tr: tr, mcp: &mcpSet{serverOf: map[string]string{}, calls: map[string]int{}}}
		ts := &loggedToolset{Toolset: fakeToolset{err: errors.New("bad args")}, s: s}
		_, err := ts.Call(t.Context(), "read_file", "{}")
		require.Error(t, err)
		assert.Empty(t, s.mcp.calls)
		data, err := os.ReadFile(files.Transcript())
		require.NoError(t, err)
		assert.Empty(t, string(data))
	})
}

func TestSession_startMCP(t *testing.T) {
	t.Parallel()
	t.Run("a server that fails to start is traced and left out", func(t *testing.T) {
		t.Parallel()
		files := FilesFor(t.TempDir(), "ws-1")
		workspace := t.TempDir()
		data := `{"mcpServers":{"broken":{"type":"stdio","command":"/nonexistent/ws-test-binary"}}}`
		require.NoError(t, os.WriteFile(filepath.Join(workspace, ".mcp.json"), []byte(data), fileMode))
		tr := openTestTranscript(t, files)
		run := sampleRun()
		run.MCPServers = []string{"broken"}
		s := &session{
			in:       Input{Files: files, Workspace: workspace, Environ: []string{"HOME=" + t.TempDir()}},
			run:      run,
			tr:       tr,
			redactor: tools.NewRedactor(),
			logOut:   io.Discard,
			log:      slog.New(slog.DiscardHandler),
		}
		mc, err := s.startMCP(t.Context(), nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = mc.manager.Close() })
		assert.Equal(t, []string{"broken"}, mc.failed)
		transcript, err := os.ReadFile(files.Transcript())
		require.NoError(t, err)
		assert.Contains(t, string(transcript), `"kind":"mcp_start"`)
		assert.Contains(t, string(transcript), `"tool":null`)
	})
}
