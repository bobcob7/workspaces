package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakemcp"
)

const (
	testToken      = "e2e-secret-4c1d"
	testFixtures   = "../../docs/design/ollama-agent/fake-mcp/"
	testLookupTool = "mcp__fake__lookup_service"
)

func TestStart(t *testing.T) {
	t.Parallel()
	bin := buildFakeMCP(t)
	t.Run("stdio with its require_env connects and lists the tools", func(t *testing.T) {
		t.Parallel()
		srv, _, err := Start(t.Context(), stdioDef(bin, "stdio.yaml"), StartOptions{Environ: []string{"FAKE_MCP_TOKEN=" + testToken}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = srv.Close() })
		require.Len(t, srv.Tools(), 1)
		tool := srv.Tools()[0]
		assert.Equal(t, testLookupTool, tool.Name)
		assert.True(t, tool.ReadOnly)
		assert.Equal(t, stateConnected, srv.status().State)
	})
	t.Run("stdio without the required variable fails and names it, never its value", func(t *testing.T) {
		t.Parallel()
		_, _, err := Start(t.Context(), stdioDef(bin, "stdio.yaml"), StartOptions{Environ: []string{"PATH=/usr/bin"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FAKE_MCP_TOKEN")
		assert.NotContains(t, err.Error(), testToken)
	})
	t.Run("http with a bearer header from the environment connects, and its value is a secret", func(t *testing.T) {
		t.Parallel()
		def := ServerDef{
			Name: "fake", Source: sourceUser, Type: typeHTTP, URL: serveFake(t, fixtureConfig(t, "http.yaml")),
			Headers: map[string]string{"Authorization": "Bearer ${FAKE_MCP_TOKEN}"},
		}
		srv, secrets, err := Start(t.Context(), def, StartOptions{Environ: []string{"FAKE_MCP_TOKEN=" + testToken}})
		require.NoError(t, err)
		t.Cleanup(func() { _ = srv.Close() })
		assert.Equal(t, stateConnected, srv.status().State)
		assert.Contains(t, secrets, testToken)
	})
	t.Run("an unset variable in a header fails before any connection, naming it", func(t *testing.T) {
		t.Parallel()
		def := ServerDef{
			Name: "fake", Source: sourceUser, Type: typeHTTP, URL: "http://127.0.0.1:1/mcp",
			Headers: map[string]string{"Authorization": "Bearer ${FAKE_MCP_TOKEN}"},
		}
		_, _, err := Start(t.Context(), def, StartOptions{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "FAKE_MCP_TOKEN")
	})
}

func TestServer_Call(t *testing.T) {
	t.Run("returns the reply's text", func(t *testing.T) {
		srv := newInMemoryServer(t, fixtureConfig(t, "stdio.yaml"))
		res, err := srv.Call(t.Context(), "lookup_service", `{"name":"api"}`)
		require.NoError(t, err)
		assert.Equal(t, "api: owner team-a, port 8080", res.Text)
		assert.False(t, res.IsError)
	})
	t.Run("an error reply is an isError result", func(t *testing.T) {
		srv := newInMemoryServer(t, fixtureConfig(t, "stdio.yaml"))
		res, err := srv.Call(t.Context(), "lookup_service", `{"name":"missing"}`)
		require.NoError(t, err)
		assert.True(t, res.IsError)
		assert.Equal(t, "no such service", res.Text)
	})
	t.Run("arguments that are not an object are an error and no call", func(t *testing.T) {
		srv := newInMemoryServer(t, fixtureConfig(t, "stdio.yaml"))
		_, err := srv.Call(t.Context(), "lookup_service", `[1]`)
		require.Error(t, err)
		assert.Equal(t, stateConnected, srv.status().State)
	})
	t.Run("a JSON-RPC error fails that call only", func(t *testing.T) {
		srv := newInMemoryServer(t, inlineConfig(fakemcp.Tool{
			Name:        "broken",
			InputSchema: map[string]any{"type": "object"},
			Replies:     []fakemcp.Reply{{ProtocolError: "boom"}},
		}))
		_, err := srv.Call(t.Context(), "broken", "")
		require.Error(t, err)
		assert.Equal(t, stateConnected, srv.status().State)
		_, err = srv.Call(t.Context(), "broken", "")
		assert.NotContains(t, errorText(err), "is not running")
	})
	t.Run("a call that times out fails that call only", func(t *testing.T) {
		sess := &sessionMock{
			CallToolFunc: func(ctx context.Context, _ *mcp.CallToolParams) (*mcp.CallToolResult, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		srv := &Server{name: "fake", sess: sess, callTimeout: time.Millisecond, state: stateConnected}
		_, err := srv.Call(t.Context(), "slow", "")
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, stateConnected, srv.status().State)
	})
	t.Run("a closed connection marks the server failed, and it is not restarted", func(t *testing.T) {
		sess := &sessionMock{
			CallToolFunc: func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
				return nil, fmt.Errorf("%w: calling %q", mcp.ErrConnectionClosed, "tools/call")
			},
		}
		srv := &Server{name: "fake", sess: sess, callTimeout: time.Second, state: stateConnected}
		_, err := srv.Call(t.Context(), "any", "")
		require.ErrorIs(t, err, mcp.ErrConnectionClosed)
		status := srv.status()
		assert.Equal(t, stateFailed, status.State)
		require.NotNil(t, status.Error)
		_, err = srv.Call(t.Context(), "any", "")
		require.ErrorContains(t, err, "is not running")
	})
	t.Run("a server that dies is failed after its next call", func(t *testing.T) {
		t.Parallel()
		bin := buildFakeMCP(t)
		srv, _, err := Start(t.Context(), stdioDef(bin, writeCrashConfig(t)), StartOptions{})
		require.NoError(t, err)
		t.Cleanup(func() { _ = srv.Close() })
		_, err = srv.Call(t.Context(), "ping", "{}")
		require.NoError(t, err)
		_, err = srv.Call(t.Context(), "ping", "{}")
		require.Error(t, err)
		assert.Equal(t, stateFailed, srv.status().State)
	})
}

func TestNewServer(t *testing.T) {
	t.Run("a failed tool listing is an error", func(t *testing.T) {
		sess := &sessionMock{
			ListToolsFunc: func(context.Context, *mcp.ListToolsParams) (*mcp.ListToolsResult, error) {
				return nil, errors.New("boom")
			},
		}
		_, err := newServer(t.Context(), "fake", sess, sess.Close)
		require.ErrorContains(t, err, "listing tools")
	})
}

func TestToToolInfo(t *testing.T) {
	t.Run("read-only annotations and the schema map over", func(t *testing.T) {
		tool := &mcp.Tool{
			Name:        "query",
			Description: "reads",
			InputSchema: map[string]any{"type": "object"},
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}
		info := toToolInfo("metrics", tool)
		assert.Equal(t, ToolInfo{
			Server:      "metrics",
			Tool:        "query",
			Name:        "mcp__metrics__query",
			Description: "reads",
			Schema:      map[string]any{"type": "object"},
			ReadOnly:    true,
		}, info)
	})
	t.Run("an explicit destructiveHint is destructive", func(t *testing.T) {
		destructive := true
		info := toToolInfo("metrics", &mcp.Tool{Name: "drop", Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive}})
		assert.True(t, info.Destructive)
		assert.False(t, info.ReadOnly)
	})
	t.Run("missing annotations are neither read-only nor destructive", func(t *testing.T) {
		info := toToolInfo("metrics", &mcp.Tool{Name: "any"})
		assert.False(t, info.ReadOnly)
		assert.False(t, info.Destructive)
	})
}

func TestDecodeArguments(t *testing.T) {
	t.Run("empty arguments are an empty object", func(t *testing.T) {
		values, err := decodeArguments("  ")
		require.NoError(t, err)
		assert.Empty(t, values)
	})
	t.Run("a JSON object decodes", func(t *testing.T) {
		values, err := decodeArguments(`{"name":"api"}`)
		require.NoError(t, err)
		assert.Equal(t, map[string]any{"name": "api"}, values)
	})
	t.Run("invalid JSON is an error", func(t *testing.T) {
		_, err := decodeArguments(`{"name":`)
		require.ErrorContains(t, err, "not a JSON object")
	})
}

func TestStdioEnv(t *testing.T) {
	t.Run("the server's env block is appended in key order, after the runner's environment", func(t *testing.T) {
		env := stdioEnv([]string{"PATH=/bin", "HOME=/h"}, map[string]string{"Z": "1", "A": "2"})
		assert.Equal(t, []string{"PATH=/bin", "HOME=/h", "A=2", "Z=1"}, env)
	})
}

func TestLookupEnv(t *testing.T) {
	environ := []string{"K=first", "K=last", "V=a=b"}
	t.Run("the last entry for a name wins", func(t *testing.T) {
		value, found := lookupEnv(environ)("K")
		assert.True(t, found)
		assert.Equal(t, "last", value)
	})
	t.Run("the value keeps its own equals signs", func(t *testing.T) {
		value, found := lookupEnv(environ)("V")
		assert.True(t, found)
		assert.Equal(t, "a=b", value)
	})
	t.Run("a missing name is not found", func(t *testing.T) {
		_, found := lookupEnv(environ)("MISSING")
		assert.False(t, found)
	})
}

func TestRedactError(t *testing.T) {
	t.Run("a nil error stays nil", func(t *testing.T) {
		assert.NoError(t, redactError(nil, []string{testToken}))
	})
	t.Run("every secret is replaced in the message", func(t *testing.T) {
		err := redactError(errors.New("bearer "+testToken+" rejected"), []string{testToken})
		assert.Equal(t, "bearer [redacted] rejected", err.Error())
	})
	t.Run("a secret that prefixes a longer one leaves no tail, whatever the order", func(t *testing.T) {
		for _, secrets := range [][]string{{"tok", "tok-long-9f", "tok"}, {"tok-long-9f", "tok"}} {
			err := redactError(errors.New("a tok-long-9f b tok c"), secrets)
			assert.Equal(t, "a [redacted] b [redacted] c", err.Error())
		}
	})
	t.Run("the error chain survives redaction", func(t *testing.T) {
		wrapped := fmt.Errorf("calling tool with "+testToken+": %w", context.DeadlineExceeded)
		err := redactError(wrapped, []string{testToken})
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotContains(t, err.Error(), testToken)
		var rpcErr *jsonrpc.Error
		require.ErrorAs(t, redactError(fmt.Errorf("x: %w", &jsonrpc.Error{Code: 1, Message: "m"}), nil), &rpcErr)
		assert.Equal(t, int64(1), rpcErr.Code)
	})
}

func TestHeaderTransport_RoundTrip(t *testing.T) {
	t.Parallel()
	t.Run("a redirect to another host does not carry the headers", func(t *testing.T) {
		t.Parallel()
		var seen atomic.Value
		other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen.Store(r.Header.Get("Authorization"))
		}))
		t.Cleanup(other.Close)
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		}))
		t.Cleanup(origin.Close)
		client := httpClient(nil, origin.URL, map[string]string{"Authorization": "Bearer " + testToken})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Empty(t, seen.Load())
	})
	t.Run("the configured host gets the headers", func(t *testing.T) {
		t.Parallel()
		var seen atomic.Value
		origin := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			seen.Store(r.Header.Get("Authorization"))
		}))
		t.Cleanup(origin.Close)
		client := httpClient(nil, origin.URL, map[string]string{"Authorization": "Bearer " + testToken})
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, "Bearer "+testToken, seen.Load())
	})
}

