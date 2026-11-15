package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
)

const reresolveConfig = `[runner]
model = "qwen9b"

[endpoints.gpu]
url = "http://gpu-a.example:1234"

[endpoints.fake-b]
url = "http://gpu-b.example:1234"
probe = "none"

[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 65536
`

// onFakeB is reresolveConfig with qwen9b on fake-b.
func onFakeB() string {
	return strings.Replace(reresolveConfig, `endpoint = "gpu"`, `endpoint = "fake-b"`, 1)
}

// fakeProvider stands in for a provider. The tests never call it.
type fakeProvider struct {
	llm.Provider
	name string
}

// resolverSpy is a Reresolver's dependencies, recording their calls.
type resolverSpy struct {
	dir          string
	env          map[string]string
	getenvs      int
	builds       []llm.Endpoint
	preflights   []llm.Endpoint
	redacted     []string
	logs         *bytes.Buffer
	preflightRes llm.PreflightResult
	preflightErr error
}

// newSpyResolver is a Reresolver over a config dir that holds reresolveConfig,
// with its current target read from that config.
func newSpyResolver(t *testing.T) (*Reresolver, *resolverSpy) {
	t.Helper()
	spy := &resolverSpy{
		dir:          t.TempDir(),
		env:          map[string]string{},
		logs:         &bytes.Buffer{},
		preflightRes: llm.PreflightResult{ContextTokens: 65536},
	}
	r := &Reresolver{
		ConfigDir: spy.dir,
		Key:       config.RunKey{ModelAlias: "qwen9b"},
		Getenv: func(name string) string {
			spy.getenvs++
			return spy.env[name]
		},
		Build: func(ep llm.Endpoint) (llm.Provider, error) {
			spy.builds = append(spy.builds, ep)
			return &fakeProvider{name: ep.Name}, nil
		},
		Preflight: func(_ context.Context, _ llm.Provider, ep llm.Endpoint, _ string, _ int) (llm.PreflightResult, error) {
			spy.preflights = append(spy.preflights, ep)
			return spy.preflightRes, spy.preflightErr
		},
		Redact: func(secret string) { spy.redacted = append(spy.redacted, secret) },
		Log:    slog.New(slog.NewTextHandler(spy.logs, nil)),
	}
	spy.writeConfig(t, reresolveConfig)
	r.Current = targetOf(t, spy.dir, r.Key)
	return r, spy
}

func (s *resolverSpy) writeConfig(t *testing.T, text string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(s.dir, "config.toml"), []byte(text), fileMode))
}

func (s *resolverSpy) writeSecrets(t *testing.T, text string, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(s.dir, config.SecretsFile)
	require.NoError(t, os.WriteFile(path, []byte(text), mode))
	require.NoError(t, os.Chmod(path, mode))
}

// targetOf is the target key resolves to in the config dir's config.toml.
func targetOf(t *testing.T, dir string, key config.RunKey) config.Target {
	t.Helper()
	cfg, err := config.ReadFile(config.NewPaths(dir).ConfigFile)
	require.NoError(t, err)
	target, err := cfg.Reresolve(key)
	require.NoError(t, err)
	return target
}

