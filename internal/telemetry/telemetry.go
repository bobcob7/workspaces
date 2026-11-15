// Package telemetry is the runner's OpenTelemetry setup and the
// decorators that instrument the provider seam and the toolset (ADR 0065).
// It is the only package that imports go.opentelemetry.io.
//
// Copied from llm-router internal/telemetry @9789c32 (ADR 0065); maintained here.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/semconv/v1.39.0/genaiconv"
	"go.opentelemetry.io/otel/trace"
)

// ServiceName is service.name; OTEL_SERVICE_NAME overrides it.
const ServiceName = "ws-runner"

// exporterEnv are the environment variables that turn on OTLP export. With
// none of them set, telemetry is collected (so trace IDs exist) but not sent
// anywhere, so local runs don't fail to reach a collector.
func exporterEnv() []string {
	return []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
		"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT",
		"OTEL_TRACES_EXPORTER",
		"OTEL_METRICS_EXPORTER",
		"OTEL_LOGS_EXPORTER",
	}
}

// Telemetry is the runner's tracer, meter and logger.
type Telemetry struct {
	Logger   *slog.Logger
	tracer   trace.Tracer
	inst     instruments
	shutdown []func(context.Context) error
}

// instruments are the metrics the runner records.
type instruments struct {
	duration        genaiconv.ClientOperationDuration
	tokens          genaiconv.ClientTokenUsage
	runs            metric.Int64Counter
	endpointChanges metric.Int64Counter
}

// New sets up SDK tracer and meter providers, always, and exporters and the
// OTLP log bridge when an exporter variable is set. Logs go to w as slog JSON
// either way.
func New(ctx context.Context, w io.Writer, getenv func(string) string) (*Telemetry, error) {
	t := &Telemetry{}
	handler, err := t.setUp(ctx, w, exportConfigured(getenv))
	if err != nil {
		return nil, errors.Join(err, t.Shutdown(ctx))
	}
	t.Logger = slog.New(handler)
	return t, nil
}

// Shutdown flushes and stops exporters. It is safe without exporters.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	for _, fn := range t.shutdown {
		errs = append(errs, fn(ctx))
	}
	return errors.Join(errs...)
}

func exportConfigured(getenv func(string) string) bool {
	for _, key := range exporterEnv() {
		if getenv(key) != "" {
			return true
		}
	}
	return false
}

// setUp installs the SDK providers, with exporters when export is set, and
// returns the logger's handler, which also exports logs when it is.
func (t *Telemetry) setUp(ctx context.Context, w io.Writer, export bool) (slog.Handler, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(semconv.ServiceName(ServiceName)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("building resource: %w", err)
	}
	tracerOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	meterOpts := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if export {
		if tracerOpts, meterOpts, err = withExporters(ctx, tracerOpts, meterOpts); err != nil {
			return nil, err
		}
	}
	tp := sdktrace.NewTracerProvider(tracerOpts...)
	mp := sdkmetric.NewMeterProvider(meterOpts...)
	t.shutdown = append(t.shutdown, tp.Shutdown, mp.Shutdown)
	if err = t.setProviders(tp, mp); err != nil {
		return nil, err
	}
	handler := slog.NewJSONHandler(w, nil)
	if !export {
		return handler, nil
	}
	return t.logBridge(ctx, res, handler)
}

// setProviders takes the tracer and the instruments from the providers.
func (t *Telemetry) setProviders(tp trace.TracerProvider, mp metric.MeterProvider) error {
	inst, err := newInstruments(mp.Meter(ServiceName))
	if err != nil {
		return fmt.Errorf("creating instruments: %w", err)
	}
	t.tracer = tp.Tracer(ServiceName)
	t.inst = inst
	return nil
}

// logBridge returns handler with the OTLP log bridge added.
func (t *Telemetry) logBridge(ctx context.Context, res *resource.Resource, handler slog.Handler) (slog.Handler, error) {
	exporter, err := autoexport.NewLogExporter(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating log exporter: %w", err)
	}
	//nolint:forbidigo // the OTel log SDK is the bridge's exporter; records still go through the injected handler
	lp := sdklog.NewLoggerProvider(sdklog.WithResource(res), sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)))
	t.shutdown = append(t.shutdown, lp.Shutdown) //nolint:forbidigo // shuts down the bridge's provider
	bridge := &levelHandler{Handler: otelslog.NewHandler(ServiceName, otelslog.WithLoggerProvider(lp)), level: slog.LevelInfo}
	return slog.NewMultiHandler(handler, bridge), nil
}

func withExporters(ctx context.Context, tracerOpts []sdktrace.TracerProviderOption, meterOpts []sdkmetric.Option) ([]sdktrace.TracerProviderOption, []sdkmetric.Option, error) {
	spans, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("creating span exporter: %w", err)
	}
	reader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("creating metric reader: %w", err), spans.Shutdown(ctx))
	}
	return append(tracerOpts, sdktrace.WithBatcher(spans)), append(meterOpts, sdkmetric.WithReader(reader)), nil
}

func newInstruments(meter metric.Meter) (instruments, error) {
	var inst instruments
	var errs [4]error
	inst.duration, errs[0] = genaiconv.NewClientOperationDuration(meter)
	inst.tokens, errs[1] = genaiconv.NewClientTokenUsage(meter)
	inst.runs, errs[2] = meter.Int64Counter(MetricRuns, metric.WithDescription("Agent process runs, by exit."), metric.WithUnit("{run}"))
	inst.endpointChanges, errs[3] = meter.Int64Counter(MetricEndpointChanges, metric.WithDescription("Endpoint switches and refusals."), metric.WithUnit("{change}"))
	return inst, errors.Join(errs[:]...)
}

// levelHandler drops records below level. The OTel log bridge has no level
// of its own, so this keeps exported logs in step with stderr.
type levelHandler struct {
	slog.Handler
	level slog.Leveler
}

// Enabled implements slog.Handler.
func (h *levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.Handler.Enabled(ctx, l)
}

// WithAttrs implements slog.Handler.
func (h *levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithAttrs(attrs), level: h.level}
}

// WithGroup implements slog.Handler.
func (h *levelHandler) WithGroup(name string) slog.Handler {
	return &levelHandler{Handler: h.Handler.WithGroup(name), level: h.level}
}
