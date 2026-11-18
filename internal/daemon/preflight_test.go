package daemon

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
)

// preflightConfig is a config.toml with two endpoints. gpu serves gemma and
// qwen; lab, which is unreachable in the tests, serves other.
func preflightConfig(gpuURL, labURL string) string {
	return fmt.Sprintf(`[runner]
model = "gemma"

[endpoints.gpu]
url = %q
probe = "none"

[endpoints.lab]
url = %q
probe = "none"

[models.gemma]
endpoint = "gpu"
id = "gemma3:12b"
context_tokens = 32768

[models.qwen]
endpoint = "gpu"
id = "qwen3.5:9b"
context_tokens = 32768

[models.other]
endpoint = "lab"
id = "qwen3.5:9b"
context_tokens = 32768
`, gpuURL, labURL)
}

// closedURL is an address nothing listens on.
func closedURL(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return "http://" + addr
}

// modelsServer serves /v1/models with the given ids and records the
// authorization header of each request.
func modelsServer(t *testing.T, ids ...string) (*httptest.Server, *[]string) {
	t.Helper()
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		data := make([]map[string]string, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]string{"id": id, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.MarshalWrite(w, map[string]any{"object": "list", "data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &auth
}

// preflightSupervisor is a supervisor whose config is the file at path, read
// at each request as the daemon does.
func preflightSupervisor(t *testing.T, path, configDir string) *Supervisor {
	t.Helper()
	cfg := SupervisorConfig{
		StateDir:   t.TempDir(),
		ConfigDir:  configDir,
		BaseEnv:    []string{"PATH=/usr/bin"},
		LoadConfig: func() (config.Config, error) { return config.ReadFile(path) },
	}
	return NewSupervisor(cfg, nil, nil, discardLog())
}

func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestSupervisor_Preflight(t *testing.T) {
	t.Parallel()
	t.Run("the check answers the object for the first alias on the endpoint", func(t *testing.T) {
		srv, _ := modelsServer(t, "gemma3:12b", "qwen3.5:9b")
		path := writeConfigFile(t, preflightConfig(srv.URL, closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		res, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu"})
		require.NoError(t, err)
		assert.Equal(t, "gpu", res.Endpoint)
		assert.Equal(t, srv.URL, res.URL)
		assert.Equal(t, "openai", res.API)
		assert.Equal(t, "none", res.Probe)
		assert.Equal(t, "gemma3:12b", *res.Model)
		assert.Nil(t, res.Version)
		assert.Nil(t, res.Capabilities)
		assert.Equal(t, 32768, *res.ContextTokens)
		assert.Equal(t, []string{"gemma3:12b", "qwen3.5:9b"}, res.Models)
		assert.Nil(t, res.TokenSource)
		assert.Equal(t, []string{}, res.Warnings)
	})
	t.Run("the default endpoint is runner.model's, and the default model its first alias", func(t *testing.T) {
		srv, _ := modelsServer(t, "gemma3:12b", "qwen3.5:9b")
		path := writeConfigFile(t, preflightConfig(srv.URL, closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		res, err := sup.Preflight(t.Context(), PreflightRequest{Check: true})
		require.NoError(t, err)
		assert.Equal(t, "gpu", res.Endpoint)
		assert.Equal(t, "gemma3:12b", *res.Model)
	})
	t.Run("a token in secrets.env is used and named", func(t *testing.T) {
		srv, auth := modelsServer(t, "gemma3:12b")
		path := writeConfigFile(t, preflightConfig(srv.URL, closedURL(t)))
		configDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(configDir, config.SecretsFile), []byte("WS_ENDPOINT_TOKEN_GPU=abc\n"), 0o600))
		sup := preflightSupervisor(t, path, configDir)
		res, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu", Model: "gemma"})
		require.NoError(t, err)
		require.NotNil(t, res.TokenSource)
		assert.Equal(t, "secrets.env", *res.TokenSource)
		assert.Equal(t, []string{"Bearer abc"}, *auth)
	})
	t.Run("a model the endpoint doesn't serve is model_not_served", func(t *testing.T) {
		srv, _ := modelsServer(t, "gemma3:12b")
		path := writeConfigFile(t, preflightConfig(srv.URL, closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		_, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu", Model: "nope"})
		assertRemoteCode(t, err, "model_not_served")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Equal(t, "nope", remote.Details["model"])
		assert.Equal(t, []string{"gemma3:12b"}, remote.Details["models"])
	})
	t.Run("an endpoint that doesn't answer is endpoint_unreachable with its url", func(t *testing.T) {
		labURL := closedURL(t)
		path := writeConfigFile(t, preflightConfig(closedURL(t), labURL))
		sup := preflightSupervisor(t, path, t.TempDir())
		_, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "lab"})
		assertRemoteCode(t, err, "endpoint_unreachable")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Equal(t, labURL, remote.Details["url"])
	})
	t.Run("an endpoint that isn't configured is endpoint_unknown with the endpoints there are", func(t *testing.T) {
		path := writeConfigFile(t, preflightConfig(closedURL(t), closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		_, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "nowhere"})
		assertRemoteCode(t, err, "endpoint_unknown")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Equal(t, []string{"gpu", "lab"}, remote.Details["endpoints"])
	})
	t.Run("a config that can't be loaded is config_invalid", func(t *testing.T) {
		sup := NewSupervisor(SupervisorConfig{
			LoadConfig: func() (config.Config, error) { return config.Config{}, errors.New("broken") },
		}, nil, nil, discardLog())
		_, err := sup.Preflight(t.Context(), PreflightRequest{Check: true})
		assertRemoteCode(t, err, "config_invalid")
	})
	t.Run("a role's request resolves in the role's order", func(t *testing.T) {
		srv, _ := modelsServer(t, "gemma3:12b", "qwen3.5:9b")
		path := writeConfigFile(t, preflightConfig(srv.URL, closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		res, err := sup.Preflight(t.Context(), PreflightRequest{Model: "qwen"})
		require.NoError(t, err)
		assert.Equal(t, "qwen3.5:9b", *res.Model)
	})
}

// endpointsOnlyConfig has endpoints and no model alias, as a fresh config does.
func endpointsOnlyConfig(gpuURL, labURL string) string {
	return fmt.Sprintf("[endpoints.gpu]\nurl = %q\nprobe = \"none\"\n\n[endpoints.lab]\nurl = %q\n", gpuURL, labURL)
}

func TestSupervisor_Preflight_endpointOnly(t *testing.T) {
	t.Parallel()
	t.Run("a check of an endpoint with no model lists the models it serves", func(t *testing.T) {
		srv, _ := modelsServer(t, "gemma3:12b", "qwen3.5:9b")
		path := writeConfigFile(t, endpointsOnlyConfig(srv.URL, closedURL(t)))
		sup := preflightSupervisor(t, path, t.TempDir())
		res, err := sup.Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu"})
		require.NoError(t, err)
		assert.Equal(t, "gpu", res.Endpoint)
		assert.Equal(t, srv.URL, res.URL)
		assert.Equal(t, "openai", res.API)
		assert.Equal(t, "none", res.Probe)
		assert.Nil(t, res.Model)
		assert.Nil(t, res.ContextTokens)
		assert.Nil(t, res.Version)
		assert.Nil(t, res.Capabilities)
		assert.Equal(t, []string{"gemma3:12b", "qwen3.5:9b"}, res.Models)
	})
	t.Run("the token is still sent and named", func(t *testing.T) {
		srv, auth := modelsServer(t, "m")
		path := writeConfigFile(t, endpointsOnlyConfig(srv.URL, closedURL(t)))
		configDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(configDir, config.SecretsFile), []byte("WS_ENDPOINT_TOKEN_GPU=abc\n"), 0o600))
		res, err := preflightSupervisor(t, path, configDir).Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu"})
		require.NoError(t, err)
		require.NotNil(t, res.TokenSource)
		assert.Equal(t, "secrets.env", *res.TokenSource)
		assert.Equal(t, []string{"Bearer abc"}, *auth)
	})
	t.Run("an unreachable endpoint is endpoint_unreachable", func(t *testing.T) {
		path := writeConfigFile(t, endpointsOnlyConfig(closedURL(t), closedURL(t)))
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "lab"})
		assertRemoteCode(t, err, "endpoint_unreachable")
	})
	t.Run("an unknown endpoint is endpoint_unknown", func(t *testing.T) {
		path := writeConfigFile(t, endpointsOnlyConfig(closedURL(t), closedURL(t)))
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "nowhere"})
		assertRemoteCode(t, err, "endpoint_unknown")
	})
	t.Run("with no endpoint named, the only endpoint is checked", func(t *testing.T) {
		srv, _ := modelsServer(t, "m")
		path := writeConfigFile(t, fmt.Sprintf("[endpoints.gpu]\nurl = %q\n", srv.URL))
		res, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true})
		require.NoError(t, err)
		assert.Equal(t, "gpu", res.Endpoint)
		assert.Equal(t, []string{"m"}, res.Models)
	})
	t.Run("with no endpoint named and several configured, endpoint_unknown lists them", func(t *testing.T) {
		path := writeConfigFile(t, endpointsOnlyConfig(closedURL(t), closedURL(t)))
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true})
		assertRemoteCode(t, err, "endpoint_unknown")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Equal(t, []string{"gpu", "lab"}, remote.Details["endpoints"])
	})
	t.Run("with no endpoint and none configured, endpoint_unknown", func(t *testing.T) {
		path := writeConfigFile(t, "")
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true})
		assertRemoteCode(t, err, "endpoint_unknown")
	})
	t.Run("a spawn's preflight with no model still fails with config_invalid", func(t *testing.T) {
		srv, _ := modelsServer(t, "m")
		path := writeConfigFile(t, endpointsOnlyConfig(srv.URL, closedURL(t)))
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Endpoint: "gpu"})
		assertRemoteCode(t, err, "config_invalid")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Contains(t, remote.Message, config.ErrNoModel.Error())
	})
	t.Run("a named model with no alias to run on is still config_invalid", func(t *testing.T) {
		srv, _ := modelsServer(t, "m")
		path := writeConfigFile(t, endpointsOnlyConfig(srv.URL, closedURL(t)))
		_, err := preflightSupervisor(t, path, t.TempDir()).Preflight(t.Context(), PreflightRequest{Check: true, Endpoint: "gpu", Model: "m"})
		assertRemoteCode(t, err, "config_invalid")
	})
}

