package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/render"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// Report formats for ws review report and wait.
const (
	formatJSON     = "json"
	formatMarkdown = "markdown"
	formatHTML     = "html"
)

// defaultReviewPoll is how often ws review wait reads the review agent's
// session (ADR 0039).
const defaultReviewPoll = 5 * time.Second

var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// reviewSubjectOptions name a review: a workspace, or a repo and commit.
type reviewSubjectOptions struct {
	repo string
	ref  string
}

func (a *App) newReviewReportCmd() *cobra.Command {
	var subject reviewSubjectOptions
	var format string
	cmd := &cobra.Command{
		Use:   "report [--format json|markdown|html] (<ID> | --repo <REPO> --ref <SHA>)",
		Short: "Print a review's combined report",
		Args:  reviewSubjectArgs(&subject),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			return a.runReviewReport(cmd.Context(), args, &subject, format)
		})),
	}
	addReviewSubjectFlags(cmd, &subject)
	cmd.Flags().StringVar(&format, "format", "", "json, markdown, or html; default: json with --json, else markdown")
	return cmd
}

func (a *App) newReviewWaitCmd() *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait [--timeout <DURATION>] <ID>",
		Short: "Wait for a review to finish, then print its report",
		Args:  exactArgs(1, "review workspace"),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("timeout") {
				timeout = a.config.Review.WaitTimeout()
			}
			return a.runReviewWait(cmd.Context(), args[0], timeout)
		})),
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long, such as 90m; default: [review] timeout, 60m")
	return cmd
}

func addReviewSubjectFlags(cmd *cobra.Command, subject *reviewSubjectOptions) {
	cmd.Flags().StringVar(&subject.repo, "repo", "", "the reviewed repo's id, host/owner/name")
	cmd.Flags().StringVar(&subject.ref, "ref", "", "the reviewed commit, a full SHA")
}

// reviewSubjectArgs accepts a workspace id, or --repo and --ref, not both.
func reviewSubjectArgs(subject *reviewSubjectOptions) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		byRepo := subject.repo != "" || subject.ref != ""
		switch {
		case len(args) > 1:
			return usageErrorf("%s takes one review workspace, got %d", commandName(cmd), len(args))
		case len(args) == 1 && byRepo:
			return usageErrorf("%s takes a review workspace or --repo and --ref, not both", commandName(cmd))
		case len(args) == 0 && (subject.repo == "" || subject.ref == ""):
			return usageErrorf("%s needs a review workspace, or both --repo and --ref", commandName(cmd))
		case byRepo && !fullCommitPattern.MatchString(subject.ref):
			return usageErrorf("--ref needs the reviewed commit's full 40-character SHA, got %q", subject.ref)
		}
		return nil
	}
}

// reviewSubject is a review found by its workspace or by repo and commit.
type reviewSubject struct {
	id  repo.ID
	ref string
	// record and agent are nil when no workspace holds the review.
	record *record.Record
	agent  *reviewAgentJSON
}

// reviewAgentJSON is the review agent's workspace and session state.
type reviewAgentJSON struct {
	Workspace string         `json:"workspace"`
	Status    lifetime.State `json:"status"`
}

// findReviewByID finds the review workspace arg names and reads its
// record. A missing workspace or record is review_not_found.
func (a *App) findReviewByID(arg string) (workspace.Workspace, record.Record, error) {
	ws, err := a.workspaces.Find(arg)
	if errors.Is(err, workspace.ErrNotFound) {
		return workspace.Workspace{}, record.Record{}, newError(codeReviewNotFound, fmt.Errorf("no review workspace %q; with the workspace removed, use --repo and --ref", arg))
	}
	if err != nil {
		return workspace.Workspace{}, record.Record{}, err
	}
	rec, err := record.Read(record.Path(a.paths.State, ws.ID))
	if errors.Is(err, record.ErrNotFound) {
		return workspace.Workspace{}, record.Record{}, newError(codeReviewNotFound, fmt.Errorf("workspace %s isn't a review workspace", ws.ID))
	}
	if err != nil {
		return workspace.Workspace{}, record.Record{}, fmt.Errorf("reading the review record: %w", err)
	}
	return ws, rec, nil
}