func TestTailWriter_Write(t *testing.T) {
	t.Run("keeps only the last bytes written", func(t *testing.T) {
		tail := &tailWriter{limit: 5}
		_, err := tail.Write([]byte("abc"))
		require.NoError(t, err)
		_, err = tail.Write([]byte("defgh"))
		require.NoError(t, err)
		assert.Equal(t, "defgh", tail.String())
	})
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// stdioDef is a stdio definition that runs the built fake-mcp with a fixture.
func stdioDef(bin, fixture string) ServerDef {
	return ServerDef{
		Name: "fake", Source: sourceUser, Type: typeStdio, Command: bin,
		Args: []string{"--config", fixtureName(fixture)},
	}
}

func fixtureName(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return testFixtures + name
}

func fixtureConfig(t *testing.T, name string) *fakemcp.Config {
	t.Helper()
	cfg, err := fakemcp.LoadConfig(fixtureName(name))
	require.NoError(t, err)
	return &cfg
}

// inlineConfig is a fake server config made of the given tools.
func inlineConfig(tools ...fakemcp.Tool) *fakemcp.Config {
	return &fakemcp.Config{Server: fakemcp.ServerInfo{Name: "fake", Version: "0.0.0-fake"}, Tools: tools}
}

// newInMemoryServer is a Server over an in-memory connection to a fake server.
func newInMemoryServer(t *testing.T, cfg *fakemcp.Config) *Server {
	t.Helper()
	cs := inMemorySession(t, cfg)
	srv, err := newServer(t.Context(), "fake", cs, cs.Close)
	require.NoError(t, err)
	return srv
}

// inMemorySession connects a client to a fake server over in-memory transports.
func inMemorySession(t *testing.T, cfg *fakemcp.Config) *mcp.ClientSession {
	t.Helper()
	fake, err := fakemcp.NewServer(*cfg, nil, testLogger())
	require.NoError(t, err)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := fake.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil).Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	return cs
}

