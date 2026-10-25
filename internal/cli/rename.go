package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) newRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename [OPTIONS] <WORKSPACE> <NAME>",
		Short: "Change a workspace's display name",
		Args:  exactArgs(2, "arguments: a workspace and a name"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runRename(args[0], args[1])
		})),
	}
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
		return domainError(err)
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
