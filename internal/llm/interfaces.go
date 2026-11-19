package llm

import "context"

//go:generate go tool moq -out moq_test.go . Provider

// Provider is one wire protocol to one endpoint (ADR 0062).
type Provider interface {
	// Chat sends the conversation and returns the whole answer. Providers
	// stream internally when the API allows it.
	Chat(ctx context.Context, req Request) (Response, error)
	// Models lists the model ids the endpoint serves.
	Models(ctx context.Context) ([]string, error)
	// Probe reports what the endpoint can say about a model. It returns
	// ErrProbeUnsupported when the API has no way to tell.
	Probe(ctx context.Context, model string) (ModelInfo, error)
}
