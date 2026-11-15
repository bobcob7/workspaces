package openai

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/bobcob7/workspaces/internal/llm"
)

// Copied from llm-router internal/agent @9789c32 (ADR 0062); maintained here.

const doneMarker = "data: [DONE]"

var (
	errNoChoices     = errors.New("response contained no choices")
	errRejectedChunk = errors.New("stream chunk rejected by accumulator")
)

// reasoningFields are the delta fields servers use for reasoning: Ollama
// sends "reasoning", vLLM and DeepSeek "reasoning_content".

// streamGuard watches a response body. It notes the [DONE] sentinel, which
// the SDK doesn't report, and cancels the request when no byte arrives for
// the idle timeout. A non-2xx response is read here, before the SDK sees it.
type streamGuard struct {
	cancel  context.CancelCauseFunc
	idleFor time.Duration
	idle    *time.Timer
	body    io.ReadCloser
	tail    []byte
	sawDone bool
	failure *httpFailure
}

func newStreamGuard(idleFor time.Duration, cancel context.CancelCauseFunc) *streamGuard {
	return &streamGuard{cancel: cancel, idleFor: idleFor}
}

// middleware is the request option that passes each response to watch.
func (g *streamGuard) middleware(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
	res, err := next(req)
	if err != nil {
		return res, err
	}
	g.watch(res)
	return res, nil
}

func (g *streamGuard) watch(res *http.Response) {
	if res.StatusCode >= http.StatusMultipleChoices {
		g.recordFailure(res)
		return
	}
	g.body = res.Body
	if g.idleFor > 0 {
		g.idle = time.AfterFunc(g.idleFor, func() { g.cancel(llm.ErrStreamStalled) })
	}
	res.Body = g
}

func (g *streamGuard) recordFailure(res *http.Response) {
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	_ = res.Body.Close()
	g.failure = &httpFailure{status: res.StatusCode, body: string(data), header: res.Header}
	res.Body = io.NopCloser(bytes.NewReader(data))
}

// Read resets the idle timer and notes [DONE] in what it returns.
func (g *streamGuard) Read(p []byte) (int, error) {
	if g.idle != nil {
		g.idle.Reset(g.idleFor)
	}
	n, err := g.body.Read(p)
	g.noteDone(p[:n])
	if err != nil {
		g.stopIdle()
	}
	return n, err
}

// Close stops the idle timer and closes the body.
func (g *streamGuard) Close() error {
	g.stopIdle()
	return g.body.Close()
}

func (g *streamGuard) stopIdle() {
	if g.idle != nil {
		g.idle.Stop()
	}
}

// noteDone looks for [DONE] across read boundaries by keeping the last
// len(doneMarker)-1 bytes.
func (g *streamGuard) noteDone(p []byte) {
	if g.sawDone {
		return
	}
	window := slices.Concat(g.tail, p)
	g.sawDone = bytes.Contains(window, []byte(doneMarker))
	keep := min(len(window), len(doneMarker)-1)
	g.tail = bytes.Clone(window[len(window)-keep:])
}

// accumulated is one whole streamed completion.
type accumulated struct {
	acc       openai.ChatCompletionAccumulator
	reasoning string
	hasUsage  bool
}

// stream sends req as a streamed chat completion and accumulates the answer.
// Errors it returns are classified, except the caller's cancellation and
// stream causes, which classify sorts out.
func (p *Provider) stream(reqCtx context.Context, cancel context.CancelCauseFunc, req llm.Request) (llm.Response, error) {
	guard := newStreamGuard(p.endpoint.IdleTimeout, cancel)
	stream := p.completions.NewStreaming(reqCtx, chatParams(req), requestOptions(req, guard)...)
	defer func() { _ = stream.Close() }()
	if guard.failure != nil {
		return llm.Response{}, guard.failure.classify(req.Model)
	}
	if err := stream.Err(); err != nil {
		return llm.Response{}, err
	}
	got, err := accumulate(stream, guard)
	if err != nil {
		return llm.Response{}, err
	}
	return got.response(), nil
}

func accumulate(stream *ssestream.Stream[openai.ChatCompletionChunk], guard *streamGuard) (accumulated, error) {
	var got accumulated
	var reasoning strings.Builder
	for stream.Next() {
		chunk := stream.Current()
		if !got.acc.AddChunk(chunk) {
			return accumulated{}, errRejectedChunk
		}
		got.hasUsage = got.hasUsage || chunk.JSON.Usage.Valid()
		if len(chunk.Choices) > 0 {
			reasoning.WriteString(reasoningOf(chunk.Choices[0].Delta.JSON.ExtraFields))
		}
	}
	if err := stream.Err(); err != nil {
		return accumulated{}, fmt.Errorf("reading stream: %w: %w", llm.ErrStreamDropped, err)
	}
	if !guard.sawDone {
		return accumulated{}, fmt.Errorf("reading stream: %w", llm.ErrStreamDropped)
	}
	if len(got.acc.Choices) == 0 {
		return accumulated{}, errNoChoices
	}
	got.reasoning = reasoning.String()
	return got, nil
}

func (a *accumulated) response() llm.Response {
	choice := a.acc.Choices[0]
	msg := llm.Message{Role: llm.RoleAssistant, Content: choice.Message.Content, Reasoning: a.reasoning}
	for i := range choice.Message.ToolCalls {
		tc := &choice.Message.ToolCalls[i]
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: tc.Function.Arguments})
	}
	return llm.Response{
		Message:    msg,
		StopReason: stopReason(choice.FinishReason),
		Usage: llm.Usage{
			InputTokens:  int(a.acc.Usage.PromptTokens),
			OutputTokens: int(a.acc.Usage.CompletionTokens),
			Reported:     a.hasUsage,
		},
	}
}

func stopReason(finish string) llm.StopReason {
	switch finish {
	case "tool_calls", "function_call":
		return llm.StopToolCalls
	case "length":
		return llm.StopMaxTokens
	default:
		return llm.StopEnd
	}
}

// reasoningOf returns the first non-empty reasoning field of a delta. The
// SDK marks unknown fields invalid, so they are decoded from their raw JSON.
func reasoningOf(extra map[string]respjson.Field) string {
	for _, name := range []string{"reasoning", "reasoning_content"} {
		var s string
		if err := json.Unmarshal([]byte(extra[name].Raw()), &s); err == nil && s != "" {
			return s
		}
	}
	return ""
}
