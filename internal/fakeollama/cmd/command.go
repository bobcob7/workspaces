// Package cmd implements the fake-ollama command.
//
// Copied from llm-router internal/cli/fakeollama @9789c32 (ADR 0067); maintained here.
package cmd

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/fakeollama"
	"github.com/bobcob7/workspaces/internal/fakeollama/internal/httpserver"
)

const (
	defaultConfigPath = "/etc/fake-ollama/config.yaml"
	shutdownTimeout   = 10 * time.Second
)

type serverFactory func(cfg fakeollama.Config, logger *slog.Logger) (server, error)

func newServer(cfg fakeollama.Config, logger *slog.Logger) (server, error) {
	return fakeollama.New(cfg, logger)
}

// NewCommand returns the fake-ollama command. It serves until its context is
// cancelled, then shuts down gracefully.
func NewCommand(logger *slog.Logger) *cobra.Command {
	return newCommand(logger, newServer)
}

// options are the command's flags.
type options struct {
	configPath    string
	listen        string
	controlListen string
}

func newCommand(logger *slog.Logger, factory serverFactory) *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:           "fake-ollama",
		Short:         "Serve a deterministic, fault-injectable imitation of Ollama's OpenAI-compatible API",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return run(cmd.Context(), cmd.OutOrStdout(), opts, logger, factory)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.configPath, "config", defaultConfigPath, "path to the YAML config file")
	f.StringVar(&opts.listen, "listen", "", "API address, overriding the config (e.g. 127.0.0.1:0 for any free port)")
	f.StringVar(&opts.controlListen, "control-listen", "", "control API address, overriding the config")
	return cmd
}

func run(ctx context.Context, out io.Writer, opts options, logger *slog.Logger, factory serverFactory) error {
	cfg, err := fakeollama.LoadConfig(opts.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg.Listen = cmp.Or(opts.listen, cfg.Listen)
	cfg.ControlListen = cmp.Or(opts.controlListen, cfg.ControlListen)
	srv, err := factory(cfg, logger)
	if err != nil {
		return fmt.Errorf("configuring server: %w", err)
	}
	if err := srv.Start(ctx); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	fmt.Fprintf(out, "API listening on %s\ncontrol API listening on %s\n", srv.URL(), srv.ControlURL())
	if err := httpserver.WaitAndShutdown(ctx, shutdownTimeout, srv.Shutdown); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	return nil
}