func TestResolveModel(t *testing.T) {
	t.Parallel()
	cfg, err := config.ReadFile(writeConfigFile(t, preflightConfig("http://gpu.invalid", "http://lab.invalid")))
	require.NoError(t, err)
	t.Run("a check without an endpoint takes runner.model's", func(t *testing.T) {
		resolved, err := resolveModel(&cfg, PreflightRequest{Check: true})
		require.NoError(t, err)
		assert.Equal(t, "gpu", resolved.EndpointName)
		assert.Equal(t, "gemma", resolved.ModelAlias)
	})
	t.Run("a check with an endpoint takes the first alias on it", func(t *testing.T) {
		resolved, err := resolveModel(&cfg, PreflightRequest{Check: true, Endpoint: "lab"})
		require.NoError(t, err)
		assert.Equal(t, "other", resolved.ModelAlias)
	})
	t.Run("a check with a model keeps the model and takes its endpoint", func(t *testing.T) {
		resolved, err := resolveModel(&cfg, PreflightRequest{Check: true, Model: "other"})
		require.NoError(t, err)
		assert.Equal(t, "lab", resolved.EndpointName)
	})
	t.Run("a role's request resolves through Resolve", func(t *testing.T) {
		resolved, err := resolveModel(&cfg, PreflightRequest{Model: "qwen"})
		require.NoError(t, err)
		assert.Equal(t, "qwen3.5:9b", resolved.ModelID)
	})
}

