package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/environment"
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
		out.entries = append(out.entries, e)
	}
	return out, nil
}

// snapshotOld is whether a snapshot received at receivedAt, an RFC 3339
// time, is older than remoteStaleAfter. One whose time can't be read is old.
func (a *App) snapshotOld(receivedAt string) bool {
	received, err := time.Parse(time.RFC3339, receivedAt)
	return err != nil || a.now().Sub(received) > remoteStaleAfter
}

// remoteEnvListing is environment name's part of ws list, filtered as the
// local listing is. A snapshot that can't be read is a warning.
func (a *App) remoteEnvListing(name string, opts *listOptions, filter *repo.Spec, presence map[string]string) envListing {
	remote, err := a.remoteEntries(name, presence)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read environment %s's snapshot: %v", name, err))
	}
	var kept []listEntryJSON
	hidden := 0
	for i := range remote.entries {
		e := remote.entries[i]
		switch {
		case filter != nil && !remoteHasRepo(e.Repos, filter):
		case opts.parent != "" && deref(e.Parent) != opts.parent:
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
			env:     envText(e),
			id:      deref(e.ID),
			depth:   depths[k],
			name:    nameText(deref(e.Name), e.Hidden, attentionOf(e.LoopRun)),
			created: createdAgo(e.CreatedAt, now),
			session: sessionText(sessionOf(e.Session)),
			repos:   reposText(reposOf(e.Repos)),
		})
	}
	return rows
}

// envText is the ENV column: the environment's name, and "(offline)" after
// it when the entry is stale.
func envText(e *listEntryJSON) string {
	if e.Stale {
		return e.Environment + " (offline)"
	}
	return e.Environment
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
