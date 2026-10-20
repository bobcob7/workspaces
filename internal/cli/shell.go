package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lockfile"
	"github.com/bobcob7/workspaces/internal/shell"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type shellOptions struct {
	name    string
	offline bool
	specs   []string
}

func (a *App) newShellCmd() *cobra.Command {
	var opts shellOptions
	cmd := &cobra.Command{
		Use:     "shell [OPTIONS] <SPEC>...",
		Short:   "New workspace with an interactive shell",
		Args:    allArgs(atLeastOneArg("a repo spec"), validSpecs),
		PreRunE: a.noJSON,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			opts.specs = args
			return a.runShell(cmd.Context(), &opts)
		})),
	}
	addCreateFlags(cmd, &opts.name, &opts.offline)
	return cmd
}

// runShell creates the workspace, runs the shell in it, and removes the
// workspace when the shell exits (ADR 0015). The lock creation took is the
// session lock: ws holds it, and passes it to the shell, from creation
// until the workspace is gone, so the workspace never looks abandoned in
// between. Once the shell has run, ws exits with its status (ADR 0016).
func (a *App) runShell(ctx context.Context, opts *shellOptions) error {
	ws, lock, err := a.workspaces.Create(ctx, workspace.CreateOptions{Name: opts.name, Specs: parseSpecs(opts.specs), Offline: opts.offline})
	if err != nil {
		a.printWarnings()
		return domainError(err)
	}
	defer lock.Unlock()
	a.printWarnings()
	status, err := a.shellSession(ctx, &ws, lock)
	if err != nil {
		return err
	}
	if status != 0 {
		return &shellExitError{status: status}
	}
	return nil
}

// shellSession records the shell session and runs the shell with the
// workspace's lock. The workspace is removed in every case: after the
// shell, while ws still catches the signals the shell runner handles
// (ADR 0023), or right away when the session can't be recorded.
func (a *App) shellSession(ctx context.Context, ws *workspace.Workspace, lock *lockfile.Lock) (int, error) {
	if err := a.workspaces.RecordSession(ws.ID, workspace.SessionShell, ""); err != nil {
		a.removeWorkspace(ws.ID)
		return 0, err
	}
	return a.shell.Run(ctx, shell.Session{
		Dir:       ws.Path,
		Env:       []string{"WS_ID=" + ws.ID, "WS_WORKSPACE=" + ws.Path},
		Lock:      lock.File(),
		AfterExit: func() { a.removeWorkspace(ws.ID) },
	})
}

// removeWorkspace removes a workspace whose session has ended or never
// started: after its shell exits, whatever its exit status, or after its
// agent failed to start. A failure is reported but doesn't change ws's
// exit status, which is the shell's (ADR 0016) or the start's error; the
// workspace is left, with its lock free, for a later removal (ADR 0015).
func (a *App) removeWorkspace(id string) {
	if err := a.workspaces.Remove(id, workspace.RemoveOptions{}); err != nil {
		fmt.Fprintf(a.stderr, "ws: couldn't remove workspace %s: %v\n", id, err)
	}
}
