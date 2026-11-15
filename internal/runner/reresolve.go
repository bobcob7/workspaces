package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/llm"
)

// Reresolver is the agent loop's re-resolver for one run (ADR 0064). It
// reads config.toml and the new endpoint's token as they are now,
// preflights a changed endpoint with that token, and builds its provider.
// It never chooses another alias and never returns a model without tools
// when a probe can tell.
type Reresolver struct {
	ConfigDir string        // WS_CONFIG_DIR
	Key       config.RunKey // run.Key(), fixed for the run
	Current   config.Target // the target in use; Adopt moves it on a switch
	Getenv    func(string) string
	Build     func(ep llm.Endpoint) (llm.Provider, error)
	Preflight func(ctx context.Context, p llm.Provider, ep llm.Endpoint, model string, contextTokens int) (llm.PreflightResult, error)
	Redact    func(secret string)
	Log       *slog.Logger

	next   config.Target   // the target of the last Changed resolution
	warned map[string]bool // the distinct warnings already logged
}

// Reresolve implements agent's reresolver.
func (r *Reresolver) Reresolve(ctx context.Context, current agent.Target) agent.Resolution { //nolint:gocritic // hugeParam: a target copied once per retry.
	cfg, err := config.ReadFile(config.NewPaths(r.ConfigDir).ConfigFile)
	if err != nil {
		return r.warning(err)
	}
	target, err := cfg.Reresolve(r.Key)
	if err != nil {
		return r.warning(err)
	}
	changes := Changes(r.Current, target)
	if len(changes) == 0 {
		return agent.Resolution{}
	}
	if !agent.SwitchesEndpoint(changes) {
		return r.sameEndpoint(current, target, changes)
	}
	return r.otherEndpoint(ctx, target, changes)
}

// Adopt makes the target of the last Changed resolution the current one. The
// loop's OnSwitch calls it once the switch has happened.
func (r *Reresolver) Adopt() config.Target {
	r.Current = r.next
	return r.Current
}

// sameEndpoint applies settings that don't need a probe. Only a timeout
// change rebuilds the provider, with the same token.
func (r *Reresolver) sameEndpoint(current agent.Target, target config.Target, changes []string) agent.Resolution { //nolint:gocritic // hugeParam: a target copied once per retry.
	next := withSettings(current, target, target.ContextTokens)
	if rebuildsProvider(changes) {
		p, _, err := r.connect(target)
		if err != nil {
			return r.warning(err)
		}
		next.Chat = p
	}
	r.next = target
	return agent.Resolution{Target: next, Changed: true, Changes: changes}
}

// otherEndpoint checks a changed endpoint, URL, api or model id with its
// token before the loop may switch to it.
func (r *Reresolver) otherEndpoint(ctx context.Context, target config.Target, changes []string) agent.Resolution { //nolint:gocritic // hugeParam: a target copied once per retry.
	p, ep, err := r.connect(target)
	if err != nil {
		return r.warning(err)
	}
	res, err := r.Preflight(ctx, p, ep, target.ModelID, target.ContextTokens)
	switch {
	case errors.Is(err, llm.ErrUnreachable):
		return agent.Resolution{Failed: err}
	case errors.Is(err, llm.ErrModelMissing):
		return rejected(target, "model_not_served", err)
	case errors.Is(err, llm.ErrNoTools):
		return rejected(target, "model_without_tools", err)
	case err != nil:
		return agent.Resolution{Failed: err}
	}
	r.next = target
	return agent.Resolution{
		Target:  withSettings(agent.Target{Chat: p}, target, res.ContextTokens),
		Changed: true,
		Changes: changes,
		Probe:   probeOf(res),
	}
}

// connect builds the provider for target's endpoint with its token, and
// keeps the token out of the transcript and the log.
func (r *Reresolver) connect(target config.Target) (llm.Provider, llm.Endpoint, error) { //nolint:gocritic // hugeParam: a target copied once per retry.
	ep := llm.Endpoint{
		Name:           target.EndpointName,
		URL:            target.Endpoint.URL,
		API:            target.Endpoint.API,
		Token:          r.token(target.EndpointName),
		RequestTimeout: target.Endpoint.RequestTimeout,
		IdleTimeout:    target.Endpoint.IdleTimeout,
	}
	if ep.Token != "" {
		r.Redact(ep.Token)
	}
	p, err := r.Build(ep)
	if err != nil {
		return nil, ep, fmt.Errorf("building the provider for %s: %w", ep.Name, err)
	}
	return p, ep, nil
}

