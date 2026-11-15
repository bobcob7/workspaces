package fakeollama

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/httpserver"
)

// apiServer serves the API on a listener that can be closed and reopened on
// the same address, so the down fault refuses connections like a dead
// process would.
type apiServer struct {
	handler http.Handler
	logger  *slog.Logger
	mu      sync.Mutex
	addr    string
	srv     *httpserver.Server
	down    bool
	closed  bool
}

func (a *apiServer) start(ctx context.Context, addr string) error {
	srv, err := httpserver.Listen(ctx, addr, a.handler, a.logger)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.addr = srv.Addr()
	if a.down {
		return srv.Close()
	}
	a.srv = srv
	return nil
}

// setDown closes or reopens the listener. Closing also drops every open
// connection, including ones with requests in progress.
func (a *apiServer) setDown(ctx context.Context, down bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.down == down {
		return nil
	}
	a.down = down
	if a.addr == "" {
		return nil
	}
	if down {
		a.logger.InfoContext(ctx, "API going down", "addr", a.addr)
		srv := a.srv
		a.srv = nil
		return srv.Close()
	}
	srv, err := httpserver.Listen(ctx, a.addr, a.handler, a.logger)
	if err != nil {
		a.down = true
		return err
	}
	a.logger.InfoContext(ctx, "API back up", "addr", a.addr)
	a.srv = srv
	return nil
}

func (a *apiServer) address() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.addr
}

func (a *apiServer) shutdown(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.srv == nil {
		return nil
	}
	srv := a.srv
	a.srv = nil
	return srv.Shutdown(ctx)
}
