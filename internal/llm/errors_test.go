package llm

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnreachableError_Is(t *testing.T) {
	t.Parallel()
	cause := errors.New("connection refused")
	err := fmt.Errorf("preflight: %w", &UnreachableError{URL: "http://x", Cause: cause})
	require.ErrorIs(t, err, ErrUnreachable)
	require.ErrorIs(t, err, cause)
	var unreachable *UnreachableError
	require.ErrorAs(t, err, &unreachable)
	assert.Equal(t, "http://x", unreachable.URL)
}

func TestModelMissingError_Is(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("preflight: %w", &ModelMissingError{Model: "qwen3.5:9b", Models: []string{"a"}})
	require.ErrorIs(t, err, ErrModelMissing)
	var missing *ModelMissingError
	require.ErrorAs(t, err, &missing)
	assert.Equal(t, []string{"a"}, missing.Models)
}

func TestNoToolsError_Is(t *testing.T) {
	t.Parallel()
	err := fmt.Errorf("preflight: %w", &NoToolsError{Model: "m", Capabilities: []string{"completion"}})
	require.ErrorIs(t, err, ErrNoTools)
	var noTools *NoToolsError
	require.ErrorAs(t, err, &noTools)
	assert.Equal(t, []string{"completion"}, noTools.Capabilities)
}

func TestRetryableError_Is(t *testing.T) {
	t.Parallel()
	cause := errors.New("429")
	err := fmt.Errorf("chat: %w", &RetryableError{Cause: cause, RetryAfter: time.Second})
	require.ErrorIs(t, err, ErrRetryable)
	require.ErrorIs(t, err, cause)
	var retryable *RetryableError
	require.ErrorAs(t, err, &retryable)
	assert.Equal(t, time.Second, retryable.RetryAfter)
}

func TestRetryable(t *testing.T) {
	t.Parallel()
	t.Run("wrapped retryable error gives its retry after", func(t *testing.T) {
		t.Parallel()
		err := fmt.Errorf("chat: %w", &RetryableError{Cause: errors.New("busy"), RetryAfter: 7 * time.Second})
		retry, after := Retryable(err)
		assert.True(t, retry)
		assert.Equal(t, 7*time.Second, after)
	})
	t.Run("sentinels without a wait are retryable", func(t *testing.T) {
		t.Parallel()
		for _, sentinel := range []error{ErrRetryable, ErrStreamDropped, ErrStreamStalled} {
			retry, after := Retryable(fmt.Errorf("chat: %w", sentinel))
			assert.True(t, retry, sentinel.Error())
			assert.Zero(t, after, sentinel.Error())
		}
	})
	t.Run("other errors are not retryable", func(t *testing.T) {
		t.Parallel()
		for _, err := range []error{ErrUnreachable, ErrModelMissing, ErrNoTools, ErrContextExceeded, errors.New("other")} {
			retry, _ := Retryable(err)
			assert.False(t, retry, err.Error())
		}
	})
}

func TestTraceParent(t *testing.T) {
	t.Parallel()
	pattern := `^00-[0-9a-f]{32}-[0-9a-f]{16}-01$`
	traceID := NewTraceID()
	assert.Regexp(t, pattern, TraceParent(traceID))
	assert.Contains(t, TraceParent(traceID), "-"+traceID+"-")
}

func TestNewTraceID(t *testing.T) {
	t.Parallel()
	assert.Regexp(t, `^[0-9a-f]{32}$`, NewTraceID())
	first, second := NewTraceID(), NewTraceID()
	assert.NotEqual(t, first, second)
}
