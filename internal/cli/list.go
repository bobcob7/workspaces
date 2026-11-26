package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/agent"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/loop"
	"github.com/bobcob7/workspaces/internal/machine"
	"github.com/bobcob7/workspaces/internal/mcp"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/workspace"
)

type listOptions struct {
	all     bool
	idsOnly bool
	repo    string
	parent  string
	envs    []string
}

func (a *App) newListCmd() *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list [OPTIONS]",
		Short: "List workspaces and their sessions",
		Long:  "List workspaces and their sessions.\n\nFor people and the desktop app. Agents use ws machine children instead; see `ws llm machine children`.",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runList(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().BoolVarP(&opts.all, "all", "a", false, "include hidden workspaces")
	cmd.Flags().BoolVarP(&opts.idsOnly, "ids", "q", false, "print only workspace IDs")
	cmd.Flags().StringVar(&opts.repo, "repo", "", "only workspaces containing this repo")
	cmd.Flags().StringVar(&opts.parent, "parent", "", "only the workspaces this one started")
	cmd.Flags().StringArrayVar(&opts.envs, "env", nil, "only these environments (local or a name from ws env list); default: every environment")
	return cmd
}

// listJSON is ws list's output (docs/tools/ws-list.md).
type listJSON struct {
	Workspaces []listEntryJSON `json:"workspaces"`
	// HiddenCount is how many hidden workspaces were left out (ADR 0043).
	HiddenCount int      `json:"hidden_count"`
	Warnings    []string `json:"warnings"`
}

// listEntryJSON is one ws list entry: a workspace and where it runs. Only ws
// list uses it; ws machine children keeps listWorkspaceJSON.
type listEntryJSON struct {
	listWorkspaceJSON
	Environment       string `json:"environment"`
	EnvironmentStatus string `json:"environment_status"`
	Stale             bool   `json:"stale"`
	// AgentState is the state of the remote agent that runs the entry's
	// session, or null (D9).
	AgentState *string `json:"agent_state"`
	// ScopeParent is a cross-environment child's parent from the controller's
	// scope ledger, or null (ADR 0082).
	ScopeParent *scopeParentJSON `json:"scope_parent"`
	// Scope is the environments the workspace may start children on when wider
	// than its own, or null (ADR 0082).
	Scope []envproto.ScopeEnv `json:"scope"`
}

type listWorkspaceJSON struct {
	ID *string `json:"id"`
	// Name is the display name: --name, or ws rename's (ADR 0036).
	Name      *string          `json:"name"`
	Path      string           `json:"path"`
	CreatedAt *string          `json:"created_at"`
	Session   *listSessionJSON `json:"session"`
	Hidden    bool             `json:"hidden"`
	// Parent is the workspace this one is removed with (ADR 0044).
	Parent *string        `json:"parent"`
	Repos  []listRepoJSON `json:"repos"`
	// Review is a review workspace's record, or null (ADR 0039).
	Review *record.Record `json:"review"`
	// LoopRun is the loop run this workspace is, or null (ADR 0057).
	LoopRun *listLoopRunJSON `json:"loop_run"`
	// Progress is the workspace's own progress record, or null (ADR 0077).
	Progress *progressJSON `json:"progress"`
	// Rollup is its steps and its descendants', or null when none is planned.
	Rollup *rollupJSON `json:"rollup"`
}

// listLoopRunJSON is the loop a workspace runs, and what that run asks of a
// person: null attention when it asks nothing (ADR 0057).
type listLoopRunJSON struct {
	Loop      string  `json:"loop"`
	Attention *string `json:"attention"`
}

type listSessionJSON struct {
	Kind  lifetime.Kind  `json:"kind"`
	ID    *string        `json:"id"`
	State lifetime.State `json:"state"`
	PID   *int           `json:"pid"`
	Stale *bool          `json:"stale"`
	// Runner is what runs an agent, or null for a shell (ADR 0063).
	Runner *string `json:"runner"`
	// Model is the agent's model, or null until it has answered.
	Model    *string           `json:"model"`
	WSRunner *listWSRunnerJSON `json:"ws_runner"`
}

// listWSRunnerJSON is a ws runner child's details, null for any other
// session (ADR 0063).
type listWSRunnerJSON struct {
	// ModelAlias is the model alias the run was spawned with, "" for a raw id.
	ModelAlias string       `json:"model_alias"`
	Endpoint   string       `json:"endpoint"`
	Step       int          `json:"step"`
	MaxSteps   int          `json:"max_steps"`
	Restarts   int          `json:"restarts"`
	Usage      agent.Usage  `json:"usage"`
	MCP        []mcp.Status `json:"mcp"`
	// Exit is the exit reason once the runner has finished, else null.
	Exit            *string `json:"exit"`
	EndpointChanges int     `json:"endpoint_changes"`
}

