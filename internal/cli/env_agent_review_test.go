package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
