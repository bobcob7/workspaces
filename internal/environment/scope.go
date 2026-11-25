package environment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
)

// ErrWorkspaceNotFound is returned by LocalScopes for a workspace that doesn't exist here.
var ErrWorkspaceNotFound = errors.New("workspace not found")

const (
	// sweepInterval is how often the controller sweeps the scope ledger (design §2.6).
	sweepInterval = time.Minute
	// snapshotFresh is how old a snapshot may be and still say what an environment runs.
	snapshotFresh = 90 * time.Second
	// stateRemoved is the scope state of an environment with no record.
	stateRemoved = "removed"
	// codeUsage is the error code of a request the controller refuses as malformed.
	codeUsage = "usage"
)

// localRef is the controller's own environment in a scope.
func localRef() envscope.Ref {
	return envscope.Ref{ID: envscope.Local, Name: envscope.Local}
}

// SetLocalScopes sets where local workspaces' scopes and the scope ledger come
// from. Call it before Start.
func (c *Controller) SetLocalScopes(l LocalScopes, ledger *envscope.Ledger) {
	c.local = l
	c.ledger = ledger
}

// Scope runs one scope operation for caller ("local" or an environment id)
// and returns the reply to send. op is "envs", "spawn", "children" or
// "child"; args the JSON body. It never returns a Go error: failures are
// error replies with ws error codes.
func (c *Controller) Scope(ctx context.Context, caller, op string, args json.RawMessage) envproto.Reply {
	return c.scope(ctx, caller, op, args, "")
}

// scope is Scope. requestID is the up request's id, which a spawn reuses on
// the target, or "" for a fresh one.
func (c *Controller) scope(ctx context.Context, caller, op string, args json.RawMessage, requestID string) envproto.Reply {
	wire := scopeWireOp(op)
	if wire == "" {
		return scopeFailure(&scopeError{code: codeUsage, message: fmt.Sprintf("unknown scope operation %q", op)})
	}
	if c.ledger == nil || c.local == nil {
		return scopeFailure(errors.New("scopes aren't set up"))
	}
	parsed, err := envproto.DecodeArgs(wire, args)
	if err != nil {
		return scopeFailure(err)
	}
	switch a := parsed.(type) {
	case *envproto.ScopeEnvsArgs:
		return c.scopeEnvs(caller, &a.ScopeCallerArgs) //nolint:contextcheck // the scope ledger reads take no context (ADR 0082)
	case *envproto.ScopeSpawnArgs:
		return c.scopeSpawn(ctx, caller, a, requestID)
	case *envproto.ScopeChildrenArgs:
		return c.scopeChildren(caller, a) //nolint:contextcheck // the scope ledger reads take no context (ADR 0082)
	case *envproto.ScopeChildArgs:
		return c.scopeChild(ctx, caller, a)
	}
	return scopeFailure(&scopeError{code: codeUsage, message: "not a scope operation"})
}

// scopeWireOp is the envproto operation of a scope operation name, or "".
func scopeWireOp(op string) string {
	switch op {
	case "envs":
		return envproto.OpScopeEnvs
	case "spawn":
		return envproto.OpScopeSpawn
	case "children":
		return envproto.OpScopeChildren
	case "child":
		return envproto.OpScopeChild
	}
	return ""
}

// scopeEnvs lists the environments in the caller's scope and their states.
func (c *Controller) scopeEnvs(caller string, a *envproto.ScopeCallerArgs) envproto.Reply {
	scope, err := c.effectiveScope(caller, a)
	if err != nil {
		return scopeFailure(err)
	}
	refs := scope.Refs()
	envs := make([]envproto.ScopeEnv, 0, len(refs))
	for _, ref := range refs {
		envs = append(envs, envproto.ScopeEnv{Name: ref.Name, ID: ref.ID, State: c.envState(ref.ID), Current: ref.ID == caller})
	}
	return okReply(envproto.ScopeEnvsResult{Environments: envs})
}

// envState is the scope state of environment id: "online", "offline" or "removed".
func (c *Controller) envState(id string) string {
	if id == envscope.Local {
		return envproto.StatusOnline
	}
	rec, err := c.store.ByID(id)
	if err != nil || rec.State != StateEnrolled {
		return stateRemoved
	}
	if c.isOnline(id) {
		return envproto.StatusOnline
	}
	return envproto.StatusOffline
}

// isOnline reports whether environment id's last hello said it is online.
func (c *Controller) isOnline(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.presence[id].online
}

