package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/cache"
	"github.com/bobcob7/workspaces/internal/claude"
	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/workspace"
)

func (a *App) newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache <OP>",
		Short: "Manage, search, and resolve cached repos",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newCacheAddCmd(),
		a.newCacheUpdateCmd(),
		a.newCacheListCmd(),
		a.newCacheSearchCmd(),
		a.newCacheResolveCmd(),
		a.newCachePathCmd(),
		a.newCacheRemoveCmd(),
		a.newCachePruneCmd(),
	)
	return cmd
}

func (a *App) newCacheAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <SPEC>...",
		Short: "Clone repos into the cache if missing",
		Args:  allArgs(atLeastOneArg("a repo spec"), validSpecs),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cacheAdd(cmd.Context(), parseSpecs(args))
		})),
	}
}

func (a *App) newCacheUpdateCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "update [<SPEC>... | --all]",
		Short: "Fetch cached repos from upstream",
		Args: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) > 0) {
				return usageErrorf("%s needs repo specs or --all, not both", commandName(cmd))
			}
			return validSpecs(cmd, args)
		},
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cacheUpdate(cmd.Context(), parseSpecs(args), all)
		})),
	}
	cmd.Flags().BoolVar(&all, "all", false, "update every cached repo")
	return cmd
}

func (a *App) newCacheListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List cached repos",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.cacheList(cmd.Context())
		})),
	}
}

func (a *App) newCacheSearchCmd() *cobra.Command {
	var (
		limit int
		refs  bool
	)
	cmd := &cobra.Command{
		Use:   "search <QUERY>",
		Short: "Search cached repo ids",
		Args: func(cmd *cobra.Command, args []string) error {
			if limit < 1 {
				return usageErrorf("--limit must be at least 1")
			}
			return exactArgs(1, "query")(cmd, args)
		},
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cacheSearch(cmd.Context(), args[0], limit, refs)
		})),
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum results")
	cmd.Flags().BoolVar(&refs, "refs", false, "also search branch and tag names")
	return cmd
}

func (a *App) newCacheResolveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <SPEC>...",
		Short: "Resolve specs to canonical ids without cloning",
		Args:  allArgs(atLeastOneArg("a repo spec"), validSpecs),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cacheResolve(cmd.Context(), parseSpecs(args))
		})),
	}
}

func (a *App) newCachePathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path <SPEC>",
		Short: "Print the cache directory for a spec",
		Args:  allArgs(exactArgs(1, "repo spec"), validSpecs),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cachePath(cmd.Context(), &parseSpecs(args)[0])
		})),
	}
}

func (a *App) newCacheRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <SPEC>...",
		Short: "Delete cache entries",
		Args:  allArgs(atLeastOneArg("a repo spec"), validSpecs),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.cacheRemove(cmd.Context(), parseSpecs(args))
		})),
	}
}

func (a *App) newCachePruneCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Delete commit refs no workspace uses",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.cachePrune(cmd.Context(), dryRun)
		})),
	}
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "list what would be deleted")
	return cmd
}

// parseSpecs parses arguments that validSpecs has already checked.
func parseSpecs(args []string) []repo.Spec {
	specs := make([]repo.Spec, 0, len(args))
	for _, arg := range args {
		sp, err := repo.ParseSpec(arg)
		if err == nil {
			specs = append(specs, sp)
		}
	}
	return specs
}

type cacheAddJSON struct {
	ID       string         `json:"id"`
	Path     string         `json:"path"`
	Upstream string         `json:"upstream"`
	Added    bool           `json:"added"`
	Error    *itemErrorJSON `json:"error"`
}

// cacheAdd resolves every spec, then clones each repo that isn't cached. A
// spec that doesn't resolve stops the command before anything is cloned;
// a failed clone doesn't stop the others.
func (a *App) cacheAdd(ctx context.Context, specs []repo.Spec) error {
	resolved := make([]cache.Resolution, 0, len(specs))
	for i := range specs {
		r, err := a.cache.Resolve(ctx, &specs[i])
		if err != nil {
			return domainError(err)
		}
		resolved = append(resolved, r)
	}
	var items itemResults
	results := make([]cacheAddJSON, 0, len(resolved))
	for _, r := range resolved {
		added, err := a.cache.Add(ctx, r.ID, r.Upstream)
		results = append(results, cacheAddJSON{
			ID:       r.ID.String(),
			Path:     a.cache.Path(r.ID),
			Upstream: r.Upstream,
			Added:    added,
			Error:    items.add(r.ID.String(), err),
		})
	}
	return a.finishItems("repos", results, &items)
}

