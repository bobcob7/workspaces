package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const runnerBase = `
[runner]
model = "qwen9b"

[endpoints.gpu]
url = "http://gpu-a.inference.svc:1234"

[endpoints.cpu]
url = "http://cpu.example.internal:1234"

[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 65536
reasoning = "none"

[models.qwen9b-think]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 131072

[runner.roles.implementer]
runner = "ws"
model = "qwen9b-think"
`

func mustParse(t *testing.T, text string) Config {
	t.Helper()
	cfg, err := parse([]byte(text))
	require.NoError(t, err)
	return cfg
}

func TestConfig_RunnerFor(t *testing.T) {
	t.Parallel()
	t.Run("a configured ws role runs on ws", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		assert.Equal(t, RunnerWS, cfg.RunnerFor("implementer"))
	})
	t.Run("an unconfigured role and the empty role run on claude", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		assert.Equal(t, RunnerClaude, cfg.RunnerFor("reviewer"))
		assert.Equal(t, RunnerClaude, cfg.RunnerFor(""))
	})
	t.Run("a role with no runner key runs on claude", func(t *testing.T) {
		cfg := mustParse(t, "[runner.roles.verifier]\nmax_steps = 30\n")
		assert.Equal(t, RunnerClaude, cfg.RunnerFor("verifier"))
	})
	t.Run("a value that can't be read runs on ws, so it fails", func(t *testing.T) {
		cfg := mustParse(t, "[runner.roles.verifier]\nrunner = 5\n")
		assert.Equal(t, RunnerWS, cfg.RunnerFor("verifier"))
		assert.Error(t, cfg.RunnerError())
	})
}

func TestConfig_Resolve(t *testing.T) {
	t.Parallel()
	t.Run("the role's model gives its endpoint and budgets", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		got, err := cfg.Resolve("implementer", "", "")
		require.NoError(t, err)
		assert.Equal(t, "qwen9b-think", got.ModelAlias)
		assert.Equal(t, "gpu", got.EndpointName)
		assert.Equal(t, "qwen/qwen3.5-9b", got.ModelID)
		assert.Equal(t, 131072, got.ContextTokens)
		assert.Equal(t, Endpoint{
			URL:            "http://gpu-a.inference.svc:1234",
			API:            APIOpenAI,
			Probe:          "auto",
			RequestTimeout: 10 * time.Minute,
			IdleTimeout:    90 * time.Second,
		}, got.Endpoint)
		assert.Equal(t, 80, got.MaxSteps)
		assert.Equal(t, 60*time.Minute, got.Timeout)
		assert.Equal(t, 4_000_000, got.MaxTokens)
		assert.Equal(t, 4096, got.MaxOutput)
		assert.InDelta(t, 0.2, got.Temperature, 1e-9)
		assert.Empty(t, got.Reasoning)
		assert.Equal(t, []string{"*"}, got.MCPTools)
		assert.Equal(t, 8, got.MCPUpfront)
		assert.Empty(t, got.EndpointAlias)
	})
	t.Run("the alias's reasoning is kept", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		got, err := cfg.Resolve("", "", "qwen9b")
		require.NoError(t, err)
		assert.Equal(t, "none", got.Reasoning)
	})
	t.Run("the empty role uses runner.model with the other budget", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		got, err := cfg.Resolve("", "", "")
		require.NoError(t, err)
		assert.Equal(t, "qwen9b", got.ModelAlias)
		assert.Equal(t, 60, got.MaxSteps)
	})
	t.Run("flags win over the role and runner.model", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		got, err := cfg.Resolve("implementer", "cpu", "qwen9b")
		require.NoError(t, err)
		assert.Equal(t, "qwen9b", got.ModelAlias)
		assert.Equal(t, "cpu", got.EndpointName)
	})
	t.Run("an unknown endpoint flag is an error", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		_, err := cfg.Resolve("implementer", "nope", "")
		assert.ErrorIs(t, err, ErrEndpointUnknown)
	})
	t.Run("a raw id runs on the chosen alias's endpoint with a warning", func(t *testing.T) {
		cfg := mustParse(t, runnerBase)
		for _, id := range []string{"qwen/qwen3.5-9b", "qwen3.5:9b"} {
			got, err := cfg.Resolve("x", "", id)
			require.NoError(t, err)
			assert.Empty(t, got.ModelAlias)
			assert.Equal(t, id, got.ModelID)
			assert.Equal(t, "gpu", got.EndpointName)
			assert.Equal(t, 65536, got.ContextTokens)
			assert.Equal(t, "qwen9b", got.EndpointAlias)
			assert.Len(t, got.Warnings, 1)
		}
	})
	t.Run("no model anywhere is ErrNoModel", func(t *testing.T) {
		cfg := mustParse(t, "[endpoints.gpu]\nurl = \"http://gpu\"\n")
		_, err := cfg.Resolve("", "", "")
		require.ErrorIs(t, err, ErrNoModel)
		_, err = cfg.Resolve("x", "", "qwen/qwen3.5-9b")
		require.ErrorIs(t, err, ErrNoModel)
	})
	t.Run("a broken runner table is returned as is", func(t *testing.T) {
		cfg := mustParse(t, runnerBase+"\n[models.bad]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 4096\n")
		_, err := cfg.Resolve("implementer", "", "")
		require.Error(t, err)
		assert.Equal(t, cfg.RunnerError(), err)
	})
}

