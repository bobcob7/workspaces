package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bobcob7/workspaces/internal/daemon"
	"github.com/bobcob7/workspaces/internal/envproto"
	"github.com/bobcob7/workspaces/internal/review/store"
	"github.com/bobcob7/workspaces/internal/workspace"
)

const remoteCommit = "3f2a9c1e3f2a9c1e3f2a9c1e3f2a9c1e3f2a9c1e"

// insideWorkspace makes the App run in a workspace.
func insideWorkspace(e *mockedApp) {
	e.app.getwd = func() (string, error) { return "/cfg/workspaces/fix-login-d8efb24f/buff-bear", nil }
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) {
		return workspace.Workspace{ID: "fix-login-d8efb24f"}, nil
	}
}

// outsideWorkspace makes the App run outside any workspace.
func outsideWorkspace(e *mockedApp) {
	e.app.getwd = func() (string, error) { return "/home/me", nil }
	e.store.ContainingFunc = func(string) (workspace.Workspace, error) { return workspace.Workspace{}, workspace.ErrNotFound }
}

func TestApp_reviewEnv(t *testing.T) {
	t.Run("--env inside a workspace is refused", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		insideWorkspace(e)
		_, err := e.app.reviewEnv("linux-server", "")
		requireCode(t, codeUsage, err)
		assert.Contains(t, err.Error(), "an agent never chooses an environment")
	})
	t.Run("--env with --requester none inside a workspace is allowed", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		insideWorkspace(e)
		env, err := e.app.reviewEnv("linux-server", requesterNone)
		require.NoError(t, err)
		assert.Equal(t, "linux-server", env)
	})
	t.Run("--env outside a workspace is allowed", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		outsideWorkspace(e)
		env, err := e.app.reviewEnv("linux-server", "")
		require.NoError(t, err)
		assert.Equal(t, "linux-server", env)
	})
	t.Run("no --env is local, even inside a workspace", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		insideWorkspace(e)
		env, err := e.app.reviewEnv("", "")
		require.NoError(t, err)
		assert.Empty(t, env)
		assert.Empty(t, e.store.ContainingCalls())
	})
	t.Run("--env local is local", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		outsideWorkspace(e)
		env, err := e.app.reviewEnv(localEnvironment, "")
		require.NoError(t, err)
		assert.Empty(t, env)
	})
	t.Run("an unknown environment is environment_unknown", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		outsideWorkspace(e)
		_, err := e.app.reviewEnv("nowhere", "")
		requireCode(t, codeEnvironmentUnknown, err)
	})
	t.Run("a workspace lookup failure is returned", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		e.app.getwd = func() (string, error) { return "", errors.New("no cwd") }
		_, err := e.app.reviewEnv("linux-server", "")
		require.Error(t, err)
	})
}

func TestApp_reviewEnv_agentWorksLocally(t *testing.T) {
	t.Run("an agent's ws review without --env stays local on the remote", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, `{}`)
		insideWorkspace(e)
		env, err := e.app.reviewEnv("", "")
		require.NoError(t, err)
		assert.Empty(t, env)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
}

const startResult = `{"id":"review-r-42-3f2a9c1-d8efb24f","session":"e8cdf5d8","path":"/w/review","repos":[],"warnings":[],` +
	`"review":{"repo":"github.com/o/r","ref":"` + remoteCommit + `"},"wait_command":"ws review wait --json review-r-42-3f2a9c1-d8efb24f"}`

func okReply(result string) string { return `{"ok":true,"result":` + result + `,"error":null}` }

