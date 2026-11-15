package openai

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/bobcob7/workspaces/internal/llm"
)

// autoProbe remembers the answer of GET /api/version for an "auto" endpoint,
// which is asked once per Provider.
type autoProbe struct {
	once    sync.Once
	version string
	err     error
}

type showResponse struct {
	Capabilities []string       `json:"capabilities"`
	ModelInfo    map[string]any `json:"model_info"`
}

// Probe reports what the endpoint says about model, as its probe setting
// names: ollama asks /api/version and /api/show, none never asks, and auto
// does the first when /api/version answers. It returns
// llm.ErrProbeUnsupported when there is nothing to ask.
func (p *Provider) Probe(ctx context.Context, model string) (llm.ModelInfo, error) {
	switch p.endpoint.Probe {
	case "none":
		return llm.ModelInfo{}, llm.ErrProbeUnsupported
	case "ollama":
		version, err := p.version(ctx)
		if err != nil {
			return llm.ModelInfo{}, err
		}
		return p.showModel(ctx, model, version)
	default:
		version, ok := p.autoVersion(ctx)
		if !ok {
			return llm.ModelInfo{}, llm.ErrProbeUnsupported
		}
		return p.showModel(ctx, model, version)
	}
}

func (p *Provider) autoVersion(ctx context.Context) (string, bool) {
	p.auto.once.Do(func() {
		p.auto.version, p.auto.err = p.version(ctx)
	})
	return p.auto.version, p.auto.err == nil
}

func (p *Provider) version(ctx context.Context) (string, error) {
	var out struct {
		Version string `json:"version"`
	}
	if err := p.getJSON(ctx, "/api/version", &out); err != nil {
		return "", fmt.Errorf("probing version: %w", err)
	}
	return out.Version, nil
}

func (p *Provider) showModel(ctx context.Context, model, version string) (llm.ModelInfo, error) {
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return llm.ModelInfo{}, fmt.Errorf("encoding show request: %w", err)
	}
	status, data, err := p.send(ctx, http.MethodPost, "/api/show", body)
	if err != nil {
		return llm.ModelInfo{}, err
	}
	if status == http.StatusNotFound {
		return llm.ModelInfo{}, &llm.ModelMissingError{Model: model}
	}
	if err := checkStatus("POST /api/show", status, data); err != nil {
		return llm.ModelInfo{}, err
	}
	var show showResponse
	if err := json.Unmarshal(data, &show); err != nil {
		return llm.ModelInfo{}, fmt.Errorf("decoding show response: %w", err)
	}
	return llm.ModelInfo{
		Version:       version,
		Capabilities:  show.Capabilities,
		ContextLength: contextLength(show.ModelInfo),
	}, nil
}

// contextLength reads "<architecture>.context_length" from model_info; 0
// when the key is absent.
func contextLength(info map[string]any) int {
	for key, value := range info {
		n, ok := value.(float64)
		if ok && strings.HasSuffix(key, ".context_length") {
			return int(n)
		}
	}
	return 0
}
