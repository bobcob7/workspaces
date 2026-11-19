package agent

import (
	"context"
	"fmt"
	"slices"
)

// resumeReresolve checks the endpoint once before a resumed run's first
// request. A failure keeps the target and spends no retry.
func (r *run) resumeReresolve(ctx context.Context) {
	res := r.agent.cfg.Reresolver.Reresolve(ctx, r.target)
	switch {
	case res.Warning != nil:
		r.warn(*res.Warning)
	case res.Failed != nil:
		r.reject(Rejection{Endpoint: r.target.Endpoint, Model: r.target.Model, Code: "endpoint_unreachable", Message: res.Failed.Error()}, "resume", nil)
	case res.Rejected != nil:
		r.reject(*res.Rejected, "resume", nil)
	case res.Changed:
		r.switchTo(&res, "resume", nil)
	default:
		r.rejection = nil
	}
}

// reresolve re-reads the run's endpoint after a retry's backoff. It returns
// the new endpoint's error when that endpoint failed the check, which spends
// the retry.
func (r *run) reresolve(ctx context.Context) error {
	if r.agent.cfg.Reresolver == nil {
		return nil
	}
	res := r.agent.cfg.Reresolver.Reresolve(ctx, r.target)
	attempt := r.spent
	switch {
	case res.Warning != nil:
		r.warn(*res.Warning)
	case res.Failed != nil:
		return r.failedRetry(&res)
	case res.Rejected != nil:
		r.reject(*res.Rejected, "retry", &attempt)
	case res.Changed:
		r.switchTo(&res, "retry", &attempt)
	default:
		r.rejection = nil
	}
	return nil
}

func (r *run) failedRetry(res *Resolution) error {
	name := r.target.Endpoint
	if res.Target.Endpoint != "" {
		name = res.Target.Endpoint
	}
	r.record(kindRetry, RetryEvent{Attempt: r.spent, Error: res.Failed.Error(), Endpoint: name})
	return res.Failed
}

func (r *run) warn(w ConfigWarning) {
	r.record(kindConfigWarning, ConfigWarningEvent{Code: w.Code, Message: w.Message, KeptEndpoint: r.target.Endpoint})
	r.agent.log.Warn("configuration not used", "code", w.Code)
}

// reject keeps the target, remembers the rejection for the exit, and records it.
func (r *run) reject(rej Rejection, reason string, attempt *int) {
	r.rejection = &rej
	r.record(kindEndpointRejected, EndpointRejectedEvent{
		Reason:       reason,
		Attempt:      attempt,
		Endpoint:     rej.Endpoint,
		Model:        rej.Model,
		Code:         rej.Code,
		Message:      rej.Message,
		KeptEndpoint: r.target.Endpoint,
	})
	if r.agent.cfg.OnReject != nil {
		r.agent.cfg.OnReject(rej, reason)
	}
}

// switchTo moves the run to the new target when the conversation fits its
// window; otherwise it is rejected as context_too_small.
func (r *run) switchTo(res *Resolution, reason string, attempt *int) {
	limit := res.Target.ContextTokens - res.Target.MaxOutput
	msgs, ev, fits := compact(r.history, r.agent.cfg.System, r.estimate(), limit, r.done, false)
	if !fits {
		r.reject(Rejection{
			Endpoint: res.Target.Endpoint,
			Model:    res.Target.Model,
			Code:     "context_too_small",
			Message:  fmt.Sprintf("the task and last turn need more than %d tokens", limit),
		}, reason, attempt)
		return
	}
	from := r.target
	r.record(kindEndpointChanged, changedEvent(&from, res, reason, attempt))
	r.target = res.Target
	r.applyCompaction(msgs, ev)
	if from.API != r.target.API {
		r.dropReasoningOpaque()
	}
	r.truncations, r.truncatedTo = 0, 0
	r.rejection = nil
	r.changes++
	r.endpoints = append(r.endpoints, EndpointUsage{Endpoint: r.target.Endpoint, Model: r.target.Model})
	if r.agent.cfg.OnSwitch != nil {
		r.agent.cfg.OnSwitch(from, r.target, reason, r.step)
	}
	r.setState("working")
}

// dropReasoningOpaque removes reasoning that only the old provider accepts.
func (r *run) dropReasoningOpaque() {
	for i := range r.history {
		r.history[i].ReasoningOpaque = nil
	}
	r.agent.log.Info("dropped provider reasoning after an api change", "endpoint", r.target.Endpoint)
}

func changedEvent(from *Target, res *Resolution, reason string, attempt *int) EndpointChangedEvent {
	ev := EndpointChangedEvent{
		Reason:            reason,
		Attempt:           attempt,
		FromEndpoint:      from.Endpoint,
		ToEndpoint:        res.Target.Endpoint,
		FromModel:         from.Model,
		ToModel:           res.Target.Model,
		FromContextTokens: from.ContextTokens,
		ToContextTokens:   res.Target.ContextTokens,
		API:               res.Target.API,
		Changed:           slices.Clone(res.Changes),
	}
	if ev.Changed == nil {
		ev.Changed = []string{}
	}
	if res.Probe != nil {
		version := res.Probe.Version
		capabilities := res.Probe.Capabilities
		ev.Version = &version
		ev.Capabilities = &capabilities
	}
	return ev
}