func TestReresolver_Reresolve(t *testing.T) {
	t.Parallel()
	t.Run("an unchanged config is an empty resolution, and nothing is built or checked", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		assert.Equal(t, agent.Resolution{}, r.Reresolve(t.Context(), agent.Target{}))
		assert.Empty(t, spy.builds)
		assert.Empty(t, spy.preflights)
		assert.Zero(t, spy.getenvs)
	})
	t.Run("a changed url is checked with the endpoint's token before the switch", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, "gpu-a.example", "gpu-c.example", 1))
		spy.env["WS_ENDPOINT_TOKEN_GPU"] = "gpu-token"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.True(t, res.Changed)
		assert.Equal(t, []string{"url"}, res.Changes)
		require.Len(t, spy.builds, 1)
		assert.Equal(t, "http://gpu-c.example:1234", spy.builds[0].URL)
		assert.Equal(t, "gpu-token", spy.builds[0].Token)
		assert.Equal(t, "gpu", res.Target.Endpoint)
		assert.Equal(t, "qwen/qwen3.5-9b", res.Target.Model)
		assert.Equal(t, 65536, res.Target.ContextTokens)
		assert.Nil(t, res.Probe)
	})
	t.Run("a changed endpoint takes its token from secrets.env before the environment", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.writeSecrets(t, "WS_ENDPOINT_TOKEN_FAKE_B=b-token\n", fileMode)
		spy.env["WS_ENDPOINT_TOKEN_FAKE_B"] = "env-token"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.True(t, res.Changed)
		assert.Equal(t, []string{"endpoint", "url"}, res.Changes)
		require.Len(t, spy.builds, 1)
		assert.Equal(t, "b-token", spy.builds[0].Token)
		assert.Equal(t, []string{"b-token"}, spy.redacted)
		assert.NotContains(t, spy.logs.String(), "b-token")
	})
	t.Run("without a secrets.env line the token comes from the environment", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.env["WS_ENDPOINT_TOKEN_FAKE_B"] = "env-token"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.True(t, res.Changed)
		require.Len(t, spy.builds, 1)
		assert.Equal(t, "env-token", spy.builds[0].Token)
	})
	t.Run("a loose secrets.env is ignored, with a logged warning", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.writeSecrets(t, "WS_ENDPOINT_TOKEN_FAKE_B=b-token\n", 0o644)
		spy.env["WS_ENDPOINT_TOKEN_FAKE_B"] = "env-token"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.True(t, res.Changed)
		require.Len(t, spy.builds, 1)
		assert.Equal(t, "env-token", spy.builds[0].Token)
		assert.Contains(t, spy.logs.String(), "secrets.env not used")
	})
	t.Run("a failed check is Failed when the endpoint is unreachable", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.preflightErr = &llm.UnreachableError{URL: "http://gpu-b.example:1234", Cause: errors.New("refused")}
		res := r.Reresolve(t.Context(), agent.Target{})
		require.Error(t, res.Failed)
		require.ErrorIs(t, res.Failed, llm.ErrUnreachable)
		assert.Nil(t, res.Rejected)
		assert.False(t, res.Changed)
	})
	t.Run("a model the endpoint doesn't serve is rejected, naming the endpoint and id", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.preflightErr = &llm.ModelMissingError{Model: "qwen/qwen3.5-9b"}
		res := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, res.Rejected)
		assert.Equal(t, "model_not_served", res.Rejected.Code)
		assert.Equal(t, "fake-b", res.Rejected.Endpoint)
		assert.Equal(t, "qwen/qwen3.5-9b", res.Rejected.Model)
		assert.False(t, res.Changed)
	})
	t.Run("a model without tools is rejected", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		spy.preflightErr = &llm.NoToolsError{Model: "qwen/qwen3.5-9b", Capabilities: []string{"completion"}}
		res := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, res.Rejected)
		assert.Equal(t, "model_without_tools", res.Rejected.Code)
	})
	t.Run("a config that doesn't parse is a config_invalid warning, logged once", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, "[runner")
		first := r.Reresolve(t.Context(), agent.Target{})
		second := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, first.Warning)
		assert.Equal(t, "config_invalid", first.Warning.Code)
		require.NotNil(t, second.Warning)
		assert.Equal(t, 1, strings.Count(spy.logs.String(), "configuration not used"))
	})
	t.Run("a runner.model naming an undefined alias is a config_invalid warning, since the file no longer validates", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, `model = "qwen9b"`, `model = "gone"`, 1))
		assert.Empty(t, spy.builds)
		res := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, res.Warning)
		assert.Equal(t, "config_invalid", res.Warning.Code)
	})
	t.Run("runner.model changed to another alias is unchanged, since the alias is fixed", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, `model = "qwen9b"`, `model = "other"`, 1)+"\n[models.other]\nendpoint = \"fake-b\"\nid = \"qwen/other\"\ncontext_tokens = 65536\n")
		assert.Equal(t, agent.Resolution{}, r.Reresolve(t.Context(), agent.Target{}))
		assert.Empty(t, spy.builds)
		assert.Empty(t, spy.preflights)
		assert.Empty(t, spy.redacted)
		assert.Zero(t, spy.getenvs)
	})
	t.Run("a model alias that is gone is a model_gone warning, with no URL in it", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, "[runner]\nmodel = \"other\"\n\n[endpoints.gpu]\nurl = \"http://gpu-a.example:1234\"\n\n[models.other]\nendpoint = \"gpu\"\nid = \"qwen/qwen3.5-9b\"\ncontext_tokens = 65536\n")
		res := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, res.Warning)
		assert.Equal(t, "model_gone", res.Warning.Code)
		assert.NotContains(t, res.Warning.Message, "gpu-a.example")
	})
	t.Run("an endpoint flag that is gone is an endpoint_unknown warning", func(t *testing.T) {
		t.Parallel()
		r, _ := newSpyResolver(t)
		r.Key.EndpointFlag = "nowhere"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.NotNil(t, res.Warning)
		assert.Equal(t, "endpoint_unknown", res.Warning.Code)
	})
	t.Run("a timeout change rebuilds the provider with the same token, without a check", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, `url = "http://gpu-a.example:1234"`, "url = \"http://gpu-a.example:1234\"\nrequest_timeout = \"5m\"", 1))
		spy.env["WS_ENDPOINT_TOKEN_GPU"] = "gpu-token"
		res := r.Reresolve(t.Context(), agent.Target{})
		require.True(t, res.Changed)
		assert.Equal(t, []string{"request_timeout"}, res.Changes)
		require.Len(t, spy.builds, 1)
		assert.Equal(t, "gpu-token", spy.builds[0].Token)
		assert.NotNil(t, res.Target.Chat)
		assert.Empty(t, spy.preflights)
	})
	t.Run("a temperature change applies without a check and keeps the provider", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, "context_tokens = 65536", "context_tokens = 65536\ntemperature = \"0.5\"", 1))
		current := agent.Target{Chat: &fakeProvider{name: "gpu"}}
		res := r.Reresolve(t.Context(), current)
		require.True(t, res.Changed)
		assert.Equal(t, []string{"temperature"}, res.Changes)
		assert.InDelta(t, 0.5, res.Target.Temperature, 1e-9)
		assert.Equal(t, current.Chat, res.Target.Chat)
		assert.Empty(t, spy.builds)
		assert.Empty(t, spy.preflights)
	})
	t.Run("Adopt makes the switched-to target the current one", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, onFakeB())
		require.True(t, r.Reresolve(t.Context(), agent.Target{}).Changed)
		adopted := r.Adopt()
		assert.Equal(t, "fake-b", adopted.EndpointName)
		assert.Equal(t, adopted, r.Current)
		assert.Empty(t, Changes(r.Current, targetOf(t, spy.dir, r.Key)))
	})
	t.Run("a settings change keeps the window a probe lowered", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(onFakeB(), "context_tokens = 65536", "context_tokens = 32768", 1))
		spy.preflightRes = llm.PreflightResult{ContextTokens: 8192}
		require.True(t, r.Reresolve(t.Context(), agent.Target{}).Changed)
		assert.Equal(t, 8192, r.Adopt().ContextTokens)
		spy.writeConfig(t, strings.Replace(onFakeB(), "context_tokens = 65536", "context_tokens = 32768\ntemperature = \"0.5\"", 1))
		res := r.Reresolve(t.Context(), agent.Target{Chat: &fakeProvider{name: "fake-b"}})
		require.True(t, res.Changed)
		assert.Equal(t, []string{"temperature"}, res.Changes)
		assert.Equal(t, 8192, res.Target.ContextTokens)
		assert.Len(t, spy.preflights, 1)
	})
	t.Run("the first retry after a spawn keeps a window stored probe-lowered", func(t *testing.T) {
		t.Parallel()
		r, spy := newSpyResolver(t)
		spy.writeConfig(t, strings.Replace(reresolveConfig, "context_tokens = 65536", "context_tokens = 32768", 1))
		r.Current.ContextTokens = 8192
		assert.Equal(t, agent.Resolution{}, r.Reresolve(t.Context(), agent.Target{}))
		assert.Empty(t, spy.builds)
		assert.Empty(t, spy.preflights)
	})
}

