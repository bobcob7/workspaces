package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRun_Current(t *testing.T) {
	t.Parallel()
	t.Run("is the last endpoint history entry", func(t *testing.T) {
		run := sampleRun()
		assert.Equal(t, "gpu-b", run.Current().Endpoint)
		assert.Equal(t, "retry", run.Current().Reason)
	})
	t.Run("without history is the spawn's endpoint keys", func(t *testing.T) {
		run := sampleRun()
		run.EndpointHistory = nil
		current := run.Current()
		assert.Equal(t, "gpu", current.Endpoint)
		assert.Equal(t, "http://gpu-a.inference.svc:1234", current.URL)
		assert.Equal(t, "spawn", current.Reason)
	})
}

func TestRun_Key(t *testing.T) {
	t.Parallel()
	t.Run("an alias key has no model id", func(t *testing.T) {
		run := sampleRun()
		key := run.Key()
		assert.Equal(t, "qwen9b", key.ModelAlias)
		assert.Empty(t, key.ModelID)
	})
	t.Run("a raw id key carries the model id and the endpoint alias", func(t *testing.T) {
		run := sampleRun()
		run.ModelAlias = ""
		run.EndpointAlias = "gpu"
		run.EndpointFlag = "gpu-b"
		key := run.Key()
		assert.Equal(t, "qwen/qwen3.5-9b", key.ModelID)
		assert.Equal(t, "gpu", key.EndpointAlias)
		assert.Equal(t, "gpu-b", key.EndpointFlag)
	})
}