func TestConfig_Resolve_roleBudgets(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, `
[runner]
model = "qwen9b"
[endpoints.gpu]
url = "http://gpu"
[models.qwen9b]
endpoint = "gpu"
id = "qwen/qwen3.5-9b"
context_tokens = 65536
[runner.roles.verifier]
max_steps = 30
timeout = "30m"
max_tokens = 1500000
mcp_servers = ["metrics"]
mcp_tools = ["mcp__metrics__query_*"]
mcp_deny = ["mcp__metrics__get_panel_image"]
mcp_upfront = 3
`)
	got, err := cfg.Resolve("verifier", "", "")
	require.NoError(t, err)
	assert.Equal(t, 30, got.MaxSteps)
	assert.Equal(t, 30*time.Minute, got.Timeout)
	assert.Equal(t, 1_500_000, got.MaxTokens)
	assert.Equal(t, []string{"metrics"}, got.MCPServers)
	assert.Equal(t, []string{"mcp__metrics__query_*"}, got.MCPTools)
	assert.Equal(t, []string{"mcp__metrics__get_panel_image"}, got.MCPDeny)
	assert.Equal(t, 3, got.MCPUpfront)
}

func TestConfig_RunnerError(t *testing.T) {
	t.Parallel()
	const gpu = "[endpoints.gpu]\nurl = \"http://gpu\"\n"
	cases := []struct {
		name string
		text string
		want string
	}{
		{"context size under the floor", gpu + "[models.qwen9b]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 4096\n", "models.qwen9b.context_tokens"},
		{"ftp url", "[endpoints.gpu]\nurl = \"ftp://x\"\n", "endpoints.gpu.url"},
		{"url with user info", "[endpoints.gpu]\nurl = \"http://u:p@x\"\n", "endpoints.gpu.url"},
		{"reserved api", "[endpoints.gpu]\nurl = \"http://x\"\napi = \"anthropic\"\n", "isn't implemented yet"},
		{"model with no endpoint defined", gpu + "[models.qwen9b]\nendpoint = \"nope\"\nid = \"x\"\ncontext_tokens = 65536\n", "models.qwen9b.endpoint"},
		{"unknown reasoning", gpu + "[models.qwen9b]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 65536\nreasoning = \"max\"\n", "models.qwen9b.reasoning"},
		{"id with a newline", gpu + "[models.qwen9b]\nendpoint = \"gpu\"\nid = \"a\\nb\"\ncontext_tokens = 65536\n", "models.qwen9b.id"},
		{"token key in an endpoint", "[endpoints.gpu]\nurl = \"http://x\"\ntoken = \"x\"\n", "WS_ENDPOINT_TOKEN_"},
		{"secret in runner env", "[runner.env]\nMETRICS_TOKEN = \"x\"\n", "put secrets in secrets.env"},
		{"upper case endpoint name", "[endpoints.GPU]\nurl = \"http://x\"\n", "endpoints.GPU"},
		{"context size of the wrong type", gpu + "[models.qwen9b]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = \"big\"\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := mustParse(t, tc.text)
			err := cfg.RunnerError()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			_, resolveErr := cfg.Resolve("", "", "")
			assert.Equal(t, err, resolveErr)
		})
	}
}

