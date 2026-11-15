package agent

import (
	"context"
	"time"

	"github.com/bobcob7/workspaces/internal/agent/tools"
	"github.com/bobcob7/workspaces/internal/llm"
)

//go:generate go tool moq -out moq_test.go . chatter clock Toolset reresolver

// chatter is llm.Provider's Chat.
type chatter interface {
	Chat(ctx context.Context, req llm.Request) (llm.Response, error)
}

// clock is the loop's time source: wall-clock budgets and backoff sleeps.
type clock interface {
	Now() time.Time
	Sleep(ctx context.Context, d time.Duration) error
}

// Toolset is what the loop offers and calls. StaticToolset offers a fixed
// list; the runner adds MCP tools and tool_search (T12).
type Toolset interface {
	Definitions() []llm.Tool // for this request
	Call(ctx context.Context, name, arguments string) (tools.Result, error)
	Known(name string) bool
}

// reresolver re-reads the run's alias (T28 implements it in internal/runner).
type reresolver interface {
	Reresolve(ctx context.Context, current Target) Resolution
}

// SystemClock is the wall clock.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time {
	return time.Now()
}

// Sleep waits for d, or returns the context's error when it ends first.
func (SystemClock) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
