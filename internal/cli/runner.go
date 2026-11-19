package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/runner"
)

// newRunnerCmd is ws runner, for the daemon's use and the desktop app's:
// hidden, and absent from the agent-facing guides (ADR 0063).
func (a *App) newRunnerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "runner <OP>",
		Short:  "Run a runner session's agent loop",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newRunnerRunCmd(), a.newRunnerCheckCmd(), a.newRunnerStopCmd())
	return cmd
}

func (a *App) newRunnerCheckCmd() *cobra.Command {
	var model string
	cmd := &cobra.Command{
		Use:    "check [--json] [--model <m>] [<endpoint>]",
		Short:  "Check that an endpoint serves a model with tools",
		Hidden: true,
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageErrorf("ws runner check takes at most one endpoint, not %d", len(args))
			}
			return nil
		},
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runRunnerCheck(cmd.Context(), args, model)
		})),
	}
	cmd.Flags().StringVar(&model, "model", "", "the model alias or id to check")
	return cmd
}

// runRunnerCheck has the daemon preflight the endpoint and model, with `ws
// runner check`'s defaults for the ones not named.
func (a *App) runRunnerCheck(ctx context.Context, args []string, model string) error {
	req := daemon.PreflightRequest{Check: true, Model: model}
	if len(args) == 1 {
		req.Endpoint = args[0]
	}
	client, err := a.daemonClient(ctx)
	if err != nil {
		return err
	}
	res, err := client.Preflight(ctx, req)
	if err != nil {
		return remoteRunnerError(err)
	}
	if a.global.json {
		return a.writeJSON(res)
	}
	fmt.Fprintf(a.stdout, "%s (%s): %s, context %d tokens\n", res.Endpoint, res.URL, res.Model, res.ContextTokens)
	return nil
}

func (a *App) newRunnerStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "stop [--json] <workspace-id>",
		Short:  "Stop a runner without removing its workspace",
		Hidden: true,
		Args:   exactlyOneArg("a workspace"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runRunnerStop(cmd.Context(), args[0])
		})),
	}
}

// runnerStopResult is `ws runner stop`'s object.
type runnerStopResult struct {
	ID       string   `json:"id"`
	Session  string   `json:"session"`
	Stopped  bool     `json:"stopped"`
	Warnings []string `json:"warnings"`
}

// runRunnerStop stops workspace id's runner through the daemon. A workspace
// without a runner session is workspace_not_found.
func (a *App) runRunnerStop(ctx context.Context, id string) error {
	if !daemon.ValidWorkspaceID(id) {
		return newError(codeWorkspaceNotFound, fmt.Errorf("workspace %s has no runner session", id))
	}
	run, err := runner.FilesFor(a.paths.State, id).ReadRun()
	if errors.Is(err, fs.ErrNotExist) {
		return newError(codeWorkspaceNotFound, fmt.Errorf("workspace %s has no runner session", id))
	}
	if err != nil {
		return newError(codeInternal, err)
	}
	client, err := a.daemonClient(ctx)
	if err != nil {
		return err
	}
	stopped, err := client.StopRunner(ctx, id)
	if err != nil {
		return remoteRunnerError(err)
	}
	res := runnerStopResult{ID: id, Session: run.Session, Stopped: stopped, Warnings: []string{}}
	if a.global.json {
		return a.writeJSON(res)
	}
	if !stopped {
		fmt.Fprintf(a.stdout, "runner %s had already exited\n", run.Session)
		return nil
	}
	fmt.Fprintf(a.stdout, "stopped runner %s\n", run.Session)
	return nil
}

// daemonClient is a client to this config directory's daemon, started when
// none runs unless WS_DAEMON_AUTOSTART=0.
func (a *App) daemonClient(ctx context.Context) (*daemon.Client, error) {
	paths := a.daemonPaths()
	exe, err := os.Executable()
	if err != nil {
		return nil, newError(codeInternal, err)
	}
	autostart := environValue(a.environ, "WS_DAEMON_AUTOSTART") != "0"
	client, err := daemon.Ensure(ctx, paths, exe, autostart, a.version)
	if err != nil {
		return nil, daemonUnavailable(err, paths)
	}
	return client, nil
}

// remoteRunnerError keeps the error code the daemon answered with and its
// details. Any other error is returned as it is.
func remoteRunnerError(err error) error {
	remote, ok := errors.AsType[*daemon.RemoteError](err)
	if !ok {
		return err
	}
	ce := newError(errorCode(remote.Code), err)
	ce.details = remote.Details
	return ce
}

func (a *App) newRunnerRunCmd() *cobra.Command {
	var resume bool
	cmd := &cobra.Command{
		Use:    "run [--resume] <id>",
		Short:  "Run a runner session's agent loop until it exits",
		Hidden: true,
		Args:   exactlyOneArg("a workspace"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runRunner(cmd.Context(), args[0], resume)
		})),
	}
	cmd.Flags().BoolVar(&resume, "resume", false, "continue the run from its transcript")
	return cmd
}

// runRunner runs workspace id's runner session until it exits. It stops
// the run on SIGTERM or SIGINT, which the daemon sends to interrupt it.
func (a *App) runRunner(ctx context.Context, id string, resume bool) error {
	ws, err := a.workspaces.Find(id)
	if err != nil {
		return fmt.Errorf("finding workspace %s: %w", id, err)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, os.Interrupt)
	defer stop()
	in := runner.Input{
		Files:     runner.FilesFor(a.paths.State, ws.ID),
		Resume:    resume,
		Workspace: ws.Path,
		ConfigDir: a.paths.Root,
		Environ:   a.environ,
		Store:     runnerRecords{Store: machine.NewStore(a.paths.State), workspaces: a.workspaces},
	}
	if err := runner.Execute(ctx, &in); err != nil {
		return newError(codeInternal, err)
	}
	return nil
}

// runnerRecords is machine.Store for a runner, with the lineage ws machine
// result computes from the workspace list.
type runnerRecords struct {
	*machine.Store
	workspaces workspaceStore
}

// Lineage is id's root and its ancestors, as ws machine result computes them.
func (r runnerRecords) Lineage(id string) (root string, ancestors []string, err error) {
	list, err := r.workspaces.List()
	if err != nil {
		return "", nil, fmt.Errorf("listing workspaces: %w", err)
	}
	return machine.Root(list, id), machine.Ancestors(list, id), nil
}