// effectiveScope is the scope a caller's operation sees: a person's grant
// (on the daemon socket) or an environment's ledger ceiling, narrowed to
// what its request claims.
func (c *Controller) effectiveScope(caller string, a *envproto.ScopeCallerArgs) (envscope.Scope, error) {
	if caller == envscope.Local {
		return c.localScope(a)
	}
	return c.environmentScope(caller, a)
}

// localScope is the caller's own workspace's scope file, ignoring any claim;
// a grant with no workspace is every enrolled environment.
func (c *Controller) localScope(a *envproto.ScopeCallerArgs) (envscope.Scope, error) {
	if a.Workspace != nil {
		return c.local.Scope(*a.Workspace)
	}
	recs, err := c.store.List()
	if err != nil {
		return envscope.Scope{}, err
	}
	refs := make([]envscope.Ref, 0, len(recs))
	for i := range recs {
		if recs[i].State == StateEnrolled {
			refs = append(refs, envscope.Ref{ID: recs[i].ID, Name: recs[i].Name})
		}
	}
	return envscope.New(localRef(), refs)
}

// environmentScope is the ledger ceiling for environment caller's workspace
// and its ancestors (the first entry on that chain), narrowed to the claim.
// Without an entry the scope is the caller alone.
func (c *Controller) environmentScope(caller string, a *envproto.ScopeCallerArgs) (envscope.Scope, error) {
	if a.Workspace == nil {
		return envscope.Scope{}, &scopeError{code: codeUsage, message: "a scope operation needs a workspace"}
	}
	rec, err := c.store.ByID(caller)
	if err != nil {
		return envscope.Scope{}, err
	}
	self := envscope.Ref{ID: rec.ID, Name: rec.Name}
	ceiling, err := c.ledger.Ceiling(caller, append([]string{*a.Workspace}, a.Ancestors...))
	if err != nil {
		return envscope.Scope{}, err
	}
	if ceiling == nil {
		return envscope.Only(self), nil
	}
	refs, err := c.refsOf(ceiling)
	if err != nil {
		return envscope.Scope{}, err
	}
	ceilingScope, err := envscope.New(self, refs)
	if err != nil {
		return envscope.Scope{}, err
	}
	return ceilingScope.Intersect(a.Scope), nil
}

// refsOf names the environments with ids; removed environments drop out.
func (c *Controller) refsOf(ids []string) ([]envscope.Ref, error) {
	refs := make([]envscope.Ref, 0, len(ids))
	for _, id := range ids {
		if id == envscope.Local {
			refs = append(refs, localRef())
			continue
		}
		rec, err := c.store.ByID(id)
		if errors.Is(err, ErrUnknown) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if rec.State == StateEnrolled {
			refs = append(refs, envscope.Ref{ID: rec.ID, Name: rec.Name})
		}
	}
	return refs, nil
}

// refNamed is the environment called name, or environment_unknown.
func (c *Controller) refNamed(name string) (envscope.Ref, error) {
	if name == envscope.Local {
		return localRef(), nil
	}
	rec, err := c.store.Get(name)
	if errors.Is(err, ErrUnknown) || (err == nil && rec.State != StateEnrolled) {
		return envscope.Ref{}, &scopeError{
			code:    "environment_unknown",
			message: "no environment called " + name,
			details: map[string]any{"environment": name},
		}
	}
	if err != nil {
		return envscope.Ref{}, err
	}
	return envscope.Ref{ID: rec.ID, Name: rec.Name}, nil
}

// scopeSpawn creates a child on the target environment with the scope the
// caller may grant, and records it in the ledger.
func (c *Controller) scopeSpawn(ctx context.Context, caller string, a *envproto.ScopeSpawnArgs, requestID string) envproto.Reply {
	eff, err := c.effectiveScope(caller, &a.ScopeCallerArgs) //nolint:contextcheck // the scope ledger reads take no context (ADR 0082)
	if err != nil {
		return scopeFailure(err)
	}
	target, err := c.spawnTarget(caller, eff, a.Target)
	if err != nil {
		return scopeFailure(err)
	}
	child, err := c.childScope(eff, target, a.Narrow)
	if err != nil {
		return scopeFailure(err)
	}
	if err = c.requireScopes(target); err != nil {
		return scopeFailure(err)
	}
	create := a.Create
	create.Scope = scopeGrant(caller, a.Workspace, child)
	create.Spawn = a.Spawn
	reply, err := c.forward(ctx, target.Name, envproto.OpWorkspaceCreate, create, requestID)
	if err != nil {
		return scopeFailure(err)
	}
	if !reply.OK {
		return reply
	}
	return c.recordChild(ctx, caller, a, target, child, reply)
}

