package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// defaultRemoteWaitPoll is how often ws machine wait asks an environment about
// a child there (design 1.4).
const defaultRemoteWaitPoll = 10 * time.Second

// sessionDone is the session state of a finished agent that has stopped for
// good; with lifetime.StateStopped it ends a remote child's wait without a
// result.
const sessionDone = "done"

// unknownSession is the status of a remote child whose environment didn't say.
const unknownSession = "unknown"

// remoteChild is a child on another environment, from the controller's ledger
// (ADR 0082). envID names its copy under the caller's machine root.
type remoteChild struct {
	env    string
	envID  string
	id     string
	parent string
	role   *string
	entry  json.RawMessage
	rollup *envscope.Steps
}

// remoteChildren are the caller's children on other environments, from the
// controller, and the warnings for the environments that couldn't be asked.
// Nothing is asked when the caller's scope is this machine alone and there is
// no ledger, so a default run makes no daemon call (design 1.4).
func (a *App) remoteChildren(ctx context.Context, caller string, descendants []string) ([]remoteChild, []string) {
	self, err := a.selfRef()
	if err != nil {
		return nil, []string{fmt.Sprintf("couldn't ask for children on other environments: %v", err)}
	}
	scope := a.workspaceScope(caller)
	if !scope.WiderThanSelf() && self.ID == envscope.Local && !a.ledgerExists() {
		return nil, nil
	}
	list, err := a.workspaces.List()
	if err != nil {
		return nil, []string{fmt.Sprintf("couldn't ask for children on other environments: %v", err)}
	}
	raw, err := a.callScope(ctx, self, "children", envproto.ScopeChildrenArgs{
		ScopeCallerArgs: callerArgs(caller, list, scope),
		Descendants:     descendants,
	})
	if err != nil {
		return nil, []string{fmt.Sprintf("couldn't list the children on other environments: %v", err)}
	}
	return remoteChildrenOf(raw, scope)
}

// ledgerExists reports whether the controller has a scope ledger.
func (a *App) ledgerExists() bool {
	return envscope.LedgerExists(a.paths.Environments)
}

// remoteChildrenOf decodes a scope.children reply. A child whose environment
// isn't in scope has no id to keep its copy under, so it is a warning.
func remoteChildrenOf(raw json.RawMessage, scope envscope.Scope) ([]remoteChild, []string) {
	var res envproto.ScopeChildrenResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, []string{fmt.Sprintf("couldn't read the children on other environments: %v", err)}
	}
	var children []remoteChild
	var warnings []string
	for _, c := range res.Children {
		ref, ok := scope.ByName(c.Environment)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("ignoring child %s on %s: that environment isn't in your scope", c.Workspace, c.Environment))
			continue
		}
		if c.Rollup != nil && rollupSteps(c.Rollup) == nil {
			warnings = append(warnings, fmt.Sprintf("ignoring the rollup of %s on %s: out of range", c.Workspace, c.Environment))
		}
		children = append(children, remoteChild{env: c.Environment, envID: ref.ID, id: c.Workspace, parent: c.Parent, role: c.Role, entry: c.Entry, rollup: rollupSteps(c.Rollup)})
	}
	return children, warnings
}

// rollupSteps is the steps of a rollup the controller sent, or nil when it sent
// none or one that isn't Valid.
func rollupSteps(r *envproto.ScopeRollup) *envscope.Steps {
	if r == nil {
		return nil
	}
	steps := envscope.Steps{Total: r.Total, Done: r.Done}
	if !steps.Valid() {
		return nil
	}
	return &steps
}

// remoteSteps is rc's rollup from the controller, or the zero Rollup when it
// sent none.
func remoteSteps(rc *remoteChild) machine.Rollup {
	if rc.rollup == nil {
		return machine.Rollup{}
	}
	return machine.Rollup{Total: rc.rollup.Total, Done: rc.rollup.Done}
}

// remoteRollup is rc's rollup from the controller as ws machine prints it, or
// nil when the controller sent none.
func remoteRollup(rc *remoteChild) *rollupJSON {
	if rc.rollup == nil {
		return nil
	}
	r := rollupInfo(remoteSteps(rc))
	return &r
}

