package cli

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/artifact"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/workspace"
)

const (
	// scanInputVersion is the version of ws artifact scan record's input.
	scanInputVersion = 1
	// maxScanInput is the most stdin ws artifact scan record reads.
	maxScanInput = 1 << 20
	// scanLockWait bounds the wait for a workspace's citations.lock.
	scanLockWait = 10 * time.Second
)

// conversationJSON is one workspace's part in an artifact (ADR 0084).
// Removed is null for a workspace on another environment, which isn't
// checked.
type conversationJSON struct {
	Workspace   string   `json:"workspace"`
	Name        *string  `json:"name"`
	Environment *string  `json:"environment"`
	Removed     *bool    `json:"removed"`
	Actions     []string `json:"actions"`
	Revs        []int    `json:"revs"`
	FirstAt     string   `json:"first_at"`
	LastAt      string   `json:"last_at"`
}

// conversationRowJSON is one workspace's row in list --conversation.
type conversationRowJSON struct {
	Actions []string `json:"actions"`
	Revs    []int    `json:"revs"`
	FirstAt string   `json:"first_at"`
	LastAt  string   `json:"last_at"`
}

// liveWorkspaces reports, for a workspace id, whether it was removed: false
// while ws lists it, true once it doesn't. It is nil when the list can't be
// read, so removed is unknown (null).
func (a *App) liveWorkspaces() func(string) *bool {
	list, err := a.workspaces.List()
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("listing workspaces for conversations: %v", err))
		return nil
	}
	ids := make(map[string]bool, len(list))
	for i := range list {
		ids[list[i].ID] = true
	}
	return func(id string) *bool {
		removed := !ids[id]
		return &removed
	}
}

// conversationsJSON is e's conversations as ws prints them.
func conversationsJSON(e *artifact.Entry, removed func(string) *bool) []conversationJSON {
	out := make([]conversationJSON, 0, len(e.Conversations))
	for i := range e.Conversations {
		c := &e.Conversations[i]
		row := conversationJSON{
			Workspace: c.Workspace, Name: c.Name, Environment: c.Environment,
			Actions: nonNil(c.Actions), Revs: nonNil(c.Revs),
			FirstAt: formatTime(c.FirstAt), LastAt: formatTime(c.LastAt),
		}
		if c.Environment == nil && removed != nil {
			row.Removed = removed(c.Workspace)
		}
		out = append(out, row)
	}
	return out
}

// conversationRow is workspace id's row in e, or nil.
func conversationRow(e *artifact.Entry, id string) *conversationRowJSON {
	for i := range e.Conversations {
		c := &e.Conversations[i]
		if c.Workspace == id {
			return &conversationRowJSON{Actions: nonNil(c.Actions), Revs: nonNil(c.Revs), FirstAt: formatTime(c.FirstAt), LastAt: formatTime(c.LastAt)}
		}
	}
	return nil
}

// lastWith is when workspace id last did anything with e; zero if never.
func lastWith(e *artifact.Entry, id string) time.Time {
	for i := range e.Conversations {
		if e.Conversations[i].Workspace == id {
			return e.Conversations[i].LastAt
		}
	}
	return time.Time{}
}

// nonNil is s, or an empty slice for nil, so JSON prints [] and not null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// linkWarning records err from a link write as a warning: a link never fails
// the command it came from (ADR 0084).
func (a *App) linkWarning(id string, err error) {
	if err == nil {
		return
	}
	a.warned = append(a.warned, fmt.Sprintf("%s: link not recorded: %v", id, err))
}

// citingWorkspace is the workspace a citation is recorded for.
type citingWorkspace struct {
	id          string
	name        string
	root        string
	environment string
}

