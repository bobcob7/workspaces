package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/envscope"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/workspace"
)

const (
	// localEnvironment is the environment of the workspaces on this machine.
	localEnvironment = "local"
	envOnline        = "online"
	envOffline       = "offline"
	// remoteStaleAfter is how old an environment's snapshot may be before its
	// entries are stale (D3: three refresh periods).
	remoteStaleAfter = 90 * time.Second
	// presenceTimeout bounds the ask of the running daemon for presence.
	presenceTimeout = 2 * time.Second
)

// remoteListing is one environment's entries from its snapshot, as ws list
// shows them.
type remoteListing struct {
	name    string
	status  string // online, offline
	entries []listEntryJSON
}

// selectEnvironments resolves --env: none means local; each name must be
// local or an environment in the store (environment_unknown). Duplicates are
// dropped, and the order is kept.
func (a *App) selectEnvironments(names []string) ([]string, error) {
	if len(names) == 0 {
		return []string{localEnvironment}, nil
	}
	recs, err := a.envStore().List()
	if err != nil {
		return nil, envError(err)
	}
	known := map[string]bool{localEnvironment: true}
	for i := range recs {
		known[recs[i].Name] = true
	}
	seen := map[string]bool{}
	selected := make([]string, 0, len(names))
	for _, name := range names {
		if !known[name] {
			return nil, newError(codeEnvironmentUnknown, fmt.Errorf("%w: %s (ws env list shows them)", environment.ErrUnknown, name))
		}
		if !seen[name] {
			seen[name] = true
			selected = append(selected, name)
		}
	}
	return selected, nil
}

// listedEnvironments is the environments plain ws list reads (D9): local
// and every enrolled environment by name. -q reads local alone, --parent
// reads the parent's environment and its children's (parentEnvironments),
// and --env narrows to the names given.
func (a *App) listedEnvironments(opts *listOptions) ([]string, error) {
	switch {
	case opts.parent != "" && !opts.idsOnly && len(opts.envs) < 2:
		return a.parentEnvironments(opts)
	case len(opts.envs) > 0:
		return a.selectEnvironments(opts.envs)
	case opts.idsOnly:
		return []string{localEnvironment}, nil
	}
	return a.enrolledEnvironments()
}

// parentEnvironments is what --parent reads: the parent's environment, then
// the environments of its ledger children, local first then by name.
func (a *App) parentEnvironments(opts *listOptions) ([]string, error) {
	env, id, err := a.parentOf(opts)
	if err != nil {
		return nil, err
	}
	names := append([]string{env}, a.childEnvironments(a.envIDOf(env), id)...)
	return sortEnvironments(names), nil
}

// parentOf is the environment --parent runs on and the id of the parent
// workspace there: the environment --env names, else the one the parent is
// found in. -q reads local alone.
func (a *App) parentOf(opts *listOptions) (string, string, error) {
	env := localEnvironment
	switch {
	case opts.idsOnly && len(opts.envs) == 0:
	case len(opts.envs) == 1:
		env = opts.envs[0]
	default:
		envs, err := a.parentEnvironment(opts.parent)
		if err != nil {
			return "", "", err
		}
		env = envs[0]
	}
	if env != localEnvironment {
		return env, opts.parent, nil
	}
	ws, err := a.workspaces.Find(opts.parent)
	if err != nil {
		return "", "", domainError(err)
	}
	return env, ws.ID, nil
}

// sortEnvironments is names without repeats, local first, then by name.
func sortEnvironments(names []string) []string {
	slices.SortFunc(names, func(a, b string) int {
		switch {
		case a == b:
			return 0
		case a == localEnvironment:
			return -1
		case b == localEnvironment:
			return 1
		}
		return strings.Compare(a, b)
	})
	return slices.Compact(names)
}

