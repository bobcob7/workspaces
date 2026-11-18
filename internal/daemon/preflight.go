package daemon

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
	"github.com/bobcob7/workspaces/internal/llm/providers"
)

// Defaults for the fields of an endpoint that preflight reports.
const (
	defaultAPI   = config.APIOpenAI
	defaultProbe = "auto"
)

// Preflight resolves the model a request names and checks it through its
// endpoint: the models it serves, the id among them, and the probe's tools
// capability (ADR 0064). Each step has its own 10 s limit.
func (s *Supervisor) Preflight(ctx context.Context, req PreflightRequest) (PreflightResponse, error) {
	cfg, err := s.cfg.LoadConfig()
	if err != nil {
		return PreflightResponse{}, &RemoteError{Code: "config_invalid", Message: err.Error()}
	}
	resolved, err := resolveModel(&cfg, req)
	endpointOnly := req.Check && req.Model == "" && errors.Is(err, config.ErrNoModel)
	if endpointOnly {
		resolved, err = cfg.ResolveEndpoint(req.Endpoint)
	}
	if err != nil {
		return PreflightResponse{}, configFailure(&cfg, err)
	}
	secrets, warnings, err := config.ReadSecrets(s.cfg.ConfigDir)
	if err != nil {
		return PreflightResponse{}, fmt.Errorf("reading secrets.env: %w", err)
	}
	token, source := tokenFor(resolved.EndpointName, secrets, s.cfg.BaseEnv)
	ep := llmEndpoint(resolved.EndpointName, &resolved.Endpoint, token)
	provider, err := providers.New(ep, &http.Client{}, s.log)
	if err != nil {
		return PreflightResponse{}, &RemoteError{Code: "config_invalid", Message: err.Error()}
	}
	var result llm.PreflightResult
	if endpointOnly {
		result, err = llm.PreflightEndpoint(ctx, provider, ep)
	} else {
		result, err = llm.Preflight(ctx, provider, ep, resolved.ModelID, resolved.ContextTokens)
	}
	if err != nil {
		return PreflightResponse{}, preflightFailure(err, ep.URL)
	}
	out := preflightResponse(&resolved, &result, source, warnings)
	if endpointOnly {
		out.Model, out.ContextTokens = nil, nil
	}
	return out, nil
}

// resolveModel applies the role's order to a request. With Check it applies
// `ws runner check`'s defaults: with no model, the first alias on the endpoint
// (runner.model's, when none is named) else runner.model. A named model keeps
// its own endpoint unless one is named too.
func resolveModel(cfg *config.Config, req PreflightRequest) (config.Resolved, error) {
	if !req.Check {
		return cfg.Resolve(req.Role, req.Endpoint, req.Model)
	}
	endpoint, model := req.Endpoint, req.Model
	if model == "" {
		if endpoint == "" {
			endpoint = cfg.Models[cfg.Runner.Model].Endpoint
		}
		model = aliasOn(cfg, endpoint)
	}
	return cfg.Resolve("", endpoint, model)
}

// aliasOn is the first alias, by name, whose endpoint is endpoint, else
// runner.model.
func aliasOn(cfg *config.Config, endpoint string) string {
	for _, alias := range slices.Sorted(maps.Keys(cfg.Models)) {
		if cfg.Models[alias].Endpoint == endpoint {
			return alias
		}
	}
	return cfg.Runner.Model
}

// configFailure is endpoint_unknown, with the endpoints there are, or
// config_invalid for any other configuration error.
func configFailure(cfg *config.Config, err error) error {
	if errors.Is(err, config.ErrEndpointUnknown) {
		return &RemoteError{
			Code:    "endpoint_unknown",
			Message: err.Error(),
			Details: map[string]any{"endpoints": slices.Sorted(maps.Keys(cfg.Endpoints))},
		}
	}
	return &RemoteError{Code: "config_invalid", Message: err.Error()}
}

// tokenFor is endpoint name's token and where it came from. secrets.env wins
// over the daemon's environment, as it does in a runner's environment.
func tokenFor(name string, secrets map[string]string, base []string) (string, *string) {
	key := config.TokenEnv(name)
	if token, ok := secrets[key]; ok {
		return token, new("secrets.env")
	}
	if token, ok := parseEnv(base)[key]; ok {
		return token, new("env")
	}
	return "", nil
}

func llmEndpoint(name string, e *config.Endpoint, token string) llm.Endpoint {
	return llm.Endpoint{
		Name:           name,
		URL:            e.URL,
		API:            e.API,
		Probe:          e.Probe,
		Token:          token,
		RequestTimeout: e.RequestTimeout,
		IdleTimeout:    e.IdleTimeout,
	}
}

// preflightFailure is the answer for a failed preflight step (reference.md).
func preflightFailure(err error, url string) error {
	if unreachable, ok := errors.AsType[*llm.UnreachableError](err); ok {
		return &RemoteError{
			Code:    "endpoint_unreachable",
			Message: err.Error(),
			Details: map[string]any{"url": url, "cause": unreachable.Cause.Error()},
		}
	}
	if missing, ok := errors.AsType[*llm.ModelMissingError](err); ok {
		return &RemoteError{
			Code:    "model_not_served",
			Message: err.Error(),
			Details: map[string]any{"model": missing.Model, "models": missing.Models},
		}
	}
	if noTools, ok := errors.AsType[*llm.NoToolsError](err); ok {
		return &RemoteError{
			Code:    "model_without_tools",
			Message: err.Error(),
			Details: map[string]any{"model": noTools.Model, "capabilities": noTools.Capabilities},
		}
	}
	return fmt.Errorf("preflight: %w", err)
}

// preflightResponse is `ws runner check`'s object.
func preflightResponse(resolved *config.Resolved, result *llm.PreflightResult, source *string, warnings []string) PreflightResponse {
	out := PreflightResponse{
		Endpoint:      resolved.EndpointName,
		URL:           resolved.Endpoint.URL,
		API:           orDefault(resolved.Endpoint.API, defaultAPI),
		Probe:         orDefault(resolved.Endpoint.Probe, defaultProbe),
		Model:         new(resolved.ModelID),
		Capabilities:  result.Capabilities,
		ContextTokens: new(result.ContextTokens),
		Models:        result.Models,
		TokenSource:   source,
		Warnings:      append(append(append([]string{}, warnings...), resolved.Warnings...), result.Warnings...),
	}
	if result.Version != "" {
		out.Version = new(result.Version)
	}
	return out
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