// remoteChildOp runs op on child rc through the controller (scope.child). extra
// holds the operation's own arguments: "tail" for logs, "keep_children" for rm.
func (a *App) remoteChildOp(ctx context.Context, rc *remoteChild, op string, extra map[string]any) (json.RawMessage, error) {
	self, err := a.selfRef()
	if err != nil {
		return nil, newError(codeInternal, err)
	}
	caller, err := a.caller()
	if err != nil {
		return nil, err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	scope := a.workspaceScope(caller)
	args := envproto.ScopeChildArgs{
		ScopeCallerArgs: callerArgs(caller, list, scope),
		Environment:     rc.env,
		Child:           rc.id,
		Parent:          rc.parent,
		Op:              op,
	}
	args.Tail, _ = extra["tail"].(int)
	args.KeepChildren, _ = extra["keep_children"].(bool)
	return a.callScope(ctx, self, "child", args)
}

// remoteRecord is the machine record of child rc on its environment: its role,
// result, report and progress. A finished child's result is copied under root
// the first time it is seen (design 2.5); the returned record is the copy once
// there is one.
func (a *App) remoteRecord(ctx context.Context, root string, rc *remoteChild) (machine.Record, *envproto.MachineRecordResult, error) {
	raw, err := a.remoteChildOp(ctx, rc, "record", nil)
	if err != nil {
		return machine.Record{}, nil, err
	}
	var res envproto.MachineRecordResult
	if decodeErr := json.Unmarshal(raw, &res); decodeErr != nil {
		return machine.Record{}, nil, newError(codeInternal, fmt.Errorf("reading the record of %s: %w", rc.id, decodeErr))
	}
	store := machine.NewStore(a.paths.State)
	if res.Result != nil {
		if copyErr := a.copyBack(root, rc, &res); copyErr != nil {
			return machine.Record{}, nil, copyErr
		}
	}
	rec, readErr := store.ReadRemote(root, rc.envID, rc.id)
	return rec, &res, readErr
}

// copyBack keeps the result and report of child rc under root, when they are
// not there already. A child with a role also keeps a spawn record whose pass
// and fail lists reproduce the verdict its environment judged, since the
// record reply doesn't carry the role's lists.
func (a *App) copyBack(root string, rc *remoteChild, res *envproto.MachineRecordResult) error {
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	ancestors := append([]string{rc.parent}, machine.Ancestors(list, rc.parent)...)
	result := machine.Result{Verdict: res.Result.Verdict, Ancestors: ancestors, RecordedAt: res.Result.RecordedAt}
	var spawn *machine.Spawn
	if rc.role != nil {
		spawn = judgedSpawn(*rc.role, res.Result, ancestors)
	}
	var report []byte
	if res.Report != nil {
		report = []byte(*res.Report)
	}
	if res.ReportTruncated {
		a.warned = append(a.warned, fmt.Sprintf("the report of %s on %s was cut down by its environment", rc.id, rc.env))
	}
	return machine.NewStore(a.paths.State).WriteRemote(root, rc.envID, rc.id, spawn, &result, report)
}

// judgedSpawn is the spawn record of a role whose verdict the environment
// judged: the verdict is in its pass list, or in its fail list.
func judgedSpawn(role string, r *envproto.RecordResult, ancestors []string) *machine.Spawn {
	sp := &machine.Spawn{Role: role, Ancestors: ancestors, SpawnedAt: r.RecordedAt}
	if machine.Outcome(r.Outcome) == machine.OutcomePass {
		sp.Pass = []string{r.Verdict}
		return sp
	}
	sp.Fail = []string{r.Verdict}
	return sp
}

// remoteStatus is the session id and state of child rc on its environment, or
// "unknown" when the environment can't say.
func (a *App) remoteStatus(ctx context.Context, rc *remoteChild) (session, state string, err error) {
	raw, err := a.remoteChildOp(ctx, rc, "status", nil)
	if err != nil {
		return "", "unknown", err
	}
	var res envproto.AgentStatusResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "unknown", newError(codeInternal, fmt.Errorf("reading the status of %s: %w", rc.id, err))
	}
	if res.Agent == nil {
		return res.Session, "unknown", nil
	}
	return res.Session, res.Agent.State, nil
}

