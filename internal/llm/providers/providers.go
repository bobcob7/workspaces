// Package providers builds the llm.Provider an endpoint's api names.
package providers

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/llm/openai"
)

// New returns the provider for ep.API: "" or "openai" is the OpenAI-compatible
// provider. The reserved names wrap config.ErrAPINotImplemented; any other
// name is an error.
func New(ep llm.Endpoint, httpClient *http.Client, log *slog.Logger) (llm.Provider, error) {
	if ep.API == "" || ep.API == config.APIOpenAI {
		return openai.New(ep, httpClient, log), nil
	}
	if slices.Contains(config.ReservedAPIs(), ep.API) {
		return nil, fmt.Errorf("api %q: %w", ep.API, config.ErrAPINotImplemented)
	}
	return nil, fmt.Errorf("unknown api %q", ep.API)
}
