package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
)

type reviewListOptions struct {
	repo string
	pr   int
}

func (a *App) newReviewListCmd() *cobra.Command {
	var opts reviewListOptions
	cmd := &cobra.Command{
		Use:   "list [--repo <REPO>] [--pr <N>]",
		Short: "List reviews, including those whose agent was removed",
		Args:  noArgs,
		RunE: runE(a.withConfigUnswept(func(cmd *cobra.Command, _ []string) error {
			return a.runReviewList(cmd.Context(), &opts)
		})),
	}
	cmd.Flags().StringVar(&opts.repo, "repo", "", "only reviews of this repo id, host/owner/name")
	cmd.Flags().IntVar(&opts.pr, "pr", 0, "only reviews of this pull request")
	return cmd
}

// reviewListJSON is ws review list's output (ADR 0045).
type reviewListJSON struct {
	Reviews  []reviewRunJSON `json:"reviews"`
	Warnings []string        `json:"warnings"`
}

type reviewRunJSON struct {
	ID     string           `json:"id"`
	Review record.Record    `json:"review"`
	Agent  *reviewAgentJSON `json:"agent"`
}

// runReviewList lists every review run kept in the store, and any review
// workspace from before runs were kept, newest first, each with its
// agent while its workspace exists (ADR 0045).
func (a *App) runReviewList(ctx context.Context, opts *reviewListOptions) error {
	if opts.repo != "" {
		if _, err := repo.ParseID(opts.repo); err != nil {
			return newError(codeUsage, fmt.Errorf("--repo needs a repo id, host/owner/name: %w", err))
		}
	}
	listing, err := a.lifetime.List(ctx)
	if err != nil {
		return err
	}
	runs, err := store.New(a.paths.Reviews).Runs(nil)
	if err != nil {
		return err
	}
	runs = append(runs, a.unkeptRuns(runs, &listing)...)
	slices.SortStableFunc(runs, func(x, y store.Run) int { return y.Record.StartedAt.Compare(x.Record.StartedAt) })
	out := []reviewRunJSON{}
	for i := range runs {
		r := &runs[i]
		if !opts.matches(&r.Record) {
			continue
		}
		out = append(out, reviewRunJSON{ID: r.ID, Review: r.Record, Agent: agentOf(r.ID, &listing)})
	}
	if a.global.json {
		return a.writeJSON(reviewListJSON{Reviews: out, Warnings: a.warnings()})
	}
	a.printReviewList(out)
	a.printWarnings()
	return nil
}

// unkeptRuns are the review workspaces in listing whose run isn't in runs:
// those started before ws kept runs in the store.
func (a *App) unkeptRuns(runs []store.Run, listing *lifetime.Listing) []store.Run {
	var out []store.Run
	for i := range listing.Workspaces {
		id := listing.Workspaces[i].Workspace.ID
		if slices.ContainsFunc(runs, func(r store.Run) bool { return r.ID == id }) {
			continue
		}
		rec, err := record.Read(record.Path(a.paths.State, id))
		if errors.Is(err, record.ErrNotFound) {
			continue
		}
		if err != nil {
			a.warned = append(a.warned, fmt.Sprintf("couldn't read workspace %s's review record: %v", id, err))
			continue
		}
		out = append(out, store.Run{ID: id, Record: rec})
	}
	return out
}

func (o *reviewListOptions) matches(rec *record.Record) bool {
	if o.repo != "" && rec.Repo != o.repo {
		return false
	}
	return o.pr == 0 || (rec.PR != nil && rec.PR.Number == o.pr)
}

// printReviewList prints a table of the runs.
func (a *App) printReviewList(runs []reviewRunJSON) {
	tw := tabwriter.NewWriter(a.stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "ID\tREPO\tPR\tREF\tSTARTED\tAGENT")
	now := a.now()
	for i := range runs {
		r := &runs[i]
		pr := "-"
		if r.Review.PR != nil {
			pr = "#" + strconv.Itoa(r.Review.PR.Number)
		}
		agent := "removed"
		if r.Agent != nil {
			agent = string(r.Agent.Status)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Review.Repo, pr, shortSHA(r.Review.Ref), ago(now.Sub(r.Review.StartedAt)), agent)
	}
	_ = tw.Flush()
}

func shortSHA(ref string) string {
	if len(ref) > 7 {
		return ref[:7]
	}
	return ref
}
