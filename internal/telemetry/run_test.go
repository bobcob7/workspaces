package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"

	"github.com/bobcob7/workspaces/internal/llm"
)

const testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestTelemetry_StartRun(t *testing.T) {
	t.Run("names the span after the role and shares the trace id", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer", ModelID: "qwen/qwen3.5-9b", WorkspaceID: "ws-1", Session: "s-1"})
		end("done")
		span := endedSpan(t, rec.spans, "invoke_agent implementer")
		assert.Equal(t, testTraceID, span.SpanContext().TraceID().String())
		assert.Equal(t, trace.SpanKindInternal, span.SpanKind())
		assert.Equal(t, "done", attrOf(span.Attributes(), AttrExit).AsString())
		assert.Equal(t, "qwen/qwen3.5-9b", attrOf(span.Attributes(), "gen_ai.request.model").AsString())
		assert.Equal(t, "ws-1", attrOf(span.Attributes(), AttrWorkspaceID).AsString())
		assert.Equal(t, "s-1", attrOf(span.Attributes(), AttrSession).AsString())
		assert.Equal(t, trace.SpanFromContext(ctx).SpanContext().SpanID(), span.SpanContext().SpanID())
	})
	t.Run("names a run without a role invoke_agent ws", func(t *testing.T) {
		rec := newRecorder()
		_, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{})
		end("done")
		span := endedSpan(t, rec.spans, "invoke_agent ws")
		assert.Equal(t, "ws", attrOf(span.Attributes(), "gen_ai.agent.name").AsString())
	})
	t.Run("gives two runs of one agent the same trace id", func(t *testing.T) {
		rec := newRecorder()
		_, endFirst := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		endFirst("restart")
		_, endSecond := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		endSecond("done")
		ended := rec.spans.Ended()
		require.Len(t, ended, 2)
		assert.Equal(t, ended[0].SpanContext().TraceID(), ended[1].SpanContext().TraceID())
		assert.NotEqual(t, ended[0].SpanContext().SpanID(), ended[1].SpanContext().SpanID())
	})
	t.Run("counts one run by its exit", func(t *testing.T) {
		rec := newRecorder()
		_, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer", ModelID: "qwen3.5:9b"})
		end("done")
		m := metricNamed(t, rec.metrics, MetricRuns)
		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok)
		require.Len(t, sum.DataPoints, 1)
		assert.Equal(t, int64(1), sum.DataPoints[0].Value)
		assert.Equal(t, "done", attrOf(attrSet(sum.DataPoints[0]), AttrMetricExit).AsString())
	})
	t.Run("starts a new trace for an invalid trace id", func(t *testing.T) {
		rec := newRecorder()
		_, end := rec.tel.StartRun(t.Context(), "not-hex", RunInfo{Role: "implementer"})
		end("done")
		span := endedSpan(t, rec.spans, "invoke_agent implementer")
		assert.True(t, span.SpanContext().TraceID().IsValid())
		assert.NotEqual(t, "not-hex", span.SpanContext().TraceID().String())
	})
}

// attrSet is the attribute set of a data point, as a slice.
func attrSet(dp metricdata.DataPoint[int64]) []attribute.KeyValue {
	return dp.Attributes.ToSlice()
}

func TestTelemetry_EndpointEvent(t *testing.T) {
	t.Run("adds a switch to the run span and counts it", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		rec.tel.EndpointEvent(ctx, EndpointChange{From: "fake", To: "fake-b", FromModel: "qwen/qwen3.5-9b", ToModel: "qwen3.5:9b", Reason: "retry"})
		end("done")
		span := endedSpan(t, rec.spans, "invoke_agent implementer")
		require.Len(t, span.Events(), 1)
		event := span.Events()[0]
		assert.Equal(t, EventEndpointChanged, event.Name)
		assert.Equal(t, "fake-b", attrOf(event.Attributes, AttrEndpointTo).AsString())
		assert.Equal(t, "qwen3.5:9b", attrOf(event.Attributes, AttrModelTo).AsString())
		assert.Equal(t, "retry", attrOf(event.Attributes, AttrEndpointReason).AsString())
		m := metricNamed(t, rec.metrics, MetricEndpointChanges)
		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok)
		require.Len(t, sum.DataPoints, 1)
		assert.Equal(t, OutcomeSwitched, attrOf(attrSet(sum.DataPoints[0]), AttrEndpointOutcome).AsString())
	})
	t.Run("records a refusal with its error type and outcome", func(t *testing.T) {
		rec := newRecorder()
		ctx, end := rec.tel.StartRun(t.Context(), testTraceID, RunInfo{Role: "implementer"})
		rec.tel.EndpointEvent(ctx, EndpointChange{From: "fake", To: "fake-b", Reason: "retry", Code: "model_without_tools"})
		end("done")
		span := endedSpan(t, rec.spans, "invoke_agent implementer")
		require.Len(t, span.Events(), 1)
		event := span.Events()[0]
		assert.Equal(t, EventEndpointRejected, event.Name)
		assert.Equal(t, "model_without_tools", attrOf(event.Attributes, "error.type").AsString())
		m := metricNamed(t, rec.metrics, MetricEndpointChanges)
		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok)
		require.Len(t, sum.DataPoints, 1)
		assert.Equal(t, OutcomeRejected, attrOf(attrSet(sum.DataPoints[0]), AttrEndpointOutcome).AsString())
	})
}

func TestRemoteParent(t *testing.T) {
	t.Run("makes the trace id of the carrier the context's trace", func(t *testing.T) {
		ctx := remoteParent(t.Context(), testTraceID)
		sc := trace.SpanContextFromContext(ctx)
		assert.Equal(t, testTraceID, sc.TraceID().String())
		assert.True(t, sc.IsRemote())
	})
	t.Run("leaves the context without a parent for an invalid trace id", func(t *testing.T) {
		ctx := remoteParent(t.Context(), llm.TraceParent("00000000000000000000000000000000"))
		assert.False(t, trace.SpanContextFromContext(ctx).IsValid())
	})
}
