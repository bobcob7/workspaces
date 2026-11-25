package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envagent"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// childScope is the scope a new workspace is given, and where it came from:
// granted, inherited or narrowed (ADR 0082).
type childScope struct {
	scope  envscope.Scope
	source string
}

// selfRef is this machine's environment: the name and id it was enrolled
// with, or local when it isn't enrolled.
func (a *App) selfRef() (envscope.Ref, error) {
	st, err := envagent.StatusOf(envagent.NewFiles(a.paths.Environment), a.config.Environments.RenewDays())
	if err != nil {
		return envscope.Ref{}, fmt.Errorf("reading this machine's identity: %w", err)
	}
	if !st.Enrolled {
		return localRef(), nil
	}
	return envscope.Ref{ID: st.ID, Name: st.Name}, nil
}

// localRef is the environment of the workspaces on the controller.
func localRef() envscope.Ref {
	return envscope.Ref{ID: envscope.Local, Name: envscope.Local}
}

// workspaceScope is workspace id's scope on this machine. A missing scope file
// is this machine alone; so is an invalid one, with a warning: fail closed (ADR 0082).
func (a *App) workspaceScope(id string) envscope.Scope {
	self, err := a.selfRef()
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("ignoring this machine's identity: %v", err))
		self = localRef()
	}
	scope, _, err := envscope.Read(a.paths.State, id, self)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("ignoring the scope of %s: %v", id, err))
		return envscope.Only(self)
	}
	return scope
}

// callScope runs one scope operation for this machine's environment self: on
// the daemon when self is local, on the agent socket otherwise. Its error
// reply keeps its code (ADR 0082).
func (a *App) callScope(ctx context.Context, self envscope.Ref, op string, args any) (json.RawMessage, error) {
	client, err := a.scopeClient(ctx, self)
	if err != nil {
		return nil, err
	}
	reply, err := client.Scope(ctx, op, args)
	if err != nil {
		return nil, a.scopeCallError(self, err)
	}
	if !reply.OK {
		return nil, scopeReplyError(reply.Error)
	}
	return reply.Result, nil
}

// scopeClient is the client of the scope endpoint for self: the daemon on the
// controller, the agent socket on a remote environment.
func (a *App) scopeClient(ctx context.Context, self envscope.Ref) (scopeCaller, error) {
	if self.ID == envscope.Local {
		return a.daemonClient(ctx)
	}
	return daemon.Dial(daemon.Paths{Socket: a.daemonPaths().AgentSocket}), nil
}

// scopeCallError is err from a scope call. An agent socket that isn't there
// is environment_offline with reason agent_unreachable; a daemon that isn't
// running is daemon_unavailable.
func (a *App) scopeCallError(self envscope.Ref, err error) error {
	if !errors.Is(err, daemon.ErrNotRunning) {
		return callError(err)
	}
	if self.ID == envscope.Local {
		return daemonUnavailable(err, a.daemonPaths())
	}
	ce := newError(codeEnvironmentOffline, fmt.Errorf("the agent of %s isn't reachable: %w", self.Name, err))
	ce.details = map[string]any{"environment": self.Name, "reason": "agent_unreachable"}
	return ce
}

// scopeReplyError is the error for a scope operation's error reply: its code,
// or internal with details.remote_code when ws doesn't know the code.
func scopeReplyError(remote *envproto.ReplyError) error {
	if remote == nil {
		return newError(codeInternal, errors.New("the scope operation failed without saying why"))
	}
	code := errorCode(remote.Code)
	details := map[string]any{}
	maps.Copy(details, remote.Details)
	if !code.known() {
		code = codeInternal
		details["remote_code"] = remote.Code
	}
	ce := newError(code, errors.New(remote.Message))
	ce.details = details
	return ce
}

// grantScope is the scope a person grants a new workspace: this machine and
// names, or [environments] default_scope when names is empty. Every name must
// be local or an enrolled environment, else environment_unknown (ADR 0082).
func (a *App) grantScope(names []string) (envscope.Scope, error) {
	refs, err := a.requestedRefs(names)
	if err != nil {
		return envscope.Scope{}, err
	}
	return envscope.New(localRef(), refs)
}

// requestedRefs resolves names, or [environments] default_scope when names is
// empty, to environments: local, or enrolled ones, else environment_unknown.
func (a *App) requestedRefs(names []string) ([]envscope.Ref, error) {
	if len(names) == 0 {
		names = a.config.Environments.DefaultScope
	}
	recs, err := a.envStore().List()
	if err != nil {
		return nil, envError(err)
	}
	ids := make(map[string]string, len(recs))
	for i := range recs {
		ids[recs[i].Name] = recs[i].ID
	}
	refs := make([]envscope.Ref, 0, len(names))
	for _, name := range names {
		if name == localEnvironment {
			refs = append(refs, localRef())
			continue
		}
		id, ok := ids[name]
		if !ok {
			return nil, newError(codeEnvironmentUnknown, fmt.Errorf("%w: %s (ws env list shows them)", environment.ErrUnknown, name))
		}
		refs = append(refs, envscope.Ref{ID: id, Name: name})
	}
	return refs, nil
}

// writeChildScope writes child's scope file when scope holds more than this
// machine. A failure is a warning: the child then holds this machine alone.
func (a *App) writeChildScope(child string, cs childScope) {
	if !cs.scope.WiderThanSelf() {
		return
	}
	file := envscope.FileFor(cs.scope, cs.source, nil, a.now())
	if err := envscope.Write(a.paths.State, child, file); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't record the scope of %s: %v", child, err))
	}
}

