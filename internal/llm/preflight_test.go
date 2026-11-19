package llm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreflight(t *testing.T) {
	t.Parallel()
	ep := Endpoint{Name: "gpu", URL: "http://gpu.example:11434"}
	const model = "qwen/qwen3.5-9b"
	served := []string{"qwen3.5:9b", model}
	withTools := ModelInfo{Version: "0.9.0", Capabilities: []string{"completion", "tools"}}

	t.Run("models unreachable", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("connection refused")
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return nil, cause },
		}
		_, err := Preflight(t.Context(), p, ep, model, 65536)
		require.ErrorIs(t, err, ErrUnreachable)
		require.ErrorIs(t, err, cause)
		var unreachable *UnreachableError
		require.ErrorAs(t, err, &unreachable)
		assert.Equal(t, ep.URL, unreachable.URL)
	})

	t.Run("model not listed", func(t *testing.T) {
		t.Parallel()
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return []string{"other"}, nil },
		}
		_, err := Preflight(t.Context(), p, ep, model, 65536)
		require.ErrorIs(t, err, ErrModelMissing)
		var missing *ModelMissingError
		require.ErrorAs(t, err, &missing)
		assert.Equal(t, []string{"other"}, missing.Models)
	})

	t.Run("probe without tools", func(t *testing.T) {
		t.Parallel()
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return served, nil },
			ProbeFunc: func(context.Context, string) (ModelInfo, error) {
				return ModelInfo{Capabilities: []string{"completion"}}, nil
			},
		}
		_, err := Preflight(t.Context(), p, ep, model, 65536)
		require.ErrorIs(t, err, ErrNoTools)
		var noTools *NoToolsError
		require.ErrorAs(t, err, &noTools)
		assert.Equal(t, []string{"completion"}, noTools.Capabilities)
	})

	t.Run("probe unsupported", func(t *testing.T) {
		t.Parallel()
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return served, nil },
			ProbeFunc: func(context.Context, string) (ModelInfo, error) {
				return ModelInfo{}, ErrProbeUnsupported
			},
		}
		result, err := Preflight(t.Context(), p, ep, model, 65536)
		require.NoError(t, err)
		assert.Equal(t, served, result.Models)
		assert.Nil(t, result.Capabilities)
		assert.Empty(t, result.Version)
		assert.Equal(t, 65536, result.ContextTokens)
		assert.Empty(t, result.Warnings)
	})

	t.Run("ok with probe", func(t *testing.T) {
		t.Parallel()
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return served, nil },
			ProbeFunc: func(context.Context, string) (ModelInfo, error) {
				return withTools, nil
			},
		}
		result, err := Preflight(t.Context(), p, ep, model, 32768)
		require.NoError(t, err)
		assert.Equal(t, "0.9.0", result.Version)
		assert.Equal(t, withTools.Capabilities, result.Capabilities)
		assert.Equal(t, 32768, result.ContextTokens)
		assert.Empty(t, result.Warnings)
	})

	t.Run("smaller model context lowers context tokens", func(t *testing.T) {
		t.Parallel()
		info := withTools
		info.ContextLength = 32768
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return served, nil },
			ProbeFunc: func(context.Context, string) (ModelInfo, error) {
				return info, nil
			},
		}
		result, err := Preflight(t.Context(), p, ep, model, 65536)
		require.NoError(t, err)
		assert.Equal(t, 32768, result.ContextTokens)
		assert.Len(t, result.Warnings, 1)
	})

	t.Run("each call has a deadline within 10 s", func(t *testing.T) {
		t.Parallel()
		var deadlines []time.Time
		check := func(ctx context.Context) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok)
			deadlines = append(deadlines, deadline)
		}
		p := &ProviderMock{
			ModelsFunc: func(ctx context.Context) ([]string, error) {
				check(ctx)
				return served, nil
			},
			ProbeFunc: func(ctx context.Context, _ string) (ModelInfo, error) {
				check(ctx)
				return withTools, nil
			},
		}
		_, err := Preflight(t.Context(), p, ep, model, 65536)
		require.NoError(t, err)
		require.Len(t, deadlines, 2)
		for _, deadline := range deadlines {
			assert.LessOrEqual(t, time.Until(deadline), 10*time.Second)
		}
	})
}

func TestPreflightEndpoint(t *testing.T) {
	t.Parallel()
	ep := Endpoint{Name: "gpu", URL: "http://gpu.example:11434"}
	t.Run("lists the models without choosing one", func(t *testing.T) {
		t.Parallel()
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return []string{"a", "b"}, nil },
		}
		result, err := PreflightEndpoint(t.Context(), p, ep)
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b"}, result.Models)
		assert.Zero(t, result.ContextTokens)
		assert.Nil(t, result.Capabilities)
		assert.Empty(t, p.ProbeCalls())
	})
	t.Run("an endpoint that doesn't answer is unreachable", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("connection refused")
		p := &ProviderMock{
			ModelsFunc: func(context.Context) ([]string, error) { return nil, cause },
		}
		_, err := PreflightEndpoint(t.Context(), p, ep)
		require.ErrorIs(t, err, ErrUnreachable)
		require.ErrorIs(t, err, cause)
	})
}
