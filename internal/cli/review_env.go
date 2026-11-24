package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
	"github.com/bobcob7/workspaces/internal/review/render"
)

// reviewEnv returns the environment --env names ("" for local), refusing it
// with usage when the command runs inside a workspace and --requester isn't
// none (R5): "an agent never chooses an environment; run ws review <cmd>
// without --env". A review an agent runs without --env is local to the
// machine the agent is on, which is the remote when the agent runs there.
func (a *App) reviewEnv(flag, requesterFlag string) (string, error) {
	if flag == "" {
		return "", nil
	}
	if requesterFlag != requesterNone {
		enclosing, err := a.enclosingOr("")
		if err != nil {
			return "", err
		}
		if enclosing != "" {
			return "", usageErrorf("an agent never chooses an environment; run ws review <cmd> without --env")
		}
	}
	name, _, err := a.remoteTarget(flag)
	return name, err
}

// callRemoteObject calls op on env and returns its result, a JSON object.
func (a *App) callRemoteObject(ctx context.Context, env, op string, args any) (map[string]json.RawMessage, error) {
	raw, err := a.callEnvironment(ctx, env, op, args)
	if err != nil {
		return nil, err
	}
	return remoteObject(raw, env)
}

// printRemote prints an agent's result: with --json the object with
// "environment" and ws's warnings added, else what text prints, followed by
// the agent's warnings and ws's on stderr.
func (a *App) printRemote(object map[string]json.RawMessage, env string, text func() error) error {
	if a.global.json {
		name, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("encoding the environment name: %w", err)
		}
		object["environment"] = name
		if err := addWarnings(object, a.warned); err != nil {
			return err
		}
		return a.writeJSON(object)
	}
	if err := text(); err != nil {
		return err
	}
	var notes remoteNotes
	if raw, err := json.Marshal(object); err == nil {
		_ = json.Unmarshal(raw, &notes)
	}
	for _, w := range slices.Concat(a.warned, notes.Warnings) {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	return nil
}

// decodeRemote reads object into v.
func decodeRemote(object map[string]json.RawMessage, env string, v any) error {
	raw, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("encoding the result: %w", err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return newError(codeInternal, fmt.Errorf("reading the result of %s: %w", env, err))
	}
	return nil
}

// noRemoteRepoRef is usage for --repo and --ref with --env.
func noRemoteRepoRef(subject *reviewSubjectOptions) error {
	if subject.repo != "" || subject.ref != "" {
		return usageErrorf("--repo and --ref name a review on this machine; use the review id on an environment")
	}
	return nil
}

// remoteReviewClaudeArgs reads the arguments after "--" of a remote review:
// only --model and --permission-mode, each with its value.
func remoteReviewClaudeArgs(args []string) (model, mode *string, err error) {
	refused := usageErrorf("only --model and --permission-mode are passed to a remote review")
	for i := 0; i < len(args); i++ {
		flag, value, joined := strings.Cut(args[i], "=")
		if !joined {
			if i+1 >= len(args) {
				return nil, nil, refused
			}
			i++
			value = args[i]
		}
		if value == "" {
			return nil, nil, refused
		}
		switch flag {
		case "--model":
			model = &value
		case "--permission-mode":
			mode = &value
		default:
			return nil, nil, refused
		}
	}
	return model, mode, nil
}

// remoteReviewStartArgs is review.start's arguments for opts.
func remoteReviewStartArgs(opts *reviewStartOptions) (*envproto.ReviewStartArgs, error) {
	if opts.offline {
		return nil, usageErrorf("--offline can't be used with --env; the environment decides whether to fetch")
	}
	if opts.requester != "" && opts.requester != requesterNone {
		return nil, usageErrorf("--requester names a workspace on this machine; use --requester none with --env")
	}
	sp, err := repo.ParseSpec(opts.spec)
	if err != nil {
		return nil, newError(codeSpecInvalid, err)
	}
	model, mode, err := remoteReviewClaudeArgs(opts.claudeArgs)
	if err != nil {
		return nil, err
	}
	args := &envproto.ReviewStartArgs{
		Repo: strings.TrimSuffix(opts.spec, "@"+sp.Ref), Name: optional(opts.name), Model: model, PermissionMode: mode,
		Focus: optional(opts.focus), Profiles: slices.Clone(opts.profiles), Auto: slices.Clone(opts.auto),
	}
	if sp.Ref == "" {
		args.Repo = opts.spec
	}
	if args.Profiles == nil {
		args.Profiles = []string{}
	}
	if args.Auto == nil {
		args.Auto = []string{}
	}
	switch {
	case opts.pr < 0:
		return nil, usageErrorf("--pr needs a pull request number, got %d", opts.pr)
	case opts.pr != 0 && sp.Ref != "":
		return nil, usageErrorf("--pr reviews the pull request's head; leave @%s off the spec", sp.Ref)
	case opts.pr != 0:
		args.PR = &opts.pr
	case !fullCommitPattern.MatchString(sp.Ref):
		return nil, usageErrorf("a review on an environment needs --pr, or a spec ending in @ and the commit's full 40-character SHA")
	default:
		args.Head = &sp.Ref
	}
	return args, nil
}

