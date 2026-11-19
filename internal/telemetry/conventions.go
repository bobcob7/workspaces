package telemetry

import (
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/semconv/v1.39.0/genaiconv"
)

// ProviderOpenAICompatible is gen_ai.provider.name for endpoints whose api is
// "openai". The GenAI conventions have no value for them, and "openai" would
// suggest OpenAI's own service.
const ProviderOpenAICompatible genaiconv.ProviderNameAttr = "openai_compatible"

// Attribute keys the runner adds to the GenAI conventions (ADR 0065).
const (
	AttrWorkspaceID     = attribute.Key("ws.workspace.id")
	AttrSession         = attribute.Key("ws.runner.session")
	AttrExit            = attribute.Key("ws.runner.exit")
	AttrMetricExit      = attribute.Key("ws_runner.exit")
	AttrMCPServer       = attribute.Key("ws.mcp.server")
	AttrEndpoint        = attribute.Key("ws.runner.endpoint")
	AttrEndpointFrom    = attribute.Key("ws.runner.endpoint.from")
	AttrEndpointTo      = attribute.Key("ws.runner.endpoint.to")
	AttrModelFrom       = attribute.Key("ws.runner.model.from")
	AttrModelTo         = attribute.Key("ws.runner.model.to")
	AttrEndpointReason  = attribute.Key("ws.runner.endpoint.reason")
	AttrEndpointOutcome = attribute.Key("ws.runner.endpoint.outcome")
)

// Metric and span event names.
const (
	MetricRuns            = "ws_runner.runs"
	MetricEndpointChanges = "ws_runner.endpoint_changes"
	EventEndpointChanged  = "ws.runner.endpoint_changed"
	EventEndpointRejected = "ws.runner.endpoint_rejected"
)

// Values of AttrEndpointOutcome.
const (
	OutcomeSwitched = "switched"
	OutcomeRejected = "rejected"
)
