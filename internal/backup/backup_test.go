package backup

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errTest = errors.New("boom")

func newLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestExclude(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("would start a real tmutil; startExclusion is tested with a fake")
	}
	t.Run("returns at once off macOS", func(t *testing.T) {
		Exclude(slog.New(slog.NewJSONHandler(io.Discard, nil)), t.TempDir())
	})
}

func TestStartExclusion(t *testing.T) {
	t.Run("does nothing off macOS", func(t *testing.T) {
		started := false
		start := func(*exec.Cmd) (func() error, error) {
			started = true
			return func() error { return nil }, nil
		}
		<-startExclusion(slog.New(slog.NewJSONHandler(io.Discard, nil)), "linux", start, "/d")
		assert.False(t, started)
	})
	t.Run("starts tmutil detached", func(t *testing.T) {
		var got *exec.Cmd
		start := func(cmd *exec.Cmd) (func() error, error) {
			got = cmd
			return func() error { return nil }, nil
		}
		<-startExclusion(slog.New(slog.NewJSONHandler(io.Discard, nil)), "darwin", start, "/d")
		require.NotNil(t, got)
		assert.Equal(t, []string{"tmutil", "addexclusion", "/d"}, got.Args)
		require.NotNil(t, got.SysProcAttr)
		assert.True(t, got.SysProcAttr.Setpgid)
		assert.Nil(t, got.Stdin)
		assert.Nil(t, got.Stdout)
		assert.Nil(t, got.Stderr)
	})
	t.Run("logs a failed start at debug level", func(t *testing.T) {
		var logs bytes.Buffer
		start := func(*exec.Cmd) (func() error, error) { return nil, errTest }
		<-startExclusion(newLogger(&logs), "darwin", start, "/d")
		assert.Contains(t, logs.String(), `"level":"DEBUG"`)
		assert.Contains(t, logs.String(), "couldn't start tmutil")
	})
	t.Run("logs a failure once tmutil finishes", func(t *testing.T) {
		var logs bytes.Buffer
		start := func(*exec.Cmd) (func() error, error) {
			return func() error { return errTest }, nil
		}
		<-startExclusion(newLogger(&logs), "darwin", start, "/d")
		assert.Contains(t, logs.String(), `"level":"WARN"`)
		assert.Contains(t, logs.String(), "boom")
	})
	t.Run("logs success at debug level", func(t *testing.T) {
		var logs bytes.Buffer
		start := func(*exec.Cmd) (func() error, error) {
			return func() error { return nil }, nil
		}
		<-startExclusion(newLogger(&logs), "darwin", start, "/d")
		assert.Contains(t, logs.String(), "excluded from Time Machine backups")
	})
}

func TestStartCmd(t *testing.T) {
	t.Parallel()
	t.Run("starts and waits", func(t *testing.T) {
		t.Parallel()
		wait, err := startCmd(exec.CommandContext(t.Context(), "true"))
		require.NoError(t, err)
		assert.NoError(t, wait())
	})
	t.Run("reports the exit status", func(t *testing.T) {
		t.Parallel()
		wait, err := startCmd(exec.CommandContext(t.Context(), "false"))
		require.NoError(t, err)
		assert.Error(t, wait())
	})
	t.Run("missing command", func(t *testing.T) {
		t.Parallel()
		_, err := startCmd(exec.CommandContext(t.Context(), "ws-test-no-such-command"))
		assert.Error(t, err)
	})
}
