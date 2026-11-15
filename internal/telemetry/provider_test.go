package telemetry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/llm"
)

const testEndpointURL = "http://fake.example:1234"

func testEndpoint() llm.Endpoint {
	return llm.Endpoint{Name: "fake", URL: testEndpointURL, API: "openai"}
}

// tokenTypes is the gen_ai.token.type of each token usage data point.
func tokenTypes(t *testing.T, reader *sdkmetric.ManualReader) []string {
	t.Helper()
	h, ok := metricNamed(t, reader, "gen_ai.client.token.usage").Data.(metricdata.Histogram[int64])
	require.True(t, ok)
	var types []string
	for _, dp := range h.DataPoints {
		types = append(types, attrOf(dp.Attributes.ToSlice(), "gen_ai.token.type").AsString())
	}
	return types
}

func TestProviderName(t *testing.T) {
	assert.Equal(t, "ollama", ProviderName("ollama"))
	assert.Equal(t, "openai_compatible", ProviderName("openai"))
	assert.Equal(t, "openai_compatible", ProviderName(""))
}

func TestTelemetry_Provider(t *testing.T) {
	t.Run("passes Models and Probe through to the wrapped provider", func(t *testing.T) {
		inner := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return []string{"qwen3.5:9b"}, nil },
			ProbeFunc: func(context.Context, string) (llm.ModelInfo, error) {
				return llm.ModelInfo{ContextLength: 8192}, nil
			},
		}
		p := newRecorder().tel.Provider(inner, testEndpoint())
		models, err := p.Models(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{"qwen3.5:9b"}, models)
		info, err := p.Probe(t.Context(), "qwen3.5:9b")
		require.NoError(t, err)
		assert.Equal(t, 8192, info.ContextLength)
	})
}

// TestTracedProvider_Chat runs a request under a run span and checks the chat
// span, the traceparent it sends, and the metrics it records.
func TestTracedProvider_Chat(t *testing.T) {
	t.Run("names the span after the model and sets its attributes", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		inner := &ProviderMock{ChatFunc: func(context.Context, llm.Request) (llm.Response, error) {
			return llm.Response{StopReason: llm.StopEnd}, nil
		}}
		_, err := rec.tel.Provider(inner, testEndpoint()).Chat(ctx, llm.Request{Model: "qwen/qwen3.5-9b"})
		require.NoError(t, err)
		end("done")
		span := endedSpan(t, rec.spans, "chat qwen/qwen3.5-9b")
		assert.Equal(t, trace.SpanKindClient, span.SpanKind())
		assert.Equal(t, "chat", attrOf(span.Attributes(), "gen_ai.operation.name").AsString())
		assert.Equal(t, "openai_compatible", attrOf(span.Attributes(), "gen_ai.provider.name").AsString())
		assert.Equal(t, "qwen/qwen3.5-9b", attrOf(span.Attributes(), "gen_ai.request.model").AsString())
		assert.Equal(t, "fake.example", attrOf(span.Attributes(), "server.address").AsString())
		assert.Equal(t, int64(1234), attrOf(span.Attributes(), "server.port").AsInt64())
		assert.Equal(t, "fake", attrOf(span.Attributes(), AttrEndpoint).AsString())
	})
	t.Run("sends the chat span as the request's traceparent", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		var sent string
		inner := &ProviderMock{ChatFunc: func(_ context.Context, req llm.Request) (llm.Response, error) {
			sent = req.TraceParent
			return llm.Response{}, nil
		}}
		resp, err := rec.tel.Provider(inner, testEndpoint()).Chat(ctx, llm.Request{Model: "qwen3.5:9b", TraceParent: "00-" + testTraceID + "-0000000000000001-01"})
		require.NoError(t, err)
		end("done")
		span := endedSpan(t, rec.spans, "chat qwen3.5:9b")
		assert.Equal(t, "00-"+testTraceID+"-"+span.SpanContext().SpanID().String()+"-01", sent)
		assert.Equal(t, span.SpanContext().SpanID().String(), resp.SpanID)
	})
	t.Run("records the finish reason and token usage", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		inner := &ProviderMock{ChatFunc: func(context.Context, llm.Request) (llm.Response, error) {
			return llm.Response{StopReason: llm.StopToolCalls, Usage: llm.Usage{InputTokens: 12, OutputTokens: 5, Reported: true}}, nil
		}}
		_, err := rec.tel.Provider(inner, testEndpoint()).Chat(ctx, llm.Request{Model: "qwen3.5:9b"})
		require.NoError(t, err)
		end("done")
		span := endedSpan(t, rec.spans, "chat qwen3.5:9b")
		assert.Equal(t, []string{"tool_calls"}, attrOf(span.Attributes(), "gen_ai.response.finish_reasons").AsStringSlice())
		assert.Equal(t, int64(12), attrOf(span.Attributes(), "gen_ai.usage.input_tokens").AsInt64())
		assert.Equal(t, int64(5), attrOf(span.Attributes(), "gen_ai.usage.output_tokens").AsInt64())
		assert.ElementsMatch(t, []string{"input", "output"}, tokenTypes(t, rec.metrics))
		h, ok := metricNamed(t, rec.metrics, "gen_ai.client.operation.duration").Data.(metricdata.Histogram[float64])
		require.True(t, ok)
		assert.Equal(t, uint64(1), h.DataPoints[0].Count)
	})
	t.Run("records an error type and an error status for a failed request", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		inner := &ProviderMock{ChatFunc: func(context.Context, llm.Request) (llm.Response, error) {
			return llm.Response{}, llm.ErrStreamDropped
		}}
		resp, err := rec.tel.Provider(inner, testEndpoint()).Chat(ctx, llm.Request{Model: "qwen3.5:9b"})
		require.ErrorIs(t, err, llm.ErrStreamDropped)
		end("done")
		span := endedSpan(t, rec.spans, "chat qwen3.5:9b")
		assert.Equal(t, "stream_dropped", attrOf(span.Attributes(), "error.type").AsString())
		assert.Equal(t, codes.Error, span.Status().Code)
		assert.NotEmpty(t, resp.SpanID)
	})
}

