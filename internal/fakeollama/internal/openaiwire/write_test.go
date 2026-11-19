package openaiwire

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorType(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusBadRequest, ErrorTypeInvalidRequest},
		{http.StatusNotFound, ErrorTypeNotFound},
		{http.StatusInternalServerError, ErrorTypeAPI},
		{http.StatusTooManyRequests, ErrorTypeAPI},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, ErrorType(tt.status))
		})
	}
}

func TestWriteJSON(t *testing.T) {
	t.Run("writes status, content type, and body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusCreated, ModelList{Object: ObjectList, Data: []Model{}})
		assert.Equal(t, http.StatusCreated, rec.Code)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"object":"list","data":[]}`, rec.Body.String())
	})
}

func TestWriteError(t *testing.T) {
	t.Run("writes the error envelope", func(t *testing.T) {
		rec := httptest.NewRecorder()
		WriteError(rec, http.StatusNotFound, "model not found")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.JSONEq(t, `{"error":{"message":"model not found","type":"not_found_error","param":null,"code":null}}`, rec.Body.String())
	})
}
