// Package fakeollama imitates Ollama's OpenAI-compatible API without running
// a model. Replies are computed deterministically from the request, and
// faults can be injected through the Go API or the HTTP control API, which
// listens on a separate port so it keeps working while the API is down.
//
// Copied from llm-router internal/fakeollama @9789c32 (ADR 0067); maintained here.
package fakeollama

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/httpserver"
)

// Request is an API request the server received.
type Request struct {
	Time   time.Time `json:"time"`
	Method string    `json:"method"`
	Path   string    `json:"path"`
	Model  string    `json:"model,omitzero"`
	Stream bool      `json:"stream,omitzero"`
	// Traceparent is the W3C trace context the caller sent, if any.
	Traceparent string         `json:"traceparent,omitzero"`
	Body        jsontext.Value `json:"body,omitzero"`
}

// Server is a fake Ollama server.
type Server struct {
	cfg       Config
	logger    *slog.Logger
	rules     []rule
	created   int64
	api       *apiServer
	control   *httpserver.Server
	closing   chan struct{}
	closeOnce sync.Once
	mu        sync.Mutex
	faults    []Fault
	requests  []Request
}

// New validates cfg and returns a Server. Call Start to begin serving.
func New(cfg Config, logger *slog.Logger) (*Server, error) {
	if err := validateModels(cfg.Models); err != nil {
		return nil, err
	}
	if err := validateTiming(cfg.Timing); err != nil {
		return nil, err
	}
	rules, err := compileRules(cfg.Rules)
	if err != nil {
		return nil, err
	}
	if cfg.Timing.WordsPerChunk == 0 {
		cfg.Timing.WordsPerChunk = 1
	}
	s := &Server{cfg: cfg, logger: logger, rules: rules, created: time.Now().Unix(), closing: make(chan struct{})}
	if err := validateFaults(cfg.Faults, cfg.Models); err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidConfig, err)
	}
	s.faults = slices.Clone(cfg.Faults)
	s.api = &apiServer{handler: s.apiHandler(), logger: logger, down: anyDown(cfg.Faults)}
	return s, nil
}

// Start binds the API and control listeners and serves in the background.
func (s *Server) Start(ctx context.Context) error {
	control, err := httpserver.Listen(ctx, cmp.Or(s.cfg.ControlListen, ephemeralListen), s.controlHandler(), s.logger)
	if err != nil {
		return fmt.Errorf("starting control API: %w", err)
	}
	s.control = control
	if err := s.api.start(ctx, cmp.Or(s.cfg.Listen, ephemeralListen)); err != nil {
		return errors.Join(fmt.Errorf("starting API: %w", err), s.control.Close())
	}
	s.logger.InfoContext(ctx, "fake-ollama started", "api", s.URL(), "control", s.ControlURL())
	return nil
}

// Shutdown stops both listeners and waits for in-flight requests until ctx
// ends. Hung and stalled requests are released immediately.
func (s *Server) Shutdown(ctx context.Context) error {
	s.closeOnce.Do(func() { close(s.closing) })
	err := s.api.shutdown(ctx)
	if s.control != nil {
		err = errors.Join(err, s.control.Shutdown(ctx))
	}
	return err
}

// URL is the API base URL, without the /v1 prefix.
func (s *Server) URL() string {
	return httpserver.URL(s.api.address())
}

// ControlURL is the control API base URL.
func (s *Server) ControlURL() string {
	if s.control == nil {
		return ""
	}
	return s.control.URL()
}

// SetFaults replaces the active faults.
func (s *Server) SetFaults(ctx context.Context, faults ...Fault) error {
	if err := validateFaults(faults, s.cfg.Models); err != nil {
		return err
	}
	s.mu.Lock()
	s.faults = slices.Clone(faults)
	s.mu.Unlock()
	return s.api.setDown(ctx, anyDown(faults))
}

// ClearFaults removes every active fault.
func (s *Server) ClearFaults(ctx context.Context) error {
	return s.SetFaults(ctx)
}

// Faults returns the active faults, with Count holding the remaining count.
func (s *Server) Faults() []Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Fault{}, s.faults...)
}

// Requests returns the API requests received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request{}, s.requests...)
}

// Reset clears faults and recorded requests.
func (s *Server) Reset(ctx context.Context) error {
	s.mu.Lock()
	s.requests = nil
	s.mu.Unlock()
	return s.ClearFaults(ctx)
}

func (s *Server) record(r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r)
}

// takeFault returns the first fault affecting t, using up one of its count.
func (s *Server) takeFault(t target) Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, f := range s.faults {
		if !f.affects(t) {
			continue
		}
		switch {
		case f.Count == 1:
			s.faults = slices.Delete(s.faults, i, i+1)
		case f.Count > 1:
			s.faults[i].Count--
		}
		return f
	}
	return Fault{}
}

func (s *Server) model(name string) (Model, bool) {
	i := slices.IndexFunc(s.cfg.Models, func(m Model) bool { return m.Name == name })
	if i < 0 {
		return Model{}, false
	}
	return s.cfg.Models[i], true
}

// block waits until the client goes away or the server shuts down.
func (s *Server) block(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-s.closing:
	}
}

// sleep waits for d, returning false if the client went away first.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
