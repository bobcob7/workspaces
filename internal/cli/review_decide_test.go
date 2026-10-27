package cli

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/collect"
	"github.com/bobcob7/workspaces/internal/review/store"
)

// decisions decodes ws review decide's or acted's JSON on stdout, then
// clears stdout.
func (e *reportApp) decisions(t *testing.T) reviewDecisionsJSON {
	t.Helper()
	var out reviewDecisionsJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
	e.stdout.Reset()
	return out
}

// stored reads the decisions stored at testCommit.
func (e *reportApp) stored(t *testing.T) store.Decisions {
	t.Helper()
	id, err := repo.ParseID(reviewedRepo)
	require.NoError(t, err)
	_, decisions, err := store.New(e.app.paths.Reviews).Report(t.Context(), id, testCommit)
	require.NoError(t, err)
	return decisions
}

func (e *reportApp) decide(t *testing.T, opts decideOptions, args ...string) error {
	t.Helper()
	return e.app.runReviewDecide(t.Context(), append([]string{reportWorkspace}, args...), &reviewSubjectOptions{}, &opts)
}

func (e *reportApp) acted(t *testing.T, opts actedOptions, args ...string) error {
	t.Helper()
	return e.app.runReviewActed(t.Context(), append([]string{reportWorkspace}, args...), &reviewSubjectOptions{}, &opts)
}

// newDecideApp is a report app with a security and a go submission, each
// with finding dropped-context-1.
func newDecideApp(t *testing.T) *reportApp {
	t.Helper()
	e := newReportApp(t)
	e.submit(t, "security", reviewDoc("security"))
	e.submit(t, "go", reviewDoc("go"))
	e.app.global.json = true
	return e
}

func TestApp_newReviewDecideCmd(t *testing.T) {
	app, _, _ := newTestApp()
	args := func(t *testing.T, repoFlag, ref string, positional ...string) error {
		t.Helper()
		cmd := app.newReviewDecideCmd()
		require.NoError(t, cmd.Flags().Set("repo", repoFlag))
		require.NoError(t, cmd.Flags().Set("ref", ref))
		return cmd.Args(cmd, positional)
	}
	t.Run("takes a workspace and findings", func(t *testing.T) {
		require.NoError(t, args(t, "", "", "x", "security:a", "go:b"))
	})
	t.Run("takes --repo and --ref and findings", func(t *testing.T) {
		require.NoError(t, args(t, reviewedRepo, testCommit, "security:a"))
	})
	t.Run("refuses no finding, or a short ref", func(t *testing.T) {
		requireUsageError(t, args(t, "", "", "x"))
		requireUsageError(t, args(t, "", ""))
		requireUsageError(t, args(t, reviewedRepo, testCommit))
		requireUsageError(t, args(t, reviewedRepo, "3f2a9c1", "security:a"))
		requireUsageError(t, args(t, reviewedRepo, "", "security:a"))
	})
}

func TestApp_newReviewActedCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newReviewActedCmd()
	require.NoError(t, cmd.Args(cmd, []string{"x", "security:a"}))
	requireUsageError(t, cmd.Args(cmd, []string{"x"}))
}

func TestReviewFindingArgs(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newReviewDecideCmd()
	subject := &reviewSubjectOptions{repo: reviewedRepo, ref: testCommit}
	require.NoError(t, reviewFindingArgs(subject)(cmd, []string{"security:a"}))
	requireUsageError(t, reviewFindingArgs(subject)(cmd, nil))
	requireUsageError(t, reviewFindingArgs(&reviewSubjectOptions{})(cmd, []string{"x"}))
}

func TestDecideOptions_check(t *testing.T) {
	t.Run("takes exactly one decision", func(t *testing.T) {
		for _, opts := range []decideOptions{{comment: true}, {fix: true}, {skip: true}, {clear: true}} {
			require.NoError(t, opts.check())
		}
		requireUsageError(t, (&decideOptions{}).check())
		requireUsageError(t, (&decideOptions{fix: true, skip: true}).check())
	})
	t.Run("--suggestion only with --fix, from 0", func(t *testing.T) {
		require.NoError(t, (&decideOptions{fix: true, hasSuggestion: true, suggestion: 1}).check())
		requireUsageError(t, (&decideOptions{comment: true, hasSuggestion: true}).check())
		requireUsageError(t, (&decideOptions{fix: true, hasSuggestion: true, suggestion: -1}).check())
	})
	t.Run("--note not with --clear", func(t *testing.T) {
		require.NoError(t, (&decideOptions{skip: true, hasNote: true, note: "n"}).check())
		requireUsageError(t, (&decideOptions{clear: true, hasNote: true}).check())
	})
}

func TestDecideOptions_decision(t *testing.T) {
	assert.Equal(t, store.DecisionComment, (&decideOptions{comment: true}).decision())
	assert.Equal(t, store.DecisionFix, (&decideOptions{fix: true}).decision())
	assert.Equal(t, store.DecisionSkip, (&decideOptions{skip: true}).decision())
	assert.Empty(t, (&decideOptions{clear: true}).decision())
}