// childEnvironments is the environments of the ledger entries whose parent is
// (envID, id), by name, sorted; nil without a ledger.
func (a *App) childEnvironments(envID, id string) []string {
	if !envscope.LedgerExists(a.paths.Environments) {
		return nil
	}
	entries, err := envscope.NewLedger(a.paths.Environments).List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the scope ledger: %v", err))
		return nil
	}
	recs, err := a.envStore().List()
	if err != nil {
		a.warnOnce(fmt.Sprintf("couldn't read the environments: %v", err))
		return nil
	}
	names := map[string]string{envscope.Local: localEnvironment}
	for i := range recs {
		names[recs[i].ID] = recs[i].Name
	}
	var out []string
	for i := range entries {
		p := entries[i].Parent
		name, known := names[entries[i].Environment]
		if p == nil || p.Environment != envID || p.Workspace != id || !known {
			continue
		}
		out = append(out, name)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// enrolledEnvironments is local, then every enrolled environment by name. A
// pending environment has no snapshot yet, so it is left out.
func (a *App) enrolledEnvironments() ([]string, error) {
	recs, err := a.envStore().List()
	if err != nil {
		return nil, envError(err)
	}
	var enrolled []string
	for i := range recs {
		if recs[i].State == environment.StateEnrolled {
			enrolled = append(enrolled, recs[i].Name)
		}
	}
	slices.Sort(enrolled)
	return append([]string{localEnvironment}, enrolled...), nil
}

// parentEnvironment is the one environment --parent runs on without --env:
// local when the parent is here, else the only environment whose snapshot
// lists it. Any other parent is workspace_not_found.
func (a *App) parentEnvironment(parent string) ([]string, error) {
	_, findErr := a.workspaces.Find(parent)
	switch {
	case findErr == nil:
		return []string{localEnvironment}, nil
	case !errors.Is(findErr, workspace.ErrNotFound):
		return nil, domainError(findErr)
	}
	found, err := a.environmentsWith(parent)
	if err != nil {
		return nil, err
	}
	switch len(found) {
	case 1:
		return found, nil
	case 0:
		return nil, domainError(findErr)
	}
	return nil, newError(codeWorkspaceNotFound, fmt.Errorf("%w: %s", workspace.ErrNotFound, parent))
}

// environmentPresence is the status the running daemon reports for each
// environment name. It asks with no autostart and a short timeout; without
// an answer the map is empty, so every environment is offline.
func (a *App) environmentPresence(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, presenceTimeout)
	defer cancel()
	presence := map[string]string{}
	st, err := daemon.Dial(a.daemonPaths()).Status(ctx)
	if err != nil || st.Environments == nil {
		return presence
	}
	for i := range st.Environments.Environments {
		p := &st.Environments.Environments[i]
		presence[p.Name] = p.Status
	}
	return presence
}

// remoteEntries reads name's snapshot: its workspaces with a non-null id, as
// the environment they run in, with environment_status from presence and
// stale per D3. No snapshot means no entries and no error.
func (a *App) remoteEntries(name string, presence map[string]string) (remoteListing, error) {
	status := envOffline
	if presence[name] == envOnline {
		status = envOnline
	}
	out := remoteListing{name: name, status: status, entries: []listEntryJSON{}}
	file, snap, err := a.envStore().Snapshot(name)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var workspaces []listEntryJSON
	if err := json.Unmarshal(snap.Workspaces, &workspaces); err != nil {
		return out, fmt.Errorf("decoding the workspaces of %s: %w", name, err)
	}
	stale := status != envOnline || a.snapshotOld(file.ReceivedAt)
	for i := range workspaces {
		e := workspaces[i]
		if e.ID == nil {
			continue
		}
		e.Environment, e.EnvironmentStatus, e.Stale = name, status, stale
		e.AgentState = agentStateOf(snap.Agents, e.Session)
		out.entries = append(out.entries, e)
	}
	return out, nil
}

// agentStateOf is the state of the snapshot agent that runs session, or nil
// when no agent does.
func agentStateOf(agents []envproto.SnapshotAgent, session *listSessionJSON) *string {
	if session == nil || session.ID == nil {
		return nil
	}
	for i := range agents {
		if agents[i].SessionID == *session.ID {
			state := agents[i].State
			return &state
		}
	}
	return nil
}

// snapshotOld is whether a snapshot received at receivedAt, an RFC 3339
// time, is older than remoteStaleAfter. One whose time can't be read is old.
func (a *App) snapshotOld(receivedAt string) bool {
	received, err := time.Parse(time.RFC3339, receivedAt)
	return err != nil || a.now().Sub(received) > remoteStaleAfter
}

