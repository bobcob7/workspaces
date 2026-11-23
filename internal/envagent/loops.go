package envagent

import (
	"context"
)

// SetLoopTicker sets the ticker serve runs every Options.LoopTick while
// connected. Call it before Run.
func (a *Agent) SetLoopTicker(t loopTicker) {
	a.ticker = t
}

// tickLoops runs until ctx ends: wait LoopTick, then one pass unless one is
// still running, then signalChanged when it changed something. serve starts
// it after the hello, cancels it when the connection ends and waits for it
// to return, so no pass starts after Run waits for the passes. A pass that is
// running then isn't cancelled: it runs under the agent's run context.
func (a *Agent) tickLoops(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.opts.After(a.opts.LoopTick):
		}
		if ctx.Err() != nil {
			return
		}
		if !a.ticking.CompareAndSwap(false, true) {
			continue
		}
		a.passes.Go(func() {
			defer a.ticking.Store(false)
			a.pass(a.runCtx)
		})
	}
}

// pass runs one loop pass, logging a failure.
func (a *Agent) pass(ctx context.Context) {
	changed, err := a.ticker.Tick(ctx)
	if err != nil {
		a.log.WarnContext(ctx, "loop pass failed", "error", err)
		return
	}
	if changed {
		a.signalChanged()
	}
}