type listRepoJSON struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	RefKind string `json:"ref_kind"`
}

// runList lists the workspaces of each environment --env names, local by
// default, with their sessions, oldest first, and the directories under
// workspaces/ that aren't workspaces last (ADRs 0012, 0015, 0072).
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
	envs, parent, err := a.listTargets(opts) //nolint:contextcheck // the ledger and environment store reads take no context
	if err != nil {
		return err
	}
	listings, presence, err := a.listEnvironments(ctx, opts, envs, filter, parent)
	if err != nil {
		return err
	}
	entries, rows, hidden := joinListings(listings)
	switch {
	case a.global.json:
		a.annotateScopes(ctx, entries, presence)
		a.mergeRollups(entries) //nolint:contextcheck // the ledger, snapshot and workspace reads take no context
		return a.writeJSON(listJSON{Workspaces: entries, HiddenCount: hidden, Warnings: a.warnings()})
	case opts.idsOnly:
		for i := range entries {
			if entries[i].ID != nil {
				fmt.Fprintln(a.stdout, *entries[i].ID)
			}
		}
	default:
		a.printList(a.treeRows(rows, entries), hasEnvColumn(entries)) //nolint:contextcheck // the ledger and environment store reads take no context
		a.printHiddenNote(hidden)
	}
	a.printWarnings()
	return nil
}

// listTargets are the environments ws list reads and, with --parent, the
// filter it keeps workspaces by.
func (a *App) listTargets(opts *listOptions) ([]string, *parentFilter, error) {
	envs, err := a.listedEnvironments(opts)
	if err != nil || opts.parent == "" {
		return envs, nil, err
	}
	if len(opts.envs) > 1 {
		return nil, nil, usageErrorf("--parent takes one --env")
	}
	parent, err := a.parentFilterOf(opts)
	return envs, parent, err
}

// envListing is one environment's part of ws list: its entries as JSON in
// listing order, its table rows in tree order, and how many hidden entries
// it left out.
type envListing struct {
	entries []listEntryJSON
	rows    []listRow
	hidden  int
}

// listRow is one line of ws list's table.
type listRow struct {
	env         string
	id          string
	depth       int
	name        string
	created     string
	session     string
	repos       string
	parent      string // same-environment parent id, "" for none
	scopeParent string // rowKey of scope_parent, "" for none
	stale       bool   // the ENV column adds " (offline)"
}

// listEnvironments lists each of envs in order: local from this machine,
// the others from their snapshots. It also returns the presence it asked the
// daemon for, nil when no environment was remote.
func (a *App) listEnvironments(ctx context.Context, opts *listOptions, envs []string, filter *repo.Spec, parent *parentFilter) ([]envListing, map[string]string, error) {
	var presence map[string]string
	listings := make([]envListing, 0, len(envs))
	for _, name := range envs {
		if name == localEnvironment {
			local, err := a.localListing(ctx, opts, filter, parent)
			if err != nil {
				return nil, nil, err
			}
			listings = append(listings, local)
			continue
		}
		if presence == nil {
			presence = a.environmentPresence(ctx)
		}
		listings = append(listings, a.remoteEnvListing(name, opts, filter, parent, presence))
	}
	return listings, presence, nil
}

// joinListings is the JSON entries and table rows of all listings, in order,
// and how many hidden entries they left out in all.
func joinListings(listings []envListing) ([]listEntryJSON, []listRow, int) {
	entries := []listEntryJSON{}
	var rows []listRow
	hidden := 0
	for i := range listings {
		entries = append(entries, listings[i].entries...)
		rows = append(rows, listings[i].rows...)
		hidden += listings[i].hidden
	}
	return entries, rows, hidden
}

// hasEnvColumn is whether the table has an ENV column: some entry runs in an
// environment other than local.
func hasEnvColumn(entries []listEntryJSON) bool {
	for i := range entries {
		if entries[i].Environment != localEnvironment {
			return true
		}
	}
	return false
}

