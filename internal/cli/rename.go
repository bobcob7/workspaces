package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func (a *App) newRenameCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "rename [OPTIONS] <WORKSPACE> <NAME>",
		Short: "Change a workspace's display name",
		Args:  exactArgs(2, "arguments: a workspace and a name"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			name, remote, err := a.remoteTarget(env)
			if err != nil {
				return err
			}
			if remote {
				argsFor := func(id string) any { return envproto.RenameArgs{Workspace: id, Name: args[1]} }
				return a.remoteWorkspaceOp(cmd.Context(), name, envproto.OpWorkspaceRename, argsFor, args[:1])
			}
			return a.runRename(args[0], args[1])
		})),
	}
	cmd.Flags().StringVar(&env, "env", "", "rename the workspace on this environment (ws env list); default: local")
	return cmd
}

// renameJSON is ws rename's output (docs/tools/ws-rename.md).
type renameJSON struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Path     string   `json:"path"`
	Warnings []string `json:"warnings"`
}

// runRename sets the display name of the workspace arg names. Its id and
// directory stay as they are (ADR 0036).
func (a *App) runRename(arg, name string) error {
	ws, err := a.workspaces.Find(arg)
	if err != nil {
		return domainError(a.withRemoteHint(arg, err))
	}
	ws, err = a.workspaces.Rename(ws.ID, name)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		return a.writeJSON(renameJSON{ID: ws.ID, Name: ws.Name, Path: ws.Path, Warnings: a.warnings()})
	}
	fmt.Fprintf(a.stdout, "%s  %s\n", ws.ID, ws.Name)
	a.printWarnings()
	return nil
}
