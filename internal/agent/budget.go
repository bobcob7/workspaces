package agent

import (
	"fmt"
	"time"
)

// Budget is a run's limits (ADR 0065). A zero field means no limit.
type Budget struct {
	MaxSteps  int
	Timeout   time.Duration
	MaxTokens int
}

// checkBudget ends the run when it was stopped, has a verdict, or has used
// up a budget.
func (r *run) checkBudget() *exitInfo {
	if r.agent.cfg.Stop != nil && r.agent.cfg.Stop() {
		return &exitInfo{reason: ExitStopped}
	}
	if r.agent.cfg.ResultWritten != nil && r.agent.cfg.ResultWritten() {
		return &exitInfo{reason: ExitDone}
	}
	return r.budgetExit()
}

func (r *run) budgetExit() *exitInfo {
	b := r.agent.cfg.Budget
	switch {
	case b.Timeout > 0 && r.agent.clk.Now().Sub(r.started) >= b.Timeout:
		return &exitInfo{reason: ExitTimeout, message: fmt.Sprintf("the time budget of %s is used up", b.Timeout)}
	case b.MaxSteps > 0 && r.step >= b.MaxSteps:
		return &exitInfo{reason: ExitMaxSteps, message: fmt.Sprintf("the step budget of %d is used up", b.MaxSteps)}
	case b.MaxTokens > 0 && r.usage.PromptTokens+r.usage.CompletionTokens >= b.MaxTokens:
		return &exitInfo{reason: ExitMaxTokens, message: fmt.Sprintf("the token budget of %d is used up", b.MaxTokens)}
	}
	return nil
}

// retryWait is the backoff for the nth retry (2 s, 8 s, then 30 s), or the
// server's Retry-After when that is longer, up to 60 s.
func retryWait(attempt int, after time.Duration) time.Duration {
	base := backoff(attempt)
	if after > base {
		return min(after, maxRetryAfter)
	}
	return base
}

func backoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return 2 * time.Second
	case 2:
		return 8 * time.Second
	}
	return 30 * time.Second
}
