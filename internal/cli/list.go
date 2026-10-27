package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type listOptions struct {
	idsOnly bool
	repo    string
}

func (a *App) newListCmd() *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list [OPTIONS]",
		Short: "List workspaces and their sessions",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runList(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVarP(&opts.idsOnly, "ids", "q", false, "print only workspace IDs")
	cmd.Flags().StringVar(&opts.repo, "repo", "", "only workspaces containing this repo")
	return cmd
}

// listJSON is ws list's output (docs/tools/ws-list.md).
type listJSON struct {
	Workspaces []listWorkspaceJSON `json:"workspaces"`
	Warnings   []string            `json:"warnings"`
}

type listWorkspaceJSON struct {
	ID *string `json:"id"`
	// Name is the display name: --name, or ws rename's (ADR 0036).
	Name      *string          `json:"name"`
	Path      string           `json:"path"`
	CreatedAt *string          `json:"created_at"`
	Session   *listSessionJSON `json:"session"`
	Repos     []listRepoJSON   `json:"repos"`
	// Review is a review workspace's record, or null (ADR 0039).
	Review *record.Record `json:"review"`
}

type listSessionJSON struct {
	Kind  lifetime.Kind  `json:"kind"`
	ID    *string        `json:"id"`
	State lifetime.State `json:"state"`
	PID   *int           `json:"pid"`
	Stale *bool          `json:"stale"`
}

type listRepoJSON struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	RefKind string `json:"ref_kind"`
}

// runList removes workspaces whose session is gone, then lists the rest
// with their sessions, oldest first, and the directories under
// workspaces/ that aren't workspaces last (ADRs 0012, 0015).
func (a *App) runList(ctx context.Context, opts *listOptions) error {
	if opts.idsOnly && a.global.json {
		return usageErrorf("list -q doesn't support --json")
	}
	var filter *repo.Spec
	if opts.repo != "" {
		sp, err := repo.ParseSpec(opts.repo)
		if err != nil {
			return newError(codeSpecInvalid, err)
		}
		filter = &sp
	}
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	entries := filterEntries(listing.Workspaces, filter)
	unknown := listing.Unknown
	if filter != nil {
		unknown = nil
	}
	switch {
	case a.global.json:
		return a.writeJSON(listJSON{Workspaces: a.listWorkspaces(entries, unknown), Warnings: a.warnings()})
	case opts.idsOnly:
		for i := range entries {
			fmt.Fprintln(a.stdout, entries[i].Workspace.ID)
		}
	default:
		a.printList(entries, unknown)
	}
	a.printWarnings()
	return nil
}

// filterEntries keeps the workspaces with a repo matching filter, or all
// of them without one.
func filterEntries(entries []lifetime.Entry, filter *repo.Spec) []lifetime.Entry {
	if filter == nil {
		return entries
	}
	var kept []lifetime.Entry
	for i := range entries {
		if hasRepo(&entries[i].Workspace, filter) {
			kept = append(kept, entries[i])
		}
	}
	return kept
}

// hasRepo reports whether ws holds a repo the spec matches, by the parts
// of its id the spec gives, so it needs neither the cache nor the network:
// a name matches any owner and host, owner/name any host. A ref, if given,
// must be the ref checked out.
func hasRepo(ws *workspace.Workspace, filter *repo.Spec) bool {
	for _, r := range ws.Repos {
		if filter.Ref != "" && filter.Ref != r.Ref {
			continue
		}
		if repoMatches(r.ID, filter) {
			return true
		}
	}
	return false
}

func repoMatches(id string, filter *repo.Spec) bool {
	switch filter.Kind {
	case repo.KindName:
		return strings.HasSuffix(id, "/"+filter.Name)
	case repo.KindOwnerName:
		return strings.HasSuffix(id, "/"+filter.Owner+"/"+filter.Name)
	case repo.KindQualified, repo.KindURL:
	}
	want, _ := filter.ID()
	return id == want.String()
}

// listWorkspaces is the JSON for the workspaces and then the unknown
// directories, which have no id, creation time, session, repos, or
// review.
func (a *App) listWorkspaces(entries []lifetime.Entry, unknown []string) []listWorkspaceJSON {
	out := make([]listWorkspaceJSON, 0, len(entries)+len(unknown))
	for i := range entries {
		ws := listWorkspace(&entries[i])
		ws.Review = a.reviewRecord(entries[i].Workspace.ID)
		out = append(out, ws)
	}
	for _, dir := range unknown {
		out = append(out, listWorkspaceJSON{Path: dir, Repos: []listRepoJSON{}})
	}
	return out
}

func listWorkspace(e *lifetime.Entry) listWorkspaceJSON {
	ws := &e.Workspace
	repos := make([]listRepoJSON, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		repos = append(repos, listRepoJSON{ID: r.ID, Path: r.Path, Ref: r.Ref, RefKind: r.RefKind})
	}
	created := formatTime(ws.CreatedAt)
	return listWorkspaceJSON{ID: &ws.ID, Name: optional(ws.Name), Path: ws.Path, CreatedAt: &created, Session: listSession(e.Session), Repos: repos}
}

func listSession(s *lifetime.Session) *listSessionJSON {
	if s == nil {
		return nil
	}
	out := &listSessionJSON{Kind: s.Kind, ID: optional(s.ID), State: s.State, Stale: s.Stale}
	if s.PID != 0 {
		out.PID = &s.PID
	}
	return out
}

// reviewRecord is workspace id's review record, or nil when it has none
// or it can't be read, which is a warning.
func (a *App) reviewRecord(id string) *record.Record {
	rec, err := record.Read(record.Path(a.paths.State, id))
	if errors.Is(err, record.ErrNotFound) {
		return nil
	}
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read workspace %s's review record: %v", id, err))
		return nil
	}
	return &rec
}

// printList prints a table like `docker ps`.
func (a *App) printList(entries []lifetime.Entry, unknown []string) {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tCREATED\tSESSION\tREPOS")
	now := a.now()
	for i := range entries {
		ws := &entries[i].Workspace
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", ws.ID, nameText(ws.Name), ago(now.Sub(ws.CreatedAt)), sessionText(entries[i].Session), reposText(ws.Repos))
	}
	for _, dir := range unknown {
		fmt.Fprintf(tw, "unknown\t-\t-\t-\t%s\n", dir)
	}
	_ = tw.Flush()
}

// nameText is the NAME column: the display name, or "-" without one.
func nameText(name string) string {
	if name == "" {
		return "-"
	}
	return name
}

// sessionText is the SESSION column: "agent <id>: <state>", with ",
// stale" when it is, or "shell"; "-" while the session isn't recorded.
func sessionText(s *lifetime.Session) string {
	switch {
	case s == nil:
		return "-"
	case s.Kind == lifetime.KindShell && s.State == lifetime.StateRunning:
		return "shell"
	case s.Kind == lifetime.KindShell:
		return "shell: " + string(s.State)
	case s.Stale != nil && *s.Stale:
		return fmt.Sprintf("agent %s: %s, stale", s.ID, s.State)
	}
	return fmt.Sprintf("agent %s: %s", s.ID, s.State)
}

// reposText is the REPOS column: each repo's directory and ref.
func reposText(repos []workspace.Repo) string {
	parts := make([]string, 0, len(repos))
	for _, r := range repos {
		parts = append(parts, withRef(r.Path, r.Ref))
	}
	return strings.Join(parts, ", ")
}

// ago is a rough age, as `docker ps` shows one: "45s ago", "20m ago",
// "2h ago", "3d ago".
func ago(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}