func TestAliasOn(t *testing.T) {
	t.Parallel()
	cfg, err := config.ReadFile(writeConfigFile(t, preflightConfig("http://gpu.invalid", "http://lab.invalid")))
	require.NoError(t, err)
	t.Run("the first alias by name on the endpoint", func(t *testing.T) {
		assert.Equal(t, "gemma", aliasOn(&cfg, "gpu"))
	})
	t.Run("runner.model when no alias is on the endpoint", func(t *testing.T) {
		assert.Equal(t, "gemma", aliasOn(&cfg, "nowhere"))
	})
}

func TestConfigFailure(t *testing.T) {
	t.Parallel()
	t.Run("an unknown endpoint lists the configured ones", func(t *testing.T) {
		cfg, err := config.ReadFile(writeConfigFile(t, preflightConfig("http://gpu.invalid", "http://lab.invalid")))
		require.NoError(t, err)
		err = configFailure(&cfg, fmt.Errorf("endpoint x: %w", config.ErrEndpointUnknown))
		assertRemoteCode(t, err, "endpoint_unknown")
	})
	t.Run("any other configuration error is config_invalid", func(t *testing.T) {
		assertRemoteCode(t, configFailure(&config.Config{}, config.ErrNoModel), "config_invalid")
	})
}

func TestTokenFor(t *testing.T) {
	t.Parallel()
	t.Run("secrets.env wins over the daemon's environment", func(t *testing.T) {
		token, source := tokenFor("gpu", map[string]string{"WS_ENDPOINT_TOKEN_GPU": "file"}, []string{"WS_ENDPOINT_TOKEN_GPU=env"})
		assert.Equal(t, "file", token)
		assert.Equal(t, "secrets.env", *source)
	})
	t.Run("the daemon's environment is next", func(t *testing.T) {
		token, source := tokenFor("gpu", nil, []string{"WS_ENDPOINT_TOKEN_GPU=env"})
		assert.Equal(t, "env", token)
		assert.Equal(t, "env", *source)
	})
	t.Run("no token is null", func(t *testing.T) {
		token, source := tokenFor("gpu", nil, nil)
		assert.Empty(t, token)
		assert.Nil(t, source)
	})
}

func TestPreflightFailure(t *testing.T) {
	t.Parallel()
	t.Run("a model without tools is model_without_tools with its capabilities", func(t *testing.T) {
		err := preflightFailure(&llm.NoToolsError{Model: "m", Capabilities: []string{"completion"}}, "http://x")
		assertRemoteCode(t, err, "model_without_tools")
		remote, _ := errors.AsType[*RemoteError](err)
		assert.Equal(t, []string{"completion"}, remote.Details["capabilities"])
	})
	t.Run("another failure is wrapped, not coded", func(t *testing.T) {
		err := preflightFailure(errors.New("probe broke"), "http://x")
		_, ok := errors.AsType[*RemoteError](err)
		assert.False(t, ok)
		assert.ErrorContains(t, err, "probe broke")
	})
}