// localListing is this machine's part of ws list.
func (a *App) localListing(ctx context.Context, opts *listOptions, filter *repo.Spec, parent *parentFilter) (envListing, error) {
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return envListing{}, err
	}
	loopRuns := map[string]*listLoopRunJSON{}
	raised := func(e *lifetime.Entry) bool {
		return attentionOf(a.cachedLoopRun(loopRuns, e)) != loop.AttentionNone
	}
	entries, hidden := visibleEntries(a.parentChildren(filterEntries(listing.Workspaces, filter), parent), opts.all, raised)
	unknown := listing.Unknown
	if filter != nil || parent != nil {
		unknown = nil
	}
	rows := append(a.localRows(entries, loopRuns), unknownRows(unknown)...)
	local, err := a.localEntries(entries, unknown, loopRuns)
	if err != nil {
		return envListing{}, err
	}
	return envListing{entries: local, rows: rows, hidden: hidden}, nil
}

// localEntries is the JSON of the local entries and unknown directories,
// each with the environment it runs in.
func (a *App) localEntries(entries []lifetime.Entry, unknown []string, loopRuns map[string]*listLoopRunJSON) ([]listEntryJSON, error) {
	ws := a.listWorkspaces(entries, unknown, loopRuns)
	if a.global.json {
		var err error
		if ws, err = a.listedWorkspaces(entries, unknown, loopRuns); err != nil {
			return nil, err
		}
	}
	out := make([]listEntryJSON, 0, len(ws))
	for i := range ws {
		env := localEnvironment
		if i < len(entries) {
			env = localEnvironmentOf(&entries[i].Workspace)
		}
		out = append(out, listEntryJSON{listWorkspaceJSON: ws[i], Environment: env, EnvironmentStatus: envOnline})
	}
	return out, nil
}

// localRows are the table rows of the local entries, in tree order.
func (a *App) localRows(entries []lifetime.Entry, loopRuns map[string]*listLoopRunJSON) []listRow {
	order, depths := treeOrder(entries)
	now := a.now()
	rows := make([]listRow, 0, len(order))
	for k, i := range order {
		e := &entries[i]
		ws := &e.Workspace
		rows = append(rows, listRow{
			env:     localEnvironmentOf(ws),
			id:      ws.ID,
			depth:   depths[k],
			parent:  ws.Parent,
			name:    nameText(ws.Name, ws.Hidden, attentionOf(a.cachedLoopRun(loopRuns, e))),
			created: ago(now.Sub(ws.CreatedAt)),
			session: sessionText(e.Session),
			repos:   reposText(ws.Repos),
		})
	}
	return rows
}

// unknownRows are the table rows of the unknown directories.
func unknownRows(dirs []string) []listRow {
	rows := make([]listRow, 0, len(dirs))
	for _, dir := range dirs {
		rows = append(rows, listRow{env: localEnvironment, id: "unknown", name: "-", created: "-", session: "-", repos: dir})
	}
	return rows
}

// localEnvironmentOf is the environment a workspace on this machine runs in:
// the one its record names when an environment agent made it, else local
// (D2).
func localEnvironmentOf(ws *workspace.Workspace) string {
	if ws.Environment != "" {
		return ws.Environment
	}
	return localEnvironment
}

// visibleEntries leaves out hidden workspaces unless all, except loop runs
// that raised is true for, and counts the ones it left out (ADR 0043, ADR
// 0057).
func visibleEntries(entries []lifetime.Entry, all bool, raised func(*lifetime.Entry) bool) ([]lifetime.Entry, int) {
	if all {
		return entries, 0
	}
	var kept []lifetime.Entry
	for i := range entries {
		if !entries[i].Workspace.Hidden || raised(&entries[i]) {
			kept = append(kept, entries[i])
		}
	}
	return kept, len(entries) - len(kept)
}

// printHiddenNote says on stderr how many hidden workspaces the table left
// out, so stdout stays the table.
func (a *App) printHiddenNote(hidden int) {
	if hidden == 0 || a.global.quiet {
		return
	}
	noun := "workspaces"
	if hidden == 1 {
		noun = "workspace"
	}
	fmt.Fprintf(a.stderr, "%d hidden %s not shown; ws list -a shows them\n", hidden, noun)
}

// parentChildren keeps the entries parent keeps, on any environment, or all
// of them without a parent.
func (a *App) parentChildren(entries []lifetime.Entry, parent *parentFilter) []lifetime.Entry {
	if parent == nil {
		return entries
	}
	ids := a.environmentIDs()
	var kept []lifetime.Entry
	for i := range entries {
		ws := &entries[i].Workspace
		env := localEnvironmentOf(ws)
		if parent.keeps(env, envIDIn(ids, env), ws.Parent, ws.ID) {
			kept = append(kept, entries[i])
		}
	}
	return kept
}