func TestApp_runRemoteReviewStart(t *testing.T) {
	t.Run("sends review.start with typed args", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(startResult))
		opts := &reviewStartOptions{
			spec: "github.com/o/r", pr: 42, name: "mine", profiles: []string{"go:opus"}, auto: []string{"security"},
			claudeArgs: []string{"--model", "opus", "--permission-mode=plan"}, focus: "the parser",
		}
		require.NoError(t, e.app.runRemoteReviewStart(t.Context(), "linux-server", opts))
		calls := caller.CallEnvironmentCalls()
		require.Len(t, calls, 1)
		assert.Equal(t, envproto.OpReviewStart, calls[0].Req.Op)
		assert.JSONEq(t, `{"repo":"github.com/o/r","pr":42,"head":null,"name":"mine","profiles":["go:opus"],"auto":["security"],`+
			`"model":"opus","permission_mode":"plan","focus":"the parser"}`, string(calls[0].Req.Args))
	})
	t.Run("a commit spec is sent as head", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(startResult))
		require.NoError(t, e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r@" + remoteCommit}))
		var args envproto.ReviewStartArgs
		require.NoError(t, json.Unmarshal(caller.CallEnvironmentCalls()[0].Req.Args, &args))
		assert.Equal(t, "github.com/o/r", args.Repo)
		require.NotNil(t, args.Head)
		assert.Equal(t, remoteCommit, *args.Head)
		assert.Nil(t, args.PR)
		assert.NotNil(t, args.Profiles)
	})
	t.Run("a ref that isn't a full commit is refused", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(startResult))
		for _, spec := range []string{"github.com/o/r@main", "github.com/o/r", "github.com/o/r@abc1234"} {
			err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: spec})
			requireCode(t, codeUsage, err)
		}
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("a ref with --pr is refused", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(startResult))
		err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r@main", pr: 1})
		requireCode(t, codeUsage, err)
	})
	t.Run("a claude flag other than model is refused", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(startResult))
		for _, args := range [][]string{{"--max-turns", "3"}, {"--model"}, {"--model", "opus", "--verbose"}} {
			err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 1, claudeArgs: args})
			requireCode(t, codeUsage, err)
			assert.Contains(t, err.Error(), "only --model and --permission-mode are passed to a remote review")
		}
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("--offline with --env is refused", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(startResult))
		err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 1, offline: true})
		requireCode(t, codeUsage, err)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
	t.Run("a requester other than none is refused", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(startResult))
		err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 1, requester: "fix-login"})
		requireCode(t, codeUsage, err)
	})
	t.Run("the result has environment and an --env wait_command", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(startResult))
		e.app.global.json = true
		require.NoError(t, e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 42}))
		var got map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, "linux-server", got["environment"])
		assert.Equal(t, "ws review wait --json --env linux-server review-r-42-3f2a9c1-d8efb24f", got["wait_command"])
		assert.Equal(t, "review-r-42-3f2a9c1-d8efb24f", got["id"])
	})
	t.Run("text names the environment and the wait command", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(startResult))
		require.NoError(t, e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 42}))
		assert.Equal(t, "review-r-42-3f2a9c1-d8efb24f  e8cdf5d8  /w/review  (on linux-server)\n"+
			"wait for the report: ws review wait --env linux-server review-r-42-3f2a9c1-d8efb24f\n", e.stdout.String())
	})
	t.Run("operation_limited keeps its code and exit 1", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, "")
		caller.CallEnvironmentFunc = func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
			return daemon.CallReply{Reply: json.RawMessage(`{"ok":false,"result":null,"error":{"code":"operation_limited","message":"too many","details":{"retry_after_seconds":60}}}`)}, nil
		}
		err := e.app.runRemoteReviewStart(t.Context(), "linux-server", &reviewStartOptions{spec: "github.com/o/r", pr: 1})
		requireCode(t, codeOperationLimited, err)
		assert.Equal(t, exitFailed, asCLIError(err).exitCode())
		assert.Equal(t, "linux-server", asCLIError(err).details["environment"])
	})
}

