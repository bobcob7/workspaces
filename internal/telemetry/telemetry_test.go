package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

var errBoom = errors.New("boom")

// recorder is a Telemetry on in-memory exporters, with the logs it wrote.
type recorder struct {
	tel     *Telemetry
	spans   *tracetest.SpanRecorder
	metrics *sdkmetric.ManualReader
	logs    *bytes.Buffer
}

func newRecorder() recorder {
	spans := tracetest.NewSpanRecorder()
	reader := sdkmetric.NewManualReader()
	logs := &bytes.Buffer{}
	tel := newForTest(
		sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)),
		sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
		logs,
	)
	return recorder{tel: tel, spans: spans, metrics: reader, logs: logs}
}

// newForTest builds a Telemetry on the given providers, logging JSON to w.
func newForTest(tp trace.TracerProvider, mp metric.MeterProvider, w io.Writer) *Telemetry {
	t := &Telemetry{Logger: slog.New(slog.NewJSONHandler(w, nil))}
	if err := t.setProviders(tp, mp); err != nil {
		panic(err)
	}
	return t
}

func noEnv(string) string {
	return ""
}

// exportersNone sets the span, metric and log exporters to none, so exporting
// is on but sends nowhere. It returns the environment as the process sees it.
func exportersNone(t *testing.T) func(string) string {
	t.Helper()
	for _, key := range []string{"OTEL_TRACES_EXPORTER", "OTEL_METRICS_EXPORTER", "OTEL_LOGS_EXPORTER"} {
		t.Setenv(key, "none")
	}
	return os.Getenv
}

// endedSpan returns the ended span called name.
func endedSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s
		}
	}
	require.FailNowf(t, "missing span", "no ended span %q", name)
	return nil
}

// metricNamed returns the collected metric called name.
func metricNamed(t *testing.T, reader *sdkmetric.ManualReader, name string) metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &rm))
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m
			}
		}
	}
	require.FailNowf(t, "missing metric", "no metric %q", name)
	return metricdata.Metrics{}
}

func attrOf(attrs []attribute.KeyValue, key attribute.Key) attribute.Value {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value
		}
	}
	return attribute.Value{}
}

// spanText is everything a span exports as text: its name, status, attributes
// and events.
func spanText(s sdktrace.ReadOnlySpan) string {
	var b strings.Builder
	b.WriteString(s.Name() + " " + s.Status().Description + "\n")
	for _, kv := range s.Attributes() {
		b.WriteString(string(kv.Key) + "=" + kv.Value.String() + "\n")
	}
	for _, e := range s.Events() {
		b.WriteString(e.Name + "\n")
		for _, kv := range e.Attributes {
			b.WriteString(string(kv.Key) + "=" + kv.Value.String() + "\n")
		}
	}
	return b.String()
}

func TestNew(t *testing.T) {
	t.Run("traces without exporters and logs JSON", func(t *testing.T) {
		var out bytes.Buffer
		tel, err := New(t.Context(), &out, noEnv)
		require.NoError(t, err)
		ctx, span := tel.tracer.Start(t.Context(), "op")
		span.End()
		tel.Logger.InfoContext(ctx, "hello")
		assert.True(t, span.SpanContext().TraceID().IsValid())
		assert.Contains(t, out.String(), `"msg":"hello"`)
		assert.NoError(t, tel.Shutdown(t.Context()))
	})
	t.Run("keeps logging to the writer when exporters are configured", func(t *testing.T) {
		var out bytes.Buffer
		tel, err := New(t.Context(), &out, exportersNone(t))
		require.NoError(t, err)
		tel.Logger.Info("hello")
		assert.Contains(t, out.String(), `"msg":"hello"`)
		assert.NoError(t, tel.Shutdown(t.Context()))
	})
	t.Run("returns an error for an unknown exporter", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "bogus")
		_, err := New(t.Context(), io.Discard, os.Getenv)
		assert.Error(t, err)
	})
}

func TestTelemetry_setUp(t *testing.T) {
	t.Run("installs the tracer without exporters", func(t *testing.T) {
		var out bytes.Buffer
		tel := &Telemetry{}
		handler, err := tel.setUp(t.Context(), &out, false)
		require.NoError(t, err)
		assert.NotNil(t, handler)
		assert.NotNil(t, tel.tracer)
		assert.NoError(t, tel.Shutdown(t.Context()))
	})
}

func TestTelemetry_logBridge(t *testing.T) {
	t.Run("sends logs to the handler and the bridge", func(t *testing.T) {
		exportersNone(t)
		var out bytes.Buffer
		tel := &Telemetry{}
		handler, err := tel.logBridge(t.Context(), resource.Default(), slog.NewJSONHandler(&out, nil))
		require.NoError(t, err)
		slog.New(handler).Info("hello")
		assert.Contains(t, out.String(), `"msg":"hello"`)
		assert.NoError(t, tel.Shutdown(t.Context()))
	})
}

func TestTelemetry_setProviders(t *testing.T) {
	t.Run("takes the tracer and instruments from the providers", func(t *testing.T) {
		tel := &Telemetry{}
		require.NoError(t, tel.setProviders(tracenoop.NewTracerProvider(), noopmetric.NewMeterProvider()))
		assert.NotNil(t, tel.tracer)
		assert.NotNil(t, tel.inst.runs)
	})
}