// subjectOf is the review of rec in the workspace with id, its agent's
// state read from listing.
func subjectOf(id string, rec *record.Record, listing *lifetime.Listing) (reviewSubject, error) {
	repoID, err := repo.ParseID(rec.Repo)
	if err != nil {
		return reviewSubject{}, fmt.Errorf("reading the review record: %w", err)
	}
	return reviewSubject{id: repoID, ref: rec.Ref, record: rec, agent: agentOf(id, listing)}, nil
}

// agentOf is the state of workspace id's agent in listing, or nil when
// the workspace is gone.
func agentOf(id string, listing *lifetime.Listing) *reviewAgentJSON {
	for i := range listing.Workspaces {
		e := &listing.Workspaces[i]
		if e.Workspace.ID != id {
			continue
		}
		status := lifetime.StateStarting
		if e.Session != nil {
			status = e.Session.State
		}
		return &reviewAgentJSON{Workspace: id, Status: status}
	}
	return nil
}

// findReviewByCommit finds the review of repo at ref: the newest
// workspace whose record names them, or none, which isn't an error, since
// the submissions outlive the workspace.
func (a *App) findReviewByCommit(subject *reviewSubjectOptions, listing *lifetime.Listing) (reviewSubject, error) {
	id, err := repo.ParseID(subject.repo)
	if err != nil {
		return reviewSubject{}, newError(codeUsage, fmt.Errorf("--repo needs a repo id, host/owner/name: %w", err))
	}
	found := reviewSubject{id: id, ref: subject.ref}
	for i := range listing.Workspaces {
		ws := &listing.Workspaces[i].Workspace
		rec, err := record.Read(record.Path(a.paths.State, ws.ID))
		if errors.Is(err, record.ErrNotFound) {
			continue
		}
		if err != nil {
			a.warned = append(a.warned, fmt.Sprintf("couldn't read workspace %s's review record: %v", ws.ID, err))
			continue
		}
		if rec.Repo == id.String() && rec.Ref == subject.ref {
			found.record, found.agent = &rec, agentOf(ws.ID, listing)
		}
	}
	return found, nil
}

// runReviewReport prints the review's combined report.
func (a *App) runReviewReport(ctx context.Context, args []string, opts *reviewSubjectOptions, format string) error {
	format, err := a.reportFormat(format)
	if err != nil {
		return err
	}
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	subject, err := a.reportSubject(args, opts, &listing)
	if err != nil {
		return err
	}
	return a.printReport(ctx, &subject, format)
}

// reportSubject finds the review a workspace, or --repo and --ref, name.
func (a *App) reportSubject(args []string, opts *reviewSubjectOptions, listing *lifetime.Listing) (reviewSubject, error) {
	if len(args) == 0 {
		return a.findReviewByCommit(opts, listing)
	}
	ws, rec, err := a.findReviewByID(args[0])
	if err != nil {
		return reviewSubject{}, err
	}
	return subjectOf(ws.ID, &rec, listing)
}

// reportFormat checks --format against --json: json by default with
// --json, markdown without; --json takes only json.
func (a *App) reportFormat(format string) (string, error) {
	switch {
	case format == "" && a.global.json:
		return formatJSON, nil
	case format == "":
		return formatMarkdown, nil
	case !slices.Contains([]string{formatJSON, formatMarkdown, formatHTML}, format):
		return "", usageErrorf("--format must be json, markdown, or html, not %q", format)
	case a.global.json && format != formatJSON:
		return "", usageErrorf("--json prints JSON; leave it off for --format %s", format)
	}
	return format, nil
}

