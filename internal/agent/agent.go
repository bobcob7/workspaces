// Package agent is the runner's agent loop. Copied from llm-router
// internal/agent @9789c32 (ADR 0062); maintained here.
package agent

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/bobcob7/workspaces/internal/llm"
)

// Exit reasons (reference.md, "Runner exit reasons").
const (
	ExitDone             = "done"
	ExitMaxSteps         = "max_steps"
	ExitTimeout          = "timeout"
	ExitMaxTokens        = "max_tokens"
	ExitEndpointDown     = "endpoint_down"
	ExitEndpointRejected = "endpoint_rejected"
	ExitModelNoTools     = "model_no_tools"
	ExitModelMissing     = "model_missing"
	ExitMalformed        = "malformed_tool_calls"
	ExitNoResult         = "no_result"
	ExitLooping          = "looping"
	ExitContextOverflow  = "context_overflow"
	ExitStopped          = "stopped"
	ExitCrashed          = "crashed"
	ExitInternal         = "internal"
)

// Target is where requests go: the run's model on its endpoint. The
// re-resolver may replace it between retries (ADR 0064).
type Target struct {
	Chat          chatter
	API           string // the endpoint's api; a change drops other providers' ReasoningOpaque
	Endpoint      string // the endpoint's name, for events and status
	Model         string
	ContextTokens int
	MaxOutput     int
	Temperature   float64
	Reasoning     string // passed to llm.Request.Reasoning
}

// Resolution is the re-resolver's answer for one retry or a resume. At
// most one of Failed, Rejected and Warning is set.
type Resolution struct {
	Target   Target // a new target when Changed; else ignored
	Changed  bool
	Changes  []string       // the keys that differ (reference.md, endpoint_changed)
	Probe    *llm.ModelInfo // nil without a probe
	Failed   error          // the new endpoint didn't answer the check: this attempt failed
	Rejected *Rejection     // the configuration points elsewhere and the check refused it
	Warning  *ConfigWarning // the configuration couldn't be used
}

// Rejection is a switch the check refused.
type Rejection struct{ Endpoint, Model, Code, Message string }

// ConfigWarning is a configuration the loop could not use.
type ConfigWarning struct{ Code, Message string }

// Config is one run's settings.
type Config struct {
	Target        Target
	ModelAlias    string // the run's alias, for the transcript's start; "" for a raw model id
	Reresolver    reresolver
	OnSwitch      func(from, to Target, reason string, step int)
	OnReject      func(r Rejection, reason string)
	Budget        Budget
	Tools         Toolset
	System        string // rendered preamble + project instructions
	Task          string // rendered role prompt (first user message)
	TraceID       string
	Stop          func() bool // true once `stop` exists
	ResultWritten func() bool // true once result.toml exists (ws machine result via run)
	OnStatus      func(Status)
}

// Status is the run's state, for status.json. State is working,
// waiting_model or exited.
type Status struct {
	State           string
	Step            int
	Usage           Usage
	Endpoint, Model string
	EndpointChanges int
	Endpoints       []EndpointUsage
}

// EndpointUsage is the usage on one endpoint.
type EndpointUsage struct {
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Usage    Usage  `json:"usage"`
}