// spawnTarget is the environment a spawn names, when the caller may start a child there.
func (c *Controller) spawnTarget(caller string, eff envscope.Scope, name string) (envscope.Ref, error) {
	target, err := c.refNamed(name)
	if err != nil {
		return envscope.Ref{}, err
	}
	if !eff.Contains(target.ID) {
		return envscope.Ref{}, &envscope.OutOfScopeError{Environment: name, Allowed: eff.Names()}
	}
	if target.ID == envscope.Local && caller != envscope.Local {
		return envscope.Ref{}, &scopeError{code: codeUsage, message: "starting a child on local from an environment isn't supported yet"}
	}
	if target.ID == caller {
		return envscope.Ref{}, &scopeError{code: codeUsage, message: "spawn on your own environment without --env"}
	}
	return target, nil
}

// childScope is the scope of a child on target: the caller's when narrow is
// null, else the named environments of the caller's scope, with target itself.
func (c *Controller) childScope(eff envscope.Scope, target envscope.Ref, narrow []string) (envscope.Scope, error) {
	if narrow == nil {
		return envscope.New(target, eff.Refs())
	}
	ids := make([]string, 0, len(narrow))
	for _, name := range narrow {
		ref, err := c.refNamed(name)
		if err != nil {
			return envscope.Scope{}, err
		}
		if !eff.Contains(ref.ID) {
			return envscope.Scope{}, &envscope.OutOfScopeError{Environment: name, Allowed: eff.Names()}
		}
		ids = append(ids, ref.ID)
	}
	return eff.Narrow(target, ids)
}

// requireScopes refuses a target whose agent is online but too old to create a child with a scope.
func (c *Controller) requireScopes(target envscope.Ref) error {
	c.mu.Lock()
	h := c.presence[target.ID]
	c.mu.Unlock()
	if !h.online || h.hello == nil || h.hello.Protocol >= envproto.ProtocolScopes {
		return nil
	}
	return &OutdatedError{
		Environment: target.Name,
		Op:          envproto.OpWorkspaceCreate,
		WSVersion:   h.hello.WSVersion,
		Protocol:    h.hello.Protocol,
		Need:        envproto.ProtocolScopes,
	}
}

// scopeGrant is the scope a child is created with: child's refs, and the
// caller's workspace as its parent (null for a person's grant).
func scopeGrant(caller string, workspace *string, child envscope.Scope) *envproto.ScopeGrant {
	refs := child.Refs()
	envs := make([]envproto.EnvRef, 0, len(refs))
	for _, ref := range refs {
		envs = append(envs, envproto.EnvRef{ID: ref.ID, Name: ref.Name})
	}
	grant := &envproto.ScopeGrant{Environments: envs}
	if workspace != nil {
		grant.Parent = &envproto.WorkspaceAt{Environment: caller, Workspace: *workspace}
	}
	return grant
}

// recordChild writes the ledger entry for the child a target created, and
// returns the target's result with the environment and scope names added. A
// failed write removes the child again.
func (c *Controller) recordChild(ctx context.Context, caller string, a *envproto.ScopeSpawnArgs, target envscope.Ref, child envscope.Scope, reply envproto.Reply) envproto.Reply {
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(reply.Result, &created); err != nil || created.ID == "" {
		return scopeFailure(errors.New("the child's id is missing from its create reply"))
	}
	entry := envscope.Entry{
		Environment: target.ID,
		Workspace:   created.ID,
		Scope:       child.IDs(),
		Parent:      ledgerParent(caller, a.Workspace),
		Role:        spawnRole(a.Spawn),
		CreatedAt:   c.now().UTC(),
	}
	if err := c.ledger.Put(ctx, &entry); err != nil {
		if _, rmErr := c.forward(ctx, target.Name, envproto.OpWorkspaceRm, envproto.RmArgs{Workspace: created.ID}, ""); rmErr != nil {
			c.log.WarnContext(ctx, "orphaned child not removed", "environment", target.Name, "error", rmErr)
		}
		return scopeFailure(fmt.Errorf("recording the child: %w", err))
	}
	return spawnReply(reply, target.Name, child)
}

