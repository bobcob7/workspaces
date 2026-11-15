package fakeollama

import (
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/sse"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

// literalKeys are compared by value in golden skeletons; other strings are
// compared by type only.
func literalKeys() []string {
	return []string{"object", "role", "type", "finish_reason", "system_fingerprint", "owned_by"}
}

// skeleton reduces a decoded JSON value to its shape. See testdata/README.md.
func skeleton(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = skeleton(val, k)
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, val := range x {
			out = append(out, skeleton(val, ""))
		}
		return collapse(out)
	case string:
		if slices.Contains(literalKeys(), key) {
			return x
		}
		return "<string>"
	case float64:
		return "<number>"
	case bool:
		return "<bool>"
	default:
		return x
	}
}

func collapse(items []any) []any {
	out := []any{}
	for _, item := range items {
		if len(out) > 0 && reflect.DeepEqual(out[len(out)-1], item) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func jsonSkeleton(t *testing.T, data string) any {
	t.Helper()
	var v any
	require.NoError(t, json.Unmarshal([]byte(data), &v), data)
	return skeleton(v, "")
}

// sseSkeleton checks the stream ends with [DONE] and returns the collapsed
// skeletons of the events before it.
func sseSkeleton(t *testing.T, data string) []any {
	t.Helper()
	r := sse.NewReader(strings.NewReader(data))
	var out []any
	for r.Next() {
		out = append(out, jsonSkeleton(t, string(r.Data())))
	}
	require.NoError(t, r.Err())
	require.True(t, r.Done(), "stream does not end with [DONE]: %q", data)
	return collapse(out)
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(data)
}

// events reads SSE data payloads, including a final [DONE], until the
// stream ends, returning the error that ended it, if any.
func events(body io.Reader) ([]string, error) {
	var out []string
	r := sse.NewReader(body)
	for r.Next() {
		out = append(out, string(r.Data()))
	}
	if r.Done() {
		out = append(out, sse.DoneData)
	}
	return out, r.Err()
}

func streamChat(ctx context.Context, t *testing.T, s *Server) ([]string, error) {
	t.Helper()
	body := `{"model":"fake-chat","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"one two three four five"}]}`
	res, err := send(ctx, http.MethodPost, s.URL()+"/v1/chat/completions", body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	return events(res.Body)
}

func TestServer_handleModels(t *testing.T) {
	t.Parallel()
	t.Run("matches the golden response", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := do(t, http.MethodGet, s.URL()+"/v1/models", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
		assert.Equal(t, jsonSkeleton(t, readFixture(t, "models.response.json")), jsonSkeleton(t, body))
	})
	t.Run("lists every configured model", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		_, body := do(t, http.MethodGet, s.URL()+"/v1/models", "")
		var list openaiwire.ModelList
		require.NoError(t, json.Unmarshal([]byte(body), &list))
		ids := []string{list.Data[0].ID, list.Data[1].ID}
		assert.Equal(t, []string{"fake-chat", "fake-reasoning"}, ids)
	})
	t.Run("applies unscoped faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 503}))
		res, _ := do(t, http.MethodGet, s.URL()+"/v1/models", "")
		assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	})
}

func TestServer_handleChat(t *testing.T) {
	t.Parallel()
	t.Run("matches golden responses", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		for _, name := range []string{"chat", "chat-reasoning", "chat-tools", "model-not-found"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				res, body := chat(t, s, readFixture(t, name+".request.json"))
				assert.Equal(t, "application/json", res.Header.Get("Content-Type"))
				assert.Equal(t, jsonSkeleton(t, readFixture(t, name+".response.json")), jsonSkeleton(t, body))
			})
		}
		for _, name := range []string{"stream", "stream-no-usage", "stream-reasoning", "stream-tools"} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				res, body := chat(t, s, readFixture(t, name+".request.json"))
				assert.Equal(t, "text/event-stream", res.Header.Get("Content-Type"))
				assert.Equal(t, sseSkeleton(t, readFixture(t, name+".response.txt")), sseSkeleton(t, body))
			})
		}
	})
	t.Run("uses Ollama's ID formats", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		_, body := chat(t, s, readFixture(t, "chat-tools.request.json"))
		var completion openaiwire.ChatCompletion
		require.NoError(t, json.Unmarshal([]byte(body), &completion))
		assert.Regexp(t, `^chatcmpl-\d{1,3}$`, completion.ID)
		assert.Regexp(t, `^call_[a-z0-9]{8}$`, completion.Choices[0].Message.ToolCalls[0].ID)
	})
	t.Run("returns 400 for undecodable bodies", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := chat(t, s, "{")
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		assert.Contains(t, body, `"type":"invalid_request_error"`)
	})
	t.Run("returns 400 for missing messages", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := chat(t, s, `{"model":"fake-chat","messages":[]}`)
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		assert.Contains(t, body, errNoMessages.Error())
	})
	t.Run("returns 400 for a missing model", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := chat(t, s, `{"messages":[{"role":"user","content":"hi"}]}`)
		require.Equal(t, http.StatusBadRequest, res.StatusCode)
		assert.Contains(t, body, errNoModel.Error())
	})
	t.Run("returns 500 when a reply template fails", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Rules = []Rule{{Match: ".", Reply: `{{ find "(" .Last }}`}}
		s := startServer(t, cfg)
		res, body := chat(t, s, `{"model":"fake-chat","messages":[{"role":"user","content":"hi"}]}`)
		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
		assert.Contains(t, body, `"type":"api_error"`)
	})
	t.Run("delays non-streaming replies by their generation time", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Timing = Timing{FirstToken: yamlconfig.Duration(30 * time.Millisecond), ChunkInterval: yamlconfig.Duration(10 * time.Millisecond)}
		s := startServer(t, cfg)
		start := time.Now()
		chat(t, s, `{"model":"fake-chat","messages":[{"role":"user","content":"one two three"}]}`)
		assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
	})
	t.Run("streams words per chunk with delays", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Timing = Timing{FirstToken: yamlconfig.Duration(30 * time.Millisecond), ChunkInterval: yamlconfig.Duration(10 * time.Millisecond), WordsPerChunk: 2}
		s := startServer(t, cfg)
		start := time.Now()
		got, err := streamChat(t.Context(), t, s)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)
		assert.Len(t, got, 6, "3 content chunks, finish, usage, [DONE]")
	})
	t.Run("stops streaming when the client goes away", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Timing = Timing{ChunkInterval: yamlconfig.Duration(time.Hour)}
		s := startServer(t, cfg)
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		got, err := streamChat(ctx, t, s)
		require.Error(t, err)
		assert.Len(t, got, 1)
	})
}