func TestTelemetry_Shutdown(t *testing.T) {
	t.Run("is nil without exporters", func(t *testing.T) {
		assert.NoError(t, newRecorder().tel.Shutdown(t.Context()))
	})
	t.Run("returns the error of a shutdown function", func(t *testing.T) {
		tel := &Telemetry{shutdown: []func(context.Context) error{
			func(context.Context) error { return errBoom },
		}}
		assert.ErrorIs(t, tel.Shutdown(t.Context()), errBoom)
	})
}

func TestExportConfigured(t *testing.T) {
	t.Run("is true for each exporter variable", func(t *testing.T) {
		for _, key := range exporterEnv() {
			getenv := func(name string) string {
				if name == key {
					return "otlp"
				}
				return ""
			}
			assert.True(t, exportConfigured(getenv), key)
		}
	})
	t.Run("is false when none is set", func(t *testing.T) {
		assert.False(t, exportConfigured(noEnv))
	})
}

func TestWithExporters(t *testing.T) {
	t.Run("builds span and metric options from the environment", func(t *testing.T) {
		exportersNone(t)
		tracerOpts, meterOpts, err := withExporters(t.Context(), nil, nil)
		require.NoError(t, err)
		assert.Len(t, tracerOpts, 1)
		assert.Len(t, meterOpts, 1)
	})
	t.Run("returns an error for an unknown span exporter", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "bogus")
		_, _, err := withExporters(t.Context(), nil, nil)
		assert.Error(t, err)
	})
}

func TestNewInstruments(t *testing.T) {
	t.Run("creates the runner's instruments on a meter", func(t *testing.T) {
		inst, err := newInstruments(noopmetric.NewMeterProvider().Meter("test"))
		require.NoError(t, err)
		assert.NotNil(t, inst.runs)
		assert.NotNil(t, inst.endpointChanges)
	})
}

func TestLevelHandler_Enabled(t *testing.T) {
	t.Run("drops records below its level", func(t *testing.T) {
		h := &levelHandler{Handler: slog.NewJSONHandler(io.Discard, nil), level: slog.LevelWarn}
		assert.False(t, h.Enabled(t.Context(), slog.LevelInfo))
		assert.True(t, h.Enabled(t.Context(), slog.LevelError))
	})
}

func TestLevelHandler_WithAttrs(t *testing.T) {
	t.Run("keeps the level", func(t *testing.T) {
		h := &levelHandler{Handler: slog.NewJSONHandler(io.Discard, nil), level: slog.LevelWarn}
		derived := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
		assert.False(t, derived.Enabled(t.Context(), slog.LevelInfo))
	})
}

func TestLevelHandler_WithGroup(t *testing.T) {
	t.Run("keeps the level", func(t *testing.T) {
		h := &levelHandler{Handler: slog.NewJSONHandler(io.Discard, nil), level: slog.LevelWarn}
		derived := h.WithGroup("g")
		assert.False(t, derived.Enabled(t.Context(), slog.LevelInfo))
	})
}

// TestTelemetry_NoContent runs a model request and a tool call whose content
// is marked, and checks that no span, metric or log record carries it.
func TestTelemetry_NoContent(t *testing.T) {
	const marker = "SECRET-CONTENT-7f3a"
	rec := newRecorder()
	ctx, end := rec.tel.StartRun(t.Context(), llm.NewTraceID(), RunInfo{Role: "implementer", ModelID: "qwen3.5:9b"})
	inner := &ProviderMock{
		ChatFunc: func(context.Context, llm.Request) (llm.Response, error) {
			return llm.Response{}, fmt.Errorf("%w: %s", llm.ErrRetryable, marker)
		},
	}
	provider := rec.tel.Provider(inner, llm.Endpoint{Name: "fake", URL: "http://fake.example:1234", API: "openai"})
	_, _ = provider.Chat(ctx, llm.Request{
		Model:    "qwen3.5:9b",
		System:   marker,
		Messages: []llm.Message{{Role: llm.RoleUser, Content: marker, Reasoning: marker}},
		Tools:    []llm.Tool{{Name: marker, Description: marker}},
	})
	toolset := rec.tel.Toolset(&ToolsetMock{
		CallFunc: func(context.Context, string, string) (tools.Result, error) {
			return tools.Result{Text: marker}, errBoom
		},
	}, func(string) string { return "" })
	_, _ = toolset.Call(agent.WithToolCallID(ctx, "call_1"), "run", marker)
	rec.tel.EndpointEvent(ctx, EndpointChange{From: "fake", To: "fake-b", Reason: "retry", Code: "model_without_tools"})
	end("done")
	var all strings.Builder
	for _, s := range rec.spans.Ended() {
		all.WriteString(spanText(s))
	}
	var rm metricdata.ResourceMetrics
	require.NoError(t, rec.metrics.Collect(t.Context(), &rm))
	fmt.Fprintf(&all, "%+v", rm)
	all.WriteString(rec.logs.String())
	assert.NotContains(t, all.String(), marker)
}