// runRemoteReviewStart sends review.start and prints its result plus
// "environment" (PR 3's D9), with wait_command "ws review wait --json --env <name> <id>".
func (a *App) runRemoteReviewStart(ctx context.Context, env string, opts *reviewStartOptions) error {
	args, err := remoteReviewStartArgs(opts)
	if err != nil {
		return err
	}
	object, err := a.callRemoteObject(ctx, env, envproto.OpReviewStart, args)
	if err != nil {
		return err
	}
	var started struct {
		ID string `json:"id"`
	}
	if err = decodeRemote(object, env, &started); err != nil {
		return err
	}
	wait := "ws review wait --json --env " + env + " " + started.ID
	waitJSON, err := json.Marshal(wait)
	if err != nil {
		return fmt.Errorf("encoding the wait command: %w", err)
	}
	object["wait_command"] = waitJSON
	return a.printRemote(object, env, func() error {
		if err := a.printRemoteClaude(object, env); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "wait for the report: ws review wait --env %s %s\n", env, started.ID)
		return nil
	})
}

// remoteReportJSON is a remote review's whole report: ws review report's
// object, plus the environment and the count of values the agent redacted.
type remoteReportJSON struct {
	reviewReportJSON

	Environment string `json:"environment"`
	Redacted    int    `json:"redacted"`
}

// remoteReportPage is one review.report result: reviewReportJSON, with the
// section asked for filled and the paging keys.
type remoteReportPage struct {
	reviewReportJSON

	Section    string `json:"section"`
	Offset     int    `json:"offset"`
	NextOffset *int   `json:"next_offset"`
	Redacted   int    `json:"redacted"`
}

// remoteReport fetches every page of both sections and returns the whole
// reviewReportJSON (comments, then superseded, each in order).
func (a *App) remoteReport(ctx context.Context, env, id string) (remoteReportJSON, error) {
	commentPages, err := a.remoteSection(ctx, env, id, envproto.SectionComments)
	if err != nil {
		return remoteReportJSON{}, err
	}
	supersededPages, err := a.remoteSection(ctx, env, id, envproto.SectionSuperseded)
	if err != nil {
		return remoteReportJSON{}, err
	}
	out := remoteReportJSON{reviewReportJSON: commentPages[0].reviewReportJSON, Environment: env}
	out.Comments, out.Superseded = []reviewCommentJSON{}, []reviewCommentJSON{}
	for i := range commentPages {
		out.Comments = append(out.Comments, commentPages[i].Comments...)
		out.Redacted += commentPages[i].Redacted
	}
	for i := range supersededPages {
		out.Superseded = append(out.Superseded, supersededPages[i].Superseded...)
		out.Redacted += supersededPages[i].Redacted
	}
	return out, nil
}

// maxRemoteReportPages is the most pages of one section ws reads, so a
// misbehaving agent can't keep the loop going until the timeout.
const maxRemoteReportPages = 1000

// remoteSection fetches the pages of one section, from offset 0 until
// next_offset is null. An offset that doesn't advance is an error, so a
// misbehaving agent can't make the loop run forever.
func (a *App) remoteSection(ctx context.Context, env, id, section string) ([]remoteReportPage, error) {
	var pages []remoteReportPage
	offset := 0
	for len(pages) < maxRemoteReportPages {
		object, err := a.callRemoteObject(ctx, env, envproto.OpReviewReport, envproto.ReviewReportArgs{Review: id, Section: section, Offset: offset})
		if err != nil {
			return nil, err
		}
		var page remoteReportPage
		if err = decodeRemote(object, env, &page); err != nil {
			return nil, err
		}
		pages = append(pages, page)
		if page.NextOffset == nil {
			return pages, nil
		}
		if *page.NextOffset <= offset {
			return nil, newError(codeInternal, fmt.Errorf("environment %s's report page didn't advance past offset %d", env, offset))
		}
		offset = *page.NextOffset
	}
	return nil, newError(codeInternal, fmt.Errorf("environment %s's report has more than %d pages of %s", env, maxRemoteReportPages, section))
}

