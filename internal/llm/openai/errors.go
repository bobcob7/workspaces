package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/llm"
)

// errRequestTimeout is the cancel cause of a request past request_timeout.
// It is a cause, not a deadline, so the caller's own cancellation stays
// distinct.
var errRequestTimeout = errors.New("request timeout")

const maxErrorBody = 4 << 10

// httpFailure is a non-2xx response. It is read before the SDK decodes it:
// the SDK can't parse every server's error body (Ollama sends a string in
// "error" on some routes), and the status alone decides the error class.
type httpFailure struct {
	status int
	body   string
	header http.Header
}

// classify maps the failure to the package's error classes for model.
func (f httpFailure) classify(model string) error {
	text := strings.ToLower(f.body)
	switch {
	case f.status == http.StatusBadRequest && strings.Contains(text, "does not support tools"):
		return &llm.NoToolsError{Model: model}
	case f.status == http.StatusNotFound:
		return &llm.ModelMissingError{Model: model}
	case f.status == http.StatusBadRequest && isContextError(text):
		return fmt.Errorf("%w: %s", llm.ErrContextExceeded, f.message())
	case f.status == http.StatusTooManyRequests || f.status >= http.StatusInternalServerError:
		return &llm.RetryableError{Cause: f.err(), RetryAfter: retryAfter(f.header)}
	default:
		return f.err()
	}
}

func (f httpFailure) err() error {
	return fmt.Errorf("status %d: %s", f.status, f.message())
}

func (f httpFailure) message() string {
	text := strings.TrimSpace(f.body)
	if len(text) > maxErrorBody {
		return text[:maxErrorBody] + "…"
	}
	return text
}

func isContextError(text string) bool {
	return strings.Contains(text, "context length") ||
		strings.Contains(text, "context_length") ||
		strings.Contains(text, "maximum context")
}

// retryAfter reads a Retry-After header, in seconds or as a date; 0 when
// absent or unreadable.
func retryAfter(h http.Header) time.Duration {
	value := h.Get("Retry-After")
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return max(time.Until(when), 0)
}

// classify maps a failed stream to the package's error classes. The
// caller's own cancellation comes back unwrapped; a request cut by its own
// deadline or idle timeout is named by its cancel cause.
func classify(parent, reqCtx context.Context, err error) error {
	if parent.Err() != nil {
		return parent.Err()
	}
	switch cause := context.Cause(reqCtx); {
	case errors.Is(cause, errRequestTimeout):
		return &llm.RetryableError{Cause: fmt.Errorf("request exceeded its timeout: %w", err)}
	case errors.Is(cause, llm.ErrStreamStalled):
		return fmt.Errorf("%w: no bytes within the idle timeout", llm.ErrStreamStalled)
	}
	if _, ok := errors.AsType[*url.Error](err); ok {
		return &llm.RetryableError{Cause: err}
	}
	return err
}
