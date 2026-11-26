package cli

import (
	"context"
	"fmt"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
)

// scopeRemoved is the state of a scope environment the store no longer
// enrolls.
const scopeRemoved = "removed"

// scopeParentJSON is a cross-environment child's parent: the environment it
// runs on ("local" or its name) and its workspace id (ADR 0082).
type scopeParentJSON struct {
	Environment string `json:"environment"`
	Workspace   string `json:"workspace"`
}

// ledgerKey names a ledger entry by environment id and workspace id.
type ledgerKey struct{ env, workspace string }

// annotateScopes sets scope_parent and scope on every entry of a ws list
// --json. Remote snapshots never supply them: they are reset first, then
// filled from this machine's scope files and the controller's ledger (ADR
// 0082, design 3.1). presence is the environment presence the listing used,
// or nil to ask the daemon when a scope needs it. The ledger and scope file
// reads are local and context-free (ADR 0082), so contextcheck is silenced once,
// at the ledger call.
func (a *App) annotateScopes(ctx context.Context, entries []listEntryJSON, presence map[string]string) {
	for i := range entries {
		entries[i].ScopeParent, entries[i].Scope = nil, nil
	}
	recs, err := a.envStore().List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the environments: %v", err))
		return
	}
	states := newScopeStates(a, recs, presence)
	self := a.scopeSelf()
	ledger := a.readLedger() //nolint:contextcheck // the scope ledger and scope file reads take no context
	for i := range entries {
		e := &entries[i]
		if e.ID == nil {
			continue
		}
		if e.Environment == localEnvironment {
			e.Scope = a.localScopeOf(ctx, states, self, *e.ID)
			continue
		}
		e.ScopeParent, e.Scope = states.ledgerScope(ctx, ledger, e)
	}
}

// scopeSelf is this machine's environment, read once; local when it can't be
// read, which is a warning.
func (a *App) scopeSelf() envscope.Ref {
	self, err := a.selfRef()
	if err != nil {
		a.warnOnce(fmt.Sprintf("ignoring this machine's identity: %v", err))
		return localRef()
	}
	return self
}

// readLedger is the ledger by environment id and workspace id, or nil when
// there is none, or when it can't be read, which is a warning.
func (a *App) readLedger() map[ledgerKey]envscope.Entry {
	if !envscope.LedgerExists(a.paths.Environments) {
		return nil
	}
	entries, err := envscope.NewLedger(a.paths.Environments).List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the scope ledger: %v", err))
		return nil
	}
	byKey := make(map[ledgerKey]envscope.Entry, len(entries))
	for i := range entries {
		byKey[ledgerKey{env: entries[i].Environment, workspace: entries[i].Workspace}] = entries[i]
	}
	return byKey
}

// localScopeOf is local workspace id's scope when it is wider than self, from
// its scope file; nil for the default scope. A file that can't be read is a
// warning.
func (a *App) localScopeOf(ctx context.Context, states *scopeStates, self envscope.Ref, id string) []envproto.ScopeEnv {
	scope, _, err := envscope.Read(a.paths.State, id, self)
	if err != nil {
		a.warnOnce(fmt.Sprintf("ignoring the scope of %s: %v", id, err))
		return nil
	}
	if !scope.WiderThanSelf() {
		return nil
	}
	envs := make([]envproto.ScopeEnv, 0, len(scope.Refs()))
	for _, ref := range scope.Refs() {
		envs = append(envs, states.env(ctx, ref, self.ID))
	}
	sortScopeEnvs(envs)
	return envs
}

// scopeStates is what the scope annotations need of the environment store
// and the daemon. Presence is the listing's when it has one; else it is asked
// at most once, and only when a scope names a remote environment.
type scopeStates struct {
	app      *App
	byID     map[string]environment.Record
	byName   map[string]environment.Record
	presence map[string]string
}

func newScopeStates(a *App, recs []environment.Record, presence map[string]string) *scopeStates {
	s := &scopeStates{app: a, byID: map[string]environment.Record{}, byName: map[string]environment.Record{}, presence: presence}
	for i := range recs {
		s.byID[recs[i].ID] = recs[i]
		s.byName[recs[i].Name] = recs[i]
	}
	return s
}

// ledgerScope is the parent and wider scope of remote entry e, from the
// ledger entry for its environment and workspace; nil for both when there is
// none.
func (s *scopeStates) ledgerScope(ctx context.Context, ledger map[ledgerKey]envscope.Entry, e *listEntryJSON) (*scopeParentJSON, []envproto.ScopeEnv) {
	entry, rec, ok := s.ledgerEntryOf(ledger, e)
	if !ok {
		return nil, nil
	}
	return s.parentOf(entry.Parent), s.scopeOf(ctx, entry.Scope, rec.ID)
}

