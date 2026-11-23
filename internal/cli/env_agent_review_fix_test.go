package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/redact"
	"github.com/bobcob7/workspaces/internal/review/store"
)

func TestReviewArgvSeparators(t *testing.T) {
	t.Run("report puts -- before the id", func(t *testing.T) {
		argv := reviewReportArgv("/c", &envproto.ReviewReportArgs{Review: "--evil", Section: "comments"})
		assert.Equal(t, []string{"--json", "--config-dir", "/c", "review", "report", "--", "--evil"}, argv)
	})
	t.Run("acted passes --fix-sent= as one argument", func(t *testing.T) {
		argv := reviewActedArgv("/c", &envproto.ReviewActedArgs{Review: "r", Findings: []string{"a:b"}, FixSent: new("--x y")})
		assert.Equal(t, []string{"--json", "--config-dir", "/c", "review", "acted", "--fix-sent=--x y", "--", "r", "a:b"}, argv)
	})
}

func TestAgentRedactor_environment(t *testing.T) {
	app := New("v", strings.NewReader(""), io.Discard, io.Discard, nil, []string{"CUSTOM_API_KEY=env-only-value-123"})
	app.paths = config.NewPaths(t.TempDir())
	app.paths.Root = t.TempDir()
	clean, n := agentRedactor(app).String("leaked env-only-value-123 here")
	assert.NotContains(t, clean, "env-only-value-123")
	assert.Equal(t, 1, n)
}

func TestAgentOperations_reviewStartSpec(t *testing.T) {
	for name, args := range map[string]*envproto.ReviewStartArgs{
		"an invalid repo": {Repo: "not a repo", PR: new(1)},
		"an invalid head": {Repo: "o/n", Head: new("-x")},
	} {
		t.Run(name+" is spec_invalid and runs no CLI", func(t *testing.T) {
			ops := &agentOperations{app: New("v", strings.NewReader(""), io.Discard, io.Discard, nil, nil)}
			ops.newApp = func(io.Writer, io.Writer) *App {
				t.Fatal("the CLI ran")
				return nil
			}
			_, err := ops.Run(t.Context(), envproto.OpReviewStart, args)
			requireReplyCode(t, err, "spec_invalid")
		})
	}
}

func TestFinishResult_size(t *testing.T) {
	ops := &agentOperations{}
	object := func(n int) json.RawMessage {
		return json.RawMessage(`{"a":"` + strings.Repeat("x", n-8) + `"}`)
	}
	t.Run("a result between the slack and the ceiling is too large", func(t *testing.T) {
		_, err := ops.finishResult(object(envproto.MaxRequestBytes - 100))
		re := replyOf(t, err)
		assert.Equal(t, "internal", re.Code)
		assert.Equal(t, "result too large", re.Message)
	})
	t.Run("a result under the slack is returned", func(t *testing.T) {
		out, err := ops.finishResult(object(envproto.MaxRequestBytes - resultSlack - 100))
		require.NoError(t, err)
		assert.LessOrEqual(t, len(out), envproto.MaxRequestBytes-resultSlack)
	})
}

func TestReportPage_budget(t *testing.T) {
	fields, err := json.Marshal(map[string]any{"comments": []any{}, "superseded": []any{}, "section": "comments", "offset": 0, "total": 2, "next_offset": 2})
	require.NoError(t, err)
	budget := envproto.MaxPageBytes - len(fields) - resultSlack/4
	first := `"` + strings.Repeat("x", budget-2) + `"`
	require.Len(t, first, budget)
	page, err := reportPage(json.RawMessage(`{"comments":[`+first+`,"b"]}`), "comments", 0)
	require.NoError(t, err)
	var got struct {
		Comments []string `json:"comments"`
		Next     *int     `json:"next_offset"`
	}
	require.NoError(t, json.Unmarshal(page, &got))
	require.Len(t, got.Comments, 1, "an item exactly at the budget is the page, never an empty page")
	require.NotNil(t, got.Next)
	assert.Equal(t, 1, *got.Next)
}