func TestConfig_RunnerError_keepsClaudePath(t *testing.T) {
	t.Parallel()
	base := "[defaults]\nhost = \"git.example.com\"\n[review]\ntimeout = \"20m\"\n"
	clean := mustParse(t, base)
	t.Run("a half-written runner table still loads the rest", func(t *testing.T) {
		got := mustParse(t, base+"[endpoints.gpu\nurl = \"http://x\"\n")
		require.Error(t, got.RunnerError())
		assert.Equal(t, clean.Defaults, got.Defaults)
		assert.Equal(t, clean.Hosts, got.Hosts)
		assert.Equal(t, clean.Review.Timeout, got.Review.Timeout)
		assert.Equal(t, RunnerClaude, got.RunnerFor(""))
	})
	t.Run("an invalid table keeps the other fields and the other roles", func(t *testing.T) {
		got := mustParse(t, base+strings.Replace(runnerBase, "http://gpu-a.inference.svc:1234", "ftp://x", 1))
		require.Error(t, got.RunnerError())
		assert.Equal(t, clean.Defaults, got.Defaults)
		assert.Equal(t, RunnerWS, got.RunnerFor("implementer"))
		assert.Equal(t, RunnerClaude, got.RunnerFor("reviewer"))
	})
}

func TestConfig_RunnerError_idRoundTrips(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, "[endpoints.gpu]\nurl = \"http://x\"\n[models.qwen9b]\nendpoint = \"gpu\"\nid = \"qwen/qwen3.5-9b\"\ncontext_tokens = 65536\n")
	require.NoError(t, cfg.RunnerError())
	assert.Equal(t, "qwen/qwen3.5-9b", cfg.Models["qwen9b"].ID)
}

func TestConfig_Reresolve(t *testing.T) {
	t.Parallel()
	cfg := mustParse(t, runnerBase)
	resolved, err := cfg.Resolve("implementer", "", "")
	require.NoError(t, err)
	t.Run("an unchanged configuration gives the spawn's target", func(t *testing.T) {
		got, err := cfg.Reresolve(resolved.Key(""))
		require.NoError(t, err)
		assert.Equal(t, resolved.Target, got)
	})
	t.Run("a repointed alias follows the new endpoint and keeps the id", func(t *testing.T) {
		moved := mustParse(t, strings.Replace(runnerBase, "[models.qwen9b-think]\nendpoint = \"gpu\"", "[models.qwen9b-think]\nendpoint = \"cpu\"", 1))
		got, err := moved.Reresolve(resolved.Key(""))
		require.NoError(t, err)
		assert.Equal(t, "cpu", got.EndpointName)
		assert.Equal(t, "qwen/qwen3.5-9b", got.ModelID)
	})
	t.Run("a changed endpoint url is picked up", func(t *testing.T) {
		moved := mustParse(t, strings.Replace(runnerBase, "http://gpu-a.inference.svc:1234", "http://gpu-b:1234", 1))
		got, err := moved.Reresolve(resolved.Key(""))
		require.NoError(t, err)
		assert.Equal(t, "http://gpu-b:1234", got.Endpoint.URL)
	})
	t.Run("the endpoint flag wins over a repointed alias", func(t *testing.T) {
		moved := mustParse(t, strings.Replace(runnerBase, "[models.qwen9b-think]\nendpoint = \"gpu\"", "[models.qwen9b-think]\nendpoint = \"cpu\"", 1))
		got, err := moved.Reresolve(resolved.Key("gpu"))
		require.NoError(t, err)
		assert.Equal(t, "gpu", got.EndpointName)
	})
	t.Run("a changed runner.model or role model doesn't move the alias", func(t *testing.T) {
		moved := mustParse(t, strings.Replace(runnerBase, "model = \"qwen9b\"", "model = \"qwen9b-think\"", 1))
		got, err := moved.Reresolve(resolved.Key(""))
		require.NoError(t, err)
		assert.Equal(t, "qwen9b-think", got.ModelAlias)
	})
	t.Run("a removed alias is ErrModelGone", func(t *testing.T) {
		gone := mustParse(t, "[endpoints.gpu]\nurl = \"http://gpu\"\n[models.other]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 65536\n")
		_, err := gone.Reresolve(resolved.Key(""))
		assert.ErrorIs(t, err, ErrModelGone)
	})
	t.Run("a removed flag endpoint is ErrEndpointUnknown", func(t *testing.T) {
		gone := mustParse(t, "[endpoints.gpu]\nurl = \"http://gpu\"\n[models.qwen9b-think]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 65536\n")
		_, err := gone.Reresolve(RunKey{ModelAlias: "qwen9b-think", EndpointFlag: "cpu"})
		assert.ErrorIs(t, err, ErrEndpointUnknown)
	})
	t.Run("a raw id follows its alias's endpoint and keeps the id", func(t *testing.T) {
		raw, err := cfg.Resolve("x", "", "qwen3.5:9b")
		require.NoError(t, err)
		moved := mustParse(t, strings.Replace(runnerBase, "[models.qwen9b]\nendpoint = \"gpu\"", "[models.qwen9b]\nendpoint = \"cpu\"", 1))
		got, err := moved.Reresolve(raw.Key(""))
		require.NoError(t, err)
		assert.Equal(t, "cpu", got.EndpointName)
		assert.Equal(t, "qwen3.5:9b", got.ModelID)
	})
}

