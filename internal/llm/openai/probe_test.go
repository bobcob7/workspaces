package openai

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

const showBody = `{"capabilities":["completion","tools","thinking"],"model_info":{"general.architecture":"qwen3","qwen3.context_length":40960}}`

func ollamaServer(versionHits *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			versionHits.Add(1)
			_, _ = w.Write([]byte(`{"version":"0.40.16"}`))
		case "/api/show":
			_, _ = w.Write([]byte(showBody))
		default:
			http.NotFound(w, r)
		}
	}
}

func TestProvider_Probe(t *testing.T) {
	t.Parallel()
	t.Run("ollama reads capabilities and context length", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		p := newTestProvider(t, ollamaServer(&hits), llm.Endpoint{Probe: "ollama"})
		info, err := p.Probe(t.Context(), "qwen3:8b")
		require.NoError(t, err)
		assert.Equal(t, llm.ModelInfo{
			Version:       "0.40.16",
			Capabilities:  []string{"completion", "tools", "thinking"},
			ContextLength: 40960,
		}, info)
	})
	t.Run("auto is ollama when /api/version answers, and asks only once", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		p := newTestProvider(t, ollamaServer(&hits), llm.Endpoint{Probe: "auto"})
		for range 2 {
			info, err := p.Probe(t.Context(), "qwen3:8b")
			require.NoError(t, err)
			assert.Equal(t, 40960, info.ContextLength)
		}
		assert.Equal(t, int32(1), hits.Load())
	})
	t.Run("auto without /api/version is unsupported, and asks only once", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			http.NotFound(w, r)
		}, llm.Endpoint{Probe: "auto"})
		for range 2 {
			_, err := p.Probe(t.Context(), "qwen3:8b")
			require.ErrorIs(t, err, llm.ErrProbeUnsupported)
		}
		assert.Equal(t, int32(1), hits.Load())
	})
	t.Run("none is unsupported and asks nothing", func(t *testing.T) {
		t.Parallel()
		var hits atomic.Int32
		p := newTestProvider(t, ollamaServer(&hits), llm.Endpoint{Probe: "none"})
		_, err := p.Probe(t.Context(), "qwen3:8b")
		require.ErrorIs(t, err, llm.ErrProbeUnsupported)
		assert.Zero(t, hits.Load())
	})
	t.Run("a model the show route doesn't know is ErrModelMissing", func(t *testing.T) {
		t.Parallel()
		p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/show" {
				http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(`{"version":"0.40.16"}`))
		}, llm.Endpoint{Probe: "ollama"})
		_, err := p.Probe(t.Context(), "nope")
		require.ErrorIs(t, err, llm.ErrModelMissing)
	})
}