// childrenOf keeps the direct children of workspace parent, or everything
// for "".
func childrenOf(entries []lifetime.Entry, parent string) []lifetime.Entry {
	if parent == "" {
		return entries
	}
	var kept []lifetime.Entry
	for i := range entries {
		if entries[i].Workspace.Parent == parent {
			kept = append(kept, entries[i])
		}
	}
	return kept
}

// treeOrder puts each workspace right after its parent, deepest last, and
// gives its depth: 0 for one whose parent isn't listed (ADR 0044). Roots
// and siblings keep their order, oldest first.
func treeOrder(entries []lifetime.Entry) ([]int, []int) {
	return orderTree(len(entries), func(i int) (string, string) {
		return entries[i].Workspace.ID, entries[i].Workspace.Parent
	})
}

// orderTree is treeOrder for n items, whose id and parent id item(i) gives.
func orderTree(n int, item func(i int) (id, parent string)) ([]int, []int) {
	ids := make([]string, n)
	parents := make([]string, n)
	listed := map[string]bool{}
	for i := range n {
		ids[i], parents[i] = item(i)
		listed[ids[i]] = true
	}
	kids := map[string][]int{}
	var roots []int
	for i := range n {
		p := parents[i]
		if p != "" && p != ids[i] && listed[p] {
			kids[p] = append(kids[p], i)
			continue
		}
		roots = append(roots, i)
	}
	order := make([]int, 0, n)
	depths := make([]int, 0, n)
	seen := map[int]bool{}
	var visit func(i, depth int)
	visit = func(i, depth int) {
		if seen[i] {
			return
		}
		seen[i] = true
		order, depths = append(order, i), append(depths, depth)
		for _, c := range kids[ids[i]] {
			visit(c, depth+1)
		}
	}
	for _, r := range roots {
		visit(r, 0)
	}
	for i := range n {
		visit(i, 0)
	}
	return order, depths
}

// treeID is the ID column: the id, indented under its parent.
func treeID(id string, depth int) string {
	if depth == 0 {
		return id
	}
	return strings.Repeat("  ", depth-1) + "└ " + id
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
// directories, which have no id, creation time, session, repos, review, or
// loop run. loopRuns is the cache of loopRunInfo, or nil.
func (a *App) listWorkspaces(entries []lifetime.Entry, unknown []string, loopRuns map[string]*listLoopRunJSON) []listWorkspaceJSON {
	out := make([]listWorkspaceJSON, 0, len(entries)+len(unknown))
	for i := range entries {
		ws := listWorkspace(&entries[i])
		ws.Review = a.reviewRecord(entries[i].Workspace.ID)
		ws.LoopRun = a.cachedLoopRun(loopRuns, &entries[i])
		out = append(out, ws)
	}
	for _, dir := range unknown {
		out = append(out, listWorkspaceJSON{Path: dir, Repos: []listRepoJSON{}})
	}
	return out
}

// listedWorkspaces is listWorkspaces with each workspace's progress and
// rollup, counted over every workspace, hidden ones included (ADR 0077).
func (a *App) listedWorkspaces(entries []lifetime.Entry, unknown []string, loopRuns map[string]*listLoopRunJSON) ([]listWorkspaceJSON, error) {
	list, err := a.workspaces.List()
	if err != nil {
		return nil, fmt.Errorf("listing workspaces: %w", err)
	}
	out := a.listWorkspaces(entries, unknown, loopRuns)
	store := machine.NewStore(a.paths.State)
	for i := range out {
		if out[i].ID != nil {
			out[i].Progress, out[i].Rollup = a.workspaceProgress(store, list, *out[i].ID)
		}
	}
	return out, nil
}

// workspaceProgress is id's own progress and its rollup in the tree under its
// root. The rollup is null when no step is planned. Both are null when the
// tree can't be read, which is a warning.
func (a *App) workspaceProgress(store *machine.Store, list []workspace.Workspace, id string) (*progressJSON, *rollupJSON) {
	tree, err := progressTreeOf(store, machine.Root(list, id), list, id)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read %s's progress: %v", id, err))
		return nil, nil
	}
	return nodeProgress(tree)
}

func listWorkspace(e *lifetime.Entry) listWorkspaceJSON {
	ws := &e.Workspace
	repos := make([]listRepoJSON, 0, len(ws.Repos))
	for _, r := range ws.Repos {
		repos = append(repos, listRepoJSON{ID: r.ID, Path: r.Path, Ref: r.Ref, RefKind: r.RefKind})
	}
	created := formatTime(ws.CreatedAt)
	return listWorkspaceJSON{ID: &ws.ID, Name: optional(ws.Name), Path: ws.Path, CreatedAt: &created, Session: listSession(e.Session), Hidden: ws.Hidden, Parent: optional(ws.Parent), Repos: repos}
}

