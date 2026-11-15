package fakemcp

import (
	"bytes"
	"encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testHeaderName  = "Authorization"
	testHeaderValue = "Bearer e2e-secret-4c1d"
)

func TestNewServer(t *testing.T) {
	t.Run("lists the generated and scripted tools with their hints", func(t *testing.T) {
		cfg := loadFixture(t, "many-tools.yaml")
		cs := connect(t, newTestServer(t, cfg))
		res, err := cs.ListTools(t.Context(), nil)
		require.NoError(t, err)
		tools := make(map[string]*mcp.Tool, len(res.Tools))
		for _, tool := range res.Tools {
			tools[tool.Name] = tool
		}
		require.Len(t, res.Tools, 82)
		assert.True(t, tools["list_thing_001"].Annotations.ReadOnlyHint)
		assert.True(t, tools["list_thing_080"].Annotations.ReadOnlyHint)
		assert.True(t, derefBool(tools["delete_dashboard"].Annotations.DestructiveHint))
	})
	t.Run("sends server instructions at initialize", func(t *testing.T) {
		cs := connect(t, newTestServer(t, loadFixture(t, "stdio.yaml")))
		assert.Equal(t, "Look up services by name.", cs.InitializeResult().Instructions)
	})
	t.Run("a matching reply gets its text", func(t *testing.T) {
		cs := connect(t, newTestServer(t, loadFixture(t, "stdio.yaml")))
		res, err := call(t, cs, "lookup_service", map[string]any{"name": "api"})
		require.NoError(t, err)
		assert.False(t, res.IsError)
		assert.Equal(t, "api: owner team-a, port 8080", resultText(res))
	})
	t.Run("a text template reads the arguments", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "echo", Replies: []Reply{{Text: "hi {{.Args.who}}"}}})
		cs := connect(t, newTestServer(t, cfg))
		res, err := call(t, cs, "echo", map[string]any{"who": "ada"})
		require.NoError(t, err)
		assert.Equal(t, "hi ada", resultText(res))
	})
	t.Run("an error reply is an isError result", func(t *testing.T) {
		cs := connect(t, newTestServer(t, loadFixture(t, "stdio.yaml")))
		res, err := call(t, cs, "lookup_service", map[string]any{"name": "missing"})
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Equal(t, "no such service", resultText(res))
	})
	t.Run("a protocol_error reply is a JSON-RPC error", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "broken", Replies: []Reply{{ProtocolError: "backend exploded"}}})
		cs := connect(t, newTestServer(t, cfg))
		_, err := call(t, cs, "broken", nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "backend exploded")
	})
	t.Run("a call with no matching reply is an isError result", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "picky", Replies: []Reply{{Match: `"x":1`, Text: "one"}}})
		cs := connect(t, newTestServer(t, cfg))
		res, err := call(t, cs, "picky", map[string]any{"x": 2})
		require.NoError(t, err)
		assert.True(t, res.IsError)
	})
	t.Run("records one JSON line per call", func(t *testing.T) {
		var record bytes.Buffer
		cs := connect(t, newTestServerWith(t, loadFixture(t, "stdio.yaml"), &record, nil))
		_, err := call(t, cs, "lookup_service", map[string]any{"name": "api"})
		require.NoError(t, err)
		_, err = call(t, cs, "lookup_service", map[string]any{"name": "worker"})
		require.NoError(t, err)
		lines := bytes.Split(bytes.TrimSpace(record.Bytes()), []byte("\n"))
		require.Len(t, lines, 2)
		var line struct {
			Tool      string         `json:"tool"`
			Arguments map[string]any `json:"arguments"`
		}
		require.NoError(t, json.Unmarshal(lines[1], &line))
		assert.Equal(t, "lookup_service", line.Tool)
		assert.Equal(t, "worker", line.Arguments["name"])
	})
	t.Run("the call after crash_after_calls exits with 3 before replying", func(t *testing.T) {
		cfg := loadFixture(t, "stdio.yaml")
		cfg.Faults.CrashAfterCalls = 1
		var exitCode atomic.Int64
		cs := connect(t, newTestServerWith(t, cfg, nil, &exitCode))
		_, err := call(t, cs, "lookup_service", map[string]any{"name": "api"})
		require.NoError(t, err)
		_, err = call(t, cs, "lookup_service", map[string]any{"name": "api"})
		require.Error(t, err)
		assert.Equal(t, int64(3), exitCode.Load())
	})
	t.Run("slow_call delays the reply", func(t *testing.T) {
		cfg := loadFixture(t, "stdio.yaml")
		cfg.Faults.SlowCall = 50 * time.Millisecond
		cs := connect(t, newTestServer(t, cfg))
		start := time.Now()
		_, err := call(t, cs, "lookup_service", map[string]any{"name": "api"})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	})
	t.Run("slow_initialize delays initialize", func(t *testing.T) {
		cfg := loadFixture(t, "stdio.yaml")
		cfg.Faults.SlowInitialize = 50 * time.Millisecond
		start := time.Now()
		connect(t, newTestServer(t, cfg))
		assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	})
	t.Run("rejects a config that the SDK would panic on", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "bad", InputSchema: map[string]any{"type": "array"}})
		_, err := NewServer(cfg, nil, testLogger())
		require.Error(t, err)
	})
	t.Run("rejects an invalid match regexp", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "bad", Replies: []Reply{{Match: "(", Text: "x"}}})
		_, err := NewServer(cfg, nil, testLogger())
		require.Error(t, err)
	})
	t.Run("rejects a reply that sets two results", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "bad", Replies: []Reply{{Text: "x", Error: "y"}}})
		_, err := NewServer(cfg, nil, testLogger())
		require.Error(t, err)
	})
	t.Run("rejects duplicate tool names", func(t *testing.T) {
		cfg := inlineConfig(Tool{Name: "same"}, Tool{Name: "same"})
		_, err := NewServer(cfg, nil, testLogger())
		require.Error(t, err)
	})
}

