// Package httpserver runs HTTP servers with graceful shutdown.
//
// Copied from llm-router internal/httpserver @9789c32 (ADR 0067); maintained here.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const readHeaderTimeout = 10 * time.Second

// Server serves a handler on a TCP listener.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Listen binds addr and serves handler on it in the background. Serving
// errors other than a normal shutdown are logged.
func Listen(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) (*Server, error) {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", addr, err)
	}
	return serve(ln, handler, logger), nil
}

func serve(ln net.Listener, handler http.Handler, logger *slog.Logger) *Server {
	s := &Server{srv: &http.Server{Handler: handler, ReadHeaderTimeout: readHeaderTimeout}, ln: ln}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "addr", s.Addr(), "error", err)
		}
	}()
	return s
}

// Addr is the address the server listens on, with any port resolved.
func (s *Server) Addr() string {
	return s.ln.Addr().String()
}

// URL is the base URL clients can reach the server at. See URL.
func (s *Server) URL() string {
	return URL(s.Addr())
}

// Shutdown stops accepting connections and waits for in-flight requests
// until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.closeListener(s.srv.Shutdown(ctx))
}

// Close stops immediately, dropping every open connection.
func (s *Server) Close() error {
	return s.closeListener(s.srv.Close())
}

// closeListener frees the address. The server closes the listener itself
// once serving, but Serve may not have started yet.
func (s *Server) closeListener(err error) error {
	if lnErr := s.ln.Close(); lnErr != nil && !errors.Is(lnErr, net.ErrClosed) {
		err = errors.Join(err, lnErr)
	}
	return err
}

// URL turns a listener address into an http URL, using the loopback address
// for unspecified hosts.
func URL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// WaitAndShutdown blocks until ctx ends, then calls shutdown with a context
// that is not cancelled with ctx but expires after timeout.
func WaitAndShutdown(ctx context.Context, timeout time.Duration, shutdown func(context.Context) error) error {
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return shutdown(shutdownCtx)
}
