package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
	"github.com/bobcob7/workspaces/internal/review/store"
)

// decideOptions are ws review decide's choice: one of comment, fix, skip,
// and clear.
type decideOptions struct {
	comment, fix, skip, clear bool
	suggestion                int
	hasSuggestion             bool
	note                      string
	hasNote                   bool
}

// actedOptions are ws review acted's: one of commented and fixSent.
type actedOptions struct {
	commented string
	fixSent   string
}

func (a *App) newReviewDecideCmd() *cobra.Command {
	var subject reviewSubjectOptions
	var opts decideOptions
	cmd := &cobra.Command{
		Use:   "decide (<ID> | --repo <REPO> --ref <SHA>) <FINDING>... (--comment | --fix [--suggestion <N>] | --skip | --clear) [--note <TEXT>]",
		Short: "Decide what to do with a review's findings",
		Args:  reviewFindingArgs(&subject),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			opts.hasSuggestion = cmd.Flags().Changed("suggestion")
			opts.hasNote = cmd.Flags().Changed("note")
			if err := opts.check(); err != nil {
				return err
			}
			return a.runReviewDecide(cmd.Context(), args, &subject, &opts)
		})),
	}
	addReviewSubjectFlags(cmd, &subject)
	cmd.Flags().BoolVar(&opts.comment, "comment", false, "post the findings as review comments")
	cmd.Flags().BoolVar(&opts.fix, "fix", false, "have the findings fixed")
	cmd.Flags().IntVar(&opts.suggestion, "suggestion", 0, "with --fix, the suggestion to apply, counting from 0")
	cmd.Flags().BoolVar(&opts.skip, "skip", false, "leave the findings as they are")
	cmd.Flags().BoolVar(&opts.clear, "clear", false, "remove the findings' decisions")
	cmd.Flags().StringVar(&opts.note, "note", "", "a note kept with the decisions")
	return cmd
}

func (a *App) newReviewActedCmd() *cobra.Command {
	var subject reviewSubjectOptions
	var opts actedOptions
	cmd := &cobra.Command{
		Use:   "acted (<ID> | --repo <REPO> --ref <SHA>) <FINDING>... (--commented <URL> | --fix-sent <AGENT>)",
		Short: "Record that decided findings were acted on",
		Args:  reviewFindingArgs(&subject),
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, args []string) error {
			if err := opts.check(); err != nil {
				return err
			}
			return a.runReviewActed(cmd.Context(), args, &subject, &opts)
		})),
	}
	addReviewSubjectFlags(cmd, &subject)
	cmd.Flags().StringVar(&opts.commented, "commented", "", "the findings were posted in the review at this URL")
	cmd.Flags().StringVar(&opts.fixSent, "fix-sent", "", "the findings were sent to this agent to fix")
	return cmd
}

// reviewFindingArgs accepts a workspace id followed by findings, or
// findings alone with --repo and --ref.
func reviewFindingArgs(subject *reviewSubjectOptions) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if subject.repo != "" || subject.ref != "" {
			if len(args) == 0 {
				return usageErrorf("%s needs at least one finding", commandName(cmd))
			}
			return reviewSubjectArgs(subject)(cmd, nil)
		}
		if len(args) < 2 {
			return usageErrorf("%s needs a review workspace, or --repo and --ref, then at least one finding", commandName(cmd))
		}
		return nil
	}
}

func (o *decideOptions) check() error {
	chosen := 0
	for _, set := range []bool{o.comment, o.fix, o.skip, o.clear} {
		if set {
			chosen++
		}
	}
	switch {
	case chosen != 1:
		return usageErrorf("choose exactly one of --comment, --fix, --skip, and --clear")
	case o.hasSuggestion && !o.fix:
		return usageErrorf("--suggestion goes with --fix")
	case o.hasSuggestion && o.suggestion < 0:
		return usageErrorf("--suggestion counts from 0, got %d", o.suggestion)
	case o.hasNote && o.clear:
		return usageErrorf("--clear removes the note too; leave off --note")
	}
	return nil
}

// decision is the chosen decision's name, or "" for --clear.
func (o *decideOptions) decision() string {
	switch {
	case o.comment:
		return store.DecisionComment
	case o.fix:
		return store.DecisionFix
	case o.skip:
		return store.DecisionSkip
	}
	return ""
}

func (o *actedOptions) check() error {
	if (o.commented == "") == (o.fixSent == "") {
		return usageErrorf("give exactly one of --commented and --fix-sent")
	}
	return nil
}

// reviewDecisionsJSON is ws review decide's and acted's output: the
// findings they named, each with its decision, or null once cleared.
type reviewDecisionsJSON struct {
	Repo      string                     `json:"repo"`
	Ref       string                     `json:"ref"`
	Decisions map[string]*store.Decision `json:"decisions"`
	Warnings  []string                   `json:"warnings"`
}

// findingSubject is the repo and commit the arguments name, and the
// findings that follow them.
func (a *App) findingSubject(args []string, opts *reviewSubjectOptions) (repo.ID, string, []string, error) {
	if len(args) > 0 && opts.repo == "" {
		_, rec, err := a.findReviewByID(args[0])
		if err != nil {
			return repo.ID{}, "", nil, err
		}
		id, err := repo.ParseID(rec.Repo)
		if err != nil {
			return repo.ID{}, "", nil, fmt.Errorf("reading the review record: %w", err)
		}
		return id, rec.Ref, args[1:], nil
	}
	id, err := repo.ParseID(opts.repo)
	if err != nil {
		return repo.ID{}, "", nil, newError(codeUsage, fmt.Errorf("--repo needs a repo id, host/owner/name: %w", err))
	}
	return id, opts.ref, args, nil
}

