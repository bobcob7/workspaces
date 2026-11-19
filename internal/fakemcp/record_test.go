package fakemcp

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecorder_Write(t *testing.T) {
	t.Run("writes one JSON line with the tool and arguments", func(t *testing.T) {
		var out bytes.Buffer
		rec := &recorder{w: &out}
		require.NoError(t, rec.write("lookup_service", []byte(`{"name":"api"}`)))
		assert.Regexp(t, `^\{"time":"[^"]+","tool":"lookup_service","arguments":\{"name":"api"\}\}\n$`, out.String())
	})
	t.Run("a nil recorder records nothing", func(t *testing.T) {
		var rec *recorder
		assert.NoError(t, rec.write("lookup_service", []byte(`{}`)))
	})
	t.Run("returns the writer's error", func(t *testing.T) {
		rec := &recorder{w: failingWriter{}}
		assert.Error(t, rec.write("lookup_service", []byte(`{}`)))
	})
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("disk full")
}
