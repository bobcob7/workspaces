package openai

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/llm"
)

func TestStreamGuard_Read(t *testing.T) {
	t.Parallel()
	t.Run("notes [DONE] split across reads", func(t *testing.T) {
		t.Parallel()
		_, cancel := context.WithCancelCause(t.Context())
		g := newStreamGuard(0, cancel)
		g.body = io.NopCloser(iotest.OneByteReader(strings.NewReader("data: {}\n\ndata: [DONE]\n\n")))
		data, err := io.ReadAll(g)
		require.NoError(t, err)
		assert.Contains(t, string(data), "[DONE]")
		assert.True(t, g.sawDone)
	})
	t.Run("does not see [DONE] that was never sent", func(t *testing.T) {
		t.Parallel()
		_, cancel := context.WithCancelCause(t.Context())
		g := newStreamGuard(0, cancel)
		g.body = io.NopCloser(strings.NewReader("data: {\"choices\":[]}\n\n"))
		_, err := io.ReadAll(g)
		require.NoError(t, err)
		assert.False(t, g.sawDone)
	})
}

func TestStopReason(t *testing.T) {
	t.Parallel()
	cases := map[string]llm.StopReason{
		"stop":          llm.StopEnd,
		"tool_calls":    llm.StopToolCalls,
		"function_call": llm.StopToolCalls,
		"length":        llm.StopMaxTokens,
		"":              llm.StopEnd,
	}
	for finish, want := range cases {
		assert.Equal(t, want, stopReason(finish), finish)
	}
}
