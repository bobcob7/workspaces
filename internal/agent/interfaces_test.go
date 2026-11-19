package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSystemClock_Sleep(t *testing.T) {
	t.Run("returns the context's error when cancelled first", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := SystemClock{}.Sleep(ctx, time.Hour)
		assert.ErrorIs(t, err, context.Canceled)
	})
	t.Run("returns nil after the duration", func(t *testing.T) {
		assert.NoError(t, SystemClock{}.Sleep(t.Context(), time.Millisecond))
	})
}
