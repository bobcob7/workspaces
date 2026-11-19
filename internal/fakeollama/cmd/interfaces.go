package cmd

import "context"

//go:generate go tool moq -out moq_test.go . server

// server is the fake Ollama server the command runs.
type server interface {
	Start(ctx context.Context) error
	Shutdown(ctx context.Context) error
	URL() string
	ControlURL() string
}
