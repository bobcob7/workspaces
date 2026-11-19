package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestRun_retry(t *testing.T) {
	t.Parallel()
	t.Run("sends a fourth request after three retries, then ends endpoint_down when it fails", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), fail(retryable()), fail(retryable()))
		clk := advancingClock()
		var waits []time.Duration
		clk.SleepFunc = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			return nil
		}
		out, _ := runWithTranscript(t, testConfig(chat), clk, nil)
		assert.Equal(t, ExitEndpointDown, out.Exit)
		assert.Equal(t, []time.Duration{2 * time.Second, 8 * time.Second, 30 * time.Second}, waits)
		assert.Len(t, chat.ChatCalls(), 4)
	})
	t.Run("recovers after two retryable errors", func(t *testing.T) {
		chat := scripted(t, fail(retryable()), fail(retryable()), reply(call("1", "record_result", `{"verdict":"PASS","report":"ok"}`)))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitDone, out.Exit)
	})
	t.Run("ends model_no_tools and names the endpoint", func(t *testing.T) {
		chat := scripted(t, fail(&llm.NoToolsError{Model: "qwen/qwen3.5-9b"}))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitModelNoTools, out.Exit)
		assert.Contains(t, out.Message, "gpu:")
	})
	t.Run("ends model_missing when the endpoint doesn't serve the model", func(t *testing.T) {
		chat := scripted(t, fail(&llm.ModelMissingError{Model: "qwen/qwen3.5-9b"}))
		out, _ := runWithTranscript(t, testConfig(chat), advancingClock(), nil)
		assert.Equal(t, ExitModelMissing, out.Exit)
	})
	t.Run("ends stopped when the context is cancelled during a request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		chat := scripted(t, fail(context.Canceled))
		path := t.TempDir() + "/transcript.jsonl"
		tr, err := OpenTranscript(path, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tr.Close() })
		out := New(advancingClock(), tr, testConfig(chat), discardLog()).Run(ctx, nil, 0, nil)
		assert.Equal(t, ExitStopped, out.Exit)
	})
}
