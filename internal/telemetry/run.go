package telemetry

import (
	"cmp"
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.39.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/llm"
)

// RunInfo names one process run of an agent.
type RunInfo struct{ Role, ModelID, WorkspaceID, Session string }

// EndpointChange describes an endpoint switch. Code is set when the endpoint
// was refused instead, and says why.
type EndpointChange struct{ From, To, FromModel, ToModel, Reason, Code string }

// StartRun starts the run's "invoke_agent <role or ws>" span as a child of a
// remote span context whose trace id is traceID, so every process run of one
// agent shares a trace. A traceID that isn't 32 hex digits starts a new trace.
// The returned end function records the exit, counts the run and ends the span.
func (t *Telemetry) StartRun(ctx context.Context, traceID string, info RunInfo) (context.Context, func(exit string)) {
	ctx = remoteParent(ctx, traceID)
	ctx, span := t.tracer.Start(ctx, "invoke_agent "+cmp.Or(info.Role, "ws"),
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(runAttributes(info)...),
	)
	return ctx, func(exit string) {
		span.SetAttributes(AttrExit.String(exit))
		span.End()
		t.inst.runs.Add(ctx, 1, metric.WithAttributes(append(runIdentity(info), AttrMetricExit.String(exit))...))
	}
}

// EndpointEvent adds an endpoint switch to the run's span, or a refusal when
// e.Code is set, and counts it. Only names and ids are recorded.
//
//nolint:gocritic // the signature is the spec
func (t *Telemetry) EndpointEvent(ctx context.Context, e EndpointChange) {
	outcome, name := OutcomeSwitched, EventEndpointChanged
	attrs := []attribute.KeyValue{
		AttrEndpointFrom.String(e.From),
		AttrEndpointTo.String(e.To),
		AttrModelFrom.String(e.FromModel),
		AttrModelTo.String(e.ToModel),
		AttrEndpointReason.String(e.Reason),
	}
	if e.Code != "" {
		outcome, name = OutcomeRejected, EventEndpointRejected
		attrs = append(attrs, semconv.ErrorTypeKey.String(e.Code))
	}
	trace.SpanFromContext(ctx).AddEvent(name, trace.WithAttributes(attrs...))
	t.inst.endpointChanges.Add(ctx, 1, metric.WithAttributes(
		AttrEndpointFrom.String(e.From),
		AttrEndpointTo.String(e.To),
		AttrEndpointReason.String(e.Reason),
		AttrEndpointOutcome.String(outcome),
	))
}

// remoteParent returns ctx with a remote span context for traceID. An invalid
// traceID leaves ctx without a parent.
func remoteParent(ctx context.Context, traceID string) context.Context {
	carrier := propagation.MapCarrier{"traceparent": llm.TraceParent(traceID)}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// runIdentity is the agent and model a run belongs to, shared by its span and
// its metric.
func runIdentity(info RunInfo) []attribute.KeyValue {
	return []attribute.KeyValue{
		semconv.GenAIAgentName(cmp.Or(info.Role, "ws")),
		semconv.GenAIRequestModel(info.ModelID),
	}
}

func runAttributes(info RunInfo) []attribute.KeyValue {
	return append(runIdentity(info),
		semconv.GenAIOperationNameKey.String("invoke_agent"),
		AttrWorkspaceID.String(info.WorkspaceID),
		AttrSession.String(info.Session),
	)
}