// recordCited records refs as cited by w, one link per (artifact, revision),
// grouped by artifact. Failures are warnings.
func (a *App) recordCited(ctx context.Context, w citingWorkspace, refs []artifact.Ref) []artifact.Ref {
	store := a.artifactStore()
	var recorded []artifact.Ref
	for _, id := range refIDs(refs) {
		links := citedLinksOf(w, refs, id)
		err := store.RecordLinks(ctx, id, links...)
		if errors.Is(err, artifact.ErrNotFound) {
			continue
		}
		if err != nil {
			a.linkWarning(id, err)
			continue
		}
		for _, r := range refs {
			if r.ID == id {
				recorded = append(recorded, r)
			}
		}
	}
	return recorded
}

func refIDs(refs []artifact.Ref) []string {
	var ids []string
	for _, r := range refs {
		if !slices.Contains(ids, r.ID) {
			ids = append(ids, r.ID)
		}
	}
	return ids
}

func citedLinksOf(w citingWorkspace, refs []artifact.Ref, id string) []artifact.NewLink {
	var links []artifact.NewLink
	for _, r := range refs {
		if r.ID == id {
			links = append(links, artifact.NewLink{Workspace: w.id, Name: w.name, Root: w.root, Environment: w.environment, Action: artifact.ActionCited, Rev: r.Rev})
		}
	}
	return links
}

// recordStartCited records the citations of a new local workspace's prompt.
// Its tree's root is its parent's root, or itself.
func (a *App) recordStartCited(ctx context.Context, ws *workspace.Workspace, refs []artifact.Ref) {
	if len(refs) == 0 {
		return
	}
	name, root := ws.Name, ws.ID
	if name == "" {
		name = ws.ID
	}
	if ws.Parent != "" {
		root = a.rootOf(ws.Parent)
	}
	a.recordCited(ctx, citingWorkspace{id: ws.ID, name: name, root: root}, refs)
}

// rootOf is the root of id's tree, or id when the list can't be read.
func (a *App) rootOf(id string) string {
	list, err := a.workspaces.List()
	if err != nil {
		return id
	}
	return machine.Root(list, id)
}

// recordRemoteCited records the citations of a workspace just created on
// environment env, whose id and name are in result (Q2 of ADR 0084).
func (a *App) recordRemoteCited(ctx context.Context, result []byte, env string, refs []artifact.Ref) {
	if len(refs) == 0 {
		return
	}
	var created struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(result, &created); err != nil || !artifact.ValidWorkspaceID(created.ID) {
		a.warned = append(a.warned, "the new workspace's id couldn't be read, so its citations weren't recorded")
		return
	}
	name := created.Name
	if name == "" {
		name = created.ID
	}
	a.recordCited(ctx, citingWorkspace{id: created.ID, name: name, root: created.ID, environment: env}, refs)
}

// citingLocal is local workspace id with its display name and tree root, or
// workspace_not_found.
func (a *App) citingLocal(id string) (citingWorkspace, error) {
	list, err := a.workspaces.List()
	if err != nil {
		return citingWorkspace{}, fmt.Errorf("listing workspaces: %w", err)
	}
	ws := workspaceByID(list, id)
	if ws.ID == "" {
		return citingWorkspace{}, newError(codeWorkspaceNotFound, fmt.Errorf("no workspace %s on this machine", id))
	}
	name := ws.Name
	if name == "" {
		name = id
	}
	return citingWorkspace{id: id, name: name, root: machine.Root(list, id)}, nil
}

// citeResultJSON is ws artifact cite's output.
type citeResultJSON struct {
	Workspace string         `json:"workspace"`
	Recorded  []citedRefJSON `json:"recorded"`
	Warnings  []string       `json:"warnings"`
}

// citedRefJSON is one recorded citation: an artifact and the revision it
// named, null for none.
type citedRefJSON struct {
	ID  string `json:"id"`
	Rev *int   `json:"rev"`
}