// remoteMachineChild is a remote child in ws machine children: its entry from
// its environment's snapshot, and its role, result and progress. An
// environment that can't be asked leaves the copy's result and a warning.
func (a *App) remoteMachineChild(ctx context.Context, root string, rc *remoteChild) machineChildJSON {
	env := rc.env
	out := machineChildJSON{listWorkspaceJSON: remoteEntry(rc), Role: rc.role}
	out.Environment = &env
	rec, res, err := a.remoteRecord(ctx, root, rc)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read %s on %s: %v", rc.id, rc.env, err))
		rec, _ = machine.NewStore(a.paths.State).ReadRemote(root, rc.envID, rc.id)
	}
	out.Result = resultInfo(&rec)
	if res != nil {
		out.Progress, out.Rollup = a.remoteProgress(res)
	}
	if r := remoteRollup(rc); r != nil {
		out.Rollup = r
	}
	return out
}

// remoteEntry is the ws list object of rc from its environment's snapshot. A
// child the snapshot doesn't list has only its id.
func remoteEntry(rc *remoteChild) listWorkspaceJSON {
	var entry listWorkspaceJSON
	if len(rc.entry) > 0 && string(rc.entry) != "null" {
		if err := json.Unmarshal(rc.entry, &entry); err != nil {
			entry = listWorkspaceJSON{}
		}
	}
	if entry.ID == nil {
		id := rc.id
		entry.ID = &id
	}
	return entry
}

// remoteProgress is a remote child's own progress and rollup, from the last
// machine.record reply.
func (a *App) remoteProgress(res *envproto.MachineRecordResult) (*progressJSON, *rollupJSON) {
	var own *progressJSON
	if err := json.Unmarshal(res.Progress, &own); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read the progress of %s: %v", res.Workspace, err))
		return nil, nil
	}
	var rollup *rollupJSON
	if err := json.Unmarshal(res.Rollup, &rollup); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read the rollup of %s: %v", res.Workspace, err))
		return own, nil
	}
	return own, rollup
}

// remoteByID is the remote child whose id is id, if there is one.
func remoteByID(children []remoteChild, id string) (remoteChild, bool) {
	i := slices.IndexFunc(children, func(c remoteChild) bool { return c.id == id })
	if i < 0 {
		return remoteChild{}, false
	}
	return children[i], true
}

// descendantIDs is the ids of workspace id's descendants in list, in order.
func descendantIDs(list []workspace.Workspace, id string) []string {
	below := descendants(list, id)
	ids := make([]string, 0, len(below))
	for i := range list {
		if below[list[i].ID] {
			ids = append(ids, list[i].ID)
		}
	}
	return ids
}

// hasErrorCode reports whether err is a cli error with code.
func hasErrorCode(err error, code errorCode) bool {
	ce, ok := errors.AsType[*cliError](err)
	return ok && ce.code == code
}

// remoteLogs is ws machine logs for child rc: the tail of its log on its
// environment, as text.
func (a *App) remoteLogs(ctx context.Context, rc *remoteChild, tail int) (machineLogsJSON, error) {
	raw, err := a.remoteChildOp(ctx, rc, "logs", map[string]any{"tail": tail})
	if err != nil {
		return machineLogsJSON{}, err
	}
	var res envproto.AgentLogsResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return machineLogsJSON{}, newError(codeInternal, fmt.Errorf("reading the log of %s: %w", rc.id, err))
	}
	text := strings.Join(res.Lines, "\n")
	env := rc.env
	return machineLogsJSON{
		ID:          rc.id,
		Session:     optional(res.Session),
		Events:      []agent.Event{},
		Text:        &text,
		Truncated:   res.Truncated,
		Environment: &env,
		Warnings:    a.warnings(),
	}, nil
}

