package envagent

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/environment/envtest"
	"github.com/bobcob7/workspaces/internal/envproto"
)

// startAPIFixture starts a controller whose artifact API answers with stub,
// enrolls fixtureName into files, and saves the controller's API endpoint in them.
func startAPIFixture(t *testing.T, stub http.Handler) (*environment.Store, *environment.Controller, *Files) {
	t.Helper()
	ctx := t.Context()
	root := t.TempDir()
	store := environment.NewStore(filepath.Join(root, "environments"), filepath.Join(root, "state"), time.Now, rand.Reader)
	ctrl := environment.NewController(discardLog(), store, envtest.NewBroker(), environment.Settings{
		Listen:          "127.0.0.1:0",
		EnrollListen:    "127.0.0.1:0",
		APIListen:       "127.0.0.1:0",
		PublicName:      "127.0.0.1",
		RenewBeforeDays: 30,
	}, time.Now)
	t.Cleanup(func() { assert.NoError(t, ctrl.Close()) })
	ctrl.SetArtifactAPI(stub)
	_, _, _, err := store.Add(ctx, fixtureName, "127.0.0.1:1")
	require.NoError(t, err)
	require.NoError(t, ctrl.Start(ctx))
	_, code, _, err := store.Rotate(ctx, fixtureName, ctrl.Status().EnrollListen)
	require.NoError(t, err)
	files, enroller := newFilesAndEnroller(t)
	_, err = enroller.Enroll(ctx, code)
	require.NoError(t, err)
	require.NoError(t, files.SaveAPIEndpoint(ctrl.APIEndpoint(), time.Now()))
	return store, ctrl, files
}

// writeAPIFile writes body as api.toml, bypassing SaveAPIEndpoint's checks.
func writeAPIFile(t *testing.T, files *Files, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(files.path(apiFileName), []byte(body), fileMode))
}

func TestFiles_APIEndpoint(t *testing.T) {
	t.Parallel()
	t.Run("reports ErrNoAPIEndpoint when api.toml is missing", func(t *testing.T) {
		t.Parallel()
		_, err := NewFiles(t.TempDir()).APIEndpoint()
		assert.ErrorIs(t, err, ErrNoAPIEndpoint)
	})
	t.Run("returns the endpoint that was saved", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		require.NoError(t, files.SaveAPIEndpoint("127.0.0.1:8885", time.Now()))
		endpoint, err := files.APIEndpoint()
		require.NoError(t, err)
		assert.Equal(t, "127.0.0.1:8885", endpoint)
	})
	t.Run("saves api.toml with mode 0600", func(t *testing.T) {
		t.Parallel()
		if runtime.GOOS == "windows" {
			t.Skip("file modes don't apply on Windows")
		}
		files := NewFiles(t.TempDir())
		require.NoError(t, files.SaveAPIEndpoint("127.0.0.1:8885", time.Now()))
		info, err := os.Stat(files.path(apiFileName))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("refuses an unknown key", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		writeAPIFile(t, files, "version = 1\nendpoint = \"127.0.0.1:8885\"\nupdated_at = \"2026-11-27T00:00:00Z\"\nextra = 1\n")
		_, err := files.APIEndpoint()
		assert.ErrorContains(t, err, "unknown key extra")
	})
	t.Run("refuses another version", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		writeAPIFile(t, files, "version = 2\nendpoint = \"127.0.0.1:8885\"\nupdated_at = \"2026-11-27T00:00:00Z\"\n")
		_, err := files.APIEndpoint()
		assert.ErrorContains(t, err, "version 2 isn't 1")
	})
	t.Run("refuses an endpoint without a port", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		writeAPIFile(t, files, "version = 1\nendpoint = \"127.0.0.1\"\nupdated_at = \"2026-11-27T00:00:00Z\"\n")
		_, err := files.APIEndpoint()
		assert.Error(t, err)
	})
	t.Run("SaveAPIEndpoint refuses a bad endpoint and writes nothing", func(t *testing.T) {
		t.Parallel()
		files := NewFiles(t.TempDir())
		require.Error(t, files.SaveAPIEndpoint("127.0.0.1", time.Now()))
		_, err := os.Stat(files.path(apiFileName))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestNewAPIClient(t *testing.T) {
	t.Parallel()
	t.Run("a machine that isn't enrolled is an identity error", func(t *testing.T) {
		t.Parallel()
		_, err := NewAPIClient(NewFiles(t.TempDir()))
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, envproto.ReasonIdentity, apiErr.Reason)
	})
	t.Run("an enrolled machine without api.toml is a no_endpoint error", func(t *testing.T) {
		t.Parallel()
		_, _, files := startAPIFixture(t, http.NotFoundHandler())
		require.NoError(t, os.Remove(files.path(apiFileName)))
		_, err := NewAPIClient(files)
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, envproto.ReasonNoEndpoint, apiErr.Reason)
		assert.Contains(t, apiErr.Error(), "upgrade ws on the controller")
	})
	t.Run("an enrolled machine with api.toml knows the endpoint", func(t *testing.T) {
		t.Parallel()
		_, ctrl, files := startAPIFixture(t, http.NotFoundHandler())
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		assert.Equal(t, ctrl.APIEndpoint(), client.Endpoint())
	})
}

