package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/envproto"
)

// enrollStub is an enroll func that answers with resp and err and records its arguments.
func enrollStub(resp *envproto.EnrollResponse, err error) (func(context.Context, string, []byte) (envproto.EnrollResponse, error), *[]string) {
	var secrets []string
	return func(_ context.Context, secret string, _ []byte) (envproto.EnrollResponse, error) {
		secrets = append(secrets, secret)
		return *resp, err
	}, &secrets
}

// postEnroll sends body to path on handler with method and returns the recorder.
func postEnroll(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// errorCodeOf decodes the error envelope in body.
func errorCodeOf(t *testing.T, body []byte) string {
	t.Helper()
	var env envproto.ErrorEnvelope
	require.NoError(t, json.Unmarshal(body, &env))
	return env.Error.Code
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestEnrollHandler_ServeHTTP(t *testing.T) {
	const goodBody = `{"secret":"s3cret","csr":"csr-pem"}`
	t.Run("answers 200 with the enrollment response", func(t *testing.T) {
		resp := envproto.EnrollResponse{ID: "env-0a1b2c3d", Name: "linux-server", Broker: "b:8883", ServerName: "b"}
		enroll, secrets := enrollStub(&resp, nil)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, goodBody)
		require.Equal(t, http.StatusOK, rec.Code)
		var got envproto.EnrollResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		assert.Equal(t, resp, got)
		assert.Equal(t, []string{"s3cret"}, *secrets)
	})
	t.Run("answers 409 enrollment_used for a used code", func(t *testing.T) {
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, ErrEnrollmentUsed)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, goodBody)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "enrollment_used", errorCodeOf(t, rec.Body.Bytes()))
	})
	t.Run("answers 403 enrollment_failed for a wrong code", func(t *testing.T) {
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, ErrEnrollmentFailed)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, goodBody)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Equal(t, "enrollment_failed", errorCodeOf(t, rec.Body.Bytes()))
	})
	t.Run("answers 500 internal for any other error", func(t *testing.T) {
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, errors.New("disk full"))
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, goodBody)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, "internal", errorCodeOf(t, rec.Body.Bytes()))
		assert.NotContains(t, rec.Body.String(), "disk full")
	})
	t.Run("answers 400 usage for a malformed body", func(t *testing.T) {
		enroll, secrets := enrollStub(&envproto.EnrollResponse{}, nil)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, `{"secret":`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Equal(t, "usage", errorCodeOf(t, rec.Body.Bytes()))
		assert.Empty(t, *secrets)
	})
	t.Run("answers 400 usage for an unknown field", func(t *testing.T) {
		enroll, secrets := enrollStub(&envproto.EnrollResponse{}, nil)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, `{"secret":"x","csr":"y","extra":1}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, *secrets)
	})
	t.Run("answers 400 usage for a body over 64 KiB", func(t *testing.T) {
		enroll, secrets := enrollStub(&envproto.EnrollResponse{}, nil)
		big := `{"secret":"` + strings.Repeat("a", maxEnrollBody) + `"}`
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, envproto.EnrollPath, big)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Empty(t, *secrets)
	})
	t.Run("answers 404 for any other path", func(t *testing.T) {
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, nil)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodPost, "/v1/other", goodBody)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	t.Run("answers 405 with Allow POST for GET", func(t *testing.T) {
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, nil)
		rec := postEnroll(t, newEnrollHandler(discardLog(), enroll), http.MethodGet, envproto.EnrollPath, "")
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, http.MethodPost, rec.Header().Get("Allow"))
	})
	t.Run("logs a refusal without the secret", func(t *testing.T) {
		var logs bytes.Buffer
		log := slog.New(slog.NewJSONHandler(&logs, nil))
		enroll, _ := enrollStub(&envproto.EnrollResponse{}, ErrEnrollmentFailed)
		postEnroll(t, newEnrollHandler(log, enroll), http.MethodPost, envproto.EnrollPath, goodBody)
		assert.Contains(t, logs.String(), "enrollment refused")
		assert.Contains(t, logs.String(), "enrollment_failed")
		assert.NotContains(t, logs.String(), "s3cret")
	})
}