func TestChanges(t *testing.T) {
	t.Parallel()
	base := config.Target{
		ModelID:       "qwen/qwen3.5-9b",
		EndpointName:  "gpu",
		Endpoint:      config.Endpoint{URL: "http://gpu", API: "openai"},
		ContextTokens: 65536,
	}
	t.Run("identical targets have no changes", func(t *testing.T) {
		assert.Empty(t, Changes(base, base))
	})
	t.Run("the changed keys come in reference.md's order", func(t *testing.T) {
		to := base
		to.Endpoint.IdleTimeout = 1
		to.Temperature = 0.5
		to.Endpoint.URL = "http://other"
		assert.Equal(t, []string{"url", "temperature", "idle_timeout"}, Changes(base, to))
	})
}

// e2eConfig is a config.toml with gpu at gpuURL and fake-b at bURL, probed as
// probe, and qwen9b on endpoint.
func e2eConfig(gpuURL, bURL, probe, endpoint string) string {
	return fmt.Sprintf(`[runner]
model = "qwen9b"

[endpoints.gpu]
url = %q

[endpoints.fake-b]
url = %q
probe = %q

[models.qwen9b]
endpoint = %q
id = "qwen/qwen3.5-9b"
context_tokens = 65536
`, gpuURL, bURL, probe, endpoint)
}

