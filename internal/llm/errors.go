package llm

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors, classified once by each provider.
var (
	ErrUnreachable      = errors.New("endpoint unreachable")
	ErrRetryable        = errors.New("retryable error")
	ErrStreamDropped    = errors.New("stream dropped")
	ErrStreamStalled    = errors.New("stream stalled")
	ErrModelMissing     = errors.New("model not served")
	ErrNoTools          = errors.New("model without tools")
	ErrContextExceeded  = errors.New("context length exceeded")
	ErrProbeUnsupported = errors.New("probe unsupported")
)

// UnreachableError means the endpoint didn't answer.
type UnreachableError struct {
	URL   string
	Cause error
}

func (e *UnreachableError) Error() string {
	return fmt.Sprintf("endpoint %s unreachable: %v", e.URL, e.Cause)
}

func (e *UnreachableError) Unwrap() error { return e.Cause }

// Is matches ErrUnreachable.
func (*UnreachableError) Is(target error) bool { return target == ErrUnreachable }

// ModelMissingError means the endpoint doesn't serve the model.
type ModelMissingError struct {
	Model  string
	Models []string
}

func (e *ModelMissingError) Error() string {
	return fmt.Sprintf("model %s is not served", e.Model)
}

// Is matches ErrModelMissing.
func (*ModelMissingError) Is(target error) bool { return target == ErrModelMissing }

// NoToolsError means the model has no tools capability.
type NoToolsError struct {
	Model        string
	Capabilities []string
}

func (e *NoToolsError) Error() string {
	return fmt.Sprintf("model %s has no tools capability", e.Model)
}

// Is matches ErrNoTools.
func (*NoToolsError) Is(target error) bool { return target == ErrNoTools }

// RetryableError is a failure worth retrying after RetryAfter.
type RetryableError struct {
	Cause      error
	RetryAfter time.Duration
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable: %v", e.Cause)
}

func (e *RetryableError) Unwrap() error { return e.Cause }

// Is matches ErrRetryable.
func (*RetryableError) Is(target error) bool { return target == ErrRetryable }

// Retryable reports whether err may succeed on a retry, and the wait the
// server asked for (0 when it didn't).
func Retryable(err error) (bool, time.Duration) {
	if retryable, ok := errors.AsType[*RetryableError](err); ok {
		return true, retryable.RetryAfter
	}
	retry := errors.Is(err, ErrRetryable) ||
		errors.Is(err, ErrStreamDropped) ||
		errors.Is(err, ErrStreamStalled)
	return retry, 0
}
