package sse

//go:generate go tool moq -out moq_test.go . bodyWriter flusher

// bodyWriter receives the encoded events. http.ResponseWriter satisfies it.
type bodyWriter interface {
	Write(p []byte) (int, error)
}

// flusher sends buffered events to the client. *http.ResponseController
// satisfies it.
type flusher interface {
	Flush() error
}