func TestResponseAttributes(t *testing.T) {
	t.Run("is empty for a response with no reason and no usage", func(t *testing.T) {
		assert.Empty(t, responseAttributes(llm.Response{}))
	})
	t.Run("holds the finish reason and the reported usage", func(t *testing.T) {
		attrs := responseAttributes(llm.Response{StopReason: llm.StopEnd, Usage: llm.Usage{InputTokens: 1, OutputTokens: 2, Reported: true}})
		assert.Len(t, attrs, 3)
	})
}

func TestTraceParentOf(t *testing.T) {
	t.Run("is empty for a context without a span", func(t *testing.T) {
		assert.Empty(t, traceParentOf(t.Context()))
	})
	t.Run("names the span of the context", func(t *testing.T) {
		rec := newRecorder()
		ctx, span := rec.tel.tracer.Start(t.Context(), "op")
		span.End()
		assert.Equal(t, "00-"+span.SpanContext().TraceID().String()+"-"+span.SpanContext().SpanID().String()+"-01", traceParentOf(ctx))
	})
}

func TestServerAddress(t *testing.T) {
	t.Run("takes the host and the port from the URL", func(t *testing.T) {
		host, port := serverAddress("http://fake.example:1234")
		assert.Equal(t, "fake.example", host)
		assert.Equal(t, 1234, port)
	})
	t.Run("defaults the port to the scheme's", func(t *testing.T) {
		_, https := serverAddress("https://fake.example")
		_, http := serverAddress("http://fake.example")
		assert.Equal(t, 443, https)
		assert.Equal(t, 80, http)
	})
	t.Run("gives no address for a URL that doesn't parse", func(t *testing.T) {
		host, port := serverAddress("http://[::1")
		assert.Empty(t, host)
		assert.Zero(t, port)
	})
}

func TestChatErrorType(t *testing.T) {
	cases := map[string]error{
		"unreachable":       llm.ErrUnreachable,
		"retryable":         llm.ErrRetryable,
		"stream_dropped":    llm.ErrStreamDropped,
		"stream_stalled":    llm.ErrStreamStalled,
		"model_missing":     llm.ErrModelMissing,
		"no_tools":          llm.ErrNoTools,
		"context_exceeded":  llm.ErrContextExceeded,
		"other":             errBoom,
	}
	for want, err := range cases {
		assert.Equal(t, want, chatErrorType(err), want)
	}
}