// ledgerParent is the ledger parent of a child spawned from workspace ws on caller; nil for a person's grant.
func ledgerParent(caller string, workspace *string) *envscope.Parent {
	if workspace == nil {
		return nil
	}
	return &envscope.Parent{Environment: caller, Workspace: *workspace}
}

// spawnRole is the child's role, or nil when it has none.
func spawnRole(spawn *envproto.SpawnInfo) *string {
	if spawn == nil {
		return nil
	}
	role := spawn.Role
	return &role
}

// spawnReply is the target's create result with the environment and the child's scope names added.
func spawnReply(reply envproto.Reply, name string, child envscope.Scope) envproto.Reply {
	result := map[string]any{}
	if err := json.Unmarshal(reply.Result, &result); err != nil {
		return scopeFailure(fmt.Errorf("decoding the create reply: %w", err))
	}
	result["environment"] = name
	result["scope"] = child.Names()
	return okReply(result)
}

// scopeChildren lists the ledger children of the caller's workspace and its descendants.
func (c *Controller) scopeChildren(caller string, a *envproto.ScopeChildrenArgs) envproto.Reply {
	if a.Workspace == nil {
		return scopeFailure(&scopeError{code: codeUsage, message: "children needs a workspace"})
	}
	parents := append([]string{*a.Workspace}, a.Descendants...)
	entries, err := c.ledger.List()
	if err != nil {
		return scopeFailure(err)
	}
	children := []envproto.ScopeChild{}
	for i := range entries {
		e := &entries[i]
		if e.Parent == nil || e.Parent.Environment != caller || !slices.Contains(parents, e.Parent.Workspace) {
			continue
		}
		rec, err := c.store.ByID(e.Environment)
		if err != nil {
			continue
		}
		children = append(children, envproto.ScopeChild{
			Environment: rec.Name,
			Workspace:   e.Workspace,
			Parent:      e.Parent.Workspace,
			Role:        e.Role,
			Entry:       c.childEntry(&rec, e.Workspace),
		})
	}
	return okReply(envproto.ScopeChildrenResult{Children: children})
}

// childEntry is the ws list object for workspace ws in rec's snapshot, or null.
func (c *Controller) childEntry(rec *Record, ws string) json.RawMessage {
	_, snap, err := c.store.Snapshot(rec.Name)
	if err != nil {
		return json.RawMessage("null")
	}
	if entry, ok := snapshotWorkspace(&snap, ws); ok {
		return entry
	}
	return json.RawMessage("null")
}

// snapshotWorkspace is the ws list object for ws in snap, when snap lists it.
func snapshotWorkspace(snap *envproto.Snapshot, ws string) (json.RawMessage, bool) {
	var list []json.RawMessage
	if err := json.Unmarshal(snap.Workspaces, &list); err != nil {
		return nil, false
	}
	for _, raw := range list {
		var item struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &item); err == nil && item.ID == ws {
			return raw, true
		}
	}
	return nil, false
}

// scopeChild forwards one operation to a child the caller spawned, when the
// ledger says the caller or one of its descendants is the child's parent.
func (c *Controller) scopeChild(ctx context.Context, caller string, a *envproto.ScopeChildArgs) envproto.Reply {
	rec, err := c.store.Get(a.Environment)
	if err != nil && !errors.Is(err, ErrUnknown) {
		return scopeFailure(err)
	}
	if err != nil || rec.State != StateEnrolled {
		return scopeFailure(workspaceNotFound(a.Child))
	}
	entry, found, err := c.ledger.Get(rec.ID, a.Child) //nolint:contextcheck // the scope ledger reads take no context (ADR 0082)
	if err != nil {
		return scopeFailure(err)
	}
	if !found {
		return scopeFailure(workspaceNotFound(a.Child))
	}
	if entry.Parent == nil || entry.Parent.Environment != caller || entry.Parent.Workspace != a.Parent {
		return scopeFailure(&scopeError{
			code:    "not_descendant",
			message: "workspace " + a.Child + " isn't a descendant of your workspace",
			details: map[string]any{"workspace": a.Child},
		})
	}
	reply, err := c.forwardChild(ctx, rec.Name, a)
	if err != nil {
		return scopeFailure(err)
	}
	if a.Op == "rm" && (reply.OK || errorCode(reply) == "workspace_not_found") {
		if err := c.ledger.Remove(ctx, rec.ID, a.Child); err != nil {
			c.log.WarnContext(ctx, "scope ledger entry not removed", "child", a.Child, "error", err)
		}
	}
	return reply
}

