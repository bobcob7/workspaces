package cache

import (
	"context"
)

//go:generate go tool moq -out moq_test.go . gitRunner

// gitRunner runs git commands (ADR 0003).
type gitRunner interface {
	Run(ctx context.Context, dir string, args ...string) ([]byte, error)
}