func page(section string, offset int, next *int, ids ...string) string {
	comments, superseded := "[]", "[]"
	items := ""
	for i, id := range ids {
		if i > 0 {
			items += ","
		}
		items += fmt.Sprintf(`{"id":%q,"profile":"go","priority":5,"category":"x","body":"b","code":null,"anchors":[],"suggestions":[],"decision":null}`, id)
	}
	if section == envproto.SectionComments {
		comments = "[" + items + "]"
	} else {
		superseded = "[" + items + "]"
	}
	nextText := "null"
	if next != nil {
		nextText = fmt.Sprint(*next)
	}
	return fmt.Sprintf(`{"repo":"github.com/o/r","ref":%q,"review":null,"agent":null,"profiles":[],"comments":%s,"superseded":%s,"warnings":[],`+
		`"section":%q,"offset":%d,"next_offset":%s,"total":9,"redacted":1}`, remoteCommit, comments, superseded, section, offset, nextText)
}

func intp(n int) *int { return &n }

// pagedCaller answers review.report from pages, keyed by "<section>:<offset>".
func pagedCaller(pages map[string]string) *environmentCallerMock {
	return &environmentCallerMock{CallEnvironmentFunc: func(_ context.Context, _ string, req daemon.CallRequest) (daemon.CallReply, error) {
		var args envproto.ReviewReportArgs
		if err := json.Unmarshal(req.Args, &args); err != nil {
			return daemon.CallReply{}, err
		}
		body, ok := pages[fmt.Sprintf("%s:%d", args.Section, args.Offset)]
		if !ok {
			return daemon.CallReply{}, &daemon.RemoteError{Code: string(codeInternal), Message: "no such page"}
		}
		return daemon.CallReply{Reply: json.RawMessage(okReply(body))}, nil
	}}
}

func TestApp_remoteReport(t *testing.T) {
	t.Run("joins pages in order", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		useCaller(e.app, pagedCaller(map[string]string{
			"comments:0":   page("comments", 0, intp(2), "go:a", "go:b"),
			"comments:2":   page("comments", 2, nil, "go:c"),
			"superseded:0": page("superseded", 0, intp(1), "#1:x"),
			"superseded:1": page("superseded", 1, nil, "#1:y"),
		}))
		report, err := e.app.remoteReport(t.Context(), "linux-server", "review-r-42")
		require.NoError(t, err)
		var comments, superseded []string
		for _, c := range report.Comments {
			comments = append(comments, c.ID)
		}
		for _, c := range report.Superseded {
			superseded = append(superseded, c.ID)
		}
		assert.Equal(t, []string{"go:a", "go:b", "go:c"}, comments)
		assert.Equal(t, []string{"#1:x", "#1:y"}, superseded)
		assert.Equal(t, remoteCommit, report.Ref)
		assert.Equal(t, 4, report.Redacted)
	})
	t.Run("stops when next_offset is null", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		caller := pagedCaller(map[string]string{
			"comments:0":   page("comments", 0, nil, "go:a"),
			"superseded:0": page("superseded", 0, nil),
		})
		useCaller(e.app, caller)
		_, err := e.app.remoteReport(t.Context(), "linux-server", "review-r-42")
		require.NoError(t, err)
		assert.Len(t, caller.CallEnvironmentCalls(), 2)
	})
	t.Run("a next_offset that doesn't advance is an error", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, "")
		useCaller(e.app, pagedCaller(map[string]string{"comments:0": page("comments", 0, intp(0), "go:a")}))
		_, err := e.app.remoteReport(t.Context(), "linux-server", "review-r-42")
		requireCode(t, codeInternal, err)
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
}