// newArtifactCiteCmd is ws artifact cite, which the desktop's reply box runs
// after a message with references was sent (ADR 0084).
func (a *App) newArtifactCiteCmd() *cobra.Command {
	var workspaceID string
	cmd := &cobra.Command{
		Use:   "cite <REF>... --workspace <id>",
		Short: "Record that a person cited artifacts to an agent",
		Args:  atLeastOneArg("an artifact reference"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			if !artifact.ValidWorkspaceID(workspaceID) {
				return usageErrorf("--workspace must be a workspace id, not %q", workspaceID)
			}
			return a.cite(cmd.Context(), workspaceID, args)
		})),
	}
	cmd.Flags().StringVar(&workspaceID, "workspace", "", "the workspace whose agent the references were sent to")
	return cmd
}

// cite records args, references that must all exist, as cited by workspace
// id.
func (a *App) cite(ctx context.Context, id string, args []string) error {
	refs, err := parseCiteRefs(args)
	if err != nil {
		return err
	}
	if err = a.checkArtifactsExist(refIDs(refs)); err != nil {
		return err
	}
	w, err := a.citingLocal(id)
	if err != nil {
		return err
	}
	recorded := a.recordCited(ctx, w, refs)
	out := citeResultJSON{Workspace: id, Recorded: make([]citedRefJSON, 0, len(recorded)), Warnings: a.artifactWarnings(nil)}
	for _, r := range recorded {
		rev := r.Rev
		ref := citedRefJSON{ID: r.ID}
		if rev != 0 {
			ref.Rev = &rev
		}
		out.Recorded = append(out.Recorded, ref)
	}
	if !a.global.json {
		_, err = fmt.Fprintf(a.stdout, "recorded %d citations for %s\n", len(out.Recorded), id)
		return err
	}
	return a.writeJSON(out)
}

// parseCiteRefs parses ids and references, keeping /r<n>, without repeats.
func parseCiteRefs(args []string) ([]artifact.Ref, error) {
	var refs []artifact.Ref
	for _, arg := range args {
		id, rev, err := artifact.ParseRef(arg)
		if err != nil {
			return nil, newError(codeArtifactUnknown, err)
		}
		r := artifact.Ref{ID: id, Rev: rev}
		if !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	return refs, nil
}

// newArtifactScanCmd is ws artifact scan, the desktop's transcript scan's
// store (ADR 0084).
func (a *App) newArtifactScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan <OP>",
		Short: "Read and record the desktop's transcript scan",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newArtifactScanCursorsCmd(), a.newArtifactScanRecordCmd())
	return cmd
}

// scanCursorJSON is one workspace's stored scan cursor.
type scanCursorJSON struct {
	Workspace  string `json:"workspace"`
	Transcript string `json:"transcript"`
	Offset     int64  `json:"offset"`
	UpdatedAt  string `json:"updated_at"`
}

// scanCursorsJSON is ws artifact scan cursors' output.
type scanCursorsJSON struct {
	Cursors  []scanCursorJSON `json:"cursors"`
	Warnings []string         `json:"warnings"`
}

func (a *App) newArtifactScanCursorsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cursors",
		Short: "Print where the scan of each workspace's transcript stopped",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			cursors, warnings, err := artifact.Cursors(a.paths.State)
			if err != nil {
				return err
			}
			out := scanCursorsJSON{Cursors: make([]scanCursorJSON, 0, len(cursors)), Warnings: a.artifactWarnings(warnings)}
			for _, c := range cursors {
				out.Cursors = append(out.Cursors, scanCursorJSON{Workspace: c.Workspace, Transcript: c.Transcript, Offset: c.Offset, UpdatedAt: formatTime(c.UpdatedAt)})
			}
			return a.writeJSON(out)
		})),
	}
}

// scanInputJSON is ws artifact scan record's input.
type scanInputJSON struct {
	Version    int                `json:"version"`
	Workspace  string             `json:"workspace"`
	Transcript string             `json:"transcript"`
	Offset     int64              `json:"offset"`
	Citations  []scanCitationJSON `json:"citations"`
}