// printRemoteReport prints report in format as a local report is printed;
// with decided set, only the findings decided that way are listed.
func (a *App) printRemoteReport(report *remoteReportJSON, env, format, decided string) error {
	if decided != "" {
		report.Comments = slices.DeleteFunc(slices.Clone(report.Comments), func(c reviewCommentJSON) bool {
			return c.Decision == nil || c.Decision.Decision != decided
		})
		report.Superseded = []reviewCommentJSON{}
	}
	report.Environment = env
	report.Warnings = slices.Concat(nonNil(report.Warnings), a.warned)
	if format == formatJSON {
		return a.writeJSON(report)
	}
	result := collectResultOf(&report.reviewReportJSON)
	id, err := repo.ParseID(report.Repo)
	if err != nil {
		return newError(codeInternal, fmt.Errorf("reading the report of %s: %w", env, err))
	}
	envelope := render.CollectReviewsEnvelope{
		Ref: report.Ref, RepoName: id.String(), RepoKnown: true, StoreEnabled: true,
		HeadCheck: render.CollectReviewsHeadCheck{Source: "none"}, Result: result,
	}
	if format == formatHTML {
		err = render.NewHTML().CollectReviews(a.stdout, envelope)
	} else {
		err = a.writeMarkdownReport(&envelope, &report.reviewReportJSON)
	}
	if err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}
	for _, w := range report.Warnings {
		fmt.Fprintf(a.stderr, "ws: warning: %s\n", w)
	}
	return nil
}

// collectResultOf is the store-side form of a report, which the renderers
// read: one submission per profile that submitted, and every finding. A
// report carries no summaries, so they are empty.
func collectResultOf(report *reviewReportJSON) *collect.Result {
	result := &collect.Result{}
	for _, p := range report.Profiles {
		if p.Missing {
			continue
		}
		s := collect.Submission{Ordinal: len(result.Submissions) + 1, Profile: p.Name, Verdict: deref(p.Verdict)}
		if p.Severity != nil {
			s.Severity = collect.Severity{Max: p.Severity.Max, MustFix: p.Severity.MustFix, ShouldFix: p.Severity.ShouldFix, WorthFixing: p.Severity.WorthFixing, Optional: p.Severity.Optional}
		}
		result.Submissions = append(result.Submissions, s)
	}
	for _, list := range [][]reviewCommentJSON{report.Comments, report.Superseded} {
		for i := range list {
			result.Comments = append(result.Comments, collectCommentOf(&list[i]))
		}
	}
	return result
}

func collectCommentOf(c *reviewCommentJSON) collect.Comment {
	out := collect.Comment{ID: c.ID, Profile: c.Profile, Priority: c.Priority, Category: c.Category, Body: c.Body, Code: deref(c.Code)}
	for _, an := range c.Anchors {
		out.Anchors = append(out.Anchors, collect.Anchor{File: an.File, Line: an.Line, EndLine: an.EndLine})
	}
	for _, s := range c.Suggestions {
		out.Suggestions = append(out.Suggestions, collect.Suggestion{Summary: s.Summary, Effort: s.Effort, Scope: s.Scope, Pros: s.Pros, Cons: s.Cons, Code: deref(s.Code)})
	}
	return out
}

// runRemoteReviewReport prints the report of review id on env.
func (a *App) runRemoteReviewReport(ctx context.Context, env string, args []string, subject *reviewSubjectOptions, format, decided string) error {
	if err := noRemoteRepoRef(subject); err != nil {
		return err
	}
	format, err := a.reportFormat(format)
	if err != nil {
		return err
	}
	report, err := a.remoteReport(ctx, env, args[0])
	if err != nil {
		return err
	}
	return a.printRemoteReport(&report, env, format, decided)
}

