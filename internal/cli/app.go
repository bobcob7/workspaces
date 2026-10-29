// Package cli is the ws command tree (ADR 0005). Commands parse input and
// call into domain packages; they hold no business logic.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/gh"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/power"
	"github.com/bobcob7/workspaces/internal/proc"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/shell"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// App is one ws process: its streams, logger, and parsed global flags.
type App struct {
	version string
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	log     *slog.Logger
	level   *slog.LevelVar
	global  globalOptions
	configs configLoader
	paths   config.Paths
	config  config.Config
	// newStores builds the repo cache, the workspace store, and the
	// lifetime manager once the config is loaded.
	newStores  func(paths config.Paths, cfg config.Config) stores
	cache      repoCache
	workspaces workspaceStore
	lifetime   lifetimeManager
	// swept is what the removal pass removed before the command ran.
	swept []lifetime.Removed
	// warned are the command's own warnings, reported after the stores'.
	warned []string
	// now is the clock ws list shows ages by.
	now func() time.Time
	// shell runs ws shell's interactive shell on ws's own streams.
	shell shellRunner
	// claude runs the Claude Code CLI; nothing else in ws does.
	claude claudeClient
	// gh runs the GitHub CLI; nothing else in ws does.
	gh ghClient
	// environ is the environment git, claude, and the shell run with.
	environ []string
	// getwd is os.Getwd: ws review finds its workspace from it.
	getwd func() (string, error)
	// reviewPoll is how often ws review wait reads the review's session.
	reviewPoll time.Duration
	// continuePoll is how often ws continue --watch runs a pass.
	continuePoll time.Duration
	// indentJSON is set when stdout is a terminal (ADR 0016).
	indentJSON bool
	// progress is set when stderr is a terminal, where progress may be
	// shown unless --quiet is set (ADR 0016).
	progress bool
	// interactive is set when stdin and stdout are both terminals, so ws
	// claude can attach to an idle session (ADR 0025).
	interactive bool
	// notify and stopNotify are signal.Notify and signal.Stop: ws claude
	// catches SIGHUP while it hands a workspace to its session.
	notify     func(c chan<- os.Signal, sig ...os.Signal)
	stopNotify func(c chan<- os.Signal)
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
// --verbose logging to stderr. ws shell's shell runs on stdin, stdout, and
// stderr. configs supplies the config for commands that need it, and
// environ, normally os.Environ(), is the environment git, claude, and the
// shell run with.
func New(version string, stdin io.Reader, stdout, stderr io.Writer, configs configLoader, environ []string) *App {
	level := new(slog.LevelVar)
	level.Set(logOff)
	log := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))
	a := &App{
		shell:        shell.New(log, environ, stdin, stdout, stderr),
		claude:       claude.New(log, "claude", environ),
		gh:           gh.New(log, "gh", environ),
		version:      version,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		configs:      configs,
		log:          log,
		level:        level,
		now:          time.Now,
		indentJSON:   isTerminal(stdout),
		progress:     isTerminal(stderr),
		interactive:  isTerminal(stdin) && isTerminal(stdout),
		environ:      environ,
		getwd:        os.Getwd,
		reviewPoll:   defaultReviewPoll,
		continuePoll: defaultContinuePoll,
		notify:       signal.Notify,
		stopNotify:   signal.Stop,
	}
	a.newStores = func(paths config.Paths, cfg config.Config) stores {
		runner := git.NewRunner(log, "git", environ)
		c := cache.New(log, runner, paths.Cache, cfg.Defaults)
		ws := workspace.New(log, runner, c, a.claude, paths.Workspaces, paths.State)
		lm := lifetime.New(log, ws, a.claude, proc.Reader{}, power.Reader{}, paths.State)
		if cfg.Review.RemovesFinished() {
			lm.RemoveFinishedReviews(reviewJudge{state: paths.State, store: store.New(paths.Reviews)})
		}
		return stores{cache: c, workspaces: ws, lifetime: lm}
	}
	return a
}

// stores are what commands that read CONFIG_DIR work on.
type stores struct {
	cache      repoCache
	workspaces workspaceStore
	lifetime   lifetimeManager
}

// Run executes ws with args, excluding the program name, and returns the
// process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	return a.execute(ctx, a.newRootCmd(), args) //nolint:contextcheck // commands read ctx from cmd.Context(), set by ExecuteContext.
}

// execute runs root with args and turns its error into output on stderr and
// an exit code. A shell's exit status from ws shell is the exit code as it
// is. Errors without a code come from cobra rejecting the command line, so
// they are usage errors. A command for agents reports every error as JSON
// (ADR 0046).
func (a *App) execute(ctx context.Context, root *cobra.Command, args []string) int {
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(ctx)
	if err == nil {
		return exitOK
	}
	if isAgentCommand(cmd) {
		a.global.json = true
	}
	if se, ok := errors.AsType[*shellExitError](err); ok {
		return se.status
	}
	ce, ok := errors.AsType[*cliError](err)
	if !ok {
		ce = newError(codeUsage, err)
	}
	if !ce.reported {
		a.writeError(ce)
	}
	return ce.exitCode()
}

func (a *App) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ws",
		Short: "Disposable workspaces for AI agents and shells",
		Long: "Disposable workspaces for AI agents and shells.\n\n" +
			"Agents: read `ws llm` first, then use only the ws machine commands, " +
			"and ws review to get your work reviewed. The commands for people " +
			"and the desktop app aren't yours to run.",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			if a.global.verbose {
				a.level.Set(slog.LevelDebug)
			}
			// Commands for agents always print JSON (ADR 0046).
			if isAgentCommand(cmd) {
				a.global.json = true
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
	root.AddGroup(
		&cobra.Group{ID: agentsGroup, Title: "Commands for agents:"},
		&cobra.Group{ID: peopleGroup, Title: "Commands for people and the desktop app:"},
	)
	for _, cmd := range []*cobra.Command{
		a.newClaudeCmd(),
		a.newShellCmd(),
		a.newRestoreCmd(),
		a.newRestartCmd(),
		a.newContinueCmd(),
		a.newListCmd(),
		a.newRmCmd(),
		a.newRenameCmd(),
		a.newHideCmd(),
		a.newUnhideCmd(),
		a.newCacheCmd(),
	} {
		cmd.GroupID = peopleGroup
		root.AddCommand(cmd)
	}
	root.AddCommand(a.newMachineCmd(), a.newReviewCmd(), a.newLLMCmd())
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

// withConfig loads CONFIG_DIR and config.toml, then runs the removal pass
// (ADR 0015), before running fn. Commands that touch the cache,
// workspaces, or state use it; ws llm doesn't.
func (a *App) withConfig(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return a.configured(true, fn)
}

// withConfigUnswept is withConfig without the removal pass, for ws list,
// which runs it as part of listing.
func (a *App) withConfigUnswept(fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return a.configured(false, fn)
}

func (a *App) configured(sweep bool, fn func(cmd *cobra.Command, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		paths, cfg, err := a.configs.Load(a.global.configDir)
		if errors.Is(err, config.ErrInvalid) {
			return newError(codeConfigInvalid, err)
		}
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		a.paths, a.config = paths, cfg
		s := a.newStores(paths, cfg)
		a.cache, a.workspaces, a.lifetime = s.cache, s.workspaces, s.lifetime
		a.log.Debug("config loaded", slog.String("root", paths.Root))
		if sweep {
			a.swept = a.lifetime.Sweep(cmd.Context())
		}
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
