package agent

import (
	"context"
	"errors"
	"time"

	"github.com/bobcob7/workspaces/internal/llm"
)

const (
	maxRetries    = 3
	maxRetryAfter = 60 * time.Second
)

// retry handles a failed request. nil means the request is sent again, after
// the backoff and the re-resolution; an exit means the run ends. Each retry
// spends one of maxRetries, and a failed new endpoint spends one too.
func (r *run) retry(ctx context.Context, err error) *exitInfo {
	if errors.Is(err, llm.ErrContextExceeded) {
		return r.overflow(err)
	}
	retryable, after := llm.Retryable(err)
	if !retryable {
		return r.fatal(ctx, err)
	}
	r.lastErr = err
	for {
		r.spent++
		wait := retryWait(r.spent, after)
		r.recordRetry(err, wait)
		if sleepErr := r.agent.clk.Sleep(ctx, wait); sleepErr != nil {
			return interrupted()
		}
		failure := r.reresolve(ctx)
		if r.spent == maxRetries {
			return r.exhausted()
		}
		if failure == nil {
			return nil
		}
		err, after = failure, 0
		r.lastErr = err
	}
}

func (r *run) recordRetry(err error, wait time.Duration) {
	r.record(kindRetry, RetryEvent{Attempt: r.spent, Error: err.Error(), WaitMS: wait.Milliseconds(), Endpoint: r.target.Endpoint})
}

func interrupted() *exitInfo {
	return &exitInfo{reason: ExitStopped}
}

// fatal ends the run for an error that retrying can't fix.
func (r *run) fatal(ctx context.Context, err error) *exitInfo {
	if ctx.Err() != nil {
		return interrupted()
	}
	switch {
	case errors.Is(err, llm.ErrNoTools):
		return &exitInfo{reason: ExitModelNoTools, message: r.target.Endpoint + ": " + err.Error()}
	case errors.Is(err, llm.ErrModelMissing):
		return &exitInfo{reason: ExitModelMissing, message: r.target.Endpoint + ": " + err.Error()}
	}
	return &exitInfo{reason: ExitEndpointDown, message: err.Error()}
}

// overflow handles a context error: the history is compacted and sent again.
// Compacting happens once per run; a second overflow, or a history that
// doesn't fit after compacting, ends the run.
func (r *run) overflow(err error) *exitInfo {
	if r.overflowed || !r.compactNow(true) {
		return &exitInfo{reason: ExitContextOverflow, message: err.Error()}
	}
	r.overflowed = true
	return nil
}

// exhausted ends the run after its last retry: rejected when the last
// re-resolution was refused, else down.
func (r *run) exhausted() *exitInfo {
	if rej := r.rejection; rej != nil {
		return &exitInfo{reason: ExitEndpointRejected, message: rej.Endpoint + ": " + rej.Code + ": " + rej.Message}
	}
	return &exitInfo{reason: ExitEndpointDown, message: errorText(r.lastErr)}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
