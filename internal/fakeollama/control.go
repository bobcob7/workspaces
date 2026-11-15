package fakeollama

import (
	"errors"
	"io"
	"net/http"

	"github.com/bobcob7/workspaces/internal/fakeollama/internal/openaiwire"
)

type controlError struct {
	Error string `json:"error"`
}

func (s *Server) controlHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /faults", s.handleGetFaults)
	mux.HandleFunc("PUT /faults", s.handlePutFaults)
	mux.HandleFunc("DELETE /faults", s.handleDeleteFaults)
	mux.HandleFunc("GET /requests", s.handleGetRequests)
	mux.HandleFunc("POST /reset", s.handleReset)
	return mux
}

func (s *Server) handleGetFaults(w http.ResponseWriter, _ *http.Request) {
	openaiwire.WriteJSON(w, http.StatusOK, s.Faults())
}

// handlePutFaults replaces the active faults with the fault object or array
// of faults in the body.
func (s *Server) handlePutFaults(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		openaiwire.WriteJSON(w, http.StatusBadRequest, controlError{Error: err.Error()})
		return
	}
	faults, err := decodeFaults(body)
	if err == nil {
		err = s.SetFaults(r.Context(), faults...)
	}
	switch {
	case errors.Is(err, errInvalidFault):
		openaiwire.WriteJSON(w, http.StatusBadRequest, controlError{Error: err.Error()})
	case err != nil:
		openaiwire.WriteJSON(w, http.StatusInternalServerError, controlError{Error: err.Error()})
	default:
		openaiwire.WriteJSON(w, http.StatusOK, s.Faults())
	}
}

func (s *Server) handleDeleteFaults(w http.ResponseWriter, r *http.Request) {
	if err := s.ClearFaults(r.Context()); err != nil {
		openaiwire.WriteJSON(w, http.StatusInternalServerError, controlError{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetRequests(w http.ResponseWriter, _ *http.Request) {
	openaiwire.WriteJSON(w, http.StatusOK, s.Requests())
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	if err := s.Reset(r.Context()); err != nil {
		openaiwire.WriteJSON(w, http.StatusInternalServerError, controlError{Error: err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
