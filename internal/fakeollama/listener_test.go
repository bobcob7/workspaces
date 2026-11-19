package fakeollama

import (
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApiServer_setDown(t *testing.T) {
	t.Parallel()
	t.Run("only records the state before start", func(t *testing.T) {
		a := &apiServer{handler: http.NotFoundHandler(), logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
		require.NoError(t, a.setDown(t.Context(), true))
		assert.True(t, a.down)
		assert.Empty(t, a.address())
	})
	t.Run("does nothing when the state is unchanged", func(t *testing.T) {
		a := &apiServer{handler: http.NotFoundHandler(), logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
		require.NoError(t, a.start(t.Context(), "127.0.0.1:0"))
		t.Cleanup(func() { _ = a.shutdown(t.Context()) })
		srv := a.srv
		require.NoError(t, a.setDown(t.Context(), false))
		assert.Same(t, srv, a.srv)
	})
	t.Run("stays down when it can't listen again", func(t *testing.T) {
		t.Parallel()
		a := &apiServer{handler: http.NotFoundHandler(), logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
		require.NoError(t, a.start(t.Context(), "127.0.0.1:0"))
		require.NoError(t, a.setDown(t.Context(), true))
		occupy(t, a.address())
		require.Error(t, a.setDown(t.Context(), false))
		assert.True(t, a.down)
	})
}

func TestApiServer_start(t *testing.T) {
	t.Run("fails for a bad address", func(t *testing.T) {
		a := &apiServer{handler: http.NotFoundHandler(), logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
		assert.Error(t, a.start(t.Context(), "bad:address:here"))
	})
}
