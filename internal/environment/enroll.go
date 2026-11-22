package environment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// maxEnrollBody caps an enrollment request's body.
const maxEnrollBody = 64 << 10

// errTrailingData refuses an enrollment body with anything after its JSON object.
var errTrailingData = errors.New("trailing data after the enrollment request")

// newEnrollHandler serves POST /v1/enroll and nothing else (404 for other
// paths, 405 for other methods). Body at most 64 KiB, JSON
// EnrollRequest with unknown fields refused. Answers:
// 200 EnrollResponse; 400 {"error":{"code":"usage",…}} for a malformed
// body; 403 "enrollment_failed"; 409 "enrollment_used"; 500 "internal".
// Logs "environment enrolled" with id, or "enrollment refused" with the
// remote address and code; never the secret, CSR or code.
func newEnrollHandler(log *slog.Logger, enroll func(ctx context.Context, secret string, csrPEM []byte) (envproto.EnrollResponse, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != envproto.EnrollPath {
			writeError(w, http.StatusNotFound, "usage", "no such path")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, "usage", "use POST")
			return
		}
		req, err := decodeEnrollRequest(w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "usage", "malformed enrollment request")
			return
		}
		resp, err := enroll(r.Context(), req.Secret, []byte(req.CSR))
		if err != nil {
			refuseEnrollment(log, w, r, err)
			return
		}
		log.InfoContext(r.Context(), "environment enrolled", "id", resp.ID)
		writeJSON(w, http.StatusOK, resp)
	})
}

// decodeEnrollRequest reads the body of an enrollment request, which must be
// exactly one JSON object.
func decodeEnrollRequest(w http.ResponseWriter, r *http.Request) (envproto.EnrollRequest, error) {
	var req envproto.EnrollRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnrollBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errTrailingData
	}
	return req, nil
}

// refuseEnrollment logs a refused enrollment and answers with its code.
func refuseEnrollment(log *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := enrollFailure(err)
	log.WarnContext(r.Context(), "enrollment refused", "remote", r.RemoteAddr, "code", code)
	writeError(w, status, code, message)
}

// enrollFailure maps an enrollment error to its status, code and message.
func enrollFailure(err error) (int, string, string) {
	switch {
	case errors.Is(err, ErrEnrollmentUsed):
		return http.StatusConflict, "enrollment_used", ErrEnrollmentUsed.Error()
	case errors.Is(err, ErrEnrollmentFailed):
		return http.StatusForbidden, "enrollment_failed", ErrEnrollmentFailed.Error()
	default:
		return http.StatusInternalServerError, "internal", "internal error"
	}
}

// writeError answers with the {"error": …} envelope.
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, envproto.ErrorEnvelope{Error: envproto.ReplyError{Code: code, Message: message}})
}

// writeJSON answers with v as JSON.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body) // the status is already sent, so a failed write has nobody to report to
}