func TestServer_applyFault(t *testing.T) {
	t.Parallel()
	t.Run("status responds with an error envelope", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 502}))
		res, body := chat(t, s, readFixture(t, "chat.request.json"))
		require.Equal(t, http.StatusBadGateway, res.StatusCode)
		assert.JSONEq(t, `{"error":{"message":"fake-ollama: injected fault","type":"api_error","param":null,"code":null}}`, body)
	})
	t.Run("status uses a custom message", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 429, Message: "slow down"}))
		_, body := chat(t, s, readFixture(t, "chat.request.json"))
		assert.Contains(t, body, `"message":"slow down"`)
	})
	t.Run("status scoped to a model leaves other models alone", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500, Model: "fake-reasoning"}))
		res, _ := chat(t, s, readFixture(t, "chat.request.json"))
		assert.Equal(t, http.StatusOK, res.StatusCode)
		res, _ = chat(t, s, readFixture(t, "chat-reasoning.request.json"))
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	})
	t.Run("counted faults stop after N requests", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500, Count: 2}))
		var codes []int
		for range 3 {
			res, _ := chat(t, s, readFixture(t, "chat.request.json"))
			codes = append(codes, res.StatusCode)
		}
		assert.Equal(t, []int{500, 500, 200}, codes)
	})
	t.Run("hang never responds", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Hang: true, Model: "fake-chat"}))
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err := send(ctx, http.MethodPost, s.URL()+"/v1/chat/completions", readFixture(t, "chat.request.json"))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		res, _ := chat(t, s, readFixture(t, "chat-reasoning.request.json"))
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("slow first token delays the response", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{SlowFirstToken: yamlconfig.Duration(80 * time.Millisecond)}))
		start := time.Now()
		res, _ := chat(t, s, readFixture(t, "stream.request.json"))
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.GreaterOrEqual(t, time.Since(start), 80*time.Millisecond)
	})
	t.Run("slow first token gives up when the client goes away", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{SlowFirstToken: yamlconfig.Duration(time.Hour)}))
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := send(ctx, http.MethodGet, s.URL()+"/v1/models", "")
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

func TestServer_interrupt(t *testing.T) {
	t.Parallel()
	t.Run("drop closes the connection after N chunks", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{DropAfterChunks: 3}))
		got, err := streamChat(t.Context(), t, s)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		assert.Len(t, got, 3)
	})
	t.Run("drop happens at the end of a shorter stream", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{DropAfterChunks: 100}))
		got, err := streamChat(t.Context(), t, s)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		assert.NotContains(t, got, "[DONE]")
	})
	t.Run("drop leaves non-streaming requests alone", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{DropAfterChunks: 1, Count: 1}))
		res, _ := chat(t, s, readFixture(t, "chat.request.json"))
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Len(t, s.Faults(), 1)
	})
	t.Run("stall stops sending after N chunks without closing", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{StallAfterChunks: 2}))
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		got, err := streamChat(ctx, t, s)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Len(t, got, 2)
	})
	t.Run("stall happens at the end of a shorter stream", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{StallAfterChunks: 100}))
		ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
		defer cancel()
		got, err := streamChat(ctx, t, s)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.NotContains(t, got, "[DONE]")
	})
}