// listRemoteChildren is remoteChildren, with its warnings kept for the output.
func (a *App) listRemoteChildren(ctx context.Context, caller string, descendants []string) []remoteChild {
	children, warnings := a.remoteChildren(ctx, caller, descendants)
	a.warned = append(a.warned, warnings...)
	return children
}

// directRemoteIDs is the ids of the remote children that caller started.
func directRemoteIDs(children []remoteChild, caller string) []string {
	var ids []string
	for i := range children {
		if children[i].parent == caller {
			ids = append(ids, children[i].id)
		}
	}
	return ids
}

// attachRemote makes each target that names no local workspace the target of
// the remote child with its id, when the ledger has one.
func attachRemote(targets []rmTarget, children []remoteChild) []rmTarget {
	for i := range targets {
		t := &targets[i]
		if t.ws.ID != "" || t.swept != nil {
			continue
		}
		if rc, ok := remoteByID(children, t.arg); ok {
			targets[i] = rmTarget{arg: t.arg, remote: &rc}
		}
	}
	return targets
}

// removeRemote removes child rc on its environment, or under --dry-run only
// lists it. An environment that can't be reached keeps the child.
func (a *App) removeRemote(ctx context.Context, rc *remoteChild, opts *rmOptions, items *itemResults) rmJSON {
	env := rc.env
	out := rmJSON{ID: rc.id, Parent: optional(rc.parent), Environment: &env}
	if opts.dryRun {
		items.add(rc.id, nil)
		return out
	}
	_, err := a.remoteChildOp(ctx, rc, "rm", map[string]any{"keep_children": opts.keepChildren})
	out.Removed = err == nil
	out.Error = items.add(rc.id, err)
	return out
}

// remoteLogsOf is ws machine logs for arg when arg is a child on another
// environment and err says it isn't a local child; otherwise err.
func (a *App) remoteLogsOf(ctx context.Context, caller string, entries []lifetime.Entry, arg string, tail int, err error) error {
	if !hasErrorCode(err, codeNotDescendant) && !hasErrorCode(err, codeWorkspaceNotFound) {
		return err
	}
	rc, ok := remoteByID(a.listRemoteChildren(ctx, caller, descendantIDs(entryWorkspaces(entries), caller)), arg)
	if !ok {
		return err
	}
	out, logErr := a.remoteLogs(ctx, &rc, tail)
	if logErr != nil {
		return logErr
	}
	return a.writeJSON(out)
}

// copiedWaitTarget is the wait target of a child removed from its environment
// whose copy is kept under root and was started by caller or below. Without
// such a copy, notFound is the error.
func copiedWaitTarget(store *machine.Store, root, caller, id string, notFound error) (waitTarget, error) {
	envID, found, err := store.FindRemote(root, id)
	if err != nil {
		return waitTarget{}, fmt.Errorf("looking for %s's copy: %w", id, err)
	}
	if !found {
		return waitTarget{}, domainError(notFound)
	}
	rec, err := store.ReadRemote(root, envID, id)
	if err != nil {
		return waitTarget{}, fmt.Errorf("reading %s's copy: %w", id, err)
	}
	if !slices.Contains(rec.Ancestors(), caller) {
		return waitTarget{}, domainError(notFound)
	}
	return waitTarget{id: id, gone: true, caller: caller, remote: &remoteChild{env: envID, envID: envID, id: id}}, nil
}

// environment is the environment a wait target runs on: its name, or local.
func (t *waitTarget) environment() string {
	if t.remote != nil {
		return t.remote.env
	}
	return localEnvironment
}

// pollRemoteTarget is a remote target's state. Its environment is asked at
// most once every remotePoll; between asks the last answer stands.
func (a *App) pollRemoteTarget(ctx context.Context, t *waitTarget, root string) childState {
	if t.gone {
		return a.copiedChildState(t, root)
	}
	now := a.now()
	if !t.polled.IsZero() && now.Sub(t.polled) < a.remotePoll {
		return t.last
	}
	t.polled = now
	t.last = a.askRemote(ctx, t, root)
	return t.last
}