// newProbingServer is an endpoint whose ollama probe reports capabilities,
// and which counts the chat requests it gets.
func newProbingServer(t *testing.T, capabilities string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var chats atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"qwen/qwen3.5-9b"}]}`)
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"version":"0.12.0"}`)
	})
	mux.HandleFunc("POST /api/show", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"capabilities":[%s],"model_info":{}}`, capabilities)
	})
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		chats.Add(1)
		http.Error(w, "unexpected chat", http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, &chats
}

func TestExecuteReresolve(t *testing.T) {
	t.Parallel()
	t.Run("switches to the endpoint config.toml names after a failed retry", func(t *testing.T) {
		t.Parallel()
		gpu := newModelServer(t, func(_ int, w http.ResponseWriter) { http.Error(w, "down", http.StatusServiceUnavailable) })
		fakeB := newModelServer(t, script(recordPass))
		files, workspace := newRunFixture(t, gpu.URL, nil)
		in := testInput(t, files, workspace, false)
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte(e2eConfig(gpu.URL, fakeB.URL, "none", "fake-b")), fileMode))
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, config.SecretsFile), []byte("WS_ENDPOINT_TOKEN_FAKE_B=b-token\n"), fileMode))
		require.NoError(t, Execute(t.Context(), in))

		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitDone, *st.Exit)
		require.NotNil(t, st.Verdict)
		assert.Equal(t, "PASS", *st.Verdict)
		assert.Equal(t, "fake-b", st.Endpoint)
		assert.Equal(t, 1, st.EndpointChanges)

		run, err := files.ReadRun()
		require.NoError(t, err)
		require.Len(t, run.EndpointHistory, 2)
		assert.Equal(t, "retry", run.EndpointHistory[1].Reason)
		assert.Equal(t, "fake-b", run.EndpointHistory[1].Endpoint)

		data, err := os.ReadFile(files.path(usageFile))
		require.NoError(t, err)
		var usage Usage
		require.NoError(t, json.Unmarshal(data, &usage))
		assert.Len(t, usage.Endpoints, 2)

		transcript := readTranscript(t, files)
		assert.Equal(t, 1, strings.Count(transcript, `"kind":"endpoint_changed"`))
		assert.NotContains(t, transcript, "b-token")
		require.NotEmpty(t, fakeB.auth)
		assert.Equal(t, "Bearer b-token", fakeB.auth[0])
		for _, auth := range gpu.auth {
			assert.Equal(t, "Bearer "+testToken, auth)
		}
	})
	t.Run("a config.toml that breaks mid-run ends endpoint_down with config warnings", func(t *testing.T) {
		t.Parallel()
		var in *Input
		gpu := newModelServer(t, func(_ int, w http.ResponseWriter) {
			_ = os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte("[runner"), fileMode)
			http.Error(w, "down", http.StatusServiceUnavailable)
		})
		files, workspace := newRunFixture(t, gpu.URL, nil)
		in = testInput(t, files, workspace, false)
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte(e2eConfig(gpu.URL, gpu.URL, "none", "gpu")), fileMode))
		require.NoError(t, Execute(t.Context(), in))

		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitEndpointDown, *st.Exit)
		assert.Contains(t, readTranscript(t, files), `"kind":"config_warning"`)
	})
	t.Run("a config.toml removed mid-run ends endpoint_down with config warnings", func(t *testing.T) {
		t.Parallel()
		var in *Input
		gpu := newModelServer(t, func(_ int, w http.ResponseWriter) {
			_ = os.Remove(filepath.Join(in.ConfigDir, "config.toml"))
			http.Error(w, "down", http.StatusServiceUnavailable)
		})
		files, workspace := newRunFixture(t, gpu.URL, nil)
		in = testInput(t, files, workspace, false)
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte(e2eConfig(gpu.URL, gpu.URL, "none", "gpu")), fileMode))
		require.NoError(t, Execute(t.Context(), in))

		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitEndpointDown, *st.Exit)
		assert.Contains(t, readTranscript(t, files), `"kind":"config_warning"`)
	})
	t.Run("a switch to a model without tools is refused, and the run ends endpoint_rejected", func(t *testing.T) {
		t.Parallel()
		gpu := newModelServer(t, func(_ int, w http.ResponseWriter) { http.Error(w, "down", http.StatusServiceUnavailable) })
		probing, chats := newProbingServer(t, `"completion"`)
		files, workspace := newRunFixture(t, gpu.URL, nil)
		in := testInput(t, files, workspace, false)
		require.NoError(t, os.WriteFile(filepath.Join(in.ConfigDir, "config.toml"), []byte(e2eConfig(gpu.URL, probing.URL, "ollama", "fake-b")), fileMode))
		require.NoError(t, Execute(t.Context(), in))

		st, err := files.ReadStatus()
		require.NoError(t, err)
		require.NotNil(t, st.Exit)
		assert.Equal(t, agent.ExitEndpointRejected, *st.Exit)
		assert.Zero(t, chats.Load())
		assert.Contains(t, readTranscript(t, files), `"kind":"endpoint_rejected"`)
	})
}
