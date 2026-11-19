package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAttributeNames(t *testing.T) {
	t.Run("match the names in ADR 0065", func(t *testing.T) {
		assert.Equal(t, "ws.runner.endpoint.outcome", string(AttrEndpointOutcome))
		assert.Equal(t, "ws_runner.exit", string(AttrMetricExit))
		assert.Equal(t, "ws_runner.runs", MetricRuns)
		assert.Equal(t, "ws_runner.endpoint_changes", MetricEndpointChanges)
		assert.Equal(t, "ws.runner.endpoint_rejected", EventEndpointRejected)
	})
	t.Run("name the openai api's provider as openai_compatible", func(t *testing.T) {
		assert.Equal(t, "openai_compatible", string(ProviderOpenAICompatible))
	})
}