// serveFake serves cfg over streamable HTTP, requiring its headers, and returns the MCP URL.
func serveFake(t *testing.T, cfg *fakemcp.Config) string {
	t.Helper()
	fake, err := fakemcp.NewServer(*cfg, nil, testLogger())
	require.NoError(t, err)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return fake }, nil)
	ts := httptest.NewServer(fakemcp.RequireHeaders(*cfg, handler))
	t.Cleanup(ts.Close)
	return ts.URL + "/mcp"
}

// buildFakeMCP builds cmd/fake-mcp into a temporary directory.
func buildFakeMCP(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-mcp")
	out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "../../cmd/fake-mcp").CombinedOutput()
	require.NoError(t, err, string(out))
	return bin
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

// writeCrashConfig writes a fake server that exits after its first call and
// returns its path.
func writeCrashConfig(t *testing.T) string {
	t.Helper()
	const config = `server: {name: fake, version: 0.0.0-fake}
faults: {crash_after_calls: 1}
tools:
  - name: ping
    input_schema: {type: object}
    annotations: {readOnlyHint: true}
    replies:
      - text: "pong"
`
	path := filepath.Join(t.TempDir(), "crash.yaml")
	require.NoError(t, os.WriteFile(path, []byte(config), 0o600))
	return path
}
