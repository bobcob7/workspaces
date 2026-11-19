package telemetry

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/semconv/v1.39.0/genaiconv"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/llm"
)

// ProviderName is gen_ai.provider.name for an endpoint's api: "ollama" is
// "ollama", and "openai" (or none) is ProviderOpenAICompatible.
func ProviderName(api string) string {
	if api == "ollama" {
		return api
	}
	return string(ProviderOpenAICompatible)
}

// Provider wraps p so that each Chat is a "chat <model>" span, sent to the
// endpoint as W3C traceparent, and counted in the GenAI metrics (ADR 0065).
//
//nolint:gocritic // the signature is the spec; the endpoint is copied once here
func (t *Telemetry) Provider(p llm.Provider, ep llm.Endpoint) llm.Provider {
	return &tracedProvider{
		Provider:     p,
		t:            t,
		endpoint:     ep,
		providerName: genaiconv.ProviderNameAttr(ProviderName(ep.API)),
	}
}

type tracedProvider struct {
	llm.Provider
	t            *Telemetry
	endpoint     llm.Endpoint
	providerName genaiconv.ProviderNameAttr
}

// Chat runs the request inside its chat span, and sets the response's SpanID.
//
//nolint:gocritic // the signature is the provider seam's (ADR 0062)
func (p *tracedProvider) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	attrs := p.requestAttributes(&req)
	ctx, span := p.t.tracer.Start(ctx, "chat "+req.Model, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(attrs...))
	req.TraceParent = traceParentOf(ctx)
	start := time.Now()
	resp, err := p.Provider.Chat(ctx, req)
	resp.SpanID = span.SpanContext().SpanID().String()
	p.finish(ctx, span, start, attrs, &resp, err)
	return resp, err
}

func (p *tracedProvider) requestAttributes(req *llm.Request) []attribute.KeyValue {
	host, port := serverAddress(p.endpoint.URL)
	return []attribute.KeyValue{
		semconv.GenAIOperationNameKey.String(string(genaiconv.OperationNameChat)),
		semconv.GenAIProviderNameKey.String(string(p.providerName)),
		semconv.GenAIRequestModel(req.Model),
		semconv.ServerAddress(host),
		semconv.ServerPort(port),
		AttrEndpoint.String(p.endpoint.Name),
	}
}

// finish ends the span and records the request's duration and tokens. The
// metrics carry only the request's attributes, never the response's counts.
func (p *tracedProvider) finish(ctx context.Context, span trace.Span, start time.Time, attrs []attribute.KeyValue, resp *llm.Response, err error) {
	span.SetAttributes(responseAttributes(resp)...)
	if err != nil {
		typ := chatErrorType(err)
		attrs = append(attrs, semconv.ErrorTypeKey.String(typ))
		span.SetAttributes(semconv.ErrorTypeKey.String(typ))
		span.SetStatus(codes.Error, typ)
	}
	span.End()
	inst := p.t.inst
	inst.duration.Record(ctx, time.Since(start).Seconds(), genaiconv.OperationNameChat, p.providerName, attrs...)
	if !resp.Usage.Reported {
		return
	}
	inst.tokens.Record(ctx, int64(resp.Usage.InputTokens), genaiconv.OperationNameChat, p.providerName, genaiconv.TokenTypeInput, attrs...)
	inst.tokens.Record(ctx, int64(resp.Usage.OutputTokens), genaiconv.OperationNameChat, p.providerName, genaiconv.TokenTypeOutput, attrs...)
}

// responseAttributes are the finish reason and token counts of a response.
func responseAttributes(resp *llm.Response) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	if resp.StopReason != "" {
		attrs = append(attrs, semconv.GenAIResponseFinishReasons(string(resp.StopReason)))
	}
	if resp.Usage.Reported {
		attrs = append(attrs, semconv.GenAIUsageInputTokens(resp.Usage.InputTokens), semconv.GenAIUsageOutputTokens(resp.Usage.OutputTokens))
	}
	return attrs
}

// traceParentOf is the W3C traceparent that names ctx's span.
func traceParentOf(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

// serverAddress is the host and port of an endpoint URL. The port defaults
// to the scheme's; a URL that doesn't parse gives no address.
func serverAddress(rawURL string) (string, int) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", 0
	}
	if port, portErr := strconv.Atoi(u.Port()); portErr == nil {
		return u.Hostname(), port
	}
	if u.Scheme == "https" {
		return u.Hostname(), 443
	}
	return u.Hostname(), 80
}

// chatErrorType is error.type for a failed chat request (ADR 0065). The
// message is left out: it can hold part of a response.
func chatErrorType(err error) string {
	switch {
	case errors.Is(err, llm.ErrUnreachable):
		return "unreachable"
	case errors.Is(err, llm.ErrRetryable):
		return "retryable"
	case errors.Is(err, llm.ErrStreamDropped):
		return "stream_dropped"
	case errors.Is(err, llm.ErrStreamStalled):
		return "stream_stalled"
	case errors.Is(err, llm.ErrModelMissing):
		return "model_missing"
	case errors.Is(err, llm.ErrNoTools):
		return "no_tools"
	case errors.Is(err, llm.ErrContextExceeded):
		return "context_exceeded"
	default:
		return "other"
	}
}