// waitRemoteReview polls review.status every defaultReviewPoll until it is
// finished or the timeout passes (review_timeout as locally), then prints remoteReport.
func (a *App) waitRemoteReview(ctx context.Context, env, id string, timeout time.Duration) error {
	if timeout <= 0 {
		return usageErrorf("--timeout must be positive, got %s", timeout)
	}
	format, err := a.reportFormat("")
	if err != nil {
		return err
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		var status struct {
			Agent    *reviewAgentJSON `json:"agent"`
			Finished bool             `json:"finished"`
		}
		object, err := a.callRemoteObject(ctx, env, envproto.OpReviewStatus, envproto.ReviewRef{Review: id})
		if err != nil {
			return err
		}
		if err := decodeRemote(object, env, &status); err != nil {
			return err
		}
		if status.Finished {
			report, err := a.remoteReport(ctx, env, id)
			if err != nil {
				return err
			}
			return a.printRemoteReport(&report, env, format, "")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for review %s: %w", id, ctx.Err())
		case <-deadline.C:
			state := lifetime.StateStarting
			if status.Agent != nil {
				state = status.Agent.Status
			}
			ce := newError(codeReviewTimeout, fmt.Errorf("review %s is still %s after %s; run ws review wait again to keep waiting", id, state, timeout))
			ce.details = map[string]any{"workspace": id, "status": state, "environment": env}
			return ce
		case <-time.After(a.reviewPoll):
		}
	}
}

// runRemoteReviewList lists the reviews on env.
func (a *App) runRemoteReviewList(ctx context.Context, env string, opts *reviewListOptions) error {
	args := envproto.ReviewListArgs{Repo: optional(opts.repo)}
	if opts.repo != "" {
		if _, err := repo.ParseID(opts.repo); err != nil {
			return newError(codeUsage, fmt.Errorf("--repo needs a repo id, host/owner/name: %w", err))
		}
	}
	if opts.pr != 0 {
		args.PR = &opts.pr
	}
	object, err := a.callRemoteObject(ctx, env, envproto.OpReviewList, args)
	if err != nil {
		return err
	}
	return a.printRemote(object, env, func() error {
		var listed reviewListJSON
		if err := decodeRemote(object, env, &listed); err != nil {
			return err
		}
		a.printReviewList(listed.Reviews)
		return nil
	})
}

// printRemoteDecisions prints the decisions of a decide or acted result in
// the order the findings were named.
func (a *App) printRemoteDecisions(object map[string]json.RawMessage, env string, findings []string) error {
	return a.printRemote(object, env, func() error {
		var decided reviewDecisionsJSON
		if err := decodeRemote(object, env, &decided); err != nil {
			return err
		}
		for _, f := range findings {
			fmt.Fprintf(a.stdout, "%s: %s\n", f, describeDecision(decided.Decisions[f]))
		}
		return nil
	})
}

// reviewAndFindings splits args, the review id and its findings.
func reviewAndFindings(subject *reviewSubjectOptions, args []string) (string, []string, error) {
	if err := noRemoteRepoRef(subject); err != nil {
		return "", nil, err
	}
	return args[0], args[1:], nil
}

// runRemoteReviewDecide sends review.decide.
func (a *App) runRemoteReviewDecide(ctx context.Context, env string, args []string, subject *reviewSubjectOptions, opts *decideOptions) error {
	id, findings, err := reviewAndFindings(subject, args)
	if err != nil {
		return err
	}
	decision := opts.decision()
	if opts.clear {
		decision = envproto.DecideClear
	}
	call := envproto.ReviewDecideArgs{Review: id, Findings: findings, Decision: decision}
	if opts.hasSuggestion {
		call.Suggestion = &opts.suggestion
	}
	if opts.hasNote {
		call.Note = &opts.note
	}
	object, err := a.callRemoteObject(ctx, env, envproto.OpReviewDecide, call)
	if err != nil {
		return err
	}
	return a.printRemoteDecisions(object, env, findings)
}

// runRemoteReviewActed sends review.acted.
func (a *App) runRemoteReviewActed(ctx context.Context, env string, args []string, subject *reviewSubjectOptions, opts *actedOptions) error {
	id, findings, err := reviewAndFindings(subject, args)
	if err != nil {
		return err
	}
	call := envproto.ReviewActedArgs{Review: id, Findings: findings, Commented: optional(opts.commented), FixSent: optional(opts.fixSent)}
	object, err := a.callRemoteObject(ctx, env, envproto.OpReviewActed, call)
	if err != nil {
		return err
	}
	return a.printRemoteDecisions(object, env, findings)
}

// runRemoteReviewProfiles lists the profiles on env, read only.
func (a *App) runRemoteReviewProfiles(ctx context.Context, env string) error {
	object, err := a.callRemoteObject(ctx, env, envproto.OpReviewProfiles, envproto.ReviewProfilesArgs{})
	if err != nil {
		return err
	}
	return a.printRemote(object, env, func() error {
		var listed reviewProfilesJSON
		if err := decodeRemote(object, env, &listed); err != nil {
			return err
		}
		printProfilesTable(a.stdout, listed.Profiles)
		return nil
	})
}