// forwardChild sends a child's status, logs, rm or record operation to environment name.
func (c *Controller) forwardChild(ctx context.Context, name string, a *envproto.ScopeChildArgs) (envproto.Reply, error) {
	switch a.Op {
	case "status":
		return c.forward(ctx, name, envproto.OpAgentStatus, envproto.WorkspaceArgs{Workspace: a.Child}, "")
	case "logs":
		return c.forward(ctx, name, envproto.OpAgentLogs, envproto.LogsArgs{Workspace: a.Child, Tail: a.Tail}, "")
	case "rm":
		return c.forward(ctx, name, envproto.OpWorkspaceRm, envproto.RmArgs{Workspace: a.Child, KeepChildren: a.KeepChildren}, "")
	case "record":
		return c.forward(ctx, name, envproto.OpMachineRecord, envproto.WorkspaceArgs{Workspace: a.Child}, "")
	}
	return envproto.Reply{}, &scopeError{code: codeUsage, message: "op must be status, logs, rm or record"}
}

// forward sends op with args to environment name and waits for its reply,
// under the op's timeout. requestID "" makes a fresh request id.
func (c *Controller) forward(ctx context.Context, name, op string, args any, requestID string) (envproto.Reply, error) {
	spec, ok := envproto.LookupOp(op)
	if !ok {
		return envproto.Reply{}, fmt.Errorf("%s: %w", op, ErrNotForwarded)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return envproto.Reply{}, fmt.Errorf("encoding %s: %w", op, err)
	}
	if requestID == "" {
		if requestID, err = newRequestID(); err != nil {
			return envproto.Reply{}, err
		}
	}
	req := CallRequest{Op: op, Args: raw, RequestID: requestID, Deadline: time.Now().Add(spec.Timeout)}
	return c.call(ctx, name, &req, envproto.CallerController, nil)
}

// workspaceNotFound is the error for a child the ledger doesn't hold.
func workspaceNotFound(ws string) error {
	return &scopeError{code: "workspace_not_found", message: "no workspace " + ws + " in the scope ledger"}
}

// sweepLedger drops the ledger entries whose child or parent is gone, and
// removes a child whose parent is gone (design §2.6).
func (c *Controller) sweepLedger(ctx context.Context) {
	if c.ledger == nil {
		return
	}
	entries, err := c.ledger.List() //nolint:contextcheck // the scope ledger reads take no context (ADR 0082)
	if err != nil {
		c.log.WarnContext(ctx, "scope ledger not swept", "error", err)
		return
	}
	for i := range entries {
		c.sweepEntry(ctx, &entries[i])
	}
}

// sweepEntry keeps, drops or removes the child of one ledger entry.
func (c *Controller) sweepEntry(ctx context.Context, e *envscope.Entry) {
	rec, err := c.store.ByID(e.Environment)
	if err != nil {
		c.dropEntry(ctx, e)
		return
	}
	if c.absentFromSnapshot(&rec, e.Workspace) {
		c.dropEntry(ctx, e)
		return
	}
	if !c.parentGone(e) {
		return
	}
	reply, err := c.forward(ctx, rec.Name, envproto.OpWorkspaceRm, envproto.RmArgs{Workspace: e.Workspace}, "")
	if err != nil {
		return
	}
	if reply.OK || errorCode(reply) == "workspace_not_found" {
		c.dropEntry(ctx, e)
	}
}

// absentFromSnapshot reports whether rec is online with a fresh snapshot that doesn't list ws.
func (c *Controller) absentFromSnapshot(rec *Record, ws string) bool {
	if !c.isOnline(rec.ID) {
		return false
	}
	snap, fresh := c.freshSnapshot(rec.Name)
	if !fresh {
		return false
	}
	_, listed := snapshotWorkspace(&snap, ws)
	return !listed
}

// parentGone reports whether the parent of a ledger entry is gone: a local
// workspace that doesn't exist, or a remote one its environment no longer lists.
func (c *Controller) parentGone(e *envscope.Entry) bool {
	if e.Parent == nil {
		return false
	}
	if e.Parent.Environment == envscope.Local {
		exists, err := c.local.Exists(e.Parent.Workspace)
		return err == nil && !exists
	}
	rec, err := c.store.ByID(e.Parent.Environment)
	if err != nil {
		return true
	}
	return c.absentFromSnapshot(&rec, e.Parent.Workspace)
}

