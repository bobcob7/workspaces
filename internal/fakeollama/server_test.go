package fakeollama

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		Models: []Model{
			{Name: "fake-chat", Capabilities: []Capability{CapabilityTools}},
			{Name: "fake-reasoning", Capabilities: []Capability{CapabilityTools, CapabilityReasoning}},
		},
		Rules: []Rule{
			{Match: `(?i)status of order ([A-Z]-\d+)`, Tool: "lookup_order_status", Arguments: `{"order_id":"{{ index .Match 1 }}"}`},
			{Match: "(?i)17 multiplied by 23", Reply: "391"},
		},
	}
}

func startServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	require.NoError(t, err)
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, s.Shutdown(context.WithoutCancel(t.Context()))) })
	return s
}

// send opens a new connection for every request, so tests see the
// listener's state rather than a pooled connection.
func send(ctx context.Context, method, url, body string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	return client.Do(req)
}

func do(t *testing.T, method, url, body string) (*http.Response, string) {
	t.Helper()
	res, err := send(t.Context(), method, url, body)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res, string(data)
}

func chat(t *testing.T, s *Server, body string) (*http.Response, string) {
	t.Helper()
	return do(t, http.MethodPost, s.URL()+"/v1/chat/completions", body)
}

func occupy(t *testing.T, addr string) {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
}

func TestNew(t *testing.T) {
	t.Run("rejects invalid models", func(t *testing.T) {
		_, err := New(Config{}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		assert.ErrorIs(t, err, errInvalidConfig)
	})
	t.Run("rejects negative timing", func(t *testing.T) {
		cfg := testConfig()
		cfg.Timing.ChunkInterval = -1
		_, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		assert.ErrorIs(t, err, errInvalidConfig)
	})
	t.Run("rejects invalid rules", func(t *testing.T) {
		cfg := testConfig()
		cfg.Rules = []Rule{{Match: "("}}
		_, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		assert.ErrorIs(t, err, errInvalidConfig)
	})
	t.Run("rejects invalid faults", func(t *testing.T) {
		cfg := testConfig()
		cfg.Faults = []Fault{{Model: "missing", Hang: true}}
		_, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.ErrorIs(t, err, errInvalidConfig)
		assert.ErrorIs(t, err, errInvalidFault)
	})
	t.Run("defaults words per chunk to one", func(t *testing.T) {
		s, err := New(testConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.Equal(t, 1, s.cfg.Timing.WordsPerChunk)
	})
	t.Run("activates configured faults", func(t *testing.T) {
		cfg := testConfig()
		cfg.Faults = []Fault{{Status: 500}}
		s, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.Equal(t, cfg.Faults, s.Faults())
	})
}

func TestServer_Start(t *testing.T) {
	t.Parallel()
	t.Run("serves API and control API on separate ports", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, _ := do(t, http.MethodGet, s.URL()+"/v1/models", "")
		assert.Equal(t, http.StatusOK, res.StatusCode)
		res, _ = do(t, http.MethodGet, s.ControlURL()+"/faults", "")
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.NotEqual(t, s.URL(), s.ControlURL())
	})
	t.Run("fails when the control address is taken", func(t *testing.T) {
		t.Parallel()
		taken := startServer(t, testConfig())
		cfg := testConfig()
		cfg.ControlListen = strings.TrimPrefix(taken.ControlURL(), "http://")
		s, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.ErrorContains(t, s.Start(t.Context()), "starting control API")
	})
	t.Run("fails when the API address is taken", func(t *testing.T) {
		t.Parallel()
		taken := startServer(t, testConfig())
		cfg := testConfig()
		cfg.Listen = strings.TrimPrefix(taken.URL(), "http://")
		s, err := New(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.ErrorContains(t, s.Start(t.Context()), "starting API")
	})
	t.Run("starts down when configured down", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig()
		cfg.Faults = []Fault{{Down: true}}
		s := startServer(t, cfg)
		_, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
		assert.ErrorIs(t, err, syscall.ECONNREFUSED)
	})
}

func TestServer_Shutdown(t *testing.T) {
	t.Parallel()
	t.Run("releases hung requests", func(t *testing.T) {
		t.Parallel()
		s, err := New(testConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		require.NoError(t, s.SetFaults(t.Context(), Fault{Hang: true}))
		errs := make(chan error, 1)
		go func() {
			res, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
			if err == nil {
				_ = res.Body.Close()
			}
			errs <- err
		}()
		require.Eventually(t, func() bool { return len(s.Requests()) == 1 }, time.Second, time.Millisecond)
		require.NoError(t, s.Shutdown(t.Context()))
		assert.Error(t, <-errs)
	})
	t.Run("can't be brought back up", func(t *testing.T) {
		t.Parallel()
		s, err := New(testConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		require.NoError(t, s.Start(t.Context()))
		require.NoError(t, s.Shutdown(t.Context()))
		require.NoError(t, s.ClearFaults(t.Context()))
		_, err = send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
		assert.Error(t, err)
	})
}

func TestServer_modelIDs(t *testing.T) {
	t.Parallel()
	cfg := Config{
		Models: []Model{
			{Name: "qwen/qwen3.5-9b", Capabilities: []Capability{CapabilityTools}, ContextLength: 32768},
			{Name: "qwen3.5:9b", Capabilities: []Capability{CapabilityTools}, ContextLength: 32768},
		},
		Rules: []Rule{{Match: "", Reply: "{{ .Model }}"}},
	}
	t.Run("lists each id as written", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, cfg)
		_, models := do(t, http.MethodGet, s.URL()+"/v1/models", "")
		assert.Contains(t, models, `"id":"qwen/qwen3.5-9b"`)
		assert.Contains(t, models, `"id":"qwen3.5:9b"`)
		_, tags := do(t, http.MethodGet, s.URL()+"/api/tags", "")
		assert.Contains(t, tags, `"name":"qwen/qwen3.5-9b"`)
		assert.Contains(t, tags, `"name":"qwen3.5:9b"`)
	})
	t.Run("answers show and chat for each id", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, cfg)
		_, show := do(t, http.MethodPost, s.URL()+"/api/show", `{"model":"qwen3.5:9b"}`)
		assert.Contains(t, show, `"capabilities":["completion","tools"]`)
		_, out := chat(t, s, `{"model":"qwen/qwen3.5-9b","messages":[{"role":"user","content":"hi"}]}`)
		assert.Contains(t, out, `"content":"qwen/qwen3.5-9b"`)
	})
	t.Run("scopes faults to the id as written", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, cfg)
		require.NoError(t, s.SetFaults(t.Context(), Fault{Model: "qwen3.5:9b", Status: 503}))
		res, _ := chat(t, s, `{"model":"qwen3.5:9b","messages":[{"role":"user","content":"hi"}]}`)
		assert.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
		res, _ = chat(t, s, `{"model":"qwen/qwen3.5-9b","messages":[{"role":"user","content":"hi"}]}`)
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
}

