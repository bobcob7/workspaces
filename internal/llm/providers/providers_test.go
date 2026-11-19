package providers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/llm/openai"
)

func TestNew(t *testing.T) {
	t.Parallel()
	t.Run("openai and the empty api are the OpenAI-compatible provider", func(t *testing.T) {
		t.Parallel()
		for _, api := range []string{"", config.APIOpenAI} {
			p, err := New(llm.Endpoint{API: api, URL: "http://127.0.0.1:1"}, http.DefaultClient, nil)
			require.NoError(t, err, api)
			assert.IsType(t, &openai.Provider{}, p, api)
		}
	})
	t.Run("reserved apis are not implemented yet", func(t *testing.T) {
		t.Parallel()
		for _, api := range []string{"anthropic", "ollama", "openai-responses"} {
			_, err := New(llm.Endpoint{API: api}, http.DefaultClient, nil)
			require.ErrorIs(t, err, config.ErrAPINotImplemented, api)
		}
	})
	t.Run("an unknown api is an error", func(t *testing.T) {
		t.Parallel()
		_, err := New(llm.Endpoint{API: "carrier-pigeon"}, http.DefaultClient, nil)
		require.Error(t, err)
		assert.NotErrorIs(t, err, config.ErrAPINotImplemented)
	})
}