func TestApp_printRemoteReport(t *testing.T) {
	report := &remoteReportJSON{Redacted: 2}
	report.Repo, report.Ref = "github.com/o/r", remoteCommit
	report.Comments = []reviewCommentJSON{{ID: "go:bug-1", Profile: "go", Priority: 5, Category: "x", Body: "text", Anchors: []reviewAnchorJSON{{File: "a.go", Line: 3}}, Suggestions: []reviewSuggestionJSON{}}}
	report.Superseded = []reviewCommentJSON{}
	report.Profiles = []reviewProfileJSON{{Name: "go", Verdict: new("approve"), Severity: &severityJSON{}}, {Name: "sec", Missing: true}}
	t.Run("json has environment and redacted", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		e.app.global.json = true
		require.NoError(t, e.app.printRemoteReport(report, "linux-server", formatJSON, ""))
		var got map[string]any
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		assert.Equal(t, "linux-server", got["environment"])
		assert.EqualValues(t, 2, got["redacted"])
		assert.Len(t, got["comments"], 1)
	})
	t.Run("markdown renders the findings and the missing profiles", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		require.NoError(t, e.app.printRemoteReport(report, "linux-server", formatMarkdown, ""))
		assert.Contains(t, e.stdout.String(), "go:bug-1")
		assert.Contains(t, e.stdout.String(), "text")
		assert.Contains(t, e.stdout.String(), "**Missing** sec")
	})
	t.Run("html renders the findings", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		require.NoError(t, e.app.printRemoteReport(report, "linux-server", formatHTML, ""))
		assert.Contains(t, e.stdout.String(), "<html")
		assert.Contains(t, e.stdout.String(), "go:bug-1")
	})
	t.Run("decided keeps only the findings decided that way", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		e.app.global.json = true
		two := &remoteReportJSON{}
		two.Comments = []reviewCommentJSON{
			{ID: "go:a", Decision: decisionOf("skip")},
			{ID: "go:b", Decision: decisionOf("fix")},
			{ID: "go:c"},
		}
		require.NoError(t, e.app.printRemoteReport(two, "linux-server", formatJSON, "fix"))
		var got struct {
			Comments []reviewCommentJSON `json:"comments"`
		}
		require.NoError(t, json.Unmarshal(e.stdout.Bytes(), &got))
		require.Len(t, got.Comments, 1)
		assert.Equal(t, "go:b", got.Comments[0].ID)
	})
}

func statusReply(status string, finished bool) string {
	agent := "null"
	if status != "" {
		agent = fmt.Sprintf(`{"workspace":"review-r-42","status":%q}`, status)
	}
	return okReply(fmt.Sprintf(`{"review":"review-r-42","agent":%s,"finished":%t,"redacted":0}`, agent, finished))
}