func TestResolved_Key(t *testing.T) {
	t.Parallel()
	t.Run("an alias key has no raw id", func(t *testing.T) {
		var resolved Resolved
		resolved.ModelAlias = "qwen9b"
		resolved.ModelID = "qwen/qwen3.5-9b"
		assert.Equal(t, RunKey{ModelAlias: "qwen9b", EndpointFlag: "cpu"}, resolved.Key("cpu"))
	})
	t.Run("a raw id key keeps the id and its alias", func(t *testing.T) {
		var resolved Resolved
		resolved.ModelID = "qwen3.5:9b"
		resolved.EndpointAlias = "qwen9b"
		assert.Equal(t, RunKey{ModelID: "qwen3.5:9b", EndpointAlias: "qwen9b"}, resolved.Key(""))
	})
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	t.Run("a missing file is an error and isn't created", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		_, err := ReadFile(path)
		require.Error(t, err)
		_, statErr := os.Stat(path)
		assert.ErrorIs(t, statErr, os.ErrNotExist)
	})
	t.Run("invalid TOML is an error naming the file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(path, []byte("[defaults\n"), 0o600))
		_, err := ReadFile(path)
		require.ErrorIs(t, err, ErrInvalid)
		assert.Contains(t, err.Error(), path)
	})
	t.Run("a valid file is read", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.toml")
		require.NoError(t, os.WriteFile(path, []byte(runnerBase), 0o600))
		cfg, err := ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "qwen9b", cfg.Runner.Model)
	})
}

func TestRunner_KeepsGitTokens(t *testing.T) {
	t.Parallel()
	off := false
	assert.True(t, (&Runner{}).KeepsGitTokens())
	assert.False(t, (&Runner{KeepGitTokens: &off}).KeepsGitTokens())
}

func TestDaemon_ResumesOnStart(t *testing.T) {
	t.Parallel()
	off := false
	assert.True(t, (&Daemon{}).ResumesOnStart())
	assert.False(t, (&Daemon{ResumeOnStart: &off}).ResumesOnStart())
}

func TestDefaultBudget(t *testing.T) {
	t.Parallel()
	steps, timeout, tokens := DefaultBudget("reviewer")
	assert.Equal(t, 40, steps)
	assert.Equal(t, 30*time.Minute, timeout)
	assert.Equal(t, 2_000_000, tokens)
	steps, timeout, tokens = DefaultBudget("other")
	assert.Equal(t, 60, steps)
	assert.Equal(t, 60*time.Minute, timeout)
	assert.Equal(t, 4_000_000, tokens)
}

func TestTokenEnv(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "WS_ENDPOINT_TOKEN_GPU_A", TokenEnv("gpu-a"))
}

