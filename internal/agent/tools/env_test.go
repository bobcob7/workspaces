package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnviron(t *testing.T) {
	environ := []string{
		"HOME=/h",
		"PATH=/bin",
		"METRICS_URL=http://m",
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://o",
		"WS_ENDPOINT_TOKEN_GPU=t1",
		"OPENAI_API_KEY=k",
		"FOO_SECRET=s",
		"NPM_TOKEN=n",
		"DB_PASSWORD=p",
		"METRICS_TOKEN=m",
		"OTEL_EXPORTER_OTLP_HEADERS=h",
		"OTEL_EXPORTER_OTLP_TRACES_HEADERS=th",
		"MY_VAR=v",
		"GH_TOKEN=gh",
		"WS_RUNNER=claude",
	}
	t.Run("drops secrets and keeps the rest with WS_RUNNER set", func(t *testing.T) {
		got := Environ(environ, false, []string{"MY_VAR"})
		assert.Equal(t, []string{"HOME=/h", "PATH=/bin", "METRICS_URL=http://m", "OTEL_EXPORTER_OTLP_ENDPOINT=http://o", "WS_RUNNER=ws"}, got)
	})
	t.Run("keeps the git tokens with keepGit", func(t *testing.T) {
		got := Environ(environ, true, nil)
		assert.Contains(t, got, "GH_TOKEN=gh")
		assert.NotContains(t, got, "NPM_TOKEN=n")
	})
}

func TestSecrets(t *testing.T) {
	environ := []string{"PATH=/bin", "NPM_TOKEN=npm-value", "GH_TOKEN=gh-value", "MY_VAR=v"}
	assert.Equal(t, []string{"npm-value", "gh-value", "v"}, Secrets(environ, false, []string{"MY_VAR"}))
	assert.Equal(t, []string{"npm-value", "v"}, Secrets(environ, true, []string{"MY_VAR"}))
}