type itemErrorJSON struct {
	Code    errorCode `json:"code"`
	Message string    `json:"message"`
}

// itemResults collects a multi-repo command's per-item outcomes: the text
// line for each, and the first failure for the exit code (ADR 0016).
type itemResults struct {
	lines []itemLine
	first *cliError
}

type itemLine struct {
	text string
	err  *itemErrorJSON
}

// add records one item, printed as text on success, and returns its error
// for the JSON result: nil on success.
func (r *itemResults) add(text string, err error) *itemErrorJSON {
	if err == nil {
		r.lines = append(r.lines, itemLine{text: text})
		return nil
	}
	ce := asCLIError(domainError(err))
	if r.first == nil {
		r.first = ce
	}
	itemErr := &itemErrorJSON{Code: ce.code, Message: ce.Error()}
	r.lines = append(r.lines, itemLine{err: itemErr})
	return itemErr
}

// finishItems writes a multi-item result, {key: results} with --json or a
// line per item, failures on stderr. It returns the first failure, marked
// reported, so only its exit code is left to set.
func (a *App) finishItems(key string, results any, items *itemResults) error {
	if a.global.json {
		if err := a.writeJSON(map[string]any{key: results, "warnings": a.warnings()}); err != nil {
			return err
		}
	} else {
		for _, l := range items.lines {
			if l.err != nil {
				fmt.Fprintf(a.stderr, "ws: %s\n", l.err.Message)
				continue
			}
			fmt.Fprintln(a.stdout, l.text)
		}
		a.printWarnings()
	}
	if items.first == nil {
		return nil
	}
	items.first.reported = true
	return items.first
}

type cacheUpdateJSON struct {
	ID    string         `json:"id"`
	Error *itemErrorJSON `json:"error"`
}

// cacheUpdate fetches the given cached repos, or all of them, several at a
// time. One repo failing doesn't stop the others; results keep the order of
// the repos, and the exit code is the first failure's (ADR 0016).
func (a *App) cacheUpdate(ctx context.Context, specs []repo.Spec, all bool) error {
	ids, err := a.updateTargets(ctx, specs, all)
	if err != nil {
		return err
	}
	errs := a.cache.FetchEach(ctx, ids)
	var items itemResults
	results := make([]cacheUpdateJSON, 0, len(ids))
	for i, id := range ids {
		results = append(results, cacheUpdateJSON{ID: id.String(), Error: items.add("updated  "+id.String(), errs[i])})
	}
	return a.finishItems("repos", results, &items)
}

// updateTargets is every cached repo with --all, or the cached repos the
// specs name.
func (a *App) updateTargets(ctx context.Context, specs []repo.Spec, all bool) ([]repo.ID, error) {
	if all {
		ids, err := a.cache.IDs()
		if err != nil {
			return nil, domainError(err)
		}
		return ids, nil
	}
	ids := make([]repo.ID, 0, len(specs))
	for i := range specs {
		r, err := a.cache.Lookup(ctx, &specs[i])
		if err != nil {
			return nil, domainError(err)
		}
		ids = append(ids, r.ID)
	}
	return ids, nil
}

type cacheListJSON struct {
	ID            string `json:"id"`
	Path          string `json:"path"`
	Upstream      string `json:"upstream"`
	DefaultBranch string `json:"default_branch"`
	LastFetchedAt string `json:"last_fetched_at"`
	SizeBytes     int64  `json:"size_bytes"`
}

// cacheList lists cached repos.
func (a *App) cacheList(ctx context.Context) error {
	entries, err := a.cache.List(ctx)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		repos := make([]cacheListJSON, 0, len(entries))
		for i := range entries {
			e := &entries[i]
			repos = append(repos, cacheListJSON{
				ID:            e.ID.String(),
				Path:          e.Path,
				Upstream:      e.Upstream,
				DefaultBranch: e.DefaultBranch,
				LastFetchedAt: formatTime(e.LastFetchedAt),
				SizeBytes:     e.SizeBytes,
			})
		}
		return a.writeJSON(map[string]any{"repos": repos, "warnings": a.warnings()})
	}
	for i := range entries {
		fmt.Fprintln(a.stdout, entries[i].ID)
	}
	a.printWarnings()
	return nil
}