// askRemote is a live remote child's state from its environment: finished with
// a result, or with a session that is done or stopped.
func (a *App) askRemote(ctx context.Context, t *waitTarget, root string) childState {
	rec, _, err := a.remoteRecord(ctx, root, t.remote)
	switch {
	case hasErrorCode(err, codeWorkspaceNotFound):
		return a.copiedChildState(t, root)
	case hasErrorCode(err, codeNotDescendant):
		t.refused = err
		return childState{status: unknownSession, finished: true}
	case err != nil:
		a.warnRemote(t, err)
		rec, _ = machine.NewStore(a.paths.State).ReadRemote(root, t.remote.envID, t.id)
		return childState{status: unknownSession, record: rec}
	}
	session, state, err := a.remoteStatus(ctx, t.remote)
	if err != nil {
		a.warnRemote(t, err)
	}
	st := childState{status: state, session: session, record: rec}
	st.finished = rec.Result != nil || state == sessionDone || state == string(lifetime.StateStopped)
	st.quiet = st.finished
	return st
}

// copiedChildState is a child removed from its environment, finished, with
// the record of its copy under root.
func (a *App) copiedChildState(t *waitTarget, root string) childState {
	rec, err := machine.NewStore(a.paths.State).ReadRemote(root, t.remote.envID, t.id)
	if err != nil {
		a.warnRemote(t, err)
	}
	if t.caller != "" && !slices.Contains(rec.Ancestors(), t.caller) {
		rec = machine.Record{Child: t.id}
	}
	return childState{status: waitGone, finished: true, record: rec}
}

// warnRemote reports that environment of t couldn't be asked, once per target.
func (a *App) warnRemote(t *waitTarget, err error) {
	if t.warned {
		return
	}
	t.warned = true
	a.warned = append(a.warned, fmt.Sprintf("couldn't ask %s on %s: %v", t.id, t.environment(), err))
}

// nestRemoteProgress adds each remote child of the caller under its parent in
// node, as a leaf (design 1.4).
func (a *App) nestRemoteProgress(ctx context.Context, node *progressNodeJSON) error {
	caller, _, err := a.progressCaller()
	if err != nil {
		return err
	}
	list, err := a.workspaces.List()
	if err != nil {
		return fmt.Errorf("listing workspaces: %w", err)
	}
	for _, rc := range a.listRemoteChildren(ctx, caller, descendantIDs(list, caller)) {
		addLeaf(node, rc.parent, a.remoteLeaf(ctx, &rc))
	}
	return nil
}

// remoteLeaf is a remote child in ws machine progress show: its progress and
// rollup as its last machine.record reported them, or none when its
// environment can't say.
func (a *App) remoteLeaf(ctx context.Context, rc *remoteChild) progressNodeJSON {
	leaf := progressNodeJSON{ID: rc.id, Children: []progressNodeJSON{}}
	if r := remoteRollup(rc); r != nil {
		leaf.Rollup = *r
	}
	raw, err := a.remoteChildOp(ctx, rc, "record", nil)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read the progress of %s on %s: %v", rc.id, rc.env, err))
		return leaf
	}
	var res envproto.MachineRecordResult
	if err := json.Unmarshal(raw, &res); err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read the progress of %s on %s: %v", rc.id, rc.env, err))
		return leaf
	}
	own, rollup := a.remoteProgress(&res)
	leaf.Progress = own
	if rc.rollup == nil && rollup != nil {
		leaf.Rollup = *rollup
	}
	return leaf
}

// addLeaf appends leaf under the node named parent, in n or below it, adds its
// rollup to every node on the way down, and reports whether it found parent.
func addLeaf(n *progressNodeJSON, parent string, leaf progressNodeJSON) bool {
	if n.ID == parent {
		n.Children = append(n.Children, leaf)
		n.Rollup = addRollup(n.Rollup, leaf.Rollup)
		return true
	}
	for i := range n.Children {
		if addLeaf(&n.Children[i], parent, leaf) {
			n.Rollup = addRollup(n.Rollup, leaf.Rollup)
			return true
		}
	}
	return false
}
