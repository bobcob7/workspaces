package environment

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// callerStub answers with the caller's id and name, and records in ran that it ran.
func callerStub(ran *atomic.Bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ran != nil {
			ran.Store(true)
		}
		caller, ok := APICallerFrom(r.Context())
		if !ok {
			writeError(w, http.StatusInternalServerError, "internal", "no caller")
			return
		}
		fmt.Fprintf(w, "%s %s", caller.EnvironmentID, caller.EnvironmentName)
	})
}

// apiTestServer serves c's artifact API over mutual TLS, as the listener does.
func apiTestServer(t *testing.T, c *Controller) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(c.apiHandler())
	srv.TLS = brokerTLS(c.currentState)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

// apiClient is an HTTPS client that presents cert (none when nil) and trusts store's roots.
func apiClient(t *testing.T, store *Store, cert *tls.Certificate) *http.Client {
	t.Helper()
	transport := &http.Transport{TLSClientConfig: agentTLS(t, store, cert)}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

// apiDo sends one request and returns its status and body; err is the transport's error.
func apiDo(t *testing.T, client *http.Client, method, url string, body io.Reader) (int, []byte, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	require.NoError(t, err)
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { require.NoError(t, resp.Body.Close()) }()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, data, nil
}

// remoteErrorOp is the op of the *net.OpError a refused TLS handshake returns.
func remoteErrorOp(t *testing.T, err error) string {
	t.Helper()
	var opErr *net.OpError
	require.ErrorAs(t, err, &opErr)
	return opErr.Op
}

// enrollThrough enrolls name in store and returns the certificate its agent
// holds; enroller is a store over the same directories, with another clock.
func enrollThrough(t *testing.T, store, enroller *Store, name string) tls.Certificate {
	t.Helper()
	_, code := addEnvironment(t, store, name)
	parsed, err := envproto.ParseCode(code)
	require.NoError(t, err)
	key := newTestKey(t)
	resp, err := enroller.Enroll(t.Context(), parsed.Secret, newTestCSR(t, key), parsed.Host, "127.0.0.1")
	require.NoError(t, err)
	return tls.Certificate{Certificate: [][]byte{parseCertPEM(t, resp.Cert).Raw}, PrivateKey: key}
}

func TestController_apiHandler(t *testing.T) {
	t.Run("serves an enrolled agent's request with its environment", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		rec, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		status, body, err := apiDo(t, apiClient(t, store, &cert), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
		assert.Equal(t, rec.ID+" linux-server", string(body))
	})
	t.Run("fails the handshake for a certificate another CA issued", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrollAgent(t, store, "linux-server")
		var ran atomic.Bool
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(&ran))
		srv := apiTestServer(t, c)
		_, otherCert := enrollAgent(t, newTestStore(t), "elsewhere")
		_, _, err := apiDo(t, apiClient(t, store, &otherCert), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.Error(t, err)
		assert.Equal(t, "remote error", remoteErrorOp(t, err))
		assert.False(t, ran.Load())
	})
	t.Run("fails the handshake for an expired certificate", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		envs, state := filepath.Join(root, "environments"), filepath.Join(root, "state")
		store := NewStore(envs, state, fixedClock(), rand.Reader)
		hundredDaysAgo := NewStore(envs, state, func() time.Time { return store.now().AddDate(0, 0, -100) }, rand.Reader)
		cert := enrollThrough(t, store, hundredDaysAgo, "linux-server")
		var ran atomic.Bool
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(&ran))
		srv := apiTestServer(t, c)
		_, _, err := apiDo(t, apiClient(t, store, &cert), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.Error(t, err)
		assert.Equal(t, "remote error", remoteErrorOp(t, err))
		assert.False(t, ran.Load())
	})
	t.Run("fails the handshake without a client certificate", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		enrollAgent(t, store, "linux-server")
		var ran atomic.Bool
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(&ran))
		srv := apiTestServer(t, c)
		_, _, err := apiDo(t, apiClient(t, store, nil), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.Error(t, err)
		assert.Equal(t, "remote error", remoteErrorOp(t, err))
		assert.False(t, ran.Load())
	})
	t.Run("answers 403 artifact_unreachable for a rotated environment", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		_, _, _, err := store.Rotate(t.Context(), "linux-server", "127.0.0.1")
		require.NoError(t, err)
		assertUnreachable(t, apiClient(t, store, &cert), srv.URL+envproto.ArtifactsPath)
	})
	t.Run("answers 403 artifact_unreachable for a removed environment", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		_, _, err := store.Remove(t.Context(), "linux-server")
		require.NoError(t, err)
		assertUnreachable(t, apiClient(t, store, &cert), srv.URL+envproto.ArtifactsPath)
	})
	t.Run("answers 404 outside the artifact routes", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		client := apiClient(t, store, &cert)
		for _, path := range []string{"/v1/enroll", "/", envproto.ArtifactsPath + "X", "/v1/artifactsX/", "/v1/artifact"} {
			status, _, err := apiDo(t, client, http.MethodGet, srv.URL+path, nil)
			require.NoError(t, err, path)
			assert.Equal(t, http.StatusNotFound, status, path)
		}
	})
	t.Run("serves the artifact route's sub-paths", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		client := apiClient(t, store, &cert)
		for _, path := range []string{envproto.ArtifactsPath, envproto.ArtifactsPath + "/", envproto.ArtifactsPath + "/art-1/content"} {
			status, _, err := apiDo(t, client, http.MethodGet, srv.URL+path, nil)
			require.NoError(t, err, path)
			assert.Equal(t, http.StatusOK, status, path)
		}
	})
	t.Run("answers 500, not a refusal, when the store can't be read", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		var ran atomic.Bool
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(&ran))
		srv := apiTestServer(t, c)
		require.NoError(t, os.WriteFile(filepath.Join(store.dir, "broken"+recordSuffix), []byte("{"), 0o600))
		status, body, err := apiDo(t, apiClient(t, store, &cert), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, status)
		assert.NotContains(t, string(body), "artifact_unreachable")
		assert.False(t, ran.Load())
	})
	t.Run("frees a POST slot when a POST finishes", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), callerStub(nil))
		srv := apiTestServer(t, c)
		client := apiClient(t, store, &cert)
		for i := range apiMaxInFlight + 2 {
			status, _, err := apiDo(t, client, http.MethodPost, srv.URL+envproto.ArtifactsPath, strings.NewReader("x"))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, status, "POST %d", i)
		}
	})
	t.Run("answers 404 when no artifact handler is set", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		c := startAPIController(t, store, envtest.NewBroker(), nil)
		srv := apiTestServer(t, c)
		status, _, err := apiDo(t, apiClient(t, store, &cert), http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, status)
	})
	t.Run("answers 503 operation_limited to a POST beyond the in-flight cap and still serves a GET", func(t *testing.T) {
		t.Parallel()
		const artifactAPIPostsInFlight = apiMaxInFlight
		store := newTestStore(t)
		_, cert := enrollAgent(t, store, "linux-server")
		entered := make(chan struct{}, artifactAPIPostsInFlight)
		release := make(chan struct{})
		stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				entered <- struct{}{}
				<-release
			}
			w.WriteHeader(http.StatusOK)
		})
		c := startAPIController(t, store, envtest.NewBroker(), stub)
		srv := apiTestServer(t, c)
		client := apiClient(t, store, &cert)
		var wg sync.WaitGroup
		defer func() {
			close(release)
			wg.Wait()
		}()
		for range artifactAPIPostsInFlight {
			wg.Go(func() {
				req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+envproto.ArtifactsPath, strings.NewReader("x"))
				if err != nil {
					t.Error(err)
					return
				}
				resp, err := client.Do(req)
				if err != nil {
					t.Error(err)
					return
				}
				_ = resp.Body.Close() // the POSTs in flight are not checked: the test checks the one beyond the cap
			})
		}
		for range artifactAPIPostsInFlight {
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("the POSTs did not start")
			}
		}
		status, body, err := apiDo(t, client, http.MethodPost, srv.URL+envproto.ArtifactsPath, strings.NewReader("x"))
		require.NoError(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, status)
		assert.Contains(t, string(body), "operation_limited")
		status, _, err = apiDo(t, client, http.MethodGet, srv.URL+envproto.ArtifactsPath, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, status)
	})
}

// assertUnreachable checks that url answers 403 with artifact_unreachable and reason refused.
func assertUnreachable(t *testing.T, client *http.Client, url string) {
	t.Helper()
	status, body, err := apiDo(t, client, http.MethodGet, url, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, status)
	var envelope envproto.ErrorEnvelope
	require.NoError(t, json.Unmarshal(body, &envelope))
	assert.Equal(t, "artifact_unreachable", envelope.Error.Code)
	assert.Equal(t, envproto.ReasonRefused, envelope.Error.Details["reason"])
}