// remoteEnvListing is environment name's part of ws list, filtered as the
// local listing is. A snapshot that can't be read is a warning.
func (a *App) remoteEnvListing(name string, opts *listOptions, filter *repo.Spec, parent *parentFilter, presence map[string]string) envListing {
	remote, err := a.remoteEntries(name, presence)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read environment %s's snapshot: %v", name, err))
	}
	envID := ""
	if parent != nil {
		envID = a.envIDOf(name)
	}
	var kept []listEntryJSON
	hidden := 0
	for i := range remote.entries {
		e := remote.entries[i]
		switch {
		case filter != nil && !remoteHasRepo(e.Repos, filter):
		case parent != nil && !parent.keeps(name, envID, deref(e.Parent), deref(e.ID)):
		case e.Hidden && !opts.all && attentionOf(e.LoopRun) == loop.AttentionNone:
			hidden++
		default:
			kept = append(kept, e)
		}
	}
	return envListing{entries: kept, rows: remoteRows(kept, a.now()), hidden: hidden}
}

// remoteHasRepo is hasRepo for a remote entry, by the ids and refs of its
// repos.
func remoteHasRepo(repos []listRepoJSON, filter *repo.Spec) bool {
	for i := range repos {
		if filter.Ref != "" && filter.Ref != repos[i].Ref {
			continue
		}
		if repoMatches(repos[i].ID, filter) {
			return true
		}
	}
	return false
}

// remoteRows are the table rows of remote entries, in tree order.
func remoteRows(entries []listEntryJSON, now time.Time) []listRow {
	order, depths := orderTree(len(entries), func(i int) (string, string) {
		return deref(entries[i].ID), deref(entries[i].Parent)
	})
	rows := make([]listRow, 0, len(order))
	for k, i := range order {
		e := &entries[i]
		rows = append(rows, listRow{
			env:     e.Environment,
			id:      deref(e.ID),
			depth:   depths[k],
			parent:  deref(e.Parent),
			stale:   e.Stale,
			name:    nameText(deref(e.Name), e.Hidden, attentionOf(e.LoopRun)),
			created: createdAgo(e.CreatedAt, now),
			session: sessionText(sessionOf(e.Session)),
			repos:   reposText(reposOf(e.Repos)),
		})
	}
	return rows
}

// createdAgo is the CREATED column for a created_at, "-" when there is none
// or it can't be read.
func createdAgo(created *string, now time.Time) string {
	t, err := time.Parse(time.RFC3339, deref(created))
	if err != nil {
		return "-"
	}
	return ago(now.Sub(t))
}

// sessionOf is a listed session as the lifetime session sessionText reads,
// or nil for none.
func sessionOf(s *listSessionJSON) *lifetime.Session {
	if s == nil {
		return nil
	}
	return &lifetime.Session{Kind: s.Kind, ID: deref(s.ID), State: s.State, Stale: s.Stale}
}

// reposOf is a listed workspace's repos as reposText reads them.
func reposOf(repos []listRepoJSON) []workspace.Repo {
	out := make([]workspace.Repo, 0, len(repos))
	for i := range repos {
		out = append(out, workspace.Repo{Path: repos[i].Path, Ref: repos[i].Ref})
	}
	return out
}

// parentFilter is what --parent keeps: same-environment children of id, and
// children on other environments whose ledger entry names id as their parent.
type parentFilter struct {
	env    string                       // the parent's environment name
	envID  string                       // envscope.Local, or the parent environment's id
	id     string                       // the parent workspace's id
	ledger map[ledgerKey]envscope.Entry // nil without a ledger
}

// parentFilterOf is the filter --parent applies, or nil without --parent.
func (a *App) parentFilterOf(opts *listOptions) (*parentFilter, error) {
	if opts.parent == "" {
		return nil, nil
	}
	env, id, err := a.parentOf(opts)
	if err != nil {
		return nil, err
	}
	return &parentFilter{env: env, envID: a.envIDOf(env), id: id, ledger: a.readLedger()}, nil
}

// keeps is whether workspace id, in environment env (envID) and started from
// entryParent there, is a child of the filter's parent.
func (f *parentFilter) keeps(env, envID, entryParent, id string) bool {
	if env == f.env {
		return entryParent == f.id
	}
	entry, ok := f.ledger[ledgerKey{env: envID, workspace: id}]
	return ok && entry.Parent != nil && entry.Parent.Environment == f.envID && entry.Parent.Workspace == f.id
}

// envIDOf is environment env's id, as envIDIn.
func (a *App) envIDOf(env string) string {
	return envIDIn(a.environmentIDs(), env)
}

// envIDIn is environment env's id from ids (environmentIDs): envscope.Local
// for local, else its record's id ("" when there is none).
func envIDIn(ids map[string]string, env string) string {
	if env == localEnvironment {
		return envscope.Local
	}
	return ids[env]
}
