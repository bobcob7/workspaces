package openaiwire

import (
	"encoding/json/v2"
	"net/http"
)

// Error types.
const (
	ErrorTypeInvalidRequest = "invalid_request_error"
	ErrorTypeNotFound       = "not_found_error"
	ErrorTypeAPI            = "api_error"
)

// ErrorResponse is the OpenAI error envelope.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody describes an error.
type ErrorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

// ErrorType returns the error type for an HTTP status, the way Ollama
// assigns them.
func ErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return ErrorTypeInvalidRequest
	case http.StatusNotFound:
		return ErrorTypeNotFound
	default:
		return ErrorTypeAPI
	}
}

// WriteJSON writes v as a JSON response with status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.MarshalWrite(w, v)
}

// WriteError writes an error envelope with status, typed by ErrorType.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, ErrorResponse{Error: ErrorBody{Message: message, Type: ErrorType(status)}})
}
