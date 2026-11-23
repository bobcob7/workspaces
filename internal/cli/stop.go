package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
)

func (a *App) newStopCmd() *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "stop [OPTIONS] <WORKSPACE>...",
		Short: "Stop the Claude Code agents of workspaces",
		Long:  "Stop the Claude Code agents of workspaces.\n\nLocally this runs `claude stop` on each session; with --env it stops the agent on that environment.",
		Args:  atLeastOneArg("a workspace"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.runStop(cmd.Context(), env, args)
		})),
	}
	cmd.Flags().StringVar(&env, "env", "", "stop the agents on this environment (ws env list); default: local")
	return cmd
}

// stopJSON is one workspace in ws stop's output (docs/tools/ws-stop.md).
type stopJSON struct {
	Workspace   string  `json:"workspace"`
	Session     *string `json:"session"`
	Stopped     bool    `json:"stopped"`
	Environment string  `json:"environment"`
}

// runStop stops the Claude Code agent of each workspace id, here or on env.
// An agent that has already ended is stopped false, not an error.
func (a *App) runStop(ctx context.Context, env string, ids []string) error {
	name, remote, err := a.remoteTarget(env)
	if err != nil {
		return err
	}
	stopped := []stopJSON{}
	for _, id := range ids {
		item, err := a.stopOne(ctx, name, remote, id)
		if err != nil {
			return err
		}
		stopped = append(stopped, item)
		if !a.global.json {
			fmt.Fprintf(a.stdout, "%s  %s\n", item.Workspace, stoppedText(item.Stopped))
		}
	}
	if a.global.json {
		return a.writeJSON(map[string]any{"stopped": stopped})
	}
	a.printWarnings()
	return nil
}

// stopOne stops the agent of workspace id on environment name, or here when
// remote is false.
func (a *App) stopOne(ctx context.Context, name string, remote bool, id string) (stopJSON, error) {
	if remote {
		return a.stopRemote(ctx, name, id)
	}
	return a.stopLocal(ctx, id)
}

// stopRemote stops the agent of workspace id on environment env (agent.stop).
func (a *App) stopRemote(ctx context.Context, env, id string) (stopJSON, error) {
	raw, err := a.callEnvironment(ctx, env, envproto.OpAgentStop, envproto.WorkspaceArgs{Workspace: id})
	if err != nil {
		return stopJSON{}, err
	}
	var result envproto.AgentStopResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return stopJSON{}, newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	return stopJSON{Workspace: result.Workspace, Session: &result.Session, Stopped: result.Stopped, Environment: env}, nil
}

// stopLocal stops the Claude Code session of workspace id here. A workspace
// with no session, or whose session has ended, is stopped false.
func (a *App) stopLocal(ctx context.Context, id string) (stopJSON, error) {
	ws, err := a.workspaces.Find(id)
	if err != nil {
		return stopJSON{}, domainError(a.withRemoteHint(id, err))
	}
	item := stopJSON{Workspace: ws.ID, Environment: localEnvironment}
	session, err := a.agentSessionID(ctx, ws.ID)
	if err != nil {
		return stopJSON{}, domainError(err)
	}
	if session == "" {
		return item, nil
	}
	item.Session = &session
	err = a.claude.Stop(ctx, session)
	switch {
	case errors.Is(err, claude.ErrNoSession):
		return item, nil
	case err != nil:
		return stopJSON{}, domainError(err)
	}
	item.Stopped = true
	return item, nil
}

// agentSessionID is the id of the Claude Code session of workspace id, or ""
// when it has none (a shell, or a ws runner).
func (a *App) agentSessionID(ctx context.Context, id string) (string, error) {
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return "", err
	}
	for i := range listing.Workspaces {
		entry := &listing.Workspaces[i]
		if entry.Workspace.ID != id {
			continue
		}
		if s := entry.Session; s != nil && s.Kind == lifetime.KindAgent && s.Runner != lifetime.RunnerWS {
			return s.ID, nil
		}
		break
	}
	return "", nil
}

// stoppedText is the text of a stop result.
func stoppedText(stopped bool) string {
	if stopped {
		return "stopped"
	}
	return "not stopped"
}