type cacheMatchJSON struct {
	ID  string  `json:"id"`
	Ref *string `json:"ref"`
}

// cacheSearch searches cached repo ids, and with refs their branches and
// tags.
func (a *App) cacheSearch(ctx context.Context, query string, limit int, refs bool) error {
	matches, err := a.cache.Search(ctx, query, limit, refs)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		out := make([]cacheMatchJSON, 0, len(matches))
		for _, m := range matches {
			out = append(out, cacheMatchJSON{ID: m.ID.String(), Ref: optional(m.Ref)})
		}
		return a.writeJSON(map[string]any{"matches": out, "warnings": a.warnings()})
	}
	for _, m := range matches {
		fmt.Fprintln(a.stdout, withRef(m.ID.String(), m.Ref))
	}
	a.printWarnings()
	return nil
}

type cacheResolveJSON struct {
	ID       string       `json:"id"`
	Ref      *string      `json:"ref"`
	Upstream string       `json:"upstream"`
	Cached   bool         `json:"cached"`
	Source   cache.Source `json:"source"`
}

// cacheResolve resolves specs to canonical ids without cloning or fetching.
func (a *App) cacheResolve(ctx context.Context, specs []repo.Spec) error {
	results := make([]cacheResolveJSON, 0, len(specs))
	for i := range specs {
		r, err := a.cache.Resolve(ctx, &specs[i])
		if err != nil {
			return domainError(err)
		}
		results = append(results, cacheResolveJSON{
			ID:       r.ID.String(),
			Ref:      optional(specs[i].Ref),
			Upstream: r.Upstream,
			Cached:   r.Cached,
			Source:   r.Source,
		})
	}
	if a.global.json {
		return a.writeJSON(map[string]any{"repos": results, "warnings": a.warnings()})
	}
	for i, r := range results {
		fmt.Fprintln(a.stdout, withRef(r.ID, specs[i].Ref))
	}
	a.printWarnings()
	return nil
}

// cachePath prints a cached repo's directory.
func (a *App) cachePath(ctx context.Context, sp *repo.Spec) error {
	r, err := a.cache.Lookup(ctx, sp)
	if err != nil {
		return domainError(err)
	}
	path := a.cache.Path(r.ID)
	if a.global.json {
		return a.writeJSON(map[string]any{"id": r.ID.String(), "path": path, "warnings": a.warnings()})
	}
	fmt.Fprintln(a.stdout, path)
	a.printWarnings()
	return nil
}

type cacheRemoveJSON struct {
	ID    string         `json:"id"`
	Path  string         `json:"path"`
	Error *itemErrorJSON `json:"error"`
}

// cacheRemove looks up every spec, then deletes each cached repo. A spec
// that names no cached repo stops the command before anything is deleted;
// a failed removal doesn't stop the others.
func (a *App) cacheRemove(ctx context.Context, specs []repo.Spec) error {
	ids := make([]repo.ID, 0, len(specs))
	for i := range specs {
		r, err := a.cache.Lookup(ctx, &specs[i])
		if err != nil {
			return domainError(err)
		}
		ids = append(ids, r.ID)
	}
	var items itemResults
	results := make([]cacheRemoveJSON, 0, len(ids))
	for _, id := range ids {
		path := a.cache.Path(id)
		err := a.cache.Remove(ctx, id)
		results = append(results, cacheRemoveJSON{ID: id.String(), Path: path, Error: items.add(id.String(), err)})
	}
	return a.finishItems("repos", results, &items)
}

type cachePruneJSON struct {
	ID     string `json:"id"`
	Commit string `json:"commit"`
}

// cachePrune deletes refs/ws/commits refs that no workspace's recorded
// commit uses. The workspaces are checked while each repo is locked, so a
// workspace being created meanwhile keeps its commit. If a workspace record
// can't be read, pruning stops, since that workspace might use one.
func (a *App) cachePrune(ctx context.Context, dryRun bool) error {
	keep := func(id repo.ID, commit string) (bool, error) {
		used, err := a.workspaces.UsesCommit(id.String(), commit)
		if err != nil {
			return false, fmt.Errorf("not pruning: %w", err)
		}
		return used, nil
	}
	pruned, err := a.cache.Prune(ctx, keep, dryRun)
	if err != nil {
		return domainError(err)
	}
	if a.global.json {
		refs := make([]cachePruneJSON, 0, len(pruned))
		for _, p := range pruned {
			refs = append(refs, cachePruneJSON{ID: p.ID.String(), Commit: p.Commit})
		}
		return a.writeJSON(map[string]any{"refs": refs, "warnings": a.warnings()})
	}
	verb := "pruned"
	if dryRun {
		verb = "would prune"
	}
	for _, p := range pruned {
		fmt.Fprintf(a.stdout, "%s  %s  %s\n", verb, p.ID, p.Commit)
	}
	a.printWarnings()
	return nil
}