func TestAPIClient_Do(t *testing.T) {
	t.Parallel()
	t.Run("a GET reaches the stub as this environment with its query", func(t *testing.T) {
		t.Parallel()
		seen := make(chan seenRequest, 1)
		stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := environment.APICallerFrom(r.Context())
			seen <- seenRequest{caller: caller, ok: ok, method: r.Method, mine: r.URL.Query().Get("mine"), workspace: r.Header.Get("Ws-Workspace")}
			w.WriteHeader(http.StatusOK)
		})
		_, _, files := startAPIFixture(t, stub)
		id, err := files.Load()
		require.NoError(t, err)
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		header := http.Header{"Ws-Workspace": {"ws-0a1b2c3d"}}
		resp, err := client.Do(t.Context(), http.MethodGet, "/v1/artifacts", url.Values{"mine": {"true"}}, header, nil, 0)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		got := <-seen
		assert.Equal(t, http.MethodGet, got.method)
		assert.Equal(t, "true", got.mine)
		assert.Equal(t, "ws-0a1b2c3d", got.workspace)
		require.True(t, got.ok)
		assert.Equal(t, id.ID, got.caller.EnvironmentID)
	})
	t.Run("a 10 MiB POST body arrives whole", func(t *testing.T) {
		t.Parallel()
		stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			sum := sha256.Sum256(body)
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:])))
		})
		_, _, files := startAPIFixture(t, stub)
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		payload := make([]byte, 10<<20)
		_, err = rand.Read(payload)
		require.NoError(t, err)
		resp, err := client.Do(t.Context(), http.MethodPost, "/v1/artifacts", nil, nil, bytes.NewReader(payload), int64(len(payload)))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		answer, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		sum := sha256.Sum256(payload)
		assert.Equal(t, hex.EncodeToString(sum[:]), string(answer))
	})
	t.Run("a closed controller is an offline error naming the endpoint", func(t *testing.T) {
		t.Parallel()
		_, ctrl, files := startAPIFixture(t, http.NotFoundHandler())
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		require.NoError(t, ctrl.Close())
		_, err = client.Do(t.Context(), http.MethodGet, "/v1/artifacts", nil, nil, nil, 0)
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, envproto.ReasonOffline, apiErr.Reason)
		assert.Equal(t, client.Endpoint(), apiErr.Endpoint)
		assert.Contains(t, apiErr.Error(), client.Endpoint())
		assert.Contains(t, apiErr.Error(), "is reachable from this machine")
	})
	t.Run("roots without the controller's CA are a certificate error", func(t *testing.T) {
		t.Parallel()
		_, _, files := startAPIFixture(t, http.NotFoundHandler())
		other, _ := newTestCA(t, 99)
		require.NoError(t, files.SaveRoots([]*x509.Certificate{other}))
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		_, err = client.Do(t.Context(), http.MethodGet, "/v1/artifacts", nil, nil, nil, 0)
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, envproto.ReasonCertificate, apiErr.Reason)
	})
	t.Run("a client without a certificate is refused as a certificate error", func(t *testing.T) {
		t.Parallel()
		_, _, files := startAPIFixture(t, http.NotFoundHandler())
		id, err := files.Load()
		require.NoError(t, err)
		endpoint, err := files.APIEndpoint()
		require.NoError(t, err)
		pool := x509.NewCertPool()
		for _, root := range id.Roots {
			pool.AddCert(root)
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: id.ServerName}}
		client := &APIClient{endpoint: endpoint, client: &http.Client{Transport: transport}}
		_, err = client.Do(t.Context(), http.MethodGet, "/v1/artifacts", nil, nil, nil, 0)
		var apiErr *APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, envproto.ReasonCertificate, apiErr.Reason)
	})
	t.Run("a revoked environment gets the controller's 403, not an error", func(t *testing.T) {
		t.Parallel()
		store, ctrl, files := startAPIFixture(t, http.NotFoundHandler())
		client, err := NewAPIClient(files)
		require.NoError(t, err)
		_, _, _, err = store.Rotate(t.Context(), fixtureName, ctrl.Status().EnrollListen)
		require.NoError(t, err)
		resp, err := client.Do(t.Context(), http.MethodGet, "/v1/artifacts", nil, nil, nil, 0)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// seenRequest is what a stub artifact API saw of one request.
type seenRequest struct {
	caller    environment.APICaller
	ok        bool
	method    string
	mine      string
	workspace string
}
