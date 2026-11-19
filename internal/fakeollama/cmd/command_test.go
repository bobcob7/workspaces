package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/fakeollama"
)

const validConfig = `
listen: "127.0.0.1:0"
control_listen: "127.0.0.1:0"
models:
  - name: fake-chat
`

var errBoom = errors.New("boom")

// syncBuffer lets the test read output while the command writes it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func execute(ctx context.Context, cmd *cobra.Command, args ...string) error {
	cmd.SetArgs(args)
	return cmd.ExecuteContext(ctx)
}

func mockFactory(srv server) serverFactory {
	return func(fakeollama.Config, *slog.Logger) (server, error) { return srv, nil }
}

func TestNewCommand(t *testing.T) {
	t.Parallel()
	t.Run("serves until the context is cancelled", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cmd := NewCommand(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		var out syncBuffer
		cmd.SetOut(&out)
		errs := make(chan error, 1)
		path := writeConfig(t, validConfig)
		go func() { errs <- execute(ctx, cmd, "--config", path) }()
		apiURL := regexp.MustCompile(`API listening on (\S+)`)
		require.Eventually(t, func() bool { return apiURL.MatchString(out.String()) }, 5*time.Second, 5*time.Millisecond)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.FindStringSubmatch(out.String())[1]+"/v1/models", http.NoBody)
		require.NoError(t, err)
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = res.Body.Close()
		assert.Equal(t, http.StatusOK, res.StatusCode)
		assert.Contains(t, out.String(), "control API listening on http://127.0.0.1:")
		cancel()
		assert.NoError(t, <-errs)
	})
	t.Run("fails for a missing config file", func(t *testing.T) {
		t.Parallel()
		cmd := NewCommand(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		cmd.SetErr(io.Discard)
		err := execute(t.Context(), cmd, "--config", filepath.Join(t.TempDir(), "missing.yaml"))
		assert.ErrorContains(t, err, "loading config")
	})
	t.Run("fails for an invalid config", func(t *testing.T) {
		t.Parallel()
		cmd := NewCommand(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		cmd.SetErr(io.Discard)
		err := execute(t.Context(), cmd, "--config", writeConfig(t, "models: []"))
		assert.ErrorContains(t, err, "configuring server")
	})
	t.Run("rejects arguments", func(t *testing.T) {
		cmd := NewCommand(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		cmd.SetErr(io.Discard)
		cmd.SetOut(io.Discard)
		assert.Error(t, execute(t.Context(), cmd, "extra"))
	})
	t.Run("defaults the config path", func(t *testing.T) {
		assert.Equal(t, "/etc/fake-ollama/config.yaml", NewCommand(slog.New(slog.NewJSONHandler(io.Discard, nil))).Flag("config").DefValue)
	})
}

func TestRun(t *testing.T) {
	t.Parallel()
	t.Run("listen flags override the config", func(t *testing.T) {
		t.Parallel()
		var got fakeollama.Config
		factory := func(cfg fakeollama.Config, _ *slog.Logger) (server, error) {
			got = cfg
			return nil, errBoom
		}
		opts := options{configPath: writeConfig(t, validConfig), listen: "127.0.0.1:1", controlListen: "127.0.0.1:2"}
		require.ErrorIs(t, run(t.Context(), io.Discard, opts, slog.New(slog.NewJSONHandler(io.Discard, nil)), factory), errBoom)
		assert.Equal(t, "127.0.0.1:1", got.Listen)
		assert.Equal(t, "127.0.0.1:2", got.ControlListen)
	})
	t.Run("keeps the config's addresses without flags", func(t *testing.T) {
		t.Parallel()
		var got fakeollama.Config
		factory := func(cfg fakeollama.Config, _ *slog.Logger) (server, error) {
			got = cfg
			return nil, errBoom
		}
		require.ErrorIs(t, run(t.Context(), io.Discard, options{configPath: writeConfig(t, validConfig)}, slog.New(slog.NewJSONHandler(io.Discard, nil)), factory), errBoom)
		assert.Equal(t, "127.0.0.1:0", got.Listen)
	})
	t.Run("reports start failures", func(t *testing.T) {
		t.Parallel()
		srv := &serverMock{StartFunc: func(context.Context) error { return errBoom }}
		err := run(t.Context(), io.Discard, options{configPath: writeConfig(t, validConfig)}, slog.New(slog.NewJSONHandler(io.Discard, nil)), mockFactory(srv))
		require.ErrorIs(t, err, errBoom)
		assert.ErrorContains(t, err, "starting server")
	})
	t.Run("reports shutdown failures", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		srv := &serverMock{
			StartFunc:      func(context.Context) error { return nil },
			URLFunc:        func() string { return "http://api" },
			ControlURLFunc: func() string { return "http://control" },
			ShutdownFunc:   func(context.Context) error { return errBoom },
		}
		var out bytes.Buffer
		err := run(ctx, &out, options{configPath: writeConfig(t, validConfig)}, slog.New(slog.NewJSONHandler(io.Discard, nil)), mockFactory(srv))
		require.ErrorIs(t, err, errBoom)
		assert.Equal(t, "API listening on http://api\ncontrol API listening on http://control\n", out.String())
	})
	t.Run("shuts down with a live context after cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var shutdownErr error
		srv := &serverMock{
			StartFunc:      func(context.Context) error { return nil },
			URLFunc:        func() string { return "" },
			ControlURLFunc: func() string { return "" },
			ShutdownFunc: func(ctx context.Context) error {
				shutdownErr = ctx.Err()
				return nil
			},
		}
		require.NoError(t, run(ctx, io.Discard, options{configPath: writeConfig(t, validConfig)}, slog.New(slog.NewJSONHandler(io.Discard, nil)), mockFactory(srv)))
		assert.NoError(t, shutdownErr)
	})
}
