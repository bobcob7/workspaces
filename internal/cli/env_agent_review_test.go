package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/config"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/gh"
	"github.com/bobcob7/workspaces/internal/lifetime"
	"github.com/bobcob7/workspaces/internal/redact"
	"github.com/bobcob7/workspaces/internal/review/store"
)

// callReview decodes raw as op's arguments, as the agent does, and runs it.
func callReview(t *testing.T, ops *agentOperations, op, raw string) (map[string]any, error) {
	t.Helper()
	args, err := envproto.DecodeArgs(op, json.RawMessage(raw))
	require.NoError(t, err)
	out, err := ops.Run(t.Context(), op, args)
	if err != nil {
		return nil, err
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal(out, &got))
	return got, nil
}

func replyOf(t *testing.T, err error) *envproto.ReplyError {
	t.Helper()
	re, ok := errors.AsType[*envproto.ReplyError](err)
	require.True(t, ok, "%T: %v", err, err)
	return re
}

func requireReplyCode(t *testing.T, err error, code string) {
	t.Helper()
	assert.Equal(t, code, replyOf(t, err).Code)
}

// reviewOps is the agent's operations in app, whose paths stay the test's
// temporary ones, and which config loading returns.
func reviewOps(app *App) *agentOperations {
	paths := app.paths
	ops := opsFor(app)
	app.paths = paths
	loader, ok := app.configs.(*configLoaderMock)
	if !ok {
		panic("the test App's config loader is not a mock")
	}
	loader.LoadFunc = func(string) (config.Paths, config.Config, error) {
		return paths, testConfig(), nil
	}
	return ops
}

// startOps is the agent's operations in e's App, whose stores are e's.
func startOps(e *reviewStartEnv) *agentOperations {
	e.app.newStores = func(config.Paths, config.Config) stores {
		return stores{cache: e.cache, workspaces: e.store, lifetime: e.app.lifetime, loopData: e.app.loopData}
	}
	return reviewOps(e.app)
}

// bigDoc is a review submission with n comments whose bodies are bodyLen bytes.
func bigDoc(profile string, n, bodyLen int, extra string) string {
	var comments []string
	for i := range n {
		comments = append(comments, fmt.Sprintf(`{"id":"c-%d","priority":5,"category":"style","body":%q,"anchors":[{"file":"client.go","line":1}],"suggestions":[]}`, i, strings.Repeat("x", bodyLen)+extra))
	}
	return `{"version":"1","verdict":"comment","profile":"` + profile + `","summary":"s","comments":[` + strings.Join(comments, ",") + `]}`
}

func TestReviewStartArgv(t *testing.T) {
	head := strings.Repeat("a", 40)
	prefix := []string{"--json", "--config-dir", "/cfg", "review", "start", "--requester", "none"}
	tests := []struct {
		name string
		args envproto.ReviewStartArgs
		want []string
	}{
		{"a pull request", envproto.ReviewStartArgs{Repo: "o/n", PR: new(7)}, append(append([]string{}, prefix...), "--pr", "7", "o/n")},
		{"a head has repo@head and no --pr", envproto.ReviewStartArgs{Repo: "o/n", Head: &head}, append(append([]string{}, prefix...), "o/n@"+head)},
		{
			"a focus goes after -- and model flags before it",
			envproto.ReviewStartArgs{Repo: "o/n", PR: new(1), Name: new("nm"), Profiles: []string{"sec:opus"}, Auto: []string{"go"}, Model: new("sonnet"), PermissionMode: new("plan"), Focus: new("look at ctx")},
			append(append([]string{}, prefix...), "--name", "nm", "--pr", "1", "--profile", "sec:opus", "--auto", "go", "o/n", "--", "--model=sonnet", "--permission-mode=plan", "look at ctx"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, reviewStartArgv("/cfg", &tt.args))
		})
	}
}