func TestActedOptions_check(t *testing.T) {
	require.NoError(t, (&actedOptions{commented: "https://x"}).check())
	require.NoError(t, (&actedOptions{fixSent: "fix-1"}).check())
	requireUsageError(t, (&actedOptions{}).check())
	requireUsageError(t, (&actedOptions{commented: "https://x", fixSent: "fix-1"}).check())
}

func TestApp_runReviewDecide(t *testing.T) {
	t.Run("stores the decision on each finding named", func(t *testing.T) {
		e := newDecideApp(t)
		opts := decideOptions{fix: true, hasSuggestion: true, note: "the cheap one", hasNote: true}
		require.NoError(t, e.decide(t, opts, "security:dropped-context-1", "go:dropped-context-1"))
		out := e.decisions(t)
		assert.Equal(t, reviewedRepo, out.Repo)
		assert.Equal(t, testCommit, out.Ref)
		assert.Equal(t, []string{}, out.Warnings)
		want := store.Decision{Decision: store.DecisionFix, Suggestion: new(0), Note: "the cheap one", At: testNow().UTC()}
		assert.Equal(t, map[string]*store.Decision{"security:dropped-context-1": &want, "go:dropped-context-1": &want}, out.Decisions)
		assert.Equal(t, store.Decisions{"security:dropped-context-1": want, "go:dropped-context-1": want}, e.stored(t))
	})
	t.Run("a new decision replaces the old one, done included", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{comment: true}, "security:dropped-context-1"))
		require.NoError(t, e.acted(t, actedOptions{commented: "https://x"}, "security:dropped-context-1"))
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-1"))
		got := e.stored(t)["security:dropped-context-1"]
		assert.Equal(t, store.DecisionSkip, got.Decision)
		assert.Nil(t, got.Done)
	})
	t.Run("--clear removes the decision", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-1", "go:dropped-context-1"))
		e.stdout.Reset()
		require.NoError(t, e.decide(t, decideOptions{clear: true}, "security:dropped-context-1"))
		assert.Equal(t, map[string]*store.Decision{"security:dropped-context-1": nil}, e.decisions(t).Decisions)
		assert.Equal(t, []string{"go:dropped-context-1"}, keys(e.stored(t)))
	})
	t.Run("clearing an undecided finding is fine", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{clear: true}, "go:dropped-context-1"))
		assert.Empty(t, e.stored(t))
	})
	t.Run("finds the review by --repo and --ref", func(t *testing.T) {
		e := newDecideApp(t)
		subject := &reviewSubjectOptions{repo: reviewedRepo, ref: testCommit}
		require.NoError(t, e.app.runReviewDecide(t.Context(), []string{"go:dropped-context-1"}, subject, &decideOptions{comment: true}))
		assert.Equal(t, store.DecisionComment, e.stored(t)["go:dropped-context-1"].Decision)
	})
	t.Run("an unknown finding is finding_not_found, and nothing changes", func(t *testing.T) {
		e := newDecideApp(t)
		err := e.decide(t, decideOptions{skip: true}, "go:dropped-context-1", "go:nope", "nope:dropped-context-1")
		requireCode(t, codeFindingNotFound, err)
		ce := asCLIError(err)
		assert.Equal(t, map[string]any{"findings": []string{"go:nope", "nope:dropped-context-1"}}, ce.details)
		assert.Equal(t, exitNotFound, ce.exitCode())
		assert.Empty(t, e.stored(t))
		assert.Empty(t, e.stdout.String())
	})
	t.Run("a superseded finding is finding_not_found", func(t *testing.T) {
		e := newDecideApp(t)
		e.submit(t, "security", reviewDoc("security", `"dropped-context-1"`, `"dropped-context-2"`))
		err := e.decide(t, decideOptions{skip: true}, "#1:dropped-context-1")
		requireCode(t, codeFindingNotFound, err)
		assert.Contains(t, err.Error(), "superseded")
		requireCode(t, codeFindingNotFound, e.decide(t, decideOptions{skip: true}, "security:dropped-context-1"))
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-2"))
	})
	t.Run("a review with no submissions has no findings", func(t *testing.T) {
		e := newReportApp(t)
		requireCode(t, codeFindingNotFound, e.decide(t, decideOptions{skip: true}, "go:dropped-context-1"))
	})
	t.Run("--suggestion past the finding's suggestions is usage", func(t *testing.T) {
		e := newDecideApp(t)
		requireUsageError(t, e.decide(t, decideOptions{fix: true, hasSuggestion: true, suggestion: 1}, "go:dropped-context-1"))
		assert.Empty(t, e.stored(t))
	})
	t.Run("an unknown workspace is review_not_found", func(t *testing.T) {
		e := newDecideApp(t)
		err := e.app.runReviewDecide(t.Context(), []string{"nope", "go:dropped-context-1"}, &reviewSubjectOptions{}, &decideOptions{skip: true})
		requireCode(t, codeReviewNotFound, err)
	})
	t.Run("a bad --repo is usage", func(t *testing.T) {
		e := newDecideApp(t)
		subject := &reviewSubjectOptions{repo: "not a repo", ref: testCommit}
		requireUsageError(t, e.app.runReviewDecide(t.Context(), []string{"go:dropped-context-1"}, subject, &decideOptions{skip: true}))
	})
	t.Run("prints each finding's decision without --json", func(t *testing.T) {
		e := newDecideApp(t)
		e.app.global.json = false
		require.NoError(t, e.decide(t, decideOptions{fix: true, hasSuggestion: true}, "security:dropped-context-1"))
		require.NoError(t, e.decide(t, decideOptions{clear: true}, "go:dropped-context-1"))
		assert.Equal(t, "security:dropped-context-1: fix with suggestion 0\ngo:dropped-context-1: undecided\n", e.stdout.String())
	})
}

