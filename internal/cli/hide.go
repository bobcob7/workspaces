package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *App) newHideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "hide [OPTIONS] <WORKSPACE>",
		Short: "Hide a workspace from ws list and the desktop app",
		Args:  exactArgs(1, "argument: a workspace"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runSetHidden(args[0], true)
		})),
	}
}

func (a *App) newUnhideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unhide [OPTIONS] <WORKSPACE>",
		Short: "Show a hidden workspace again",
		Args:  exactArgs(1, "argument: a workspace"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.runSetHidden(args[0], false)
		})),
	}
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
		return domainError(err)
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