func TestServer_SetFaults(t *testing.T) {
	t.Parallel()
	t.Run("down refuses connections while the control API stays up", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		_, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
		require.ErrorIs(t, err, syscall.ECONNREFUSED)
		res, _ := do(t, http.MethodGet, s.ControlURL()+"/faults", "")
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("clearing down serves on the same address again", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		url := s.URL()
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 503, Model: "fake-chat"}))
		res, _ := do(t, http.MethodGet, url+"/v1/models", "")
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("down drops requests in progress", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Hang: true}))
		errs := make(chan error, 1)
		go func() {
			res, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
			if err == nil {
				_ = res.Body.Close()
			}
			errs <- err
		}()
		require.Eventually(t, func() bool { return len(s.Requests()) == 1 }, time.Second, time.Millisecond)
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		assert.Error(t, <-errs)
	})
	t.Run("rejects invalid faults and keeps the active ones", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500}))
		err := s.SetFaults(t.Context(), Fault{Status: 200})
		require.ErrorIs(t, err, errInvalidFault)
		assert.Equal(t, []Fault{{Status: 500}}, s.Faults())
	})
	t.Run("fails when the address was taken while down", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		occupy(t, strings.TrimPrefix(s.URL(), "http://"))
		assert.ErrorContains(t, s.ClearFaults(t.Context()), "listening on")
	})
}

func TestServer_ClearFaults(t *testing.T) {
	t.Parallel()
	s := startServer(t, testConfig())
	require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500}, Fault{Hang: true, Model: "fake-chat"}))
	require.NoError(t, s.ClearFaults(t.Context()))
	assert.Empty(t, s.Faults())
}