func TestApp_runReviewActed(t *testing.T) {
	t.Run("--fix-sent fills done on findings decided fix", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{fix: true}, "security:dropped-context-1"))
		e.stdout.Reset()
		require.NoError(t, e.acted(t, actedOptions{fixSent: "fix-buff-bear-1"}, "security:dropped-context-1"))
		done := &store.Done{FixSent: "fix-buff-bear-1", At: testNow().UTC()}
		assert.Equal(t, done, e.decisions(t).Decisions["security:dropped-context-1"].Done)
		got := e.stored(t)["security:dropped-context-1"]
		assert.Equal(t, store.DecisionFix, got.Decision)
		assert.Equal(t, done, got.Done)
	})
	t.Run("--commented fills done on findings decided comment", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{comment: true}, "security:dropped-context-1", "go:dropped-context-1"))
		require.NoError(t, e.acted(t, actedOptions{commented: "https://x/pull/42#r1"}, "security:dropped-context-1", "go:dropped-context-1"))
		for _, d := range e.stored(t) {
			assert.Equal(t, &store.Done{Commented: "https://x/pull/42#r1", At: testNow().UTC()}, d.Done)
		}
	})
	t.Run("an undecided finding, or one decided otherwise, is usage, and nothing changes", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-1"))
		requireUsageError(t, e.acted(t, actedOptions{fixSent: "f"}, "go:dropped-context-1"))
		requireUsageError(t, e.acted(t, actedOptions{commented: "u"}, "security:dropped-context-1"))
		assert.Nil(t, e.stored(t)["security:dropped-context-1"].Done)
	})
	t.Run("an unknown or superseded finding is finding_not_found", func(t *testing.T) {
		e := newDecideApp(t)
		requireCode(t, codeFindingNotFound, e.acted(t, actedOptions{fixSent: "f"}, "go:nope"))
		requireCode(t, codeFindingNotFound, e.acted(t, actedOptions{fixSent: "f"}, "#1:dropped-context-1"))
	})
	t.Run("prints what was done without --json", func(t *testing.T) {
		e := newDecideApp(t)
		e.app.global.json = false
		require.NoError(t, e.decide(t, decideOptions{fix: true}, "go:dropped-context-1"))
		e.stdout.Reset()
		require.NoError(t, e.acted(t, actedOptions{fixSent: "fix-1"}, "go:dropped-context-1"))
		assert.Equal(t, "go:dropped-context-1: fix, sent to fix-1\n", e.stdout.String())
	})
}

func TestCurrentFindings(t *testing.T) {
	result := &collect.Result{Comments: []collect.Comment{{ID: "go:a"}, {ID: "#1:b"}}}
	t.Run("finds current findings", func(t *testing.T) {
		got, err := currentFindings(result, []string{"go:a"})
		require.NoError(t, err)
		assert.Equal(t, "go:a", got["go:a"].ID)
		assert.NotContains(t, got, "#1:b")
	})
	t.Run("lists the unknown, then the superseded", func(t *testing.T) {
		_, err := currentFindings(result, []string{"#1:b", "go:z", "go:a"})
		requireCode(t, codeFindingNotFound, err)
		ce := asCLIError(err)
		assert.Equal(t, []string{"go:z", "#1:b"}, ce.details["findings"])
	})
}

func TestDescribeDecision(t *testing.T) {
	assert.Equal(t, "undecided", describeDecision(nil))
	assert.Equal(t, "skip", describeDecision(&store.Decision{Decision: store.DecisionSkip}))
	assert.Equal(t, "comment, commented at https://x", describeDecision(&store.Decision{Decision: store.DecisionComment, Done: &store.Done{Commented: "https://x"}}))
}

func keys(d store.Decisions) []string {
	out := []string{}
	for k := range d {
		out = append(out, k)
	}
	return out
}