func TestReportPage(t *testing.T) {
	t.Run("a section is paged from an offset and the other is empty", func(t *testing.T) {
		report := json.RawMessage(`{"repo":"r","comments":[{"id":"a"},{"id":"b"}],"superseded":[{"id":"z"}]}`)
		got, err := reportPage(report, "comments", 1)
		require.NoError(t, err)
		assert.JSONEq(t, `{"repo":"r","comments":[{"id":"b"}],"superseded":[],"section":"comments","offset":1,"next_offset":null,"total":2}`, string(got))
	})
	t.Run("an offset past the end is an empty page", func(t *testing.T) {
		got, err := reportPage(json.RawMessage(`{"comments":[{"id":"a"}],"superseded":[]}`), "comments", 9)
		require.NoError(t, err)
		assert.JSONEq(t, `{"comments":[],"superseded":[],"section":"comments","offset":9,"next_offset":null,"total":1}`, string(got))
	})
}

func TestAgentOperations_review(t *testing.T) {
	t.Run("start runs ws review start with requester none", func(t *testing.T) {
		e := newReviewStartEnv(t)
		ops := startOps(e)
		got, err := callReview(t, ops, envproto.OpReviewStart, `{"repo":"buff-bear","pr":42}`)
		require.NoError(t, err)
		assert.Contains(t, got, "id")
		assert.EqualValues(t, 0, got["redacted"])
		assert.Empty(t, e.record(t).Requester, "a requester of none is a top-level review")
		assert.Empty(t, e.store.ContainingCalls(), "no requester is looked up from the working directory")
		require.Len(t, e.gh.PRViewCalls(), 1)
	})
	t.Run("start with head passes repo@head and no --pr", func(t *testing.T) {
		argv := reviewStartArgv("/c", &envproto.ReviewStartArgs{Repo: "o/n", Head: new(strings.Repeat("b", 40))})
		assert.NotContains(t, argv, "--pr")
		assert.Contains(t, argv, "o/n@"+strings.Repeat("b", 40))
	})
	t.Run("a focus goes after -- and model flags before it", func(t *testing.T) {
		argv := reviewStartArgv("/c", &envproto.ReviewStartArgs{Repo: "o/n", PR: new(1), Model: new("opus"), Focus: new("check")})
		assert.Equal(t, []string{"--", "--model=opus", "check"}, argv[len(argv)-3:])
	})
	t.Run("start fails with gh_auth from the remote's gh", func(t *testing.T) {
		e := newReviewStartEnv(t)
		e.gh.PRViewFunc = func(context.Context, string, int) (gh.PR, error) { return gh.PR{}, gh.ErrAuth }
		ops := startOps(e)
		_, err := callReview(t, ops, envproto.OpReviewStart, `{"repo":"buff-bear","pr":42}`)
		requireReplyCode(t, err, "gh_auth")
	})
	t.Run("status of a removed review is finished with agent null", func(t *testing.T) {
		e := newReportApp(t)
		e.lifetime.ListFunc = func(context.Context) (lifetime.Listing, error) { return lifetime.Listing{}, nil }
		rec := testRecord()
		require.NoError(t, store.New(e.app.paths.Reviews).SaveRun(reportWorkspace, &rec))
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewStatus, `{"review":"`+reportWorkspace+`"}`)
		require.NoError(t, err)
		assert.Equal(t, true, got["finished"])
		assert.Nil(t, got["agent"])
	})
	t.Run("status of a running review is not finished", func(t *testing.T) {
		e := newReportApp(t)
		e.state = lifetime.StateWorking
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewStatus, `{"review":"`+reportWorkspace+`"}`)
		require.NoError(t, err)
		assert.Equal(t, false, got["finished"])
	})
	t.Run("status of an unknown id is review_not_found", func(t *testing.T) {
		e := newReportApp(t)
		_, err := callReview(t, reviewOps(e.app), envproto.OpReviewStatus, `{"review":"nope"}`)
		requireReplyCode(t, err, "review_not_found")
	})
	t.Run("list keeps the first limit runs and says truncated", func(t *testing.T) {
		e := newReportApp(t)
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewList, `{"limit":1}`)
		require.NoError(t, err)
		assert.Len(t, got["reviews"], 1)
		assert.Equal(t, false, got["truncated"])
	})
	t.Run("a report over 192 KiB is paged and next_offset walks it", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", bigDoc("security", 300, 3000, ""))
		ops := reviewOps(e.app)
		seen, offset := 0, 0
		for pages := 0; ; pages++ {
			require.Less(t, pages, 20)
			raw, err := ops.Run(t.Context(), envproto.OpReviewReport, &envproto.ReviewReportArgs{Review: reportWorkspace, Section: "comments", Offset: offset})
			require.NoError(t, err)
			assert.LessOrEqual(t, len(raw), envproto.MaxPageBytes)
			var page struct {
				Comments   []json.RawMessage `json:"comments"`
				Superseded []json.RawMessage `json:"superseded"`
				Next       *int              `json:"next_offset"`
				Total      int               `json:"total"`
			}
			require.NoError(t, json.Unmarshal(raw, &page))
			assert.Empty(t, page.Superseded)
			assert.Equal(t, 300, page.Total)
			seen += len(page.Comments)
			if page.Next == nil {
				break
			}
			assert.Equal(t, seen, *page.Next)
			offset = *page.Next
		}
		assert.Equal(t, 300, seen)
	})
	t.Run("a single oversized comment is cut and marked truncated", func(t *testing.T) {
		huge := json.RawMessage(`{"id":"a","body":"` + strings.Repeat("é", 150000) + `","code":"` + strings.Repeat("c", 150000) + `","suggestions":[{"code":"` + strings.Repeat("s", 150000) + `"}]}`)
		page, err := reportPage(json.RawMessage(`{"comments":[`+string(huge)+`]}`), "comments", 0)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(page), envproto.MaxPageBytes)
		var got struct {
			Comments []struct {
				Body      string `json:"body"`
				Truncated bool   `json:"truncated"`
			} `json:"comments"`
		}
		require.NoError(t, json.Unmarshal(page, &got))
		require.Len(t, got.Comments, 1)
		assert.True(t, got.Comments[0].Truncated)
		assert.True(t, strings.HasPrefix(got.Comments[0].Body, "é"))
		assert.NotContains(t, got.Comments[0].Body, "�")
	})
	t.Run("a token in a comment body comes back redacted and counted", func(t *testing.T) {
		e := newReportApp(t)
		token := "ghp_" + strings.Repeat("A", 36)
		e.submit(t, "security", reviewDoc("security", "c.do ignores", "leaked "+token+" c.do ignores"))
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewReport, `{"review":"`+reportWorkspace+`","section":"comments","offset":0}`)
		require.NoError(t, err)
		raw, _ := json.Marshal(got)
		assert.NotContains(t, string(raw), token)
		assert.Contains(t, string(raw), redact.Marker)
		assert.EqualValues(t, 1, got["redacted"])
	})
	t.Run("a secrets.env value in a finding note is redacted", func(t *testing.T) {
		e := newDecideApp(t)
		ops := reviewOps(e.app)
		ops.redactor = redact.New("hunter2-secret")
		got, err := callReview(t, ops, envproto.OpReviewDecide, `{"review":"`+reportWorkspace+`","findings":["security:dropped-context-1"],"decision":"skip","note":"see hunter2-secret"}`)
		require.NoError(t, err)
		raw, _ := json.Marshal(got)
		assert.NotContains(t, string(raw), "hunter2-secret")
		assert.EqualValues(t, 1, got["redacted"])
	})
	t.Run("an error message and details are redacted", func(t *testing.T) {
		ops := &agentOperations{redactor: redact.New("hunter2-secret")}
		err := ops.redactError(&envproto.ReplyError{Code: "x", Message: "bad hunter2-secret", Details: map[string]any{"k": []any{"hunter2-secret"}}})
		re := replyOf(t, err)
		assert.NotContains(t, re.Message, "hunter2-secret")
		assert.NotContains(t, fmt.Sprint(re.Details), "hunter2-secret")
	})
	t.Run("decide passes the note as one argument", func(t *testing.T) {
		argv := reviewDecideArgv("/c", &envproto.ReviewDecideArgs{Review: "r", Findings: []string{"a:b"}, Decision: "skip", Note: new("--clear x y")})
		assert.Equal(t, []string{"--json", "--config-dir", "/c", "review", "decide", "--skip", "--note=--clear x y", "--", "r", "a:b"}, argv)
	})
	t.Run("decide records a decision through the CLI", func(t *testing.T) {
		e := newDecideApp(t)
		_, err := callReview(t, reviewOps(e.app), envproto.OpReviewDecide, `{"review":"`+reportWorkspace+`","findings":["security:dropped-context-1"],"decision":"comment"}`)
		require.NoError(t, err)
		assert.Contains(t, keys(e.stored(t)), "security:dropped-context-1")
	})
	t.Run("acted needs exactly one of commented and fix_sent", func(t *testing.T) {
		_, err := envproto.DecodeArgs(envproto.OpReviewActed, json.RawMessage(`{"review":"r","findings":["a:b"]}`))
		require.Error(t, err)
		_, err = envproto.DecodeArgs(envproto.OpReviewActed, json.RawMessage(`{"review":"r","findings":["a:b"],"commented":"https://x.test/1","fix_sent":"w"}`))
		require.Error(t, err)
		argv := reviewActedArgv("/c", &envproto.ReviewActedArgs{Review: "r", Findings: []string{"a:b"}, Commented: new("https://x.test/1")})
		assert.Equal(t, []string{"--json", "--config-dir", "/c", "review", "acted", "--commented=https://x.test/1", "--", "r", "a:b"}, argv)
	})
	t.Run("a review id is looked up only by id, never by repo and ref", func(t *testing.T) {
		for _, argv := range [][]string{
			reviewDecideArgv("/c", &envproto.ReviewDecideArgs{Review: "r", Findings: []string{"a:b"}, Decision: "skip"}),
			reviewActedArgv("/c", &envproto.ReviewActedArgs{Review: "r", Findings: []string{"a:b"}, FixSent: new("w")}),
		} {
			assert.NotContains(t, argv, "--repo")
			assert.NotContains(t, argv, "--ref")
		}
	})
	t.Run("profiles lists the profiles", func(t *testing.T) {
		e := newReportApp(t)
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewProfiles, `{}`)
		require.NoError(t, err)
		assert.Contains(t, got, "redacted")
	})
	t.Run("the result equals the CLI's JSON plus redacted, for a report that fits one page", func(t *testing.T) {
		e := newReportApp(t)
		e.submit(t, "security", reviewDoc("security"))
		got, err := callReview(t, reviewOps(e.app), envproto.OpReviewReport, `{"review":"`+reportWorkspace+`","section":"comments","offset":0}`)
		require.NoError(t, err)
		e2 := newReportApp(t)
		e2.submit(t, "security", reviewDoc("security"))
		e2.app.global.json = true
		require.NoError(t, e2.app.runReviewReport(t.Context(), []string{reportWorkspace}, &reviewSubjectOptions{}, "", ""))
		var want map[string]any
		require.NoError(t, json.Unmarshal(e2.stdout.Bytes(), &want))
		want["redacted"], want["section"], want["offset"], want["next_offset"], want["total"] = 0.0, "comments", 0.0, nil, 1.0
		want["superseded"] = []any{}
		assert.Equal(t, want, got)
	})
}

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

func TestTruncateItem_absentCodeStaysAbsent(t *testing.T) {
	body := strings.Repeat("b", 2<<20)
	item := mustMarshal(map[string]any{
		"body":        body,
		"suggestions": []map[string]any{{"note": "no code here"}},
	})
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(truncateItem(item, 1<<20), &got))
	assert.NotContains(t, got, "code")
	assert.JSONEq(t, "true", string(got["truncated"]))
	var suggestions []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got["suggestions"], &suggestions))
	require.Len(t, suggestions, 1)
	assert.NotContains(t, suggestions[0], "code")
}