func TestServer_Faults(t *testing.T) {
	t.Parallel()
	t.Run("reports remaining counts", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500, Count: 3}))
		do(t, http.MethodGet, s.URL()+"/v1/models", "")
		assert.Equal(t, []Fault{{Status: 500, Count: 2}}, s.Faults())
	})
	t.Run("returns a copy", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500}))
		s.Faults()[0].Status = 502
		assert.Equal(t, 500, s.Faults()[0].Status)
	})
}

func TestServer_Requests(t *testing.T) {
	t.Parallel()
	t.Run("records chat requests with their model and body", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		body := `{"model":"fake-chat","stream":true,"messages":[{"role":"user","content":"hi"}]}`
		chat(t, s, body)
		requests := s.Requests()
		require.Len(t, requests, 1)
		assert.Equal(t, "fake-chat", requests[0].Model)
		assert.True(t, requests[0].Stream)
		assert.Equal(t, "/v1/chat/completions", requests[0].Path)
		assert.JSONEq(t, body, string(requests[0].Body))
	})
	t.Run("records the caller's traceparent", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		for _, path := range []string{"/v1/models", "/v1/chat/completions"} {
			method := http.MethodGet
			if path == "/v1/chat/completions" {
				method = http.MethodPost
			}
			req, err := http.NewRequestWithContext(t.Context(), method, s.URL()+path, strings.NewReader(`{"model":"fake-chat","messages":[{"role":"user","content":"hi"}]}`))
			require.NoError(t, err)
			req.Header.Set("traceparent", traceparent)
			res, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
		}
		for _, r := range s.Requests() {
			assert.Equal(t, traceparent, r.Traceparent, r.Path)
		}
	})
	t.Run("records invalid bodies without the body", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		chat(t, s, "{")
		requests := s.Requests()
		require.Len(t, requests, 1)
		assert.Nil(t, requests[0].Body)
	})
	t.Run("records model listings", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		do(t, http.MethodGet, s.URL()+"/v1/models", "")
		requests := s.Requests()
		require.Len(t, requests, 1)
		assert.Equal(t, http.MethodGet, requests[0].Method)
		assert.Equal(t, "/v1/models", requests[0].Path)
	})
}

func TestServer_Reset(t *testing.T) {
	t.Parallel()
	s := startServer(t, testConfig())
	require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
	do(t, http.MethodGet, s.ControlURL()+"/faults", "")
	require.NoError(t, s.Reset(t.Context()))
	assert.Empty(t, s.Faults())
	assert.Empty(t, s.Requests())
	res, _ := do(t, http.MethodGet, s.URL()+"/v1/models", "")
	assert.Equal(t, http.StatusOK, res.StatusCode)
}

func TestServer_takeFault(t *testing.T) {
	newServer := func(t *testing.T, faults ...Fault) *Server {
		t.Helper()
		s, err := New(testConfig(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		require.NoError(t, s.SetFaults(t.Context(), faults...))
		return s
	}
	t.Run("removes a fault when its count is used up", func(t *testing.T) {
		s := newServer(t, Fault{Status: 500, Count: 1})
		assert.Equal(t, Fault{Status: 500, Count: 1}, s.takeFault(target{}))
		assert.Equal(t, Fault{}, s.takeFault(target{}))
	})
	t.Run("keeps uncounted faults", func(t *testing.T) {
		s := newServer(t, Fault{Status: 500})
		s.takeFault(target{})
		assert.Equal(t, Fault{Status: 500}, s.takeFault(target{}))
	})
	t.Run("skips faults that don't affect the request", func(t *testing.T) {
		s := newServer(t, Fault{DropAfterChunks: 1, Count: 1}, Fault{Status: 502, Model: "fake-chat"})
		assert.Equal(t, Fault{}, s.takeFault(target{model: "fake-reasoning"}))
		assert.Equal(t, 502, s.takeFault(target{model: "fake-chat"}).Status)
		assert.Equal(t, 1, s.takeFault(target{model: "fake-chat", stream: true}).DropAfterChunks)
	})
}

func TestSleep(t *testing.T) {
	t.Run("waits for the duration", func(t *testing.T) {
		start := time.Now()
		assert.True(t, sleep(t.Context(), 20*time.Millisecond))
		assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
	})
	t.Run("returns false when the context ends", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.False(t, sleep(ctx, time.Hour))
		assert.False(t, sleep(ctx, 0))
	})
}