func TestRequireHeaders(t *testing.T) {
	cfg := Config{RequireHeader: map[string]string{testHeaderName: testHeaderValue}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	t.Run("answers 401 without the header", func(t *testing.T) {
		rec := serveWithHeader(t, RequireHeaders(cfg, next), "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.NotContains(t, rec.Body.String(), testHeaderValue)
	})
	t.Run("answers 401 with a wrong value", func(t *testing.T) {
		rec := serveWithHeader(t, RequireHeaders(cfg, next), "Bearer wrong")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.NotContains(t, rec.Body.String(), testHeaderValue)
	})
	t.Run("passes with the header", func(t *testing.T) {
		rec := serveWithHeader(t, RequireHeaders(cfg, next), testHeaderValue)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func newTestServer(t *testing.T, cfg Config) *mcp.Server {
	t.Helper()
	return newTestServerWith(t, cfg, nil, nil)
}

func newTestServerWith(t *testing.T, cfg Config, record io.Writer, exitCode *atomic.Int64) *mcp.Server {
	t.Helper()
	exit := func(int) {}
	if exitCode != nil {
		exit = func(code int) { exitCode.Store(int64(code)) }
	}
	srv, err := newServer(cfg, record, testLogger(), exit)
	require.NoError(t, err)
	return srv
}

// connect runs srv in memory and returns an SDK client connected to it.
func connect(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	cs, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	return cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
}

func resultText(res *mcp.CallToolResult) string {
	text, _ := res.Content[0].(*mcp.TextContent)
	return text.Text
}

func loadFixture(t *testing.T, name string) Config {
	t.Helper()
	cfg, err := LoadConfig(filepath.Join("..", "..", "docs", "design", "ollama-agent", "fake-mcp", name))
	require.NoError(t, err)
	return cfg
}

func inlineConfig(tools ...Tool) Config {
	cfg := Config{Server: ServerInfo{Name: "fake", Version: "0.0.0-fake"}, Tools: tools}
	applyDefaults(&cfg)
	return cfg
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func serveWithHeader(t *testing.T, h http.Handler, value string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", http.NoBody)
	if value != "" {
		req.Header.Set(testHeaderName, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}