func TestApp_waitRemoteReview(t *testing.T) {
	reportPages := map[string]string{
		"comments:0":   page("comments", 0, nil, "go:a"),
		"superseded:0": page("superseded", 0, nil),
	}
	t.Run("polls status until finished", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, `{}`)
		e.app.reviewPoll = time.Millisecond
		e.app.global.json = true
		statuses := []string{statusReply("working", false), statusReply("working", false), statusReply("idle", true)}
		reports := pagedCaller(reportPages)
		caller := &environmentCallerMock{CallEnvironmentFunc: func(ctx context.Context, name string, req daemon.CallRequest) (daemon.CallReply, error) {
			if req.Op == envproto.OpReviewReport {
				return reports.CallEnvironment(ctx, name, req)
			}
			reply := statuses[0]
			statuses = statuses[1:]
			return daemon.CallReply{Reply: json.RawMessage(reply)}, nil
		}}
		useCaller(e.app, caller)
		require.NoError(t, e.app.waitRemoteReview(t.Context(), "linux-server", "review-r-42", time.Minute))
		var statusCalls int
		for _, c := range caller.CallEnvironmentCalls() {
			if c.Req.Op == envproto.OpReviewStatus {
				statusCalls++
				assert.JSONEq(t, `{"review":"review-r-42"}`, string(c.Req.Args))
			}
		}
		assert.Equal(t, 3, statusCalls)
		assert.Contains(t, e.stdout.String(), `"go:a"`)
		assert.Contains(t, e.stdout.String(), `"environment":"linux-server"`)
	})
	t.Run("times out with review_timeout", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, statusReply("working", false))
		e.app.reviewPoll = time.Millisecond
		err := e.app.waitRemoteReview(t.Context(), "linux-server", "review-r-42", 20*time.Millisecond)
		requireCode(t, codeReviewTimeout, err)
		assert.Equal(t, "review-r-42", asCLIError(err).details["workspace"])
		assert.EqualValues(t, "working", asCLIError(err).details["status"])
	})
	t.Run("offline is environment_offline", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, "")
		e.app.reviewPoll = time.Millisecond
		caller.CallEnvironmentFunc = func(context.Context, string, daemon.CallRequest) (daemon.CallReply, error) {
			return daemon.CallReply{}, &daemon.RemoteError{Code: string(codeEnvironmentOffline), Message: "offline"}
		}
		err := e.app.waitRemoteReview(t.Context(), "linux-server", "review-r-42", time.Minute)
		requireCode(t, codeEnvironmentOffline, err)
	})
	t.Run("a non-positive timeout is usage", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, statusReply("idle", true))
		err := e.app.waitRemoteReview(t.Context(), "linux-server", "review-r-42", 0)
		requireCode(t, codeUsage, err)
	})
	t.Run("a cancelled context stops the wait", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, statusReply("working", false))
		e.app.reviewPoll = time.Hour
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		err := e.app.waitRemoteReview(ctx, "linux-server", "review-r-42", time.Hour)
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestApp_runRemoteReviewList(t *testing.T) {
	const listResult = `{"reviews":[{"id":"review-r-42","review":{"version":1,"repo":"github.com/o/r","ref":"` + remoteCommit +
		`","requester":"","started_at":"2026-06-01T00:00:00Z","profiles":[],"pr":{"number":42}},"agent":{"workspace":"review-r-42","status":"idle"}}],"warnings":[],"truncated":false,"redacted":0}`
	t.Run("sends review.list with the filters", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(listResult))
		require.NoError(t, e.app.runRemoteReviewList(t.Context(), "linux-server", &reviewListOptions{repo: "github.com/o/r", pr: 42}))
		assert.JSONEq(t, `{"repo":"github.com/o/r","pr":42,"limit":0}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
		assert.Contains(t, e.stdout.String(), "review-r-42")
	})
	t.Run("json adds environment", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(listResult))
		e.app.global.json = true
		require.NoError(t, e.app.runRemoteReviewList(t.Context(), "linux-server", &reviewListOptions{}))
		assert.Contains(t, e.stdout.String(), `"environment":"linux-server"`)
	})
	t.Run("a bad --repo is usage", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(listResult))
		requireCode(t, codeUsage, e.app.runRemoteReviewList(t.Context(), "linux-server", &reviewListOptions{repo: "nope"}))
	})
}

func TestApp_runRemoteReviewDecide(t *testing.T) {
	const decided = `{"repo":"github.com/o/r","ref":"` + remoteCommit + `","decisions":{"go:a":{"decision":"fix","note":"","at":"2026-06-01T00:00:00Z","suggestion":1}},"warnings":[],"redacted":0}`
	t.Run("maps the flags to typed args", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(decided))
		opts := &decideOptions{fix: true, suggestion: 1, hasSuggestion: true, note: "n", hasNote: true}
		require.NoError(t, e.app.runRemoteReviewDecide(t.Context(), "linux-server", []string{"review-r-42", "go:a"}, &reviewSubjectOptions{}, opts))
		assert.Equal(t, envproto.OpReviewDecide, caller.CallEnvironmentCalls()[0].Req.Op)
		assert.JSONEq(t, `{"review":"review-r-42","findings":["go:a"],"decision":"fix","suggestion":1,"note":"n"}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
		assert.Equal(t, "go:a: fix with suggestion 1\n", e.stdout.String())
	})
	t.Run("clear is the clear decision", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(`{"repo":"r","ref":"x","decisions":{"go:a":null},"warnings":[]}`))
		require.NoError(t, e.app.runRemoteReviewDecide(t.Context(), "linux-server", []string{"review-r-42", "go:a"}, &reviewSubjectOptions{}, &decideOptions{clear: true}))
		assert.JSONEq(t, `{"review":"review-r-42","findings":["go:a"],"decision":"clear","suggestion":null,"note":null}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
		assert.Equal(t, "go:a: undecided\n", e.stdout.String())
	})
	t.Run("--repo and --ref are usage", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(decided))
		err := e.app.runRemoteReviewDecide(t.Context(), "linux-server", []string{"go:a"}, &reviewSubjectOptions{repo: "github.com/o/r", ref: remoteCommit}, &decideOptions{skip: true})
		requireCode(t, codeUsage, err)
		assert.Contains(t, err.Error(), "use the review id on an environment")
		assert.Empty(t, caller.CallEnvironmentCalls())
	})
}

func TestApp_runRemoteReviewActed(t *testing.T) {
	const acted = `{"repo":"r","ref":"x","decisions":{"go:a":{"decision":"comment","note":"","at":"2026-06-01T00:00:00Z","done":{"commented":"https://x/1","fix_sent":"","at":"2026-06-01T00:00:00Z"}}},"warnings":[]}`
	t.Run("maps commented", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(acted))
		require.NoError(t, e.app.runRemoteReviewActed(t.Context(), "linux-server", []string{"review-r-42", "go:a"}, &reviewSubjectOptions{}, &actedOptions{commented: "https://x/1"}))
		assert.Equal(t, envproto.OpReviewActed, caller.CallEnvironmentCalls()[0].Req.Op)
		assert.JSONEq(t, `{"review":"review-r-42","findings":["go:a"],"commented":"https://x/1","fix_sent":null}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
		assert.Equal(t, "go:a: comment, commented at https://x/1\n", e.stdout.String())
	})
	t.Run("maps fix-sent", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(acted))
		require.NoError(t, e.app.runRemoteReviewActed(t.Context(), "linux-server", []string{"review-r-42", "go:a"}, &reviewSubjectOptions{}, &actedOptions{fixSent: "fixer"}))
		assert.JSONEq(t, `{"review":"review-r-42","findings":["go:a"],"commented":null,"fix_sent":"fixer"}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
	})
	t.Run("--repo is usage", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(acted))
		err := e.app.runRemoteReviewActed(t.Context(), "linux-server", []string{"go:a"}, &reviewSubjectOptions{repo: "github.com/o/r", ref: remoteCommit}, &actedOptions{commented: "https://x/1"})
		requireCode(t, codeUsage, err)
	})
}

func TestApp_runRemoteReviewProfiles(t *testing.T) {
	const profiles = `{"profiles":[{"name":"go","description":"Go","when":"auto","match":["*.go"],"model":"","builtin":true,"source":"builtin"}],"warnings":[],"redacted":0}`
	t.Run("prints the table", func(t *testing.T) {
		e, caller, _ := newEnvOpApp(t, okReply(profiles))
		require.NoError(t, e.app.runRemoteReviewProfiles(t.Context(), "linux-server"))
		assert.Equal(t, envproto.OpReviewProfiles, caller.CallEnvironmentCalls()[0].Req.Op)
		assert.JSONEq(t, `{}`, string(caller.CallEnvironmentCalls()[0].Req.Args))
		assert.Contains(t, e.stdout.String(), "NAME")
		assert.Contains(t, e.stdout.String(), "*.go")
	})
	t.Run("json adds environment", func(t *testing.T) {
		e, _, _ := newEnvOpApp(t, okReply(profiles))
		e.app.global.json = true
		require.NoError(t, e.app.runRemoteReviewProfiles(t.Context(), "linux-server"))
		assert.Contains(t, e.stdout.String(), `"environment":"linux-server"`)
	})
}

func TestApp_reviewCommands_env(t *testing.T) {
	for _, name := range []string{"start", "wait", "report", "list", "decide", "acted", "profiles"} {
		t.Run(name+" has --env", func(t *testing.T) {
			app, _, _ := newTestApp()
			cmd, _, err := app.newReviewCmd().Find([]string{name})
			require.NoError(t, err)
			assert.NotNil(t, cmd.Flags().Lookup("env"))
		})
	}
}

func decisionOf(decision string) *store.Decision { return &store.Decision{Decision: decision} }
