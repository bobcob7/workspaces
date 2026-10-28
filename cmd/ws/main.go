// Command ws creates disposable workspaces for AI agents and shells.
package main

import (
	"context"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/bobcob7/workspaces/internal/cli"
	"github.com/bobcob7/workspaces/internal/config"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	configs := config.NewLoader(os.Getenv, os.UserConfigDir)
	return cli.New(buildVersion(version, debug.ReadBuildInfo), os.Stdin, stdout, stderr, configs, os.Environ()).Run(ctx, args)
}

// buildVersion is the -ldflags version, or for a binary built without one,
// such as by go install, the module version or commit the Go toolchain
// recorded, so ws --version can tell builds apart.
func buildVersion(set string, read func() (*debug.BuildInfo, bool)) string {
	if set != "dev" {
		return set
	}
	info, ok := read()
	if !ok {
		return set
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var revision, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if len(revision) < 7 {
		return set
	}
	return "dev-" + revision[:7] + dirty
}
