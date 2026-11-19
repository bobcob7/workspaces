// Command fake-ollama serves a deterministic, fault-injectable imitation of
// Ollama's OpenAI-compatible API. See internal/fakeollama/cmd.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bobcob7/workspaces/internal/fakeollama/cmd"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := execute(logger); err != nil {
		logger.Error("fake-ollama failed", "error", err)
		os.Exit(1)
	}
}

func execute(logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmd.NewCommand(logger).ExecuteContext(ctx)
}
