package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/repo"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// reportApp is an App with review workspace reportWorkspace, whose record
// is testRecord, an agent in state, and submissions stored by submit.
type reportApp struct {
	*mockedApp

	state lifetime.State
	// states, when set, are the agent's states on successive listings;
	// the last repeats.
	states []lifetime.State
}

const reportWorkspace = "review-buff-bear-42-3f2a9c1-d8efb24f"

func newReportApp(t *testing.T) *reportApp {
	t.Helper()
	e := &reportApp{mockedApp: newMockedApp(), state: lifetime.StateIdle}
	e.app.paths = config.NewPaths(t.TempDir())
	e.app.reviewPoll = time.Millisecond
	ws := workspace.Workspace{ID: reportWorkspace}
	e.store.FindFunc = func(arg string) (workspace.Workspace, error) {
		if arg == reportWorkspace {
			return ws, nil
		}
		return workspace.Workspace{}, workspace.ErrNotFound
	}
	e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) {
		if len(e.states) > 0 {
			e.state = e.states[0]
			if len(e.states) > 1 {
				e.states = e.states[1:]
			}
		}
		return lifetime.Listing{Workspaces: []lifetime.Entry{{Workspace: ws, Session: &lifetime.Session{Kind: lifetime.KindAgent, ID: "e8cdf5d8", State: e.state}}}}, nil
	}
	rec := testRecord()
	require.NoError(t, os.MkdirAll(filepath.Join(e.app.paths.State, reportWorkspace), 0o750))
	require.NoError(t, record.Write(record.Path(e.app.paths.State, reportWorkspace), &rec))
	return e
}

// submit stores doc for profile at testCommit.
func (e *reportApp) submit(t *testing.T, profile, doc string) {
	t.Helper()
	id, err := repo.ParseID(reviewedRepo)
	require.NoError(t, err)
	_, err = store.New(e.app.paths.Reviews).Submit(t.Context(), id, testCommit, profile, []byte(doc))
	require.NoError(t, err)
}

// report decodes the JSON report on stdout.
func (e *reportApp) report(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
	return out
}

// typedReport decodes the JSON report on stdout into its type.
func (e *reportApp) typedReport(t *testing.T) reviewReportJSON {
	t.Helper()
	var out reviewReportJSON
	require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &out))
	return out
}

func TestApp_newReviewReportCmd(t *testing.T) {
	app, _, _ := newTestApp()
	cmd := app.newReviewReportCmd()
	args := func(t *testing.T, repoFlag, ref string, positional ...string) error {
		t.Helper()
		fresh := app.newReviewReportCmd()
		require.NoError(t, fresh.Flags().Set("repo", repoFlag))
		require.NoError(t, fresh.Flags().Set("ref", ref))
		return fresh.Args(fresh, positional)
	}
	t.Run("takes a workspace", func(t *testing.T) {
		assert.NoError(t, cmd.Args(cmd, []string{"x"}))
	})
	t.Run("takes --repo and --ref", func(t *testing.T) {
		assert.NoError(t, args(t, reviewedRepo, testCommit))
	})
	t.Run("refuses both, neither, or a short ref", func(t *testing.T) {
		requireUsageError(t, args(t, reviewedRepo, testCommit, "x"))
		requireUsageError(t, args(t, "", ""))
		requireUsageError(t, args(t, reviewedRepo, ""))
		requireUsageError(t, args(t, reviewedRepo, "3f2a9c1"))
		requireUsageError(t, cmd.Args(cmd, []string{"x", "y"}))
	})
}

func TestApp_newReviewReportCmd_decided(t *testing.T) {
	e := newReportApp(t)
	assert.Equal(t, exitUsage, e.app.Run(t.Context(), []string{"review", "report", "--decided", "maybe", reportWorkspace}))
	assert.Contains(t, e.stderr.String(), "--decided must be comment, fix, or skip")
}