// runReviewWait polls the review agent's session every reviewPoll until
// it is idle or stopped, or its workspace is gone, then prints the report
// as ws review report does (ADR 0039). Waiting longer than timeout is
// review_timeout.
func (a *App) runReviewWait(ctx context.Context, arg string, timeout time.Duration) error {
	if timeout <= 0 {
		return usageErrorf("--timeout must be positive, got %s", timeout)
	}
	format, err := a.reportFormat("")
	if err != nil {
		return err
	}
	ws, rec, err := a.findReviewByID(arg)
	if err != nil {
		return err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		listing, err := a.lifetime.List(ctx)
		if err != nil {
			return err
		}
		subject, err := subjectOf(ws.ID, &rec, &listing)
		if err != nil {
			return err
		}
		if reviewFinished(subject.agent) {
			return a.printReport(ctx, &subject, format)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for review %s: %w", ws.ID, ctx.Err())
		case <-deadline.C:
			ce := newError(codeReviewTimeout, fmt.Errorf("review %s is still %s after %s; run ws review wait again to keep waiting", ws.ID, subject.agent.Status, timeout))
			ce.details = map[string]any{"workspace": ws.ID, "status": subject.agent.Status}
			return ce
		case <-time.After(a.reviewPoll):
		}
	}
}

// reviewFinished reports whether the review agent is done: idle, stopped,
// or its workspace removed.
func reviewFinished(agent *reviewAgentJSON) bool {
	return agent == nil || agent.Status == lifetime.StateIdle || agent.Status == lifetime.StateStopped
}

// reviewReportJSON is ws review report's output (ADR 0039).
type reviewReportJSON struct {
	Repo   string           `json:"repo"`
	Ref    string           `json:"ref"`
	Review *record.Record   `json:"review"`
	Agent  *reviewAgentJSON `json:"agent"`
	// Profiles are the record's profiles, then any other profile that
	// submitted.
	Profiles []reviewProfileJSON `json:"profiles"`
	// Comments are the current submissions' findings, and Superseded the
	// findings of submissions a resubmission replaced.
	Comments   []reviewCommentJSON `json:"comments"`
	Superseded []reviewCommentJSON `json:"superseded"`
	Warnings   []string            `json:"warnings"`
}

type reviewProfileJSON struct {
	Name     string        `json:"name"`
	Model    string        `json:"model"`
	Verdict  *string       `json:"verdict"`
	Severity *severityJSON `json:"severity"`
	// Missing means the profile has no current submission.
	Missing bool `json:"missing"`
}

type severityJSON struct {
	Max         *int `json:"max"`
	MustFix     int  `json:"must_fix"`
	ShouldFix   int  `json:"should_fix"`
	WorthFixing int  `json:"worth_fixing"`
	Optional    int  `json:"optional"`
}

type reviewCommentJSON struct {
	ID          string                 `json:"id"`
	Profile     string                 `json:"profile"`
	Priority    int                    `json:"priority"`
	Category    string                 `json:"category"`
	Body        string                 `json:"body"`
	Code        *string                `json:"code"`
	Anchors     []reviewAnchorJSON     `json:"anchors"`
	Suggestions []reviewSuggestionJSON `json:"suggestions"`
	// Decision is ADR 0040's; null until ws review decide exists.
	Decision any `json:"decision"`
}

type reviewAnchorJSON struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	EndLine *int   `json:"end_line"`
}

type reviewSuggestionJSON struct {
	Summary string   `json:"summary"`
	Effort  string   `json:"effort"`
	Scope   string   `json:"scope"`
	Pros    []string `json:"pros"`
	Cons    []string `json:"cons"`
	Code    *string  `json:"code"`
}

// printReport combines the subject's submissions and prints them in
// format.
func (a *App) printReport(ctx context.Context, subject *reviewSubject, format string) error {
	result, err := store.New(a.paths.Reviews).Collect(ctx, subject.id, subject.ref)
	if err != nil {
		return err
	}
	if result.Unreadable > 0 {
		a.warned = append(a.warned, fmt.Sprintf("skipped %s that couldn't be read", plural(result.Unreadable, "stored submission")))
	}
	report := buildReport(subject, result)
	report.Warnings = a.warnings()
	if format == formatJSON {
		return a.writeJSON(report)
	}
	envelope := render.CollectReviewsEnvelope{
		Ref: subject.ref, RepoName: subject.id.String(), RepoKnown: true, StoreEnabled: true,
		HeadCheck: render.CollectReviewsHeadCheck{Source: "none"}, Result: result,
	}
	if format == formatHTML {
		err = render.NewHTML().CollectReviews(a.stdout, envelope)
	} else {
		err = a.writeMarkdownReport(&envelope, &report)
	}
	if err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}
	a.printWarnings()
	return nil
}