// token is the endpoint's token: secrets.env's when that file is usable and
// has it, else the runner's environment's.
func (r *Reresolver) token(endpoint string) string {
	name := config.TokenEnv(endpoint)
	if secrets, ok := r.readSecrets(); ok {
		if value, found := secrets[name]; found {
			return value
		}
	}
	return r.Getenv(name)
}

// readSecrets is secrets.env's values, and whether the file was usable.
func (r *Reresolver) readSecrets() (map[string]string, bool) {
	secrets, warnings, err := config.ReadSecrets(r.ConfigDir)
	if err != nil {
		r.Log.Warn("secrets.env not used", "error", err)
		return nil, false
	}
	for _, w := range warnings {
		r.Log.Warn("secrets.env not used", "warning", w)
	}
	return secrets, len(warnings) == 0
}

// warning is the resolution for a configuration the loop can't use.
func (r *Reresolver) warning(err error) agent.Resolution {
	w := agent.ConfigWarning{Code: warningCode(err), Message: err.Error()}
	r.logOnce(w)
	return agent.Resolution{Warning: &w}
}

// logOnce logs a warning unless its message was already logged.
func (r *Reresolver) logOnce(w agent.ConfigWarning) {
	if r.warned == nil {
		r.warned = map[string]bool{}
	}
	if r.warned[w.Message] {
		return
	}
	r.warned[w.Message] = true
	r.Log.Warn("configuration not used", "code", w.Code, "message", w.Message)
}

// warningCode is the reference.md warning for a configuration error.
func warningCode(err error) string {
	switch {
	case errors.Is(err, config.ErrModelGone):
		return "model_gone"
	case errors.Is(err, config.ErrEndpointUnknown):
		return "endpoint_unknown"
	case errors.Is(err, config.ErrAPINotImplemented):
		return "api_not_implemented"
	default:
		return "config_invalid"
	}
}

// Changes lists the keys that differ between two targets, in reference.md's
// order.
func Changes(from, to config.Target) []string { //nolint:gocritic // hugeParam: the targets are small values copied once per retry.
	pairs := []struct {
		key     string
		changed bool
	}{
		{"endpoint", from.EndpointName != to.EndpointName},
		{"url", from.Endpoint.URL != to.Endpoint.URL},
		{"api", from.Endpoint.API != to.Endpoint.API},
		{"id", from.ModelID != to.ModelID},
		{"context_tokens", from.ContextTokens != to.ContextTokens},
		{"max_output", from.MaxOutput != to.MaxOutput},
		{"temperature", from.Temperature != to.Temperature},
		{"reasoning", from.Reasoning != to.Reasoning},
		{"request_timeout", from.Endpoint.RequestTimeout != to.Endpoint.RequestTimeout},
		{"idle_timeout", from.Endpoint.IdleTimeout != to.Endpoint.IdleTimeout},
	}
	var keys []string
	for _, p := range pairs {
		if p.changed {
			keys = append(keys, p.key)
		}
	}
	return keys
}

func rebuildsProvider(changes []string) bool {
	return slices.Contains(changes, "request_timeout") || slices.Contains(changes, "idle_timeout")
}

// withSettings is t with target's settings, and contextTokens as its window.
func withSettings(t agent.Target, target config.Target, contextTokens int) agent.Target { //nolint:gocritic // hugeParam: a target copied once per retry.
	t.API = target.Endpoint.API
	t.Endpoint = target.EndpointName
	t.Model = target.ModelID
	t.ContextTokens = contextTokens
	t.MaxOutput = target.MaxOutput
	t.Temperature = target.Temperature
	t.Reasoning = target.Reasoning
	return t
}

func rejected(target config.Target, code string, err error) agent.Resolution { //nolint:gocritic // hugeParam: a target copied once per retry.
	return agent.Resolution{Rejected: &agent.Rejection{
		Endpoint: target.EndpointName,
		Model:    target.ModelID,
		Code:     code,
		Message:  err.Error(),
	}}
}

// probeOf is what the check learned about the model, nil without a probe.
func probeOf(res llm.PreflightResult) *llm.ModelInfo { //nolint:gocritic // hugeParam: a result copied once per retry.
	if res.Capabilities == nil {
		return nil
	}
	return &llm.ModelInfo{Version: res.Version, Capabilities: res.Capabilities}
}