func TestAgentOperations_reviewRedactsBeforeCutting(t *testing.T) {
	token := "ghp_" + strings.Repeat("A", 36)
	oneDoc := func(body string) string {
		return fmt.Sprintf(`{"version":"1","verdict":"comment","profile":"security","summary":"s","comments":[{"id":"c-0","priority":5,"category":"style","body":%q,"anchors":[{"file":"client.go","line":1}],"suggestions":[]}]}`, body)
	}
	t.Run("a token straddling the field cap leaves no prefix", func(t *testing.T) {
		e := newReportApp(t)
		body := strings.Repeat("x", truncatedFieldCap-10) + token + strings.Repeat("y", 150<<10)
		e.submit(t, "security", oneDoc(body))
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewReport, `{"review":"`+reportWorkspace+`","section":"comments","offset":0}`)
		require.NoError(t, err)
		raw, _ := json.Marshal(got)
		assert.Contains(t, string(raw), `"truncated":true`)
		assert.NotContains(t, string(raw), "ghp_")
		assert.EqualValues(t, 1, got["redacted"])
	})
	t.Run("a token in every comment leaves no prefix in any page", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", bigDoc("security", 100, 3000, " "+token))
		ops := reviewOps(e.app)
		offset, pages := 0, 0
		for {
			raw, err := ops.Run(t.Context(), envproto.OpReviewReport, &envproto.ReviewReportArgs{Review: reportWorkspace, Section: "comments", Offset: offset})
			require.NoError(t, err)
			assert.NotContains(t, string(raw), "ghp_")
			pages++
			var page struct {
				Next *int `json:"next_offset"`
			}
			require.NoError(t, json.Unmarshal(raw, &page))
			if page.Next == nil {
				break
			}
			offset = *page.Next
		}
		assert.Greater(t, pages, 1)
	})
}

func TestAgentOperations_reviewList_limit(t *testing.T) {
	saveRuns := func(t *testing.T, e *reportApp, n int) {
		t.Helper()
		for i := range n {
			rec := testRecord()
			require.NoError(t, store.New(e.app.paths.Reviews).SaveRun(fmt.Sprintf("run-%03d", i), &rec))
		}
	}
	for _, tt := range []struct {
		name      string
		runs      int
		truncated bool
	}{
		{"one over the default limit is truncated", envproto.DefaultListLimit + 1, true},
		{"exactly the default limit is not truncated", envproto.DefaultListLimit, false},
	} {
		t.Run("limit 0 keeps the default: "+tt.name, func(t *testing.T) {
			e := newReportApp(t)
			saveRuns(t, e, tt.runs-1)
			got, err := callReview(t, reviewOps(e.app), envproto.OpReviewList, `{}`)
			require.NoError(t, err)
			assert.Len(t, got["reviews"], envproto.DefaultListLimit)
			assert.Equal(t, tt.truncated, got["truncated"])
		})
	}
}

func TestAgentOperations_reviewStatus_finished(t *testing.T) {
	for state, want := range map[lifetime.State]bool{lifetime.StateIdle: true, lifetime.StateStopped: true, lifetime.StateWorking: false, lifetime.StateStarting: false} {
		t.Run(string(state), func(t *testing.T) {
			e := newReportApp(t)
			e.state = state
			got, err := callReview(t, reviewOps(e.app), envproto.OpReviewStatus, `{"review":"`+reportWorkspace+`"}`)
			require.NoError(t, err)
			assert.Equal(t, want, got["finished"])
			assert.NotNil(t, got["agent"], "the agent is listed, so finished comes from its state")
		})
	}
}

func TestRedactError_unredactableDetails(t *testing.T) {
	ops := &agentOperations{redactor: redact.New("hunter2-secret")}
	err := ops.redactError(&envproto.ReplyError{Code: "x", Message: "m", Details: map[string]any{"secret": "hunter2-secret", "bad": make(chan int)}})
	re := replyOf(t, err)
	assert.Nil(t, re.Details, "details that can't be redacted are dropped, never leaked")
	assert.Equal(t, "m", re.Message)
}