func TestApp_runReviewReport(t *testing.T) {
	t.Run("combines the submissions with the record and agent", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", reviewDoc("security"))
		e.app.global.json = true
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		out := e.report(t)
		assert.Equal(t, reviewedRepo, out["repo"])
		assert.Equal(t, testCommit, out["ref"])
		assert.Equal(t, map[string]any{"workspace": reportWorkspace, "status": "idle"}, out["agent"])
		assert.Equal(t, "fix-login-d8efb24f", e.typedReport(t).Review.Requester)
		assert.Equal(t, []any{
			map[string]any{
				"name": "security", "model": "opus", "verdict": "request_changes", "missing": false,
				"severity": map[string]any{"max": 8.0, "must_fix": 0.0, "should_fix": 1.0, "worth_fixing": 0.0, "optional": 0.0},
			},
			map[string]any{"name": "go", "model": "", "verdict": nil, "severity": nil, "missing": true},
		}, out["profiles"])
		assert.Contains(t, e.stdout.String(), `"decision":null`)
		comments := e.typedReport(t).Comments
		require.Len(t, comments, 1)
		c := comments[0]
		assert.Equal(t, "security:dropped-context-1", c.ID)
		assert.Equal(t, "security", c.Profile)
		assert.Nil(t, c.Code)
		assert.Equal(t, []reviewAnchorJSON{{File: "client.go", Line: 3, EndLine: new(5)}}, c.Anchors)
		assert.Equal(t, []any{}, out["superseded"])
		assert.Equal(t, []any{}, out["warnings"])
	})
	t.Run("a superseded submission's findings are under superseded", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", reviewDoc("security"))
		e.submit(t, "security", reviewDoc("security", `"dropped-context-1"`, `"dropped-context-2"`))
		e.app.global.json = true
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		out := e.typedReport(t)
		require.Len(t, out.Comments, 1)
		require.Len(t, out.Superseded, 1)
		assert.Equal(t, "security:dropped-context-2", out.Comments[0].ID)
		assert.Equal(t, "#1:dropped-context-1", out.Superseded[0].ID)
	})
	t.Run("--repo and --ref find the review, and work without a workspace", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "go", reviewDoc("go"))
		e.app.global.json = true
		subject := &reviewSubjectOptions{repo: reviewedRepo, ref: testCommit}
		require.NoError(t, e.app.runReviewReport(t.Context(), nil, subject, "", ""))
		assert.NotNil(t, e.report(t)["review"])
		e.stdout.Reset()
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		require.NoError(t, e.app.runReviewReport(t.Context(), nil, subject, "", ""))
		out := e.report(t)
		assert.Nil(t, out["review"])
		assert.Nil(t, out["agent"])
		assert.Equal(t, []any{map[string]any{
			"name": "go", "model": "", "verdict": "request_changes", "missing": false,
			"severity": map[string]any{"max": 8.0, "must_fix": 0.0, "should_fix": 1.0, "worth_fixing": 0.0, "optional": 0.0},
		}}, out["profiles"])
	})
	t.Run("an unknown workspace is review_not_found", func(t *testing.T) {
		e := newReportApp(t)
		requireCode(t, codeReviewNotFound, e.app.runReviewReport(t.Context(), []string{"nope"}, &reviewSubjectOptions{}, "", ""))
	})
	t.Run("a workspace without a record is review_not_found", func(t *testing.T) {
		e := newReportApp(t)
		require.NoError(t, os.Remove(record.Path(e.app.paths.State, reportWorkspace)))
		requireCode(t, codeReviewNotFound, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
	})
	t.Run("markdown by default, listing missing profiles", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", reviewDoc("security"))
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		assert.Contains(t, e.stdout.String(), "## security:dropped-context-1")
		assert.Contains(t, e.stdout.String(), "\n**Missing** go\n")
	})
	t.Run("html", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", reviewDoc("security"))
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "html", ""))
		assert.Contains(t, e.stdout.String(), "<!doctype html>")
	})
	t.Run("each current finding carries its decision", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{fix: true, hasSuggestion: true, note: "n", hasNote: true}, "security:dropped-context-1"))
		require.NoError(t, e.acted(t, actedOptions{fixSent: "fix-1"}, "security:dropped-context-1"))
		e.stdout.Reset()
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		comments := e.typedReport(t).Comments
		require.Len(t, comments, 2)
		assert.Equal(t, "go:dropped-context-1", comments[0].ID)
		assert.Nil(t, comments[0].Decision)
		assert.Equal(t, &store.Decision{
			Decision: store.DecisionFix, Suggestion: new(0), Note: "n", At: testNow().UTC(),
			Done: &store.Done{FixSent: "fix-1", At: testNow().UTC()},
		}, comments[1].Decision)
	})
	t.Run("a resubmission drops its profile's decisions", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-1", "go:dropped-context-1"))
		e.submit(t, "security", reviewDoc("security"))
		e.stdout.Reset()
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		out := e.typedReport(t)
		require.Len(t, out.Comments, 2)
		assert.NotNil(t, out.Comments[0].Decision, "go's decision stays")
		assert.Nil(t, out.Comments[1].Decision, "security resubmitted")
		require.Len(t, out.Superseded, 1)
		assert.Nil(t, out.Superseded[0].Decision)
	})
	t.Run("--decided lists only the findings decided that way", func(t *testing.T) {
		e := newDecideApp(t)
		e.submit(t, "security", reviewDoc("security", `"dropped-context-1"`, `"dropped-context-2"`))
		require.NoError(t, e.decide(t, decideOptions{fix: true}, "go:dropped-context-1"))
		require.NoError(t, e.decide(t, decideOptions{skip: true}, "security:dropped-context-2"))
		for decided, want := range map[string][]string{"fix": {"go:dropped-context-1"}, "skip": {"security:dropped-context-2"}, "comment": {}} {
			e.stdout.Reset()
			require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", decided))
			out := e.typedReport(t)
			ids := []string{}
			for _, c := range out.Comments {
				ids = append(ids, c.ID)
			}
			assert.Equal(t, want, ids, decided)
			assert.Empty(t, out.Superseded, decided)
			assert.Len(t, out.Profiles, 2, decided)
		}
	})
	t.Run("--decided filters the markdown report too", func(t *testing.T) {
		e := newDecideApp(t)
		require.NoError(t, e.decide(t, decideOptions{fix: true}, "go:dropped-context-1"))
		e.app.global.json = false
		e.stdout.Reset()
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", "fix"))
		assert.Contains(t, e.stdout.String(), "## go:dropped-context-1")
		assert.NotContains(t, e.stdout.String(), "## security:dropped-context-1")
	})
	t.Run("an unreadable submission is a warning", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", "not json")
		e.app.global.json = true
		require.NoError(t, e.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		assert.Equal(t, []any{"skipped 1 stored submission that couldn't be read"}, e.report(t)["warnings"])
	})
}

