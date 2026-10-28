package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/git"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/advisory"
	"github.com/bobcob7/workspaces/internal/review/entry"
	"github.com/bobcob7/workspaces/internal/review/guide"
	"github.com/bobcob7/workspaces/internal/review/profile"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/render"
	"github.com/bobcob7/workspaces/internal/review/schema"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/review/structural"
	"github.com/bobcob7/workspaces/internal/review/submit"
	"github.com/bobcob7/workspaces/internal/review/validate"
	"github.com/bobcob7/workspaces/internal/review/verify"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// newReviewCmd is the ws review group (ADR 0039): the requester's
// commands, and the reviewer's, run inside a review workspace.
func (a *App) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review <OP>",
		Short: "Review commits with structured, profiled reviews",
		Args:  knownOperation,
		RunE: runE(func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		}),
	}
	cmd.AddCommand(
		a.newReviewStartCmd(),
		a.newReviewWaitCmd(),
		a.newReviewReportCmd(),
		a.newReviewListCmd(),
		a.newReviewDecideCmd(),
		a.newReviewActedCmd(),
		a.newReviewProfilesCmd(),
		a.newReviewPrimeCmd(),
		a.newReviewDescribeCmd(),
		a.newReviewSubmitCmd(),
	)
	return cmd
}

func (a *App) newReviewPrimeCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:     "prime [--profile <NAME>]",
		Short:   "Print how to write and submit a review, and a profile's guide",
		Args:    noArgs,
		PreRunE: a.noJSON,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runReviewPrime(name, cmd.Flags().Changed("profile"))
		})),
	}
	cmd.Flags().StringVar(&name, "profile", "", "append this profile's guide; one of the review's profiles")
	return cmd
}

func (a *App) newReviewDescribeCmd() *cobra.Command {
	var lens string
	var list bool
	cmd := &cobra.Command{
		Use:     "describe [--lens <NAME>,... | --list]",
		Short:   "Explain the review document, a field, a check, or a topic",
		Args:    noArgs,
		PreRunE: a.noJSON,
		RunE: runE(a.withConfig(func(cmd *cobra.Command, _ []string) error {
			return a.runReviewDescribe(lens, cmd.Flags().Changed("lens"), list)
		})),
	}
	cmd.Flags().StringVar(&lens, "lens", "", "open these entries in full, comma separated")
	cmd.Flags().BoolVar(&list, "list", false, "list every entry's name")
	return cmd
}

func (a *App) newReviewSubmitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "submit [<PATH> | -]",
		Short: "Check a review and store it",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return usageErrorf("%s takes at most one path, got %d", commandName(cmd), len(args))
			}
			return nil
		},
		RunE: runE(a.withConfig(func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			return a.runReviewSubmit(cmd, path)
		})),
	}
}

// reviewWorkspace is the review workspace a reviewer's command runs in.
type reviewWorkspace struct {
	ws     workspace.Workspace
	record record.Record
}

// findReviewWorkspace finds the workspace holding the working directory
// and reads its review record. Outside a review workspace it fails with
// review_not_found (ADR 0039).
func (a *App) findReviewWorkspace() (reviewWorkspace, error) {
	dir, err := a.getwd()
	if err != nil {
		return reviewWorkspace{}, fmt.Errorf("finding the working directory: %w", err)
	}
	ws, err := a.workspaces.Containing(dir)
	if errors.Is(err, workspace.ErrNotFound) {
		return reviewWorkspace{}, newError(codeReviewNotFound, fmt.Errorf("%s isn't in a review workspace; run this where ws review start put the review", dir))
	}
	if err != nil {
		return reviewWorkspace{}, err
	}
	rec, err := record.Read(record.Path(a.paths.State, ws.ID))
	if errors.Is(err, record.ErrNotFound) {
		return reviewWorkspace{}, newError(codeReviewNotFound, fmt.Errorf("workspace %s isn't a review workspace", ws.ID))
	}
	if err != nil {
		return reviewWorkspace{}, fmt.Errorf("reading the review record: %w", err)
	}
	return reviewWorkspace{ws: ws, record: rec}, nil
}

