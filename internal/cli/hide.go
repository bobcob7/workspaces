package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func (a *App) newHideCmd() *cobra.Command {
	return a.newVisibilityCmd("hide", "Hide a workspace from ws list and the desktop app", true)
}

func (a *App) newUnhideCmd() *cobra.Command {
	return a.newVisibilityCmd("unhide", "Show a hidden workspace again", false)
}

// newVisibilityCmd is ws hide (hidden true) or ws unhide (hidden false), with
// --env to do it on a remote environment (workspace.hide).
func (a *App) newVisibilityCmd(use, short string, hidden bool) *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   use + " [OPTIONS] <WORKSPACE>",
		Short: short,
		Args:  exactArgs(1, "argument: a workspace"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			envName, remote, err := a.remoteTarget(env)
			if err != nil {
				return err
			}
			if remote {
				argsFor := func(id string) any { return envproto.HideArgs{Workspace: id, Hidden: hidden} }
				return a.remoteWorkspaceOp(cmd.Context(), envName, envproto.OpWorkspaceHide, argsFor, args)
			}
			return a.runSetHidden(args[0], hidden)
		})),
	}
	cmd.Flags().StringVar(&env, "env", "", "change the workspace on this environment (ws env list); default: local")
	return cmd
}

// hideJSON is ws hide's and ws unhide's output (docs/tools/ws-hide.md).
type hideJSON struct {
	ID       string   `json:"id"`
	Name     *string  `json:"name"`
	Path     string   `json:"path"`
	Hidden   bool     `json:"hidden"`
	Warnings []string `json:"warnings"`
}

// runSetHidden hides or shows the workspace arg names; nothing else about
// it changes (ADR 0043).
func (a *App) runSetHidden(arg string, hidden bool) error {
	ws, err := a.workspaces.Find(arg)
	if err != nil {
		return domainError(a.withRemoteHint(arg, err))
	}
	ws, err = a.workspaces.SetHidden(ws.ID, hidden)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		return a.writeJSON(hideJSON{ID: ws.ID, Name: optional(ws.Name), Path: ws.Path, Hidden: ws.Hidden, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%s  %s\n", ws.ID, visibilityText(ws.Hidden))
	a.printWarnings()
	return nil
}

func visibilityText(hidden bool) string {
	if hidden {
		return "hidden"
	}
	return "visible"
}