func TestApp_reportFormat(t *testing.T) {
	app, _, _ := newTestApp()
	t.Run("markdown by default", func(t *testing.T) {
		got, err := app.reportFormat("")
		require.NoError(t, err)
		assert.Equal(t, formatMarkdown, got)
	})
	t.Run("json with --json", func(t *testing.T) {
		app.global.json = true
		defer func() { app.global.json = false }()
		got, err := app.reportFormat("")
		require.NoError(t, err)
		assert.Equal(t, "json", got)
		_, err = app.reportFormat("html")
		requireUsageError(t, err)
	})
	t.Run("refuses an unknown format", func(t *testing.T) {
		_, err := app.reportFormat("pdf")
		requireUsageError(t, err)
	})
}

func TestApp_runReviewWait(t *testing.T) {
	t.Run("waits while the agent works, then prints the report", func(t *testing.T) {
		e := newReportApp(t)
		e.states = []lifetime.State{lifetime.StateStarting, lifetime.StateWorking, lifetime.StateWorking, lifetime.StateIdle}
		e.submit(t, "security", reviewDoc("security"))
		e.app.global.json = true
		require.NoError(t, e.app.runReviewWait(t.Context(), reportWorkspace, time.Minute))
		assert.Len(t, e.lifetime.ListCalls(), 4)
		assert.Equal(t, map[string]any{"workspace": reportWorkspace, "status": "idle"}, e.report(t)["agent"])
	})
	t.Run("a stopped agent is done", func(t *testing.T) {
		e := newReportApp(t)
		e.state = lifetime.StateStopped
		e.app.global.json = true
		require.NoError(t, e.app.runReviewWait(t.Context(), reportWorkspace, time.Minute))
		assert.Len(t, e.lifetime.ListCalls(), 1)
	})
	t.Run("a removed workspace is done", func(t *testing.T) {
		e := newReportApp(t)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		e.app.global.json = true
		require.NoError(t, e.app.runReviewWait(t.Context(), reportWorkspace, time.Minute))
		assert.Nil(t, e.report(t)["agent"])
	})
	t.Run("times out with review_timeout", func(t *testing.T) {
		e := newReportApp(t)
		e.state = lifetime.StateWorking
		err := e.app.runReviewWait(t.Context(), reportWorkspace, 20*time.Millisecond)
		requireCode(t, codeReviewTimeout, err)
		ce, ok := errors.AsType[*cliError](err)
		require.True(t, ok)
		assert.Equal(t, map[string]any{"workspace": reportWorkspace, "status": lifetime.StateWorking}, ce.details)
		assert.Empty(t, e.stdout.String())
	})
	t.Run("an unknown workspace is review_not_found", func(t *testing.T) {
		e := newReportApp(t)
		requireCode(t, codeReviewNotFound, e.app.runReviewWait(t.Context(), "nope", time.Minute))
	})
	t.Run("refuses a timeout that isn't positive", func(t *testing.T) {
		e := newReportApp(t)
		requireUsageError(t, e.app.runReviewWait(t.Context(), reportWorkspace, 0))
	})
	t.Run("listing failing is an error", func(t *testing.T) {
		e := newReportApp(t)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, errTest }
		assert.ErrorIs(t, e.app.runReviewWait(t.Context(), reportWorkspace, time.Minute), errTest)
	})
	t.Run("the default timeout is [review] timeout", func(t *testing.T) {
		e := newReportApp(t)
		e.app.configs = &configLoaderMock{LoadFunc: func(string) (config.Paths, config.Config, error) {
			cfg := testConfig()
			cfg.Review.Timeout = 10 * time.Millisecond
			return e.app.paths, cfg, nil
		}}
		e.state = lifetime.StateWorking
		e.app.global.json = true
		assert.Equal(t, exitFailed, e.app.Run(t.Context(), []string{"--json", "review", "wait", reportWorkspace}))
		assert.Contains(t, e.stderr.String(), `"code":"review_timeout"`)
	})
}

func TestReviewFinished(t *testing.T) {
	assert.True(t, reviewFinished(nil))
	assert.True(t, reviewFinished(&reviewAgentJSON{Status: lifetime.StateIdle}))
	assert.True(t, reviewFinished(&reviewAgentJSON{Status: lifetime.StateStopped}))
	assert.False(t, reviewFinished(&reviewAgentJSON{Status: lifetime.StateWorking}))
	assert.False(t, reviewFinished(&reviewAgentJSON{Status: lifetime.StateUnknown}))
}
