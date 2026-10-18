// Command ws creates disposable workspaces for AI agents and shells.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bobcob7/workspaces/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cli.New(version, stdout, stderr).Run(ctx, args)
}