// repo is the directory and id of the reviewed repo in the workspace.
func (rw *reviewWorkspace) repo() (string, repo.ID, error) {
	for _, r := range rw.ws.Repos {
		if r.ID != rw.record.Repo {
			continue
		}
		id, err := repo.ParseID(r.ID)
		if err != nil {
			return "", repo.ID{}, fmt.Errorf("reading the review record: %w", err)
		}
		return filepath.Join(rw.ws.Path, r.Path), id, nil
	}
	return "", repo.ID{}, fmt.Errorf("the review record's repo %s isn't in workspace %s", rw.record.Repo, rw.ws.ID)
}

// runReviewPrime prints the reviewer's workflow, followed by profile
// name's guide when given.
func (a *App) runReviewPrime(name string, given bool) error {
	rw, err := a.findReviewWorkspace()
	if err != nil {
		return err
	}
	text := guide.Prime()
	if given {
		p, err := a.reviewProfile(&rw.record, name)
		if err != nil {
			return err
		}
		text = guide.PrimeWithProfile(&p)
	}
	if _, err := io.WriteString(a.stdout, text); err != nil {
		return fmt.Errorf("writing the guide: %w", err)
	}
	return nil
}

// reviewProfile loads one of the review's profiles. A name the review
// doesn't run, or no profile has, is profile_unknown (ADR 0039).
func (a *App) reviewProfile(rec *record.Record, name string) (profile.Profile, error) {
	if !rec.HasProfile(name) {
		return profile.Profile{}, newError(codeProfileUnknown,
			fmt.Errorf("profile %q isn't one of this review's profiles: %s", name, strings.Join(rec.ProfileNames(), ", ")))
	}
	p, ok, err := profile.NewSet(a.paths.ReviewProfiles).Load(name)
	if err != nil {
		return profile.Profile{}, newError(codeConfigInvalid, err)
	}
	if !ok {
		return profile.Profile{}, newError(codeProfileUnknown, fmt.Errorf("no profile named %q", name))
	}
	return p, nil
}

// runReviewDescribe prints the document shape and the lens index, the
// index alone with list, or the entries lens names.
func (a *App) runReviewDescribe(lens string, lensGiven, list bool) error {
	if lensGiven && list {
		return usageErrorf("--list prints the index and --lens opens entries; use one")
	}
	if _, err := a.findReviewWorkspace(); err != nil {
		return err
	}
	registry, err := guide.NewRegistry()
	if err != nil {
		return err
	}
	out := render.NewJSON()
	switch {
	case list:
		err = out.Index(a.stdout, registry.Index())
	case !lensGiven:
		err = out.Summary(a.stdout, guide.DocumentShape(), registry.Index())
	default:
		entries, lensErr := resolveLenses(registry, lens)
		if lensErr != nil {
			return lensErr
		}
		err = out.Entries(a.stdout, entries)
	}
	if err != nil {
		return fmt.Errorf("writing the description: %w", err)
	}
	return nil
}

// resolveLenses turns a --lens value into entries, deduplicated and in the
// order given. An unknown or ambiguous name is a usage error; neither is
// guessed at.
func resolveLenses(registry *entry.Registry, value string) ([]entry.Entry, error) {
	entries := []entry.Entry{}
	seen := map[string]bool{}
	for name := range strings.SplitSeq(value, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, usageErrorf("--lens needs comma-separated names, got %q", value)
		}
		resolved, err := registry.Resolve(name)
		if ambiguous, ok := errors.AsType[*entry.AmbiguousLensError](err); ok {
			ce := usageErrorf("ambiguous lens %q; ask for one of %s", ambiguous.Name, strings.Join(ambiguous.Candidates, ", "))
			ce.details = map[string]any{"candidates": ambiguous.Candidates}
			return nil, ce
		}
		if err != nil {
			return nil, usageErrorf("unknown lens %q; ws review describe --list names every lens", name)
		}
		if !seen[resolved.Qualified()] {
			seen[resolved.Qualified()] = true
			entries = append(entries, resolved)
		}
	}
	return entries, nil
}

// submitJSON is ws review submit's output (docs/tools/ws-review.md).
type submitJSON struct {
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	Ref       string `json:"ref"`
	Profile   string `json:"profile"`
	// Path is the stored submission; Superseded is where the profile's
	// previous one went, or null.
	Path       string  `json:"path"`
	Superseded *string `json:"superseded"`
	submit.Report
	Warnings []string `json:"warnings"`
}

