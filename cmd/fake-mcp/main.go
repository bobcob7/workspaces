// Command fake-mcp serves a scripted MCP server for tests (ADR 0067): over
// stdio by default, or streamable HTTP with --http. See internal/fakemcp.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/bobcob7/workspaces/internal/fakemcp"
)

// exitEnv is the exit status when a required environment variable is missing.
const exitEnv = 2

var errEnv = errors.New("required environment missing")

type options struct {
	config string
	http   string
	record string
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, logger, os.Args[1:], os.Stdout, os.Getenv)
	stop()
	if err == nil {
		return
	}
	logger.Error("fake-mcp failed", "error", err)
	if errors.Is(err, errEnv) {
		os.Exit(exitEnv)
	}
	os.Exit(1)
}

func run(ctx context.Context, logger *slog.Logger, args []string, stdout io.Writer, getenv func(string) string) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	cfg, err := fakemcp.LoadConfig(opts.config)
	if err != nil {
		return err
	}
	if opts.http == "" {
		if checkErr := fakemcp.CheckEnv(cfg, getenv); checkErr != nil {
			return fmt.Errorf("%w: %w", errEnv, checkErr)
		}
	}
	var record io.Writer
	if opts.record != "" {
		file, createErr := os.Create(opts.record)
		if createErr != nil {
			return fmt.Errorf("creating record file: %w", createErr)
		}
		defer file.Close()
		record = file
	}
	srv, err := fakemcp.NewServer(cfg, record, logger)
	if err != nil {
		return err
	}
	if opts.http == "" {
		return srv.Run(ctx, &mcp.StdioTransport{})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
	return serveHTTP(ctx, fakemcp.RequireHeaders(cfg, handler), opts.http, stdout)
}

func parseOptions(args []string) (options, error) {
	var opts options
	flags := flag.NewFlagSet("fake-mcp", flag.ContinueOnError)
	flags.StringVar(&opts.config, "config", "", "fake-mcp YAML file (required)")
	flags.StringVar(&opts.http, "http", "", "serve streamable HTTP on this address, e.g. 127.0.0.1:0")
	flags.StringVar(&opts.record, "record", "", "write one JSON line per call to this file")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if opts.config == "" {
		return options{}, errors.New("--config is required")
	}
	return opts, nil
}

func serveHTTP(ctx context.Context, handler http.Handler, addr string, stdout io.Writer) error {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", addr, err)
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	context.AfterFunc(ctx, func() { _ = httpServer.Shutdown(context.WithoutCancel(ctx)) })
	fmt.Fprintf(stdout, "MCP listening on http://%s/mcp\n", ln.Addr())
	if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving http: %w", err)
	}
	return nil
}