// runReviewDecide sets or clears the findings' decisions (ADR 0040).
// Setting one replaces any earlier decision, done included.
func (a *App) runReviewDecide(ctx context.Context, args []string, subject *reviewSubjectOptions, opts *decideOptions) error {
	id, ref, findings, err := a.findingSubject(args, subject)
	if err != nil {
		return err
	}
	out := map[string]*store.Decision{}
	err = store.New(a.paths.Reviews).Decide(ctx, id, ref, func(result *collect.Result, decisions store.Decisions) error {
		comments, findErr := currentFindings(result, findings)
		if findErr != nil {
			return findErr
		}
		for _, f := range findings {
			if opts.clear {
				delete(decisions, f)
				out[f] = nil
				continue
			}
			d := store.Decision{Decision: opts.decision(), Note: opts.note, At: a.now().UTC()}
			if opts.hasSuggestion {
				if n := len(comments[f].Suggestions); opts.suggestion >= n {
					return usageErrorf("%s has %s; --suggestion counts from 0", f, plural(n, "suggestion"))
				}
				d.Suggestion = &opts.suggestion
			}
			decisions[f] = d
			out[f] = &d
		}
		return nil
	})
	if err != nil {
		return err
	}
	return a.printDecisions(id, ref, findings, out)
}

// runReviewActed fills in how decided findings were acted on (ADR 0041):
// --commented for those decided comment, --fix-sent for those decided
// fix.
func (a *App) runReviewActed(ctx context.Context, args []string, subject *reviewSubjectOptions, opts *actedOptions) error {
	id, ref, findings, err := a.findingSubject(args, subject)
	if err != nil {
		return err
	}
	want, flag := store.DecisionComment, "--commented"
	if opts.fixSent != "" {
		want, flag = store.DecisionFix, "--fix-sent"
	}
	out := map[string]*store.Decision{}
	err = store.New(a.paths.Reviews).Decide(ctx, id, ref, func(result *collect.Result, decisions store.Decisions) error {
		if _, findErr := currentFindings(result, findings); findErr != nil {
			return findErr
		}
		for _, f := range findings {
			d, ok := decisions[f]
			switch {
			case !ok:
				return usageErrorf("%s has no decision; %s needs it decided %s", f, flag, want)
			case d.Decision != want:
				return usageErrorf("%s is decided %s; %s needs it decided %s", f, d.Decision, flag, want)
			}
			d.Done = &store.Done{Commented: opts.commented, FixSent: opts.fixSent, At: a.now().UTC()}
			decisions[f] = d
			out[f] = &d
		}
		return nil
	})
	if err != nil {
		return err
	}
	return a.printDecisions(id, ref, findings, out)
}

// currentFindings looks up each finding among result's current comments.
// A superseded finding (#<n>:<id>) or an unknown one is finding_not_found,
// listing every such finding in details.
func currentFindings(result *collect.Result, findings []string) (map[string]*collect.Comment, error) {
	current := map[string]*collect.Comment{}
	for i := range result.Comments {
		c := &result.Comments[i]
		if !strings.HasPrefix(c.ID, "#") {
			current[c.ID] = c
		}
	}
	var missing, superseded []string
	for _, f := range findings {
		switch {
		case strings.HasPrefix(f, "#"):
			superseded = append(superseded, f)
		case current[f] == nil:
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 && len(superseded) == 0 {
		return current, nil
	}
	var reasons []string
	if len(missing) > 0 {
		reasons = append(reasons, "no current finding "+strings.Join(missing, ", "))
	}
	if len(superseded) > 0 {
		reasons = append(reasons, strings.Join(superseded, ", ")+" superseded, and only <profile>:<id> findings can be decided")
	}
	ce := newError(codeFindingNotFound, fmt.Errorf("%s; run ws review report for the review's findings", strings.Join(reasons, "; ")))
	ce.details = map[string]any{"findings": slices.Concat(missing, superseded)}
	return nil, ce
}

// printDecisions prints the findings' decisions in the order they were
// named.
func (a *App) printDecisions(id repo.ID, ref string, findings []string, decisions map[string]*store.Decision) error {
	if a.global.json {
		return a.writeJSON(reviewDecisionsJSON{Repo: id.String(), Ref: ref, Decisions: decisions, Warnings: a.warnings()})
	}
	for _, f := range findings {
		fmt.Fprintf(a.stdout, "%s: %s\n", f, describeDecision(decisions[f]))
	}
	a.printWarnings()
	return nil
}

// describeDecision is a decision in a few words.
func describeDecision(d *store.Decision) string {
	if d == nil {
		return "undecided"
	}
	s := d.Decision
	if d.Suggestion != nil {
		s += fmt.Sprintf(" with suggestion %d", *d.Suggestion)
	}
	if d.Done != nil {
		if d.Done.Commented != "" {
			s += ", commented at " + d.Done.Commented
		} else {
			s += ", sent to " + d.Done.FixSent
		}
	}
	return s
}