// ledgerEntryOf is the ledger entry for remote entry e, and the environment
// record it runs in; false when there is none.
func (s *scopeStates) ledgerEntryOf(ledger map[ledgerKey]envscope.Entry, e *listEntryJSON) (envscope.Entry, environment.Record, bool) {
	rec, ok := s.byName[e.Environment]
	if !ok {
		return envscope.Entry{}, environment.Record{}, false
	}
	entry, ok := ledger[ledgerKey{env: rec.ID, workspace: *e.ID}]
	return entry, rec, ok
}

// parentOf is the parent a ledger entry names, with the environment's current
// name; nil for none, or when that environment is no longer in the store.
func (s *scopeStates) parentOf(p *envscope.Parent) *scopeParentJSON {
	if p == nil {
		return nil
	}
	if p.Environment == envscope.Local {
		return &scopeParentJSON{Environment: envscope.Local, Workspace: p.Workspace}
	}
	rec, ok := s.byID[p.Environment]
	if !ok {
		return nil
	}
	return &scopeParentJSON{Environment: rec.Name, Workspace: p.Workspace}
}

// scopeOf is the wider scope a ledger entry gives an entry on environment
// envID; nil when it names no more than that environment.
func (s *scopeStates) scopeOf(ctx context.Context, ids []string, envID string) []envproto.ScopeEnv {
	if len(ids) <= 1 {
		return nil
	}
	envs := make([]envproto.ScopeEnv, 0, len(ids))
	for _, id := range ids {
		envs = append(envs, s.env(ctx, envscope.Ref{ID: id, Name: s.nameOf(id)}, envID))
	}
	sortScopeEnvs(envs)
	return envs
}

// nameOf is the current name of environment id, or the id itself when the
// store doesn't hold it.
func (s *scopeStates) nameOf(id string) string {
	if id == envscope.Local {
		return envscope.Local
	}
	if rec, ok := s.byID[id]; ok {
		return rec.Name
	}
	return id
}

// env is the scope entry for ref, on an entry whose environment is self.
// Local is online; an environment the store doesn't enroll is removed; any
// other is online when the daemon reports it so, else offline.
func (s *scopeStates) env(ctx context.Context, ref envscope.Ref, self string) envproto.ScopeEnv {
	out := envproto.ScopeEnv{Name: ref.Name, ID: ref.ID, State: envOnline, Current: ref.ID == self}
	if ref.ID == envscope.Local {
		return out
	}
	rec, ok := s.byID[ref.ID]
	if !ok || rec.State != environment.StateEnrolled {
		out.State = scopeRemoved
		return out
	}
	out.Name = rec.Name
	out.State = envOffline
	if s.presenceOf(ctx)[rec.Name] == envOnline {
		out.State = envOnline
	}
	return out
}

func (s *scopeStates) presenceOf(ctx context.Context) map[string]string {
	if s.presence == nil {
		s.presence = s.app.environmentPresence(ctx)
	}
	return s.presence
}

// treeRows nests the table's rows across environments by the ledger's scope
// parents. Without a ledger they keep their order.
func (a *App) treeRows(rows []listRow, entries []listEntryJSON) []listRow {
	if !envscope.LedgerExists(a.paths.Environments) {
		return rows
	}
	a.fillScopeParents(entries)
	return crossOrder(attachScopeParents(rows, entries))
}

// fillScopeParents sets ScopeParent on each remote entry from the ledger, and
// resets it on every entry. It reads the ledger and the environment store
// only, so it never asks the daemon.
func (a *App) fillScopeParents(entries []listEntryJSON) {
	for i := range entries {
		entries[i].ScopeParent = nil
	}
	recs, err := a.envStore().List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the environments: %v", err))
		return
	}
	states := newScopeStates(a, recs, nil)
	ledger := a.readLedger()
	for i := range entries {
		e := &entries[i]
		if e.ID == nil || e.Environment == localEnvironment {
			continue
		}
		if entry, _, ok := states.ledgerEntryOf(ledger, e); ok {
			e.ScopeParent = states.parentOf(entry.Parent)
		}
	}
}

// attachScopeParents sets each row's scope parent to the rowKey of the scope
// parent of the entry it shows.
func attachScopeParents(rows []listRow, entries []listEntryJSON) []listRow {
	parents := make(map[string]string, len(entries))
	for i := range entries {
		sp := entries[i].ScopeParent
		if sp == nil || entries[i].ID == nil {
			continue
		}
		parents[rowKey(entries[i].Environment, *entries[i].ID)] = rowKey(sp.Environment, sp.Workspace)
	}
	for i := range rows {
		rows[i].scopeParent = parents[rowKey(rows[i].env, rows[i].id)]
	}
	return rows
}