func listSession(s *lifetime.Session) *listSessionJSON {
	if s == nil {
		return nil
	}
	out := &listSessionJSON{Kind: s.Kind, ID: optional(s.ID), State: s.State, Stale: s.Stale, Runner: optional(s.Runner), Model: optional(s.Model), WSRunner: listWSRunner(s.WSRunner)}
	if s.PID != 0 {
		out.PID = &s.PID
	}
	return out
}

// listWSRunner is a ws runner's details as ws list shows them, nil for no
// runner.
func listWSRunner(info *lifetime.RunnerInfo) *listWSRunnerJSON {
	if info == nil {
		return nil
	}
	servers := info.MCP
	if servers == nil {
		servers = []mcp.Status{}
	}
	return &listWSRunnerJSON{
		ModelAlias:      info.Alias,
		Endpoint:        info.Endpoint,
		Step:            info.Step,
		MaxSteps:        info.MaxSteps,
		Restarts:        info.Restarts,
		Usage:           info.Usage,
		MCP:             servers,
		Exit:            info.Exit,
		EndpointChanges: info.EndpointChanges,
	}
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

// loopRunInfo is the loop_run of the listed workspace e, or nil when it
// isn't a loop run. Failures are warnings.
func (a *App) loopRunInfo(e *lifetime.Entry) *listLoopRunJSON {
	id := e.Workspace.ID
	mark, found, err := a.loopData.ReadRunMark(id)
	if err != nil {
		a.warned = append(a.warned, fmt.Sprintf("couldn't read workspace %s's loop run: %v", id, err))
		return nil
	}
	if !found {
		return nil
	}
	verdict := ""
	rec, err := machine.NewStore(a.paths.State).Read(id, id)
	switch {
	case err != nil:
		a.warned = append(a.warned, fmt.Sprintf("couldn't read workspace %s's machine record: %v", id, err))
	case rec.Result != nil:
		verdict = rec.Result.Verdict
	}
	state := ""
	if e.Session != nil {
		state = string(e.Session.State)
	}
	info := &listLoopRunJSON{Loop: mark.Loop}
	if attention := loop.AttentionOf(state, verdict); attention != loop.AttentionNone {
		info.Attention = optional(string(attention))
	}
	return info
}

// cachedLoopRun is loopRunInfo of e, remembered in runs by workspace id so
// each workspace is read once. A nil runs reads every time.
func (a *App) cachedLoopRun(runs map[string]*listLoopRunJSON, e *lifetime.Entry) *listLoopRunJSON {
	if runs == nil {
		return a.loopRunInfo(e)
	}
	id := e.Workspace.ID
	info, ok := runs[id]
	if !ok {
		info = a.loopRunInfo(e)
		runs[id] = info
	}
	return info
}

// attentionOf is the attention of a listed loop run, or none for nil.
func attentionOf(info *listLoopRunJSON) loop.Attention {
	if info == nil || info.Attention == nil {
		return loop.AttentionNone
	}
	return loop.Attention(*info.Attention)
}

// printList prints a table like `docker ps`, each workspace under the
// one that started it. withEnv adds the ENV column after ID; a listing
// without a remote entry has none, so its table is the same as it always was.
func (a *App) printList(rows []listRow, withEnv bool) {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	if withEnv {
		fmt.Fprintln(tw, "ID\tENV\tNAME\tCREATED\tSESSION\tREPOS")
	} else {
		fmt.Fprintln(tw, "ID\tNAME\tCREATED\tSESSION\tREPOS")
	}
	for i := range rows {
		r := &rows[i]
		id := treeID(r.id, r.depth)
		if withEnv {
			env := r.env
			if r.stale {
				env += " (offline)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", id, env, r.name, r.created, r.session, r.repos)
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, r.name, r.created, r.session, r.repos)
	}
	_ = tw.Flush()
}

// nameText is the NAME column: the display name, or "-" without one,
// followed by the attention of a loop run, or else "(hidden)" for a hidden
// workspace.
func nameText(name string, hidden bool, attention loop.Attention) string {
	if name == "" {
		name = "-"
	}
	switch {
	case attention == loop.AttentionBlocked:
		return name + " (blocked)"
	case attention == loop.AttentionNeedsAttention:
		return name + " (needs attention)"
	case hidden:
		return name + " (hidden)"
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
