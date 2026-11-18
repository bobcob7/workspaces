package llm

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// preflightTimeout limits each request preflight makes.
const preflightTimeout = 10 * time.Second

// toolsCapability is the capability a model needs to call tools.
const toolsCapability = "tools"

// PreflightResult is what a successful preflight learned.
type PreflightResult struct {
	Models        []string
	Version       string   // "" without a probe
	Capabilities  []string // nil without a probe
	ContextTokens int      // min(configured, the model's when known)
	Warnings      []string
}

// Preflight checks that p serves model with tools and returns what it learned.
// Models must answer (UnreachableError otherwise), model must be listed
// (ModelMissingError), and Probe, unless ErrProbeUnsupported, must report
// "tools" (NoToolsError). A smaller context length lowers contextTokens with a
// warning. Each call gets a 10 s limit.
//
//nolint:gocritic // hugeParam: the endpoint is passed by value, as the seam's contract states.
func Preflight(ctx context.Context, p Provider, ep Endpoint, model string, contextTokens int) (PreflightResult, error) {
	models, err := listModels(ctx, p, ep)
	if err != nil {
		return PreflightResult{}, err
	}
	if !slices.Contains(models, model) {
		return PreflightResult{}, &ModelMissingError{Model: model, Models: models}
	}
	result := PreflightResult{Models: models, ContextTokens: contextTokens}
	info, err := probeModel(ctx, p, model)
	if errors.Is(err, ErrProbeUnsupported) {
		return result, nil
	}
	if err != nil {
		return PreflightResult{}, fmt.Errorf("probe %s: %w", model, err)
	}
	if !slices.Contains(info.Capabilities, toolsCapability) {
		return PreflightResult{}, &NoToolsError{Model: model, Capabilities: info.Capabilities}
	}
	result.Version = info.Version
	result.Capabilities = info.Capabilities
	lowerContext(&result, info.ContextLength)
	return result, nil
}

// PreflightEndpoint is Preflight's first step alone, for an endpoint with no
// model to check yet: it reports the models p serves, and chooses none.
//
//nolint:gocritic // hugeParam: see Preflight.
func PreflightEndpoint(ctx context.Context, p Provider, ep Endpoint) (PreflightResult, error) {
	models, err := listModels(ctx, p, ep)
	if err != nil {
		return PreflightResult{}, err
	}
	return PreflightResult{Models: models}, nil
}

//nolint:gocritic // hugeParam: see Preflight.
func listModels(ctx context.Context, p Provider, ep Endpoint) ([]string, error) {
	callCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	models, err := p.Models(callCtx)
	if err != nil {
		return nil, &UnreachableError{URL: ep.URL, Cause: err}
	}
	return models, nil
}

func probeModel(ctx context.Context, p Provider, model string) (ModelInfo, error) {
	callCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	return p.Probe(callCtx, model)
}

// lowerContext uses the model's context length when it is known and smaller
// than the configured one, and says so in a warning.
func lowerContext(result *PreflightResult, modelContext int) {
	if modelContext <= 0 || modelContext >= result.ContextTokens {
		return
	}
	result.Warnings = append(result.Warnings, fmt.Sprintf(
		"context_tokens lowered from %d to %d, the model's context length",
		result.ContextTokens, modelContext))
	result.ContextTokens = modelContext
}