// scanCitationJSON is one citing message in the input.
type scanCitationJSON struct {
	Key  string     `json:"key"`
	At   *time.Time `json:"at"`
	Refs []string   `json:"refs"`
}

// scanRecordJSON is ws artifact scan record's output.
type scanRecordJSON struct {
	Workspace string   `json:"workspace"`
	Offset    int64    `json:"offset"`
	Recorded  int      `json:"recorded"`
	Skipped   int      `json:"skipped"`
	Warnings  []string `json:"warnings"`
}

func (a *App) newArtifactScanRecordCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "record -",
		Short: "Record the citations a transcript scan found, read from stdin",
		Args:  artifactArgs(1, "- to read the scan from stdin"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			if args[0] != "-" {
				return usageErrorf("ws artifact scan record reads only stdin: give -")
			}
			return a.recordScan(cmd.Context())
		})),
	}
}

// recordScan reads a scan from stdin and records it.
func (a *App) recordScan(ctx context.Context) error {
	scan, err := a.readScanInput()
	if err != nil {
		return err
	}
	w, err := a.citingLocal(scan.Workspace)
	if err != nil {
		return err
	}
	scan.Name, scan.Root = w.name, w.root
	ctx, cancel := context.WithTimeout(ctx, scanLockWait)
	defer cancel()
	res, err := a.artifactStore().RecordScan(ctx, a.paths.State, scan)
	if err != nil {
		return scanError(err)
	}
	return a.writeJSON(scanRecordJSON{
		Workspace: scan.Workspace, Offset: res.Offset, Recorded: res.Recorded, Skipped: res.Skipped,
		Warnings: a.artifactWarnings(res.Warnings),
	})
}

// scanError maps RecordScan's errors: bad input or a newer state is usage, a
// workspace whose record is gone workspace_not_found.
func scanError(err error) error {
	switch {
	case errors.Is(err, artifact.ErrInvalid), errors.Is(err, artifact.ErrNewer):
		return newError(codeUsage, err)
	case errors.Is(err, artifact.ErrNoWorkspace):
		return newError(codeWorkspaceNotFound, err)
	}
	return err
}

// readScanInput reads and checks stdin's scan: usage for anything that isn't
// a valid version 1 scan of at most 1 MiB.
func (a *App) readScanInput() (*artifact.Scan, error) {
	data, err := io.ReadAll(io.LimitReader(a.stdin, maxScanInput+1))
	if err != nil {
		return nil, fmt.Errorf("reading stdin: %w", err)
	}
	if len(data) > maxScanInput {
		return nil, usageErrorf("the scan on stdin is over %d bytes", maxScanInput)
	}
	var in scanInputJSON
	if err = json.Unmarshal(data, &in); err != nil {
		return nil, usageErrorf("reading the scan on stdin: %v", err)
	}
	if in.Version != scanInputVersion {
		return nil, usageErrorf("the scan's version must be %d, not %d", scanInputVersion, in.Version)
	}
	scan, err := scanOf(&in)
	if err != nil {
		return nil, err
	}
	return scan, scanError(artifact.CheckScan(scan))
}

// scanOf converts the input, parsing each reference; one that doesn't parse
// is usage.
func scanOf(in *scanInputJSON) (*artifact.Scan, error) {
	scan := &artifact.Scan{Workspace: in.Workspace, Transcript: in.Transcript, Offset: in.Offset}
	for i := range in.Citations {
		c := &in.Citations[i]
		citation := artifact.Citation{Key: c.Key}
		if c.At != nil {
			citation.At = *c.At
		}
		for _, ref := range c.Refs {
			id, rev, err := artifact.ParseRef(ref)
			if err != nil {
				return nil, newError(codeUsage, err)
			}
			citation.Refs = append(citation.Refs, artifact.Ref{ID: id, Rev: rev})
		}
		scan.Citations = append(scan.Citations, citation)
	}
	return scan, nil
}