// narrowedScope is the scope of a child of caller that is narrowed to names,
// or caller's own, inherited, when names is empty. A name outside caller's
// scope is environment_out_of_scope (ADR 0082).
func (a *App) narrowedScope(caller string, names []string) (childScope, error) {
	base := a.workspaceScope(caller)
	if len(names) == 0 {
		return childScope{scope: base, source: "inherited"}, nil
	}
	ids, err := scopeIDsOf(base, names)
	if err != nil {
		return childScope{}, err
	}
	narrowed, err := base.Narrow(base.Self(), ids)
	if err != nil {
		return childScope{}, scopeError(err)
	}
	return childScope{scope: narrowed, source: "narrowed"}, nil
}

// scopeIDsOf is the ids of names in scope, in order. The first name the scope
// doesn't hold is environment_out_of_scope.
func scopeIDsOf(scope envscope.Scope, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	for _, name := range names {
		ref, ok := scope.ByName(name)
		if !ok {
			return nil, scopeError(&envscope.OutOfScopeError{Environment: name, Allowed: scope.Names()})
		}
		ids = append(ids, ref.ID)
	}
	return ids, nil
}

// scopeError is err as ws's error: an out-of-scope environment is
// environment_out_of_scope, with its name and the environments allowed.
func scopeError(err error) error {
	outOfScope, ok := errors.AsType[*envscope.OutOfScopeError](err)
	if !ok {
		return err
	}
	ce := newError(codeEnvironmentOutOfScope, fmt.Errorf("environment %s isn't in your scope (%s); ask a person to start you with --env-scope", outOfScope.Environment, strings.Join(outOfScope.Allowed, ", ")))
	ce.details = map[string]any{"environment": outOfScope.Environment, "allowed": slices.Clone(outOfScope.Allowed)}
	return ce
}

// refuseScopeFlagsInWorkspace refuses --env and --env-scope from inside a
// workspace: an agent never grants a scope or picks an environment with
// command; it starts children with ws machine spawn (ADR 0082).
func (a *App) refuseScopeFlagsInWorkspace(command, env string, envScope []string) error {
	if env == "" && len(envScope) == 0 {
		return nil
	}
	id, err := a.enclosingOr("")
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	return usageErrorf("an agent never grants a scope or picks an environment with %s; use ws machine spawn --env", command)
}

// runRemoteSpawn starts a child of caller on environment opts.target through
// the controller, which checks the child's scope and records it in the ledger
// (ADR 0082, design 1.2).
func (a *App) runRemoteSpawn(ctx context.Context, opts *machineSpawnOptions, caller string, self envscope.Ref) error {
	scope := a.workspaceScope(caller)
	if _, err := scopeIDsOf(scope, []string{opts.target}); err != nil {
		return err
	}
	if _, err := scopeIDsOf(scope, opts.narrow); err != nil {
		return err
	}
	create, spawn, err := a.remoteSpawnArgs(opts)
	if err != nil {
		return err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	result, err := a.callScope(ctx, self, "spawn", envproto.ScopeSpawnArgs{
		ScopeCallerArgs: callerArgs(caller, list, scope),
		Target:          opts.target,
		Narrow:          optionalNames(opts.narrow),
		Create:          *create,
		Spawn:           spawn,
	})
	if err != nil {
		return err
	}
	return a.printRemoteCreated(result, opts.target)
}

// remoteSpawnArgs is the create arguments and the role record of a child on
// another environment. A role is rendered here, as ws machine spawn renders it
// for a local child, and its record goes with the child (ADR 0050).
func (a *App) remoteSpawnArgs(opts *machineSpawnOptions) (*envproto.CreateArgs, *envproto.SpawnInfo, error) {
	if opts.role == "" {
		create, err := remoteCreateArgs(&opts.claudeOptions, "")
		return create, nil, err
	}
	ro, err := a.loadRole(opts.role)
	if err != nil {
		return nil, nil, err
	}
	vars, err := loop.ParseVars(opts.vars)
	if err != nil {
		return nil, nil, newError(codeUsage, err)
	}
	var extra string
	var after []string
	if len(opts.claudeArgs) > 0 {
		extra, after = opts.claudeArgs[0], opts.claudeArgs[1:]
	}
	prompt, err := ro.Render(vars, extra)
	if err != nil {
		return nil, nil, newError(codeUsage, err)
	}
	_, model, err := remotePromptArgs(after)
	if err != nil {
		return nil, nil, err
	}
	if model == "" {
		model = ro.Model
	}
	roleOpts := opts.claudeOptions
	roleOpts.claudeArgs = []string{prompt}
	if model != "" {
		roleOpts.claudeArgs = append(roleOpts.claudeArgs, "--model", model)
	}
	create, err := remoteCreateArgs(&roleOpts, "")
	if err != nil {
		return nil, nil, err
	}
	spawn := &envproto.SpawnInfo{Role: ro.Name, Model: ro.Model, Pass: ro.Pass, Fail: ro.Fail, Vars: vars}
	return create, spawn, nil
}

// callerArgs are the arguments every scope operation shares: the caller's
// workspace, its ancestors and its scope (ADR 0082).
func callerArgs(caller string, list []workspace.Workspace, scope envscope.Scope) envproto.ScopeCallerArgs {
	return envproto.ScopeCallerArgs{Workspace: &caller, Ancestors: machine.Ancestors(list, caller), Scope: scope.IDs()}
}

// optionalNames is names, or nil when there are none, so the JSON says null.
func optionalNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	return slices.Clone(names)
}
