package fakeollama

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/yamlconfig"
)

func TestServer_handleGetFaults(t *testing.T) {
	t.Parallel()
	t.Run("returns an empty list when there are no faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := do(t, http.MethodGet, s.ControlURL()+"/faults", "")
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.JSONEq(t, `[]`, body)
	})
	t.Run("returns active faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Model: "fake-chat", SlowFirstToken: yamlconfig.Duration(1500000000), Count: 2}))
		_, body := do(t, http.MethodGet, s.ControlURL()+"/faults", "")
		assert.JSONEq(t, `[{"model":"fake-chat","count":2,"slow_first_token":"1.5s"}]`, body)
	})
}

func TestServer_handlePutFaults(t *testing.T) {
	t.Parallel()
	t.Run("accepts a single fault", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, body := do(t, http.MethodPut, s.ControlURL()+"/faults", `{"drop_after_chunks": 3}`)
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.JSONEq(t, `[{"drop_after_chunks":3}]`, body)
		assert.Equal(t, []Fault{{DropAfterChunks: 3}}, s.Faults())
	})
	t.Run("accepts a list of faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, _ := do(t, http.MethodPut, s.ControlURL()+"/faults", `[{"status":500,"model":"fake-chat"},{"hang":true,"count":1}]`)
		require.Equal(t, http.StatusOK, res.StatusCode)
		assert.Equal(t, []Fault{{Status: 500, Model: "fake-chat"}, {Hang: true, Count: 1}}, s.Faults())
	})
	t.Run("down refuses API connections", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, _ := do(t, http.MethodPut, s.ControlURL()+"/faults", `{"down": true}`)
		require.Equal(t, http.StatusOK, res.StatusCode)
		_, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
		assert.ErrorIs(t, err, syscall.ECONNREFUSED)
	})
	t.Run("rejects invalid faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		for _, body := range []string{``, `{`, `{"bogus":1}`, `[{"bogus":1}]`, `{}`, `{"status":200}`} {
			t.Run(body, func(t *testing.T) {
				t.Parallel()
				res, resBody := do(t, http.MethodPut, s.ControlURL()+"/faults", body)
				assert.Equal(t, http.StatusBadRequest, res.StatusCode)
				assert.Contains(t, resBody, "invalid fault")
			})
		}
	})
	t.Run("reports listener failures", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		occupy(t, strings.TrimPrefix(s.URL(), "http://"))
		res, body := do(t, http.MethodPut, s.ControlURL()+"/faults", `{"status":500}`)
		require.Equal(t, http.StatusInternalServerError, res.StatusCode)
		assert.Contains(t, body, "address already in use")
	})
}

func TestServer_handleDeleteFaults(t *testing.T) {
	t.Parallel()
	t.Run("clears faults", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		res, _ := do(t, http.MethodDelete, s.ControlURL()+"/faults", "")
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		res, _ = do(t, http.MethodGet, s.URL()+"/v1/models", "")
		assert.Equal(t, http.StatusOK, res.StatusCode)
	})
	t.Run("reports listener failures", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		occupy(t, strings.TrimPrefix(s.URL(), "http://"))
		res, _ := do(t, http.MethodDelete, s.ControlURL()+"/faults", "")
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	})
}

func TestServer_handleGetRequests(t *testing.T) {
	t.Parallel()
	s := startServer(t, testConfig())
	chat(t, s, `{"model":"fake-chat","messages":[{"role":"user","content":"hi"}]}`)
	res, body := do(t, http.MethodGet, s.ControlURL()+"/requests", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var requests []map[string]any
	require.NoError(t, json.Unmarshal([]byte(body), &requests))
	require.Len(t, requests, 1)
	assert.Equal(t, "fake-chat", requests[0]["model"])
	assert.Equal(t, "/v1/chat/completions", requests[0]["path"])
	reqBody, ok := requests[0]["body"].(map[string]any)
	require.True(t, ok)
	messages, ok := reqBody["messages"].([]any)
	require.True(t, ok)
	first, ok := messages[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "hi", first["content"])
	t.Run("records an identifier for the bearer token, never the token", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		res, err := send(t.Context(), http.MethodGet, s.URL()+"/v1/models", "")
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.URL()+"/v1/chat/completions", strings.NewReader(`{"model":"fake-chat","messages":[{"role":"user","content":"hi"}]}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret")
		res, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		recorded := s.Requests()
		require.Len(t, recorded, 2)
		assert.Empty(t, recorded[0].Auth)
		assert.Equal(t, "bearer:2bb80d53", recorded[1].Auth)
		body, err := json.Marshal(recorded)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "secret")
	})
}

func TestAuthOf(t *testing.T) {
	t.Run("is empty without an Authorization header", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		assert.Empty(t, authOf(req))
	})
	t.Run("hashes the bearer token", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "Bearer secret")
		assert.Equal(t, "bearer:2bb80d53", authOf(req))
	})
	t.Run("accepts any case of the scheme", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "bearer secret")
		assert.Equal(t, "bearer:2bb80d53", authOf(req))
	})
	t.Run("hashes a value without a scheme whole", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		req.Header.Set("Authorization", "secret")
		assert.Equal(t, "bearer:2bb80d53", authOf(req))
	})
}

func TestServer_handleReset(t *testing.T) {
	t.Parallel()
	t.Run("clears faults and requests", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Status: 500}))
		do(t, http.MethodGet, s.URL()+"/v1/models", "")
		res, _ := do(t, http.MethodPost, s.ControlURL()+"/reset", "")
		require.Equal(t, http.StatusNoContent, res.StatusCode)
		assert.Empty(t, s.Faults())
		assert.Empty(t, s.Requests())
	})
	t.Run("reports listener failures", func(t *testing.T) {
		t.Parallel()
		s := startServer(t, testConfig())
		require.NoError(t, s.SetFaults(t.Context(), Fault{Down: true}))
		occupy(t, strings.TrimPrefix(s.URL(), "http://"))
		res, _ := do(t, http.MethodPost, s.ControlURL()+"/reset", "")
		assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
	})
}
