package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// remoteTarget is where a workspace command runs for --env: "" and local
// run here (remote false), and a remote environment's name is returned with
// remote true.
func (a *App) remoteTarget(env string) (name string, remote bool, err error) {
	if env == "" {
		return "", false, nil
	}
	selected, err := a.selectEnvironments([]string{env})
	if err != nil {
		return "", false, err
	}
	if selected[0] == localEnvironment {
		return "", false, nil
	}
	return selected[0], true, nil
}

// remoteHint is workspace_not_found "it is on <name>; pass --env <name>"
// when exactly one environment's snapshot lists id (D9). It is nil when no
// environment, or more than one, lists it.
func (a *App) remoteHint(id string) error {
	found, err := a.environmentsWith(id)
	if err == nil && len(found) == 1 {
		return newError(codeWorkspaceNotFound, fmt.Errorf("it is on %s; pass --env %s", found[0], found[0]))
	}
	return nil
}

// withRemoteHint is err, or the remoteHint error when err says the workspace
// id isn't here.
func (a *App) withRemoteHint(id string, err error) error {
	if !errors.Is(err, workspace.ErrNotFound) {
		return err
	}
	if hint := a.remoteHint(id); hint != nil {
		return hint
	}
	return err
}

// remoteWorkspaceOp runs op on env for each id, in order, and prints each
// result with "environment" added (PR 3's D9); it stops at the first error.
func (a *App) remoteWorkspaceOp(ctx context.Context, env, op string, argsFor func(id string) any, ids []string) error {
	for _, id := range ids {
		raw, err := a.callEnvironment(ctx, env, op, argsFor(id))
		if err != nil {
			return err
		}
		if err := a.printRemoteResult(op, raw, env); err != nil {
			return err
		}
	}
	return nil
}

// printRemoteResult prints an agent's result for op: the object with
// "environment" added as JSON, or the text of the result.
func (a *App) printRemoteResult(op string, raw json.RawMessage, env string) error {
	if !a.global.json {
		return a.printRemoteText(op, raw, env)
	}
	object, err := remoteObject(raw, env)
	if err != nil {
		return err
	}
	name, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encoding the environment name: %w", err)
	}
	object["environment"] = name
	if err := addWarnings(object, a.warned); err != nil {
		return err
	}
	return a.writeJSON(object)
}

// remoteNotes are the warnings every workspace result carries.
type remoteNotes struct {
	Warnings []string `json:"warnings"`
}

// printRemoteText prints the text of an agent's result for op, one line per
// workspace, and its warnings on stderr. A removal the agent failed is the
// error.
func (a *App) printRemoteText(op string, raw json.RawMessage, env string) error {
	var notes remoteNotes
	if err := json.Unmarshal(raw, &notes); err != nil {
		return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	for _, w := range slices.Concat(a.warned, notes.Warnings) {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	switch op {
	case envproto.OpWorkspaceRm:
		return a.printRemoteRm(raw, env)
	case envproto.OpWorkspaceHide:
		var hidden hideJSON
		if err := json.Unmarshal(raw, &hidden); err != nil {
			return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
		}
		fmt.Fprintf(a.stdout, "%s  %s\n", hidden.ID, visibilityText(hidden.Hidden))
	case envproto.OpWorkspaceRename:
		var renamed renameJSON
		if err := json.Unmarshal(raw, &renamed); err != nil {
			return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
		}
		fmt.Fprintf(a.stdout, "%s  %s\n", renamed.ID, renamed.Name)
	}
	return nil
}

// printRemoteRm prints a line for each workspace the agent removed, a
// descendant marked "└ ". The first item the agent failed to remove is
// returned, with environment in its details.
func (a *App) printRemoteRm(raw json.RawMessage, env string) error {
	var result struct {
		Workspaces []rmJSON `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	for _, item := range result.Workspaces {
		if item.Error != nil {
			ce := newError(item.Error.Code, errors.New(item.Error.Message))
			ce.details = map[string]any{"environment": env}
			return ce
		}
		id := item.ID
		if item.Parent != nil {
			id = "└ " + id
		}
		fmt.Fprintf(a.stdout, "removed  %s\n", id)
	}
	return nil
}
