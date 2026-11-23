package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/artifact"
)

// Listing limits for --limit (README.md, Limits).
const (
	defaultListLimit = 100
	maxListLimit     = 1000
)

// artifactSummaryJSON is an artifact without its revisions, as ws prints it
// (README.md › JSON shapes).
type artifactSummaryJSON struct {
	ID          string                `json:"id"`
	Reference   string                `json:"reference"`
	Name        string                `json:"name"`
	Title       *string               `json:"title"`
	Kind        string                `json:"kind"`
	ContentType string                `json:"content_type"`
	Latest      int                   `json:"latest"`
	Size        int64                 `json:"size"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
	CreatedBy   artifact.Actor        `json:"created_by"`
	Threads     artifact.ThreadCounts `json:"threads"`
}

// revisionJSON is one revision of an artifact, oldest first in artifactJSON.
type revisionJSON struct {
	Rev         int            `json:"rev"`
	ContentType string         `json:"content_type"`
	Size        int64          `json:"size"`
	SHA256      string         `json:"sha256"`
	CreatedAt   string         `json:"created_at"`
	CreatedBy   artifact.Actor `json:"created_by"`
	Note        *string        `json:"note"`
}

// artifactJSON is an artifact with its revisions.
type artifactJSON struct {
	artifactSummaryJSON
	Revisions []revisionJSON `json:"revisions"`
}

// artifactResultJSON is the output of add, show and put: one artifact.
type artifactResultJSON struct {
	Artifact artifactJSON `json:"artifact"`
	Warnings []string     `json:"warnings"`
}

// artifactListJSON is the output of list: artifact summaries, latest first.
type artifactListJSON struct {
	Artifacts []artifactSummaryJSON `json:"artifacts"`
	Warnings  []string              `json:"warnings"`
}

// artifactRemoveJSON is one artifact in the output of rm.
type artifactRemoveJSON struct {
	ID      string         `json:"id"`
	Removed bool           `json:"removed"`
	Error   *itemErrorJSON `json:"error"`
}

// artifactRemoveResultJSON is the output of rm: one item per argument.
type artifactRemoveResultJSON struct {
	Artifacts []artifactRemoveJSON `json:"artifacts"`
	Warnings  []string             `json:"warnings"`
}

// summaryJSON is e as a summary: its latest revision's type, size and time,
// and its thread counts.
func summaryJSON(e *artifact.Entry) artifactSummaryJSON {
	rec := &e.Record
	latest := rec.Latest()
	return artifactSummaryJSON{
		ID:          rec.ID,
		Reference:   "@artifact:" + rec.ID,
		Name:        rec.Name,
		Title:       rec.Title,
		Kind:        string(rec.Kind),
		ContentType: latest.ContentType,
		Latest:      latest.Rev,
		Size:        latest.Size,
		CreatedAt:   formatTime(rec.CreatedAt),
		UpdatedAt:   formatTime(latest.CreatedAt),
		CreatedBy:   rec.CreatedBy,
		Threads:     e.Threads,
	}
}

// fullJSON is e with every revision, oldest first.
func fullJSON(e *artifact.Entry) artifactJSON {
	revisions := make([]revisionJSON, 0, len(e.Record.Revisions))
	for i := range e.Record.Revisions {
		r := &e.Record.Revisions[i]
		revisions = append(revisions, revisionJSON{
			Rev:         r.Rev,
			ContentType: r.ContentType,
			Size:        r.Size,
			SHA256:      r.SHA256,
			CreatedAt:   formatTime(r.CreatedAt),
			CreatedBy:   r.CreatedBy,
			Note:        r.Note,
		})
	}
	return artifactJSON{artifactSummaryJSON: summaryJSON(e), Revisions: revisions}
}

// artifactStore is the store over a.paths.Artifacts (ADR 0078).
func (a *App) artifactStore() *artifact.Store {
	return artifact.NewStore(a.paths.Artifacts, rand.Reader, a.now)
}

// artifactError maps the store's errors to ws error codes (README.md ›
// Error codes). nil stays nil; anything else is returned as it is.
func artifactError(err error) error {
	if err == nil {
		return nil
	}
	if conflict, ok := errors.AsType[*artifact.ConflictError](err); ok {
		return &cliError{code: codeArtifactConflict, err: err, details: map[string]any{"latest": conflict.Latest}}
	}
	switch {
	case errors.Is(err, artifact.ErrInvalid), errors.Is(err, artifact.ErrNewer):
		return newError(codeArtifactInvalid, err)
	case errors.Is(err, artifact.ErrNotFound):
		return newError(codeArtifactUnknown, err)
	case errors.Is(err, artifact.ErrFull):
		return newError(codeArtifactFull, err)
	}
	return err
}

// readArtifactInput reads the file at path, or stdin for "-", and returns
// the name to store it under: name, else the file's base name. "-" needs a
// name, since stdin has none (usage). A file over MaxImageSize is
// artifact_invalid; the read stops one byte past the limit.
func (a *App) readArtifactInput(path, name string) (string, []byte, error) {
	if name == "" && path == "-" {
		return "", nil, usageErrorf("reading stdin needs --name")
	}
	if name == "" {
		name = filepath.Base(path)
	}
	src, closeSrc, err := a.artifactSource(path)
	if err != nil {
		return "", nil, err
	}
	defer closeSrc()
	data, err := io.ReadAll(io.LimitReader(src, artifact.MaxImageSize+1))
	if err != nil {
		return "", nil, newError(codeArtifactInvalid, fmt.Errorf("reading %s: %w", path, err))
	}
	if len(data) > artifact.MaxImageSize {
		return "", nil, newError(codeArtifactInvalid, fmt.Errorf("%s is over %d bytes", path, artifact.MaxImageSize))
	}
	return name, data, nil
}

// artifactSource opens path for reading, or returns stdin for "-". The
// returned func closes it.
func (a *App) artifactSource(path string) (io.Reader, func(), error) {
	if path == "-" {
		return a.stdin, func() {}, nil
	}
	f, err := os.Open(path) //nolint:gosec // G304: the file is the one the person or agent named.
	if err != nil {
		return nil, nil, newError(codeArtifactInvalid, fmt.Errorf("reading %s: %w", path, err))
	}
	return f, func() { _ = f.Close() }, nil
}

// artifactRef parses an <ID> argument, which may be a reference with /r<n>.
// rev is the --rev flag, used when the argument names no revision; a
// reference's revision and a different --rev is usage.
func artifactRef(arg string, rev int, revSet bool) (id string, r int, err error) {
	id, refRev, err := artifact.ParseRef(arg)
	if err != nil {
		return "", 0, newError(codeArtifactUnknown, err)
	}
	switch {
	case revSet && rev < 1:
		return "", 0, usageErrorf("--rev must be at least 1, not %d", rev)
	case refRev != 0 && revSet && refRev != rev:
		return "", 0, usageErrorf("%s names r%d, so --rev %d can't also be given", arg, refRev, rev)
	case refRev != 0:
		return id, refRev, nil
	}
	return id, rev, nil
}

// artifactArgs requires exactly n positional arguments, described by what.
func artifactArgs(n int, what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return usageErrorf("%s needs %s", commandName(cmd), what)
		}
		return nil
	}
}

// checkArtifactKind accepts "" (any kind) or one of the kinds list --kind takes.
func checkArtifactKind(kind string) error {
	switch artifact.Kind(kind) {
	case "", artifact.KindImage, artifact.KindMarkdown, artifact.KindText:
		return nil
	}
	return usageErrorf("--kind must be image, markdown or text, not %q", kind)
}

// checkListLimit accepts a --limit from 1 to maxListLimit.
func checkListLimit(limit int) error {
	if limit < 1 || limit > maxListLimit {
		return usageErrorf("--limit must be from 1 to %d, not %d", maxListLimit, limit)
	}
	return nil
}

// artifactWarnings is the store's warnings, then the other warnings ws has.
// Never null.
func (a *App) artifactWarnings(store []string) []string {
	w := slices.Concat(store, a.warnings())
	if w == nil {
		return []string{}
	}
	return w
}

// writeArtifactResult prints e as the output of add, show and put.
func (a *App) writeArtifactResult(e *artifact.Entry) error {
	return a.writeJSON(artifactResultJSON{Artifact: fullJSON(e), Warnings: a.artifactWarnings(nil)})
}

// artifactLine is one artifact for text output: its id, kind, size and its
// title, or its name when it has none.
func artifactLine(e *artifact.Entry) string {
	label := e.Record.Name
	if e.Record.Title != nil {
		label = *e.Record.Title
	}
	latest := e.Record.Latest()
	return fmt.Sprintf("%s  %s %d  %s", e.Record.ID, e.Record.Kind, latest.Size, label)
}

// artifactFilter is which artifacts a list keeps, up to limit: those of one
// kind, those a workspace created or whose tree it is in, or with mine
// those whose tree is root.
type artifactFilter struct {
	kind      string
	workspace string
	mine      bool
	root      string
	limit     int
}

// keep reports whether rec passes every filter the list sets.
func (f *artifactFilter) keep(rec *artifact.Record) bool {
	switch {
	case f.kind != "" && string(rec.Kind) != f.kind:
		return false
	case f.workspace != "" && !createdIn(rec, f.workspace):
		return false
	case f.mine && !(rec.Root != nil && *rec.Root == f.root):
		return false
	}
	return true
}

// createdIn reports whether rec was created by workspace id or in its tree.
func createdIn(rec *artifact.Record, id string) bool {
	byWorkspace := rec.CreatedBy.Workspace != nil && *rec.CreatedBy.Workspace == id
	return byWorkspace || (rec.Root != nil && *rec.Root == id)
}

// listArtifacts prints the artifacts f keeps: as JSON, or a line each.
func (a *App) listArtifacts(f *artifactFilter) error {
	entries, warnings, err := a.artifactStore().List()
	if err != nil {
		return artifactError(err)
	}
	kept := make([]*artifact.Entry, 0, f.limit)
	for i := range entries {
		if len(kept) == f.limit {
			break
		}
		if f.keep(&entries[i].Record) {
			kept = append(kept, &entries[i])
		}
	}
	if !a.global.json {
		return a.printArtifactLines(kept)
	}
	summaries := make([]artifactSummaryJSON, 0, len(kept))
	for _, e := range kept {
		summaries = append(summaries, summaryJSON(e))
	}
	return a.writeJSON(artifactListJSON{Artifacts: summaries, Warnings: a.artifactWarnings(warnings)})
}

// printArtifactLines writes one text line per artifact.
func (a *App) printArtifactLines(entries []*artifact.Entry) error {
	for _, e := range entries {
		if _, err := fmt.Fprintln(a.stdout, artifactLine(e)); err != nil {
			return fmt.Errorf("writing artifacts: %w", err)
		}
	}
	return nil
}

// addArtifact stores the file at path as a new artifact, by actor and in the
// tree root ("" for a person).
func (a *App) addArtifact(ctx context.Context, path string, opts *artifactAddOptions, by artifact.Actor, root string) (artifact.Record, error) {
	name, data, err := a.readArtifactInput(path, opts.name)
	if err != nil {
		return artifact.Record{}, err
	}
	rec, err := a.artifactStore().Add(ctx, &artifact.NewArtifact{
		Name: name, Title: opts.title, Note: opts.note, Data: data, By: by, Root: root,
	})
	return rec, artifactError(err)
}

// artifactAddOptions are add's flags, for people and agents.
type artifactAddOptions struct {
	name  string
	title string
	note  string
}

// addArtifactFlags sets add's flags on cmd.
func addArtifactFlags(cmd *cobra.Command, opts *artifactAddOptions) {
	cmd.Flags().StringVar(&opts.name, "name", "", "the name to store the file under; needed for -, else the file's base name")
	cmd.Flags().StringVar(&opts.title, "title", "", "a title to show instead of the name, up to 200 characters")
	cmd.Flags().StringVar(&opts.note, "note", "", "a note on the first revision, up to 200 characters")
}

// showArtifact prints the artifact arg names: as JSON, or a line.
func (a *App) showArtifact(arg string) error {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return err
	}
	entry, err := a.artifactStore().Show(id)
	if err != nil {
		return artifactError(err)
	}
	if a.global.json {
		return a.writeArtifactResult(&entry)
	}
	return a.printArtifactLines([]*artifact.Entry{&entry})
}

// removeArtifacts removes each artifact the arguments name, if check allows
// it. A failure doesn't stop the others; the exit code is the first failure's
// (ADR 0016).
func (a *App) removeArtifacts(ctx context.Context, args []string, check func(*artifact.Entry) error) error {
	var items itemResults
	results := make([]artifactRemoveJSON, 0, len(args))
	for _, arg := range args {
		id, err := a.removeArtifact(ctx, arg, check)
		results = append(results, artifactRemoveJSON{ID: id, Removed: err == nil, Error: items.add(id, err)})
	}
	return a.finishItems("artifacts", results, &items)
}

// removeArtifact removes the artifact arg names, after check passes for it.
// It returns the artifact's id, or arg when it names none.
func (a *App) removeArtifact(ctx context.Context, arg string, check func(*artifact.Entry) error) (string, error) {
	id, _, err := artifactRef(arg, 0, false)
	if err != nil {
		return arg, err
	}
	store := a.artifactStore()
	entry, err := store.Show(id)
	if err != nil {
		return id, artifactError(err)
	}
	if err = check(&entry); err != nil {
		return id, err
	}
	return id, artifactError(store.Remove(ctx, id))
}

// anyoneMayRemove is removeArtifacts' check for people, who remove any artifact.
func anyoneMayRemove(*artifact.Entry) error {
	return nil
}

// newArtifactCmd is ws artifact, for people and the desktop app (ADR 0078).
// It is hidden from ws --help, and it writes as a person.
func (a *App) newArtifactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "artifact <OP>",
		Short:  "Share files with agents, for people and the desktop app",
		Hidden: true,
		Args:   knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(a.newArtifactAddCmd(), a.newArtifactListCmd(), a.newArtifactShowCmd(), a.newArtifactCatCmd(), a.newArtifactRmCmd())
	return cmd
}

func (a *App) newArtifactAddCmd() *cobra.Command {
	var opts artifactAddOptions
	cmd := &cobra.Command{
		Use:   "add <PATH|-> [--name <name>] [--title <text>] [--note <text>]",
		Short: "Store a file as a new artifact",
		Args:  artifactArgs(1, "a file to add, or - for stdin"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			rec, err := a.addArtifact(cmd.Context(), args[0], &opts, artifact.Person(), "")
			if err != nil {
				return err
			}
			if a.global.json {
				return a.writeArtifactResult(&artifact.Entry{Record: rec})
			}
			_, err = fmt.Fprintln(a.stdout, rec.ID)
			return err
		})),
	}
	addArtifactFlags(cmd, &opts)
	return cmd
}

func (a *App) newArtifactListCmd() *cobra.Command {
	var f artifactFilter
	cmd := &cobra.Command{
		Use:   "list [--kind image|markdown|text] [--workspace <id>] [--limit <n>]",
		Short: "List artifacts, latest first",
		Args:  noArgs,
		RunE: runE(a.withConfig(func(_ *cobra.Command, _ []string) error {
			if err := errors.Join(checkArtifactKind(f.kind), checkListLimit(f.limit)); err != nil {
				return err
			}
			return a.listArtifacts(&f)
		})),
	}
	cmd.Flags().StringVar(&f.kind, "kind", "", "only artifacts of this kind: image, markdown or text")
	cmd.Flags().StringVar(&f.workspace, "workspace", "", "only artifacts this workspace created, or in its tree")
	cmd.Flags().IntVar(&f.limit, "limit", defaultListLimit, "how many to list, from 1 to 1000")
	return cmd
}

func (a *App) newArtifactShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <ID>",
		Short: "Show an artifact and its revisions",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: runE(a.withConfig(func(_ *cobra.Command, args []string) error {
			return a.showArtifact(args[0])
		})),
	}
}

func (a *App) newArtifactCatCmd() *cobra.Command {
	var rev int
	cmd := &cobra.Command{
		Use:   "cat <ID> [--rev <n>]",
		Short: "Write an artifact's bytes to stdout",
		Args:  artifactArgs(1, "an artifact id"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			id, r, err := artifactRef(args[0], rev, cmd.Flags().Changed("rev"))
			if err != nil {
				return err
			}
			_, _, data, err := a.artifactStore().Read(id, r)
			if err != nil {
				return artifactError(err)
			}
			_, err = a.stdout.Write(data)
			return err
		})),
	}
	cmd.Flags().IntVar(&rev, "rev", 0, "the revision to write, default the latest")
	return cmd
}

func (a *App) newArtifactRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <ID>...",
		Short: "Remove artifacts, whoever made them",
		Args:  atLeastOneArg("an artifact"),
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			return a.removeArtifacts(cmd.Context(), args, anyoneMayRemove)
		})),
	}
}