// Usage counts requests and tokens.
type Usage struct {
	Requests         int `json:"requests"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Outcome is how a run ended.
type Outcome struct {
	Exit, Message, Verdict string
	Usage                  Usage
	Steps                  int
	Endpoints              []EndpointUsage
}

// exitInfo is the reason a run ends, before it is recorded.
type exitInfo struct {
	reason, message, verdict string
}

// Agent runs the loop. New only stores its inputs; Run does the work.
type Agent struct {
	clk clock
	tr  *Transcript
	cfg Config
	log *slog.Logger
}

// New returns an Agent. tr may be nil, which writes no transcript.
//
//nolint:gocritic // the signature is the spec; the config is copied once here
func New(clk clock, tr *Transcript, cfg Config, log *slog.Logger) *Agent {
	return &Agent{clk: clk, tr: tr, cfg: cfg, log: log}
}

// Run starts, or resumes from history (non-empty), until an exit reason.
// prior is the previous process run's Status.Endpoints (nil at a start),
// so per-endpoint usage survives a resume. It never returns an error:
// every failure is an Outcome.
func (a *Agent) Run(ctx context.Context, history []llm.Message, step int, prior []EndpointUsage) Outcome {
	resume := len(history) > 0
	r := a.newRun(history, step, prior)
	r.recordStart(resume)
	if resume && a.cfg.Reresolver != nil {
		r.resumeReresolve(ctx)
	}
	return r.loop(ctx)
}

// run is one Run call's state.
type run struct {
	agent       *Agent
	history     []llm.Message
	target      Target
	step        int
	started     time.Time
	usage       Usage
	endpoints   []EndpointUsage
	changes     int
	spent       int // retries spent on the current request
	sent        int // the estimate sent with the current request
	rejection   *Rejection
	lastErr     error
	done        []ToolRecord
	lastPrompt  int
	measured    int // history[:measured] is in lastPrompt
	truncations int
	truncatedTo int
	overflowed  bool // the once-per-run compaction for a context error has been spent
	noCall      int
	malformed   int
	repeatSig   string
	repeats     int
	pending     []string // nudges to add after the tool results
}

func (a *Agent) newRun(history []llm.Message, step int, prior []EndpointUsage) *run {
	r := &run{agent: a, target: a.cfg.Target, step: step, started: a.clk.Now(), history: slices.Clone(history)}
	if len(history) == 0 {
		r.history = []llm.Message{{Role: llm.RoleUser, Content: a.cfg.Task}}
	}
	r.endpoints = startEndpoints(prior, &r.target)
	r.usage = sumUsage(r.endpoints)
	return r
}

func startEndpoints(prior []EndpointUsage, t *Target) []EndpointUsage {
	if len(prior) == 0 {
		return []EndpointUsage{{Endpoint: t.Endpoint, Model: t.Model}}
	}
	return slices.Clone(prior)
}

func sumUsage(endpoints []EndpointUsage) Usage {
	var total Usage
	for _, e := range endpoints {
		total.plus(e.Usage)
	}
	return total
}

func (u *Usage) plus(o Usage) {
	u.Requests += o.Requests
	u.PromptTokens += o.PromptTokens
	u.CompletionTokens += o.CompletionTokens
}

func (u *Usage) add(in llm.Usage) {
	u.Requests++
	u.PromptTokens += in.InputTokens
	u.CompletionTokens += in.OutputTokens
}

func (r *run) loop(ctx context.Context) Outcome {
	for {
		if ex := r.checkBudget(); ex != nil {
			return r.finish(*ex)
		}
		if ex := r.fitHistory(); ex != nil {
			return r.finish(*ex)
		}
		if ex := r.turn(ctx); ex != nil {
			return r.finish(*ex)
		}
	}
}

// turn makes one request and acts on its reply. A nil exit means the loop goes on.
func (r *run) turn(ctx context.Context) *exitInfo {
	resp, ex := r.send(ctx)
	if ex != nil {
		return ex
	}
	r.accept(&resp)
	if len(resp.Message.ToolCalls) == 0 {
		return r.noToolCall(resp.Message.Content)
	}
	r.noCall = 0
	if ex = r.dispatch(ctx, resp.Message.ToolCalls); ex != nil {
		return ex
	}
	r.flushNudges()
	return nil
}

// send makes the request, retrying as the retry rules allow.
func (r *run) send(ctx context.Context) (llm.Response, *exitInfo) {
	r.spent = 0
	r.sent = r.estimate()
	r.step++
	r.record(kindRequest, RequestEvent{Messages: len(r.history), Tools: len(r.agent.cfg.Tools.Definitions()), EstimatedTokens: r.sent})
	for {
		resp, err := r.attempt(ctx)
		if err == nil {
			return resp, nil
		}
		if ex := r.retry(ctx, err); ex != nil {
			return llm.Response{}, ex
		}
	}
}

func (r *run) attempt(ctx context.Context) (llm.Response, error) {
	r.setState("waiting_model")
	return r.target.Chat.Chat(ctx, r.request())
}

func (r *run) request() llm.Request {
	return llm.Request{
		Model:       r.target.Model,
		System:      r.agent.cfg.System,
		Messages:    r.history,
		Tools:       r.agent.cfg.Tools.Definitions(),
		MaxOutput:   r.target.MaxOutput,
		Temperature: r.target.Temperature,
		Reasoning:   r.target.Reasoning,
		TraceParent: traceParent(r.agent.cfg.TraceID),
	}
}

func traceParent(traceID string) string {
	if traceID == "" {
		return ""
	}
	return llm.TraceParent(traceID)
}

// accept records a reply and adds it to the history.
func (r *run) accept(resp *llm.Response) {
	r.usage.add(resp.Usage)
	r.endpoints[len(r.endpoints)-1].Usage.add(resp.Usage)
	r.checkTruncation(resp.Usage)
	if resp.Usage.Reported {
		r.lastPrompt = resp.Usage.InputTokens
		r.measured = len(r.history)
	} else {
		r.lastPrompt = 0
		r.measured = 0
	}
	r.record(kindResponse, responseEvent(resp))
	msg := resp.Message
	msg.Role = llm.RoleAssistant
	r.history = append(r.history, msg)
	r.setState("working")
}

// checkTruncation notes a prompt the server counted well under the estimate,
// and after the second one lowers the working limit to it.
func (r *run) checkTruncation(u llm.Usage) {
	if !u.Reported || u.InputTokens*100 >= r.sent*80 {
		return
	}
	r.truncations++
	r.record(kindTruncated, TruncatedEvent{EstimatedTokens: r.sent, PromptTokens: u.InputTokens})
	if r.truncations >= 2 {
		r.truncatedTo = u.InputTokens
	}
}

func responseEvent(resp *llm.Response) ResponseEvent {
	ev := ResponseEvent{
		Content:    resp.Message.Content,
		Reasoning:  resp.Message.Reasoning,
		ToolCalls:  toolCallViews(resp.Message.ToolCalls),
		StopReason: string(resp.StopReason),
		DurationMS: resp.Duration.Milliseconds(),
	}
	if resp.Usage.Reported {
		ev.Usage = &tokenUsage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}
	}
	if resp.SpanID != "" {
		span := resp.SpanID
		ev.SpanID = &span
	}
	return ev
}

func toolCallViews(calls []llm.ToolCall) []toolCallView {
	views := make([]toolCallView, 0, len(calls))
	for _, tc := range calls {
		views = append(views, toolCallView{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
	}
	return views
}

func (r *run) finish(ex exitInfo) Outcome {
	r.record(kindExit, ExitEvent{Reason: ex.reason, Message: ex.message, Verdict: nullableString(ex.verdict)})
	r.setState("exited")
	return Outcome{Exit: ex.reason, Message: ex.message, Verdict: ex.verdict, Usage: r.usage, Steps: r.step, Endpoints: r.endpoints}
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (r *run) recordStart(resume bool) {
	names := make([]string, 0)
	for _, def := range r.agent.cfg.Tools.Definitions() {
		names = append(names, def.Name)
	}
	r.record(kindStart, StartEvent{
		Model:         r.target.Model,
		ModelAlias:    r.agent.cfg.ModelAlias,
		Endpoint:      r.target.Endpoint,
		API:           r.target.API,
		ContextTokens: r.target.ContextTokens,
		Reasoning:     r.target.Reasoning,
		Resume:        resume,
		TraceID:       r.agent.cfg.TraceID,
		Messages:      []messageView{{Role: "system", Content: r.agent.cfg.System}, {Role: "user", Content: r.agent.cfg.Task}},
		Tools:         names,
	})
}

// record appends one transcript line; a failed write is logged, not fatal.
func (r *run) record(kind string, payload any) {
	if r.agent.tr == nil {
		return
	}
	ev := Event{Time: r.agent.clk.Now(), Step: r.step, Kind: kind, Payload: payload}
	if err := r.agent.tr.Write(ev); err != nil {
		r.agent.log.Error("writing transcript", "kind", kind, "error", err)
	}
}

func (r *run) setState(state string) {
	if r.agent.cfg.OnStatus == nil {
		return
	}
	r.agent.cfg.OnStatus(Status{
		State:           state,
		Step:            r.step,
		Usage:           r.usage,
		Endpoint:        r.target.Endpoint,
		Model:           r.target.Model,
		EndpointChanges: r.changes,
		Endpoints:       r.endpoints,
	})
}