// runReviewSubmit checks the review at path, or on stdin, against the
// review workspace's record and repo, and stores it (ADRs 0037 and 0039).
func (a *App) runReviewSubmit(cmd *cobra.Command, path string) error {
	rw, err := a.findReviewWorkspace()
	if err != nil {
		return err
	}
	source, err := a.readReview(path)
	if err != nil {
		return newError(codeUsage, err)
	}
	dir, id, err := rw.repo()
	if err != nil {
		return err
	}
	checker, err := a.newReviewChecker()
	if err != nil {
		return err
	}
	out, err := checker.Check(cmd.Context(), &rw.record, dir, source)
	if err != nil {
		return err
	}
	report := submit.NewReport(out.Result)
	if out.Status != submit.StatusValid {
		return a.reviewRejected(out.Status, &report)
	}
	stored, err := store.New(a.paths.Reviews).Submit(cmd.Context(), id, rw.record.Ref, out.Profile, out.Document)
	if err != nil {
		return err
	}
	if a.global.json {
		return a.writeJSON(submitJSON{
			Workspace: rw.ws.ID, Repo: rw.record.Repo, Ref: rw.record.Ref, Profile: out.Profile,
			Path: stored.Path, Superseded: optional(stored.Superseded), Report: report, Warnings: a.warnings(),
		})
	}
	fmt.Fprintf(a.stdout, "stored the %s review of %s at %s: %s\n", out.Profile, rw.record.Repo, shortCommit(rw.record.Ref), stored.Path)
	if stored.Superseded != "" {
		fmt.Fprintf(a.stdout, "superseded your earlier review, now %s\n", stored.Superseded)
	}
	printDiagnostics(a.stdout, &report)
	a.printWarnings()
	return nil
}

// newReviewChecker wires the forked checks; anchor verification runs git
// through internal/git (ADR 0003).
func (a *App) newReviewChecker() (*submit.Checker, error) {
	v, err := schema.NewValidator()
	if err != nil {
		return nil, fmt.Errorf("compiling the review schema: %w", err)
	}
	runner := git.NewRunner(a.log, "git", verify.PlainEnv(a.environ))
	return submit.New(validate.New(
		structural.New(v, a.log),
		advisory.New(a.log, advisory.All()),
		validate.NewGitFinder(runner, a.log),
		a.log,
	)), nil
}

// readReview reads the document at path, or stdin for "" and "-".
func (a *App) readReview(path string) ([]byte, error) {
	if path == "" || path == "-" {
		source, err := io.ReadAll(a.stdin)
		if err != nil {
			return nil, fmt.Errorf("reading the review from stdin: %w", err)
		}
		return source, nil
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading the review: %w", err)
	}
	return source, nil
}

// reviewRejected is the error for a review that wasn't stored:
// review_invalid or review_unverifiable, with the report in details
// (ADR 0037). As text, the diagnostics and the lenses to open follow the
// message.
func (a *App) reviewRejected(status submit.Status, report *submit.Report) error {
	code := codeReviewInvalid
	err := fmt.Errorf("the review has %s; revise it and resubmit", plural(report.Counts.Errors, "error"))
	if status == submit.StatusUnverifiable {
		code = codeReviewUnverifiable
		err = errors.New("the review's anchors can't be checked against the reviewed commit; resubmitting won't help")
	}
	ce := newError(code, err)
	ce.details = map[string]any{
		"valid":        report.Valid,
		"verification": report.Verification,
		"counts":       report.Counts,
		"diagnostics":  report.Diagnostics,
		"skipped":      report.Skipped,
		"lenses":       report.Lenses,
	}
	if a.global.json {
		return ce
	}
	fmt.Fprintf(a.stderr, "ws: %s\n", ce.Error())
	printDiagnostics(a.stderr, report)
	ce.reported = true
	return ce
}

// printDiagnostics lists a report's diagnostics, then the lenses that
// explain them.
func printDiagnostics(w io.Writer, report *submit.Report) {
	for _, d := range report.Diagnostics {
		where := strings.TrimSpace(deref(d.Comment) + " " + deref(d.Path))
		if where != "" {
			where = " " + where
		}
		fmt.Fprintf(w, "  %s %s%s: %s\n", d.Severity, d.Name, where, d.Message)
	}
	if len(report.Lenses) > 0 {
		fmt.Fprintf(w, "  ws review describe --lens=%s explains them\n", strings.Join(report.Lenses, ","))
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