// freshSnapshot is the snapshot of environment name, when it arrived within snapshotFresh.
func (c *Controller) freshSnapshot(name string) (envproto.Snapshot, bool) {
	file, snap, err := c.store.Snapshot(name)
	if err != nil {
		return envproto.Snapshot{}, false
	}
	received, err := time.Parse(time.RFC3339, file.ReceivedAt)
	if err != nil || c.now().Sub(received) > snapshotFresh {
		return envproto.Snapshot{}, false
	}
	return snap, true
}

// dropEntry removes a ledger entry, logging a failure.
func (c *Controller) dropEntry(ctx context.Context, e *envscope.Entry) {
	if err := c.ledger.Remove(ctx, e.Environment, e.Workspace); err != nil {
		c.log.WarnContext(ctx, "scope ledger entry not dropped", "workspace", e.Workspace, "error", err)
	}
}

// sweepEvery sweeps the scope ledger every interval until ctx ends.
func (c *Controller) sweepEvery(ctx context.Context, interval time.Duration) {
	ticks, stop := c.newTicker(interval)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			c.sweepLedger(ctx)
		}
	}
}

// scopeError is a scope operation's refusal: its error code, message and details.
type scopeError struct {
	code    string
	message string
	details map[string]any
}

// Error is the refusal's message.
func (e *scopeError) Error() string {
	return e.message
}

// scopeFailure is the error reply for err.
func scopeFailure(err error) envproto.Reply {
	return envproto.Reply{Error: scopeReplyError(err)}
}

// scopeReplyError is err as the error part of a reply, with its ws code.
func scopeReplyError(err error) *envproto.ReplyError {
	if fault, ok := errors.AsType[*scopeError](err); ok {
		return &envproto.ReplyError{Code: fault.code, Message: fault.message, Details: fault.details}
	}
	if args, ok := errors.AsType[*envproto.ArgsError](err); ok {
		return &envproto.ReplyError{Code: args.Code, Message: args.Message}
	}
	if out, ok := errors.AsType[*envscope.OutOfScopeError](err); ok {
		return &envproto.ReplyError{Code: "environment_out_of_scope", Message: out.Error(), Details: map[string]any{
			"environment": out.Environment,
			"allowed":     out.Allowed,
		}}
	}
	if errors.Is(err, ErrWorkspaceNotFound) {
		return &envproto.ReplyError{Code: "workspace_not_found", Message: err.Error()}
	}
	if outdated, ok := errors.AsType[*OutdatedError](err); ok {
		return &envproto.ReplyError{Code: "environment_outdated", Message: err.Error(), Details: map[string]any{
			"environment":   outdated.Environment,
			"op":            outdated.Op,
			"ws_version":    outdated.WSVersion,
			"protocol":      outdated.Protocol,
			"need_protocol": outdated.Need,
		}}
	}
	if offline, ok := errors.AsType[*OfflineError](err); ok {
		return &envproto.ReplyError{Code: "environment_offline", Message: err.Error(), Details: offlineReplyDetails(offline)}
	}
	return &envproto.ReplyError{Code: "internal", Message: "the scope operation failed"}
}

// offlineReplyDetails is the details of an environment_offline reply.
func offlineReplyDetails(offline *OfflineError) map[string]any {
	var lastSeen any
	if offline.LastSeen != nil {
		lastSeen = offline.LastSeen.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"environment": offline.Environment,
		"reason":      offline.Reason,
		"last_seen":   lastSeen,
		"request_id":  offline.RequestID,
		"op":          offline.Op,
	}
}

// okReply is a successful reply carrying result.
func okReply(result any) envproto.Reply {
	raw, err := json.Marshal(result)
	if err != nil {
		return scopeFailure(err)
	}
	return envproto.Reply{OK: true, Result: raw}
}

// errorCode is the code of reply's error, or "".
func errorCode(reply envproto.Reply) string {
	if reply.Error == nil {
		return ""
	}
	return reply.Error.Code
}

// encodeReply is reply as the JSON an agent reads.
func encodeReply(reply envproto.Reply) []byte {
	body, err := json.Marshal(reply)
	if err != nil {
		return envproto.ErrorReply("internal", "reply not encoded")
	}
	return body
}

// scopeOpName is the scope operation name of an agent's scope.* request op.
func scopeOpName(op string) string {
	return strings.TrimPrefix(op, "scope.")
}
