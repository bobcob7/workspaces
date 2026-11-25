package cli

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
)

// newMachineEnvsCmd is ws machine envs: the environments the caller may start
// children on (ADR 0082).
func (a *App) newMachineEnvsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "envs",
		Short: "List the environments your children may start on",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runMachineEnvs(cmd.Context())
		})),
	}
}

// machineEnvsJSON is ws machine envs' output (design 1.3).
type machineEnvsJSON struct {
	Environments []envproto.ScopeEnv `json:"environments"`
	Warnings     []string            `json:"warnings"`
}

// runMachineEnvs prints the environments the caller's scope holds, from the
// controller when this machine has more than itself, and from the scope file
// when the controller can't be asked (ADR 0082, design 1.3).
func (a *App) runMachineEnvs(ctx context.Context) error {
	caller, err := a.caller()
	if err != nil {
		return err
	}
	self, err := a.selfRef()
	if err != nil {
		return newError(codeInternal, err)
	}
	scope := a.workspaceScope(caller)
	if !scope.WiderThanSelf() {
		return a.writeEnvsJSON(scopeEnvsOf(scope, self, "online"))
	}
	envs, err := a.scopeEnvs(ctx, caller, scope, self)
	if err == nil {
		return a.writeEnvsJSON(envs)
	}
	if !controllerUnreachable(err) {
		return err
	}
	a.warned = append(a.warned, "couldn't ask the controller which environments you can use; showing your scope file, with their state unknown")
	return a.writeEnvsJSON(scopeEnvsOf(scope, self, "unknown"))
}

// scopeEnvs asks the controller for the environments of caller's scope.
func (a *App) scopeEnvs(ctx context.Context, caller string, scope envscope.Scope, self envscope.Ref) ([]envproto.ScopeEnv, error) {
	list, err := a.workspaces.List()
	if err != nil {
		return nil, err
	}
	args := envproto.ScopeEnvsArgs{ScopeCallerArgs: callerArgs(caller, list, scope)}
	raw, err := a.callScope(ctx, self, "envs", args)
	if err != nil {
		return nil, err
	}
	var res envproto.ScopeEnvsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, newError(codeInternal, err)
	}
	return res.Environments, nil
}

// scopeEnvsOf lists every environment of scope with the given state, self
// first, then by name.
func scopeEnvsOf(scope envscope.Scope, self envscope.Ref, state string) []envproto.ScopeEnv {
	envs := make([]envproto.ScopeEnv, 0, len(scope.Refs()))
	for _, ref := range scope.Refs() {
		envs = append(envs, envproto.ScopeEnv{Name: ref.Name, ID: ref.ID, State: state, Current: ref.ID == self.ID})
	}
	slices.SortFunc(envs, func(x, y envproto.ScopeEnv) int {
		switch {
		case x.Current != y.Current && x.Current:
			return -1
		case x.Current != y.Current:
			return 1
		}
		return strings.Compare(x.Name, y.Name)
	})
	return envs
}

// controllerUnreachable reports whether err means the controller can't be
// asked: the daemon isn't running, or this machine's agent isn't reachable.
func controllerUnreachable(err error) bool {
	ce, ok := errors.AsType[*cliError](err)
	return ok && (ce.code == codeEnvironmentOffline || ce.code == codeDaemonUnavailable)
}

// writeEnvsJSON prints ws machine envs' object, always JSON.
func (a *App) writeEnvsJSON(envs []envproto.ScopeEnv) error {
	return a.writeJSON(machineEnvsJSON{Environments: envs, Warnings: a.warnings()})
}
