package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/envproto"
)

func (a *App) newAttachCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:     "attach [OPTIONS] <WORKSPACE>",
		Short:   "Attach this terminal to a workspace's Claude Code agent",
		Long:    "Attach this terminal to a workspace's Claude Code agent.\n\nLocally this runs `claude attach`; with --env it attaches through the daemon to the agent on that environment.",
		Args:    exactArgs(1, "workspace"),
		PreRunE: a.noJSON,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runAttach(cmd.Context(), env, args[0])
		})),
	}
	cmd.Flags().StringVar(&env, "env", "", "attach to the agent on this environment (ws env list); default: local")
	return cmd
}

// runAttach attaches this terminal to the Claude Code agent of workspace id,
// here or on env. It needs a terminal on stdin.
func (a *App) runAttach(ctx context.Context, env, id string) error {
	if !a.stdinTerminal() {
		return usageErrorf("ws attach needs a terminal")
	}
	name, remote, err := a.remoteTarget(env)
	if err != nil {
		return err
	}
	if remote {
		workspace := id
		_, err := a.runRemoteTerminal(ctx, name, envproto.TerminalOpenArgs{Kind: envproto.TermAttach, Workspace: &workspace, Specs: []string{}})
		return err
	}
	return a.attachLocal(ctx, id)
}

// attachLocal hands the terminal to `claude attach` for the session of
// workspace id, which replaces ws.
func (a *App) attachLocal(ctx context.Context, id string) error {
	ws, err := a.workspaces.Find(id)
	if err != nil {
		return domainError(a.withRemoteHint(id, err))
	}
	session, err := a.agentSessionID(ctx, ws.ID)
	if err != nil {
		return domainError(err)
	}
	if session == "" {
		return usageErrorf("workspace %s has no Claude Code agent: only a Claude agent can be attached", ws.ID)
	}
	if err := a.claude.Attach(ctx, session); err != nil {
		return domainError(fmt.Errorf("attaching to session %s: %w", session, err))
	}
	return nil
}
