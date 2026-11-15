package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/gate"
)

func TestRunCommand(t *testing.T) {
	t.Parallel()
	t.Run("runs through the shell in the session directory with the default timeout", func(t *testing.T) {
		cfg := testConfig(t)
		var got gate.Request
		cfg.Shell = &shellRunnerMock{RunFunc: func(_ context.Context, req gate.Request) (gate.Result, error) {
			got = req
			return gate.Result{ExitCode: 0, Stdout: "hi"}, nil
		}}
		res, err := runCommand(t.Context(), &cfg, runArgs{Command: "echo hi"})
		require.NoError(t, err)
		assert.Equal(t, "echo hi", got.Script)
		assert.Equal(t, cfg.Dir, got.Dir)
		assert.Equal(t, runDefaultTimeout, got.Timeout)
		assert.Equal(t, "exit 0\n--- stdout ---\nhi\n--- stderr ---\n", res.Text)
	})
	t.Run("caps the timeout at 900 seconds", func(t *testing.T) {
		cfg := testConfig(t)
		var got time.Duration
		cfg.Shell = &shellRunnerMock{RunFunc: func(_ context.Context, req gate.Request) (gate.Result, error) {
			got = req.Timeout
			return gate.Result{}, nil
		}}
		_, err := runCommand(t.Context(), &cfg, runArgs{Command: "true", TimeoutSeconds: 5000})
		require.NoError(t, err)
		assert.Equal(t, runMaxTimeout, got)
	})
	t.Run("reports a timeout as a result", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.Shell = &shellRunnerMock{RunFunc: func(context.Context, gate.Request) (gate.Result, error) {
			return gate.Result{}, gate.ErrTimedOut
		}}
		res, err := runCommand(t.Context(), &cfg, runArgs{Command: "sleep 9", TimeoutSeconds: 3})
		require.NoError(t, err)
		assert.Equal(t, "timed out after 3s", res.Text)
	})
	t.Run("refuses a command the guard blocks without running it", func(t *testing.T) {
		cfg := testConfig(t)
		shell := &shellRunnerMock{}
		cfg.Shell = shell
		_, err := runCommand(t.Context(), &cfg, runArgs{Command: "ws list"})
		require.ErrorContains(t, err, "command refused")
		assert.Empty(t, shell.RunCalls())
	})
	t.Run("returns a shell failure", func(t *testing.T) {
		cfg := testConfig(t)
		cfg.Shell = &shellRunnerMock{RunFunc: func(context.Context, gate.Request) (gate.Result, error) {
			return gate.Result{}, errors.New("boom")
		}}
		_, err := runCommand(t.Context(), &cfg, runArgs{Command: "true"})
		require.EqualError(t, err, "boom")
	})
	t.Run("requires a command", func(t *testing.T) {
		_, err := runCommand(t.Context(), new(testConfig(t)), runArgs{})
		require.EqualError(t, err, "missing command")
	})
	t.Run("runs the real gate runner", func(t *testing.T) {
		t.Parallel()
		cfg := testConfig(t)
		cfg.Shell = gate.New(Environ([]string{"PATH=/usr/bin:/bin"}, false, nil))
		res, err := runCommand(t.Context(), &cfg, runArgs{Command: "echo hi"})
		require.NoError(t, err)
		assert.Contains(t, res.Text, "exit 0\n--- stdout ---\nhi")
	})
}

func TestRunTimeout(t *testing.T) {
	assert.Equal(t, runDefaultTimeout, runTimeout(0))
	assert.Equal(t, 7*time.Second, runTimeout(7))
	assert.Equal(t, runMaxTimeout, runTimeout(901))
}

func TestOutputTail(t *testing.T) {
	t.Run("keeps short output", func(t *testing.T) {
		assert.Equal(t, "short", outputTail("short", false))
	})
	t.Run("keeps the last 8 KiB of long output", func(t *testing.T) {
		got := outputTail(strings.Repeat("a", runOutputTail)+"END", false)
		assert.True(t, strings.HasPrefix(got, "[truncated]\n"))
		assert.True(t, strings.HasSuffix(got, "END"))
	})
	t.Run("marks output the gate already cut", func(t *testing.T) {
		assert.Equal(t, "[truncated]\nx", outputTail("x", true))
	})
}