// domainError gives a cache, git, or claude failure its error code (ADR
// 0016). When git or claude failed, its stderr is in details.stderr.
func domainError(err error) error {
	ce := codedDomainError(err)
	if ce == nil {
		return err
	}
	if stderr := commandStderr(err); stderr != "" {
		if ce.details == nil {
			ce.details = map[string]any{}
		}
		ce.details["stderr"] = stderr
	}
	return ce
}

// commandStderr is the stderr of the git or claude command err comes from,
// if any.
func commandStderr(err error) string {
	if cmdErr, ok := errors.AsType[*git.CommandError](err); ok {
		return cmdErr.Stderr
	}
	if cmdErr, ok := errors.AsType[*claude.CommandError](err); ok {
		return cmdErr.Stderr
	}
	return ""
}

// codedDomainError maps err to its error code, or nil when it has none.
func codedDomainError(err error) *cliError {
	if ae, ok := errors.AsType[*cache.AmbiguousError](err); ok {
		candidates := make([]string, 0, len(ae.Candidates))
		for _, id := range ae.Candidates {
			candidates = append(candidates, id.String())
		}
		ce := newError(codeRepoAmbiguous, err)
		ce.details = map[string]any{"candidates": candidates}
		return ce
	}
	for _, m := range []struct {
		target error
		code   errorCode
	}{
		{git.ErrNotInstalled, codeGitFailed},
		{git.ErrTooOld, codeGitFailed},
		{repo.ErrInvalidSpec, codeSpecInvalid},
		{cache.ErrRepoNotFound, codeRepoNotFound},
		{cache.ErrRefNotFound, codeRefNotFound},
		{cache.ErrCloneFailed, codeCloneFailed},
		{cache.ErrFetchFailed, codeFetchFailed},
		{workspace.ErrNameInvalid, codeNameInvalid},
		{workspace.ErrSpecConflict, codeSpecInvalid},
		{workspace.ErrNotFound, codeWorkspaceNotFound},
		{workspace.ErrExists, codeWorkspaceExists},
		{lifetime.ErrShellRunning, codeShellRunning},
		{lifetime.ErrBusy, codeWorkspaceBusy},
		{lifetime.ErrSessionElsewhere, codeSessionMismatch},
		{lifetime.ErrNotAgent, codeWorkspaceNotFound},
		{claude.ErrSettingsArg, codeUsage},
		{claude.ErrNotInstalled, codeClaudeMissing},
		{claude.ErrUntrusted, codeClaudeUntrusted},
		{claude.ErrTooOld, codeClaudeFailed},
		{claude.ErrNoSession, codeClaudeFailed},
		{claude.ErrUnexpectedOutput, codeClaudeFailed},
		{lifetime.ErrServiceFailed, codeClaudeFailed},
	} {
		if errors.Is(err, m.target) {
			return newError(m.code, err)
		}
	}
	if _, ok := errors.AsType[*git.CommandError](err); ok {
		return newError(codeGitFailed, err)
	}
	if _, ok := errors.AsType[*claude.CommandError](err); ok {
		return newError(codeClaudeFailed, err)
	}
	return nil
}

// warnings is every warning for JSON output: the removal pass's, the
// cache's, and the workspace store's. Never null.
func (a *App) warnings() []string {
	w := slices.Concat(a.lifetime.Warnings(), a.cache.Warnings(), a.workspaces.Warnings())
	if w == nil {
		return []string{}
	}
	return w
}

// printWarnings writes every warning to stderr, for text output.
func (a *App) printWarnings() {
	for _, w := range a.warnings() {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
}

// formatTime is RFC 3339 in UTC to the second (ADR 0016).
func formatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

// optional is nil for "", so JSON shows null rather than an empty string.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// withRef appends "@ref" to id when there is a ref.
func withRef(id, ref string) string {
	if ref == "" {
		return id
	}
	return id + "@" + ref
}
