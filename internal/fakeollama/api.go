package fakeollama

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
	"github.com/bobcob7/workspaces/internal/fakeollama/internal/sse"
)

const (
	injectedFaultMessage = "fake-ollama: injected fault"
	fakeVersion          = "0.0.0-fake"
)

var (
	errNoMessages = errors.New("[] is too short - 'messages'")
	errNoModel    = errors.New("model is required")
)

// showRequest is the body of POST /api/show.
type showRequest struct {
	Model string `json:"model"`
}

// ollamaTags is the body of GET /api/tags.
type ollamaTags struct {
	Models []ollamaTag `json:"models"`
}

type ollamaTag struct {
	Name    string        `json:"name"`
	Model   string        `json:"model"`
	Size    int           `json:"size"`
	Details ollamaDetails `json:"details"`
}

type ollamaDetails struct {
	Family string `json:"family"`
}

// ollamaShow is the body of POST /api/show.
type ollamaShow struct {
	Capabilities []string       `json:"capabilities"`
	ModelInfo    map[string]int `json:"model_info"`
	Details      ollamaDetails  `json:"details"`
}

func (s *Server) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /api/version", s.handleVersion)
	mux.HandleFunc("GET /api/tags", s.handleTags)
	mux.HandleFunc("POST /api/show", s.handleShow)
	return mux
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	s.record(newRequest(r))
	if !s.applyFault(r.Context(), w, s.takeFault(target{})) {
		return
	}
	list := openaiwire.ModelList{Object: openaiwire.ObjectList, Data: make([]openaiwire.Model, 0, len(s.cfg.Models))}
	for _, m := range s.cfg.Models {
		list.Data = append(list.Data, openaiwire.Model{ID: m.Name, Object: openaiwire.ObjectModel, Created: s.created, OwnedBy: ownedBy(m.Name)})
	}
	openaiwire.WriteJSON(w, http.StatusOK, list)
}

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	s.record(newRequest(r))
	if !s.applyFault(r.Context(), w, s.takeFault(target{})) {
		return
	}
	openaiwire.WriteJSON(w, http.StatusOK, map[string]string{"version": fakeVersion})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	s.record(newRequest(r))
	if !s.applyFault(r.Context(), w, s.takeFault(target{})) {
		return
	}
	tags := ollamaTags{Models: make([]ollamaTag, 0, len(s.cfg.Models))}
	for _, m := range s.cfg.Models {
		tags.Models = append(tags.Models, ollamaTag{Name: m.Name, Model: m.Name, Details: ollamaDetails{Family: m.family()}})
	}
	openaiwire.WriteJSON(w, http.StatusOK, tags)
}

// handleShow applies faults before validating the request, as handleChat does.
func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	body, req, parseErr := readShowRequest(r)
	rec := newRequest(r)
	rec.Model = req.Model
	if jsontext.Value(body).IsValid() {
		rec.Body = body
	}
	s.record(rec)
	if !s.applyFault(r.Context(), w, s.takeFault(target{model: req.Model})) {
		return
	}
	if parseErr != nil {
		writeOllamaError(w, http.StatusBadRequest, parseErr.Error())
		return
	}
	m, ok := s.model(req.Model)
	if !ok {
		writeOllamaError(w, http.StatusNotFound, fmt.Sprintf("model '%s' not found", req.Model))
		return
	}
	openaiwire.WriteJSON(w, http.StatusOK, showFor(m))
}

func readShowRequest(r *http.Request) ([]byte, showRequest, error) {
	var req showRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return body, req, fmt.Errorf("reading body: %w", err)
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return body, req, fmt.Errorf("decoding body: %w", err)
	}
	return body, req, nil
}

// showFor describes m as Ollama's /api/show does.
func showFor(m Model) ollamaShow {
	capabilities := []string{"completion"}
	if m.has(CapabilityTools) {
		capabilities = append(capabilities, "tools")
	}
	if m.has(CapabilityReasoning) {
		capabilities = append(capabilities, "thinking")
	}
	contextKey := m.family() + ".context_length"
	return ollamaShow{
		Capabilities: capabilities,
		ModelInfo:    map[string]int{contextKey: cmp.Or(m.ContextLength, defaultContextLength)},
		Details:      ollamaDetails{Family: m.family()},
	}
}

