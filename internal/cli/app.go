// Package cli is the ws command tree (ADR 0005). Commands parse input and
// call into domain packages; they hold no business logic.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/config"
)

// App is one ws process: its streams, logger, and parsed global flags.
type App struct {
	version string
	stdout  io.Writer
	stderr  io.Writer
	log     *slog.Logger
	level   *slog.LevelVar
	global  globalOptions
	configs configLoader
	paths   config.Paths
	config  config.Config
	// indentJSON is set when stdout is a terminal (ADR 0016).
	indentJSON bool
}

type globalOptions struct {
	configDir string
	json      bool
	quiet     bool
	verbose   bool
}

// logOff is above every slog level, so nothing is logged until --verbose.
const logOff = slog.LevelError + 4

// New returns an App that writes results to stdout, and errors and
// --verbose logging to stderr. configs supplies the config for commands
// that need it.
func New(version string, stdout, stderr io.Writer, configs configLoader) *App {
	level := new(slog.LevelVar)
	level.Set(logOff)
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	return &App{
		version:    version,
		stdout:     stdout,
		stderr:     stderr,
		configs:    configs,
		log:        log,
		level:      level,
		indentJSON: isTerminal(stdout),
	}
}

// Run executes ws with args, excluding the program name, and returns the
// process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	return a.execute(ctx, a.newRootCmd(), args)
}

// execute runs root with args and turns its error into output on stderr and
// an exit code. Errors without a code come from cobra rejecting the command
// line, so they are usage errors.
func (a *App) execute(ctx context.Context, root *cobra.Command, args []string) int {
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	ce, ok := errors.AsType[*cliError](err)
	if !ok {
		ce = newError(codeUsage, err)
	}
	a.writeError(ce)
	return ce.exitCode()
}

func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "ws",
		Short:         "Disposable workspaces for AI agents and shells",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			if a.global.verbose {
				a.level.Set(slog.LevelDebug)
			}
		},
	}
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newError(codeUsage, err)
	})
	flags := root.PersistentFlags()
	flags.StringVar(&a.global.configDir, "config-dir", "", "config, cache, and workspace directory (env WS_CONFIG_DIR)")
	flags.BoolVar(&a.global.json, "json", false, "machine-readable JSON on stdout")
	flags.BoolVar(&a.global.quiet, "quiet", false, "no progress output on stderr")
	flags.BoolVarP(&a.global.verbose, "verbose", "v", false, "show git and claude commands and timings on stderr")
	root.AddCommand(
		a.newClaudeCmd(),
		a.newShellCmd(),
		a.newRestoreCmd(),
		a.newRestartCmd(),
		a.newListCmd(),
		a.newRmCmd(),
		a.newCacheCmd(),
		a.newLLMCmd(),
	)
	return root
}

// runE gives every error a RunE returns an error code, so Run can treat any
// error without one as cobra rejecting the command line.
func runE(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		if err := fn(cmd, args); err != nil {
			return asCLIError(err)
		}
		return nil
	}
}

// withConfig loads CONFIG_DIR and config.toml before running fn. Commands
// that touch the cache, workspaces, or state use it; ws llm doesn't.
func (a *App) withConfig(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := a.configs.Load(a.global.configDir)
		if errors.Is(err, config.ErrInvalid) {
			return newError(codeConfigInvalid, err)
		}
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		a.paths, a.config = paths, cfg
		a.log.Debug("config loaded", slog.String("root", paths.Root))
		return fn(cmd, args)
	}
}

// noJSON rejects --json on commands whose output isn't a result: ws shell
// and ws llm (ADR 0016).
func (a *App) noJSON(cmd *cobra.Command, _ []string) error {
	if a.global.json {
		return usageErrorf("%s doesn't support --json", commandName(cmd))
	}
	return nil
}
