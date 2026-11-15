package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestAgent_Reresolve(t *testing.T) {
	t.Parallel()
	recordDone := reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`))
	t.Run("switches to the new endpoint for the retry and the rest", func(t *testing.T) {
		chatA := scripted(t, fail(retryable()))
		chatB := scripted(t, recordDone)
		targetB := testTarget(chatB)
		targetB.Endpoint = "gpu-b"
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetB, Changed: true, Changes: []string{"endpoint", "url"}}
		}}
		var switches []string
		cfg := testConfig(chatA)
		cfg.Reresolver = resolver
		var last Status
		cfg.OnStatus = func(s Status) { last = s }
		cfg.OnSwitch = func(from, to Target, endpoint bool, reason string, _ int) {
			switches = append(switches, from.Endpoint+">"+to.Endpoint+":"+reason)
			assert.True(t, endpoint)
		}
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, []string{"gpu>gpu-b:retry"}, switches)
		assert.Equal(t, "gpu-b", last.Endpoint)
		assert.Equal(t, 1, last.EndpointChanges)
		assert.Len(t, chatB.ChatCalls(), 1)
		require.Len(t, out.Endpoints, 2)
		assert.Equal(t, "gpu-b", out.Endpoints[1].Endpoint)
		changed := eventsOf(events, kindEndpointChanged)
		require.Len(t, changed, 1)
		payload := payloadOf[*EndpointChangedEvent](t, changed[0])
		assert.Equal(t, "retry", payload.Reason)
		assert.Equal(t, "gpu", payload.FromEndpoint)
		assert.Equal(t, "gpu-b", payload.ToEndpoint)
		assert.Equal(t, []string{"endpoint", "url"}, payload.Changed)
		assert.Nil(t, payload.Version)
	})
	t.Run("a settings-only change is recorded but is not an endpoint switch", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), recordDone)
		targetTemp := testTarget(chat)
		targetTemp.Temperature = 0.5
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetTemp, Changed: true, Changes: []string{"temperature"}}
		}}
		var switches []bool
		cfg := testConfig(chat)
		cfg.Reresolver = resolver
		cfg.OnSwitch = func(_, to Target, endpoint bool, reason string, _ int) {
			switches = append(switches, endpoint)
			assert.InDelta(t, 0.5, to.Temperature, 1e-9)
			assert.Equal(t, "retry", reason)
		}
		var last Status
		cfg.OnStatus = func(s Status) { last = s }
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, []bool{false}, switches)
		assert.Equal(t, 0, last.EndpointChanges)
		require.Len(t, out.Endpoints, 1)
		assert.Equal(t, "gpu", out.Endpoints[0].Endpoint)
		changed := eventsOf(events, kindEndpointChanged)
		require.Len(t, changed, 1)
		assert.Equal(t, []string{"temperature"}, payloadOf[*EndpointChangedEvent](t, changed[0]).Changed)
		assert.Equal(t, "gpu", payloadOf[*EndpointChangedEvent](t, changed[0]).ToEndpoint)
	})
	t.Run("counts the failed attempt on the endpoint it was sent to, not the one switched to", func(t *testing.T) {
		chatA := scripted(t, fail(retryable()))
		chatB := scripted(t, recordDone)
		targetB := testTarget(chatB)
		targetB.Endpoint = "gpu-b"
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetB, Changed: true, Changes: []string{"endpoint"}}
		}}
		cfg := testConfig(chatA)
		cfg.Reresolver = resolver
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, 2, out.Usage.Requests)
		require.Len(t, out.Endpoints, 2)
		assert.Equal(t, EndpointUsage{Endpoint: "gpu", Model: "qwen/qwen3.5-9b", Usage: Usage{Requests: 1}}, out.Endpoints[0])
		assert.Equal(t, EndpointUsage{Endpoint: "gpu-b", Model: "qwen/qwen3.5-9b", Usage: Usage{Requests: 1}}, out.Endpoints[1])
	})
	t.Run("calls the sleep before each re-resolution", func(t *testing.T) {
		var order []string
		clk := advancingClock()
		clk.SleepFunc = func(context.Context, time.Duration) error {
			order = append(order, "sleep")
			return nil
		}
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			order = append(order, "reresolve")
			return Resolution{}
		}}
		cfg := testConfig(scripted(t, fail(retryable()), recordDone))
		cfg.Reresolver = resolver
		out, _ := runWithTranscript(t, cfg, clk, nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Equal(t, []string{"sleep", "reresolve"}, order)
	})
	t.Run("ends endpoint_rejected after three rejected switches", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), fail(retryable()), fail(retryable()))
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Rejected: &Rejection{Endpoint: "gpu-b", Model: "qwen3.5:9b", Code: "model_not_served", Message: "not listed"}}
		}}
		var rejects []string
		cfg := testConfig(chat)
		cfg.Reresolver = resolver
		cfg.OnReject = func(r Rejection, reason string) { rejects = append(rejects, r.Code+":"+reason) }
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitEndpointRejected, out.Exit)
		assert.Equal(t, "gpu-b: model_not_served: not listed", out.Message)
		assert.Len(t, eventsOf(events, kindEndpointRejected), 3)
		assert.Equal(t, []string{"model_not_served:retry", "model_not_served:retry", "model_not_served:retry"}, rejects)
	})
	t.Run("ends endpoint_down when an unchanged answer follows the failures", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), fail(retryable()), fail(retryable()))
		cfg := testConfig(chat)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution { return Resolution{} }}
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitEndpointDown, out.Exit)
	})
	t.Run("spends a retry on a new endpoint that fails the check", func(t *testing.T) {
		chat := scripted(t, fail(retryable()))
		clk := advancingClock()
		var waits []time.Duration
		clk.SleepFunc = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}
		checkErr := errors.New("probe: connection refused")
		cfg := testConfig(chat)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Failed: checkErr}
		}}
		out, events := runWithTranscript(t, cfg, clk, nil)
		assert.Equal(t, ExitEndpointDown, out.Exit)
		assert.Equal(t, []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}, waits)
		assert.Len(t, chat.ChatCalls(), 1)
		retries := eventsOf(events, kindRetry)
		require.NotEmpty(t, retries)
		assert.Contains(t, payloadOf[*RetryEvent](t, retries[len(retries)-1]).Error, "connection refused")
	})
	t.Run("keeps the endpoint and records a warning when the configuration is unusable", func(t *testing.T) {
		chatA := scripted(t, fail(retryable()), recordDone)
		chatB := scripted(t)
		cfg := testConfig(chatA)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Warning: &ConfigWarning{Code: "config_invalid", Message: "endpoints.gpu.url"}}
		}}
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Len(t, chatA.ChatCalls(), 2)
		assert.Empty(t, chatB.ChatCalls())
		warnings := eventsOf(events, kindConfigWarning)
		require.Len(t, warnings, 1)
		assert.Equal(t, "config_invalid", payloadOf[*ConfigWarningEvent](t, warnings[0]).Code)
	})
	t.Run("keeps the old target when the conversation does not fit the new window", func(t *testing.T) {
		chatA := scripted(t, fail(retryable()), recordDone)
		chatB := scripted(t)
		targetB := testTarget(chatB)
		targetB.Endpoint = "small"
		targetB.ContextTokens = 4000
		targetB.MaxOutput = 1000
		cfg := testConfig(chatA)
		cfg.Task = strings.Repeat("x", 10000)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetB, Changed: true}
		}}
		out, events := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Empty(t, chatB.ChatCalls())
		rejected := eventsOf(events, kindEndpointRejected)
		require.Len(t, rejected, 1)
		assert.Equal(t, "context_too_small", payloadOf[*EndpointRejectedEvent](t, rejected[0]).Code)
		assert.Equal(t, "gpu", payloadOf[*EndpointRejectedEvent](t, rejected[0]).KeptEndpoint)
	})
	t.Run("drops reasoning the old provider needs when the api changes", func(t *testing.T) {
		opaque := &llm.Opaque{Provider: "openai", Data: []byte("sealed")}
		chatA := scripted(t, step{resp: llm.Response{
			Message:    llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{call("1", "run", `{"command":"true"}`)}, ReasoningOpaque: opaque},
			StopReason: llm.StopToolCalls,
		}}, fail(retryable()))
		chatB := scripted(t, recordDone)
		targetB := testTarget(chatB)
		targetB.Endpoint = "ollama"
		targetB.API = "ollama"
		cfg := testConfig(chatA)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetB, Changed: true}
		}}
		out, _ := runWithTranscript(t, cfg, advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
		for _, m := range chatB.ChatCalls()[0].Req.Messages {
			assert.Nil(t, m.ReasoningOpaque)
		}
	})
	t.Run("re-resolves once before the first request of a resume", func(t *testing.T) {
		chatA := scripted(t)
		chatB := scripted(t, recordDone)
		targetB := testTarget(chatB)
		targetB.Endpoint = "gpu-b"
		resolver := &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Target: targetB, Changed: true}
		}}
		cfg := testConfig(chatA)
		cfg.Reresolver = resolver
		history := []llm.Message{{Role: llm.RoleUser, Content: "fix it"}}
		out, events := runWithTranscript(t, cfg, advancingClock(), history)
		assert.Equal(t, ExitDone, out.Exit)
		assert.Len(t, resolver.ReresolveCalls(), 1)
		assert.Empty(t, chatA.ChatCalls())
		changed := eventsOf(events, kindEndpointChanged)
		require.Len(t, changed, 1)
		assert.Equal(t, "resume", payloadOf[*EndpointChangedEvent](t, changed[0]).Reason)
		assert.Nil(t, payloadOf[*EndpointChangedEvent](t, changed[0]).Attempt)
	})
	t.Run("keeps the endpoint on resume when the new one is unreachable", func(t *testing.T) {
		chat := scripted(t, recordDone)
		cfg := testConfig(chat)
		cfg.Reresolver = &reresolverMock{ReresolveFunc: func(context.Context, Target) Resolution {
			return Resolution{Failed: errors.New("connection refused")}
		}}
		history := []llm.Message{{Role: llm.RoleUser, Content: "fix it"}}
		out, events := runWithTranscript(t, cfg, advancingClock(), history)
		assert.Equal(t, ExitDone, out.Exit)
		rejected := eventsOf(events, kindEndpointRejected)
		require.Len(t, rejected, 1)
		assert.Equal(t, "endpoint_unreachable", payloadOf[*EndpointRejectedEvent](t, rejected[0]).Code)
	})
}

func TestSwitchesEndpoint(t *testing.T) {
	t.Run("an endpoint, url, api or id change is a switch", func(t *testing.T) {
		assert.True(t, SwitchesEndpoint([]string{"endpoint", "url"}))
		assert.True(t, SwitchesEndpoint([]string{"api"}))
		assert.True(t, SwitchesEndpoint([]string{"id"}))
	})
	t.Run("settings alone are not a switch", func(t *testing.T) {
		assert.False(t, SwitchesEndpoint(nil))
		assert.False(t, SwitchesEndpoint([]string{"context_tokens", "max_output", "temperature", "reasoning", "request_timeout", "idle_timeout"}))
	})
}