func TestRunnerEdit_Set(t *testing.T) {
	t.Parallel()
	t.Run("a model's context size is written and comments are kept", func(t *testing.T) {
		data := []byte(runnerBase + "# keep me\n")
		out, changed, err := Set(data, "models.qwen9b.context_tokens", []string{"98304"})
		require.NoError(t, err)
		assert.True(t, changed)
		setting, err := Lookup(out, "models.qwen9b.context_tokens")
		require.NoError(t, err)
		assert.Equal(t, 98304, setting.Value)
		assert.Contains(t, string(out), "# keep me")
	})
	t.Run("an id with slashes and colons is kept exactly", func(t *testing.T) {
		out, _, err := Set([]byte(runnerBase), "models.qwen9b.id", []string{"qwen/qwen3.5-9b"})
		require.NoError(t, err)
		setting, err := Lookup(out, "models.qwen9b.id")
		require.NoError(t, err)
		assert.Equal(t, "qwen/qwen3.5-9b", setting.Value)
	})
	t.Run("a context size under the floor is ErrValueInvalid", func(t *testing.T) {
		_, _, err := Set([]byte(runnerBase), "models.qwen9b.context_tokens", []string{"4096"})
		assert.ErrorIs(t, err, ErrValueInvalid)
	})
	t.Run("a write that breaks the runner's tables is refused", func(t *testing.T) {
		_, _, err := Set([]byte(runnerBase), "models.qwen9b.endpoint", []string{"nope"})
		assert.ErrorIs(t, err, ErrValueInvalid)
	})
	t.Run("a role's list takes several values", func(t *testing.T) {
		out, _, err := Set([]byte(runnerBase), "runner.roles.implementer.mcp_servers", []string{"metrics", "docs"})
		require.NoError(t, err)
		setting, err := Lookup(out, "runner.roles.implementer.mcp_servers")
		require.NoError(t, err)
		assert.Equal(t, []string{"metrics", "docs"}, setting.Value)
	})
	t.Run("a temperature is stored as a string", func(t *testing.T) {
		out, _, err := Set([]byte(runnerBase), "models.qwen9b.temperature", []string{"0.7"})
		require.NoError(t, err)
		assert.Contains(t, string(out), `temperature = "0.7"`)
	})
	t.Run("a key under a new table adds the table", func(t *testing.T) {
		out, _, err := Set(nil, "endpoints.gpu.url", []string{"http://gpu:1234"})
		require.NoError(t, err)
		assert.Contains(t, string(out), "[endpoints.gpu]")
		assert.Contains(t, string(out), `url = "http://gpu:1234"`)
	})
	t.Run("a runner env name with a secret suffix is refused", func(t *testing.T) {
		_, _, err := Set(nil, "runner.env.METRICS_TOKEN", []string{"x"})
		assert.ErrorIs(t, err, ErrValueInvalid)
	})
	t.Run("the fallback key isn't one ws config knows", func(t *testing.T) {
		_, _, err := Set(nil, "runner.fallback", []string{"claude"})
		assert.ErrorIs(t, err, ErrKeyUnknown)
	})
}

func TestRunnerEdit_Unset(t *testing.T) {
	t.Parallel()
	t.Run("a model table that nothing uses is removed whole", func(t *testing.T) {
		data := []byte(runnerBase + "\n[models.spare]\nendpoint = \"gpu\"\nid = \"x\"\ncontext_tokens = 65536\n")
		out, changed, err := Unset(data, "models.spare")
		require.NoError(t, err)
		assert.True(t, changed)
		assert.NotContains(t, string(out), "models.spare")
		setting, err := Lookup(out, "models.spare.id")
		require.NoError(t, err)
		assert.False(t, setting.Set)
	})
	t.Run("a model the runner uses is refused", func(t *testing.T) {
		_, _, err := Unset([]byte(runnerBase), "models.qwen9b")
		assert.ErrorIs(t, err, ErrValueInvalid)
	})
	t.Run("an endpoint table nothing uses is removed", func(t *testing.T) {
		out, changed, err := Unset([]byte(runnerBase), "endpoints.cpu")
		require.NoError(t, err)
		assert.True(t, changed)
		assert.NotContains(t, string(out), "endpoints.cpu")
	})
	t.Run("an absent table leaves the file as it is", func(t *testing.T) {
		out, changed, err := Unset([]byte(runnerBase), "endpoints.nope")
		require.NoError(t, err)
		assert.False(t, changed)
		assert.Equal(t, runnerBase, string(out))
	})
}

func TestRunnerEdit_Settings(t *testing.T) {
	t.Parallel()
	settings, err := Settings([]byte(runnerBase))
	require.NoError(t, err)
	keys := make([]string, 0, len(settings))
	for _, s := range settings {
		keys = append(keys, s.Key)
	}
	assert.Equal(t, "runner.model", keys[5])
	assert.Equal(t, "endpoints.cpu.url", keys[9])
	assert.Equal(t, "endpoints.gpu.url", keys[14])
	assert.Equal(t, "models.qwen9b.endpoint", keys[19])
	assert.Equal(t, "runner.roles.implementer.runner", keys[len(keys)-9])
}