// handleChat applies faults before validating the request, since a broken
// server fails whatever it's sent.
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	req, body, parseErr := readChatRequest(r)
	s.recordChat(r, body, req)
	fault := s.takeFault(target{model: req.Model, stream: req.Stream})
	if !s.applyFault(ctx, w, fault) {
		return
	}
	if err := validateChat(req, parseErr); err != nil {
		openaiwire.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	m, ok := s.model(req.Model)
	if !ok {
		openaiwire.WriteError(w, http.StatusNotFound, fmt.Sprintf("model %q not found, try pulling it first", req.Model))
		return
	}
	req, ok = s.fitRequest(w, req, m)
	if !ok {
		return
	}
	rep, err := respond(s.rules, req, m)
	if err != nil {
		s.logger.ErrorContext(ctx, "generating reply", "error", err)
		openaiwire.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Stream {
		s.stream(ctx, w, req, rep, fault)
		return
	}
	s.complete(ctx, w, req, rep)
}

// fitRequest returns req as model m would see it: with the context window
// applied. It writes the error and returns false if m can't serve req.
func (s *Server) fitRequest(w http.ResponseWriter, req openaiwire.ChatRequest, m Model) (openaiwire.ChatRequest, bool) {
	if s.cfg.StrictCapabilities && len(req.Tools) > 0 && !m.has(CapabilityTools) {
		writeOllamaError(w, http.StatusBadRequest, req.Model+" does not support tools")
		return req, false
	}
	if m.ContextLength > 0 {
		req = truncateToContext(req, m.ContextLength)
	}
	return req, true
}

// writeOllamaError writes an error in Ollama's native shape, which its
// /api/* endpoints use instead of the OpenAI one.
func writeOllamaError(w http.ResponseWriter, status int, msg string) {
	openaiwire.WriteJSON(w, status, controlError{Error: msg})
}

func readChatRequest(r *http.Request) (openaiwire.ChatRequest, []byte, error) {
	var req openaiwire.ChatRequest
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return req, body, fmt.Errorf("reading body: %w", err)
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return req, body, fmt.Errorf("decoding body: %w", err)
	}
	return req, body, nil
}

func validateChat(req openaiwire.ChatRequest, parseErr error) error {
	switch {
	case parseErr != nil:
		return parseErr
	case len(req.Messages) == 0:
		return errNoMessages
	case req.Model == "":
		return errNoModel
	}
	return nil
}

// newRequest describes an API request for the requests the server records.
func newRequest(r *http.Request) Request {
	return Request{Time: time.Now(), Method: r.Method, Path: r.URL.Path, Traceparent: r.Header.Get("traceparent"), Auth: authOf(r)}
}

func (s *Server) recordChat(r *http.Request, body []byte, req openaiwire.ChatRequest) {
	rec := newRequest(r)
	rec.Model, rec.Stream = req.Model, req.Stream
	if jsontext.Value(body).IsValid() {
		rec.Body = body
	}
	s.record(rec)
}

// applyFault applies the effects that precede a response, returning false if
// the response is finished.
func (s *Server) applyFault(ctx context.Context, w http.ResponseWriter, f Fault) bool {
	if f == (Fault{}) {
		return true
	}
	s.logger.InfoContext(ctx, "injecting fault", "fault", f)
	if f.Hang {
		s.block(ctx)
		panic(http.ErrAbortHandler)
	}
	if !sleep(ctx, time.Duration(f.SlowFirstToken)) {
		return false
	}
	if f.Status != 0 {
		if f.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		openaiwire.WriteError(w, f.Status, cmp.Or(f.Message, injectedFaultMessage))
		return false
	}
	return true
}

func (s *Server) complete(ctx context.Context, w http.ResponseWriter, req openaiwire.ChatRequest, rep reply) {
	n := time.Duration(len(deltas(rep, s.cfg.Timing.WordsPerChunk)) - 1)
	if !sleep(ctx, time.Duration(s.cfg.Timing.FirstToken)+n*time.Duration(s.cfg.Timing.ChunkInterval)) {
		return
	}
	resp := completionFor(req, rep)
	resp.Usage = s.reportedUsage(resp.Usage)
	openaiwire.WriteJSON(w, http.StatusOK, resp)
}

// reportedUsage scales u by TokensPerWord, which defaults to 1.
func (s *Server) reportedUsage(u openaiwire.Usage) openaiwire.Usage {
	k := cmp.Or(s.cfg.TokensPerWord, 1)
	u.PromptTokens *= k
	u.CompletionTokens *= k
	u.TotalTokens *= k
	return u
}

func (s *Server) stream(ctx context.Context, w http.ResponseWriter, req openaiwire.ChatRequest, rep reply, f Fault) {
	events := sse.NewWriter(w)
	sent := 0
	for _, c := range streamChunks(req, rep, s.cfg.Timing.WordsPerChunk) {
		if c.Usage != nil {
			u := s.reportedUsage(*c.Usage)
			c.Usage = &u
		}
		if !sleep(ctx, s.delayBefore(sent, c)) {
			return
		}
		s.interrupt(ctx, f, sent, false)
		if events.JSON(c) != nil {
			return
		}
		sent++
	}
	s.interrupt(ctx, f, sent, true)
	_ = events.Done()
}

func (s *Server) delayBefore(i int, c openaiwire.Chunk) time.Duration {
	switch {
	case i == 0:
		return time.Duration(s.cfg.Timing.FirstToken)
	case c.Usage != nil || c.Choices[0].FinishReason != nil:
		return 0
	default:
		return time.Duration(s.cfg.Timing.ChunkInterval)
	}
}

// interrupt drops or stalls the stream once sent chunks reach the fault's
// limit, or at the end of a stream that was shorter than the limit.
func (s *Server) interrupt(ctx context.Context, f Fault, sent int, end bool) {
	if f.DropAfterChunks > 0 && (end || sent >= f.DropAfterChunks) {
		s.logger.InfoContext(ctx, "dropping stream", "chunks", sent)
		panic(http.ErrAbortHandler)
	}
	if f.StallAfterChunks > 0 && (end || sent >= f.StallAfterChunks) {
		s.logger.InfoContext(ctx, "stalling stream", "chunks", sent)
		s.block(ctx)
		panic(http.ErrAbortHandler)
	}
}
