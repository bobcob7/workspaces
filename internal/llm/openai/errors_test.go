package openai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	t.Run("reads seconds", func(t *testing.T) {
		t.Parallel()
		h := http.Header{"Retry-After": {"7"}}
		assert.Equal(t, 7*time.Second, retryAfter(h))
	})
	t.Run("reads an HTTP date in the future", func(t *testing.T) {
		t.Parallel()
		h := http.Header{}
		h.Set("Retry-After", time.Now().Add(time.Minute).UTC().Format(http.TimeFormat))
		assert.Greater(t, retryAfter(h), 30*time.Second)
	})
	t.Run("is zero when absent or unreadable", func(t *testing.T) {
		t.Parallel()
		assert.Zero(t, retryAfter(http.Header{}))
		assert.Zero(t, retryAfter(http.Header{"Retry-After": {"soon"}}))
	})
}

func TestClassify(t *testing.T) {
	t.Parallel()
	t.Run("the caller's cancellation is unwrapped", func(t *testing.T) {
		t.Parallel()
		parent, cancel := context.WithCancel(t.Context())
		cancel()
		reqCtx, reqCancel := context.WithCancelCause(parent)
		defer reqCancel(nil)
		assert.Equal(t, context.Canceled, classify(parent, reqCtx, errors.New("x")))
	})
	t.Run("a transport failure is retryable", func(t *testing.T) {
		t.Parallel()
		reqCtx, reqCancel := context.WithCancelCause(t.Context())
		defer reqCancel(nil)
		err := classify(t.Context(), reqCtx, &url.Error{Op: "Post", URL: "http://x", Err: errors.New("refused")})
		retry, _ := llm.Retryable(err)
		assert.True(t, retry)
	})
	t.Run("an error with no cause is returned as it came", func(t *testing.T) {
		t.Parallel()
		reqCtx, reqCancel := context.WithCancelCause(t.Context())
		defer reqCancel(nil)
		want := errors.New("plain")
		assert.Equal(t, want, classify(t.Context(), reqCtx, want))
	})
}

func TestHTTPFailure_classify(t *testing.T) {
	t.Parallel()
	t.Run("a 404 names the model", func(t *testing.T) {
		t.Parallel()
		err := httpFailure{status: http.StatusNotFound, body: "nope"}.classify("m1")
		var missing *llm.ModelMissingError
		require.ErrorAs(t, err, &missing)
		assert.Equal(t, "m1", missing.Model)
	})
	t.Run("a long body is cut", func(t *testing.T) {
		t.Parallel()
		long := make([]byte, maxErrorBody+100)
		for i := range long {
			long[i] = 'x'
		}
		err := httpFailure{status: http.StatusBadRequest, body: string(long)}.classify("m1")
		assert.Less(t, len(err.Error()), maxErrorBody+100)
	})
}