// writeMarkdownReport writes the forked Markdown report, then the
// profiles that haven't submitted.
func (a *App) writeMarkdownReport(envelope *render.CollectReviewsEnvelope, report *reviewReportJSON) error {
	if err := render.NewMarkdown().CollectReviews(a.stdout, *envelope); err != nil {
		return err
	}
	var missing []string
	for _, p := range report.Profiles {
		if p.Missing {
			missing = append(missing, p.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	_, err := io.WriteString(a.stdout, "\n**Missing** "+strings.Join(missing, ", ")+"\n")
	return err
}

// buildReport is the JSON report of result for subject.
func buildReport(subject *reviewSubject, result *collect.Result) reviewReportJSON {
	report := reviewReportJSON{
		Repo: subject.id.String(), Ref: subject.ref, Review: subject.record, Agent: subject.agent,
		Profiles: reportProfiles(subject.record, result.Submissions), Comments: []reviewCommentJSON{}, Superseded: []reviewCommentJSON{},
	}
	for i := range result.Comments {
		c := reportComment(&result.Comments[i])
		if strings.HasPrefix(c.ID, "#") {
			report.Superseded = append(report.Superseded, c)
		} else {
			report.Comments = append(report.Comments, c)
		}
	}
	return report
}

// reportProfiles is one entry per profile in rec, in its order, then one
// per other profile with a current submission. A profile without one is
// missing.
func reportProfiles(rec *record.Record, submissions []collect.Submission) []reviewProfileJSON {
	current := map[string]*collect.Submission{}
	var names []string
	for i := range submissions {
		s := &submissions[i]
		if s.Profile != "" && s.SupersededBy == nil {
			current[s.Profile] = s
			names = append(names, s.Profile)
		}
	}
	profiles := []reviewProfileJSON{}
	if rec != nil {
		for _, p := range rec.Profiles {
			profiles = append(profiles, reportProfile(p.Name, p.Model, current[p.Name]))
		}
	}
	for _, name := range names {
		if rec == nil || !rec.HasProfile(name) {
			profiles = append(profiles, reportProfile(name, "", current[name]))
		}
	}
	return profiles
}

func reportProfile(name, model string, s *collect.Submission) reviewProfileJSON {
	if s == nil {
		return reviewProfileJSON{Name: name, Model: model, Missing: true}
	}
	verdict := s.Verdict
	return reviewProfileJSON{Name: name, Model: model, Verdict: &verdict, Severity: &severityJSON{
		Max: s.Severity.Max, MustFix: s.Severity.MustFix, ShouldFix: s.Severity.ShouldFix,
		WorthFixing: s.Severity.WorthFixing, Optional: s.Severity.Optional,
	}}
}

func reportComment(c *collect.Comment) reviewCommentJSON {
	out := reviewCommentJSON{
		ID: c.ID, Profile: c.Profile, Priority: c.Priority, Category: c.Category, Body: c.Body, Code: optional(c.Code),
		Anchors: make([]reviewAnchorJSON, 0, len(c.Anchors)), Suggestions: make([]reviewSuggestionJSON, 0, len(c.Suggestions)),
	}
	for _, an := range c.Anchors {
		out.Anchors = append(out.Anchors, reviewAnchorJSON{File: an.File, Line: an.Line, EndLine: an.EndLine})
	}
	for _, s := range c.Suggestions {
		out.Suggestions = append(out.Suggestions, reviewSuggestionJSON{
			Summary: s.Summary, Effort: s.Effort, Scope: s.Scope,
			Pros: nonNil(s.Pros), Cons: nonNil(s.Cons), Code: optional(s.Code),
		})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
