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
	"time"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
	"github.com/bobcob7/workspaces/internal/fakeollama/internal/sse"
)

const injectedFaultMessage = "fake-ollama: injected fault"

var (
	errNoMessages = errors.New("[] is too short - 'messages'")
	errNoModel    = errors.New("model is required")
)

func (s *Server) apiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	return mux
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	s.record(Request{Time: time.Now(), Method: r.Method, Path: r.URL.Path, Traceparent: r.Header.Get("traceparent")})
	if !s.applyFault(r.Context(), w, s.takeFault(target{})) {
		return
	}
	list := openaiwire.ModelList{Object: openaiwire.ObjectList, Data: make([]openaiwire.Model, 0, len(s.cfg.Models))}
	for _, m := range s.cfg.Models {
		list.Data = append(list.Data, openaiwire.Model{ID: m.Name, Object: openaiwire.ObjectModel, Created: s.created, OwnedBy: ownedBy(m.Name)})
	}
	openaiwire.WriteJSON(w, http.StatusOK, list)
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

func (s *Server) recordChat(r *http.Request, body []byte, req openaiwire.ChatRequest) {
	rec := Request{Time: time.Now(), Method: r.Method, Path: r.URL.Path, Model: req.Model, Stream: req.Stream, Traceparent: r.Header.Get("traceparent")}
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
	openaiwire.WriteJSON(w, http.StatusOK, completionFor(req, rep))
}

func (s *Server) stream(ctx context.Context, w http.ResponseWriter, req openaiwire.ChatRequest, rep reply, f Fault) {
	events := sse.NewWriter(w)
	sent := 0
	for _, c := range streamChunks(req, rep, s.cfg.Timing.WordsPerChunk) {
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
