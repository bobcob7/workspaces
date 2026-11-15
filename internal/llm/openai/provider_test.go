package openai

import (
	"bytes"
	"context"
	"encoding/json/v2"
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

	"github.com/bobcob7/workspaces/internal/llm"
)

// capture keeps the last request a test server received.
type capture struct {
	mu     sync.Mutex
	body   []byte
	header http.Header
}

func (c *capture) record(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.body = data
		c.header = r.Header.Clone()
		c.mu.Unlock()
		next(w, r)
	}
}

func (c *capture) json(t *testing.T) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var body map[string]any
	require.NoError(t, json.Unmarshal(c.body, &body))
	return body
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fakeollama", "testdata", name))
	require.NoError(t, err)
	return data
}

// replay answers every request with a testdata fixture and the status.
func replay(t *testing.T, name string) http.HandlerFunc {
	t.Helper()
	data := fixture(t, name)
	return func(w http.ResponseWriter, _ *http.Request) {
		if strings.HasSuffix(name, ".txt") {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		_, _ = w.Write(data)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func newTestProvider(t *testing.T, handler http.HandlerFunc, ep llm.Endpoint) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	ep.URL = srv.URL
	return New(ep, srv.Client(), discardLogger())
}

func userRequest(content string) llm.Request {
	return llm.Request{
		Model:    "qwen/qwen3.5-9b",
		Messages: []llm.Message{{Role: llm.RoleUser, Content: content}},
	}
}

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("warns once for a token over plain http to another host", func(t *testing.T) {
		t.Parallel()
		var logs bytes.Buffer
		log := slog.New(slog.NewTextHandler(&logs, nil))
		New(llm.Endpoint{Name: "gpu", URL: "http://gpu-a.inference.svc:1234", Token: "t"}, http.DefaultClient, log)
		assert.Contains(t, logs.String(), "sending a token over plain http")
		assert.Contains(t, logs.String(), "gpu-a.inference.svc")
	})
	t.Run("stays quiet for loopback, https, or no token", func(t *testing.T) {
		t.Parallel()
		endpoints := []llm.Endpoint{
			{URL: "http://127.0.0.1:11434", Token: "t"},
			{URL: "http://localhost:11434", Token: "t"},
			{URL: "https://gpu.example", Token: "t"},
			{URL: "http://gpu.example", Token: ""},
		}
		for _, ep := range endpoints {
			var logs bytes.Buffer
			New(ep, http.DefaultClient, slog.New(slog.NewTextHandler(&logs, nil)))
			assert.Empty(t, logs.String(), ep.URL)
		}
	})
}

func TestProvider_Chat(t *testing.T) {
	t.Parallel()
	t.Run("accumulates streamed tool calls and usage", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, replay(t, "stream-tools.response.txt"), llm.Endpoint{})
		res, err := p.Chat(t.Context(), userRequest("What is the status of order A-1042?"))
		require.NoError(t, err)
		assert.Equal(t, llm.StopToolCalls, res.StopReason)
		assert.Equal(t, []llm.ToolCall{{ID: "call_7fq2m0zd", Name: "lookup_order_status", Arguments: `{"order_id":"A-1042"}`}}, res.Message.ToolCalls)
		assert.Equal(t, llm.Usage{InputTokens: 182, OutputTokens: 22, Reported: true}, res.Usage)
		assert.Equal(t, llm.RoleAssistant, res.Message.Role)
	})
	t.Run("reports no usage when the server sent none", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, replay(t, "stream-no-usage.response.txt"), llm.Endpoint{})
		res, err := p.Chat(t.Context(), userRequest("count"))
		require.NoError(t, err)
		assert.False(t, res.Usage.Reported)
		assert.Equal(t, llm.StopEnd, res.StopReason)
		assert.NotEmpty(t, res.Message.Content)
	})
	t.Run("fills reasoning from reasoning and reasoning_content deltas", func(t *testing.T) {
		t.Parallel()
		const reasoningField = `data: {"choices":[{"index":0,"delta":{"reasoning":"think "},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"reasoning":"more"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: [DONE]

`
		const reasoningContentField = `data: {"choices":[{"index":0,"delta":{"reasoning_content":"deep "},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"reasoning_content":"thought"},"finish_reason":null}]}

data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}

data: [DONE]

`
		cases := map[string]struct{ body, want string }{
			"reasoning":         {body: reasoningField, want: "think more"},
			"reasoning_content": {body: reasoningContentField, want: "deep thought"},
		}
		for field, tc := range cases {
			p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, tc.body)
			}, llm.Endpoint{})
			res, err := p.Chat(t.Context(), userRequest("hi"))
			require.NoError(t, err, field)
			assert.Equal(t, tc.want, res.Message.Reasoning, field)
		}
	})
	t.Run("maps finish reasons", func(t *testing.T) {
		t.Parallel()
		for finish, want := range map[string]llm.StopReason{"length": llm.StopMaxTokens, "tool_calls": llm.StopToolCalls, "stop": llm.StopEnd} {
			p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"x"},"finish_reason":"`+finish+`"}]}`+"\n\ndata: [DONE]\n\n")
			}, llm.Endpoint{})
			res, err := p.Chat(t.Context(), userRequest("hi"))
			require.NoError(t, err, finish)
			assert.Equal(t, want, res.StopReason, finish)
		}
	})
	t.Run("sends the request in the wire format", func(t *testing.T) {
		t.Parallel()
		var c capture
		p := newTestProvider(t, c.record(replay(t, "stream.response.txt")), llm.Endpoint{Token: "t"})
		req := llm.Request{
			Model:       "qwen/qwen3.5-9b",
			System:      "be brief",
			Messages:    []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
			Tools:       []llm.Tool{{Name: "lookup", Description: "find it", Parameters: map[string]any{"type": "object"}}},
			MaxOutput:   512,
			Temperature: 0.2,
			Reasoning:   "none",
			TraceParent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		}
		_, err := p.Chat(t.Context(), req)
		require.NoError(t, err)
		body := c.json(t)
		assert.Equal(t, "qwen/qwen3.5-9b", body["model"])
		assert.Equal(t, true, body["stream"])
		assert.Equal(t, map[string]any{"include_usage": true}, body["stream_options"])
		assert.NotContains(t, body, "tool_choice")
		assert.Equal(t, "none", body["reasoning_effort"])
		assert.EqualValues(t, 512, body["max_tokens"])
		assert.Len(t, body["tools"], 1)
		msgs, ok := body["messages"].([]any)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"role": "system", "content": "be brief"}, msgs[0])
		c.mu.Lock()
		defer c.mu.Unlock()
		assert.Equal(t, "Bearer t", c.header.Get("Authorization"))
		assert.Equal(t, req.TraceParent, c.header.Get("traceparent"))
	})
	t.Run("omits reasoning_effort when unset and sends the placeholder key", func(t *testing.T) {
		t.Parallel()
		var c capture
		p := newTestProvider(t, c.record(replay(t, "stream.response.txt")), llm.Endpoint{})
		_, err := p.Chat(t.Context(), userRequest("hi"))
		require.NoError(t, err)
		assert.NotContains(t, c.json(t), "reasoning_effort")
		c.mu.Lock()
		defer c.mu.Unlock()
		assert.Equal(t, "Bearer ws", c.header.Get("Authorization"))
	})
	t.Run("replays assistant tool calls and tool results without reasoning", func(t *testing.T) {
		t.Parallel()
		var c capture
		p := newTestProvider(t, c.record(replay(t, "stream.response.txt")), llm.Endpoint{})
		req := llm.Request{Model: "qwen3:8b", Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "status?"},
			{Role: llm.RoleAssistant, Reasoning: "secret", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "lookup", Arguments: `{"id":"A"}`}}},
			{Role: llm.RoleTool, ToolCallID: "call_1", Content: "shipped"},
		}}
		_, err := p.Chat(t.Context(), req)
		require.NoError(t, err)
		msgs, ok := c.json(t)["messages"].([]any)
		require.True(t, ok)
		require.Len(t, msgs, 3)
		assistant, ok := msgs[1].(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, assistant, "reasoning")
		assert.Len(t, assistant["tool_calls"], 1)
		assert.Equal(t, map[string]any{"role": "tool", "content": "shipped", "tool_call_id": "call_1"}, msgs[2])
	})
	t.Run("keeps model ids with slashes and colons", func(t *testing.T) {
		t.Parallel()
		var c capture
		p := newTestProvider(t, c.record(replay(t, "stream.response.txt")), llm.Endpoint{})
		req := llm.Request{Model: "qwen3.5:9b", Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}}
		_, err := p.Chat(t.Context(), req)
		require.NoError(t, err)
		assert.Equal(t, "qwen3.5:9b", c.json(t)["model"])
	})
	t.Run("a dropped stream is ErrStreamDropped", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"par"},"finish_reason":null}]}`+"\n\n")
		}, llm.Endpoint{})
		_, err := p.Chat(t.Context(), userRequest("hi"))
		require.ErrorIs(t, err, llm.ErrStreamDropped)
		retry, _ := llm.Retryable(err)
		assert.True(t, retry)
	})
	t.Run("a stream silent past the idle timeout is ErrStreamStalled", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, `data: {"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`+"\n\n")
			_ = http.NewResponseController(w).Flush()
			<-r.Context().Done()
		}, llm.Endpoint{IdleTimeout: 100 * time.Millisecond})
		_, err := p.Chat(t.Context(), userRequest("hi"))
		require.ErrorIs(t, err, llm.ErrStreamStalled)
		retry, _ := llm.Retryable(err)
		assert.True(t, retry)
	})
	t.Run("a request over request_timeout is retryable", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, func(_ http.ResponseWriter, r *http.Request) {
			_, _ = io.ReadAll(r.Body)
			<-r.Context().Done()
		}, llm.Endpoint{RequestTimeout: 100 * time.Millisecond})
		_, err := p.Chat(t.Context(), userRequest("hi"))
		retry, _ := llm.Retryable(err)
		assert.True(t, retry)
	})
	t.Run("a cancelled caller context comes back unwrapped and not retryable", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, replay(t, "stream.response.txt"), llm.Endpoint{RequestTimeout: time.Minute})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := p.Chat(ctx, userRequest("hi"))
		assert.Equal(t, context.Canceled, err)
		retry, _ := llm.Retryable(err)
		assert.False(t, retry)
	})
	t.Run("a refused connection is retryable", func(t *testing.T) {
		t.Parallel()
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		addr := listener.Addr().String()
		require.NoError(t, listener.Close())
		p := New(llm.Endpoint{URL: "http://" + addr}, http.DefaultClient, discardLogger())
		_, err = p.Chat(t.Context(), userRequest("hi"))
		retry, _ := llm.Retryable(err)
		assert.True(t, retry)
	})
	t.Run("status failures are classified", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			name      string
			status    int
			body      string
			headers   map[string]string
			wantErr   error
			wantText  string
			wantRetry bool
			wantAfter time.Duration
		}{
			{name: "500 is retryable", status: 500, body: `{"error":"boom"}`, wantRetry: true},
			{name: "503 honours Retry-After seconds", status: 503, body: `{}`, headers: map[string]string{"Retry-After": "7"}, wantRetry: true, wantAfter: 7 * time.Second},
			{name: "429 is retryable", status: 429, body: `{}`, wantRetry: true},
			{name: "string error without tools", status: 400, body: `{"error":"m does not support tools"}`, wantErr: llm.ErrNoTools},
			{name: "object error without tools", status: 400, body: `{"error":{"message":"model does not support tools"}}`, wantErr: llm.ErrNoTools},
			{name: "404 is a missing model", status: 404, body: fixtureText(t, "model-not-found.response.json"), wantErr: llm.ErrModelMissing},
			{name: "400 for context length", status: 400, body: `{"error":{"message":"prompt exceeds the context length"}}`, wantErr: llm.ErrContextExceeded},
			{name: "other 400 keeps the server message", status: 400, body: `{"error":{"message":"bad field"}}`, wantText: "bad field"},
		}
		for _, tc := range cases {
			p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}, llm.Endpoint{})
			_, err := p.Chat(t.Context(), userRequest("hi"))
			require.Error(t, err, tc.name)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr, tc.name)
			}
			if tc.wantText != "" {
				require.ErrorContains(t, err, tc.wantText, tc.name)
			}
			retry, after := llm.Retryable(err)
			assert.Equal(t, tc.wantRetry, retry, tc.name)
			assert.Equal(t, tc.wantAfter, after, tc.name)
		}
	})
}

func fixtureText(t *testing.T, name string) string {
	t.Helper()
	return string(fixture(t, name))
}

func TestProvider_Models(t *testing.T) {
	t.Parallel()
	t.Run("lists the served ids", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, replay(t, "models.response.json"), llm.Endpoint{})
		ids, err := p.Models(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"qwen3:8b", "llama3.2:3b"}, ids)
	})
	t.Run("keeps ids with slashes and colons unchanged", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"qwen/qwen3.5-9b"},{"id":"qwen3.5:9b"}]}`)
		}, llm.Endpoint{})
		ids, err := p.Models(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"qwen/qwen3.5-9b", "qwen3.5:9b"}, ids)
	})
	t.Run("an unreachable endpoint is ErrUnreachable", func(t *testing.T) {
		t.Parallel()
		p := New(llm.Endpoint{URL: "http://127.0.0.1:1"}, http.DefaultClient, discardLogger())
		_, err := p.Models(t.Context())
		require.ErrorIs(t, err, llm.ErrUnreachable)
	})
}
