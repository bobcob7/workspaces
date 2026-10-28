package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/review/record"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

// saveRun keeps a run of testRecord for workspace id, started minutes
// after the record's start, with pull request pr.
func (e *reportApp) saveRun(t *testing.T, id string, minutes, pr int) {
	t.Helper()
	rec := testRecord()
	rec.StartedAt = rec.StartedAt.Add(time.Duration(minutes) * time.Minute)
	rec.PR = &record.PR{Number: pr, URL: "https://github.com/bobcob7/buff-bear/pull/1"}
	require.NoError(t, store.New(e.app.paths.Reviews).SaveRun(id, &rec))
}

func TestApp_findReviewByID_removed(t *testing.T) {
	t.Run("a removed review is found by its run", func(t *testing.T) {
		e := newReportApp(t)
		e.saveRun(t, "gone-review", 0, 42)
		ws, rec, err := e.app.findReviewByID("gone-review")
		require.NoError(t, err)
		assert.Equal(t, "gone-review", ws.ID)
		assert.Equal(t, testCommit, rec.Ref)
	})
	t.Run("wait returns at once for a removed review", func(t *testing.T) {
		e := newReportApp(t)
		e.saveRun(t, "gone-review", 0, 42)
		e.submit(t, "security", reviewDoc("security"))
		e.app.global.json = true
		require.NoError(t, e.app.runReviewWait(t.Context(), "gone-review", time.Minute))
		assert.Nil(t, e.report(t)["agent"])
		assert.Equal(t, testCommit, e.report(t)["ref"])
	})
	t.Run("in neither is review_not_found", func(t *testing.T) {
		e := newReportApp(t)
		_, _, err := e.app.findReviewByID("nope")
		requireCode(t, codeReviewNotFound, err)
	})
}

func TestApp_runReviewList(t *testing.T) {
	t.Run("lists runs newest first, with their agent while it exists", func(t *testing.T) {
		e := newReportApp(t)
		e.saveRun(t, reportWorkspace, 0, 42)
		e.saveRun(t, "gone-review", 5, 41)
		e.app.global.json = true
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{}))
		var got reviewListJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Reviews, 2)
		assert.Equal(t, "gone-review", got.Reviews[0].ID)
		assert.Nil(t, got.Reviews[0].Agent)
		assert.Equal(t, reportWorkspace, got.Reviews[1].ID)
		assert.Equal(t, &reviewAgentJSON{Workspace: reportWorkspace, Status: lifetime.StateIdle}, got.Reviews[1].Agent)
	})
	t.Run("includes a review workspace with no run", func(t *testing.T) {
		e := newReportApp(t)
		e.app.global.json = true
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{}))
		assert.Contains(t, e.stdout.String(), `"id":"`+reportWorkspace+`"`)
		assert.Equal(t, 1, strings.Count(e.stdout.String(), `"id":`))
	})
	t.Run("a workspace that's both is listed once", func(t *testing.T) {
		e := newReportApp(t)
		e.saveRun(t, reportWorkspace, 0, 42)
		e.app.global.json = true
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{}))
		assert.Equal(t, 1, strings.Count(e.stdout.String(), `"id":`))
	})
	t.Run("filters by repo and pull request", func(t *testing.T) {
		e := newReportApp(t)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		e.saveRun(t, "a", 0, 41)
		e.saveRun(t, "b", 1, 42)
		e.app.global.json = true
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{repo: reviewedRepo, pr: 41}))
		var got reviewListJSON
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Reviews, 1)
		assert.Equal(t, "a", got.Reviews[0].ID)
		e.stdout.Reset()
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{repo: "github.com/o/other"}))
		assert.JSONEq(t, `{"reviews":[],"warnings":[]}`, e.stdout.String())
	})
	t.Run("a table marks removed agents", func(t *testing.T) {
		e := newReportApp(t)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		e.saveRun(t, "gone-review", 0, 41)
		e.app.now = func() time.Time { return testRecord().StartedAt.Add(time.Hour) }
		require.NoError(t, e.app.runReviewList(t.Context(), &reviewListOptions{}))
		assert.Contains(t, e.stdout.String(), "gone-review")
		assert.Contains(t, e.stdout.String(), "#41")
		assert.Contains(t, e.stdout.String(), "removed")
	})
	t.Run("an invalid --repo", func(t *testing.T) {
		e := newReportApp(t)
		requireUsageError(t, e.app.runReviewList(t.Context(), &reviewListOptions{repo: "nope"}))
	})
}

func TestReviewJudge_Complete(t *testing.T) {
	e := newReportApp(t)
	j := reviewJudge{state: e.app.paths.State, store: store.New(e.app.paths.Reviews)}
	ws := &workspace.Workspace{ID: reportWorkspace}
	done, err := j.Complete(t.Context(), ws)
	require.NoError(t, err)
	assert.False(t, done, "no submissions yet")
	rec := testRecord()
	for _, p := range rec.ProfileNames() {
		e.submit(t, p, reviewDoc(p))
	}
	done, err = j.Complete(t.Context(), ws)
	require.NoError(t, err)
	assert.True(t, done)
	done, err = j.Complete(t.Context(), &workspace.Workspace{ID: "not-a-review"})
	require.NoError(t, err)
	assert.False(t, done)
}
