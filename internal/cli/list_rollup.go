package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"

	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// mergeRollups adds remote children's steps to every entry's rollup (ADR 0083
// D1): local entries get their descendants' ledger children, remote entries
// get their merged rollup. Nothing is read without a scope ledger.
func (a *App) mergeRollups(entries []listEntryJSON) {
	if !envscope.LedgerExists(a.paths.Environments) {
		return
	}
	ledger, err := envscope.NewLedger(a.paths.Environments).List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the scope ledger: %v", err))
		return
	}
	list, err := a.workspaces.List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("listing workspaces: %v", err))
		return
	}
	merger := envscope.NewMerger(a.snapshotListed(), ledger)
	ids := a.environmentIDs()
	for i := range entries {
		e := &entries[i]
		if e.ID == nil {
			continue
		}
		if e.Environment == localEnvironment {
			e.Rollup = localMergedRollup(merger, list, *e.ID, e.Rollup)
			continue
		}
		id, ok := ids[e.Environment]
		if !ok {
			continue
		}
		if steps, listed := merger.Of(id, *e.ID); listed {
			e.Rollup = rollupOfSteps(steps)
		}
	}
}

// localMergedRollup is id's own rollup plus the merged rollups of the ledger
// children of id and its descendants.
func localMergedRollup(m *envscope.Merger, list []workspace.Workspace, id string, own *rollupJSON) *rollupJSON {
	below := m.Below(envscope.Local, append([]string{id}, descendantIDs(list, id)...))
	return rollupOfSteps(stepsOf(own).Add(below))
}

// snapshotListed is every enrolled environment's snapshot as Listed, read
// once each; a snapshot that can't be read or decoded is a warning.
func (a *App) snapshotListed() []envscope.Listed {
	recs, err := a.envStore().List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the environments: %v", err))
		return nil
	}
	var listed []envscope.Listed
	for i := range recs {
		if recs[i].State == environment.StateEnrolled {
			listed = append(listed, a.listedOf(&recs[i])...)
		}
	}
	return listed
}

// listedOf is the workspaces of rec's snapshot; none when it has no snapshot
// yet, or when it can't be read or decoded, which is a warning.
func (a *App) listedOf(rec *environment.Record) []envscope.Listed {
	_, snap, err := a.envStore().Snapshot(rec.Name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the snapshot of %s: %v", rec.Name, err))
		return nil
	}
	listed, invalid, err := envscope.ListedOf(rec.ID, snap.Workspaces)
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't decode the workspaces of %s: %v", rec.Name, err))
		return nil
	}
	for _, id := range invalid {
		a.warnOnce(fmt.Sprintf("ignoring the rollup of %s on %s: out of range", id, rec.Name))
	}
	return listed
}

// environmentIDs is the id of each environment in the store by its name.
func (a *App) environmentIDs() map[string]string {
	recs, err := a.envStore().List()
	if err != nil {
		return nil
	}
	ids := make(map[string]string, len(recs))
	for i := range recs {
		ids[recs[i].Name] = recs[i].ID
	}
	return ids
}

// warnOnce records msg as a warning unless it already is one.
func (a *App) warnOnce(msg string) {
	if !slices.Contains(a.warned, msg) {
		a.warned = append(a.warned, msg)
	}
}

// stepsOf is a rollup's steps, none for a null rollup.
func stepsOf(r *rollupJSON) envscope.Steps {
	if r == nil {
		return envscope.Steps{}
	}
	return envscope.Steps{Total: r.Total, Done: r.Done}
}

// rollupOfSteps is steps as a rollup, null when no step is planned.
func rollupOfSteps(s envscope.Steps) *rollupJSON {
	if s.Total == 0 {
		return nil
	}
	info := rollupInfo(machine.Rollup{Total: s.Total, Done: s.Done})
	return &info
}
