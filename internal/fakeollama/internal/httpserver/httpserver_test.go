package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errBoom = errors.New("boom")

func ok() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
}

func get(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	return string(body), err
}

func listen(t *testing.T, handler http.Handler) *Server {
	t.Helper()
	s, err := Listen(t.Context(), "127.0.0.1:0", handler, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestListen(t *testing.T) {
	t.Parallel()
	t.Run("serves the handler", func(t *testing.T) {
		t.Parallel()
		s := listen(t, ok())
		body, err := get(t.Context(), s.URL())
		require.NoError(t, err)
		assert.Equal(t, "ok", body)
	})
	t.Run("rejects bad addresses", func(t *testing.T) {
		t.Parallel()
		_, err := Listen(t.Context(), "bad:address:here", ok(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		assert.ErrorContains(t, err, "listening on bad:address:here")
	})
}

// messageRecorder is a slog.Handler that keeps the message of every record.
type messageRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (*messageRecorder) Enabled(context.Context, slog.Level) bool {
	return true
}

func (r *messageRecorder) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, rec.Message)
	return nil
}

func (r *messageRecorder) WithAttrs([]slog.Attr) slog.Handler {
	return r
}

func (r *messageRecorder) WithGroup(string) slog.Handler {
	return r
}

func (r *messageRecorder) Messages() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messages...)
}

func TestServe(t *testing.T) {
	t.Run("logs serving failures", func(t *testing.T) {
		rec := &messageRecorder{}
		logger := slog.New(rec)
		ln := &listenerMock{
			AcceptFunc: func() (net.Conn, error) { return nil, errBoom },
			AddrFunc:   func() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9} },
			CloseFunc:  func() error { return nil },
		}
		serve(ln, ok(), logger)
		require.Eventually(t, func() bool { return len(rec.Messages()) > 0 }, 5*time.Second, time.Millisecond)
		assert.Equal(t, []string{"server stopped"}, rec.Messages())
	})
}

func TestServer_Addr(t *testing.T) {
	t.Parallel()
	t.Run("resolves the port", func(t *testing.T) {
		t.Parallel()
		_, port, err := net.SplitHostPort(listen(t, ok()).Addr())
		require.NoError(t, err)
		assert.NotEqual(t, "0", port)
	})
}

func TestServer_URL(t *testing.T) {
	t.Parallel()
	t.Run("is an http URL for the address", func(t *testing.T) {
		t.Parallel()
		s := listen(t, ok())
		assert.Equal(t, "http://"+s.Addr(), s.URL())
	})
}

func TestServer_Shutdown(t *testing.T) {
	t.Parallel()
	t.Run("stops serving and frees the address", func(t *testing.T) {
		t.Parallel()
		s := listen(t, ok())
		require.NoError(t, s.Shutdown(t.Context()))
		again, err := Listen(t.Context(), s.Addr(), ok(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		require.NoError(t, err)
		assert.NoError(t, again.Close())
	})
	t.Run("waits for in-flight requests", func(t *testing.T) {
		t.Parallel()
		started, release := make(chan struct{}), make(chan struct{})
		s := listen(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			close(started)
			<-release
			_, _ = io.WriteString(w, "finished")
		}))
		result := make(chan string, 1)
		go func() {
			body, _ := get(context.WithoutCancel(t.Context()), s.URL())
			result <- body
		}()
		<-started
		shutdown := make(chan error, 1)
		go func() { shutdown <- s.Shutdown(t.Context()) }()
		close(release)
		require.NoError(t, <-shutdown)
		assert.Equal(t, "finished", <-result)
	})
}

func TestServer_Close(t *testing.T) {
	t.Parallel()
	t.Run("drops open connections", func(t *testing.T) {
		t.Parallel()
		started := make(chan struct{})
		s := listen(t, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
		}))
		errs := make(chan error, 1)
		go func() {
			_, err := get(t.Context(), s.URL())
			errs <- err
		}()
		<-started
		require.NoError(t, s.Close())
		assert.Error(t, <-errs)
	})
	t.Run("reports listener close failures", func(t *testing.T) {
		ln := &listenerMock{
			AcceptFunc: func() (net.Conn, error) { return nil, net.ErrClosed },
			AddrFunc:   func() net.Addr { return &net.TCPAddr{} },
			CloseFunc:  func() error { return errBoom },
		}
		s := serve(ln, ok(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
		assert.ErrorIs(t, s.Close(), errBoom)
	})
}

func TestURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"[::]:11434", "http://127.0.0.1:11434"},
		{"0.0.0.0:11434", "http://127.0.0.1:11434"},
		{":11434", "http://127.0.0.1:11434"},
		{"[::1]:9", "http://[::1]:9"},
		{"not-an-address", "http://not-an-address"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			assert.Equal(t, tt.want, URL(tt.addr))
		})
	}
}

func TestWaitAndShutdown(t *testing.T) {
	t.Run("shuts down with a live context once ctx ends", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var shutdownErr error
		var deadline bool
		err := WaitAndShutdown(ctx, time.Minute, func(ctx context.Context) error {
			shutdownErr = ctx.Err()
			_, deadline = ctx.Deadline()
			return nil
		})
		require.NoError(t, err)
		require.NoError(t, shutdownErr)
		assert.True(t, deadline)
	})
	t.Run("returns the shutdown error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := WaitAndShutdown(ctx, time.Minute, func(context.Context) error { return errBoom })
		assert.ErrorIs(t, err, errBoom)
	})
}
