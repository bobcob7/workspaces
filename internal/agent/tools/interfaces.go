package tools

import (
	"context"

	"github.com/bobcob7/workspaces/internal/gate"
)

//go:generate go tool moq -out moq_test.go . recorder shellRunner

// recorder records a verdict and its report, as ws machine result does.
type recorder interface {
	Record(ctx context.Context, verdict string, report []byte) error
}

// shellRunner runs a script through internal/gate.
type shellRunner interface {
	Run(ctx context.Context, req gate.Request) (gate.Result, error)
}
